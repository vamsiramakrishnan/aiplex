# Quota administration and reconciliation

Quota administration is mounted only with the opt-in managed QuotaPlex runtime.
It uses the same verified tenant, canonical principal/actor identity, current
scope resolver and shared durable quota store as execution. No request body can
select the tenant or identify its own administrator. A control-plane editor role
alone is not a quota action grant.

| Scope | Operations |
|---|---|
| `aiplex:quota:read` | Policy, bounded inventories, balances, loans, original terminal evidence and reconciliation inspection |
| `aiplex:quota:simulate` | Admission preview and policy validation/export |
| `aiplex:quota:loans` | Policy-authorized loan grant and unused-loan return |
| `aiplex:quota:reconcile:plan` | Propose terminal reconciliation against the exact stored attempt |
| `aiplex:quota:reconcile:approve` | Apply the exact reviewed reconciliation digest |

Grant these through existing identity/role mechanisms. Current grants are resolved
again on each operation; a stale ingress token's application scopes cannot replace
that check. Normal authenticated control-plane role checks still apply at the API
mount. A read grant covers the verified tenant inventory, not merely one user's
attempts, and therefore exposes subject/group/account metadata to that operator.

## Inspection and dry runs

| API | Meaning |
|---|---|
| `GET /api/v1/quota/policy` | Configured policy, its reference, active head and as-of timestamp |
| `POST /api/v1/quota/policy/validate` | `{policy, expected_digest}` → validated policy export |
| `POST /api/v1/quota/preview` | Full typed `Demand` → explanatory admission decision and plan |
| `GET /api/v1/quota/inventory?kind=...&limit=20&after=...` | Bounded administration inventory |
| `GET /api/v1/quota/balances/{key}` | Committed accounting balance with its limit-policy provenance |
| `GET /api/v1/quota/loans/{id}` | Original loan terms, windows, returned/outstanding quantities |
| `GET /api/v1/quota/evidence/{event}` | Original immutable terminal producer facts and acknowledgement/resolution state |

Inventory kinds are `budgets`, `loans`, `attempts`, `reconciliations`, `audit` and
`terminal-work`. Limits range from 1 to 64. Continue strictly after
`next_after_id`; periodically begin another scan because separate pages are
separate snapshots. A balance's stored limit is its last committed limit
snapshot, not an implicit recomputation under a later policy.

Preview executes the actual admission algorithm against a private mutation
overlay on one read snapshot. It persists no attempt, denial, rate debit, balance
hold or concurrency permit. `mutates` is always false. A returned allowed decision
never authorizes dispatch or promises later availability. It previews the core
budget/rate/concurrency engine; it does not enqueue work or promise scheduler
selection, protected-pool capacity, provider availability or latency. Supplied preview
memberships are hypothetical operator inputs; they are not authenticated facts
usable by the native invocation path.

Policy validation executes the actual policy activation checks against a read-only
snapshot, including expected-head, immutable pricing and structural lineage
constraints. Its result is explicitly `state: "validated-export"` and
`deployment_required: true`. It does not publish an active runtime policy or
replace host bindings. Deploy the reviewed policy, bounds, membership authority
and binding configuration together using the existing configuration-generation
checks; old replicas must not silently continue under replaced bindings.

## Audited allocation loans

`POST /api/v1/quota/loans` accepts `id`, `rule_id`, `amount` and `expires_at`.
`POST /api/v1/quota/loans/{id}/return` returns only currently unused borrower
capacity. Both require one stable `Idempotency-Key`.

Sharing rules still determine eligible account-budget pairs, compatible meters
and target selectors, protected floors, maximum amount/duration and original
windows. Administration cannot manufacture an unsupported transfer or reclaim
capacity committed to uncertain provider work. Audit and loan mutations commit
in one transaction. Replaying one administration key returns the original result;
a later maintenance pass that should return newly freed capacity uses a new key.
These APIs are explicit allocation operations, not an automatic lending allocator.

## Reviewed terminal reconciliation

Use reconciliation only after investigating provider work and obtaining complete
terminal usage and exact model identity. It is an explicit authorized operator
attestation, not a native provider lookup: the service does not fetch or verify
the external `evidence_ref` itself. Keep the referenced provider response/support
case in a governed evidence system and review it before approving.

1. A planner calls `POST /api/v1/quota/reconciliations` with a stable
   `Idempotency-Key` and the proposal below.
2. The service persists an immutable plan binding the proposal to the complete
   attempt-state digest, original terminal-evidence digest, original dispatch
   claim, model proof, planner and expiry. Planning changes no holds or permits.
3. The reviewer reads `GET /api/v1/quota/reconciliations/{id}` and the original
   evidence. A distinct principal is required by default. Only operator-owned
   `QuotaAdminOptions.AllowSelfApproval` can relax that separation; it is never a
   request-body option.
4. The reviewer calls `POST .../{id}/apply` with `{ "digest": "<reviewed>" }`
   and its own stable approval `Idempotency-Key`.
5. One transaction verifies unchanged reviewed state, settles through the existing
   engine at the original prices, appends approval/audit receipts and resolves
   original pending work. A lost response can be retried without double charging
   or returning another hold.

Example proposal, with deployment-specific IDs and genuinely reviewed values:

```json
{
  "id": "investigation-42",
  "attempt_id": "opaque-attempt-id",
  "reason": "Provider correction confirms complete terminal usage",
  "evidence_ref": "evidence-system/provider-case-42",
  "usage": { "requests": 1, "uncached_input_tokens": 120,
             "cached_tokens": 0, "output_tokens": 36, "thought_tokens": 8 },
  "model_version": "exact-admission-pinned-version",
  "provider_resource_id": "original-provider-resource-if-pinned",
  "expires_at": "2026-11-09T05:30:00Z"
}
```

Expiry must be within one hour when planning and approval must occur before it.
Usage must contain every admitted raw meter and no computed money or unknown
meter. Required model proof must exactly match the admitted version; the API
cannot price a different actual model at the original model's rates. The provider
resource must match any pinned native resource. A changed attempt, new canonical
terminal evidence, altered plan digest, expired plan or duplicate competing
settlement invalidates approval.

Original terminal evidence and its canonical producer index are never rewritten.
Its acknowledgement gains `resolution_id` linking the approved plan/receipt;
recovery no longer retries superseded work. An existing settlement cannot be
repriced or refunded through this operation. A binding quarantine is not cleared
by correcting one attempt: establish a new verified binding/policy generation
through deployment. Arbitrary balance edits, active-work cancellation, provider
writes, historical repricing and implicit currency conversion are not supported.

## SDK and CLI

The Go SDK provides `GetQuotaPolicy`, `ValidateQuotaPolicy`, `PreviewQuota`,
`GetQuotaInventory`, `GetQuotaBalance`, `GetQuotaLoan`, `GrantQuotaLoan`,
`ReturnQuotaLoan`, `GetQuotaEvidence`, `PlanQuotaReconciliation`,
`GetQuotaReconciliation` and `ApplyQuotaReconciliation`.

```sh
aiplex quota policy
aiplex quota inventory terminal-work --limit 20
aiplex quota balance <budget-key>
aiplex quota policy-validate policy.json --expected-digest <active-digest>
aiplex quota preview demand.json
aiplex quota loan-grant loan.json --idempotency-key allocation-42
aiplex quota loan-return loan-42 --idempotency-key maintenance-43
aiplex quota reconcile-plan proposal.json --idempotency-key investigation-42
aiplex quota reconcile investigation-42
aiplex quota evidence <original-event>
aiplex quota reconcile-apply investigation-42 --digest <reviewed-digest> \
  --idempotency-key approval-42
```

The CLI prints JSON by default and supports its existing YAML output setting.
JSON proposal files are bounded and decoded strictly. SDK payloads never forward a
caller's tenant field as administration authority; tenancy comes from the server's
verified identity binding.

# Quota administration and reconciliation

Quota administration is mounted only with the opt-in managed QuotaPlex runtime.
It uses the same verified tenant, canonical principal/actor identity, current
scope resolver and shared durable quota store as execution. No request body can
select the tenant or identify its own administrator. A control-plane editor role
alone is not a quota action grant.

| Scope | Operations |
|---|---|
| `aiplex:quota:read` | Policy, bounded inventories, balances, loans, original terminal evidence and reconciliation inspection |
| `aiplex:quota:simulate` | Admission preview and policy validation (the active policy document itself is read with `aiplex:quota:read` via `GET /api/v1/quota/policy`; there is no separate export route) |
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
| `GET /api/v1/quota/inventory?kind=attempts&older_than=1h` | Attempts in the `unknown` state dispatched at least that long ago, with a count, oldest dispatch time and exposure summary ([details](#attempts-in-the-unknown-state)) |
| `GET /api/v1/quota/balances/{key}` | Committed accounting balance with its limit-policy provenance |
| `GET /api/v1/quota/loans/{id}` | Original loan terms, windows, returned/outstanding quantities |
| `GET /api/v1/quota/evidence/{event}` | Original immutable terminal producer facts and acknowledgement/resolution state |
| `GET /api/v1/quota/attempts/{id}` | One admission attempt. Readable by the caller that made it (as the principal, or as the delegated actor), or by a caller with `aiplex:quota:read`. Any other caller gets `404 NOT_FOUND`. |

An attempt read needs a verified tenant and identity. Without a verified tenant the route
answers `403 TENANT_REQUIRED`. Without a verified identity it answers `401 IDENTITY_REQUIRED`. When
no quota engine is configured for the tenant it answers `404 NOT_FOUND`. A missing attempt answers
`404 QUOTA_ATTEMPT_UNAVAILABLE`. A store failure answers `503 QUOTA_ATTEMPT_UNAVAILABLE`.
The role alone does not grant the read.

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

## Attempts in the unknown state

An attempt enters the `unknown` state when it was dispatched and QuotaPlex never
learned how it ended (a lost upstream response, a stream cut before final
usage, a batch whose results are pending). It keeps its budget holds and
concurrency permits until terminal evidence or a reviewed reconciliation
settles it. Nothing resolves it by age.

**What is reported.** For each tenant, a summary gives:

- `attempts`: how many attempts are in the `unknown` state;
- `oldest_dispatch_at`: the earliest dispatch time among them, and so the age
  of the oldest one;
- `exposure`: per meter, the attempts' admitted bounds (the most each may
  have consumed, which their budget holds reserve). Each entry names the
  meter, its unit and, for money, its currency and scale. Meters are never
  converted or added into one cost.

Unknown stays unknown:

- When an amount cannot be computed, the entry's `quantity` is `null`,
  `uncomputed_attempts` says how many attempts it is missing, and `known_part`
  is only a lower bound. This happens for a cost that was admitted unpriced
  under `on_unknown_cost: allow_and_flag` (even when the plan also holds a money
  bound, which then counts only toward `known_part`), a bound on a meter the plan does not
  declare, a plan without bounds, and a sum that would overflow (`overflow`).
  A meter or unit the plan does not identify is shown as `meter_id: ""` /
  `unit: ""` ("unknown meter").
- `undated_attempts` counts unknown attempts without a valid dispatch time;
  their age is unknown, so the oldest age is unknown too.
- `unreadable` counts attempt records that could not be decoded or validated.
  While it is nonzero, the count and exposure may be understated.

**Asking for it.** `GET /api/v1/quota/inventory?kind=attempts&older_than=1h`
(scope `aiplex:quota:read`) returns, from the scanned page, the attempts in
the `unknown` state dispatched at least that long ago, plus that page's
summary in `unknown`. `older_than=0s` selects all of them. An undated unknown
attempt is always included, since it cannot be shown to be younger. An
unreadable record is returned and counted, not skipped. Paging is over scanned
attempt records, so a page can hold no match and still not be `done`; continue
with `next_after_id` and merge the summaries. `older_than` must be one Go
duration that is not negative, and only `kind=attempts` accepts it; anything
else answers `400 QUOTA_INVALID_REQUEST`, as a malformed `limit` does.

```sh
aiplex quota unknown --older-than 1h          # walks every page and sums them
aiplex quota unknown --older-than 1h -o json  # oldest_age_seconds is null when unknown; -o yaml has the same keys
aiplex quota inventory attempts --older-than 1h --limit 64   # one raw page
```

`aiplex quota unknown` prints the count, the oldest age and dispatch time, and
one line per meter, for example `usd: unknown (known part 150 USD minor units
(scale 6); 1 attempt(s) not computable)`. It stops after 1024 pages (65,536
attempt records) and then says the figures are lower bounds. A malformed or
negative `--older-than` exits with the usage code 2, as does a `400` from the
server.

**The recovery worker.** The API's quota worker scans every tenant's attempts
in pages of 32, one page per recovery interval. When a scan completes, its
summary becomes that tenant's current figures, exported as the
`aiplex.quota.unknown.*` metrics (see [observability](observability.md#quotaplex-unknown-attempts)),
and the worker logs **one warning** for the tenant when the oldest unknown
attempt is older than `unknown_warn_after`, or when its age is unknown:

```text
quota attempts in the unknown state are older than the warning threshold and still hold budget
  tenant=... attempts=2 oldest_age=3h0m0s threshold=1h0m0s exposure_complete=false
  exposure=["tokens: 30 tokens", "usd: unknown (...)"]
```

A separate warning says when attempt records could not be read. A scan with a
failed page is not published; the previous figures stay. Every failed page logs
`quota unknown-attempt scan page failed` with `last_complete_scan` (how old the
kept figures are, or `never`), at most once per tenant and pass, so a store
that keeps failing is not silent; the `aiplex.quota.unknown.scan_age` gauge
grows meanwhile. A full scan takes about (attempt records ÷ 32) recovery
intervals, so the figures and the warning lag by that much; the inventory
query reads the store directly.

`unknown_warn_after` is set in the quota configuration file next to
`recovery_interval`, as a Go duration between `1m` and `720h`. The default is
`1h`.

What the warning means: provider work may have run and not been charged yet,
and the budget it holds is unavailable to other requests. Investigate the
attempt (`aiplex quota attempt <id>`), and settle it from complete provider
evidence through [reviewed reconciliation](#reviewed-terminal-reconciliation).

**Limits.** This reports and warns; it does not page anyone, deliver
notifications, or resolve, abort or expire attempts. Only the `unknown` state
is counted: an attempt left in `dispatching` (for example, a replica stopped
mid-request before it recorded the outcome) is not included. The metrics and
the warning come from the recovery scan described above; they are per process,
and every replica scans and reports the same shared store. This has not been
run against a live provider or a production store.

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
recovery does not retry superseded work. An existing settlement cannot be
repriced or refunded through this operation. A binding quarantine is not cleared
by correcting one attempt: establish a new verified binding/policy generation
through deployment. Arbitrary balance edits, active-work cancellation, provider
writes, historical repricing and implicit currency conversion are not supported.

## SDK and CLI

The Go SDK provides `GetQuotaPolicy`, `ValidateQuotaPolicy`, `PreviewQuota`,
`GetQuotaInventory`, `GetQuotaUnknownAttempts`, `GetQuotaBalance`, `GetQuotaLoan`, `GrantQuotaLoan`,
`ReturnQuotaLoan`, `GetQuotaEvidence`, `PlanQuotaReconciliation`,
`GetQuotaReconciliation` and `ApplyQuotaReconciliation`.

```sh
aiplex quota policy
aiplex quota inventory terminal-work --limit 20
aiplex quota unknown --older-than 1h
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

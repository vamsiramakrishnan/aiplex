---
title: Quota Administration API
---

The quota administration API operates the tenant's QuotaPlex engine. It reads the active policy, simulates a policy or an admission without changing state, reads budget balances and terminal evidence, grants and returns loans, and runs the two-step reconciliation of a settled attempt. Every route is tenant-scoped, and the tenant comes from the verified binding. Each route needs a specific action grant, `aiplex:quota:{action}`, on the caller's token. Mutations need an `Idempotency-Key` header. The attempt route at `/api/v1/quota/attempts/{id}` is the one exception: it has no action grant and is limited to the attempt's own caller.

## Routes

| Method | Path | Action grant | Purpose |
|---|---|---|---|
| GET | `/api/v1/quota/policy` | `read` | Active policy and its reference |
| POST | `/api/v1/quota/policy/validate` | `simulate` | Validate a candidate policy against the active digest. Changes nothing. |
| POST | `/api/v1/quota/preview` | `simulate` | Preview the admission of one demand. Changes nothing. |
| GET | `/api/v1/quota/inventory` | `read` | Page through the administration records of one kind |
| GET | `/api/v1/quota/balances/{id}` | `read` | Balance of one budget key |
| POST | `/api/v1/quota/loans` | `loans` | Grant a loan from a sharing rule |
| GET | `/api/v1/quota/loans/{id}` | `read` | Read one loan |
| POST | `/api/v1/quota/loans/{id}/return` | `loans` | Return the unused part of a loan |
| GET | `/api/v1/quota/evidence/{id}` | `read` | Terminal evidence and its usage acknowledgement for one event |
| POST | `/api/v1/quota/reconciliations` | `reconcile:plan` | Plan a reconciliation of a settled attempt |
| GET | `/api/v1/quota/reconciliations/{id}` | `read` | Read one reconciliation plan and, once applied, its receipt |
| POST | `/api/v1/quota/reconciliations/{id}/apply` | `reconcile:approve` | Approve and apply a reconciliation plan |
| GET | `/api/v1/quota/attempts/{id}` | None. Admin, or the attempt's own principal and actor. | Read one quota attempt |

The routes are under `/api/v1`, and every route requires a verified identity. Outside development mode, the router requires the `admin` or `deployer` role for the non-GET routes. The handler then checks the action grant at the moment of the request, so a grant that has been revoked since the token was issued is refused.

Errors use this shape:

```json
{
  "code": "QUOTA_INVALID_REQUEST",
  "message": "quota operation could not be completed",
  "request_id": "req-example"
}
```

## Common checks

Every route in the table, except the attempt route, runs these checks in order:

1. The quota host must be configured. Otherwise `503 QUOTA_UNAVAILABLE`.
2. The tenant must be bound. Otherwise `403 TENANT_REQUIRED`.
3. A verified identity must be present. Otherwise `401 IDENTITY_REQUIRED`.
4. The action grant `aiplex:quota:{action}` must be current. Otherwise `403 QUOTA_ACTION_DENIED`. A wildcard grant such as `aiplex:quota:*` satisfies every action.
5. The tenant must have a quota engine. Otherwise `404 NOT_FOUND`.
6. Mutations must send exactly one `Idempotency-Key` header. It must not be blank, must be at most 256 characters, and must not contain CR, LF or NUL. Otherwise `400 IDEMPOTENCY_REQUIRED`. The value identifies the operation for replay.

Request bodies are read up to 1 MiB. A body must be one JSON object. Duplicate or ambiguous JSON field names are refused. Unknown fields are refused. Any failure is a `400 QUOTA_INVALID_REQUEST`, except where another code is listed.

### Error mapping

Domain errors from the engine are mapped as follows:

| Status | Code | Engine condition |
|---|---|---|
| 400 | `QUOTA_INVALID_REQUEST` | Invalid input, or any error not listed below |
| 403 | `QUOTA_OPERATION_DENIED` | The operation is not allowed in the current state. For example, a plan approved by its own requester while independent approval is required, or a plan past its expiry. |
| 404 | `QUOTA_NOT_FOUND` | The referenced record does not exist |
| 409 | `QUOTA_STATE_CONFLICT` | The record is in a conflicting state, or the operation is an invalid transition |
| 413 | `QUOTA_OPERATION_BOUND_EXCEEDED` | The operation exceeds a transaction bound |

## GET `/api/v1/quota/policy`

Returns the active policy.

| Field | Type | Meaning |
|---|---|---|
| `policy` | object | The active policy: `version`, `tenant_id`, `id`, `revision`, `effective_at`, `expires_at` (omitted when empty), `meters`, `accounts`, and the optional `price_books`, `budgets`, `rates`, `concurrency` and `sharing` lists |
| `reference` | object | `id`, `revision` and `digest` of the policy |
| `active` | object | The same reference, for the active policy |
| `as_of` | RFC 3339 timestamp | Time of the read |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Inspection returned |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 404 | `QUOTA_NOT_FOUND` | No policy |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/quota/policy" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## POST `/api/v1/quota/policy/validate`

Validates a candidate policy against the digest of the active policy. Nothing is stored.

### Request body

| Field | Type | Required | Meaning |
|---|---|---|---|
| `policy` | object | Yes | The candidate policy. Its `tenant_id` must equal the verified tenant. |
| `expected_digest` | string | Yes | The digest of the policy the candidate is based on |

### Response

The response is the validation artifact: `version`, `policy`, `reference`, `expected_digest`, `state` and `deployment_required`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Artifact returned |
| 400 | `QUOTA_INVALID_REQUEST` | The body is invalid, or the candidate does not compile |
| 403 | `TENANT_MISMATCH` | The candidate's `tenant_id` is not the verified tenant |
| 403 | `QUOTA_ACTION_DENIED` | The `simulate` grant is missing |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/quota/policy/validate" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"policy":{"version":"0.3.0","tenant_id":"tenant-example","id":"policy-example","revision":2},"expected_digest":"digest-example"}'
```

## POST `/api/v1/quota/preview`

Previews the admission of one demand. The response shows the plan and the decision that the engine would make. Nothing is reserved.

### Request body

The body is a `Demand`: `version`, `context` (the request context, whose `tenant_id` must equal the verified tenant), `principal`, `actor` (optional), `logical_operation_id`, `attempt_id`, `payload_digest`, `target`, `funding_account_id` (optional), `bounds` (the trusted maximum exposure as a map of counter names to integers), and optionally `require_model_version`.

### Response

| Field | Type | Meaning |
|---|---|---|
| `plan` | object | The plan the engine would use |
| `decision` | object | `tenant_id`, `attempt_id`, `allowed`, `state`, `reason`, `policy` (a reference), `payer_id` (omitted when empty), `retry_after` (omitted when zero), `overrun` (omitted unless `true`) |
| `as_of` | RFC 3339 timestamp | Time of the simulation |
| `mutates` | boolean | Always `false` for this route |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Preview returned |
| 400 | `QUOTA_INVALID_REQUEST` | The body is invalid |
| 403 | `TENANT_MISMATCH` | The demand's `context.tenant_id` is not the verified tenant |
| 403 | `QUOTA_ACTION_DENIED` | The `simulate` grant is missing |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/quota/preview" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"version":"0.3.0","context":{"tenant_id":"tenant-example"}}'
```

## GET `/api/v1/quota/inventory`

Pages through the administration records of one kind.

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `kind` | string | No | The record kind to list |
| `after` | string | No | Continue after this record identifier |
| `limit` | integer | No | Page size. Default 20. A value that is not an integer returns `400 QUOTA_INVALID_REQUEST`. The engine enforces its own bounds. |

### Response

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | Record kind |
| `records` | array of record | Records on this page |
| `next_after_id` | string | Omitted on the last page. Pass it as `after`. |
| `done` | boolean | `true` when no further records exist |
| `as_of` | RFC 3339 timestamp | Time of the read |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Page returned |
| 400 | `QUOTA_INVALID_REQUEST` | `limit` is not an integer, or the engine rejects the query |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/quota/inventory?kind=loan&limit=10" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/quota/balances/{id}`

Returns the balance of one budget key. The `id` is the budget key.

| Field | Type | Meaning |
|---|---|---|
| `key` | string | Budget key |
| `rule_id` | string | Budget rule that governs the key |
| `meter_id` | string | Meter the budget counts |
| `window` | object | The budget period window |
| `limit` | integer | Budget limit |
| `limit_policy` | object | Reference to the policy that set the limit |
| `held` | integer | Amount held |
| `consumed` | integer | Amount consumed |
| `credited` | integer | Amount credited |
| `debited` | integer | Amount debited |
| `available` | integer | Amount available now |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Balance returned |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 404 | `QUOTA_NOT_FOUND` | No budget key with this identifier |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/quota/balances/budget-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## POST `/api/v1/quota/loans`

Grants a loan under a sharing rule of the active policy. Requires the `loans` grant and an `Idempotency-Key`.

### Request body

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | Yes | Loan identifier |
| `rule_id` | string | Yes | Sharing rule the loan is granted under |
| `amount` | integer | Yes | Amount of the loan |
| `expires_at` | RFC 3339 timestamp | Yes | Expiry of the loan |

The body has no `tenant_id`. A `tenant_id` field is an unknown field and is refused with `400`.

### Response

The response is the loan: `request` (`tenant_id`, `id`, `rule_id`, `amount`, `expires_at`), `policy` (reference), `terms`, `lender`, `borrower`, `granted_at`, `returned`, `outstanding`, and `state`, which is `open`, `expired_committed` or `closed`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Loan granted. A replay with the same key returns the stored loan. |
| 400 | `IDEMPOTENCY_REQUIRED` | Header missing, repeated or invalid |
| 400 | `QUOTA_INVALID_REQUEST` | The body is invalid, or it has an unknown field |
| 403 | `QUOTA_ACTION_DENIED` | The `loans` grant is missing |
| 403 | `QUOTA_OPERATION_DENIED` | The engine refuses the loan under the sharing rule |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 404 | `QUOTA_NOT_FOUND` | The sharing rule does not exist |
| 409 | `QUOTA_STATE_CONFLICT` | The loan conflicts with an existing record |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/quota/loans" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: loan-example-1" \
  -d '{"id":"loan-example","rule_id":"rule-example","amount":100,"expires_at":"2026-11-11T00:00:00Z"}'
```

## GET `/api/v1/quota/loans/{id}`

Returns one loan in the shape described above.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Loan returned |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 404 | `QUOTA_NOT_FOUND` | No loan with this identifier |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/quota/loans/loan-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## POST `/api/v1/quota/loans/{id}/return`

Returns the unused part of a loan. The request has no body. Requires the `loans` grant and an `Idempotency-Key`. The response is the loan in the shape described above.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Unused amount returned |
| 400 | `IDEMPOTENCY_REQUIRED` | Header missing, repeated or invalid |
| 403 | `QUOTA_ACTION_DENIED` | The `loans` grant is missing |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 404 | `QUOTA_NOT_FOUND` | No loan with this identifier |
| 409 | `QUOTA_STATE_CONFLICT` | The loan is not in a state that can be returned |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/quota/loans/loan-example/return" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Idempotency-Key: loan-return-example-1"
```

## GET `/api/v1/quota/evidence/{id}`

Returns the terminal evidence of one event, with its usage acknowledgement. The `id` is the event identifier.

| Field | Type | Meaning |
|---|---|---|
| `evidence` | object | `version`, `producer_id`, `configuration_digest`, `observed_model_version` (omitted when empty), `require_model_version` (omitted unless `true`) and `settlement` |
| `acknowledgement` | object | `tenant_id`, `attempt_id`, `event_id`, `evidence_digest`, `status`, `resolution_id` (omitted when empty), `accepted_at`, `settled_at` (omitted when empty), `next_attempt_at` (omitted when empty), `attempts`, `last_error_code` (omitted when empty) and `decision` (omitted when empty) |

The `settlement` holds `tenant_id`, `attempt_id`, `event_id`, `claim`, `usage` (a map of counter names to integers) and, when set, `model_version` and `provider_resource_id`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Evidence returned |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 404 | `QUOTA_NOT_FOUND` | No evidence for this event |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/quota/evidence/event-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## POST `/api/v1/quota/reconciliations`

Plans the reconciliation of a settled attempt: a correction to the recorded usage, with evidence. The plan is stored and is approved by a second administrator through the apply route. Requires the `reconcile:plan` grant and an `Idempotency-Key`.

### Request body

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | Yes | Plan identifier |
| `attempt_id` | string | Yes | The attempt to reconcile |
| `reason` | string | Yes | Why the usage is corrected |
| `evidence_ref` | string | Yes | Reference to the evidence supporting the correction |
| `usage` | object | Yes | The corrected usage: a map of counter names to integers |
| `model_version` | string | No | Model version the usage belongs to |
| `provider_resource_id` | string | No | Provider resource the usage belongs to |
| `expires_at` | RFC 3339 timestamp | Yes | When the plan stops being approvable |

### Response

The plan:

| Field | Type | Meaning |
|---|---|---|
| `version` | string | Plan format version |
| `id`, `tenant_id`, `attempt_id`, `reason`, `evidence_ref` | string | As submitted |
| `requested_by` | object | `principal_id`, `actor_id` (omitted when empty) and `request_id` |
| `independent_approval` | boolean | `true` when the approver must differ from the requester |
| `expected_attempt_digest` | string | Digest of the attempt the plan was built on |
| `original_evidence_digest` | string | Omitted when empty |
| `settlement` | object | The corrected settlement |
| `created_at` | RFC 3339 timestamp | Creation time |
| `expires_at` | RFC 3339 timestamp | Expiry |
| `digest` | string | Digest of the plan. Passed to the apply route. |

Whether the requester may also approve is an operator setting. Independent approval is required unless the server is configured to allow self-approval.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Plan stored |
| 400 | `IDEMPOTENCY_REQUIRED` | Header missing, repeated or invalid |
| 400 | `QUOTA_INVALID_REQUEST` | The body is invalid or has an unknown field |
| 403 | `QUOTA_ACTION_DENIED` | The `reconcile:plan` grant is missing |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 404 | `QUOTA_NOT_FOUND` | The attempt does not exist |
| 409 | `QUOTA_STATE_CONFLICT` | The attempt cannot be reconciled in its current state |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/quota/reconciliations" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: plan-example-1" \
  -d '{"id":"review","attempt_id":"attempt-example","reason":"example reason","evidence_ref":"evidence-example","usage":{"output_tokens":10},"expires_at":"2026-11-11T00:00:00Z"}'
```

## GET `/api/v1/quota/reconciliations/{id}`

Returns the stored plan and, once it has been applied, its receipt.

| Field | Type | Meaning |
|---|---|---|
| `plan` | object | The plan described above |
| `receipt` | object | Omitted until the plan is applied. See the apply route. |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Inspection returned |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 404 | `QUOTA_NOT_FOUND` | No plan with this identifier |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/quota/reconciliations/review" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## POST `/api/v1/quota/reconciliations/{id}/apply`

Approves and applies a plan. The body carries the digest of the plan being approved. The digest must match the stored plan. Requires the `reconcile:approve` grant and an `Idempotency-Key`.

### Request body

| Field | Type | Required | Meaning |
|---|---|---|---|
| `digest` | string | Yes | Digest of the plan, as returned by the plan route |

### Response

The receipt:

| Field | Type | Meaning |
|---|---|---|
| `version` | string | Receipt format version |
| `plan_id` | string | Plan identifier |
| `plan_digest` | string | Digest of the approved plan |
| `tenant_id`, `attempt_id` | string | As in the plan |
| `approved_by` | object | `principal_id`, `actor_id` (omitted when empty) and `request_id` |
| `applied_at` | RFC 3339 timestamp | Time of application |
| `decision` | object | The settlement decision. Its `state` is `settled` once applied. |

A repeated apply of the same plan by the same approver returns the stored receipt, as a replay.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Plan applied, or a replay returned |
| 400 | `IDEMPOTENCY_REQUIRED` | Header missing, repeated or invalid |
| 400 | `QUOTA_INVALID_REQUEST` | The body is invalid |
| 403 | `QUOTA_ACTION_DENIED` | The `reconcile:approve` grant is missing |
| 403 | `QUOTA_OPERATION_DENIED` | The approver is the requester while independent approval is required, or the plan has expired |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 404 | `QUOTA_NOT_FOUND` | No plan with this identifier |
| 409 | `QUOTA_STATE_CONFLICT` | A stored receipt for this plan belongs to a different digest or approver |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/quota/reconciliations/review/apply" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: apply-example-1" \
  -d '{"digest":"digest-example"}'
```

## GET `/api/v1/quota/attempts/{id}`

Returns one quota attempt. This route does not check an action grant. A caller with the `admin` role may read any attempt of the tenant. Any other verified caller may read only an attempt whose principal and actor match their own.

The response is the attempt: `demand`, `plan`, `decision`, `admitted_at`, `claim`, `dispatch_at`, and the optional `unknown_reason`, `actual` (usage map), and `provider_resource`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Attempt returned |
| 401 | `IDENTITY_REQUIRED` | No verified identity |
| 403 | `TENANT_REQUIRED` | No verified tenant |
| 404 | `NOT_FOUND` | No quota engine for the tenant, or the caller may not read this attempt. Non-admins get the same response for another caller's attempt as for a missing one. |
| 404 | `QUOTA_ATTEMPT_UNAVAILABLE` | The attempt does not exist |
| 503 | `QUOTA_ATTEMPT_UNAVAILABLE` | The attempt store could not be read |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/quota/attempts/attempt-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## Behaviour notes

- **Two-step reconciliation.** A plan is created by one administrator and applied by another. The apply route checks that the digest matches, that the plan has not expired, and that the approver is not the requester while independent approval is required.
- **Simulation changes nothing.** `validate` and `preview` are read-only. Their responses say so.
- **Tenant scope.** The tenant comes from the verified binding. A body that names another tenant is refused with `TENANT_MISMATCH` (for policies and demands) or with `QUOTA_INVALID_REQUEST` (for loan bodies, which have no tenant field).
- **Replays.** Mutations are idempotent on the `Idempotency-Key`. A recognised replay returns the stored result with `200`.
- **Grants are current.** The action grant is resolved again at the time of each request, so a revoked grant stops working before the token expires.
- **Operator option.** Whether a plan's requester may also approve it is an operator setting, never a request option.
- **Evidence and balances are read-only.** Evidence, balances and attempts can be read, and they cannot be changed through this API.

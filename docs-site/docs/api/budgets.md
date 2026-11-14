---
title: Budgets API
---

The budgets API manages tenant budgets: team and project limits on cost, tokens or requests, arranged in a tree and enforced by QuotaPlex in the same admission as the operator policy. What a budget counts, how windows and unknown prices behave, the overshoot bound and the replica rules are in [Budgets](/docs/library/budgets). This page is the route reference.

Every route is tenant-scoped; the tenant comes from the verified binding. QuotaPlex must be enabled for the tenant, or a write is refused with `409 QUOTAPLEX_REQUIRED`.

## Routes

| Method | Path | Access | Purpose |
|---|---|---|---|
| GET | `/api/v1/budgets` | admin, deployer | List budget records with revisions |
| GET | `/api/v1/budgets/{id}` | admin, deployer | Read one budget; the `ETag` header is its revision |
| PUT | `/api/v1/budgets/{id}` | admin | Create (`If-Match: 0`) or replace (`If-Match: <revision>`) |
| DELETE | `/api/v1/budgets/{id}` | admin | Delete (`If-Match: <revision>`, required) |
| POST | `/api/v1/budgets/preview` | admin | Which budgets a hypothetical request would draw from, and why |
| GET | `/api/v1/budgets/{id}/status` | any verified caller | Counters for the current window, scoped to the caller |

## PUT /api/v1/budgets/{id}

The body is the specification. `id` must equal the path id. Unknown fields are refused.

```json
{
  "id": "platform",
  "display_name": "Platform team",
  "parent": "engineering",
  "limit": {"unit": "cost", "currency": "USD", "amount": "250.00"},
  "window": {"kind": "calendar", "unit": "month", "time_zone": "UTC"},
  "members": {"groups": [{"source": "source-3f9c...", "id": "platform-eng"}], "access_points": ["ide"]},
  "on_unknown_cost": "deny",
  "thresholds": [50, 80, 100]
}
```

The response is the stored record: `tenant_id`, `kind` (`budget`), `id`, `revision` and `spec`.

Validation errors answer `400 INVALID_SPEC` with `details.field` naming the specification path, such as `limit/currency`, `limit/amount`, `window/kind`, `parent` or `members/client_registrations/0`. They cover: a cross-currency child, a cycle, a tree deeper than 4 levels, more than 24 budgets, an amount finer than the money meter's scale, a rolling window, a currency or unit the quota policy cannot meter, and a member that does not resolve (client registration, access point).

| Status | Code | Meaning |
|---|---|---|
| 400 | `INVALID_SPEC` | The specification is invalid; `details.field` names the field when there is one |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the stored revision |
| 409 | `QUOTAPLEX_REQUIRED` | QuotaPlex is not enabled for the tenant |
| 409 | `BUDGET_HAS_CHILDREN` | Delete refused while another budget names this one as `parent` |
| 428 | `REVISION_REQUIRED` | Delete without `If-Match` |
| 403 | `ROLE_REQUIRED` | The caller lacks the role |

## GET /api/v1/budgets/{id}/status

```json
{
  "id": "platform",
  "display_name": "Platform team",
  "unit": "cost",
  "currency": "USD",
  "limit": "250.000000",
  "spent": "12.500000",
  "reserved": "1.000000",
  "remaining": "236.500000",
  "window": {"start": "2026-11-01T00:00:00Z", "end": "2026-12-01T00:00:00Z"},
  "unknown_cost_requests": 3,
  "on_unknown_cost": "allow_and_flag",
  "thresholds": [50, 80, 100],
  "threshold_events": [{"percent": 50, "at": "2026-11-05T10:00:00Z", "consumed": "125.000000"}],
  "children": [{"id": "platform-chat", "unit": "cost", "currency": "USD", "limit": "10.000000", "spent": "1.000000", "reserved": "0.000000", "remaining": "9.000000"}],
  "top_spenders": [{"principal_id": "principal-...", "requests": 12, "cost": [{"currency": "USD", "basis": "settled", "amount": "12500000", "scale": 6}]}]
}
```

Amounts are exact decimal strings (whole numbers for `tokens` and `requests`). A direct member of the budget receives only `id`, `display_name`, `parent`, `unit`, `currency`, `limit`, `spent`, `reserved`, `remaining`, `over_limit` and `window`. Operators also receive the fields from `unknown_cost_requests` down (`unknown_cost_requests` and `on_unknown_cost` for cost budgets only). `top_spenders` comes from the usage ledger and needs the admin role. A caller who is not a direct member, including one whose group membership is unknown, receives `404 NOT_FOUND`, the same answer as a missing budget.

## POST /api/v1/budgets/preview

```json
{
  "subject": {"principal": "principal-...", "groups": [{"source": "source-3f9c...", "id": "platform-eng"}], "agent": "", "client": ""},
  "access_point": "ide",
  "plane": "llmplex",
  "draft": {"id": "late", "limit": {"unit": "requests", "amount": "5"}, "window": {"kind": "tumbling", "hours": 1}, "members": {"principals": ["principal-..."]}}
}
```

`plane` is `llmplex` (default), `mcplex` or `a2aplex`. The subject is hypothetical and nothing in it is verified. `draft` is validated like a save, included in the answer when valid, and never stored.

```json
{
  "hypothetical": true,
  "plane": "llmplex",
  "access_point": "ide",
  "draws": [{"id": "platform", "direct": true, "reasons": ["group:platform-eng"], "unit": "cost", "currency": "USD", "limit": "250.00", "window": {"kind": "calendar", "unit": "month", "time_zone": "UTC"}}],
  "skipped": [{"id": "chat-tokens", "reason": "tokens budgets count model traffic only; this request is on mcplex"}],
  "draft": {"valid": true},
  "notes": []
}
```

A draw with `direct: false` is an ancestor reached through `via_child`. The list is ordered leaf first.

## Refusals on model and tool routes

A request refused by a budget keeps the protocol's own quota error. The quota code is `QUOTA_BUDGET_EXHAUSTED` (HTTP 429, with `Retry-After`), `QUOTA_BUDGET_COST_UNKNOWN` (403, a cost budget and no price), `QUOTA_BUDGET_UNSUPPORTED_OPERATION` (403, the operation cannot meter the budget's unit), `QUOTA_BUDGETS_UNAVAILABLE` (503, budgets could not be verified) or `QUOTA_MEMBERSHIP_UNAVAILABLE` (503, the caller's groups were not verified). Direct members of the budget see its name and remaining allowance in the message; a caller who reaches it only through a child sees only the child's name.

---
title: Runs API
---

Access rules are being tightened in the same release; check the route's response for the authoritative answer.

The runs API is the read side of the AIPlex and Tape integration. Tape records each agent run as a journal. AIPlex projects that journal into runs, events, effects, obligations and budget charges, and it serves them here. The same API also exposes operator actions that act on a run through Tape, such as redriving a stuck run or cancelling one. Each action is written to a separate operator audit trail, so the run's Tape journal stays unchanged.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/runs` | List projected runs, optionally filtered | Verified identity. Tenant filter as described under Tenant scope. |
| GET | `/api/v1/runs/_health` | Report whether the run pipeline is receiving data | Verified identity |
| GET | `/api/v1/runs/{run_id}` | Read one run summary | Verified identity |
| GET | `/api/v1/runs/{run_id}/events` | Read the ordered event timeline of one run | Verified identity |
| GET | `/api/v1/runs/{run_id}/effects` | Read the effect events of one run | Verified identity |
| GET | `/api/v1/runs/{run_id}/obligations` | Read the obligation events of one run | Verified identity |
| GET | `/api/v1/runs/{run_id}/budgets` | Read the budget events of one run | Verified identity |
| GET | `/api/v1/runs/{run_id}/operator-audit` | Read the operator actions taken on one run | Verified identity |
| POST | `/api/v1/runs/{run_id}/redrive` | Ask Tape to resume a run | `admin` or `deployer` outside development mode |
| POST | `/api/v1/runs/{run_id}/reconcile` | Ask Tape to reconcile a run | `admin` or `deployer` outside development mode |
| POST | `/api/v1/runs/{run_id}/cancel` | Ask Tape to cancel a run | `admin` or `deployer` outside development mode |
| POST | `/api/v1/runs/{run_id}/signal` | Send a resolution to a waiting gate | `admin` or `deployer` outside development mode |
| POST | `/api/v1/runs/{run_id}/compensate` | Ask Tape to start compensation for a run | `admin` or `deployer` outside development mode |
| POST | `/api/v1/runs/{run_id}/compact` | Ask Tape to compact a run now | `admin` or `deployer` outside development mode |

All routes are mounted under `/api/v1/runs`. They require a verified identity. Outside development mode, the `/api/v1` group requires the `admin` or `deployer` role for every non-GET method, so the operator actions need one of those roles.

## Tenant scope

Every route in this group sits behind a tenant check. The check is enabled only when the environment variable `AIPLEX_REQUIRE_TENANT` is set to `1`. When it is set, the caller must have a tenant claim or the cross-tenant scope `aiplex:runs:read:cross_tenant`. Otherwise the response is `403 TENANT_REQUIRED`.

The list route applies the tenant claim as a filter. A caller with a tenant claim and no cross-tenant scope gets only that tenant's runs. A `tenant_id` query value for another tenant returns an empty list, not an error. When the tenant check is off, a caller without a tenant claim gets runs from every tenant unless `tenant_id` is given. The routes for one run do not compare the run's tenant with the caller's tenant.

## Response and error format

Successful responses are JSON. Most lists are wrapped in an object keyed by the field name, as shown below. Errors from the run handlers are plain text with the matching HTTP status, not the AIPlex error shape. The only AIPlex error shape in this group is the `403 TENANT_REQUIRED` response, which the tenant check returns when it is enabled.

```json
{
  "code": "TENANT_REQUIRED",
  "message": "caller has no tenant claim and no cross-tenant scope",
  "request_id": "req-example"
}
```

## GET `/api/v1/runs`

Returns the projected runs as `{"runs": [...]}`. Results come from the store and are capped by `limit`.

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `tenant_id` | string | No | Tenant filter. Subject to the tenant scope above. |
| `agent_id` | string | No | Filter by agent identifier |
| `limit` | integer | No | Maximum runs to read. Default 100. A value that is not a positive integer falls back to the default. |
| `has_unknown_effects` | string | No | `true` keeps only runs with at least one unknown effect. Applied after the store read. |
| `has_obligations` | string | No | `true` keeps only runs with at least one obligation. Applied after the store read. |

### Run fields

The run objects are described under [Run summary fields](#run-summary-fields).

### Status codes and errors

| Status | Body | When |
|---|---|---|
| 200 | `{"runs": [...]}` | Runs returned |
| 401 | `UNAUTHENTICATED` (AIPlex shape) | No verified identity outside development mode |
| 403 | `TENANT_REQUIRED` (AIPlex shape) | Tenant check enabled and no tenant claim or cross-tenant scope |
| 500 | plain text | Store read failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/runs?agent_id=agent-example&has_unknown_effects=true" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/runs/_health`

Reports whether the run pipeline has received data. It is intended for the Console empty state.

### Response fields

| Field | Type | Meaning |
|---|---|---|
| `last_ingest_at` | RFC 3339 timestamp | Time of the last ingest, as the store reports it |
| `has_runs` | boolean | `true` when at least one run exists. The handler does not filter by tenant. |
| `tape_instances_count` | integer | Number of instances that use the Tape runtime. The handler does not filter by tenant. |
| `now` | RFC 3339 timestamp | Server time, UTC |

This route does not return an error status. Store errors are ignored and their values are zero.

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/runs/_health" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
{
  "last_ingest_at": "2026-11-10T09:00:00Z",
  "has_runs": true,
  "tape_instances_count": 1,
  "now": "2026-11-10T09:05:00Z"
}
```

## GET `/api/v1/runs/{run_id}`

Returns one run summary as a JSON object.

### Status codes and errors

| Status | Body | When |
|---|---|---|
| 200 | run summary | Run returned |
| 404 | plain text | No run with this identifier |
| 500 | plain text | Store read failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/runs/run-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/runs/{run_id}/events`

Returns the ordered event timeline of one run as `{"events": [...]}`.

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `from_seq` | integer | No | Return events from this sequence number. Default 0. |
| `limit` | integer | No | Maximum events. Default 1000. A value that is not a positive integer falls back to the default. |

### Status codes and errors

| Status | Body | When |
|---|---|---|
| 200 | `{"events": [...]}` | Events returned |
| 500 | plain text | Store read failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/runs/run-example/events?from_seq=0&limit=200" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/runs/{run_id}/effects`

Returns the effect events of one run as `{"effects": [...]}`. The handler reads up to 10000 events of the run and keeps the effect kinds: `effect.begin`, `effect.confirmed`, `effect.failed`, `effect.unknown` and `effect.duplicate`.

### Status codes and errors

| Status | Body | When |
|---|---|---|
| 200 | `{"effects": [...]}` | Effects returned |
| 500 | plain text | Store read failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/runs/run-example/effects" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/runs/{run_id}/obligations`

Returns the `obligation.created` events of one run as `{"obligations": [...]}`. Like effects, the handler reads up to 10000 events before filtering.

### Status codes and errors

| Status | Body | When |
|---|---|---|
| 200 | `{"obligations": [...]}` | Obligations returned |
| 500 | plain text | Store read failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/runs/run-example/obligations" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/runs/{run_id}/budgets`

Returns the `budget.charged` events of one run as `{"budgets": [...]}`. Like effects, the handler reads up to 10000 events before filtering.

### Status codes and errors

| Status | Body | When |
|---|---|---|
| 200 | `{"budgets": [...]}` | Budget events returned |
| 500 | plain text | Store read failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/runs/run-example/budgets" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/runs/{run_id}/operator-audit`

Returns the operator actions recorded for one run as `{"audit": [...]}`. Each entry is an operator audit row, described below. This trail is separate from the event timeline.

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `limit` | integer | No | Maximum rows. Default 100. A value that is not a positive integer falls back to the default. |

### Operator audit fields

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Audit row identifier |
| `run_id` | string | Run the action applied to |
| `action` | string | `redrive`, `reconcile`, `cancel`, `signal`, `compensate` or `compact` |
| `actor` | string | The verified identity's email, or its subject when there is no email. Without a verified identity, the `X-Aiplex-Actor` header. Without that, `authenticated` when an `Authorization` header is present, and otherwise `unknown`. |
| `at` | RFC 3339 timestamp | Time of the action, UTC |
| `reason` | string | Omitted when empty. Set by `cancel`. |
| `gate_name` | string | Omitted when empty. Set by `signal`. |
| `resolution` | string | Omitted when empty. Set by `signal`. |
| `status` | string | `accepted` or `failed` |
| `error` | string | Omitted when empty. Set when Tape rejected the action. |

### Status codes and errors

| Status | Body | When |
|---|---|---|
| 200 | `{"audit": [...]}` | Rows returned |
| 500 | plain text | Store read failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/runs/run-example/operator-audit?limit=20" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## Operator actions

The six `POST` routes below act on a run through Tape's admin interface. They share one flow:

1. The run must exist. An unknown run returns `404`.
2. The action is sent to Tape.
3. An operator audit row is written. A failed action is recorded with status `failed` and the error text.
4. On success the response is `202 Accepted`. Tape enqueues the work, so the response does not mean the action has finished.

The routes are wrapped in an idempotency check. The key is the `Idempotency-Key` header when it is sent. Otherwise it is built from the run identifier, the action and a hash of the body. Only `2xx` responses are cached, for five minutes. A repeated request with the same key returns the cached response with the header `X-Idempotent-Replay: true`. Errors are not cached, so a retry runs again.

### Success response

```json
{
  "accepted": true,
  "action": "redrive",
  "run_id": "run-example"
}
```

The `action` value matches the route: `redrive`, `reconcile`, `cancel`, `signal`, `compensate` or `compact`.

### Body and status codes

| Route | Body | Status codes beyond the shared ones |
|---|---|---|
| `POST .../redrive` | none | None |
| `POST .../reconcile` | none | None |
| `POST .../cancel` | optional JSON object with `reason` (string) | None. A body that does not decode is ignored and no reason is recorded. |
| `POST .../signal` | JSON object with `gate_name` (string, required) and `resolution_json` (string, optional) | `400` plain text when the body is not JSON or `gate_name` is empty |
| `POST .../compensate` | none | None |
| `POST .../compact` | none | None. On success the run is marked `compacted` and `compacted_at` is set. |

Shared status codes for every action:

| Status | Body | When |
|---|---|---|
| 202 | action response | Tape accepted the action |
| 401 | `UNAUTHENTICATED` (AIPlex shape) | No verified identity outside development mode |
| 403 | `FORBIDDEN` (AIPlex shape) | Caller lacks `admin` or `deployer` outside development mode |
| 403 | `TENANT_REQUIRED` (AIPlex shape) | Tenant check enabled and no tenant claim or cross-tenant scope |
| 404 | plain text | No run with this identifier |
| 500 | plain text | Store read or write failed |
| 502 | plain text | Tape rejected or could not complete the call. The audit row records status `failed`. |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/runs/run-example/signal" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: signal-example-1" \
  -d '{"gate_name":"gate-example","resolution_json":"{\"approved\":true}"}'
```

## Run summary fields

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Run identifier |
| `tenant_id` | string | Owning tenant |
| `agent_id` | string | Agent that ran |
| `plane` | string | Plane of the run |
| `actor` | string | Actor recorded on the run |
| `subject` | string | Subject recorded on the run |
| `aiplex_instance_id` | string | Omitted when empty. AIPlex instance that ran the agent. |
| `status` | string | `runnable`, `running`, `waiting`, `terminal`, `failed`, `compensating`, `stuck` or `cancelled` |
| `started_at` | RFC 3339 timestamp | Start of the run |
| `ended_at` | RFC 3339 timestamp | Omitted until the run ends |
| `decisions_count` | integer | Decision events |
| `effects_count` | integer | Effect events |
| `unknown_effects` | integer | Effects whose outcome is unknown |
| `obligations` | integer | Obligations created |
| `policy_violations` | integer | Policy violation events |
| `budget_usd_charged` | number | Total charged to the run budget, in US dollars |
| `compacted` | boolean | `true` once Tape has archived the bulky payloads |
| `compacted_at` | RFC 3339 timestamp | Omitted until the run is compacted |
| `retained_until` | RFC 3339 timestamp | Omitted when not set |

## Event fields

| Field | Type | Meaning |
|---|---|---|
| `run_id` | string | Run the event belongs to |
| `seq` | integer | Sequence number within the run |
| `tenant_id` | string | Owning tenant |
| `agent_id` | string | Agent that ran |
| `plane` | string | Plane of the run |
| `actor` | string | Actor recorded on the run |
| `subject` | string | Subject recorded on the run |
| `aiplex_instance_id` | string | Omitted when empty |
| `kind` | string | Event kind, listed below |
| `scope` | string | Omitted when empty. Set on effect and policy events. |
| `tool` | string | Omitted when empty. Set on effect and policy events. |
| `timestamp` | RFC 3339 timestamp | Time of the event |
| `payload_json` | string | Omitted when empty. The raw Tape journal payload as a JSON string. |

Event kinds: `run.started`, `run.completed`, `run.failed`, `decision.recorded`, `effect.begin`, `effect.confirmed`, `effect.failed`, `effect.unknown`, `effect.duplicate`, `obligation.created`, `gate.waiting`, `timer.scheduled`, `budget.charged`, `policy.violation` and `run.compacted`.

## Behaviour notes

- **Runs are a projection.** The run and event data come from Tape's journal through the ingest path. This API only reads and projects them. The ingest route is internal and is not part of this reference.
- **Operator actions do not rewrite the journal.** Each action is sent to Tape and recorded in the operator audit trail. The event timeline is unchanged.
- **Limits are read limits.** The effect, obligation and budget routes read up to 10000 events. The list route reads up to `limit` runs.
- **Errors are plain text.** Only the tenant check and the `/api/v1` group use the AIPlex error shape.

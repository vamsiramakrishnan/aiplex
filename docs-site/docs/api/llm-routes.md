---
title: LLM Routes API
---

Access rules are being tightened in the same release; check the route's response for the authoritative answer.

The LLM routes API manages how models are routed to provider backends, which provider credentials are registered, and the usage that agents report for model calls. A route names a model, its weighted backends and its fallbacks. Saving a route records the desired state and then submits it to the gateway deployment. Usage reports are recorded for accounting and warnings only. They never block a call, because the work they describe has already happened.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/llm/routes` | List saved route configurations | Verified identity |
| GET | `/api/v1/llm/routes/{modelId}` | Read one route configuration | Verified identity |
| PUT | `/api/v1/llm/routes/{modelId}` | Create or replace a route configuration | `admin` or `deployer` outside development mode |
| DELETE | `/api/v1/llm/routes/{modelId}` | Delete a route configuration | `admin` or `deployer` outside development mode |
| GET | `/api/v1/llm/providers` | List provider configurations | Verified identity |
| PUT | `/api/v1/llm/providers/{provider}` | Create or replace a provider configuration | `admin` or `deployer` outside development mode |
| POST | `/api/v1/llm/usage` | Record a usage report | `admin` outside development mode |
| GET | `/api/v1/llm/usage` | List recent usage reports | Verified identity |
| GET | `/api/v1/llm/usage/summary` | Summarize usage reports | Verified identity |

The routes are mounted under `/api/v1` by the `llmplex` module, which is enabled by default. All routes require a verified identity. Outside development mode, the `/api/v1` group requires the `admin` or `deployer` role for every non-GET method. Usage reports need the `admin` role. A caller without the required role receives `403 FORBIDDEN`. Development mode skips that check.

Errors use this shape:

```json
{
  "code": "INVALID_MODEL",
  "message": "valid route model identifier required",
  "request_id": "req-example"
}
```

`details`, `request_id` and `trace_id` are optional and omitted when empty.

## Revisions for route and provider configurations

Route and provider configurations use the configuration revision. A read returns it as a quoted `ETag`. A replace or delete of an existing configuration must send that value in `If-Match`, exactly.

- A replace or delete of an existing configuration without `If-Match` returns `428 PRECONDITION_REQUIRED`.
- `If-Match` that is not one exact non-negative integer returns `400 INVALID_PRECONDITION`.
- A stale value returns `412 REVISION_CONFLICT`.
- A create may omit `If-Match`.

Writes need a revisioned configuration backend. Without one, every write returns `503 CONFIGURATION_CAS_UNAVAILABLE`.

## Route configuration

A route is stored under its model identifier. The path `{modelId}` must be a valid DNS-style name, checked as `llm-{modelId}`, so it can be used in a resource name. A bad path returns `400 INVALID_MODEL`.

### Route fields

| Field | Type | Who sets it | Meaning |
|---|---|---|---|
| `model_id` | string | Caller, optional | Must equal the path `{modelId}` when sent |
| `id` | string | Server | Always `route-` followed by the model identifier |
| `owner` | string | Caller | Owner label. Stored as sent. |
| `backends` | array of object | Caller | Weighted backends. At most 128. Each backend has `provider`, `model_id`, `weight`, `enabled` and `secret_ref`. |
| `fallbacks` | array of string | Caller | Ordered fallback model identifiers. At most 32. Each must be a distinct valid identifier, not the route itself, and must name a saved route that is not pending. |
| `cache_ttl_seconds` | integer | Caller | Optional. Omitted when zero. Recorded as metadata only. |
| `budget` | object | Caller | Optional cost and token limits. Fields: `max_daily_cost_usd`, `max_monthly_cost_usd`, `max_daily_tokens`, `alert_threshold_pct`. |
| `rate_limit` | object | Caller | Optional. Fields: `requests_per_minute` and `tokens_per_minute`. |
| `backend_naming_version` | integer | Server | Always `2` for routes saved through this API |
| `activation_pending` | boolean | Server | `true` while a saved route has not been confirmed by the gateway |
| `deletion_pending` | boolean | Server | `true` while a delete is in progress |
| `retired_backends` | array of string | Server | Backends to clean up after a change |
| `created_at` | RFC 3339 timestamp | Server | Kept from the first save |
| `updated_at` | RFC 3339 timestamp | Stored value | Returned as stored |
| `revision` | integer | Server | Configuration revision. Concurrency uses `If-Match`. |

Backend `secret_ref` names a Secret Manager key for the backend's API key. When the backend leaves `secret_ref` empty and the provider is registered, the provider's reference is used.

## GET `/api/v1/llm/routes`

Returns every saved route configuration as a JSON array.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 500 | `STORE_ERROR` | Store read failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/llm/routes" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/llm/routes/{modelId}`

Returns one route configuration. The response carries an `ETag` header with the revision.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Route returned |
| 400 | `INVALID_MODEL` | Path is not a valid route model identifier |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 404 | `NOT_FOUND` | No route for this model |
| 500 | `STORE_ERROR` | Store read failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/llm/routes/model-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## PUT `/api/v1/llm/routes/{modelId}`

Creates or replaces a route. The body is one JSON object with the caller fields above. Unknown fields are rejected, and the body is limited to 1 MiB.

The handler saves the desired route, submits its backends and route to the gateway, and then removes any backends the route no longer uses. The desired state is saved before submission. If submission fails, the saved desired state stays in place, and the same request can be retried. No rollback across systems is implied.

Checks run in this order:

1. The path identifier and body decode.
2. The body's `model_id` and `id`, when sent, match the path.
3. The route's backends and fallbacks are valid. Provider registrations apply here.
4. An existing route that is being deleted cannot be replaced. An existing route with an unfinished activation can only be retried with the same desired route.
5. A change to the backend list is refused while another route uses this route as a fallback.
6. The gateway deployment adapter is configured.
7. Backend names do not overlap another route's inventory.

The response header `X-LLM-Activation` reports the activation state. A successful replace returns `submitted-unverified`, because the gateway has accepted the submission but its serving state has not been confirmed.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Route saved and submitted. Body is the route. |
| 400 | `INVALID_MODEL` | Path is not a valid route model identifier |
| 400 | `BAD_REQUEST` | Body does not decode, identity does not match the path, or backends, fallbacks or providers fail validation |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` or `deployer` outside development mode |
| 409 | `ROUTE_DELETION_PENDING` | The saved route is being deleted |
| 409 | `ROUTE_ACTIVATION_PENDING` | The saved route has an unfinished activation for a different desired route |
| 409 | `ROUTE_IN_USE` | The backend list changes while another route uses this route as a fallback |
| 409 | `INVALID_CLEANUP_INVENTORY` | The saved backend inventory needs operator review |
| 409 | `BACKEND_OWNERSHIP_CONFLICT` | Backend names overlap another route |
| 412 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `PRECONDITION_REQUIRED` | An existing route was replaced without `If-Match` |
| 400 | `INVALID_PRECONDITION` | `If-Match` is malformed |
| 500 | `STORE_ERROR` | Store read failed |
| 502 | `ROUTE_APPLY_FAILED` | Desired state saved, but gateway submission failed. The `X-LLM-Activation` header is `failed`. |
| 502 | `ROUTE_CLEANUP_FAILED` | Desired state saved and submitted, but cleanup of old backends failed. The header is `cleanup-pending`. |
| 503 | `CONFIGURATION_CAS_UNAVAILABLE` | The configuration store does not support revisions |
| 503 | `DEPLOYMENT_UNAVAILABLE` | The gateway deployment adapter is not configured |

### Response headers

| Header | Meaning |
|---|---|
| `X-LLM-Configuration` | `saved` |
| `X-LLM-Policy-Mode` | `reporting-only` |
| `X-LLM-Activation` | `submitted-unverified` on success, `failed` or `cleanup-pending` on the 502 responses |
| `X-LLM-Configuration-Warning` | `cache_ttl_metadata_not_applied_to_legacy_route` when `cache_ttl_seconds` is set |

### Example

```bash
curl -sS -X PUT "$AIPLEX_URL/api/v1/llm/routes/model-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -H 'If-Match: "1"' \
  -d '{"backends":[{"provider":"provider-example","model_id":"model-example","weight":100,"enabled":true,"secret_ref":"secret-example"}],"fallbacks":["model-fallback-example"]}'
```

## DELETE `/api/v1/llm/routes/{modelId}`

Deletes a route. `If-Match` is required and must carry the current revision. The handler marks the route as deleting, removes the gateway route, cleans up its backends, and then removes the saved configuration. A route cannot be deleted while another route uses it as a fallback, or while its activation is pending.

On success the response is `204` with no body.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 204 | none | Route deleted |
| 400 | `INVALID_MODEL` | Path is not a valid route model identifier |
| 400 | `INVALID_PRECONDITION` | `If-Match` is malformed |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` or `deployer` outside development mode |
| 404 | `NOT_FOUND` | No route for this model |
| 409 | `ROUTE_ACTIVATION_PENDING` | An unfinished activation must finish first |
| 409 | `ROUTE_IN_USE` | Another route uses this route as a fallback |
| 409 | `BACKEND_OWNERSHIP_CONFLICT` | Backend names overlap another route. Desired state is kept. |
| 412 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `PRECONDITION_REQUIRED` | `If-Match` is missing |
| 500 | `STORE_ERROR` | Store read failed |
| 502 | `ROUTE_DELETE_FAILED` | Gateway route deletion failed. Desired state is kept. |
| 502 | `ROUTE_CLEANUP_FAILED` | Backend cleanup failed. Desired state is kept. |
| 503 | `DEPLOYMENT_UNAVAILABLE` | The gateway deployment adapter is not configured |

### Example

```bash
curl -sS -X DELETE "$AIPLEX_URL/api/v1/llm/routes/model-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H 'If-Match: "2"'
```

## Provider configuration

A provider is a registered credential source for backends of the same name. A provider that is enabled needs a `secret_ref`. When Secret Manager is configured, the reference must exist there.

### Provider fields

| Field | Type | Who sets it | Meaning |
|---|---|---|---|
| `revision` | integer | Server | Configuration revision |
| `provider` | string | Path | Must equal the path `{provider}` when sent. A DNS-style name. |
| `display_name` | string | Caller | Up to 512 characters |
| `base_url` | string | Caller | Optional. HTTP or HTTPS, with a host, and no user information, query or fragment. |
| `secret_ref` | string | Caller | Kubernetes secret reference. Required when `enabled` is `true`. |
| `enabled` | boolean | Caller | Whether backends may use this provider |
| `region` | string | Caller | Optional. Up to 128 characters. Used for Bedrock. |
| `project_id` | string | Caller | Optional. Up to 256 characters. Used for Vertex AI. |

### GET `/api/v1/llm/providers`

Returns every provider configuration as a JSON array.

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 500 | `STORE_ERROR` | Store read failed |

### PUT `/api/v1/llm/providers/{provider}`

Creates or replaces a provider configuration. Revision rules are the same as for routes. Disabling a provider is refused while an enabled route backend uses it, or while backend cleanup for it is still pending.

The response headers say that the change is saved and not applied to existing routes. `X-LLM-Activation` is `configuration-only`. `X-LLM-Configuration-Warning` lists `credential_reference_requires_operator_managed_kubernetes_secret` and `provider_defaults_do_not_rewrite_existing_backend_references`. It also lists `provider_endpoint_metadata_not_applied_to_legacy_routes` when `base_url`, `region` or `project_id` is set.

| Status | Code | When |
|---|---|---|
| 200 | none | Provider saved. Body is the provider. |
| 400 | `BAD_REQUEST` | Body does not decode, or `provider` differs from the path |
| 400 | `INVALID_PROVIDER` | Name, secret reference, base URL or metadata fail validation, or the secret does not exist |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` or `deployer` outside development mode |
| 409 | `PROVIDER_IN_USE` | Disabling a provider that enabled route backends still use, or whose backend cleanup is pending |
| 412 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `PRECONDITION_REQUIRED` | An existing provider was replaced without `If-Match` |
| 400 | `INVALID_PRECONDITION` | `If-Match` is malformed |
| 503 | `CONFIGURATION_CAS_UNAVAILABLE` | The configuration store does not support revisions |
| 503 | `CONFIGURATION_UNAVAILABLE` | The configuration could not be read or written |

### Example

```bash
curl -sS -X PUT "$AIPLEX_URL/api/v1/llm/providers/provider-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"display_name":"Provider example","enabled":true,"secret_ref":"secret-example"}'
```

## Usage reports

Usage reports record what an agent or service has already consumed. Each report is stored in the legacy LLM usage store and mirrored into the tenant's usage ledger, as described in [Ledger mirroring](#ledger-mirroring).

### POST `/api/v1/llm/usage`

Records one usage report. Send one JSON object. Unknown fields are rejected.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | No | Report identifier. Up to 512 characters, with no `/` or line breaks. |
| `model_id` | string | Yes | Model identifier. Up to 512 characters. |
| `provider` | string | Yes | Provider name. Up to 128 characters. |
| `agent_id` | string | No | Reporting agent. Up to 512 characters. |
| `user_id` | string | No | User on whose behalf the call was made. Up to 1024 characters. |
| `input_tokens` | integer | No | Zero or more |
| `output_tokens` | integer | No | Zero or more |
| `cost_usd` | number | No | Zero or more. When it is zero and the catalog template has pricing, the cost is computed from the template. |
| `latency_ms` | integer | No | Zero or more |
| `cached` | boolean | No | Whether the response came from a cache |
| `timestamp` | RFC 3339 timestamp | No | Defaults to the time of the request |

The server sets `total_tokens` to `input_tokens` plus `output_tokens`. The response is the stored report, with those values filled in.

Headers on the response:

| Header | Meaning |
|---|---|
| `X-LLM-Policy-Mode` | `reporting-only` |
| `X-LLM-Price-Warning` | `reporting_price_unavailable` when no price was available to compute cost |
| `X-Budget-Warning` | Budget state for the model: `daily_cost_exceeded`, `monthly_cost_exceeded`, `daily_cost_threshold`, `monthly_cost_threshold` or `daily_tokens_exceeded`. Added when the limit is reached. The report is still stored. |
| `X-RateLimit-Warning` | `requests_per_minute_exceeded` or `tokens_per_minute_exceeded`, for the last minute. The report is still stored. |
| `X-LLM-Reporting-Warning` | `configuration_unavailable`, `budget_summary_unavailable`, `rate_summary_unavailable` or `rate_summary_out_of_range` when a check could not be completed |

#### Status codes and errors

| Status | Code | When |
|---|---|---|
| 201 | none | Report stored. Body is the stored report. |
| 400 | `BAD_REQUEST` | Body does not decode, has unknown fields, or is not one JSON object |
| 400 | `INVALID_USAGE` | A required identifier is missing or too long, a counter or cost is negative or out of range, or the total overflows |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` outside development mode |
| 500 | `STORE_ERROR` | The report could not be stored |

#### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/llm/usage" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"model_id":"model-example","provider":"provider-example","agent_id":"agent-example","input_tokens":120,"output_tokens":40,"latency_ms":900}'
```

```json
{
  "id": "usage-example",
  "model_id": "model-example",
  "provider": "provider-example",
  "agent_id": "agent-example",
  "user_id": "",
  "input_tokens": 120,
  "output_tokens": 40,
  "total_tokens": 160,
  "cost_usd": 0,
  "latency_ms": 900,
  "cached": false,
  "timestamp": "2026-11-10T09:00:00Z"
}
```

### GET `/api/v1/llm/usage`

Returns recent usage reports as a JSON array.

#### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `model_id` | string | No | Filter by model |
| `agent_id` | string | No | Filter by agent |
| `since` | RFC 3339 timestamp | No | Return reports at or after this time. Default 24 hours before the request. |
| `limit` | integer | No | From 1 to 1000. Default 100. |

#### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 400 | `INVALID_SINCE` | `since` is not an RFC 3339 timestamp |
| 400 | `INVALID_LIMIT` | `limit` is not an integer from 1 to 1000 |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 500 | `STORE_ERROR` | Store read failed |

#### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/llm/usage?model_id=model-example&limit=50" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

### GET `/api/v1/llm/usage/summary`

Returns one summary of the usage reports that match the filters.

#### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `model_id` | string | No | Filter by model |
| `agent_id` | string | No | Filter by agent |
| `period` | string | No | `day`, `week` or `month`. Default `day`. |

#### Response fields

| Field | Type | Meaning |
|---|---|---|
| `model_id` | string | Omitted when empty. The model filter. |
| `provider` | string | Omitted when empty |
| `agent_id` | string | Omitted when empty. The agent filter. |
| `period` | string | `day`, `week` or `month` |
| `input_tokens` | integer | Sum of input tokens |
| `output_tokens` | integer | Sum of output tokens |
| `total_tokens` | integer | Sum of total tokens |
| `total_cost_usd` | number | Sum of reported cost |
| `request_count` | integer | Number of reports |
| `cache_hits` | integer | Number of reports marked `cached` |
| `avg_latency_ms` | number | Average latency |

#### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Summary returned |
| 400 | `INVALID_PERIOD` | `period` is not `day`, `week` or `month` |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 500 | `STORE_ERROR` | Store read failed |

#### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/llm/usage/summary?model_id=model-example&period=week" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## Ledger mirroring

Every accepted `POST /api/v1/llm/usage` report is also mirrored into the tenant's usage ledger, which the [usage API](./usage.md) reads. The legacy report is authoritative for the `POST` response. A mirroring failure is logged and does not change that response.

The mirrored record differs from the report in these ways:

- **Source.** The ledger record has `source: "reported"`. Records from managed exchanges have `source: "managed"`. Filter on `source=reported` in the usage API to see only mirrored reports.
- **Plane and status.** The plane is `llmplex`. The status is always `ok`, because a report carries no status.
- **Identities.** The report's `user_id` becomes `principal_id`, and its `agent_id` becomes `actor_id`. The request identifier is the request's own.
- **Timing.** The record's finish time is the report's `timestamp`. Its start is that time minus `latency_ms`.
- **Usage.** Input, output and total tokens are copied. Cache and reasoning counters are not reported, so they stay unknown.
- **Cost.** The report's floating-point `cost_usd` is never copied. The ledger estimates cost from a matching model price resource, or leaves it unknown when none matches.
- **Tenant.** The tenant comes from the verified binding. Without one, the report is not mirrored, and a warning is logged.

## Behaviour notes

- **Reports never block calls.** A budget or rate warning is returned in headers. The report is stored in every case.
- **Route saves are desired state.** A saved route is recorded before it is submitted. Its `activation_pending` flag and `X-LLM-Activation` header show whether submission is confirmed. A `submitted-unverified` result does not prove that the gateway is serving the route.
- **Providers do not rewrite routes.** Changing a provider does not change backends that already reference it. Only new routes pick up provider defaults.
- **Reads are not tenant-scoped.** The LLM handlers read the LLM store without resolving a tenant. The results depend on that store.

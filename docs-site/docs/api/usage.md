---
title: Usage API
---

The usage API reads the tenant's usage ledger: one append-only record per managed exchange on the LLM, MCP and A2A planes, plus the operator-maintained model prices that the ledger uses to estimate cost. Any verified caller of the tenant can list and read records. Administrators see every record of their tenant. Other callers see only records in which they are the `principal_id` or the `actor_id`, and a record they cannot see is reported as `404 NOT_FOUND`. Price writes require the `admin` role outside development mode. Every route is tenant-scoped: the tenant comes from the verified binding, never from a query parameter.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/usage/records` | List one page of visible records, newest first | Verified identity and tenant. Admin sees all tenant records; others see their own. |
| GET | `/api/v1/usage/records/{id}` | Read one visible record | Same as the list route |
| GET | `/api/v1/usage/summary` | Aggregate visible records into groups and optional time buckets | Same as the list route |
| GET | `/api/v1/usage/records/{id}/feedback` | Read a record's feedback history, newest first | The record's principal or actor, or an operator (`admin` or `deployer`) |
| POST | `/api/v1/usage/records/{id}/feedback` | Add feedback to a record (append-only) | Same as the history route |
| GET | `/api/v1/usage/config` | Read the deployment's usage settings | Verified identity and tenant |
| GET | `/api/v1/usage/prices` | List the tenant's model-price resources | Verified identity and tenant |
| GET | `/api/v1/usage/prices/{id}` | Read one model-price resource | Verified identity and tenant |
| PUT | `/api/v1/usage/prices/{id}` | Create or update a model-price resource | `admin` role (outside development mode) |
| DELETE | `/api/v1/usage/prices/{id}` | Delete a model-price resource | `admin` role (outside development mode) |
| POST | `/api/v1/usage/backfill` | Price records whose cost was unknown when written | `admin` role (outside development mode) |

The API is mounted under `/api/v1` and all routes require a verified identity. Requests and responses are JSON. Errors use this shape:

```json
{
  "code": "INVALID_QUERY",
  "message": "limit must be between 1 and 1000",
  "request_id": "req-example"
}
```

`details`, `request_id` and `trace_id` are optional and omitted when empty.

## GET /api/v1/usage/records

Returns one page of records, newest first, ordered by `(started_at, id)`.

### Query parameters

All filters are exact matches. Each parameter may appear at most once. Unknown parameters and repeated parameters are rejected with `400 INVALID_QUERY`. Any value longer than 512 characters is rejected.

| Name | Type | Required | Meaning |
|---|---|---|---|
| `plane` | string | No | `llmplex`, `mcplex` or `a2aplex` |
| `provider` | string | No | Provider name recorded on the exchange |
| `model_id` | string | No | Model identifier |
| `tool_name` | string | No | MCP tool name |
| `actor_id` | string | No | Acting identity recorded on the exchange |
| `principal_id` | string | No | Principal identity recorded on the exchange |
| `client_id` | string | No | Verified token's authorized party |
| `access_point_id` | string | No | Access point that the exchange entered through |
| `caller_kind` | string | No | `agent`, `user` or `client` |
| `status` | string | No | `ok`, `error`, `denied` or `unknown` |
| `source` | string | No | `managed` (exchanges through AIPlex) or `reported` (mirrored from a legacy usage report, or read from a harness's own telemetry sent to [`/otlp`](./otlp.md)) |
| `cost_basis` | string | No | `settled`, `estimated`, `reported`, `cached` or `unknown` |
| `harness` | string | No | Reported records from one harness, for example `claude-code` |
| `tag.<key>` | string | No | Records carrying that tag, for example `tag.experiment=arm-a`. Several `tag.*` parameters must all match. |
| `since` | RFC 3339 timestamp | No | Inclusive lower bound on `started_at` |
| `until` | RFC 3339 timestamp | No | Exclusive upper bound on `started_at`. Must be after `since` when both are set. |
| `limit` | integer | No | Page size from 1 to 1000. Default 100. |
| `cursor` | string | No | Opaque cursor from `next_cursor` of the previous page |

### Response

The response is a page object.

| Field | Type | Meaning |
|---|---|---|
| `records` | array of record | Records on this page |
| `next_cursor` | string | Present only when another page exists. Pass it as `cursor`. |

### Record fields

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Record identifier, prefixed `ul_` |
| `tenant_id` | string | Owning tenant |
| `started_at` | RFC 3339 timestamp | Start of the exchange |
| `finished_at` | RFC 3339 timestamp | End of the exchange |
| `latency_ms` | integer | Duration in milliseconds |
| `plane` | string | `llmplex`, `mcplex` or `a2aplex` |
| `provider` | string | Omitted when empty |
| `resource_id` | string | Omitted when empty. Identifier of the managed resource. |
| `operation` | string | Omitted when empty |
| `model_id` | string | Omitted when empty |
| `model_version` | string | Omitted when empty |
| `tool_name` | string | Omitted when empty. Set for `tools/call`. A `tools/list` exchange carries `operation` `tools/list` and no tool name. |
| `principal_id` | string | Omitted when empty |
| `actor_id` | string | Omitted when empty |
| `client_id` | string | Omitted when empty. Attribution only, not a security boundary. |
| `access_point_id` | string | Omitted unless the exchange entered through an access point |
| `caller_kind` | string | `agent`, `user` or `client`. Omitted unless entered through an access point. |
| `request_id` | string | Omitted when empty |
| `trace_id` | string | W3C trace identifier (32 hex), for correlation only. Set on every record AIPlex writes; continues the caller's `traceparent` when it was valid. Omitted on older records. |
| `span_id` | string | W3C span identifier (16 hex) of the exchange's span: the CLIENT span of a managed exchange, the SERVER span of a legacy reporting request, or the id on a harness's own log event. Omitted on older records. |
| `harness`, `harness_version`, `session_id` | string | Reported records from a harness: its name, version and own session id. Labels the sender chose; never identity. |
| `client_request_id` | string | Omitted when empty. A caller-chosen per-request id, kept as a correlation key. |
| `caller_span_id` | string | Omitted when empty. Managed records: the span id of the `traceparent` the caller sent. |
| `reported_attributes` | object of string | Omitted when empty. Reported records: a bounded subset of the sender's labels (`event`, `prompt_id`, `request_id`, `query_source`, `speed`, `effort`, `service_tier`, `attempt`, `entrypoint`, `terminal_type`). |
| `tags` | object of string | Omitted when empty. Experiment labels from the access point's `labels` and the allowlisted `X-AIPlex-Tags` header. |
| `quota_attempt_id` | string | Omitted when empty. Links the record to its quota attempt. |
| `streaming` | boolean | `true` for streamed responses |
| `session` | boolean | Omitted unless `true`. `true` for a WebSocket session covering the whole session. |
| `status` | string | `ok`, `error`, `denied` or `unknown` |
| `http_status` | integer | Omitted when zero |
| `error_class` | string | Omitted when empty. Stable class such as `unauthenticated`, `forbidden`, `rate_limited`, `upstream_error`, `timeout`, `client_disconnected`, `upstream_auth` (the provider refused the gateway's own credential), `upstream_quota_exhausted` (the provider's quota or balance is exhausted) or `model_not_found` (an admitted caller asked an access point for a model it does not offer). |
| `source` | string | `managed` or `reported` |
| `requests` | integer | Number of requests the record counts |
| `usage` | object or null | Token and unit counters. `null` when no counter is known. See Usage counters. |
| `usage_bound` | string | Omitted when the counters are final. `lower_bound`, `ambiguous` or `partial`. See Behaviour notes. |
| `batch` | boolean | Omitted unless `true`. Provider batch exchange. |
| `image_size` | string | Omitted when empty. Reported image dimension, for example `1024x1024`. |
| `image_quality` | string | Omitted when empty. Reported image quality, for example `high`. |
| `service_tier` | string | Omitted when empty. The processing tier the provider reported (`default`, `standard`, `flex`, `priority`), or `mixed`. |
| `server_tools` | object of integer | Omitted when empty. Provider-side tool requests the provider reported, by kind (`web_search`, `file_search`, `google_search`, ...). At most 16 kinds. |
| `cost` | object | Cost basis (`settled`, `estimated`, `reported`, `cached` or `unknown`) and amount: `basis`, and for a known cost `currency`, `amount`, `scale`, `amount_decimal`. An estimate adds `price_id`, `exact` (the unrounded amount in minor units as a fraction, present when `amount` was rounded up) and, for a cost computed after the record was written, `backfilled_at`. See Behaviour notes. |
| `provider_limits` | object | Omitted unless the provider reported rate-limit metadata on the exchange |

### Usage counters

`usage` holds the counters below. The six token counters are always present in the object, and each is `null` when unknown. The unit fields are omitted when unknown.

| Field | Meaning |
|---|---|
| `input_tokens` | Total input tokens. Cache reads and cache writes are included. |
| `output_tokens` | Output tokens |
| `cache_read_tokens` | Input tokens served from cache |
| `cache_write_tokens` | Input tokens written to cache |
| `reasoning_tokens` | Reasoning tokens, reported separately from `output_tokens` |
| `total_tokens` | Total tokens as reported by the provider |
| `input_text_tokens`, `input_audio_tokens`, `input_image_tokens`, `input_video_tokens` | Per-modality splits of `input_tokens`. They are subsets and are never added to it. |
| `output_text_tokens`, `output_audio_tokens`, `output_image_tokens` | Per-modality splits of `output_tokens`. Subsets, never added to it. |
| `audio_input_milliseconds` | Milliseconds of input audio billed by duration |
| `voice_session_milliseconds` | Milliseconds of a voice session |
| `video_output_milliseconds` | Milliseconds of generated video |
| `generated_images` | Count of generated images |
| `cache_write_5m_tokens`, `cache_write_1h_tokens`, `cache_write_30m_tokens`, `cache_write_12h_tokens` | The parts of `cache_write_tokens` written with each TTL (Anthropic and Bedrock report 5m and 1h; OpenAI Responses reports 30m and 12h). Subsets, never added to it. |
| `tool_use_prompt_tokens` | Gemini tool-use prompt tokens. Not part of `input_tokens`. |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Page returned |
| 400 | `INVALID_QUERY` | Unknown or repeated parameter, bad enumeration, bad timestamp, `until` not after `since`, `limit` outside 1 to 1000, malformed `cursor` |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 500 | `USAGE_LEDGER_ERROR` | Ledger read failed |
| 503 | `USAGE_LEDGER_UNAVAILABLE` | Ledger or tenant resolver not configured |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/usage/records?plane=llmplex&status=ok&limit=50" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
{
  "records": [
    {
      "id": "ul_0123456789abcdef0123456789abcdef",
      "tenant_id": "tenant-example",
      "started_at": "2026-11-10T09:00:00Z",
      "finished_at": "2026-11-10T09:00:01Z",
      "latency_ms": 1000,
      "plane": "llmplex",
      "model_id": "model-example",
      "streaming": false,
      "status": "ok",
      "source": "managed",
      "requests": 1,
      "usage": {
        "input_tokens": 120,
        "output_tokens": null,
        "cache_read_tokens": null,
        "cache_write_tokens": null,
        "reasoning_tokens": null,
        "total_tokens": null
      },
      "cost": {
        "basis": "unknown"
      }
    }
  ],
  "next_cursor": "opaque-cursor-from-previous-page"
}
```

## GET `/api/v1/usage/records/{id}`

Returns one record. The path parameter `id` is the record identifier. A record that exists but is not visible to the caller returns the same `404` as a missing record. The body is the record plus, when it has feedback, `feedback`: the latest entry (`id`, `record_id`, `by`, `created_at`, `score`, `label`, and `comment` only if comments are enabled).

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Record returned. Body is the record object described above. |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | Missing, in another tenant, or not visible to the caller |
| 500 | `USAGE_LEDGER_ERROR` | Ledger read failed |
| 503 | `USAGE_LEDGER_UNAVAILABLE` | Ledger not configured |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/usage/records/ul_0123456789abcdef0123456789abcdef" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET /api/v1/usage/summary

Aggregates the visible records that match the filters. It accepts every filter parameter of the list route except `limit` and `cursor`, and it adds the two aggregation parameters below.

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `group_by` | string | No | Comma-separated subset of `plane`, `provider`, `model_id`, `tool_name`, `actor_id`, `principal_id`, `client_id`, `status`, `access_point_id`, `caller_kind`, `source`, `error_class`, `usage_bound`, `batch`, `harness`, or `tag:<key>`. Each field at most once. Empty means one group for all matching records. |
| `merge_sources` | boolean | No | `true` adds managed and reported records into the same groups. Not allowed with a `source` filter. |
| `feedback` | boolean | No | `true` adds `feedback` totals to every group. |
| `bucket` | string | No | `hour` or `day`. Splits each group by the start of `started_at` in UTC. Omitted when empty. |

### Response

| Field | Type | Meaning |
|---|---|---|
| `group_by` | array of string | The grouping fields in request order |
| `bucket` | string | Omitted when no bucket was requested |
| `groups` | array of group | Ordered by bucket start, then by key values |
| `records_scanned` | integer | Records read by this summary |
| `truncated` | boolean | `true` when the scan reached 200000 records or the result reached 10000 groups. Totals then cover only the newest records scanned. |
| `source_split` | boolean | Omitted unless `true`. With no `source` filter and no `merge_sources`, `source` is added to `group_by` so managed and reported records are separate groups. |
| `excluded_correlated_reported` | integer | Omitted when zero. Reported records left out because they describe a request that has a managed record (same principal, and a shared `client_request_id`, trace and span, caller-chosen request id or propagated trace). |
| `notes` | array of string | Omitted when empty. How sources were treated, in plain words. |

### Group fields

| Field | Type | Meaning |
|---|---|---|
| `key` | object of string | Value of each `group_by` field. Empty string means the record did not carry the field. |
| `bucket_start` | RFC 3339 timestamp | Present only when `bucket` was requested |
| `records` | integer | Records in the group |
| `requests` | integer | Sum of `requests` |
| `errors` | integer | Records with status `error` |
| `denied` | integer | Records with status `denied` |
| `unknown_status` | integer | Records with status `unknown` |
| `tokens` | object of integer | Sums of each token counter over records that reported it. Keys are `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`, `reasoning_tokens`, `total_tokens`. A key is absent when no record reported that counter. |
| `tokens_unknown` | object of integer | Per token counter, the number of records that did not report it |
| `unknown_usage_records` | integer | Records with no known counter at all |
| `bounded_usage_records` | integer | Omitted when zero. Records whose `usage_bound` is set. Their counters are included in `tokens` but are not final. |
| `lower_bound_records` | integer | Omitted when zero |
| `ambiguous_usage_records` | integer | Omitted when zero |
| `partial_usage_records` | integer | Omitted when zero |
| `cost` | array of object | Exact totals per currency and basis. Each entry has `currency`, `basis`, `amount` (decimal string), `amount_decimal` (the same amount as an exact decimal string), `scale` and `records`. Estimated costs are added by their unrounded `exact` value and the total is rounded up once. |
| `unknown_cost_records` | integer | Records with no amount |
| `modality_tokens` | object of integer | Omitted when empty. Sums of the per-modality token splits and of the per-TTL cache writes over records that reported them. |
| `units` | object of integer | Omitted when empty. Sums of the unit fields. |
| `unit_records` | object of integer | Omitted when empty. Number of records that reported each unit. Records that did not report a unit are not counted as zero. |
| `batch_records` | integer | Omitted when zero. Records of provider batch exchanges. |
| `feedback` | object | Present with `feedback=true`: `records` (with feedback), `without_feedback`, `positive`, `negative`, `neutral` and `average_score` (a number, or `null` when no record has feedback). Records without feedback are not a neutral score. |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Summary returned |
| 400 | `INVALID_QUERY` | Unknown or repeated parameter (including `limit` and `cursor`), bad filter value, bad `group_by` field or repeated field, `bucket` not `hour` or `day` |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 500 | `USAGE_LEDGER_ERROR` | Ledger read failed |
| 503 | `USAGE_LEDGER_UNAVAILABLE` | Ledger not configured |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/usage/summary?group_by=model_id&bucket=day" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
{
  "group_by": ["model_id"],
  "bucket": "day",
  "groups": [
    {
      "key": {"model_id": "model-example"},
      "bucket_start": "2026-11-10T00:00:00Z",
      "records": 1,
      "requests": 1,
      "errors": 0,
      "denied": 0,
      "unknown_status": 0,
      "tokens": {"input_tokens": 120},
      "tokens_unknown": {
        "output_tokens": 1,
        "cache_read_tokens": 1,
        "cache_write_tokens": 1,
        "reasoning_tokens": 1,
        "total_tokens": 1
      },
      "unknown_usage_records": 0,
      "cost": [],
      "unknown_cost_records": 1
    }
  ],
  "records_scanned": 1,
  "truncated": false
}
```

## POST /api/v1/usage/records/{id}/feedback

Adds feedback to a record. The record's own principal or actor may, and so may an operator (`admin` or `deployer`). Anyone else gets the `404` of a missing record. Feedback is append-only; the newest entry is the record's current feedback.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `score` | number | Yes | From -1 (worst) to 1 (best) |
| `label` | string | No | `[a-z][a-z0-9_.-]{0,31}` |
| `comment` | string | No | Up to 500 characters. Accepted only when the operator set `AIPLEX_FEEDBACK_COMMENTS=true`; otherwise refused. |

Unknown fields are rejected. The author (`by`) is the verified principal, not a body field. The response is `201` with the stored entry (`id`, `tenant_id`, `record_id`, `by`, `created_at`, `score`, `label`, `comment`).

| Status | Code | When |
|---|---|---|
| 201 | none | Stored |
| 400 | `INVALID_FEEDBACK` | Missing or out-of-range `score`, bad `label`, unknown field, body not one JSON object |
| 400 | `COMMENTS_DISABLED` | A `comment` was sent while comments are off |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 404 | `NOT_FOUND` | The record is missing or not visible to the caller |
| 503 | `FEEDBACK_UNAVAILABLE` | Feedback is not configured |

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/usage/records/ul_0123456789abcdef0123456789abcdef/feedback" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" -H "Content-Type: application/json" \
  -d '{"score":1,"label":"helpful"}'
```

## GET /api/v1/usage/records/{id}/feedback

Returns `{"record_id": "...", "feedback": [...]}`, newest first (at most 100). Same visibility and errors as the POST.

## GET /api/v1/usage/config

Returns the usage settings the Console reads: `trace_url_template` (the operator's trace viewer link with `{trace_id}`, or an empty string), `feedback` (available), `feedback_comments` (accepted) and `tag_keys_allowed` (the keys `X-AIPlex-Tags` may set). It carries no secret.

## GET /api/v1/usage/prices

Returns every model-price resource of the tenant as a JSON array of resource envelopes.

### Resource envelope

| Field | Type | Meaning |
|---|---|---|
| `tenant_id` | string | Owning tenant |
| `kind` | string | Always `model-price` |
| `id` | string | Resource identifier. Must equal `spec.id`. |
| `revision` | integer | Revision. Used as the `If-Match` value. |
| `spec` | object | The price spec, described below |

### Price spec

The spec is decoded strictly: unknown fields are rejected.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | Yes | Must match the resource `id` |
| `model_id` | string | Yes | Model the price applies to |
| `provider` | string | No | When set, the price matches only records with this provider |
| `currency` | string | Yes | Three uppercase ASCII letters |
| `scale` | integer | Yes | Decimal places of the money amounts, from 0 to 18 |
| `meters` | object | Yes | At least one meter. Each value is `{"numerator": n, "denominator": d}` with `n` at least 0 and `d` greater than 0. A numerator of 0 declares a free meter. |
| `effective_from` | RFC 3339 timestamp | No | Matches records that started at or after this time |
| `effective_until` | RFC 3339 timestamp | No | Matches records that started before this time. Must be after `effective_from`. |
| `batch` | boolean | No | `true` matches only batch exchanges, `false` only non-batch exchanges |
| `image_size` | string | No | Matches the record's `image_size` |
| `image_quality` | string | No | Matches the record's `image_quality` |
| `service_tier` | string | No | Matches the record's `service_tier` (`flex`, `priority`, ...). A price without it covers standard-tier exchanges only (no tier reported, `default`, `standard` or `batch`). |
| `tiers` | array | No | Context-length overrides, at most 8: each `{"above_input_tokens": n, "meters": {...}}` with `n` positive and strictly increasing. When the record's `input_tokens` (total input, cache included) is greater than `n`, the whole request is priced at that tier's `meters`, which must name exactly the base meters. An unknown `input_tokens` makes the cost unknown. |
| `source` | object | No | Provenance of a price imported from the bundled catalogue: `catalog` (required), `commit`, `retrieved_at`, `source_url`. It is a label only; it never changes matching or cost. |

Allowed meter names are `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`, `reasoning_tokens`, `requests`, the modality splits `input_text_tokens`, `input_audio_tokens`, `input_image_tokens`, `input_video_tokens`, `output_text_tokens`, `output_audio_tokens`, `output_image_tokens`, the unit meters `audio_input_milliseconds`, `voice_session_milliseconds`, `video_output_milliseconds`, `generated_images`, `tool_use_prompt_tokens`, the per-TTL cache-write meters `cache_write_5m_tokens`, `cache_write_1h_tokens`, `cache_write_30m_tokens` and `cache_write_12h_tokens` (each requires `cache_write_tokens` to be priced too), and per-call server-tool meters named `server_tool.<kind>` with a lowercase kind such as `server_tool.web_search`. Any other name is rejected.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/usage/prices" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## POST /api/v1/usage/backfill

Prices records whose cost was `unknown` when they were written, using the price effective at each record's own `started_at` (a price created later with a window that covers the record applies; a price outside its window never does). Requires the `admin` role. It never changes a `settled` or `estimated` cost, writes only while a record's cost is still unknown, and is idempotent. A record qualifies when its usage is known and final (no `usage_bound`) and one price covers it exactly. Its cost becomes `estimated` with `price_id` and `backfilled_at`; its usage and other fields are untouched.

### Request body

All fields are optional. Unknown fields are rejected.

| Field | Type | Meaning |
|---|---|---|
| `since` | RFC 3339 timestamp | Inclusive lower bound on `started_at` |
| `until` | RFC 3339 timestamp | Exclusive upper bound. Must be after `since`. |
| `model_id` | string | Only this model |
| `provider` | string | Only this provider |
| `dry_run` | boolean | Report counts and totals and write nothing |
| `limit` | integer | Most records scanned by this call, 1 to 1000. Default 500. |
| `cursor` | string | The previous response's `next_cursor` |

### Response

| Field | Type | Meaning |
|---|---|---|
| `scanned` | integer | Unknown-cost records examined |
| `eligible` | integer | Records a price covers. On a dry run, what would be written. |
| `updated` | integer | Records written. `0` on a dry run. |
| `skipped` | object | `usage_not_final` (no usage, or a `usage_bound`), `no_price` (no single price covers the record, or a priced quantity is unknown), `changed` (another writer gave the record a cost first). Each omitted when zero. |
| `totals` | array | Per currency, the eligible records' costs: `currency`, `basis`, `amount`, `amount_decimal`, `scale`, `records`, and `exact` when `amount` was rounded up. Totals add exact values and round up once. |
| `next_cursor` | string | Present when more records remain. Call again with it. |
| `dry_run` | boolean | Echoes the request |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Batch processed |
| 400 | `INVALID_REQUEST` | Malformed body, unknown field, `limit` out of range, malformed `cursor`, `until` not after `since` |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks the `admin` role |
| 501 | `NOT_IMPLEMENTED` | The ledger store cannot back-estimate |
| 503 | `PRICES_UNAVAILABLE` | Model prices could not be read |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/usage/backfill" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" -H "Content-Type: application/json" \
  -d '{"model_id":"model-example","dry_run":true}'
```

```json
{
  "scanned": 4,
  "eligible": 1,
  "updated": 0,
  "skipped": {"no_price": 3},
  "totals": [{"currency": "USD", "basis": "estimated", "amount": "8", "amount_decimal": "0.000008", "scale": 6, "records": 1, "exact": "31/4"}],
  "dry_run": true
}
```

## GET `/api/v1/usage/prices/{id}`

Returns one resource envelope. The response carries an `ETag` header whose value is the revision in quotes.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Envelope returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No price with this identifier |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/usage/prices/price-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## PUT `/api/v1/usage/prices/{id}`

Creates or replaces a price. The body is the price spec, at most 1 MiB. The `If-Match` header carries the revision you are replacing.

- To create a price, send no `If-Match`. The write succeeds only when no live price exists with that identifier. A deleted identifier can be created again.
- To replace a price, send `If-Match` with its current revision. Quotes around the value are accepted. A stale value returns `409`.
- A malformed `If-Match` returns `400 INVALID_REVISION`.

Successful responses return the envelope with the new revision and an `ETag` header.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Price stored. Body is the envelope. |
| 400 | `INVALID_REVISION` | `If-Match` is not a revision number |
| 400 | `INVALID_SPEC` | Body is not valid, unknown fields, `id` does not match, currency or scale invalid, unknown meter, negative numerator, non-positive denominator, or body over 1 MiB |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks the `admin` role outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision, or create was sent while a live price exists |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS -X PUT "$AIPLEX_URL/api/v1/usage/prices/price-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -H 'If-Match: "3"' \
  -d '{"id":"price-example","model_id":"model-example","currency":"USD","scale":6,"meters":{"input_tokens":{"numerator":1,"denominator":1}}}'
```

```json
{
  "tenant_id": "tenant-example",
  "kind": "model-price",
  "id": "price-example",
  "revision": 4,
  "spec": {
    "id": "price-example",
    "model_id": "model-example",
    "currency": "USD",
    "scale": 6,
    "meters": {"input_tokens": {"numerator": 1, "denominator": 1}}
  }
}
```

## DELETE `/api/v1/usage/prices/{id}`

Deletes a price. `If-Match` is required and must carry the current revision. Without it the request fails with `428`. On success the response has no body.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 204 | none | Price deleted |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks the `admin` role outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No live price with this identifier |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `REVISION_REQUIRED` | `If-Match` missing, zero, or not a revision number |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS -X DELETE "$AIPLEX_URL/api/v1/usage/prices/price-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H 'If-Match: "4"'
```

## Behaviour notes

- **Unknown is null, never zero.** A `null` counter means the provider did not report it. A `0` is a reported zero. Summaries count records that did not report a counter in `tokens_unknown`, not as zero.
- **Usage can be absent.** `usage` is `null` when no counter is known for the exchange.
- **Cost basis.** `cost.basis` is one of:
  - `settled`: the amount a quota settlement charged for the record's `quota_attempt_id`. It is exact.
  - `estimated`: computed from the single matching model price. The sum of all meters is rounded up once.
  - `reported`: a cost the harness itself reported in its telemetry (Claude Code's `cost_usd_micros` or `cost_usd`), in `USD`. It is the sender's estimate, not an invoice and not an AIPlex price, and totals keep it apart from the other bases.
  - `unknown`: neither a settlement nor a matching price exists, or a priced meter's quantity is unknown. Such a cost has no `currency`, `amount` or `scale`.
- **Amounts are fixed-point.** `cost.amount` is a decimal integer string of minor units, with `cost.scale` decimal places of the currency. An amount of `"1500"` at scale `6` is 0.001500 of the currency unit. The amount is always a string so that JSON readers do not lose precision. A priced record also carries `cost.amount_decimal`, the same value written as an exact decimal string (`"0.001500"` in the example above). Read `amount_decimal` when a decimal is needed and `amount` and `scale` when exact integer arithmetic is needed. The `cost` array in a summary uses the same convention and rescales amounts of different scales to the largest scale without loss.
- **No built-in prices.** Without a matching price resource, estimated cost is unknown. The API never invents a price.
- **Price matching.** When several prices match a record, the one with the most selectors wins (`provider`, `batch`, `image_size`, `image_quality`). A tie yields no match, and the cost is unknown.
- **Usage bounds.** `usage_bound` is omitted when the counters are final.
  - `lower_bound`: the exchange ended before final usage was observed. The counters are what was seen before it ended. This includes WebSocket sessions with no terminal observation.
  - `ambiguous`: a resumed session whose counter composition is not confirmed.
  - `partial`: non-token meters were observed that the record cannot represent.
- **Session records.** `session: true` covers a whole WebSocket session. Its usage is final only with a terminal observation.
- **Status.** `denied` is set for HTTP 401, 403, 407 and 429 responses. Other 4xx and 5xx responses are `error`. A stream that ended without completing is `unknown` with error class `stream_aborted` or `client_disconnected`.
- **Ordering and paging.** Records are ordered newest first by `(started_at, id)`. `next_cursor` is an opaque base64url token. Pass it unchanged as `cursor` and keep the other filters the same. The default page size is 100 and the maximum is 1000.
- **Visibility.** Non-admin callers see only records whose `principal_id` or `actor_id` equals their own principal. Filters cannot widen that view.
- **Content.** Records never contain prompts, completions, tool arguments, tool results or credentials. Identifiers are bounded to 512 characters.
- **Reported records.** `source: "reported"` records are mirrored from legacy usage reports, or read from a harness's own telemetry sent to [`/otlp`](./otlp.md). A legacy report's float cost is never copied; its cost is estimated from a price or left unknown. A harness's own cost has basis `reported`; without one the cost is estimated from a price or left unknown.
- **Sources in summaries.** A managed and a reported record can describe the same request, so a summary with no `source` filter keeps them in separate groups and leaves out reported records that share an explicit key with a managed one (`excluded_correlated_reported`). It never matches on model, time or token counts. `merge_sources=true` adds them together and says it cannot rule out double counting.
- **Retention.** The Firestore-backed ledger is configured with a 90-day retention (`usageledger.DefaultRetention`). Older records are not returned.
- **Writes.** The usage records themselves have no write route on this API. Records are created by the gateways.

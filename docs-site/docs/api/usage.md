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
| GET | `/api/v1/usage/prices` | List the tenant's model-price resources | Verified identity and tenant |
| GET | `/api/v1/usage/prices/{id}` | Read one model-price resource | Verified identity and tenant |
| PUT | `/api/v1/usage/prices/{id}` | Create or update a model-price resource | `admin` role (outside development mode) |
| DELETE | `/api/v1/usage/prices/{id}` | Delete a model-price resource | `admin` role (outside development mode) |

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
| `source` | string | No | `managed` (exchanges through AIPlex) or `reported` (mirrored from a legacy usage report) |
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
| `trace_id` | string | Omitted when empty. W3C trace identifier, for correlation only. |
| `quota_attempt_id` | string | Omitted when empty. Links the record to its quota attempt. |
| `streaming` | boolean | `true` for streamed responses |
| `session` | boolean | Omitted unless `true`. `true` for a WebSocket session covering the whole session. |
| `status` | string | `ok`, `error`, `denied` or `unknown` |
| `http_status` | integer | Omitted when zero |
| `error_class` | string | Omitted when empty. Stable class such as `unauthenticated`, `forbidden`, `rate_limited`, `upstream_error`, `timeout` or `client_disconnected`. |
| `source` | string | `managed` or `reported` |
| `requests` | integer | Number of requests the record counts |
| `usage` | object or null | Token and unit counters. `null` when no counter is known. See Usage counters. |
| `usage_bound` | string | Omitted when the counters are final. `lower_bound`, `ambiguous` or `partial`. See Behaviour notes. |
| `batch` | boolean | Omitted unless `true`. Provider batch exchange. |
| `image_size` | string | Omitted when empty. Reported image dimension, for example `1024x1024`. |
| `image_quality` | string | Omitted when empty. Reported image quality, for example `high`. |
| `cost` | object | Cost basis and amount. See Behaviour notes. |
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

Returns one record. The path parameter `id` is the record identifier. A record that exists but is not visible to the caller returns the same `404` as a missing record.

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
| `group_by` | string | No | Comma-separated subset of `plane`, `provider`, `model_id`, `tool_name`, `actor_id`, `principal_id`, `client_id`, `status`, `access_point_id`, `caller_kind`. Each field at most once. Empty means one group for all matching records. |
| `bucket` | string | No | `hour` or `day`. Splits each group by the start of `started_at` in UTC. Omitted when empty. |

### Response

| Field | Type | Meaning |
|---|---|---|
| `group_by` | array of string | The grouping fields in request order |
| `bucket` | string | Omitted when no bucket was requested |
| `groups` | array of group | Ordered by bucket start, then by key values |
| `records_scanned` | integer | Records read by this summary |
| `truncated` | boolean | `true` when the scan reached 200000 records or the result reached 10000 groups. Totals then cover only the newest records scanned. |

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
| `cost` | array of object | Exact totals per currency and basis. Each entry has `currency`, `basis`, `amount` (decimal string), `amount_decimal` (the same amount as an exact decimal string), `scale` and `records`. |
| `unknown_cost_records` | integer | Records with no amount |
| `modality_tokens` | object of integer | Omitted when empty. Sums of the per-modality token splits over records that reported them. |
| `units` | object of integer | Omitted when empty. Sums of the unit fields. |
| `unit_records` | object of integer | Omitted when empty. Number of records that reported each unit. Records that did not report a unit are not counted as zero. |
| `batch_records` | integer | Omitted when zero. Records of provider batch exchanges. |

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
| `source` | object | No | Provenance of a price imported from the bundled catalogue: `catalog` (required), `commit`, `retrieved_at`, `source_url`. It is a label only; it never changes matching or cost. |

Allowed meter names are `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`, `reasoning_tokens`, `requests`, the modality splits `input_text_tokens`, `input_audio_tokens`, `input_image_tokens`, `input_video_tokens`, `output_text_tokens`, `output_audio_tokens`, `output_image_tokens`, and the unit meters `audio_input_milliseconds`, `voice_session_milliseconds`, `video_output_milliseconds`, `generated_images`. Any other name is rejected.

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
- **Reported records.** `source: "reported"` records are mirrored from legacy usage reports. Their float cost is never copied. Their cost is estimated from a price or left unknown.
- **Retention.** The Firestore-backed ledger is configured with a 90-day retention (`usageledger.DefaultRetention`). Older records are not returned.
- **Writes.** The usage records themselves have no write route on this API. Records are created by the gateways.

# Usage ledger

The usage ledger keeps one append-only, tenant-scoped record for each managed
exchange on the LLMPlex, MCPlex and A2APlex paths, plus records reported to
`POST /api/v1/llm/usage`. It answers questions such as which agent used which
model through which client, how many tokens the provider reported, and what the
exchange cost when a price or a quota settlement establishes a cost. Records hold
identifiers, status, timing and counters. They never hold prompts, completions,
tool arguments, results or credentials.

Quota admission and settlement are documented in [managed QuotaPlex execution](quotaplex-integration.md).
Which provider usage is exact, advisory or not observed is stated in the generated
[usage-format coverage matrix](reference/usage-formats/coverage.md). That matrix is the
authority. This page describes the ledger's fields and routes, not what each provider reports.

## Routes

All routes are under `/api/v1/usage`. They require a verified tenant. Callers with
the admin role read every record in their tenant. Other callers read only records
where they are the principal or the actor. Writes to `/api/v1/usage/` require the
admin role. Deployers are not admins: they read only their own records, like any other
non-admin caller. The overview roll-ups under `/api/v1/overview` are admin-only too. A
deployer gets the overview's configuration view with usage withheld (see
[access points](access-points.md#the-single-pane)).

| Route | Purpose |
| --- | --- |
| `GET /api/v1/usage/records` | List records, newest first. Filters: `request_id`, `client_request_id`, `trace_id` ([find the record of a call](#find-the-record-of-a-call)), `plane`, `provider`, `model_id`, `tool_name`, `actor_id`, `principal_id`, `client_id`, `access_point_id`, `caller_kind`, `budget_id`, `status`, `source`, `error_class`, `usage_bound`, `batch`, `hook_decision`, `harness`, `tag.<key>`, `cost_basis`, `since`, `until`. Pagination: `limit` (1 to 1000), `cursor`. |
| `GET /api/v1/usage/records/{id}` | One record, with its latest `feedback` when it has any. |
| `GET /api/v1/usage/records/{id}/feedback` | The record's feedback history, newest first. |
| `POST /api/v1/usage/records/{id}/feedback` | Add feedback to a record: the record's own principal or actor, or an operator (admin or deployer). See [feedback](#feedback). |
| `GET /api/v1/usage/summary` | Totals. Accepts the same filters, except `limit` and `cursor`, plus `group_by`, `bucket=hour\|day`, `merge_sources=true` and `feedback=true`. Returns requests, errors, token sums, records with unknown usage and cost per currency. Managed and reported records are separate groups: see [sources in summaries](#sources-in-summaries). |
| `GET /api/v1/usage/config` | The deployment's usage settings the Console reads: the trace viewer link template, whether feedback and feedback comments are on, and the tag keys a caller may set. No secrets. |
| `GET /api/v1/usage/prices` | List model-price resources. |
| `GET /api/v1/usage/prices/{id}` | One price with its revision. |
| `PUT /api/v1/usage/prices/{id}` | Create or replace a price. Send the current revision in `If-Match`; `0` creates. |
| `DELETE /api/v1/usage/prices/{id}` | Delete a price. Send the current revision in `If-Match`. |
| `POST /api/v1/usage/backfill` | Price records whose cost was unknown when written (admin). See [back-estimation](#back-estimation). |

Unknown query parameters, repeated parameters, and unknown `group_by` fields are rejected with
`400 INVALID_QUERY`. The valid `group_by` fields are `plane`, `provider`,
`model_id`, `tool_name`, `actor_id`, `principal_id`, `client_id`,
`access_point_id`, `caller_kind`, `status`, `source`, `error_class`, `usage_bound`,
`batch`, `hook_decision` and `harness`, or `tag:<key>` to group by a record [tag](#tags). A `group_by`
field may appear once.

Filter values are checked before the query runs:

| Parameter | Accepted values |
| --- | --- |
| `plane` | `llmplex`, `mcplex`, `a2aplex` |
| `caller_kind` | `agent`, `user`, `client` |
| `status` | `ok`, `error`, `denied`, `unknown` |
| `source` | `managed`, `reported` |
| `harness` | Any text up to 512 characters, for example `claude-code`. Matches reported records from that harness. |
| `tag.<key>` | A tag value, for example `tag.experiment=arm-a`. Repeat with other keys to require several. Keys are `[a-z][a-z0-9_.-]{0,31}`. |
| `usage_bound` | `lower_bound`, `ambiguous`, `partial`, `none` |
| `batch` | `true` or `false`. A record with `batch: true` matches `true`. |
| `error_class` | Any text up to 512 characters. Matches records with exactly that `error_class`. |
| `hook_decision` | `allow`, `deny`, `transform`, `error`, `skipped`, or `none` (no [request hook](request-hooks.md) ran). Matches `hooks.decision`. |
| `cost_basis` | `settled`, `estimated`, `reported`, `cached` or `unknown`. Matches `cost.basis`. |
| `since`, `until` | RFC 3339 timestamps. `until` must be after `since`. |

`usage_bound=none` matches records with no `usage_bound`, meaning counters that are final or
unknown. Any other value is matched exactly.

### Find the record of a call

Three filters select the records of one call by an exact match, on `GET /api/v1/usage/records`
and `GET /api/v1/usage/summary`:

| Parameter | Matches | Accepted values |
| --- | --- | --- |
| `client_request_id` | The `X-Client-Request-Id` the caller sent (a Branchyard turn sends the id of its own usage record). | 1 to 128 characters of `A-Z a-z 0-9 . _ : -` |
| `request_id` | AIPlex's request id: the `X-Aiplex-Request-Id` response header of an [access point](access-points.md#response-headers) call. | 1 to 128 characters of `A-Z a-z 0-9 . _ : -` |
| `trace_id` | The W3C trace id of the exchange. | 32 lower-case hexadecimal characters, not all zero |

A malformed value, a repeated parameter, a parameter that is present but empty, or a query string that cannot
be parsed is `400 INVALID_QUERY` (an empty id must never read as "no filter").
The visibility rule is unchanged: an admin sees the tenant's records, anyone else only records where they
are the principal or the actor, so another caller's `client_request_id` gives an empty list. CLI:
`aiplex usage records --client-request-id ID`, `--request-id ID`, `--trace-id ID`.
With one of these three the CLI follows empty pages that carry a cursor, up to 50 pages. Exit 0 means the
answer is complete: a record was found, or the scan reached the end of the ledger and none matches (it
prints "No usage records match."). If the scan stops at the 50-page bound it exits 1, prints how far it got
(the oldest time scanned) and the `--cursor` to continue with, and `-o json` still prints the page and its
`next_cursor` on stdout.

`request_id` and `trace_id` can match several records (a request id the caller chose twice, several
exchanges of one trace). The ledger stores a
record's fields in one JSON document, so these filters are applied after a read in the Firestore store
as every other filter is, so the code needs and adds no index (not checked against a real Firestore). On a tenant with many records the search reads
newest first and one call stops after 5,000 documents and returns a `next_cursor` even when nothing
matched yet: follow the cursor, or pass `since` (and `until`) to bound the search. The in-memory store
filters in process.

Not tested: the Firestore emulator was not run for these filters (the in-process wire fake of the Firestore
client was); a deployment with several replicas; and no measurement of the scan cost on a large tenant was
made.

## Record fields

| Field | Meaning |
| --- | --- |
| `id` | Record identifier, prefixed `ul_`. |
| `plane` | `llmplex`, `mcplex` or `a2aplex`. |
| `provider`, `model_id`, `model_version` | Set for model exchanges. Tool calls use provider `mcp`. `model_id` is the model AIPlex dispatched; `model_version` is the name the provider reported in its response, as sent (a provider may report an alias, a dated snapshot or another name), so it can differ from `model_id`. Prices match `model_id`. |
| `resource_id` | Surface, tool connection or agent connection identifier. |
| `tool_name` | Tool name for `tools/call` exchanges. A `tools/list` exchange has `operation: tools/list` and no tool name, so it counts as a request but not as a tool call in the dashboard. |
| `principal_id`, `actor_id` | Verified principal and delegated actor, when present. |
| `client_id` | The verified token's authorized party (`azp`). A public OAuth client ID is attribution, not a security boundary. |
| `access_point_id`, `caller_kind` | Set only when the exchange entered through an [access point](access-points.md). `caller_kind` is `agent`, `user` or `client`. |
| `budget_ids` | The [tenant budgets](budgets.md) the exchange drew from (its own and every ancestor), sorted. Set only when QuotaPlex dispatched the attempt: a request refused or released before dispatch has none. `group_by=budget` counts a record in every budget it drew from, so budget groups overlap. Reported (`source=reported`) records never have budget ids; a summary by budget counts managed records only and is not source-split. |
| `status` | `ok`, `error`, `denied` (HTTP 401, 403, 407 or 429), or `unknown` (the response began but did not complete, or the client disconnected). |
| `source` | `managed` for records captured on AIPlex paths. `reported` for records the caller or its client reported: `POST /api/v1/llm/usage`, or a harness's OpenTelemetry sent to [`/otlp`](#harness-telemetry-reported-records). |
| `harness`, `harness_version`, `session_id` | Reported records from a harness: its name (`claude-code`, `codex`, `gemini-cli`), its version and its own session id. These are labels the sender chose, not identity. |
| `client_request_id` | The caller's per-request id (`X-Client-Request-Id`, or the harness's `client_request_id`), kept only as a correlation key. |
| `caller_span_id` | Managed records: the span id in the `traceparent` the caller sent. A correlation key only. |
| `reported_attributes` | Reported records: a bounded, documented subset of the sender's labels (see [harness telemetry](#harness-telemetry-reported-records)). Never identity, prompts, tool input or error text. |
| `tags` | Experiment labels (see [tags](#tags)). |
| `http_status` | The HTTP status the caller received. Omitted when the exchange had none. |
| `error_class` | The reason for a non-`ok` status. See [error classes](#error-classes). |
| `operation` | The native operation ID, for managed model exchanges. |
| `started_at`, `finished_at`, `latency_ms` | Timing of the exchange. |
| `streaming` | `true` when the response was a stream, or a WebSocket relay. |
| `requests` | Requests counted for the record. |
| `usage` | Provider-normalized counters, each nullable: `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`, `reasoning_tokens`, `total_tokens`. Optional [unit fields](#units-and-modality-splits) appear only when reported. The whole object is `null` when no counter or unit is known. |
| `batch` | `true` when the exchange belongs to a provider batch job (its creation, retrieval, or the results fetch that settled it). Omitted otherwise. |
| `image_size`, `image_quality` | Billing dimensions the provider reported for generated images (for example `1024x1024`, `high`); `mixed` when one exchange reported different values. Omitted when not reported. |
| `service_tier` | The processing tier the provider reported for the exchange: OpenAI `service_tier` (`default`, `flex`, `priority`), Anthropic `usage.service_tier`, Gemini `usageMetadata.serviceTier` (`standard`, `flex`, `priority`; `unspecified` is treated as not reported). `mixed` when parts of one exchange disagreed. Omitted when not reported. A price selects on it (see [service tiers](#service-tiers)). |
| `server_tools` | Provider-side tool requests the provider reported, as a map of tool kind to request count, for example `{"web_search": 2}`. At most 16 kinds. Omitted when the provider reported none. See [server tools](#server-tools). |
| `usage_bound` | Present only when `usage` is not final. One of `lower_bound`, `ambiguous` or `partial`. See [bounded usage](#bounded-usage). |
| `cost` | `basis`. When the basis is not `unknown`, also `currency`, `amount` (a decimal integer in minor units at `scale`), `scale` and `amount_decimal`, the same amount written as an exact decimal string (for example `0.000006` for `amount` `6` at scale `6`). An estimate also carries `price_id` (the price used) and, when `amount` was rounded up, `exact` (the unrounded amount in minor units as a fraction, for example `37/4`). A cost computed after the record was written carries `backfilled_at` ([back-estimation](#back-estimation)). |
| `provider_limits` | The provider rate-limit observation from the native response, when one was reported. See [model providers](model-providers.md). |
| `hooks` | Present when a [request hook](request-hooks.md) ran on the exchange. `decision` is the most significant decision of all runs (`deny`, then `error`, then `transform`, then `allow`, then `skipped`); `uninspected_parts` counts images, audio, files and unknown blocks that were present but not inspected; `fail_open_used` is `true` when a `fail_open` hook failed and the exchange continued without it. `results` lists each hook run: `id` (`policy/hook`), `type`, `phase` (`request` or `response`), `decision`, `rules` (rule identifiers with match `count`; for a denial the denying rule comes first), `duration_ms` and `fail_open_used`. It never holds content or the matched text. |
| `attempts`, `backend`, `routing_strategy` | Set for an exchange of a routed access-point model ([reliability and routing](reliability-and-routing.md)). `attempts` lists every attempt in order: `surface_ref`, `native_model`, `outcome` (`ok`, `connect_error`, `rate_limited`, `overloaded`, `unavailable`, `server_error`, `bad_gateway`, `gateway_timeout`, `request_timeout`, `upstream_error`, `transport_error`, `capacity_refused`, `quota_denied`, `not_granted`, `breaker_open`, `refused`, `body_mismatch`, `backend_unavailable`, `deadline_exceeded`, `cache_hit`), `upstream_status`, `retry_after_ms`, `duration_ms`, `breaker_state` and `reason` (why the backend was in the order). `backend` is the `{surface_ref, native_model}` that served. One record covers the whole exchange; its `provider`, `resource_id`, `model_id`, usage and cost are the serving backend's. Usage of failed attempts that a provider billed is not known to AIPlex and is in no record. A caller who is not an admin reading its own records sees backends numbered (`backend-1`, ...) instead of named. |
| `cache` | Set when the access-point model has a response cache: `status` (`hit`, `miss`, `bypass`), `reason` (why it bypassed or was not stored) and `scope`. |
| `cached_response_usage` | On a cache hit, the usage of the original exchange whose answer was replayed. It is not provider usage of this exchange: on a hit `usage` is all zero and the cost is `{"basis": "cached", "amount": "0"}`. Summaries never add it to token totals. |
| `tool_policy_rule` | The identifier of the [policy rule](tool-policy-rules.md) that refused a tool call (`error_class` `tool_policy_denied` or `tool_policy_error`). Omitted otherwise and for the default deny. Never the arguments. |
| `quota_attempt_id` | QuotaPlex attempt, when quota admitted the exchange. |
| `session` | `true` for a WebSocket session. One record covers the whole session, and its usage is final only with a terminal observation. Omitted otherwise. |
| `request_id` | The request's correlation identifier (`X-Request-Id`). |
| `trace_id`, `span_id` | W3C trace and span identifiers of the exchange's span: for a managed record, the CLIENT span that covers the exchange (for a routed access-point model, the INTERNAL `aiplex.route` span that is the parent of every attempt's CLIENT span; see [observability](observability.md)); for a `reported` record, the SERVER span of the request that reported it. `trace_id` continues the caller's `traceparent` when it sent a valid one; otherwise AIPlex starts a trace. Both are set on every record whether or not telemetry export is configured, so request logs (`trace_id`), traces and records correlate. Records written before this field existed have no `span_id`. |

## Unknown is not zero

A counter that the provider did not report is `null`. A reported zero is `0`.
Summaries count records with unknown usage separately, so an absent counter never
lowers a total silently.

Cost has five bases:

| Basis | Source |
| --- | --- |
| `settled` | The QuotaPlex attempt for the exchange settled with a money meter in one currency. |
| `estimated` | A `model-price` resource matched the record. The amount is the exact rational sum of the priced quantities, rounded up once to the price's scale ([rounding](#rounding)). With `backfilled_at`, the record was unknown when written and a price was applied later ([back-estimation](#back-estimation)). |
| `reported` | A harness reported the cost itself (Claude Code's `cost_usd`). It is the sender's estimate, never a provider invoice and never an AIPlex price. Totals keep it apart from `settled` and `estimated`. |
| `cached` | The answer came from the AIPlex [response cache](reliability-and-routing.md#response-cache): no provider ran. The amount is `"0"` with no currency. Summaries count these records in `cached_response_records`, not in `cost` and not in `unknown_cost_records`. |
| `unknown` | None of the above applies. The record carries no currency, amount or scale. |

A record gets `unknown` cost when:

- a known non-zero quantity has no matching meter in the price,
- a priced meter's quantity is unknown, or
- no single price is the best match. Two or more matching prices tie on the number of
  selectors (see [prices](#prices)),
- the price has context-length tiers and the record's `input_tokens` is unknown,
- the record reports a quantity the price separates but does not rate: a non-text modality
  count when the price rates another modality of that direction, one-hour or 12-hour cache
  writes, Gemini tool-use prompt tokens, or a server-tool call count (see below),
- the record's per-TTL cache writes add up to more than its `cache_write_tokens`, or
- the record's service tier is not one the price covers.

Prices are never invented. The binary carries a third-party list-price catalogue, but nothing is priced from it automatically: an operator imports the entries they choose ([price catalogue](#price-catalogue)).

## Bounded usage

`usage_bound` qualifies `usage` for an exchange that ended without final usage evidence.
It is set in two places:

| Value | Meaning | Where it is set |
| --- | --- | --- |
| `lower_bound` | The counters are what was observed before the exchange ended. The real total may be higher. | A WebSocket session ended without final evidence: the socket dropped without a clean close, a started response had no usage, activity followed the last snapshot, or `MaxStreamDuration` was reached. |
| `ambiguous` | The counters come from a resumed Gemini Live session, whose counter composition across resumption is not established. | A resumed Live session. |
| `partial` | Units were reported that the ledger has no field for, so the counters do not describe the whole exchange. Units the ledger holds (see [units](#units-and-modality-splits)) never make a record partial. | Realtime input transcription (`transcription_*` counters, billed to a different model than the record's) and any other `*_milliseconds` or `*_images` counter without a ledger field. |

A bounded record's `status` is `unknown`, and its `error_class` is `session_usage_<bound>`
when the cause is a session. A record with no `usage_bound` has final counters, or `usage`
is `null` when nothing was observed.

**Summaries add bounded records into totals.** `GET /api/v1/usage/summary` sums every
record's counters, including records with a `usage_bound`. The summary also reports
`bounded_usage_records`, `lower_bound_records`, `ambiguous_usage_records` and
`partial_usage_records`. Each is omitted when zero. `usage_bound` is a filter and a
`group_by` field. A record
counts toward `unknown_usage_records` only when its `usage` is `null`. A bounded record with
counters is therefore counted in the token totals as observed, not as unknown. To exclude
lower bounds from a total, filter with `usage_bound=none`, or group by
`usage_bound` (CLI: `--by bound`). `GET /api/v1/usage/records` also returns the `usage_bound` field on each
record.

Cost is not estimated from a bounded record's counters unless a settlement or price
establishes it, as described above.

## Units and modality splits

These `usage` fields are held beside the token counters, never added into
them, and are omitted when the provider did not report them (unknown, not zero):

| Field | Unit | Meaning |
| --- | --- | --- |
| `input_text_tokens`, `input_audio_tokens`, `input_image_tokens`, `input_video_tokens` | tokens | Per-modality subsets of `input_tokens` (OpenAI `*_tokens_details`, Gemini `promptTokensDetails`). |
| `output_text_tokens`, `output_audio_tokens`, `output_image_tokens` | tokens | Per-modality subsets of `output_tokens` (OpenAI `*_tokens_details`, Gemini `candidatesTokensDetails` / `responseTokensDetails`). |
| `audio_input_milliseconds` | milliseconds | Input audio billed by duration (OpenAI transcription `usage.type: "duration"`, seconds rounded up to whole milliseconds). |
| `voice_session_milliseconds` | milliseconds | Voice duration of a seconds-metered session. |
| `video_output_milliseconds` | milliseconds | Generated video duration, when a provider reports it. |
| `generated_images` | images | Images returned by an image generation or edit. |
| `cache_write_5m_tokens`, `cache_write_1h_tokens` | tokens | The parts of `cache_write_tokens` written with the five-minute and one-hour TTL (Anthropic `usage.cache_creation.ephemeral_5m_input_tokens` / `ephemeral_1h_input_tokens`, Bedrock `cacheDetails` TTL `5m` / `1h`). Subsets of `cache_write_tokens` that sum to it. |
| `cache_write_30m_tokens`, `cache_write_12h_tokens` | tokens | The parts of `cache_write_tokens` written with the 30-minute and 12-hour TTL (OpenAI Responses `input_tokens_details.cache_write_tokens_details.ttl_30m_tokens` / `ttl_12h_tokens`). Subsets of `cache_write_tokens` that sum to it. |
| `tool_response_bytes` | bytes | A managed tool call's response body as the gateway read it, decompressed, for a complete response only (an HTTP 200 carrying a JSON-RPC error is complete). Unknown when the call failed before a response, the connection was reset, the response exceeded `max_response_bytes`, the call timed out or the upstream answered an error status. Bytes read before a failure are never recorded as the total. |
| `tool_duration_milliseconds` | milliseconds | A managed tool call's duration on the gateway's monotonic clock, from the first dispatch to the end of the response body, rounded up to whole milliseconds (at least 1). A retried call's duration covers both attempts and the pause between them. Known only with `tool_response_bytes`. Neither tool unit has been compared with a real tool provider's billing. |
| `tool_use_prompt_tokens` | tokens | Gemini `toolUsePromptTokenCount`: tokens a built-in tool retrieved, reported apart from `promptTokenCount` and **not** part of `input_tokens`. Never zero-filled. |

A Gemini `promptTokensDetails` or `candidatesTokensDetails` list names only the modalities
that carried tokens (on the Gemini Developer API a text-only prompt lists `TEXT` alone). When the listed counts add up to `promptTokenCount` (or
`candidatesTokenCount`), the standard modalities that are not listed are recorded as reported
zeros; when the list does not add up, they stay unknown. Without this a price that rates audio
could never cost a text-only request.

Summaries report them separately: `modality_tokens` (sums of the splits and of the
per-TTL cache writes, beside `cache_write_tokens` and never added to it),
`units` (sums of the unit fields), `unit_records` (records that reported each
unit) and `batch_records`. Each is omitted when empty. `aiplex usage records -o wide` and
`aiplex usage summary -o wide` show the reported TTLs after the cache-write total, as
`30 (5m=10 1h=20)`; a TTL that was not reported is left out. In a summary the per-TTL figures
are sums over the records that reported them, while the total also counts records that reported
only `cache_write_tokens`, so the figures need not add up to the total (`100 (5m=10 1h=20)`).
Batch usage is recorded
once, on the results fetch that settled the batch; see
[model metering profiles](model-metering-profiles.md#provider-batches).

## Prices

Prices are tenant resources of kind `model-price`, managed with `aiplex usage prices`.
Cost is computed once, when the record is written. Saving a price later does not
change existing records; [back-estimation](#back-estimation) prices records that were
unknown when written, on request. A price applies only to records whose `started_at` falls
inside its `effective_from` and `effective_until` window.

```json
{
  "id": "example-flash",
  "model_id": "gemini-2.5-flash",
  "currency": "USD",
  "scale": 6,
  "meters": {
    "input_tokens": {"numerator": 1, "denominator": 1},
    "output_tokens": {"numerator": 1, "denominator": 1},
    "requests": {"numerator": 0, "denominator": 1}
  }
}
```

The rates above are placeholders that show the shape of the resource. Replace
them with your contracted prices. Each meter is `numerator` fixed-point money
units, at the price's `scale`, per `denominator` raw units. Allowed meters are
`input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`,
`reasoning_tokens`, `requests`, every modality split, every unit field and
every per-TTL cache write listed under [units](#units-and-modality-splits), and per-call
`server_tool.<kind>` meters ([server tools](#server-tools)). A zero numerator declares a
free meter.
A priced modality split is charged at its own rate and removed from its parent
(`input_tokens` or `output_tokens`), like `cache_read_tokens`. When both a cache
subset and an input modality split are priced, cost is estimated only if the
priced cache subset is zero or every non-text input modality is known to be zero,
because cached tokens can also be audio or image tokens (the ledger does not hold
cached tokens by modality). A known, non-zero unit without a price makes the cost
unknown.

**A price that rates one non-text modality of a direction must rate them all.** If a price
states a rate for any of `input_audio_tokens`, `input_image_tokens` or `input_video_tokens`,
a record that reports a positive count of another of them with no rate is `unknown`, not
partially costed (and likewise for `output_audio_tokens` and `output_image_tokens`). A modality
rate the price states for a quantity the record does not report is `unknown` too, which is
why the Gemini zero-fill above matters. A price with no non-text modality rate is flat: it
charges every input token at `input_tokens` and every output token at `output_tokens`, as
before. Text is the remainder and needs no rate of its own.

**Per-TTL cache writes.** `cache_write_tokens` is rated at the provider's default TTL: five
minutes for Anthropic and Bedrock, 30 minutes for OpenAI (the catalogue's
`cache_creation_input_token_cost` is that rate; it states one-hour and 12-hour rates
separately). Each of `cache_write_5m_tokens`, `cache_write_1h_tokens`, `cache_write_30m_tokens`
and `cache_write_12h_tokens` may carry its own rate:

- A priced TTL is charged at its own rate and removed from `cache_write_tokens`, which is
  charged at its rate only for the rest. A price that rates a TTL must also rate
  `cache_write_tokens`.
- An unpriced five-minute or 30-minute write is charged at the `cache_write_tokens` rate.
  Positive one-hour or 12-hour writes with no rate of their own are `unknown`: they are never
  charged at the default-TTL rate.
- A priced TTL whose count the record does not hold is `unknown`. A reported zero
  `cache_write_tokens` means zero writes of every TTL.
- The TTL counts never exceed `cache_write_tokens`, alone or together. A record whose counts
  do is `unknown`, whatever the price rates. A record with a positive TTL count and no
  `cache_write_tokens` is `unknown` too: its total is at least that count and cannot be priced.
  Observers always report the total with its breakdown; this covers other writers.

A positive `cache_write_tokens` reported without a TTL breakdown (OpenAI Chat Completions, an
Anthropic response without `cache_creation`) is charged at the `cache_write_tokens` rate when
the price rates no TTL: the ledger cannot tell that some of it was a one-hour or 12-hour
write, so such a cost can be too low. Rate the long TTLs to make those records `unknown`
instead. `tool_use_prompt_tokens` (Gemini) is a unit that must be rated when it is positive;
the catalogue does not rate it.

A price matches a record when its `model_id` equals the record's model, its `provider`
(when set) equals the record's provider, the record's `started_at` falls in its effective
window, and every selector it sets matches the record. Optional selectors narrow a price:
`batch` (`true` matches only batch exchanges, `false` only others), `image_size` and
`image_quality`. Among the prices that match a record, the one with the most selectors
(`provider` counts as one) wins. A tie is no match, and the cost is `unknown`. There is no built-in batch discount: configure a `batch: true`
price with the contracted batch rates.

`input_tokens` is the total input. When `cache_read_tokens` is also priced, only
the uncached remainder is charged at the input rate. `reasoning_tokens` is charged
in addition to `output_tokens`, so price it only for providers that report
reasoning separately. Optional `provider` narrows a price to one provider. A
price without `provider` matches any provider for that model.

A price may carry an optional `source` object (`catalog`, `commit`, `retrieved_at`,
`source_url`). It records where an imported price came from and has no effect on matching
or cost.

The Gemini Developer API omits `cachedContentTokenCount` and `thoughtsTokenCount` when they are zero (observed on the live API), so for `gemini-developer` generation a record that reports prompt, candidate and total counts gets `cache_read_tokens` 0 and `reasoning_tokens` equal to the total minus prompt, candidates and tool-use prompt tokens. Gemini reports thinking tokens apart from output tokens and bills them as output, so catalogue imports for `gemini-developer` price `reasoning_tokens` at the output rate.

If a priced meter's quantity is not reported for a record (for example `cache_read_tokens`
on a provider that omits cached tokens), that record's cost stays `unknown`; the ledger does
not treat the missing count as zero.

### Context-length tiers

A price may carry `tiers`, long-context overrides keyed on the request's input size:

```json
{
  "id": "haiku-long", "model_id": "claude-haiku-5-5", "currency": "USD", "scale": 6,
  "meters": {"input_tokens": {"numerator": 1, "denominator": 10}, "output_tokens": {"numerator": 1, "denominator": 2}},
  "tiers": [{"above_input_tokens": 100000, "meters": {"input_tokens": {"numerator": 1, "denominator": 2}, "output_tokens": {"numerator": 5, "denominator": 2}}}]
}
```

A tier applies when the record's `input_tokens` (the ledger's total input, which includes
cache reads and cache writes) is **greater than** `above_input_tokens`, and then prices the
**whole request** at the tier's meters, not only the tokens above the threshold. When several
tiers apply, the one with the highest threshold wins. Thresholds must be positive and strictly
increasing, at most 8, and each tier must name exactly the meters of the base price, so a meter
never switches between priced and unpriced. A record whose `input_tokens` is unknown cannot
select a tier, so its cost is `unknown`.

That is the rule of the providers whose tiers the catalogue imports. A provider that charges
only the excess cannot be expressed. Each provider's rule, from its documentation as of 2026-11-10:

| Provider | Threshold | What the provider says | Imported |
| --- | --- | --- | --- |
| `anthropic` (Claude Haiku 5.5) | over 100,000 input tokens | "A request's prompt length counts all of its input tokens, including cache reads and cache writes. Each request is priced on its own: a request over the threshold pays the higher prices" ([pricing, long context](https://platform.claude.com/docs/en/about-claude/pricing#long-context-pricing)). | Yes |
| `openai` | over 272K input tokens (read as 272,000: the 400K window is 272K input plus 128K output) | "Prompts with >272K input tokens are priced at 2x input and 1.5x output for the full session" ([GPT-5.5 model page](https://developers.openai.com/api/docs/models/gpt-5.5)). The page does not say in words that cached tokens count; OpenAI `input_tokens` includes them, and the ledger compares that total. | Yes, with that caveat |
| `gemini-developer`, `google` | 200K (128K on a few older entries) | The pricing page labels "prompts <= 200k tokens" and "> 200k" but does not say whether the higher rate covers the whole prompt or only the excess, or whether cached tokens count ([Gemini pricing](https://ai.google.dev/gemini-api/docs/pricing)). The Vertex AI pricing page could not be read. | **No**: left as `--accept-partial` |
| `xai` | 128K or 200K | "Requests whose prompt reaches the listed token threshold are billed at the higher rate for all tokens in the request" ([xAI models](https://docs.x.ai/docs/models)): whole request, but "reaches" (at or over) is not the catalogue's "above". | **No** |
| `azure-openai`, `bedrock`, `vertex-anthropic` | various | Their own pricing pages were not confirmed. | **No** |

### Service tiers

A price may carry `service_tier` (for example `flex` or `priority`). It matches only records
whose `service_tier` equals it. A price **without** the selector covers standard-tier
exchanges only: records with no tier reported, or `default`, `standard` or `batch`. A record
served on a tier no price covers is `unknown`, never priced at the standard rate. The catalogue
imports priority and flex rates as `-priority` and `-flex` prices for the providers that report
the tier actually used (`openai`, `gemini-developer`, `google`); a provider that does not
report it gets no tier prices.

### Server tools

Providers run some tools themselves and bill them per request. A record's `server_tools` map
holds the request counts the provider reported, by kind, in the formats already parsed:

| Provider | Kind | Reported as |
| --- | --- | --- |
| Anthropic | `web_search`, `web_fetch`, `code_execution`, and any other `server_tool_use.<kind>_requests` key under its own name | `usage.server_tool_use` (JSON and the streaming `message_delta`). |
| OpenAI Responses | `web_search`, `file_search`, `code_interpreter`, `image_generation` | One per `web_search_call`, `file_search_call`, `code_interpreter_call` or `image_generation_call` item in the final response's `output` (JSON and the terminal streaming event). Function calls run in the caller and are not counted. |
| Gemini | `google_search` (and other tools by their lowercase name) | `usageMetadata.billedToolCalls`, counted per `searchQueryCount` for a search tool and per `successfulToolCallCount` otherwise (shape observed live, 2026-11-10). `groundingMetadata.webSearchQueries` is the fallback when `billedToolCalls` is absent. |

A count is recorded only when the provider reports it; a response without the field leaves
the kind absent, never zero. A price rates a kind with a per-call meter named
`server_tool.<kind>` (for example `"server_tool.web_search": {"numerator": 10000, "denominator": 1}`
is $0.01 per call at scale 6). A record with a positive count for a kind the price has no
meter for is `unknown`. A priced `server_tool.*` meter on a record with no reported count
contributes nothing, because providers report a tool only when it ran. Gemini bills grounding
per search query on current models and per grounded prompt on older ones, so rate
`server_tool.google_search` only for a model billed per query. The Gemini tool-use prompt
tokens (`tool_use_prompt_tokens`) are a separate unit.

### Rounding

A record's cost is a whole number of minor units at its price's scale. `Estimate` adds exact
rationals (no floating point) and rounds **up** once, at the price's scale: the amount is the
smallest whole number of minor units that is not below the exact cost, so one record is at most
one minor unit above it. For example, 9.25 micro-USD is stored as 10.

To keep that from accumulating, the exact value is kept: when the amount was rounded up,
`cost.exact` holds the unrounded amount in minor units as a fraction (`"37/4"` for 9.25).
Summaries (`cost` totals) and back-estimation totals **add the exact values** (a record without
`exact`, such as a settled cost, adds its whole amount) and round up once on the total. Four
records of 9.25 micro-USD sum to 37, not 40. Prices from the catalogue use scale 6; a higher
scale on a price you set shrinks one record's rounding but is not needed for exact sums.
Records written before `exact` existed contribute their stored amount.

### Back-estimation

Records written before a price existed have an unknown cost. `aiplex usage prices backfill`
(`POST /api/v1/usage/backfill`, admin) computes it afterwards:

- A record is eligible when its cost is `unknown`, its usage is known and final (no `usage_bound`),
  and one price covers it exactly. It is priced with the price effective at the record's own
  `started_at`: a price created later with an effective window that covers the record applies, and
  a price outside its window never does.
- The cost becomes `estimated` with `price_id` and `backfilled_at`. That marks that the cost was
  unknown when the record was written; the usage and every other field are untouched. A `settled`
  or `estimated` cost is never changed, and a second run changes nothing. The write fails closed:
  a store updates a record only while its cost is still unknown (Firestore does this in a
  transaction), so a concurrent writer's cost is never overwritten.
- Work is bounded: one call scans at most 1,000 records (`limit`, default 500) and returns a
  `next_cursor` to continue. The CLI loops and adds the pages up exactly.
- `--dry-run` (`dry_run: true`) reports counts and the total per currency and writes nothing.
  Filters: `since`, `until`, `model_id`, `provider`. The result counts records left unknown and
  why (`usage_not_final`, `no_price`, `changed`).
- Summaries reflect backfilled costs, and `cost_basis=unknown` finds what remains. Only `unknown` costs are ever written: a `reported` cost (a harness's own figure) and a `settled` or `estimated` one are left as they are. A reported record that has usage but no cost may be priced like any other unknown-cost record, which is what the OTLP receiver does at ingest with the same `Estimate`. Summaries that split by source still add exact values and round once per group.

### Price catalogue

Without a price, cost is `unknown`. To start from public list prices, the `aiplex` binary
carries pinned, trimmed snapshots of two third-party price lists (both MIT licence; the notices
are in `THIRD_PARTY_NOTICES.md` at the repository root):

- **litellm** (the default): LiteLLM's `model_prices_and_context_window.json`, 780 models.
- **models.dev**: the model files under `providers/` in [sst/models.dev](https://github.com/sst/models.dev),
  537 models. It carries text token prices, context tiers, cache rates, and web search for
  Anthropic; it has no batch, priority or flex rates here.

Each snapshot is taken at one recorded commit on a recorded date. The data is in
`pkg/pricecatalog/snapshot.json` and `snapshot_modelsdev.json`; `aiplex usage prices catalog`
reads them and creates ordinary `model-price` resources from the entries you choose.

```bash
aiplex usage prices catalog search "claude haiku"            # substring match on <provider>/<model>, both sources
aiplex usage prices catalog show anthropic/claude-haiku-5-5  # rates, tiers, source, what is not imported, disagreements
aiplex usage prices catalog import anthropic/claude-haiku-5-5 --dry-run
aiplex usage prices catalog import anthropic/claude-haiku-5-5 --yes
aiplex usage prices catalog import mistral/open-mistral-nemo --source models.dev --yes
aiplex usage prices catalog import --all-for-access-point team --yes
```

What to know before relying on it:

- **A snapshot, not a feed.** The prices are what a third party published at the snapshot date
  shown by every catalogue command. They can be stale or wrong. They are public list prices:
  discounts, commitments, negotiated rates and regional uplifts are not reflected. Cost computed
  from an imported price keeps the basis `estimated`; it is not an invoice. Compare it with your
  bill before using it for chargeback.
- **Two sources, and they can disagree.** `search` and `show` list a model from both sources and
  flag every meter that both state with a different value, in USD per million tokens as each
  source states it. Statements are compared numerically (`2.50` equals `2.5`); a meter only one
  source states is not a disagreement. On the pinned commits the sources share 397 models and
  disagree on 55 meter values across 22 models, for example `deepseek/deepseek-v4-flash`
  (input 0.3 against 0.15 USD per million tokens), `mistral/open-mistral-nemo` (0.3 against 0.15),
  `azure-openai/gpt-4-turbo` (input 11 against 10), and several Bedrock and Nova entries that differ
  by about 10 percent. An import of such a model is refused until `--source litellm|models.dev`
  chooses one, and the refusal prints both values. Without a disagreement, `--source` defaults to
  litellm; a model only models.dev lists needs `--source models.dev`. The chosen source is
  recorded in the price's `source` object.
- **Nothing is priced automatically.** There is no fallback to the catalogue when cost is
  computed. A model you did not import stays `unknown`. On a terminal, `usage records` and
  `usage summary` print one line on stderr naming a catalogue model whose cost is unknown and the
  import command; `-o json` and pipes get nothing. Under `aiplex dev --provider ...` the summary
  names the catalogue entry for the wired model; it imports nothing.
- **Import shows exactly what it writes**: each meter as an exact decimal per million tokens and
  as the stored rational, each context tier, the source URL and the snapshot date. It never
  overwrites an existing price unless `--replace` is given, and it will not add a price beside
  another one that already covers the same provider, model, batch and service-tier selector (a tie
  matches nothing). Writing needs `--yes` when there is no terminal; `--dry-run` writes nothing.
- **Conversion is exact.** The source stores numbers per token or per million tokens (for example
  `1.5e-07`). The generator reads the number's text, converts it with exact decimal arithmetic to a
  price per million tokens (`0.15`), and the import stores it at scale 6 as a rational (`3/20`). A
  meter the source does not state stays unset (unknown); a rate the source states as `0` is kept as
  a declared free meter. models.dev's TOML numbers are read through float64 and refused if they have
  more than 15 significant digits.
- **Meters imported**: `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`,
  `cache_write_1h_tokens` where the source states a one-hour rate, input and output modality token
  rates (`input_audio_tokens`, `input_image_tokens`, `input_video_tokens`, `output_audio_tokens`,
  `output_image_tokens`) for the providers whose responses report those splits (OpenAI, Azure OpenAI
  and Gemini), and `server_tool.web_search` ($0.01 per call, the "$10 USD per 1,000 searches" that
  Anthropic's page states) for Anthropic, whose usage reports the call count. Context tiers are
  carried where the provider's rule is confirmed (see [context-length tiers](#context-length-tiers)).
  When the source states batch rates, a second price with `batch: true` (id ending `-batch`) carries
  them, with its own tiers. Priority and flex rates become `-priority` and `-flex` prices with a
  `service_tier` selector for the providers that report the tier used, and only when they price the
  same meters (and tiers) as the standard rates; otherwise they are not imported and a request on that
  tier is `unknown`. Reasoning tokens are never priced from the catalogue except for
  `gemini-developer`, which bills thinking tokens at the output rate. Output tokens already include
  reasoning for OpenAI and Anthropic, but the ledger holds a record that reports reasoning tokens as
  unknown cost unless the price declares the meter; `--reasoning-included` declares it as a free meter
  on your say-so. Do not use it for providers that report reasoning separately from output.
- **What is not imported** is listed on `show` and `import`: per-image, per-second, per-character,
  per-page and per-query rates, other tools' surcharges, the 12-hour and other long cache lifetimes
  (the pinned litellm snapshot names `cache_creation_input_token_cost_above_12hr` and the models.dev
  snapshot `cost.text.input.cache.write.30m` as left out, without a value, so no 5m, 30m or 12h
  meter is imported and none is derived from another rate; a record with positive 12-hour writes is
  `unknown` under an imported OpenAI price until you add `cache_write_12h_tokens` yourself),
  and cached-input rates for a modality whose own token rate is imported (a record that has both
  cached tokens and tokens of that modality is `unknown`, so it cannot be mispriced). Pricing a price
  cannot express that changes what a request costs makes the import refuse unless you pass
  `--accept-partial`; the output then names the fields left out and says the import goes ahead because
  the flag was given. Entries with only per-unit pricing (image, audio, video, rerank, OCR) are not in
  the snapshot.
- **DeepSeek context-cache hits.** litellm states a DeepSeek cache-hit rate
  (`input_cost_per_token_cache_hit`) beside its cache-read rate; on the pinned commit the two are
  equal in every DeepSeek entry, so the generator imports it as the `cache_read_tokens` rate, and
  would block the entry if they ever differed. That rate prices records from a surface that declares
  `usage_semantics: "deepseek"` ([model providers](model-providers.md#usage-semantics-for-openai-compatible-servers)),
  which records the hits as `cache_read_tokens` and cache writes as 0. Without that declaration a
  record's cache split is whatever the server reported as `prompt_tokens_details.cached_tokens`, and
  `cache_write_tokens` stays unknown, so a DeepSeek price that states `cache_write_tokens` (as `0`)
  leaves its cost `unknown`. DeepSeek's off-peak discount is not imported: the litellm rates are the
  peak rates, and the models.dev snapshot (which states the off-peak ones) names the multiplier as
  left out. Import a DeepSeek model with `--source litellm` or `--source models.dev`; the two disagree.
  `deepseek-chat` is not in the litellm snapshot: its two upstream rows (`deepseek-chat` and
  `deepseek/deepseek-chat`) disagree about cache writes, and the generator drops a model whose rows
  conflict instead of choosing one. A record for that model has no imported litellm price, so its cost
  stays `unknown` until you set a price yourself.
- **What still needs `--accept-partial`.** 140 of the 781 litellm entries, down from 212:
  129 have context tiers for a provider whose rule is not confirmed (Azure OpenAI 25, Bedrock 42, xAI
  43, Gemini 16, Vertex Claude 2, plus one OpenAI entry whose tier set does not match its base
  rates), 7 (Gemini) state a
  video output token rate (the ledger holds no output video tokens), and 4 Bedrock entries state audio
  and image token rates Bedrock usage does not report. In the models.dev snapshot, 124 of 537 do, mostly
  because it states audio, image and video rates this import does not carry.
- **Provider and model ids match what the ledger records.** Catalogue providers are `openai`,
  `anthropic`, `azure-openai`, `bedrock`, `gemini-developer`, `google` (Gemini on Vertex AI, whose
  records carry the Google publisher), `vertex-anthropic` (Claude on Vertex, recorded as
  `anthropic`) and the OpenAI-compatible vendors `deepseek`, `groq`, `mistral` and `xai` (recorded
  as `openai-compatible`). Model ids are the provider's own: routing prefixes such as `azure/`,
  `gemini/`, `vertex_ai/` and `bedrock/` are removed. Bedrock ids (including geography
  inference-profile ids such as `us.anthropic.claude-...`) are kept as written; region-specific,
  commitment and image-size entries are not in the snapshot. Vertex Claude entries are imported only
  in Vertex's versioned form (`claude-...@20251001`), because an unversioned id would collide with
  the first-party Anthropic id. models.dev files map the same way (its `google-vertex` provider only
  for `gemini-` ids, `google-vertex-anthropic` only for versioned ids, router providers not at all).
  Matching is exact: there is no fuzzy matching. Azure entries use the model name; if your
  deployment has another name, set that price yourself. A surface of an OpenAI-compatible endpoint is
  not matched to a vendor automatically; import it by name.
- **Rebuilding the snapshots.** `make update-price-catalog [COMMIT=<sha>] [MODELSDEV_COMMIT=<sha>]`
  pins new upstream commits for both sources, checks that each licence text is unchanged, and rewrites
  the snapshots and their recorded checksums; a test fails if an embedded file does not match its
  checksum. Regenerating from the recorded commits (`go generate ./pkg/pricecatalog`) must not change
  the files. litellm upstream rows that state two different prices for one id are left out.

## Error classes

`error_class` is set when `status` is not `ok`. Provider and gateway failures use the
HTTP-status class below. A quota, capacity or session reason replaces that class.

| `error_class` | Set when |
| --- | --- |
| `unauthenticated` (401), `forbidden` (403), `not_found` (404), `conflict` (409), `rate_limited` (429), `invalid_request` (400, 413, 415, 422), `client_error` (other 4xx), `unsupported` (501), `upstream_error` (502), `unavailable` (503), `timeout` (504), `server_error` (other 5xx) | The HTTP status is 400 or higher. |
| `stream_aborted` | The response began but did not complete. |
| `client_disconnected` | The caller disconnected, or a write to the caller failed. |
| `session_usage_<bound>` | A WebSocket session ended without final usage. `<bound>` is `lower_bound`, `ambiguous` or `partial`. |
| `capacity_cooldown` | The request was rejected by the surface's [capacity policy](model-providers.md#capacity-policy) while the provider was throttling. It is not a provider error. |
| `capacity_shed` | The request was rejected by the capacity policy's headroom rule. Callers below `min_weight` are shed first. It is not a provider error. |
| `upstream_auth` | The provider refused the **gateway's** credential (HTTP 401 or 403, or Gemini's 400 `API_KEY_INVALID`). The caller received a `502` in the provider's own error shape with the code `upstream_credential_rejected`; the provider's body was not relayed. Status `error`, not `denied`, so it is distinct from a caller authentication failure. Operators (admin or deployer) see the surface id in the error details; other callers do not. |
| `upstream_quota_exhausted` | The provider's quota or balance is exhausted, as it says in a distinct code: OpenAI, Azure OpenAI and OpenAI-compatible `429` with error code `insufficient_quota`; Bedrock `ServiceQuotaExceededException`. The provider's response reaches the caller unchanged. Anthropic and Gemini do not distinguish quota from rate limits in a `429`, so theirs stay `rate_limited`. |
| `model_not_found` | A caller the access point admitted asked for a model it does not offer (HTTP 404, status `error`). See [access points](access-points.md). |
| Quota reason codes | QuotaPlex refused the exchange before the provider was called. |
| `hook_denied` | A [request hook](request-hooks.md) refused the request (before the provider was called; no quota is reserved) or withheld the response (after it; usage is still recorded). Status `denied`, HTTP 403. |
| `tool_policy_denied` | A [policy rule](tool-policy-rules.md) on a tool call refused it: a matching `deny` rule, or no `allow` rule matched. Refused before quota, hooks and the upstream call. Status `denied`, HTTP 403. The record's `tool_policy_rule` names the rule (empty for the default deny). |
| `tool_policy_error` | A policy rule on a tool call could not be evaluated (or the arguments were over the evaluation limit) and the call was refused. Status `denied`, HTTP 403. `tool_policy_rule` names the rule that failed. |
| `hook_unavailable` | A request hook could not decide (timeout, unreachable or invalid sidecar) and is not `fail_open`. Status `error`, HTTP 503. |
| `hook_policy_unavailable` | The tenant's hook policies could not be read, or a stored one no longer validates. Status `error`, HTTP 503. |

The capacity rejections are status `denied`, with HTTP 429 and a `Retry-After` header.

## Worked example

```sh
# Set a price from the command line: dollars per million tokens (currency defaults to USD)
aiplex usage prices set gpt-4o-mini --input-per-mtok 0.15 --output-per-mtok 0.60
# Or save a full price specification from a file (JSON or YAML)
aiplex usage prices set -f example-flash.json
aiplex usage prices list
aiplex usage prices get example-flash

# Records for one agent in the last day, errors only
aiplex usage records --agent tutor-agent --since 24h --status error

# Records that entered through one access point, by a user or agent caller
aiplex usage records --access-point ide-team --caller-kind agent --since 24h

# Daily totals by model and status for the last week
# (--by model,status is the same as --by model_id,status)
aiplex usage summary --by model,status --bucket day --since 7d

# Model-plane totals over a period, same data as `aiplex usage summary --plane llm`.
# A cost or token count the ledger lacks is "unknown", never $0.00, and the
# output says how many records it leaves out. `aiplex llm usage` lists the records.
aiplex llm costs --period week

# Totals per access point and caller kind
aiplex usage summary --by access-point,caller-kind --since 7d

# One record in full
aiplex usage get ul_<id>
```

`--by` takes the friendly aliases the CLI help lists: `plane`, `provider`, `model`,
`tool`, `agent`, `user`, `client`, `status`, `source`, `error-class`, `bound` and `batch`. The SDK maps `model` to `model_id`,
`agent` or `actor` to `actor_id`, `user` or `principal` to `principal_id`, `client` to
`client_id`, `tool` to `tool_name`, `error-class` to `error_class` and `bound` to `usage_bound`
before the request is sent. The API field names
(`model_id`, `actor_id`, `principal_id`, `client_id`, `tool_name`, `plane`, `provider`,
`status`, `access_point_id`, `caller_kind`, `source`, `error_class`, `usage_bound`, `batch`) are
also accepted. The CLI aliases `access-point`
(`access_point_id`) and `caller-kind` (`caller_kind`) are accepted too. Repeating a field is
an error.
Summary responses carry `truncated` and `records_scanned`. A truncated total covers only the
records scanned.

## Harness telemetry (reported records)

Coding harnesses export their own OpenTelemetry. AIPlex can receive it and keep the
usage it describes as `source=reported` records, next to the managed records for
requests that went through an access point. Point a harness at AIPlex with
`aiplex connect <access-point> --client <client> --write --with-telemetry aiplex`; the
receiver, its limits and exactly which signals are mapped are in
[observability](observability.md#harness-telemetry-received-over-otlp) and
[harness onboarding](harness-onboarding.md#telemetry-into-the-usage-ledger).

A reported record is written from one harness log event per model request:

| Ledger field | From |
| --- | --- |
| `tenant_id`, `principal_id`, `principal_display`, `actor_id`, `client_id`, `caller_kind` | The verified token of the sender. Never the payload. |
| `plane`, `operation` | `llmplex`; `<harness>.<event>` such as `claude-code.api_request`. |
| `model_id` | The event's model. |
| `usage` | The event's token counts. Each missing count stays `null`; `input_tokens` is the total input including cache tokens, so Claude Code's `input_tokens` is added to its cache read and cache creation counts, and is `null` if either is missing. |
| `cost` | Basis `reported` with currency `USD` when the event carries a cost (`cost_usd_micros` exactly, else `cost_usd` as its shortest decimal, rounded up beyond 12 places). When it carries none and a `model-price` matches, basis `estimated`. Otherwise `unknown`. |
| `started_at`, `finished_at`, `latency_ms` | The event time and, when the harness reports it, the request duration. A record with no reported duration has `latency_ms` 0 and `started_at` equal to `finished_at`: the field cannot hold "unknown", so read 0 as "not reported" on reported records. |
| `trace_id`, `span_id` | The ids on the log event, when it carried any. |
| `harness`, `harness_version`, `session_id`, `client_request_id` | The event and its resource (`service.version`). |
| `reported_attributes` | `event`, `prompt_id`, `request_id`, `query_source`, `speed`, `effort`, `service_tier`, `attempt`, `entrypoint`, `terminal_type`. At most 16, each at most 256 characters. |

Every record id is the hash of (tenant, sender, harness, session, event time, event name,
sequence, prompt id, request ids, model), so an OTLP retry of the same event is stored once,
also across replicas. User email, account and organization ids, repository details, prompts,
tool input and error text are never copied.

### Sources in summaries

A managed record and a reported record can describe the same request. A summary never
adds them silently:

- With no `source` filter, `source` is added to the grouping, so managed and reported
  are separate groups (`source_split: true`), and reported records that correspond to a
  managed record are left out and counted in `excluded_correlated_reported`.
- `source=reported` returns every reported record, correlated or not, with a note that its
  totals must not be added to managed totals.
- `merge_sources=true` adds the two together, still leaving out correlated records. The
  response says that reported records with no key in common with a managed record may
  describe requests that are also counted as managed.
- `notes` says in plain words what was done. The dashboard and the access point and subject
  roll-ups under `/api/v1/overview` count managed records only.

A reported record is matched to a managed one only by an explicit key both carry, within one
tenant and one verified principal; model, time and token counts are never used to guess:

1. `client_request_id` on both (the harness sends it as `X-Client-Request-Id`; AIPlex stores
   it on the managed record).
2. The managed record's `caller_span_id` (the span id in the `traceparent` the harness sent)
   equals the reported event's own `span_id`, and the trace ids are equal.
3. A `request_id` the caller chose with `X-Request-Id` (the server-generated `req-<token>-<number>`
   form, and the older `req-<number>` that earlier records carry, are never keys; the API ignores a
   caller's id of that form).
4. As a fallback, the same trace: the harness propagated a trace and the managed record
   continued it, but the event's span is not the request's span. Each managed record absorbs
   at most one reported record this way.

When no key exists the records stay in separate groups. Whether a given harness provides a
key depends on how it is configured; see [harness onboarding](harness-onboarding.md#observed-with-real-clients).

## Tags

Tags are small key/value labels on a record, for comparing experiments (an arm, a prompt
version). They come from two places:

- the access point's `labels`, which are trusted configuration and always apply;
- the `X-AIPlex-Tags: key=value,key=value` request header (also accepted on OTLP exports),
  honored only for keys in `AIPLEX_TAG_KEYS_ALLOWED` (default: none). A header cannot override
  a configured label. Unlisted or malformed parts are dropped and counted in
  `aiplex.usage.tags_rejected`.

Keys are `[a-z][a-z0-9_.-]{0,31}`, values up to 64 characters of `[A-Za-z0-9_.:/@+() -]`, at
most 8 tags a record. Header values per key and tenant are capped at `AIPLEX_TAG_MAX_VALUES`
(default 50); beyond it a value is stored as `(other)` and counted with reason `overflow`.
The cap is kept in process memory, so each replica applies it to its own view and a restart
begins afresh. Filter with `tag.<key>=<value>`; group with `tag:<key>`.
The record of a refused call carries the call's tags, `client_request_id`, `access_point_id` and
`caller_kind` as a successful call's record does (a model not found, a missing grant, quota, capacity and
hook refusals, a routed model with no backend, and a managed tool call). A refusal that writes no record
(see [response headers](access-points.md#response-headers)) has nothing to carry them.

## Feedback

`POST /api/v1/usage/records/{id}/feedback` takes `{"score": -1..1, "label": "helpful",
"comment": "..."}`. `score` is required; `label` is `[a-z][a-z0-9_.-]{0,31}`. The record's own
principal or actor, or an operator (admin or deployer), may submit it; a record the caller
cannot see answers 404. `comment` is stored only when the operator set
`AIPLEX_FEEDBACK_COMMENTS=true`; otherwise a request with a comment is refused
(`COMMENTS_DISABLED`), not silently trimmed. The author is the verified principal.

Feedback is stored beside the record, never in it. It is append-only: the newest entry is
the record's current feedback (shown as `feedback` in the record) and earlier entries stay in
`GET .../feedback`. A summary with `feedback=true` adds per group: `records` with
feedback, `without_feedback`, `positive`, `negative`, `neutral` and `average_score`. Records
without feedback are not a neutral score: they are counted in `without_feedback` and left out
of the average, which is `null` when no record has feedback. Free-text comments never appear in
summaries.

## Capture and enablement

Capture needs no switch. Each completed exchange on these paths writes one record:

| Path | Record |
| --- | --- |
| `/api/v1/llm/native/{id}/*` and `/api/v1/llm/gateway/{id}/*` | `plane=llmplex`, `source=managed` |
| `/api/v1/a2a/connections/{id}/invoke` | `plane=a2aplex`, `source=managed` |
| `/api/v1/mcp/connections/{id}/invoke` | `plane=mcplex`, `source=managed` |
| `POST /api/v1/llm/usage` | `plane=llmplex`, `source=reported`. The route requires the admin role, and a verified tenant is needed to mirror the report. |
| `POST /otlp/v1/logs` | `plane=llmplex`, `source=reported`, one record per recognized harness usage event. Any verified caller reports its own telemetry. |

The access-point model and tool endpoints call the same handlers, so their
exchanges are recorded with `access_point_id` and `caller_kind` set.

The ledger uses Firestore when the registry store is Firestore. Otherwise it uses
an in-process memory store that keeps up to 100,000 records per tenant and loses
them on restart. The bundled stores bound retention to 90 days. Records are
written in the background. A failed write is logged with the record ID and does
not change the response to the client.

## CLI

`aiplex usage records` and `aiplex usage summary` take the same filters: `--plane`,
`--provider`, `--model`, `--tool`, `--agent`, `--user`, `--client`, `--access-point`,
`--caller-kind`, `--budget`, `--status`, `--source`, `--error-class`, `--bound`, `--batch`, `--hook-decision`, `--cost-basis`, `--since` and `--until`.
`--error-class` and `--bound` pass through to `error_class` and `usage_bound`. `--bound` accepts
`lower_bound`, `ambiguous`, `partial` or `none`. `--batch` takes `true` or `false`. Given alone,
`--batch` means `--batch=true`. `--since` and `--until` also accept a duration such as `24h` or
`7d`, measured back from now. `--until` is exclusive. `records` also takes
`--limit`, `--cursor` and `--all`. `summary` takes `--by` and `--bucket` (`hour` or `day`).

`aiplex usage records` prints one row per record. Tables fit the terminal width (from `$COLUMNS`, then the
terminal size). When standard output is not a terminal, nothing is shortened. `-o wide` prints every column,
untruncated. The default columns are:

| Column | Shows |
| --- | --- |
| `TIME` | When the exchange started, relative (`just now`) in the default table. |
| `MODEL/TOOL` | The model, or the tool name for an MCP call. |
| `CALLER` | The readable caller: the user's display name (the verified email or subject), with the agent in front of it when the call was made by an agent for a user, as `agent (user)`. |
| `ACCESS POINT` | The access point the call came through. |
| `STATUS` | The status. A capacity rejection shows its reason, for example `denied (capacity_cooldown)`. |
| `IN`, `OUT` | `input_tokens` and `output_tokens`. Unknown counters show `unknown`. |
| `COST` | The cost and its basis, `(settled)` or `(estimated)`. Unknown cost shows `unknown`. |

`-o wide` adds the columns the default table leaves out: `ID` (the record ID), `PLANE`, `AGENT`, `USER` (the
readable user name), `PRINCIPAL ID` (the key the caller is recorded under), `CLIENT`, `SESSION`, `BATCH`, `CACHE-W`,
`MODALITY`, `UNITS`, `LATENCY` and `HOOKS` (the hook decision with rule identifiers and counts, for example
`deny (private_key×1)`). Use `-o json` for every field of each record.

A record carries two readable names for the caller, set by the server from the verified identity when it
verified it: `principal_display` for the user (or client) and `actor_display` for the agent. The CLI shows the
display name in `CALLER`, and falls back to a shortened principal ID when none is recorded. The display name is
for people to read and is not a key. It can be an email address, so do not publish a ledger export without
checking it. `principal_id` and `actor_id` are the keys that `--user` and `--agent` filter on.

`aiplex usage get <id>` prints every field of one record, including `principal_display`, `actor_display`,
`http_status`, `error_class`, `usage_bound`, `batch`, `image_size`, `image_quality`, `service_tier`, `server_tools`,
the cost's `price_id` and `backfilled_at`, and each hook run.

`aiplex usage prices backfill [--since] [--until] [--model] [--provider] [--dry-run] [--yes]` prices records that
were unknown when written ([back-estimation](#back-estimation)). It shows the dry-run result first and asks before
writing.

`aiplex usage config` prints the usage settings the API serves (`GET /api/v1/usage/config`): the trace link
template, whether feedback and feedback comments are accepted, and the tag keys a caller may set. An empty template
is shown as `not set`, and an empty key list as `none`. A field the API did not send is shown as `unknown`. It is
read-only and open to any signed-in caller.

`aiplex usage summary` prints one row per group, with the group keys first. The default columns are
`REQUESTS`, `ERRORS`, `DENIED`, `IN`, `OUT` and `COST`. `-o wide` adds `CACHE-W`, `MODALITY`, `UNITS`,
`BATCH`, `UNKNOWN` and `BOUNDED`. `BATCH` counts batch records. `BOUNDED` counts records with `lower_bound`,
`ambiguous` or `partial` usage. The summary prints a note when any total includes bounded usage.

A group whose key is empty is labelled `(no model)` with `--by model`, and `(no tool)` with `--by tool`. A
`tools/list` record has no model and no tool, so it appears under both labels, and a chat record appears under
`(no tool)` in `--by tool`.

```text
$ aiplex usage summary --by model
MODEL     REQUESTS  ERRORS  DENIED  IN  OUT  COST
mock-gpt  1         0       0       1   9    USD 0.000006 (estimated)

1 records scanned.
```

## Limits

- Tool and agent records carry no token counts. Only model exchanges install a
  usage observer, so `usage` is `null` for them.
- A cache write reported without its TTL breakdown is charged at the `cache_write_tokens` rate
  under a price that rates no TTL, so a one-hour or 12-hour write among it is undercharged (see
  [per-TTL cache writes](#prices)). The catalogue imports no 12-hour rate, because its pinned
  snapshots carry none. Server-tool request counts are in `server_tools`, per
  [server tools](#server-tools). See [model metering profiles](model-metering-profiles.md#native-usage-composition).
- Only managed paths and trusted reports are captured. Envoy routes, direct
  provider calls and other traffic that bypasses these paths produce no record.
- A reported record is only as accurate as its reporter. The ledger does not
  check it against the provider.
- Reported OTLP records come from log events only. Metrics and traces are accepted and not
  recorded, so a request is never counted both as an event and as a counter or a span.
  A harness that exports only metrics produces no records. Tool results, prompts and other
  events are not recorded.
- Correlation of reported records with managed ones needs a key the harness actually sends.
  Claude Code, for example, sends no `X-Client-Request-Id` and no `traceparent` to a gateway
  unless `CLAUDE_CODE_PROPAGATE_TRACEPARENT=1` (and the traces beta) is set; without a key its
  reported records stay in their own group, and a summary shows them beside the managed ones
  rather than merging them. Run against a real Claude Code, the propagated trace did pair them.
- Feedback is not in list rows. It is in the record detail and, with `feedback=true`, in
  summaries (read page by page from a separate store). The tag value cap is per process.
- `POST /api/v1/llm/usage` mirrors its counters into the ledger but does not copy
  the report's floating-point cost. The ledger cost comes from a price resource or
  is `unknown`.
- A price is not applied retroactively. Correcting a price does not correct stored
  records. [Back-estimation](#back-estimation) fills in only costs that were `unknown`; it never
  rewrites an `estimated` or `settled` cost.
- Summaries group only on the fields listed above, and `group_by` is not a free-text search. There is no free-text search
  and no export route.
- Summaries count bounded records in full and report them in the `*_records` counts (see
  [bounded usage](#bounded-usage)). Records and summaries both filter on `usage_bound`.
- The memory store is not durable. Firestore-backed records follow the bundled
  90-day retention bound.
- Live and asynchronous job billing is outside this ledger. A managed request that
  starts a provider job records the request, not the job's later work.
- Currency conversion is not performed. Costs are reported per currency.

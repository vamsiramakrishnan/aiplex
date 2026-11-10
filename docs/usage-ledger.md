# Usage ledger

The usage ledger keeps one append-only, tenant-scoped record for each managed
exchange on the LLMPlex, MCPlex and A2APlex paths, plus records reported to
`POST /api/v1/llm/usage`. It answers questions such as which agent used which
model through which client, how many tokens the provider reported, and what the
exchange cost when a price or a quota settlement establishes a cost. Records hold
identifiers, status, timing and counters. They never hold prompts, completions,
tool arguments, results or credentials.

The design is in [usage ledger, agent identity and tool auth](design/2026-11-09-usage-ledger-agent-identity-tool-auth.md).
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
| `GET /api/v1/usage/records` | List records, newest first. Filters: `plane`, `provider`, `model_id`, `tool_name`, `actor_id`, `principal_id`, `client_id`, `access_point_id`, `caller_kind`, `status`, `source`, `error_class`, `usage_bound`, `batch`, `hook_decision`, `since`, `until`. Pagination: `limit` (1 to 1000), `cursor`. |
| `GET /api/v1/usage/records/{id}` | One record. |
| `GET /api/v1/usage/summary` | Totals. Accepts the same filters, except `limit` and `cursor`, plus `group_by` and `bucket=hour\|day`. Returns requests, errors, token sums, records with unknown usage and cost per currency. |
| `GET /api/v1/usage/prices` | List model-price resources. |
| `GET /api/v1/usage/prices/{id}` | One price with its revision. |
| `PUT /api/v1/usage/prices/{id}` | Create or replace a price. Send the current revision in `If-Match`; `0` creates. |
| `DELETE /api/v1/usage/prices/{id}` | Delete a price. Send the current revision in `If-Match`. |

Unknown query parameters, repeated parameters, and unknown `group_by` fields are rejected with
`400 INVALID_QUERY`. The valid `group_by` fields are `plane`, `provider`,
`model_id`, `tool_name`, `actor_id`, `principal_id`, `client_id`,
`access_point_id`, `caller_kind`, `status`, `source`, `error_class`, `usage_bound`,
`batch` and `hook_decision`. A `group_by` field may appear once.

Filter values are checked before the query runs:

| Parameter | Accepted values |
| --- | --- |
| `plane` | `llmplex`, `mcplex`, `a2aplex` |
| `caller_kind` | `agent`, `user`, `client` |
| `status` | `ok`, `error`, `denied`, `unknown` |
| `source` | `managed`, `reported` |
| `usage_bound` | `lower_bound`, `ambiguous`, `partial`, `none` |
| `batch` | `true` or `false`. A record with `batch: true` matches `true`. |
| `error_class` | Any text up to 512 characters. Matches records with exactly that `error_class`. |
| `hook_decision` | `allow`, `deny`, `transform`, `error`, `skipped`, or `none` (no [request hook](request-hooks.md) ran). Matches `hooks.decision`. |
| `since`, `until` | RFC 3339 timestamps. `until` must be after `since`. |

`usage_bound=none` matches records with no `usage_bound`, meaning counters that are final or
unknown. Any other value is matched exactly.

## Record fields

| Field | Meaning |
| --- | --- |
| `id` | Record identifier, prefixed `ul_`. |
| `plane` | `llmplex`, `mcplex` or `a2aplex`. |
| `provider`, `model_id`, `model_version` | Set for model exchanges. Tool calls use provider `mcp`. |
| `resource_id` | Surface, tool connection or agent connection identifier. |
| `tool_name` | Tool name for `tools/call` exchanges. A `tools/list` exchange has `operation: tools/list` and no tool name, so it counts as a request but not as a tool call in the dashboard. |
| `principal_id`, `actor_id` | Verified principal and delegated actor, when present. |
| `client_id` | The verified token's authorized party (`azp`). A public OAuth client ID is attribution, not a security boundary. |
| `access_point_id`, `caller_kind` | Set only when the exchange entered through an [access point](access-points.md). `caller_kind` is `agent`, `user` or `client`. |
| `status` | `ok`, `error`, `denied` (HTTP 401, 403, 407 or 429), or `unknown` (the response began but did not complete, or the client disconnected). |
| `source` | `managed` for records captured on AIPlex paths, `reported` for `POST /api/v1/llm/usage`. |
| `http_status` | The HTTP status the caller received. Omitted when the exchange had none. |
| `error_class` | The reason for a non-`ok` status. See [error classes](#error-classes). |
| `operation` | The native operation ID, for managed model exchanges. |
| `started_at`, `finished_at`, `latency_ms` | Timing of the exchange. |
| `streaming` | `true` when the response was a stream, or a WebSocket relay. |
| `requests` | Requests counted for the record. |
| `usage` | Provider-normalized counters, each nullable: `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`, `reasoning_tokens`, `total_tokens`. Optional [unit fields](#units-and-modality-splits) appear only when reported. The whole object is `null` when no counter or unit is known. |
| `batch` | `true` when the exchange belongs to a provider batch job (its creation, retrieval, or the results fetch that settled it). Omitted otherwise. |
| `image_size`, `image_quality` | Billing dimensions the provider reported for generated images (for example `1024x1024`, `high`); `mixed` when one exchange reported different values. Omitted when not reported. |
| `usage_bound` | Present only when `usage` is not final. One of `lower_bound`, `ambiguous` or `partial`. See [bounded usage](#bounded-usage). |
| `cost` | `basis`. When the basis is not `unknown`, also `currency`, `amount` (a decimal integer in minor units at `scale`), `scale` and `amount_decimal`, the same amount written as an exact decimal string (for example `0.000006` for `amount` `6` at scale `6`). |
| `provider_limits` | The provider rate-limit observation from the native response, when one was reported. See [model providers](model-providers.md). |
| `hooks` | Present when a [request hook](request-hooks.md) ran on the exchange. `decision` is the most significant decision of all runs (`deny`, then `error`, then `transform`, then `allow`, then `skipped`); `uninspected_parts` counts images, audio, files and unknown blocks that were present but not inspected; `fail_open_used` is `true` when a `fail_open` hook failed and the exchange continued without it. `results` lists each hook run: `id` (`policy/hook`), `type`, `phase` (`request` or `response`), `decision`, `rules` (rule identifiers with match `count`; for a denial the denying rule comes first), `duration_ms` and `fail_open_used`. It never holds content or the matched text. |
| `quota_attempt_id` | QuotaPlex attempt, when quota admitted the exchange. |
| `session` | `true` for a WebSocket session. One record covers the whole session, and its usage is final only with a terminal observation. Omitted otherwise. |
| `request_id` | The request's correlation identifier (`X-Request-Id`). |
| `trace_id`, `span_id` | W3C trace and span identifiers of the exchange's span: for a managed record, the CLIENT span that covers the exchange (see [observability](observability.md)); for a `reported` record, the SERVER span of the request that reported it. `trace_id` continues the caller's `traceparent` when it sent a valid one; otherwise AIPlex starts a trace. Both are set on every record whether or not telemetry export is configured, so request logs (`trace_id`), traces and records correlate. Records written before this field existed have no `span_id`. |

## Unknown is not zero

A counter that the provider did not report is `null`. A reported zero is `0`.
Summaries count records with unknown usage separately, so an absent counter never
lowers a total silently.

Cost has three bases:

| Basis | Source |
| --- | --- |
| `settled` | The QuotaPlex attempt for the exchange settled with a money meter in one currency. |
| `estimated` | A `model-price` resource matched the record. The amount is the exact rational sum of the priced quantities, rounded up once to the price's scale. |
| `unknown` | Neither source applies. The record carries no currency, amount or scale. |

A record gets `unknown` cost when:

- a known non-zero quantity has no matching meter in the price,
- a priced meter's quantity is unknown, or
- no single price is the best match. Two or more matching prices tie on the number of
  selectors (see [prices](#prices)).

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

Summaries report them separately: `modality_tokens` (sums of the splits),
`units` (sums of the unit fields), `unit_records` (records that reported each
unit) and `batch_records`. Each is omitted when empty. Batch usage is recorded
once, on the results fetch that settled the batch; see
[model metering profiles](model-metering-profiles.md#provider-batches).

## Prices

Prices are tenant resources of kind `model-price`, managed with `aiplex usage prices`.
Cost is computed once, when the record is written. Saving a price later does not
change existing records. A price applies only to records whose `started_at` falls
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
`reasoning_tokens`, `requests`, every modality split and every unit field listed
under [units](#units-and-modality-splits). A zero numerator declares a free meter.
A priced modality split is charged at its own rate and removed from its parent
(`input_tokens` or `output_tokens`), like `cache_read_tokens`; when both a cache
subset and an input modality split are priced, cost is estimated only if the
priced cache subset is zero, because cached tokens can also be audio or image
tokens. A known, non-zero unit without a price makes the cost unknown.

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

If a priced meter's quantity is not reported for a record (for example `cache_read_tokens`
on a provider that omits cached tokens), that record's cost stays `unknown`; the ledger does
not treat the missing count as zero.

### Price catalogue

Without a price, cost is `unknown`. To start from public list prices, the `aiplex` binary
carries a pinned, trimmed snapshot of a third-party price list: LiteLLM's
`model_prices_and_context_window.json` (MIT licence; the notice is in
`THIRD_PARTY_NOTICES.md` at the repository root),
taken at one recorded commit on a recorded date. The snapshot is data in
`pkg/pricecatalog/snapshot.json`; `aiplex usage prices catalog` reads it and creates ordinary
`model-price` resources from the entries you choose.

```bash
aiplex usage prices catalog search "claude haiku"            # substring match on <provider>/<model>
aiplex usage prices catalog show anthropic/claude-haiku-4-5  # rates, source, what is not imported
aiplex usage prices catalog import anthropic/claude-haiku-4-5 --dry-run
aiplex usage prices catalog import anthropic/claude-haiku-4-5 --yes
aiplex usage prices catalog import --all-for-access-point team --yes
```

What to know before relying on it:

- **A snapshot, not a feed.** The prices are what a third party published at the snapshot date
  shown by every catalogue command. They can be stale or wrong. They are public list prices:
  discounts, commitments, negotiated rates and regional uplifts are not reflected. Cost computed
  from an imported price keeps the basis `estimated`; it is not an invoice. Compare it with your
  bill before using it for chargeback.
- **Nothing is priced automatically.** There is no fallback to the catalogue when cost is
  computed. A model you did not import stays `unknown`. On a terminal, `usage records` and
  `usage summary` print one line on stderr naming a catalogue model whose cost is unknown and the
  import command; `-o json` and pipes get nothing. Under `aiplex dev --provider ...` the summary
  names the catalogue entry for the wired model; it imports nothing.
- **Import shows exactly what it writes**: each meter as an exact decimal per million tokens and
  as the stored rational, the source URL and the snapshot date. It never overwrites an existing
  price unless `--replace` is given, and it will not add a price beside another one that already
  covers the same provider, model and batch selector (a tie matches nothing). Writing needs
  `--yes` when there is no terminal; `--dry-run` writes nothing.
- **Conversion is exact.** The source stores floats per token (for example `1.5e-07`). The
  generator reads the number's text, converts it with exact decimal arithmetic to a price per
  million tokens (`0.15`), and the import stores it at scale 6 as a rational (`3/20`). A meter
  the source does not state stays unset (unknown); a rate the source states as `0` is kept as a
  declared free meter.
- **Meters imported**: `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`,
  and, when the catalogue states batch rates, a second price with `batch: true` (id ending
  `-batch`). Reasoning tokens are never priced from the catalogue. Output tokens already include
  reasoning for OpenAI and Anthropic, but the ledger holds a record that reports reasoning tokens
  as unknown cost unless the price declares the meter; `--reasoning-included` declares it as a free
  meter on your say-so. Do not use it for providers that report reasoning separately from output.
- **What is not imported** is listed on `show` and `import`: per-image, per-second,
  per-character, per-page and per-query rates, tool and search surcharges, long cache lifetimes
  (for example one-hour Anthropic cache writes, which would be priced at the five-minute rate),
  and other service tiers (priority, flex), whose usage is priced at the standard rate.
  Pricing the price cannot express and that changes what a request costs (context-length tiers
  such as "above 200k tokens", and audio, image or video token rates) makes the import refuse
  unless you pass `--accept-partial`; the output then names the fields left out. Entries with only
  per-unit pricing (image, audio, video, rerank, OCR) are not in the snapshot.
- **Provider and model ids match what the ledger records.** Catalogue providers are `openai`,
  `anthropic`, `azure-openai`, `bedrock`, `gemini-developer`, `google` (Gemini on Vertex AI, whose
  records carry the Google publisher), `vertex-anthropic` (Claude on Vertex, recorded as
  `anthropic`) and the OpenAI-compatible vendors `deepseek`, `groq`, `mistral` and `xai` (recorded
  as `openai-compatible`). Model ids are the provider's own: routing prefixes such as `azure/`,
  `gemini/`, `vertex_ai/` and `bedrock/` are removed. Bedrock ids (including geography
  inference-profile ids such as `us.anthropic.claude-...`) are kept as written; region-specific,
  commitment and image-size entries are not in the snapshot. Vertex Claude entries are imported only
  in Vertex's versioned form (`claude-...@20251001`), because an unversioned id would collide with
  the first-party Anthropic id. Matching is exact: there is no fuzzy matching. Azure entries use the
  model name; if your deployment has another name, set that price yourself. A surface of an
  OpenAI-compatible endpoint is not matched to a vendor automatically; import it by name.
- **Rebuilding the snapshot.** `make update-price-catalog [COMMIT=<sha>]` pins a new upstream
  commit, checks that the licence text is unchanged, and rewrites the snapshot and its recorded
  checksum; a test fails if the embedded file does not match the checksum. The snapshot is about
  360 KB and lists 780 models; upstream rows that state two different prices for one id are left out.

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
| Quota reason codes | QuotaPlex refused the exchange before the provider was called. |
| `hook_denied` | A [request hook](request-hooks.md) refused the request (before the provider was called; no quota is reserved) or withheld the response (after it; usage is still recorded). Status `denied`, HTTP 403. |
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

## Capture and enablement

Capture needs no switch. Each completed exchange on these paths writes one record:

| Path | Record |
| --- | --- |
| `/api/v1/llm/native/{id}/*` and `/api/v1/llm/gateway/{id}/*` | `plane=llmplex`, `source=managed` |
| `/api/v1/a2a/connections/{id}/invoke` | `plane=a2aplex`, `source=managed` |
| `/api/v1/mcp/connections/{id}/invoke` | `plane=mcplex`, `source=managed` |
| `POST /api/v1/llm/usage` | `plane=llmplex`, `source=reported`. The route requires the admin role, and a verified tenant is needed to mirror the report. |

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
`--caller-kind`, `--status`, `--source`, `--error-class`, `--bound`, `--batch`, `--hook-decision`, `--since` and `--until`.
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
`http_status`, `error_class`, `usage_bound`, `batch`, `image_size`, `image_quality` and each hook run.

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
- A record carries only the scalar `cache_write_tokens`. The per-TTL cache-write counters
  (`cache_write_5m_tokens`, `cache_write_1h_tokens`, `cache_write_30m_tokens`,
  `cache_write_12h_tokens`) and the `server_tool_*` counters have no field in the record, so
  they are not in `usage`, summaries or price meters. They are used only by quota metering
  and strict settlement. See [model metering profiles](model-metering-profiles.md#native-usage-composition).
- Only managed paths and trusted reports are captured. Envoy routes, direct
  provider calls and other traffic that bypasses these paths produce no record.
- A reported record is only as accurate as its reporter. The ledger does not
  check it against the provider.
- `POST /api/v1/llm/usage` mirrors its counters into the ledger but does not copy
  the report's floating-point cost. The ledger cost comes from a price resource or
  is `unknown`.
- A price is not applied retroactively. Correcting a price does not correct stored
  records.
- Summaries group only on the fields listed above, and `group_by` is not a free-text search. There is no free-text search
  and no export route.
- Summaries count bounded records in full and report them in the `*_records` counts (see
  [bounded usage](#bounded-usage)). Records and summaries both filter on `usage_bound`.
- The memory store is not durable. Firestore-backed records follow the bundled
  90-day retention bound.
- Live and asynchronous job billing is outside this ledger. A managed request that
  starts a provider job records the request, not the job's later work.
- Currency conversion is not performed. Costs are reported per currency.

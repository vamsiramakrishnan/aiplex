# Native model execution and metering profiles

The managed model gateway selects an exact saved surface, native operation,
model, credential/network configuration and operator quota binding. Models are
not inferred from their name or enumerated in an embedded product list.
`modelapi.Coverage` describes the installed execution adapter. A surface still
chooses its own operations and supported models; the catalog does not grant
upstream availability, entitlement or runtime permissions.

## Coverage contracts

| Binding profile | Operations | Required token mappings | Optional token mappings |
| --- | --- | --- | --- |
| `request-count` | Explicit cataloged HTTP/SSE generation, prediction, embeddings, counting, supported native resource operations and job/cache creation; WebSocket sessions on session-metered surfaces | None; `requests` is required | — |
| `gemini-text` | Native model/endpoint `generateContent` and `streamGenerateContent` (Vertex, provider `google`; SSE or JSON array); Gemini Developer API `gemini.models.generateContent` / `gemini.models.streamGenerateContent` (provider `gemini-developer`) | `uncached_input_tokens`, `cached_tokens`, `output_tokens`, `thought_tokens` | — |
| `openai-text` | `openai.chat-completions`, `response.create`, OpenAI and Azure OpenAI Chat/Responses, `openai.v1.responses-websocket` (per admitted response), `compat.chat-completions` only with `provider.usage_semantics: "openai"` | `uncached_input_tokens`, `cached_tokens`, `output_tokens` | cache writes: `cache_write_tokens` **or** `cache_write_30m_tokens` + `cache_write_12h_tokens` |
| `anthropic-text` | `anthropic.messages`; Anthropic `rawPredict` / `streamRawPredict` on Vertex; Anthropic Messages bodies on Bedrock `bedrock.invoke` / `bedrock.invoke-stream` (provider `bedrock`) | `uncached_input_tokens`, `cached_tokens`, `output_tokens` | cache writes: `cache_write_tokens` **or** `cache_write_5m_tokens` + `cache_write_1h_tokens` |
| `bedrock-converse-text` | `bedrock.converse`, `bedrock.converse-stream` | `uncached_input_tokens`, `cached_tokens`, `output_tokens` | cache writes: `cache_write_tokens` **or** `cache_write_5m_tokens` + `cache_write_1h_tokens` |
| `vertex-openai-text` | `openai.chat-completions` on a Google surface (Vertex OpenAI-compatible endpoint) | `input_tokens`, `output_tokens` | — |
| `gemini-interaction-text` | Model-backed `interaction.create` (Vertex) and `gemini.interactions.create` (Developer API) | `uncached_input_tokens`, `cached_tokens`, `output_tokens`, `thought_tokens` | — |
| `embedding-input` | `openai.v1.embeddings`, `azure.v1.embeddings`, `azure.deployments.embeddings`, `gemini.models.embedContent`, `gemini.models.batchEmbedContents`, Vertex `models.predict` / `endpoints.predict` (embedding statistics), `models.embedContent` | `input_tokens` | — |
| `openai-realtime` | `openai.v1.realtime` per admitted response (token-metered surface) | `uncached_text_input_tokens`, `uncached_audio_input_tokens`, `uncached_image_input_tokens`, `cached_text_input_tokens`, `cached_audio_input_tokens`, `cached_image_input_tokens`, `text_output_tokens`, `audio_output_tokens` | — |
| `gemini-live-allowance` | `live.connect` per allowance window (token-metered surface) | at least one of `input_tokens`, `cached_tokens`, `output_tokens`, `thought_tokens`, `tool_use_tokens`, `total_tokens` | the others in that list |

A binding maps at most 12 meters including `requests`. Cache-write meters are
one complete option: the scalar `cache_write_tokens` (one price for every TTL)
or the provider's full per-TTL set (one price per TTL); mapping both, a partial
set or another provider's set is rejected at configuration. Disjoint input
subsets (uncached, cache read, each mapped cache write) each reserve the whole
attested input ceiling; the shared-ceiling check applies to their sum.

Every profile additionally maps `requests`. Mapped raw meters must be distinct;
a thought meter is deliberately rejected for the output-inclusive profiles.
Existing versioned price books price those raw units into explicit money meters.
Different token profiles need price books matching their actual meter sets:
a Gemini price book that requires a thought meter cannot be reused for an
output-inclusive profile by silently dropping that price input.

A request-count allocation counts the managed API exchange. For a batch, cache,
background response or long-running prediction, it counts the submission/read/
cancellation HTTP request, **not** generated tokens, retained storage, upstream
job lifetime or internal tool work. Its concurrency permit spans the managed
exchange. A configured request price is a fixed per-request tariff. Native
resource IDs and ownership are still bound before delivery, but API acceptance
is not proof that an upstream job has completed. Operations without an
installed coverage contract remain unsupported by the managed host.

## WebSocket sessions

Gemini Live (`live.connect`), OpenAI Realtime (`openai.v1.realtime`) and OpenAI
Responses WebSocket mode (`openai.v1.responses-websocket`) are configured per
surface with the `websocket` block of the saved surface spec:

| Field | Values | Default | Meaning |
| --- | --- | --- | --- |
| `websocket.metering` | `session`, `token` | `session` | How sessions are admitted (below). |
| `websocket.max_session_seconds` | 60..86400 | the gateway's maximum stream duration (1800) | Hard session duration; both sockets close when it elapses. |
| `websocket.token_allowance` | 1024..4194304 (`token` only) | 32768 | Gemini Live allowance window, held per mapped meter. |

`metering: "token"` is refused at save time for a surface that includes the
OpenAI Live voice operation (`openai.v1.live`): it is billed by session
seconds, which no token path observes, so it keeps session metering. The
provider-capacity hook (`Options.Admit`) runs once per session, at session
admission (Live: the first allowance window; Realtime/Responses: before the
upgrade), never per `response.create` or allowance renewal.

**Session metering (default)** keeps the request-count contract: one admission
prepared once the model is known (Live: after the bounded `setup`; Realtime:
the `?model=` connect parameter; Responses: at the upgrade, each
`response.create` re-authorized) and before credentials or the dial; one
settlement at session end; usage is advisory (`lower_bound`, `ambiguous` for a
resumed Live session, `partial` for non-token units). A token-profile lease is
refused (`UNSUPPORTED_EXECUTION_TRANSPORT`). Exposure: one admitted request and
one concurrency permit for at most the session duration; tokens unbounded.

**Token metering, OpenAI Realtime and Responses WebSocket mode** (per-response
admission). Each client `response.create` is its own exchange: it is admitted
(budgets, rates, concurrency, trusted exposure bound; idempotency key derived
from the session's key plus `/ws/<n>`), its dispatch is claimed, and only then
is the event forwarded. One response may be in flight; a second
`response.create`, a quota denial or an unbounded request (Realtime:
`response.max_output_tokens` must be an integer, `"inf"` is refused; tools,
out-of-band `input` and `conversation` selection are refused; Responses: the
`openai-text` request rules plus no `stream_id` lanes) is answered with the
protocol's own `error` event (`error.code` = the quota code, e.g.
`QUOTA_BUDGET_EXHAUSTED`) and dropped; the session stays open. `response.created`
binds the lease; the terminal event (Realtime `response.done` with status
`completed`, Responses `response.completed`) is settled on its exact usage and
acknowledged before it reaches the client. Cancelled, failed or incomplete
responses, a response without usage and a disconnect retain the hold as
unknown, as for an incomplete HTTP stream; a provider `invalid_request_error`
(Realtime) or a 4xx rejection status (Responses) before `response.created`
releases it. Realtime turn detection can create responses without a client
`response.create`, so a token-metered Realtime session must first send a
`session.update` with `turn_detection` null or `create_response: false` (GA
`session.audio.input.turn_detection` or beta `session.turn_detection`); every
other event before that, and any later update that re-enables automatic
responses, is refused natively. A response the gateway did not admit closes
the session with a policy-violation close. Exposure: every response is bounded
by its own admitted hold; nothing is generated outside an admitted hold.
Realtime settles on disjoint modality meters (`openai-realtime`):
`input_token_details` text/audio/image minus `cached_tokens_details`, and
`output_token_details` text/audio; every breakdown must add up to the reported
totals. Input-transcription usage (billed to the ASR model) stays advisory.

**Token metering, Gemini Live** (renewable allowance). Live `usageMetadata`
messages are session-cumulative snapshots whose composition Google does not
fully document. The first window lease holds `token_allowance` per mapped
meter at setup, before credentials and the dial. When any mapped counter has
consumed half of the window, the window settles exactly on the snapshot delta
(windows never overlap: one concurrency permit per session) and the next
window is admitted. A denied renewal sends `{"goAway":{"timeLeft":"0s"}}` and
closes both sockets with a policy-violation close. A clean close whose last
message was a snapshot settles the open window; an interruption, or model
output/input after the last snapshot, retains the open window as unknown.
Resumed sessions are refused in token mode. Omitted Live counters are 0
(proto3 scalar omission). **Worst-case exposure beyond admitted holds: one
allowance per mapped meter** — a snapshot jump larger than half a window
settles as a recorded overrun (debt that blocks further admissions, never
dropped), and after a denied renewal the usage generated between that snapshot
and the close has no hold and reaches the ledger only as `lower_bound`
telemetry. The `gemini-live-allowance` profile uses the surface allowance as
its exposure bound; no provider bound authority is involved.

## Strict text request contracts

All strict profiles require an exact model and observed native model identifier,
operator-attested provider-enforced input/output ceilings, unexpired evidence,
and configured prices for any money budget. The existing `provider_limits`
configuration applies to the exact binding/provider/model/version/account/region.
A configured maximum does not establish that a provider enforces it. Check the
actual native model contract before activation; tokenizer estimates are not
substitutes for a conservative exposure bound.

The profiles reserve a conservative input ceiling for each input subset and the
explicit requested output cap for each applicable output subset. They recheck
the evidence at dispatch and quarantine contradictory observed exposure. They
do not estimate exact input tokens from prompt bytes.

The current supported payloads are intentionally explicit:

- Gemini generation keeps the existing text-only, one-candidate contract.
- OpenAI Chat requires inline string message content, one completion and
  `max_completion_tokens`. Streaming additionally requires
  `stream_options.include_usage: true` so its final usage chunk is available.
- Responses requires inline string/text-message input, `max_output_tokens`, and
  explicit `store: false`. Tools, conversation/previous-response references,
  remote files, multimodal content and background execution are refused.
- Anthropic requires inline string message/system content and `max_tokens`.
  An explicit thinking budget must fit that output cap. When the binding maps a
  cache-write meter, text content blocks with `cache_control`
  (`{"type":"ephemeral"}`, optional `ttl` `5m`/`1h`), system text blocks and the
  top-level `cache_control` (automatic caching) are accepted; otherwise they are
  refused. Tools, image/document blocks, context-management work and unknown
  fields are refused. Anthropic Messages bodies on Bedrock InvokeModel follow
  the same rules.
- Bedrock Converse requires inline text content blocks, an optional text
  `system` and `inferenceConfig.maxTokens`; `cachePoint` blocks
  (`type: default`, optional `ttl` `5m`/`1h`) only with a cache-write meter.
  Tools, guardrails, documents, images and `additionalModelRequestFields` are
  refused.
- OpenAI-format `prompt_cache_key` and `prompt_cache_options` (`mode`
  `implicit`/`explicit`, `ttl`) are accepted only with a cache-write meter.
- Embeddings (`embedding-input`) require inline text input (OpenAI `input`
  string or up to 2048 strings; Gemini text `content` parts; Vertex predict
  `instances[].content` strings); the attested input ceiling is the only bound.
  The existing Messages client facade can translate to the corresponding native
  Vertex Anthropic operation; it uses the same quota admission and accounting.
- Interactions requires a direct model, one inline string input, a bounded
  generation configuration and explicit `store: false`. Agent selectors, tools,
  previous interactions, continuations, background work and media are refused.
  Both documented snake-case SDK and camel-case generation-limit spellings are
  accepted; conflicting aliases are refused.

Strict profiles reject `Anthropic-Beta` toggles and `Last-Event-ID` resumption.
Protocol headers that are forwarded are pinned into the invocation configuration
along with the provider configuration; changing them cannot reuse an admitted
idempotency key. A request-count profile makes no token-billing promise and can
forward supported native fields without acquiring a stricter profile implicitly.

## Native usage composition

Gemini reports prompt tokens including cached input, separate candidate/output
and thought counters, and a total. Settlement verifies that composition, removes
cached input from uncached input, and never adds prompt totals to their subsets.
Interactions uses its corresponding total input/cache/output/thought counters.

The Vertex OpenAI-compatible endpoint (`openai.chat-completions` on a Google
surface) reuses OpenAI field names with different semantics for Gemini:
`completion_tokens` **excludes** `reasoning_tokens` (prompt + completion +
reasoning = total). The observer folds reasoning into output only when the
native total proves that composition, keeps an inclusive composition when the
total proves that instead (open models), and otherwise leaves output unknown.
The endpoint documents no cached-input breakdown, so the strict `openai-text`
profile fails closed there unless the response carries one. The
`vertex-openai-text` profile settles total input and billed output only: the
native total must be reported when reasoning is present (it proves the
composition) and must equal input + output. Without a cache split an implicit
cache hit is charged at the operator's input price for `input_tokens`.

Usage-format conformance against provider-documented samples, and the
generated provider x surface x transport coverage matrix, are in
[the usage-format reference](reference/usage-formats/coverage.md).

OpenAI reports reasoning **inside** output/completion tokens. Its output meter
therefore retains the full billed output count; reasoning details are diagnostic
and are not charged a second time. Cached prompt tokens are a subset of input,
so uncached input is total input minus cached input. The strict profile requires
that cached-input breakdown and verifies the native total when supplied.

Anthropic input tokens exclude cache-read input. Its uncached meter is therefore
the native input count; cache-read input is a separate meter. Thinking remains
inside billed output. No separately billable thought count is invented.

Cache writes are a first-class meter. Composition, without double counting:
OpenAI and Azure input includes reads and writes (uncached = input − read −
write; Responses `cache_write_tokens_details.ttl_30m_tokens`/`ttl_12h_tokens`
"sum to `cache_write_tokens`"); Anthropic Messages, Vertex Anthropic and
Anthropic bodies on Bedrock InvokeModel report input exclusive of both (total
input = input + `cache_read_input_tokens` + `cache_creation_input_tokens`;
`cache_creation.ephemeral_5m_input_tokens` + `ephemeral_1h_input_tokens` equal
the scalar); Bedrock Converse `inputTokens` is "only the non-cached input
tokens" and `cacheDetails[{inputTokens, ttl: 5m|1h}]` breaks down the writes
(AWS prompt-caching guide). A breakdown that contradicts its scalar is refused.
A reported zero writes all TTL buckets as zero; positive writes without a
breakdown leave the buckets unknown, so a per-TTL binding fails closed. A
binding with no cache-write meter refuses positive writes at settlement
(`QUOTA_USAGE_UNMETERED`, exposure retained). An absent OpenAI-format
`cache_write_tokens` settles as 0 (documented default; models without
separately billed writes keep written tokens in ordinary input).

Server-tool quantities always fail closed, per counter: Anthropic
`server_tool_use.web_search_requests`, `web_fetch_requests` and
`code_execution_requests` (per-request billing; code execution also bills
container time that no usage object reports), any unknown `server_tool_use`
key, and Gemini `toolUsePromptTokenCount` / `total_tool_use_tokens`. The
documented counters are kept as `server_tool_*` ledger telemetry. The strict
profiles also refuse tool requests, so no exposure bound exists for them.

Bedrock Converse responses carry no model identifier. Under a strict profile
the versioned `modelId` the gateway dispatched to (the URL path selector,
checked against the surface) is the observed identity; embeddings without a
model in the response use the dispatched model the same way.

Gemini `cachedContentTokenCount` omitted from a response is 0: it is a plain
proto3 `int32` without presence (`google/cloud/aiplatform/v1/prediction_service.proto`,
`google/ai/generativelanguage/v1beta/generative_service.proto`) and the
[proto3 JSON mapping](https://protobuf.dev/programming-guides/json/) omits
default values.

## Provider rejections

The same rule applies to the media and batch operations; for a batch only the
create exchange can be released this way (later polls and results fetches are
separate attempts and never release an open batch, which stays `unknown` with
reason `batch_results_pending` until its results settle it).
A provider status that its documentation defines as refused before generation
releases the hold: the exchange settles as one request with zero consumption
and the provider's status, error body and `Retry-After` reach the client
unchanged (subject to the surface's header passthrough). Other failures keep
exposure retained as unknown.

| Provider | Released (not processed) | Retained (ambiguous) |
| --- | --- | --- |
| OpenAI, Azure OpenAI, OpenAI-compatible | 400, 401, 403, 404, 413, 422, 429 | 5xx incl. 503, cut connections, errors after a 2xx |
| Anthropic | 400, 401, 402, 403, 404, 413, 429, 529 `overloaded_error` | 500 `api_error`, 504 `timeout_error`, mid-stream `error` events |
| Google (Vertex incl. partner rawPredict, Gemini) | 400, 401, 403, 404, 429 `RESOURCE_EXHAUSTED` | 500, 503, 504 |
| Bedrock | 400 `ValidationException`/`ServiceQuotaExceededException`, 403, 404, 429 `ThrottlingException`/`ModelNotReadyException` | 408 `ModelTimeoutException`, 424 `ModelErrorException`, 500, 503 |

The provider's actual native model identifier is recorded separately from the
configured target. Missing or different required proof retains exposure and
quarantines that binding generation; it never silently selects a different price
book. Identifier granularity is provider-specific. Deployment must establish that
the configured native identifier and attestation represent the intended version;
a stable alias alone does not prove that its serving implementation cannot change.

## Completion and streaming

JSON settlement requires a valid native terminal response and complete usage.
OpenAI Chat SSE waits for a native finish marker, its final usage chunk, `[DONE]`
and clean EOF. Responses and Interactions use their native terminal events.
Anthropic SSE preserves the input facts from `message_start`, merges cumulative
`message_delta` usage without summing repeated counts, requires a final usage
update and `message_stop`, and then waits for clean EOF. A stop marker alone is
not a usage report. New data after the Anthropic stop marker is refused.

Final native frames remain buffered until terminal evidence is durably
acknowledged. Earlier content and HTTP headers may already have been sent;
an incomplete stream must be treated as failure even if its HTTP status is 200.
Version-only/drift metadata is acknowledged before rejection. A clean final
response submits version and usage together so quarantine can preserve the known
terminal facts. Truncated streams do not manufacture terminal usage or release
uncertain holds.

Every cumulative update is checked, including updates inside the withheld final
tail. Later counters cannot replace higher observed usage; repeated equal
counters are not summed. Contradictions retain unknown exposure and suppress the
final tail. Request-count profiles keep token telemetry advisory. See the
[execution coverage and regression matrix](quotaplex-execution-coverage.md).

The local regression suite covers native JSON/SSE, client facade translation,
exact money composition, retry exclusion and unsupported exposure. It does not
prove billing equivalence, native model availability or live cloud entitlement.

## Primary contracts

- [OpenAI output token counting](https://developers.openai.com/api/docs/guides/token-counting)
- [OpenAI Responses creation and usage](https://developers.openai.com/api/reference/resources/responses/methods/create)
- [Anthropic Messages reference](https://platform.claude.com/docs/en/api/http/messages)
- [Anthropic streaming lifecycle](https://platform.claude.com/docs/en/build-with-claude/streaming)
- [Google Cloud Interactions API](https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/models/interactions-api)
- [Google Interactions generation configuration](https://googleapis.github.io/js-genai/release_docs/types/gaos_models_interactions_generation-config.GenerationConfig.html)
- [Google model output/context semantics](https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/guides/gemini-4-argon)

## Unit and media profiles

Media operations report usage that is not plain text tokens. The gateway observes
it as first-class counters held beside the token counters (never added to them):
`audio_input_milliseconds`, `voice_session_milliseconds`,
`video_output_milliseconds`, `generated_images`, and the per-modality token splits
`input_{text,audio,image,video}_tokens` and `output_{text,audio,image}_tokens`.
The same names are usage ledger fields, `model-price` meters and quota binding
counters. See [usage ledger units](usage-ledger.md#units-and-modality-splits).

| Operation | Surface path | Observed | Quota profile |
| --- | --- | --- | --- |
| `openai.v1.audio-transcriptions`, `openai.v1.audio-translations` | `POST /v1/audio/{transcriptions,translations}` (multipart; JSON or SSE) | `usage.type: "tokens"`: input/output/total plus `input_token_details` audio/text splits; `usage.type: "duration"`: `seconds` as `audio_input_milliseconds` (rounded up). SSE usage only on `transcript.text.done`. Translations document no usage: unknown unless reported in the same shape. | `media-units` (`audio_input_milliseconds` bounded by the trusted bounds authority) or `request-count` |
| `openai.v1.audio-speech` | `POST /v1/audio/speech` | Raw audio relayed without buffering; SSE relayed. No usage is documented for either (SSE event names unconfirmed in OpenAI's reference). | `request-count` |
| `openai.v1.images-generations`, `openai.v1.images-edits` | `POST /v1/images/{generations,edits}` (JSON; edits multipart; SSE with partial images) | `generated_images` = images returned (JSON `data[]`, or `*.completed` events in a stream); GPT image models' token usage and image/text splits; `size`/`quality` as `image_size`/`image_quality`. With more than one completed stream event, tokens stay unknown (per-image vs cumulative unconfirmed). | `media-units` (`generated_images` bounded by the request's `n`, default 1, at most 10) or `request-count` |
| `openai.v1.live` | `GET /v1/live/sessions?model=` (WebSocket) | `voice_session_milliseconds` from cumulative `session.usage.updated` snapshots (replaced, never summed), final only with `session.closed`; nested `response.event`/`decision.event` token usage counted once per response. | `request-count` only (existing session admission, unchanged) |
| `gemini.models.predictLongRunning`, `gemini.operations.get` | `POST /v1beta/models/{model}:predictLongRunning`, `GET /v1beta/models/{model}/operations/{id}` | Nothing: the done result reports neither usage nor duration, so `video_output_milliseconds` stays unknown. Operation names are bound to the caller. | `request-count` (asynchronous) |
| Gemini `generateContent`, Live | existing | `promptTokensDetails`, `candidatesTokensDetails`, `responseTokensDetails` modality entries as modality splits (subsets). | unchanged |

Multipart uploads are spooled once to a private temporary file while a multipart
reader walks the copy: form fields (at most 64, 64 KiB each) are kept for
validation, authorization and the quota payload digest; file parts are only read
to advance the parser and are never parsed, held in memory or logged. The spooled
bytes are relayed upstream unchanged. `Options.MaxUploadBytes` bounds the upload
(default 32 MiB, at most 1 GiB); a larger upload is refused with 413 before any
reservation or provider I/O. The quota payload is the canonical JSON of the form
fields, not the file bytes.

A `media-units` binding maps `requests` plus any of `generated_images` (meter unit
`images`), `audio_input_milliseconds` / `video_output_milliseconds` (unit
`milliseconds`) and `input_tokens` / `output_tokens` (unit `tokens`). Quantities
fixed by the request and enforced by the provider (image `n`) are derived from the
native body; every other mapped meter needs the trusted bounds authority, as token
profiles do. Settlement uses the terminal observation; a response that omits a
bound meter (for example a token-billed transcription under a duration binding)
fails closed: the attempt stays unknown with its exposure and the response is not
delivered as settled.

## Provider batches

| Operation | Path |
| --- | --- |
| `openai.v1.files-create` | `POST /v1/files` (multipart, `purpose=batch` only; the file id is bound to the caller) |
| `openai.v1.batches-create`, `-get`, `-cancel`, `-list` | `/v1/batches[/{id}[/cancel]]` (create requires an input file the caller uploaded; list requires shared-admin authorization) |
| `openai.v1.files-content` | `GET /v1/files/{id}/content` (only output/error files of a batch the caller owns) |
| `anthropic.message-batches-create`, `-get`, `-cancel`, `-list`, `-results` | `/v1/messages/batches[/{id}[/cancel or /results]]` (every request's model must be allowed) |
| `gemini.models.batchGenerateContent`, `gemini.batches.get`, `gemini.batches.cancel` | `/v1beta/models/{model}:batchGenerateContent`, `/v1beta/batches/{id}[:cancel]` |

Vertex batch prediction output is written to Cloud Storage or BigQuery, which the
gateway does not read, so a Vertex batch cannot settle through it; `batch.create`
stays request-count.

**Admission.** A `batch-usage` binding on a batch-create operation admits the job
once, at create, with a trusted exposure bound: `requests` = 1, `batch_requests`
= the inline request count when the body lists requests (Anthropic), and every
other mapped meter (`input_tokens`, `output_tokens`, `cached_tokens`,
`cache_write_tokens`, or `batch_requests` for OpenAI's file input) from the
trusted bounds authority. The create response's batch id is bound to the caller,
pinned on the attempt and written to a durable batch record (tenant resource kind
`model-batch`) before the id is released. The attempt then stays open: state
`unknown`, reason `batch_results_pending`, holding its exposure and its
concurrency permit (size concurrency rules that cover batch bindings
accordingly). A provider rejection of the create (4xx other than 408/429) is
provider evidence that no job exists and settles one request with zero for every
other meter.

**Settlement, exactly once.** Usage is taken only from results fetched through
the gateway: the Anthropic results stream, the OpenAI output and error files, or
the Gemini inline responses in a terminal batch object. A results body counts only
if it ends cleanly and every line parses; a cut or malformed body changes nothing.
The record is complete when the parsed items account for every item the provider
reported (Anthropic: the inline create count, checked against the ended
`request_counts`; OpenAI: `request_counts.total` of a terminal batch, covered by
output plus error lines; Gemini: `batchStats.requestCount`), with no duplicate
`custom_id`. Usage is the per-counter sum over succeeded items; a counter missing
on any succeeded line has an unknown sum. Errored, cancelled and expired items
contribute nothing because the results say so; an item without a line is never
assumed released. The OpenAI batch object's `usage` is compared with the per-line
sum and never added; a disagreement leaves the batch unsettled and flags the
record (`cross_check_mismatch`).

On completion the gateway settles the attempt with deterministic evidence (event
id derived from the attempt, producer `native-batch`), then compare-and-swaps the
batch record to settled. Quota settlement is idempotent: a repeated fetch
re-acknowledges the same evidence, and different totals are refused as a conflict.
Only the exchange that wins the swap writes the batch usage to its ledger record
(marked `batch`, attributed to the batch's attempt and model), so a second or
concurrent fetch never settles or records twice. The final results line is held
until the settlement is acknowledged; if quota cannot acknowledge, it is withheld.
If the binding's meters are not all reported (for example cache writes absent on
a line), the results are delivered and the batch keeps its exposure. A batch whose
results are never fetched keeps its admitted exposure and appears in the existing
aging/unknown reporting.

Batch pricing is the operator's: every batch exchange's ledger record has
`batch: true`, so a `model-price` with `"batch": true` matches it in preference to
the standard price. No discount is built in.

**Managed Agents** session usage is not observed: the session object's cumulative
`usage` is documented, but the `session.usage` event envelope is not shown and a
session has no single terminal exchange through which a snapshot could settle once
without double counting repeated polls.

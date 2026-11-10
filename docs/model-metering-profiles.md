# Native model execution and metering profiles

The managed model gateway selects an exact saved surface, native operation,
model, credential/network configuration and operator quota binding. Models are
not inferred from their name or enumerated in an embedded product list.
`modelapi.Coverage` describes the installed execution adapter. A surface still
chooses its own operations and supported models; the catalog does not grant
upstream availability, entitlement or runtime permissions.

## Coverage contracts

| Binding profile | Operations | Required token mappings |
| --- | --- | --- |
| `request-count` | Explicit cataloged HTTP/SSE generation, prediction, embeddings, counting, supported native resource operations and job/cache creation | None; `requests` is required |
| `gemini-text` | Native model/endpoint `generateContent` and `streamGenerateContent` | `uncached_input_tokens`, `cached_tokens`, `output_tokens`, `thought_tokens` |
| `openai-text` | `openai.chat-completions`, `response.create` | `uncached_input_tokens`, `cached_tokens`, `output_tokens` |
| `anthropic-text` | `anthropic.messages`, or Anthropic model `rawPredict` / `streamRawPredict` on Vertex | `uncached_input_tokens`, `cached_tokens`, `output_tokens` |
| `gemini-interaction-text` | Model-backed `interaction.create` | `uncached_input_tokens`, `cached_tokens`, `output_tokens`, `thought_tokens` |

Every profile additionally maps `requests`. Mapped raw meters must be distinct;
a thought meter is deliberately rejected for the two output-inclusive profiles.
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
Responses WebSocket mode (`openai.v1.responses-websocket`) are covered by the
`request-count` profile only. A session maps onto the one-exchange model:

- **Admission**: one lease per session, prepared once the session's model is
  known (Live: after the bounded `setup` message; Realtime: the `?model=`
  connect parameter; Responses: no model at upgrade, so the lease is prepared at the upgrade and each `response.create` is re-authorized, not admitted again) and
  before credentials or the upstream dial. A token-profile lease is aborted and
  the session refused (`UNSUPPORTED_EXECUTION_TRANSPORT`).
- **Observation**: usage server events are forwarded as advisory, non-terminal
  observations: Live `usageMetadata` as cumulative session snapshots (last
  replaces earlier, never added), Realtime `response.done` and Responses
  `response.completed` as per-response deltas summed over distinct response ids.
  Realtime input-transcription usage is a separate `transcription_*` meter
  (billed at the transcription model's rate) and is never merged into the
  session's token counters. Realtime `rate_limits.updated` feeds the provider
  limit observation.
- **Settlement**: exactly once at session end. Only a clean close with complete
  evidence (every started response finished with usage; no Live model output,
  turn signal or client input after the last snapshot) yields a terminal
  observation with session totals. Otherwise the observed usage is labelled
  `lower_bound`, `ambiguous` (a resumed Live session, whose counter reset is
  unconfirmed) or `partial` (non-token units such as transcription seconds),
  and the ledger records the exchange as `unknown` with that `usage_bound`.
  Either way the request-count lease settles its one request and releases its
  permit.
- **Exposure bound**: per session, one admitted request against request
  budgets/rates and one concurrency permit for at most the gateway's
  `MaxStreamDuration` (default 30 minutes, at most 24 hours), after which both
  sockets are closed. Tokens on these paths are not bounded by quota, only by
  provider session limits and that duration; no token-metered claim is made.

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
  An explicit thinking budget must fit that output cap. Tools, cache controls,
  image/document blocks, context-management work and unknown fields are refused.
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
profile fails closed there.

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

Positive cache-write/cache-creation quantities and native server-tool quantities
are currently outside the output-inclusive strict profiles. They fail closed
rather than using ordinary input prices. These dimensions need their own
attested exposure and price contracts. Thus a provider/version that automatically
reports cache writes cannot acquire this token-accounting profile simply because
it speaks an otherwise compatible Messages/Responses protocol.

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

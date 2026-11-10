# OpenAI usage and rate-limit formats

Purpose: let an engineer audit AIPlex usage observers against what OpenAI documents. Source: OpenAI's official docs, retrieved 2026-11-09 (API reference pages under `https://developers.openai.com/api/reference/resources/...` and guides under `https://developers.openai.com/api/docs/guides/...`). Details the documentation does not state are marked UNCONFIRMED. Model names in doc examples are placeholders and are not repeated here.

Fixtures for each section live in `pkg/modelapi/testdata/usage-formats/openai/` (see `expected.json` there).

Base URL `https://api.openai.com`. Reference URL prefix used below: `REF = https://developers.openai.com/api/reference/resources`, `GUIDE = https://developers.openai.com/api/docs/guides`.

## Counter vocabulary (read first)

| Family | Input | Output | Total | Detail objects |
|---|---|---|---|---|
| Chat Completions (`usage`) | `prompt_tokens` | `completion_tokens` | `total_tokens` | `prompt_tokens_details{audio_tokens, cache_write_tokens, cached_tokens, image_tokens, text_tokens}`, `completion_tokens_details{accepted_prediction_tokens, audio_tokens, reasoning_tokens, rejected_prediction_tokens, text_tokens}` |
| Responses (`usage`) | `input_tokens` | `output_tokens` | `total_tokens` | `input_tokens_details{cached_tokens, cache_write_tokens, cache_write_tokens_details{ttl_30m_tokens, ttl_12h_tokens}}`, `output_tokens_details{reasoning_tokens}` |
| Realtime (`response.usage`) | `input_tokens` | `output_tokens` | `total_tokens` | `input_token_details{text_tokens, audio_tokens, image_tokens, cached_tokens, cached_tokens_details{text_tokens, audio_tokens, image_tokens}}`, `output_token_details{text_tokens, audio_tokens}` |
| Embeddings | `prompt_tokens` | none | `total_tokens` | none |
| Transcription | `input_tokens` | `output_tokens` | `total_tokens` | `input_token_details{audio_tokens, text_tokens}`, `type: "tokens"`; or `{type: "duration", seconds}` |
| Images (GPT image models) | `input_tokens` | `output_tokens` | `total_tokens` | `input_tokens_details{image_tokens, text_tokens}`, `output_tokens_details{image_tokens, text_tokens}` |

Semantics the docs state or imply:

- Cached tokens are a subset of input. Chat: `cached_tokens` = "Cached tokens present in the prompt". Realtime: "Cached tokens here are counted as a subset of input tokens, meaning input tokens will include cached and uncached tokens." Responses: prompt-caching guide computes ordinary input as `input_tokens - cached_tokens - cache_write_tokens` (example `12000 - 4000 - 7000`), so input includes both cache reads and cache writes. (GUIDE/prompt-caching)
- Cache writes exist as counters: Chat `prompt_tokens_details.cache_write_tokens` ("The unadjusted number of prompt tokens written to cache"), Responses `input_tokens_details.cache_write_tokens` plus `cache_write_tokens_details.ttl_30m_tokens` / `ttl_12h_tokens` (these "do not overlap and sum to `cache_write_tokens`"). Billed only for models supporting `prompt_cache_options`; earlier models have no extra write charge (GUIDE/prompt-caching). `cached_tokens` "counts billed reads and excludes tokens charged as writes". Whether older models populate `cache_write_tokens` at all: UNCONFIRMED.
- Reasoning tokens are inside output: Chat docs say predicted-output rejected tokens "like reasoning tokens ... are still counted in the total completion tokens"; reasoning guide shows `output_tokens` 1186 containing `reasoning_tokens` 1024 and `total_tokens` = input + output (GUIDE/reasoning). Do not add `reasoning_tokens` to output.
- Detail objects are optional; missing means unknown, not zero. Many detail fields are documented "default 0".

## 1. Chat Completions

Source: `REF/chat/subresources/completions/methods/create`, `REF/chat/subresources/completions/streaming-events`.

`POST /v1/chat/completions`. Model id: top-level `model` on the response and on every chunk; request `model` too.

| Transport | Usage location | Counters / semantics | Terminal signal | Absent-usage cases | Source |
|---|---|---|---|---|---|
| JSON | `usage` (optional per schema) | `prompt_tokens`, `completion_tokens`, `total_tokens` (prompt+completion), details above. `prompt_tokens` includes cached and cache-write; `completion_tokens` includes reasoning. | `choices[].finish_reason`: `stop`, `length`, `tool_calls`, `content_filter` seen (docs list "or 2 more"; the fifth value is UNCONFIRMED). | Schema marks `usage` optional; when it is omitted is UNCONFIRMED. Non-2xx has no usage. | REF/chat/.../methods/create |
| SSE (`stream:true`) | Only the extra chunk emitted when `stream_options.include_usage:true`: "an additional chunk will be streamed before the `data: [DONE]` message", its `usage` is for the whole request and its `choices` is "always an empty array". | Same counters as JSON. Every other chunk has `usage:null`. | The `finish_reason` chunk is not the end; the usage chunk follows it, then `data: [DONE]`. | `include_usage` not set (no usage at all). Stream interrupted or cancelled: "you may not receive the final usage chunk". Whether tokens generated before an interruption are billed or retrievable elsewhere: UNCONFIRMED. | REF/chat/.../streaming-events |

Chunk fields seen: `id` (same on every chunk), `object: "chat.completion.chunk"`, `created`, `model`, `system_fingerprint`, `choices[]{index, delta, finish_reason, logprobs}`, `usage`, `obfuscation` (present by default, disable with `stream_options.include_obfuscation:false`; it is padding, not usage). Chunk-level `service_tier` is mentioned for the request (`service_tier` response field) but whether it appears on chunks is UNCONFIRMED. Wire framing is `data: <json>\n\n` per chunk, `data: [DONE]` last (framing itself seen in the `[DONE]` description only; the blank-line separator is standard SSE, UNCONFIRMED in these pages). Fixtures: `chat.json`, `chat.sse`, `chat.interrupted.sse`, `chat.nousage.sse`.

Legacy `POST /v1/completions` (`REF/completions/methods/create`): same `stream_options.include_usage` text and `CompletionUsage`. Fixture not provided.

## 2. Responses API

Sources: `REF/responses/methods/create`, `REF/responses/streaming-events`, `REF/responses/methods/retrieve`, `GUIDE/background`.

`POST /v1/responses`. Model id: `response.model` (also in every embedded response snapshot). Status enum on the response object: `completed`, `failed`, `in_progress`, `cancelled`, `queued`, `incomplete`. `error{code,message}` for failures, `incomplete_details.reason` in `max_output_tokens`, `max_messages`, `content_filter`, `steered`.

| Transport | Usage location | Counters / semantics | Terminal signal | Absent-usage cases | Source |
|---|---|---|---|---|---|
| JSON | `usage` (`ResponseUsage`, nullable) | `input_tokens` (includes cached and cache-write), `input_tokens_details.{cached_tokens, cache_write_tokens, cache_write_tokens_details.{ttl_30m_tokens, ttl_12h_tokens}}`, `output_tokens` (includes reasoning), `output_tokens_details.reasoning_tokens`, `total_tokens`. | `status` terminal: `completed`/`failed`/`incomplete`/`cancelled`. | `usage` is `null` for `queued`/`in_progress`; docs examples show `null` on failed/incomplete. | REF/responses/methods/create |
| SSE (`stream:true`) | `response.completed` event: `response.usage`. `response.created` and `response.in_progress` carry `usage:null`. No `stream_options.include_usage` exists for Responses (only `include_obfuscation` seen). | As JSON. | One of `response.completed`, `response.failed`, `response.incomplete`; also standalone `error` event `{type:"error", code, message, param, sequence_number}`. Every event has `sequence_number`. | Failed and incomplete docs examples have `usage:null` (real incomplete behaviour UNCONFIRMED). Stream ends on `error` or connection loss: unknown. Cancel of a synchronous response: "you terminate the connection" (no usage). | REF/responses/streaming-events |
| Background + poll | `GET /v1/responses/{id}` returns the response object; usage as JSON once terminal. | Poll while `queued`/`in_progress`; cancel via `POST /v1/responses/{id}/cancel` (idempotent; repeat calls return the final object). | Leaving `queued`/`in_progress`. | Usage availability for background responses is not stated in the guide: UNCONFIRMED. Data retained about 10 minutes unless `store:true` (modified abuse monitoring) so a late poll can 404. | GUIDE/background; REF/responses/methods/retrieve |
| Background + stream | `background:true, stream:true`; resume with `GET /v1/responses/{id}?stream=true&starting_after=<sequence_number>`. | Replayed events are the same events: a resumed stream can re-deliver `response.completed`, so de-duplicate by response id. | Same terminal events. Keepalive events are not resume positions. | Time to first token higher in background mode. | GUIDE/background |
| WebSocket mode | Same events as SSE; usage on `response.completed`. Client sends `response.create` (same fields as POST, plus `stream_id`, `generate`); `stream` and `background` not used. | One connection carries many sequential responses and up to 16 concurrent lanes (`stream_id`); steering can end a response `incomplete` with `incomplete_details.reason:"steered"` followed by a successor `response.created`. | `response.completed`/`failed`/`incomplete`; `error` events carry `status`, optional `stream_id`; named errors include `previous_response_not_found`, `invalid_stream_id`, `websocket_stream_limit_reached`, `websocket_connection_limit_reached` (60 minute limit). | Connection drop mid-response. A `generate:false` warmup returns a response id; its usage is UNCONFIRMED. No in-band rate-limit event documented. | GUIDE/websocket-mode; REF/responses/websocket-events |

Other usage-bearing fields on the Response object:

- `tool_usage{image_gen{input_tokens, input_tokens_details{image_tokens,text_tokens}, output_tokens, total_tokens, cached_tokens?, cached_tokens_details?, output_tokens_details?}, web_search{num_requests}}`: "Usage reported by hosted tools, separate from base model usage"; may be omitted; image_gen "sums available token counts" and "may skip" calls with incomplete records; `web_search.num_requests` counts billable searches only. Do not add this to `usage`.
- `svg_usage{input_tokens, model, output_tokens, reasoning_tokens, total_tokens}` on image-generation related items: separate SVG authoring meter (seen on several item schemas; exact parent UNCONFIRMED).
- `POST /v1/responses/input_tokens` returns `{object:"response.input_tokens", input_tokens}`: a count, not billed usage.

Fixtures: `responses.json`, `responses.sse`, `responses.failed.sse`, `responses.incomplete.sse`, `responses.error.sse`, `responses.background.queued.json`, `responses.background.cancelled.json`, `responses.ws.jsonl`, `responses.interrupted.ws.jsonl`.

## 3. Realtime API (conversational)

Sources: `REF/realtime/server-events`, `GUIDE/voice-latency-cost` (Realtime tab), `GUIDE/voice-sip`, `GUIDE/voice-webrtc`, `GUIDE/realtime`.

Transports: WebSocket `wss://api.openai.com/v1/realtime` (server-side; SIP calls attach with `wss://api.openai.com/v1/realtime?call_id={call_id}`); WebRTC via `POST /v1/realtime/calls` with the JSON events on a data channel named `oai-events` (the `oai-events` name was seen in the Live WebRTC guide; for Realtime WebRTC specifically UNCONFIRMED); SIP via webhook plus `POST /v1/realtime/calls/{call_id}/accept|reject|refer|hangup`. Model id: session configuration (`session.created`/`session.updated`); not on `response.done` in the documented object (UNCONFIRMED where an observer should read the model for a response).

| Transport | Usage location | Counters / semantics | Terminal signal | Absent-usage cases | Source |
|---|---|---|---|---|---|
| WebSocket / WebRTC data channel / SIP sideband | `response.done` event: `response.usage` (optional). `response.created` has `usage:null`. | Per response, billing-accurate ("will correspond to billing"). `input_tokens` includes `input_token_details.cached_tokens` (subset), splits `text_tokens`/`audio_tokens`/`image_tokens`; `cached_tokens_details` splits cached by modality; `output_tokens` = `output_token_details.text_tokens + audio_tokens`; `total_tokens`. Each turn re-sends the conversation, so later turns' input includes earlier turns' output; summing per response is correct and large by design. No cache-write or reasoning counters documented. | `response.done`, "Always emitted, no matter the final state". `response.status`: `completed`, `cancelled`, `failed`, `incomplete`, `in_progress`; `status_details{type, reason, error{code,message,type}}`; reason in `turn_detected`, `client_cancelled`, `max_output_tokens`, `content_filter`. | Cancelled/failed/incomplete responses: usage presence UNCONFIRMED. Connection loss before `response.done`. Transport equivalence of events across WebSocket/WebRTC/SIP: the docs describe the same server events, byte-identity UNCONFIRMED. | REF/realtime/server-events; GUIDE/voice-latency-cost |
| Input transcription (same sockets) | `conversation.item.input_audio_transcription.completed`: `usage` | Separate meter billed at the ASR model's rate ("rather than the realtime model's pricing"): `{type:"tokens", input_tokens, output_tokens, total_tokens, input_token_details{audio_tokens,text_tokens}}` or `{type:"duration", seconds}`. Must not be merged into `response.done` totals unless intentionally attributed to the ASR model. | n/a | `...transcription.failed` has no usage. | REF/realtime/server-events |
| Translation / streaming transcription sessions | No per-response usage. Docs: these sessions "don't use the normal Response lifecycle" and are billed by audio duration. | Events seen: `session.created/updated/closed`, `session.input_transcript.delta`, `session.output_transcript.delta`, `session.output_audio.delta`, `error`. No usage field. | `session.closed` | Duration is not reported in-band in anything seen: UNCONFIRMED. | REF/realtime/translation-server-events; GUIDE/voice-latency-cost |

In-band errors: `{type:"error", event_id, error{type, code, message, param, event_id}}`; "Most errors are recoverable and the session will stay open". Not usage.

Fixtures: `realtime.ws.jsonl`, `realtime.cancelled.ws.jsonl`, `realtime.interrupted.ws.jsonl`.

## 4. Live API (voice sessions billed by duration)

Sources: `REF/live/primary-websocket`, `GUIDE/voice-latency-cost` (GPT-Live tab), `GUIDE/voice-webrtc`, `GUIDE/voice-sip`.

Primary WebSocket `wss://api.openai.com/v1/live/sessions` (send `session.start`, wait for `session.started`); WebRTC session creation `POST /v1/live/sessions` (data channel `oai-events`); sideband `wss://api.openai.com/v1/live/sessions/{session_id}/attach`. Model id: `session.model` in `session.start` and in the `session` object of `session.closed`. Names of some of these endpoints were seen only in guides.

| Transport | Usage location | Counters / semantics | Terminal signal | Absent-usage cases | Source |
|---|---|---|---|---|---|
| WebSocket / WebRTC / SIP sideband | `session.usage.updated` `{usage{seconds}, context_window{usage_ratio}}`; final `session.closed` `{reason, session, usage{seconds}}` | `seconds` (number) is cumulative voice duration; "Each update replaces the previous duration snapshot. Do not sum the snapshots." Record `session.closed` `usage.seconds` once. `context_window.usage_ratio` is not billing. | `session.closed`; reasons `close_requested`, `expired`, `content`, `remote_hangup`, `connection_lost`. | No `session.closed` (docs: closing the connection "does not confirm successful finalization"; final usage unconfirmed). WebRTC creation bills 15 seconds up front, credited against the running session (do not add). | REF/live/primary-websocket; GUIDE/voice-latency-cost |
| Delegated Responses backend (same socket) | `response.event` wraps a Responses streaming event (`event.type` e.g. `response.completed`) plus `delegation_id`. Token usage is `event.response.usage` on the nested `response.completed` | Same as Responses section. "Count each backend response once, using its response ID". Lifecycle snapshots omit input/instructions/tools/output. | nested `response.completed`/`failed`/`incomplete` | Nested events may arrive without `delegation_id`. | same |
| Decision API (same socket) | `decision.event` -> `event.decision.usage` | Responses-style `ResponseUsage` (`input_tokens`, `input_tokens_details{cached_tokens, cache_write_tokens}`, `output_tokens`, `output_tokens_details{reasoning_tokens}`, `total_tokens`) with `decision.model`. | `event.type: "decision.completed"` | n/a | REF/live/primary-websocket |

Fixtures: `live.ws.jsonl`, `live.interrupted.ws.jsonl`.

## 5. Embeddings

`POST /v1/embeddings` (`REF/embeddings/methods/create`). Model id: top-level `model`. Usage `usage{prompt_tokens, total_tokens}`; units tokens; no output counter. Terminal: the HTTP response. Usage absent: error responses only. Limits seen: 8192 tokens per input, 300,000 tokens per request. Fixture: `embeddings.json`.

## 6. Audio

- Transcription `POST /v1/audio/transcriptions` (`REF/audio/subresources/transcriptions/methods/create`). Response is `Transcription{text, languages, logprobs, usage}` where `usage` is `Tokens{type:"tokens", input_tokens, output_tokens, total_tokens, input_token_details{audio_tokens,text_tokens}}` or `Duration{type:"duration", seconds}` (unit: seconds of input audio). Diarized responses have the same union plus top-level `duration`. Model id is not in the response (request only). Verbose (non-union) shape has `usage{type:"duration", seconds}` only. Streaming (`REF/audio/subresources/transcriptions/streaming-events`): events `transcript.text.delta`, `transcript.text.segment`, `transcript.text.done`; usage only on `transcript.text.done` with the `tokens` shape (duration variant on streams UNCONFIRMED). Absent: stream cut before `transcript.text.done`. Fixtures: `transcription.json`, `transcription.duration.json`, `transcription.sse`, `transcription.interrupted.sse`.
- Translation `POST /v1/audio/translations` (`REF/audio/subresources/translations/methods/create`): no `usage` field found on the page. Billing unit UNCONFIRMED.
- Speech `POST /v1/audio/speech` (`REF/audio/subresources/speech/methods/create`): returns raw audio (`application/octet-stream`) by default, or SSE with `stream_format:"sse"` (not supported for `tts-1`/`tts-1-hd`). No usage in the response body or headers is documented; SSE event names and any usage payload are not documented (the speech guide does not document SSE). UNCONFIRMED. The speech guide notes `tts-1`, `tts-1-hd`, `gpt-4o-mini-tts` shut down 2027-01-06. No fixture (nothing to build faithfully).

## 7. Image generation

`POST /v1/images/generations`, `/v1/images/edits` (`REF/images/methods/generate`, `REF/images/generation-streaming-events`, `REF/images/edit-streaming-events`). JSON: `usage{input_tokens, input_tokens_details{image_tokens,text_tokens}, output_tokens, output_tokens_details?{image_tokens,text_tokens}, total_tokens}`, "for the GPT image models only"; also optional `svg_usage` (separate). Units tokens; for other image models usage is absent and per-image billing is not reported in-band (UNCONFIRMED). Streaming: `image_generation.partial_image` events (no usage) then `image_generation.completed` with `usage{input_tokens, input_tokens_details, output_tokens, total_tokens}` (no `output_tokens_details` listed there). Absent: stream interrupted before `completed`. The response has a `created` timestamp; model id placement UNCONFIRMED. Fixtures: `images.json`, `images.sse`, `images.interrupted.sse`.

## 8. Moderation

`POST /v1/moderations` (`REF/moderations/methods/create`). Response fields `id`, `model`, `results[]{flagged, categories, category_scores, category_applied_input_types}`. No usage object documented. Fixture `moderation.json` expects all null.

## 9. Batch API

Sources: `REF/batches/methods/create`, `GUIDE/batch`.

`POST /v1/batches` with `endpoint` one of `/v1/responses`, `/v1/chat/completions`, `/v1/embeddings`, `/v1/completions`, `/v1/moderations`, `/v1/images/generations`, `/v1/images/edits`, `/v1/videos`; `completion_window: "24h"`. Input lines: `{custom_id, method, url, body}`. Output file (`output_file_id`) lines: `{id, custom_id, response{status_code, request_id, body}, error}`; `body` is the endpoint's own response object, so usage is at `response.body.usage` (chat/embeddings) in the shape of that endpoint. The guide's abbreviated examples do not show a usage field, so this is derived: UNCONFIRMED by explicit example. Error file (`error_file_id`) lines have `response:null` and `error{code,message}` (e.g. `batch_expired`). Batch object `usage{input_tokens, input_tokens_details{cached_tokens}, output_tokens, output_tokens_details{reasoning_tokens}, total_tokens}`, "Only populated on batches created after September 7, 2025"; no cache-write counter at batch level. Batch statuses include `failed`, `in_progress`, `finalizing`, `completed`, `expired`, `cancelling`, `cancelled`; `request_counts{total, completed, failed}`. Output lines are not guaranteed in input order; key on `custom_id`. Do not add batch-object usage to the sum of output-file lines. Fixtures: `batch.output.jsonl`, `batch.errors.jsonl`, `batch.object.json`.

## 10. Rate limits

Sources: `GUIDE/rate-limits` (fetched via the 301 target `https://developers.openai.com/api/docs/guides/rate-limits`), `GUIDE/error-codes`.

Response headers (docs sample values in parentheses):

| Header | Meaning |
|---|---|
| `x-ratelimit-limit-requests` (60) | max requests before exhausted |
| `x-ratelimit-limit-tokens` (150000) | max tokens before exhausted |
| `x-ratelimit-remaining-requests` (59) | requests remaining |
| `x-ratelimit-remaining-tokens` (149984) | tokens remaining |
| `x-ratelimit-reset-requests` (`1s`) | time until the request limit resets (duration string such as `6m0s`, not an epoch) |
| `x-ratelimit-reset-tokens` (`6m0s`) | time until the token limit resets |
| `x-ratelimit-limit-project-tokens`, `x-ratelimit-remaining-project-tokens`, `x-ratelimit-reset-project-tokens` (60000 / 57000 / `3s`) | only when a project-scoped token limit applies |
| `Retry-After` (56) | minimum seconds to wait; may appear on 429 (temporary rate limit) and 503 (temporary overload); treat as a floor and add jitter; if missing/invalid use exponential backoff with jitter |

Header case is shown mixed (`Retry-After` capitalized in the table; HTTP header names are case-insensitive).
The guide says "Failed requests still count toward your per-minute limit". Which endpoints emit these headers (e.g. embeddings, audio, images, batch, SSE streams, WebSocket upgrade responses) is not stated: UNCONFIRMED. In streaming responses the headers are on the initial HTTP response only.

429 cases (error-codes guide): message "Rate limit reached for requests" (type/code not listed); "Slow down" with `error.type: "rate_limit_error"`, `error.code: "slow_down"`; and non-retryable quota cases with `error.code` `credit_balance_exhausted`, `organization_spend_limit_exceeded`, `project_spend_limit_exceeded`, `organization_usage_limit_exceeded`. `insufficient_quota` is mentioned as a possible `error.type` for billing errors but not tied to a row. `rate_limit_exceeded` is not mentioned in these pages (UNCONFIRMED; commonly seen elsewhere). The docs give no full JSON body; the surrounding envelope is the usual `{"error":{"message", "type", "param", "code"}}` (`message`, `type`, `param`, `code` are the fields referenced; the wrapper object name `error` is referenced as `error.code` etc.). Python SDK raises `RateLimitError`.

In-band rate-limit events:

- Realtime: `rate_limits.updated` `{event_id, type, rate_limits[]{name: "requests"|"tokens", limit, remaining, reset_seconds}}`, "Emitted at the beginning of a Response"; when a Response is created some tokens are reserved for output, so `remaining` reflects that reservation and is "adjusted accordingly once the Response is completed". Resets are numeric seconds here, unlike the HTTP duration strings.
- Responses WebSocket mode, Live, Chat/Responses SSE: no rate-limit event documented (only `error`). A 429 on a WebSocket upgrade is plain HTTP.

Fixture: the `rate_limits.updated` event is inside `realtime.ws.jsonl` (not a usage event).

## 11. UNCONFIRMED / open questions

1. Chat Completions: when `usage` is omitted on a non-streaming response; the fifth `finish_reason` value; whether chunks carry `service_tier`; billing/usage for interrupted streams.
2. Whether `cache_write_tokens` is populated for models that predate `prompt_cache_options`.
3. Responses: whether `response.incomplete`, `response.failed`, and cancelled responses ever carry non-null `usage` in real traffic (docs examples show `null`); `event:` line presence on the SSE wire (fixtures use it in `responses.sse` and omit it elsewhere to exercise both); usage on background responses; usage for `generate:false` warmups; parent object of `svg_usage`.
4. Realtime: usage on cancelled/failed/incomplete `response.done`; location of the model id per response; WebRTC data-channel name for Realtime (seen for Live); byte-level event equivalence across WebSocket/WebRTC/SIP; whether `rate_limits.updated` is also sent over WebRTC/SIP.
5. Realtime translation/transcription sessions: where duration is reported in-band (none seen).
6. Live API: endpoint and event names are in the reference and guides; behaviour on `connection_lost` (final seconds) is not documented.
7. Speech (TTS) SSE: event names and any usage payload; billing unit of speech and translations.
8. Images: usage for non-GPT image models; whether partial-image tokens are inside the final usage; model id location on image responses.
9. Batch: no explicit example of usage inside output-file lines; per-request 4xx representation (output line with non-200 `status_code` vs error file); batch-level usage cache-write counter absent.
10. Rate limits: which endpoints and transports emit `x-ratelimit-*`; full 429 JSON body example; whether `rate_limit_exceeded` is still used.
11. Doc pages are rendered for the current model generation; older SDK/proxy behaviour (e.g. pre-2025 Chat usage without `cache_write_tokens`) is not covered.

## Fixture notes (media, Live voice and batch operations)

- `transcription*.json/.sse` and `images*.json/.sse` are served by `openai.v1.audio-transcriptions` and `openai.v1.images-generations`. Duration usage is observed as `audio_input_milliseconds` (seconds rounded up), images as `generated_images`, detail splits as `input_*_tokens` / `output_*_tokens`.
- `live*.ws.jsonl` are served by the `openai.v1.live` relay (`/v1/live/sessions?model=`, the model pinned against `session.start`). `session.closed` `usage.seconds` = 45.8 is recorded once as 45800 ms.
- `batch.output.jsonl`, `batch.errors.jsonl` and `batch.object.json` are aggregated by the gateway's batch settlement. Paths (retrieved 2026-11-10): `POST /v1/batches` (https://developers.openai.com/api/reference/resources/batches/methods/create) and `GET /v1/files/{file_id}/content` (https://developers.openai.com/api/reference/resources/files/methods/content). Retrieve, cancel and list paths (`GET /v1/batches/{id}`, `POST /v1/batches/{id}/cancel`, `GET /v1/batches`) are not shown on the create page and are taken from the reference index.
- Speech: https://developers.openai.com/api/reference/resources/audio/subresources/speech/methods/create (retrieved 2026-11-10) documents `stream_format: "sse"` but shows no SSE event names or usage fields. Third-party sources describe `speech.audio.done` with usage; not used. Speech stays request-count with unknown usage.

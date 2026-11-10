# Model providers

LLMPlex can route native model requests to OpenAI, Anthropic, Azure OpenAI, Amazon
Bedrock, Gemini Developer API and OpenAI-compatible endpoints, in addition to the Google
Cloud surfaces it already served. A provider surface is a saved model surface with a
`provider` block. The block pins the provider's native operations, credential scheme and
stream-usage policy. Callers reach the provider through AIPlex's native route, or through a
client facade that accepts the provider's own SDK paths. Requests are checked against the
operation catalog, so an operation that is not in the catalog fails closed.

The authority on what is metered exactly, advisory, or not observed at all is the
generated [usage-format coverage matrix](reference/usage-formats/coverage.md). This page
lists the operations and their metering class. Where the two differ, the matrix wins.
Wire formats and usage fields are in the reference pages linked below. The surface model
is in [model surfaces](model-surfaces.md). The metering contracts are in
[model metering profiles](model-metering-profiles.md). Provider batch jobs are described in
[model batches](model-batches.md).

## Operations

Metering has two parts. **Quota metering** is either request count (one admitted exchange
per request) or a token profile (quota is charged on token exposure, with exact settlement
where the matrix says so). **Usage observation** is what the ledger records. The coverage
matrix states its level per row. Where this page does not repeat a level, it says so and
links to the matrix. A profile is assigned per operation in `pkg/modelapi/coverage.go`. The
quota binding chooses which admissible profile applies. An operation without a token
profile is request-count metered for quota.

### OpenAI and OpenAI-compatible

| Provider (`provider.id`) | Operation ID | Native method and path | Client protocol | Transport | Quota metering | Usage observation |
| --- | --- | --- | --- | --- | --- | --- |
| `openai` | `openai.v1.chat-completions` | `POST /v1/chat/completions` | `openai-chat` | SSE | Request count, `openai-text` token profile | exact |
| `openai` | `openai.v1.responses` | `POST /v1/responses` | `openai-responses` | HTTP. SSE when the body sets `stream: true`. | Request count, `openai-text` token profile | exact |
| `openai` | `openai.v1.responses-websocket` | `GET /v1/responses` (WebSocket) | `openai-responses` | WebSocket | Session: request count. Per response, `openai-text`, with `websocket.metering: "token"`. | exact |
| `openai` | `openai.v1.realtime` | `GET /v1/realtime?model=…` (WebSocket) | `openai-realtime` | WebSocket | Session: request count. Per response, `openai-realtime`, with `websocket.metering: "token"`. | exact |
| `openai` | `openai.v1.live` | `GET /v1/live/sessions?model=…` (WebSocket) | `openai-live` | WebSocket | Session: request count only | advisory |
| `openai` | `openai.v1.embeddings` | `POST /v1/embeddings` | `openai-embeddings` | HTTP | Request count, `embedding-input` token profile | exact |
| `openai` | `openai.v1.moderations` | `POST /v1/moderations` | `openai-moderations` | HTTP | Request count only | advisory (the response documents no usage) |
| `openai` | `openai.v1.responses-retrieve`, `openai.v1.responses-cancel` | `GET /v1/responses/{id}`, `POST /v1/responses/{id}/cancel` | `openai-responses` | HTTP | Request count only, owner only | advisory (never read for usage) |
| `openai` | `openai.v1.responses-delete` | `DELETE /v1/responses/{id}` | `openai-responses` | HTTP | Request count only, owner only | see matrix |
| `openai` | `openai.v1.audio-transcriptions` | `POST /v1/audio/transcriptions` (multipart; JSON or SSE) | `openai-audio` | HTTP | Request count, or `media-units` binding | exact (units and token splits) |
| `openai` | `openai.v1.audio-translations` | `POST /v1/audio/translations` (multipart) | `openai-audio` | HTTP | Request count, or `media-units` binding | see matrix |
| `openai` | `openai.v1.audio-speech` | `POST /v1/audio/speech` | `openai-audio` | HTTP (raw audio or SSE) | Request count only | no usage documented by the provider |
| `openai` | `openai.v1.images-generations` | `POST /v1/images/generations` | `openai-images` | HTTP (JSON or SSE) | Request count, or `media-units` binding | exact (generated images, token counters) |
| `openai` | `openai.v1.images-edits` | `POST /v1/images/edits` (multipart) | `openai-images` | HTTP | Request count, or `media-units` binding | see matrix |
| `openai` | `openai.v1.files-create` | `POST /v1/files` (`purpose: batch` only) | `openai-batch` | HTTP | Request count (asynchronous) | see matrix |
| `openai` | `openai.v1.files-content` | `GET /v1/files/{id}/content` (output or error file of a tracked batch) | `openai-batch` | HTTP | Request count (asynchronous) | exact (batch results, see [model batches](model-batches.md)) |
| `openai` | `openai.v1.batches-create` | `POST /v1/batches` | `openai-batch` | HTTP | Request count, or `batch-usage` binding | exact (batch object is a cross-check only) |
| `openai` | `openai.v1.batches-get`, `openai.v1.batches-cancel` | `GET /v1/batches/{id}`, `POST /v1/batches/{id}/cancel` | `openai-batch` | HTTP | Request count (asynchronous) | exact (batch object is a cross-check only) |
| `openai` | `openai.v1.batches-list` | `GET /v1/batches` | `openai-batch` | HTTP | Request count (asynchronous) | see matrix |
| `openai-compatible` | `compat.chat-completions` | `POST {base}/chat/completions`, `base` defaults to `/v1` | `openai-chat` | SSE | Request count. `openai-text` only with `provider.usage_semantics: "openai"`. | exact in the matrix. Token settlement needs `usage_semantics`. |

### Azure OpenAI

| Provider (`provider.id`) | Operation ID | Native method and path | Client protocol | Transport | Quota metering | Usage observation |
| --- | --- | --- | --- | --- | --- | --- |
| `azure-openai` | `azure.v1.chat-completions` | `POST /openai/v1/chat/completions` | `openai-chat` | SSE | Request count, `openai-text` token profile | exact |
| `azure-openai` | `azure.v1.responses` | `POST /openai/v1/responses` | `openai-responses` | HTTP. SSE when the body sets `stream: true`. | Request count, `openai-text` token profile | exact |
| `azure-openai` | `azure.deployments.chat-completions` | `POST /openai/deployments/{model}/chat/completions` | `openai-chat` | SSE | Request count, `openai-text` token profile | exact in the matrix. The strict profile fails closed when the response has no cached-input breakdown. |
| `azure-openai` | `azure.v1.embeddings` | `POST /openai/v1/embeddings` | `openai-embeddings` | HTTP | Request count, `embedding-input` token profile | see matrix |
| `azure-openai` | `azure.deployments.embeddings` | `POST /openai/deployments/{model}/embeddings` | `openai-embeddings` | HTTP | Request count, `embedding-input` token profile | see matrix |
| `azure-openai` | `azure.v1.responses-retrieve`, `azure.v1.responses-cancel`, `azure.v1.responses-delete` | `GET`, `POST …/cancel`, `DELETE` on `/openai/v1/responses/{id}` | `openai-responses` | HTTP | Request count only, owner only | see matrix |

Deployment-scoped Azure operations (`azure.deployments.*`) need a pinned
`provider.api_version`. The matrix has no Azure rows for audio, images, files or batches.
Those operations are not installed on Azure surfaces.

### Anthropic

| Provider (`provider.id`) | Operation ID | Native method and path | Client protocol | Transport | Quota metering | Usage observation |
| --- | --- | --- | --- | --- | --- | --- |
| `anthropic` | `anthropic.messages` | `POST /v1/messages` | `anthropic-messages` | SSE | Request count, `anthropic-text` token profile | exact |
| `anthropic` | `anthropic.messages-count-tokens` | `POST /v1/messages/count_tokens` | `anthropic-messages` | HTTP | Request count only | advisory (an estimate; the response is never read for usage) |
| `anthropic` | `anthropic.message-batches-create` | `POST /v1/messages/batches` | `anthropic-message-batches` | HTTP | Request count, or `batch-usage` binding | exact, from results |
| `anthropic` | `anthropic.message-batches-get`, `anthropic.message-batches-cancel`, `anthropic.message-batches-list` | `GET /v1/messages/batches/{id}`, `POST …/{id}/cancel`, `GET /v1/messages/batches` | `anthropic-message-batches` | HTTP | Request count (asynchronous) | not a usage source. Usage comes from results only. |
| `anthropic` | `anthropic.message-batches-results` | `GET /v1/messages/batches/{id}/results` | `anthropic-message-batches` | HTTP (JSONL) | Request count (asynchronous). Settles the batch under `batch-usage`. | exact, from results |

### Bedrock

Bedrock rows appear under the `anthropic` provider in the coverage matrix.

| Provider (`provider.id`) | Operation ID | Native method and path | Client protocol | Transport | Quota metering | Usage observation |
| --- | --- | --- | --- | --- | --- | --- |
| `bedrock` | `bedrock.converse` | `POST /model/{model}/converse` | `bedrock-converse` | HTTP | Request count, or `bedrock-converse-text` token profile | exact in the matrix |
| `bedrock` | `bedrock.converse-stream` | `POST /model/{model}/converse-stream` | `bedrock-converse` | AWS event stream | Request count, or `bedrock-converse-text` token profile | exact in the matrix |
| `bedrock` | `bedrock.invoke` | `POST /model/{model}/invoke` | `bedrock-invoke` | HTTP | Request count, or `anthropic-text` token profile (Messages-shaped bodies only) | exact for Messages-shaped bodies. Other InvokeModel bodies are not observed. |
| `bedrock` | `bedrock.invoke-stream` | `POST /model/{model}/invoke-with-response-stream` | `bedrock-invoke` | AWS event stream | Request count, or `anthropic-text` token profile (Messages-shaped chunks only) | exact for Messages-shaped chunks. Other chunks are not observed. |

The matrix holds the level for each Bedrock row. The `anthropic-text` profile is admitted
for Bedrock InvokeModel only when the body is Messages-shaped.

### Gemini Developer API

Provider `gemini-developer` serves the Google GenAI wire (`google-genai`) on
`generativelanguage.googleapis.com`. The matrix has no `gemini-developer` rows, so usage
levels for these operations are not stated here. The
[coverage matrix](reference/usage-formats/coverage.md) lists the Vertex (`google`) rows.

| Operation IDs | Native method and path | Transport | Quota metering |
| --- | --- | --- | --- |
| `gemini.models.generateContent`, `gemini.models.streamGenerateContent` | `POST /v1beta/models/{model}:generateContent`, `…:streamGenerateContent` (SSE with `?alt=sse`) | HTTP, SSE | Request count, `gemini-text` token profile |
| `gemini.interactions.create` | `POST /v1beta/interactions` (SSE when the body sets `stream: true`) | HTTP, SSE | Request count, `gemini-interaction-text` token profile |
| `gemini.models.embedContent`, `gemini.models.batchEmbedContents` | `POST /v1beta/models/{model}:embedContent`, `…:batchEmbedContents` | HTTP | Request count, `embedding-input` token profile |
| `gemini.models.countTokens` | `POST /v1beta/models/{model}:countTokens` | HTTP | Request count only |
| `gemini.cachedContents.create`, `.list`, `.get`, `.update`, `.delete` | `POST`, `GET`, `GET`, `PATCH`, `DELETE` on `/v1beta/cachedContents[/{id}]` | HTTP | Request count (asynchronous). Cache size is not read for usage. |
| `gemini.interactions.get`, `.cancel`, `.delete` | `GET`, `POST …/{id}/cancel`, `DELETE` on `/v1beta/interactions/{id}` | HTTP | Request count (asynchronous) |
| `gemini.models.batchGenerateContent`, `gemini.batches.get`, `gemini.batches.cancel` | `POST /v1beta/models/{model}:batchGenerateContent`, `GET /v1beta/batches/{id}`, `POST /v1beta/batches/{id}:cancel` | HTTP | Request count, or `batch-usage` binding (create). Get and cancel are asynchronous. |
| `gemini.models.predictLongRunning`, `gemini.operations.get` | `POST /v1beta/models/{model}:predictLongRunning`, `GET /v1beta/models/{model}/operations/{id}` | HTTP | Request count (asynchronous). The done result reports no usage or duration. |

Usage observation for Gemini Developer operations is not stated in the matrix. Generation
and interactions responses that carry usage are read by the observers. Cached contents,
their list, and the operations that return no usage are never read for usage. The batch
rules are in [model batches](model-batches.md).

### Google Cloud surfaces

Google Cloud surfaces (no `provider` block) use the same two-part model:

| Operation ID | Native method and path | Streaming transport | Quota metering | Usage observation |
| --- | --- | --- | --- | --- |
| `models.generateContent` | `POST …:generateContent` | HTTP | Request count, `gemini-text` token profile | exact (Vertex) |
| `models.streamGenerateContent` | `POST …:streamGenerateContent` | SSE | Request count, `gemini-text` token profile | exact with `alt=sse` (Vertex) |
| `interaction.create` | `POST …/interactions` | HTTP (catalog default) | Request count, `gemini-interaction-text` token profile | exact (Vertex) |
| `models.rawPredict` | `POST …:rawPredict` (Vertex Anthropic) | HTTP | Request count, `anthropic-text` token profile | exact (Vertex) |
| `models.streamRawPredict` | `POST …:streamRawPredict` (Vertex Anthropic) | SSE | Request count, `anthropic-text` token profile | exact (Vertex) |
| `live.connect` | `GET /ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent` (WebSocket) | WebSocket | Session: request count. Allowance windows with `websocket.metering: "token"`. | exact in the matrix (`vertex-live`) |

Operations that the catalog lists without a token profile, such as `endpoints.rawPredict`,
`embedContent` and `countTokens`, are request-count metered only. Asynchronous operations
(batch, tuning, cache creation and long-running prediction) are request-count only, and
their later work is not metered. The full list is in `pkg/modelapi/coverage.go`. The
matrix lists each row's status and the reason for any gap.

### Metering notes

Token profiles accept only complete, supported text usage. Two rules apply to every
output-inclusive token profile:

- **Server-tool quantities fail closed** for token metering, per counter. This covers
  Anthropic `server_tool_use` counters, OpenAI built-in tool calls in Responses output, Gemini
  billed tool calls and Gemini tool-use prompt tokens.
- **Cache writes** are a mapped meter. A binding with no cache-write meter refuses an
  observation that reports positive cache writes, and the exposure is retained.

For the OpenAI and Anthropic token profiles, reasoning tokens are diagnostic and already
included in `output_tokens`, so they are not added again. The one-hour cache-write part and the
server-tool request counts are in the usage ledger record (`cache_write_1h_tokens`,
`server_tools`); the other per-TTL counters are not. See [usage ledger](usage-ledger.md#limits).

## WebSocket sessions

Four session operations are implemented as WebSocket relays:

| Operation ID | Native path | Provider | Client protocol | Default metering (`session`) | Token metering (`token`) |
| --- | --- | --- | --- | --- | --- |
| `openai.v1.realtime` | `GET /v1/realtime?model=…` | `openai` | `openai-realtime` | One request-count admission per session | One `openai-realtime` admission per `response.create` |
| `openai.v1.responses-websocket` | `GET /v1/responses` | `openai` | `openai-responses` | One request-count admission per session | One `openai-text` admission per `response.create` |
| `openai.v1.live` | `GET /v1/live/sessions?model=…` | `openai` | `openai-live` | One request-count admission per session | Not available. Refused at save time. |
| `live.connect` | `GET /ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent` | Google Cloud (Vertex Live) | `google-genai` | One request-count admission per session | `gemini-live-allowance` windows |

The model is chosen at connect time by the `model` query parameter for Realtime, Live
voice and Live. For Responses WebSocket mode, it is chosen per `response.create` event, and
the connect request must not carry `model`.

### The `websocket` block

The block is part of the `surface` object of the model-surface spec.

| Field | Values | Default | Meaning |
| --- | --- | --- | --- |
| `surface.websocket.metering` | `session`, `token` | `session` | How sessions are admitted. See below. |
| `surface.websocket.max_session_seconds` | 60 to 86400 | the gateway's `MaxStreamDuration`, 30 minutes unless configured | Duration bound. Both sockets close when it elapses. Applies to every WebSocket session on the surface. |
| `surface.websocket.token_allowance` | 1024 to 4194304, with `token` only | 32768 | Gemini Live allowance window, held per mapped meter. |

`metering: "token"` is refused at save time for a surface that includes `openai.v1.live`.
Live voice is billed by session seconds, which no token path observes, so it keeps session
metering.

### Session metering (default)

- **Admission.** One lease per session, prepared once the model is known (Live: after the
  bounded `setup` message; Realtime: the `?model=` parameter; Responses: at the upgrade,
  with each `response.create` re-authorized). It is prepared before the upstream dial.
  Only the request-count profile is accepted. A token-profile lease is refused with
  `UNSUPPORTED_EXECUTION_TRANSPORT`.
- **Permit.** One concurrency permit for the session's lifetime.
- **Quota.** The session counts as one request against request budgets and rates. Tokens
  are not bounded by quota on this path.
- **Usage.** Server usage events are forwarded to the ledger as advisory observations while
  the session runs. Live reports cumulative snapshots. Realtime and Responses report
  per-response usage, which the adapter sums over distinct response IDs. Input-transcription
  usage is a separate `transcription_*` meter and is not merged into the session's tokens.
  Live voice seconds are observed as `voice_session_milliseconds`.
- **Duration.** The session is bounded by `max_session_seconds`, or by the gateway default.
  Reaching the bound closes both sockets and labels the usage as a lower bound. The bound
  is applied as a context deadline to every gateway request, not only to sessions.
- **Settlement.** Exactly one settlement at session end. The request is settled and the
  permit is released. A closed socket never retains exposure.

How an interrupted or uncertain session is recorded:

- With final evidence (a clean close, every started response finished with usage, and no
  activity after the last snapshot), the session records final usage.
- Without final evidence, the usage is labelled by `usage_bound` in the ledger:
  `lower_bound` for a session that ended without it, `ambiguous` for a resumed Live
  session, and `partial` when non-token units (for example transcription seconds) were
  reported. The record's status is `unknown`, and its `error_class` is
  `session_usage_<bound>`. See [usage ledger](usage-ledger.md#bounded-usage).
- The lease finishes with an error, and its one request is still counted.

### Token metering

Token metering is set per surface with `websocket.metering: "token"`. Each admitted exchange
is then metered on its own. The admission bound is per response or per allowance window,
not per session.

- **OpenAI Realtime and Responses WebSocket mode.** Each client `response.create` is
  admitted before it is relayed. One response may be in flight at a time. A second
  `response.create`, a quota denial, or an unbounded request is answered with the protocol's
  own error event, and the session stays open. The terminal event (`response.done` with
  status `completed` for Realtime, `response.completed` for Responses) settles the response on
  its exact usage. Realtime settles on disjoint modality meters (`openai-realtime`). Its
  input and output breakdowns must add up to the reported totals. Cancelled, failed or
  incomplete responses, a response without usage, and a disconnect retain the hold as
  unknown.
- **Realtime turn detection.** A token-metered Realtime session must first send a
  `session.update` that disables automatic responses (`turn_detection` null, or
  `create_response: false`). Any other event before that is refused. A response the gateway
  did not admit closes the session with a policy-violation close.
- **Gemini Live (renewable allowance).** Live `usageMetadata` messages are
  session-cumulative snapshots whose composition Google does not fully document. The first
  window lease holds `token_allowance` per mapped meter at setup, before the dial. When any
  mapped counter has consumed half of the window, the window settles exactly on the snapshot
  delta, and the next window is admitted. Windows never overlap. A denied renewal sends
  `{"goAway":{"timeLeft":"0s"}}` and closes both sockets with a policy-violation close. A
  clean close whose last message was a snapshot settles the open window. An interruption, or
  model output or input after the last snapshot, retains the open window as unknown. Resumed
  sessions are refused in token mode.
- **Gemini Live overrun bound.** Worst-case exposure beyond admitted holds: one allowance per
  mapped meter — a snapshot jump larger than half a window settles as a recorded overrun
  (debt that blocks further admissions, never dropped), and after a denied renewal the usage
  generated between that snapshot and the close has no hold and reaches the ledger only as
  `lower_bound` telemetry. The `gemini-live-allowance` profile uses the surface allowance as
  its exposure bound.

### Live voice

Live voice (`openai.v1.live`) is session-only. It has one request-count admission and one
settlement per session, as in the default session metering above. Its primary meter is
voice seconds, observed as `voice_session_milliseconds`. Nested Responses and decision token
usage is advisory and counted once per response. Token metering is refused at save time.
The matrix lists this row as advisory.

Responses WebSocket mode is not routed through access points. See
[access points](access-points.md#websocket-sessions).

## Reference pages

Wire-format and usage-field details are in these pages. They are not repeated here.

- [Usage-format coverage matrix](reference/usage-formats/coverage.md), the authority on
  exact, advisory, unsupported and not-listed observation
- [OpenAI](reference/usage-formats/openai.md)
- [Anthropic](reference/usage-formats/anthropic.md)
- [Google](reference/usage-formats/google.md)

There is no separate reference page for Bedrock. Its formats are described in the matrix
rows.

## Create a surface with the CLI

`aiplex llm surfaces create <id>` (alias `add`) writes a model surface from flags. `--provider` fills in the
endpoint, protocol, credential header and default operations from the provider catalog: `openai`, `anthropic`,
`gemini` or `openai-compatible` (for servers such as Ollama or vLLM, which also need `--origin`). `--models` lists the
model names callers may use, `--secret-ref` names the secret that holds the provider key (never the key itself), and
`--operations` overrides the defaults. `aiplex llm providers <provider>` lists the operation IDs.

```sh
aiplex llm surfaces create openai --provider openai --models gpt-4o-mini,gpt-4o --secret-ref openai-key
aiplex llm surfaces create claude --provider anthropic --models claude-sonnet-4-5 --secret-ref anthropic-key
aiplex llm surfaces create local-ollama --provider openai-compatible --origin http://127.0.0.1:11434 --models llama3.2
```

Then expose the models on an access point: `aiplex access-points create team --model gpt-4o-mini=openai --allow-groups eng`.

On `aiplex dev`, a surface's origin must be one the local API may call. The seeded `local-models` surface uses the
mock model's loopback address. A surface for a public provider needs its origin given to `aiplex dev --allow-origin`
when the local API starts. With `aiplex dev`, a secret reference name resolves from the environment variable
`AIPLEX_TOOL_SECRET_<NAME>` (upper case, `-` as `_`) of the shell that started `aiplex dev`, so the key is never
passed on the command line. Creating a surface for an origin that is not approved fails:

```text
Error: apply surface: origin not approved
  The API only calls upstream origins its network policy approves.
  With aiplex dev, restart it with: aiplex dev --allow-origin <scheme://host[:port]>
```

Creating a surface on the mock model's loopback origin (the address of the seeded `local-models` surface, which
`aiplex llm surfaces get local-models` shows) succeeds:

```text
$ aiplex llm surfaces create mock-b --provider openai-compatible --origin http://127.0.0.1:9000 --models mock-gpt
Created model surface mock-b (revision 1): openai-compatible at http://127.0.0.1:9000, models mock-gpt, 1 operations.

Next: expose a model on an access point:
  aiplex access-points create <id> --model mock-gpt=mock-b --allow-users <email>

$ aiplex llm surfaces list
ID            PROVIDER           PROTOCOL     OPERATIONS  MODELS  AUTH (reference)  CAPACITY POLICY  REVISION
local-models  openai-compatible  openai-chat  1           1       auth:none         off              1
mock-b        openai-compatible  openai-chat  1           1       auth:none         off              1
```

The port in that output is the one the mock upstream was given when `aiplex dev` started, and it changes each run.
`--update` replaces a surface; the CLI reads its revision for you. `aiplex llm surfaces delete <id>` asks first on a terminal.

## Surface configuration

A provider surface is a `model-surface` resource with a `provider` block. Every field
is operator configuration. No caller value overrides it.

| Field | Used by | Meaning |
| --- | --- | --- |
| `id` | All | Provider ID: `openai`, `anthropic`, `azure-openai`, `bedrock`, `openai-compatible` or `gemini-developer`. |
| `base_path` | `openai-compatible`, `anthropic` | `openai-compatible`: prefix for operation paths, default `/v1`. `anthropic`: a path prefix before every upstream path, for an Anthropic-compatible endpoint served under one (z.ai serves it under `/api/anthropic`, so `/v1/messages` is sent to `<origin>/api/anthropic/v1/messages`); clients still call `/v1/messages`. An absolute path of 1 to 4 plain segments (`[A-Za-z0-9._-]`, no query, fragment, dot segment or trailing slash) is required. The origin rules below are unchanged: the prefix is not part of the origin, so the same approved origin covers it. |
| `api_version` | `azure-openai` | Pinned `api-version` query value for deployment-scoped operations. |
| `region` | `bedrock` | Signing region. It must match the origin. |
| `organization`, `project` | `openai` | Pinned `OpenAI-Organization` and `OpenAI-Project` headers. |
| `anthropic_version` | `anthropic` | Pinned `anthropic-version` header. |
| `anthropic_betas` | `anthropic` | Allowlist of caller `anthropic-beta` values. |
| `stream_usage` | `openai`, `azure-openai`, `openai-compatible` | Stream-usage policy for Chat Completions. See below. |
| `usage_semantics` | `openai-compatible` | Declares the usage contract of a generic server. See below. |
| `limit_header_passthrough` | All | Forwards the provider's rate-limit headers to the caller. Off by default. |

The `websocket` block is on the `surface` object. It is described under
[WebSocket sessions](#the-websocket-block). The `capacity_policy` block is at the top level
of the model-surface spec, beside `surface`. It is described under
[capacity policy](#capacity-policy).

Credentials are either an `auth` block that references a tenant secret, or an
`identity_ref` to an operator identity profile. Google Cloud surfaces accept `identity_ref`
only.

| Provider | Accepted credential |
| --- | --- |
| `openai` | `auth.type: bearer`, or identity type `bearer-env` |
| `anthropic` | `auth.type: api_key` in header `x-api-key`, with no prefix |
| `azure-openai` | `auth.type: api_key` in header `api-key`, with no prefix, or identity type `azure-default` (Entra) |
| `bedrock` | `auth.type: bearer` (Bedrock API key), or identity type `aws-default` (SigV4) |
| `openai-compatible` | `auth.type: bearer` or `none`, or identity type `bearer-env` |
| `gemini-developer` | `auth.type: api_key` in header `x-goog-api-key`, with no prefix |

Origins are checked at save time, for `https` origins. An `https` `gemini-developer` origin
must be `generativelanguage.googleapis.com`. An `https` `bedrock` origin must be the Bedrock
Runtime endpoint of its signing region. An `https` `azure-openai` origin must be an Azure
OpenAI or Foundry resource endpoint.

### Stream usage

Chat Completions streams report usage only when the request asks for it. The
`stream_usage` policy controls that:

| Value | Effect |
| --- | --- |
| `require` | A streaming request without `stream_options.include_usage: true` is rejected. Default for `openai` and `azure-openai`. |
| `inject` | AIPlex sets `stream_options.include_usage` to `true`. This is the only body change AIPlex makes to a request. |
| `off` | The request is forwarded unchanged. Streaming usage may stay unknown. Default for `openai-compatible`. |

### Usage semantics for OpenAI-compatible servers

A generic compatible server has no known usage contract. `usage_semantics: "openai"` is
the operator's declaration that the server follows OpenAI Chat Completions semantics:
`prompt_tokens` includes cached tokens and cache writes, `completion_tokens` includes
reasoning, and `total_tokens` equals prompt plus completion. It is accepted only on
`openai-compatible` surfaces, and no other value is accepted.

- With the declaration, `compat.chat-completions` can carry an `openai-text` token profile.
- Without it, a token-profile binding on `compat.chat-completions` is refused with
  `QUOTA_PROFILE_UNSUPPORTED`. The operation stays request-count metered.
- Usage that the server reports is not gated by this field. The matrix's compatible rows
  describe observation, and token settlement depends on the declaration.

## Caller authentication

A caller presents an AIPlex token. On provider-native model routes, an SDK can send the
token in its own vendor header instead of `Authorization`. The adapter moves that value into
`Authorization: Bearer` before identity checks, and then verifies it like any bearer token.
No other credential type is accepted.

| Header | Accepted on | Wire |
| --- | --- | --- |
| `Authorization: Bearer <token>` | All model routes, including the access point and facade routes below | Any |
| `x-api-key` | `/ap/{id}/v1/messages`, `/ap/{id}/v1/messages/count_tokens`, `/ap/{id}/v1/models` (only when the request carries `anthropic-version`), `/api/v1/llm/gateway/{id}/v1/messages`, `/api/v1/llm/gateway/{id}/v1/messages/count_tokens` | Anthropic Messages |
| `x-goog-api-key` | `/ap/{id}/v1beta/…`, `/ap/{id}/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent`, `/api/v1/llm/gateway/{id}/v1beta/…`, the same Live path on the facade, and facade paths ending in `:generateContent`, `:streamGenerateContent`, `:countTokens`, `:embedContent` or `:batchEmbedContents` | Google GenAI |
| `api-key` | `/ap/{id}/v1/…` (other than the Anthropic paths above, so this includes `/v1/models` without `anthropic-version`), `/ap/{id}/openai/…` (Azure OpenAI shapes), `/api/v1/llm/gateway/{id}/openai/…` | OpenAI and Azure OpenAI |

Rules:

- The header applies only to the routes in its row. For example, `x-goog-api-key` on an
  OpenAI path is ignored, and the request has no credential.
- `api-key` is not read on `/api/v1/llm/gateway/{id}/v1/…` (the OpenAI paths of the facade). Those
  requests need `Authorization: Bearer`.
- Bedrock paths (`/ap/{id}/model/…` and `/api/v1/llm/gateway/{id}/model/…`) read no provider header.
  Send `Authorization: Bearer`.
- `/api/v1/llm/native/{id}/*` is not covered by this adapter. Send `Authorization: Bearer`.
- A token in a URL query parameter is refused with `401` on these model routes. The refused
  parameter names are `key`, `api_key`, `api-key`, `apikey` and `access_token`.
- If `Authorization` and a provider header are both present, they must carry the same token.
  Conflicting values are refused with `401`. So are repeated provider header values that differ,
  and a provider key sent together with an `X-Goog-Iap-Jwt-Assertion` header.
- **A real provider key beside the AIPlex token** is refused the same way. Claude Code does this when
  its settings already hold `ANTHROPIC_API_KEY`: it sends that key as `x-api-key` beside the AIPlex
  token. Nothing is forwarded or used. The error says which header to remove (`remove the X-Api-Key
  header`) and that the value reached the gateway in this request, is discarded, and is never logged,
  stored or forwarded, so it should be treated as seen by the gateway operator in transit only. The key
  value is dropped before identity extraction: it appears in no log, response, ledger record, audit
  event or span (tested with a planted marker). If you do not run or trust the gateway, rotate the key.
  Sending the AIPlex token alone in `x-api-key` keeps working.

## Routes

| Route | Purpose |
| --- | --- |
| `GET /api/v1/llm/operations` | The native operation catalog. With `?provider={id}`, that provider's descriptor: protocols, credential schemes and operations. Without it, the Google and Anthropic native operations. |
| `GET /api/v1/llm/providers` | The legacy provider route configuration. `aiplex llm providers --configured` reads it. |
| `GET /api/v1/llm/surfaces/{id}` | One surface, including its `provider` block. Surface writes use the shared resource routes. |
| `GET /api/v1/llm/surfaces/{id}/capacity` | The last provider limit observation for this surface on this replica, the surface's `capacity_policy` with defaults applied, and its cooldown state. Admin only. |
| `GET /api/v1/llm/batches` | Provider batch records, for admins. See [model batches](model-batches.md#listing-batches). |
| `/api/v1/llm/native/{id}/*` | Native provider requests through the surface. The path after `{id}` is the provider's own path. |
| `/api/v1/llm/gateway/{id}/*` | Client facade for unmodified provider SDKs. See below. |

Client facade paths, relative to `/api/v1/llm/gateway/{id}`. The facade rewrites only the
path. The body is not changed except as described under stream usage. Each path maps to the
native operation of the same surface. Operations that are not in the native allowlist fail
closed.

| Provider | SDK base setting | Paths the facade maps |
| --- | --- | --- |
| `openai` | `base_url` `…/gateway/{id}/v1` | `/v1/chat/completions`, `/v1/responses` (POST); `/v1/embeddings`, `/v1/moderations`; `/v1/responses/{id}` (GET, DELETE), `/v1/responses/{id}/cancel`; `/v1/audio/transcriptions`, `/v1/audio/translations`, `/v1/audio/speech`; `/v1/images/generations`, `/v1/images/edits`; `/v1/files`, `/v1/files/{id}/content`; `/v1/batches`, `/v1/batches/{id}`, `/v1/batches/{id}/cancel`; `/v1/realtime`, `/v1/responses` and `/v1/live/sessions` (GET upgrades for the WebSocket operations) |
| `anthropic` | `base_url` `…/gateway/{id}` | `/v1/messages`, `/v1/messages/count_tokens`; `/v1/messages/batches`, `/v1/messages/batches/{id}`, `…/{id}/cancel`, `…/{id}/results` |
| `azure-openai` (v1) | `base_url` `…/gateway/{id}/openai/v1` | `/openai/v1/chat/completions`, `/openai/v1/responses`, `/openai/v1/embeddings`, `/openai/v1/responses/{id}` (GET, DELETE) and `…/{id}/cancel` |
| `azure-openai` (deployments) | `azure_endpoint` `…/gateway/{id}` | `/openai/deployments/{deployment}/chat/completions`, `/openai/deployments/{deployment}/embeddings` |
| `bedrock` | `endpoint_url` `…/gateway/{id}` | `/model/{model}/converse`, `converse-stream`, `invoke`, `invoke-with-response-stream` |
| `openai-compatible` | `base_url` `…/gateway/{id}{base}` | `{base}/chat/completions` |
| `gemini-developer` | Google GenAI paths under `…/gateway/{id}` | `/v1beta/models/{model}:{generateContent, streamGenerateContent, countTokens, embedContent, batchEmbedContents, batchGenerateContent, predictLongRunning}`; `/v1beta/cachedContents[/{id}]`; `/v1beta/interactions[/{id}[/cancel]]`; `/v1beta/batches/{id}[:cancel]`; `/v1beta/models/{model}/operations/{id}` |

## Provider rejections

A provider response that the provider documents as refused before generation releases the
quota hold. The exchange then settles as one request with zero consumption. The provider's
status, body and `Retry-After` reach the caller unchanged. Other failures keep the exposure
as unknown.

The code (`pkg/modelapi/rejections.go`) decides on the HTTP status line.

| Provider | Released (not processed) | Retained (ambiguous) |
| --- | --- | --- |
| `openai`, `azure-openai`, `openai-compatible` | 400, 401, 403, 404, 413, 422, 429 | 5xx including 503, 408, cut connections, errors after a 2xx |
| `anthropic` | 400, 401, 402, 403, 404, 413, 429, 529 | 500, 504, mid-stream `error` events |
| `gemini-developer`, Google Cloud (Vertex, rawPredict, Gemini) | 400, 401, 403, 404, 429 | 500, 503, 504, cut connections |
| `bedrock` | 400, 401, 403, 404, 429 | 408, 424, 500, 503, cut connections |

The status set applies to the provider's rejection of any operation, including batch
create. [Model metering profiles](model-metering-profiles.md#provider-rejections) gives the
error names for each row. The error names there are descriptive. The code decides on the
status.

## Capacity policy

A provider surface can act on the limit signals it observes. The `capacity_policy` block on
the model-surface spec is off by default. A surface without it, or with an empty block, behaves
exactly as before: every request is forwarded and the provider's own 429 reaches the caller.

```json
"capacity_policy": {
  "cooldown_enabled": true,
  "max_cooldown_seconds": 60,
  "headroom_fraction": 0.1,
  "min_weight": 10,
  "default_weight": 1,
  "signal_max_age_seconds": 60
}
```

| Field | Meaning |
| --- | --- |
| `cooldown_enabled` | Back off while the provider is throttling. Default `false`. |
| `max_cooldown_seconds` | Upper bound on any cooldown, 1 to 3600. Default 60. Requires `cooldown_enabled`. |
| `headroom_fraction` | Between 0 and 1. When remaining requests or tokens fall below this fraction of the limit, only callers with `min_weight` or more are admitted. `0` or absent disables it. |
| `min_weight` | The lowest access-point QoS `weight` still admitted under low headroom, 1 to 1000. Required with `headroom_fraction`. |
| `default_weight` | The weight of a request that has no access-point QoS, or a QoS block without a weight. Default 1. Requires `headroom_fraction`. |
| `signal_max_age_seconds` | How old an observation may be and still be used, 1 to 3600. Default 60. |

The block is validated strictly when the surface is saved. Unknown fields, out-of-range
values, and settings that would have no effect (for example `min_weight` without
`headroom_fraction`) are rejected. Only provider surfaces accept it. It is part of the
surface configuration that the quota host pins, so changing it changes that configuration.

**Observation.** `GET /api/v1/llm/surfaces/{id}/capacity` returns the last normalized limit
observation for a surface, when one exists, in this shape:

```json
{"surface_id": "…", "observed": true, "scope": "replica-local last observation; advisory", "limits": {…}, "capacity_policy": {…}}
```

The observation records the classification (`rate_limited`, `overloaded` or
`quota_exceeded`), retry-after, and any requests, tokens, input-tokens or output-tokens
windows the provider reported. The store is in memory, bounded, and kept per process.
`aiplex llm capacity <surface>` prints the same data.

**Cooldown.** When the last response for the tenant's surface was a 429 or an overloaded
response, new requests are rejected until the wait ends. The wait is the provider's
`Retry-After` or `retry-after-ms`, else the reset of an exhausted requests or tokens window
(the later one when several are exhausted), else the earliest window reset on a rate-limit
response, else a default of 2 to 3 seconds with a stable jitter. It is capped by
`max_cooldown_seconds`, so a bad header cannot hold a surface closed. A `Retry-After` of `0`
means retry now and starts no cooldown. `quota_exceeded` responses start no cooldown because
waiting does not change the outcome. When the wait ends, one request is let through as a
probe; the others are rejected with a one-second hint until the probe answers. A probe whose
response is not itself limiting ends the cooldown. A limiting response starts a new one. A
probe that never reaches the provider releases its slot, and a probe that never answers is
replaced after 30 seconds.

**Headroom.** When any reported requests or token window has `remaining` below
`headroom_fraction` of its `limit`, callers whose weight is below `min_weight` are rejected.
The retry hint is the time to the window's reset, capped by `max_cooldown_seconds`, or the
default backoff when the provider reports no reset. A window whose reset time has passed is
ignored. Windows missing a limit or a remaining value are ignored; an unknown value is never
treated as zero.

**Rejections.** A rejected request is answered in the calling protocol's native rate-limit
error with a `Retry-After` header. It makes no upstream call, acquires no credential, and
charges no quota. WebSocket sessions are rejected at admission before the upgrade. The
usage ledger records it as a denied exchange with `error_class` `capacity_cooldown` or
`capacity_shed`, which are not provider errors.

The gate scope is the tenant and the surface. Providers that scope limits per model or per
deployment (OpenAI, Anthropic, Azure OpenAI) do not tell AIPlex which model a stored
observation belongs to, so a throttled model puts the whole surface in cooldown. Put models
that should not throttle each other on separate surfaces.

Limits of this gate:

- Observations are per replica, in memory, and are not shared. Each replica learns about
  throttling only from its own responses, and a restart forgets it. With several replicas
  the provider can still throttle traffic that arrives at a replica that has not seen it.
- Signals are advisory and stale by nature. They are used only while younger than
  `signal_max_age_seconds`, and an absent signal admits the request.
- The last observation wins. A later successful response with rate-limit headers replaces a
  throttling observation, and a response without headers leaves it in place.
- It is an extra gate before upstream dispatch. It never admits what QuotaPlex denies and
  does not replace QuotaPlex limits, budgets or weighted scheduling.
- It acts at request and session admission. It does not interrupt a stream or WebSocket
  session in progress.
- It applies to provider surfaces only, and only to the operations that go through the
  admission hook.

## Limits

- Provider limit signals are enforced only when a surface opts in with a
  `capacity_policy` (see [capacity policy](#capacity-policy)), and then only as an
  additional, advisory gate. QuotaPlex remains the only accounting and quota authority.
- Limit observations describe the shared upstream credential at one instant. Restarts and
  other replicas do not share them.
- `limit_header_passthrough` forwards the upstream credential's limit headers to callers,
  which reveals shared-account capacity. It is off by default.
- Only the operations in the tables above are installed. Other provider operations fail
  closed.
- Request-count metering counts managed exchanges. It does not measure asynchronous job
  work, cached-content retention or internal tool or agent work.
- Token quota applies only to operations with a token profile, and only when the quota
  binding selects it. Advisory usage is recorded but does not bound or settle quota.
- Bedrock InvokeModel usage is observed only when the body has the Messages shape. Other
  InvokeModel bodies are model-specific and are not observed.
- Anthropic token metering fails closed for server-tool calls, and for cache writes when the
  binding maps no cache-write meter.
- A provider's `401` or `403` is the **gateway's** credential being refused, since a caller never
  supplies the credential a provider sees. It reaches the caller as a `502` in the provider's own
  error shape with the code `upstream_credential_rejected` (the provider's body, which could name a key
  prefix, is not relayed), and the ledger records `error_class` `upstream_auth`. Operators see the
  surface id in the error details; other callers do not. This also covers the Gemini Developer API's
  `400` with reason `API_KEY_INVALID`. The quota hold is released as for any rejection before processing.
- WebSocket sessions on the default `session` metering are bounded by time, not by tokens.
  A session can run until `max_session_seconds` even when quota has no token headroom left.
  Token metering bounds each admitted response or allowance window instead.
- Provider credentials are references. Values are resolved at dispatch and are not
  returned by any route.

## Exercised against live providers

Small requests with the smallest models were sent through `aiplex dev` to the providers below. This is not a performance or reliability claim.

| Provider and surface | Exercised | Not exercised |
| --- | --- | --- |
| Gemini Developer API (`gemini-developer`) | `generateContent`, `streamGenerateContent?alt=sse`, `countTokens`, `embedContent`, `batchEmbedContents`, function calling, thinking, implicit caching, text, image and audio inputs, and the `google_search` tool. Ledger input, output, thinking and cached counts equalled the response's `usageMetadata`. Estimated costs from the imported catalogue price matched a hand calculation (rounded up to the price scale). | Live (WebSocket) sessions, `flex` and `priority` service tiers, requests over a long-context threshold, one-hour cache writes. |
| z.ai as `openai-compatible` (`base_path: /api/coding/paas/v4`) | Chat Completions JSON and SSE. Usage captured, including `reasoning_tokens` and `cached_tokens`. | |
| z.ai's Anthropic-compatible API through the `anthropic` provider | Messages JSON and SSE, `count_tokens`, a Claude Code session. Usage captured (input arrives on `message_delta`, `cache_read_input_tokens` present), through a local path-rewriting proxy. | `base_path` for `anthropic` is tested against a local upstream only. |
| OpenAI (`openai`) | `moderations`. | Chat Completions, Responses and embeddings (token capture with and without `include_usage`, cached and reasoning tokens), and the OpenAI built-in tools. |
| Anthropic first-party API | | Everything. |

Observed behaviour that the gateway relies on:

- `model_version` is the model name the provider reports in its response, recorded as sent. A provider may report an alias, a dated snapshot or another model's name (z.ai does), and AIPlex does not map it to the requested `model_id`. Price and filter on `model_id`, which is the model AIPlex dispatched.
- An unknown model is refused by the access point with a protocol-native 404. For a caller the access point admitted, the ledger records `error_class` `model_not_found` with the requested name, sanitized and cut to 128 characters, as `model_id`. A caller the access point does not admit leaves no record. A request the provider rejects (400) is recorded as `error`/`invalid_request`.
- A wrong provider key is the gateway's own credential failing, so it is a `502` with `error_class` `upstream_auth` (see the Limits section). A 429 with error code `insufficient_quota` is classed `upstream_quota_exhausted` and passed through unchanged; that code comes from OpenAI's documentation and has been tested against a local upstream only.
- No key value appeared in responses, the API log, the ledger or the audit log (checked by searching for the literal values).
- With `--otel`, spans reached an OTLP/HTTP receiver. The `traceparent` header sent to the provider carried the trace and span ID of the ledger record, and both Google and z.ai accepted it.
- `gemini-embedding-001` reports usage only as a top-level `tokenCount` string. The Developer API omits `cachedContentTokenCount` and `thoughtsTokenCount` when they are zero, and the ledger records those as 0 for the Developer API. Gemini reports thinking tokens apart from output tokens, so the catalogue import prices `reasoning_tokens` for `gemini-developer` at the output rate.
- `promptTokensDetails` lists only the modalities present. When the list adds up to `promptTokenCount`, the ledger records the unlisted standard modalities as reported zeros. An image or audio count with no rate for that modality leaves the cost `unknown` rather than partially costed.
- `usageMetadata.serviceTier` was `standard` on every response and is recorded as `service_tier`; values other than `standard` come from the API reference, not observation.
- `usageMetadata.billedToolCalls` is recorded as `server_tools` (for example `{"google_search": 1}`). Cost stays `unknown` until a price rates `server_tool.google_search`.
- `aiplex usage prices backfill` prices records written before a price existed, leaves records with an unrated count unknown, and a second run changes nothing.

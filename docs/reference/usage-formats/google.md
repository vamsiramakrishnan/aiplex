# Google model APIs: token-usage and quota wire formats

Audit reference for usage observers. Every field name below was read in
Google's official documentation on 2026-11-09. Where a detail could not be
confirmed it is marked **UNCONFIRMED**. Fixtures that mirror each section live
in `pkg/modelapi/testdata/usage-formats/google/` (see `expected.json` there);
fixture filenames are given in the "Fixture" column.

Docs sites (both moved since older docs; old URLs 301 to these):

- Gemini Developer API: `https://aistudio.google.com/docs/...` and
  `https://aistudio.google.com/api-reference/...` (formerly `ai.google.dev`).
  Full text dump used for cross-checking: `https://aistudio.google.com/llms-full.txt`.
- Vertex AI, now titled "Gemini Enterprise Agent Platform":
  `https://docs.cloud.google.com/gemini-enterprise-agent-platform/...`.

Abbreviations: **DA** = Gemini Developer API (`generativelanguage.googleapis.com`),
**VX** = Vertex AI (`aiplatform.googleapis.com`).

## 0. Cross-cutting semantics (read first)

| Question | Answer | Source |
|---|---|---|
| Does prompt count include cached tokens? | Yes, on `generateContent`-style APIs: `promptTokenCount` "is still the total effective prompt size, including cached tokens"; `cachedContentTokenCount` is a subset. Interactions `total_input_tokens` also includes cached (implicit caching discounts the repeated input inside it). Partner Claude is the opposite (see 11). | DA `https://aistudio.google.com/api-reference/generate-content`; VX `https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/GenerateContentResponse` |
| Are thoughts inside candidates? | No. `thoughtsTokenCount` / `total_thought_tokens` are separate. Documented examples: VX Flex `3 + 900 + 1054 = 1957`; Interactions `62 + 171 + 297 = 530`; `7 + 23 + 49 = 79`. | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/flex-paygo`; `https://aistudio.google.com/docs/thinking`; `https://aistudio.google.com/api-reference/interactions` |
| Total formula | `totalTokenCount = promptTokenCount + candidatesTokenCount + toolUsePromptTokenCount + thoughtsTokenCount` (cached is inside prompt). Interactions: `total_tokens` = "prompt + responses + other internal tokens". | VX GenerateContentResponse page above; DA generate-content page |
| Stream chunks cumulative? | DA: "`usage_metadata` is cumulative within each stream. Keep only the last value of each stream, then add the values across hops." Interactions: `usage` on `status_update`/`completed` is cumulative for the hop (or whole interaction in background mode). VX: not stated (UNCONFIRMED). | `https://aistudio.google.com/docs/long-decode?generate-content=true`; `https://aistudio.google.com/docs/long-decode` |
| Multi-hop (long decode, `finishReason: CONTINUATION`) | Each REST response reports only its own hop: add every field across hops for cost; keep the first hop's `promptTokenCount` as prompt size. SDK auto-continuation sums fields itself (`initial_prompt_token_count` is computed client-side, not in REST). Summed prompt/total are NOT context size. | `https://aistudio.google.com/docs/long-decode?generate-content=true` |
| Price class indicator | DA: `usageMetadata.serviceTier` (`unspecified|standard|flex|priority`) and response header `x-gemini-service-tier` (priority downgrade check). VX: `usageMetadata.trafficType` (`TRAFFIC_TYPE_UNSPECIFIED|ON_DEMAND|ON_DEMAND_PRIORITY|ON_DEMAND_FLEX|PROVISIONED_THROUGHPUT`), OpenAI-compat `usage.extra_properties.google.traffic_type`. | DA generate-content page; `https://aistudio.google.com/docs/priority-inference`; VX GenerateContentResponse page |

`ModalityTokenCount` = `{ "modality": "MODALITY_UNSPECIFIED|TEXT|IMAGE|VIDEO|AUDIO|DOCUMENT", "tokenCount": int }` (DA enum list; VX uses the same type name).

## 1. generateContent (JSON)

| Item | DA | VX |
|---|---|---|
| Endpoint | `POST https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent` | `POST https://{service-endpoint}/v1/{model}:generateContent`, `{model}` = `projects/{p}/locations/{l}/publishers/*/models/*` or a tuned endpoint |
| Model id | URL path; echoed as `modelVersion` | URL path; echoed as `modelVersion` |
| Usage location | top-level `usageMetadata` | top-level `usageMetadata` |
| Counters | `promptTokenCount`, `cachedContentTokenCount`, `candidatesTokenCount`, `toolUsePromptTokenCount`, `thoughtsTokenCount`, `totalTokenCount`, `promptTokensDetails[]`, `cacheTokensDetails[]`, `candidatesTokensDetails[]`, `toolUsePromptTokensDetails[]`, `serviceTier` | same names (no `serviceTier`) plus `trafficType` |
| Semantics | prompt includes cached; thoughts separate; tool-use prompt separate and inside total | same |
| Terminal signal | `candidates[].finishReason` (`STOP`, `MAX_TOKENS`, `SAFETY`, ... `CONTINUATION`, `MALFORMED_FUNCTION_CALL`, `NO_IMAGE`, ...) | `candidates[].finishReason` (`STOP`, `MAX_TOKENS`, `SAFETY`, `MODEL_ARMOR`, ...; Gemini 4 docs also show `FINISH_REASON_CONTINUATION`) |
| Usage absent | Blocked prompt: `promptFeedback.blockReason`, no candidates (usage presence UNCONFIRMED). Zero counters appear to be omitted rather than sent as 0 (UNCONFIRMED). Error responses: no body usage. | Failed requests: `usageMetadata` and `trafficType` absent (logging doc). Same zero-omission caveat. |
| Error shape | `{error:{code,message,status,details[]}}`, `details` e.g. `google.rpc.ErrorInfo`, `LocalizedMessage` | standard Google error envelope (UNCONFIRMED on Vertex pages); 429 messages in section 12 |
| Fixtures | `gemini-generate.json`, `gemini-generate-tooluse.json`, `gemini-generate-error-429.json` | `vertex-generate.json`, `vertex-generate-flex.json`, `vertex-generate-image.json`, `vertex-generate-error-429.json` |
| Source | `https://aistudio.google.com/api-reference/generate-content`; errors `https://aistudio.google.com/docs/api-errors?generate-content=true` | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/GenerateContentResponse`; `.../reference/rest/v1/projects.locations.publishers.models/generateContent` |

Notes:

- Image-output Gemini models: image tokens are counted in `candidatesTokensDetails` as an `IMAGE` entry (VX logging doc). Agentic video understanding maps initial prompt to `promptTokenCount`, navigation thinking to `thoughtsTokenCount`, on-demand transcript/frames/audio to `toolUsePromptTokenCount`, final answer to `candidatesTokenCount` (DA tokens doc).
- DA `totalTokenCount` description on the generate page includes thoughts and tool-use; the Live API page says "(prompt + response candidates)" (see 5).
- Gemini 4 long decode: a hop can return empty text, `finishReason: CONTINUATION`, `candidatesTokenCount: 0` and large `thoughtsTokenCount` (documented example `142 / 0 / 32768 / 32910`).
- `serviceTier` request value `priority` can be silently downgraded to standard; detect with `usageMetadata.serviceTier` or response header `x-gemini-service-tier: standard`.

## 2. streamGenerateContent (SSE and JSON array)

| Item | DA | VX |
|---|---|---|
| Endpoint | `POST /v1beta/models/{model}:streamGenerateContent?alt=sse` (SSE, `alt=sse` appears in the curl examples) | `POST https://{service-endpoint}/v1/{model}:streamGenerateContent`; response "a stream of `GenerateContentResponse` instances" |
| Framing | SSE: `data: <GenerateContentResponse JSON>` per chunk (framing text not shown in docs; UNCONFIRMED). Without `alt=sse`: JSON array form (UNCONFIRMED in docs). | SSE with `alt=sse` is the SDK default (UNCONFIRMED in the pages read); JSON array otherwise (UNCONFIRMED) |
| Usage location | `usageMetadata` on chunks | `usageMetadata` on chunks |
| Cumulative? | Yes within a stream (long-decode doc). Which chunks carry it: UNCONFIRMED. | UNCONFIRMED. Fixtures cover both "only final chunk" and "every chunk, growing". |
| Final chunk | The chunk with `finishReason` set; its `usageMetadata` is authoritative | same |
| First-chunk-only | - | `promptFeedback` "Sent only in the first stream chunk" (VX GenerateContentResponse page) |
| Interrupted | no `finishReason`, no terminator; last snapshot is a lower bound | same; usage may be entirely absent |
| Long decode | Stream caps at 65,536 output tokens per hop, then `CONTINUATION` + `continuationToken` (also emitted every 5,000 tokens after the first 32,768 as checkpoints). Server closes streams after 20 min, non-stream after 10 min. | `continuationToken` field documented on Gemini 4 |
| Fixtures | `gemini-generate.sse`, `gemini-generate.stream.json`, `gemini-generate-interrupted.sse` | `vertex-generate.sse` (usage on last), `vertex-generate-cumulative.sse` (usage every chunk), `vertex-generate.stream.json`, `vertex-generate-interrupted.sse` |
| Source | `https://aistudio.google.com/api-reference/generate-content/stream-generate-content`; `https://aistudio.google.com/docs/long-decode?generate-content=true` | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.publishers.models/streamGenerateContent` |

Multiple candidates (`candidateCount > 1`) are a Preview feature on VX and not supported with `streamGenerateContent` there.

## 3. countTokens

| Item | DA | VX |
|---|---|---|
| Endpoint | `POST /v1beta/models/{model}:countTokens` | `POST https://{service-endpoint}/v1/{endpoint}:countTokens` (path param `endpoint`; optional body `model`) |
| Response | `totalTokens`, `cachedContentTokenCount`, `promptTokensDetails[]`, `cacheTokensDetails[]` | `totalTokens`, `totalBillableCharacters`, `promptTokensDetails[]` |
| Semantics | "total number of tokens in the input only"; not a consumption event | same (input only) |
| Terminal / absent | single JSON response; no streaming | same |
| Fixtures | `gemini-count-tokens.json` | `vertex-count-tokens.json` |
| Source | `https://aistudio.google.com/api-reference/tokens/count-tokens`; `https://aistudio.google.com/docs/tokens` | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/CountTokensResponse` |

Claude on VX has its own count-tokens endpoint `.../publishers/anthropic/models/count-tokens:rawPredict` returning `{ "input_tokens": 14 }`, default quota 2000 requests/min; such calls appear in logs as `RAW_PREDICT` with `model_user_id` `count-tokens` and no usage (`https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/partner-models/claude/count-tokens`).

## 4. Embeddings

| Item | DA | VX |
|---|---|---|
| Endpoints | `POST /v1beta/models/{model}:embedContent`, `:batchEmbedContents`, `:asyncBatchEmbedContent` | `POST .../publishers/google/models/{model}:predict` (Gemini embedding example) |
| Usage location | `usageMetadata` (`EmbeddingUsageMetadata`): `promptTokenCount`, `promptTokenDetails[]` (singular "Token" in the details name) | `predictions[].embeddings.statistics.token_count`, `statistics.truncated`. SDK also shows `metadata.billableCharacterCount` (REST path UNCONFIRMED) |
| Semantics | input tokens only; no total; no output tokens | same |
| Batch | `asyncBatchEmbedContent` output: `EmbedContentBatch.output` with `responsesFile` (JSONL of `EmbedContentResponse`) or `inlinedResponses`; `batchStats` has request counts only. Per-item usage in those responses: UNCONFIRMED. | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/embeddings/batch-prediction-genai-embeddings` not read; UNCONFIRMED |
| Fixtures | `gemini-embed.json` | `vertex-embed-predict.json` |
| Source | `https://aistudio.google.com/api-reference/embeddings` | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/embeddings/get-text-embeddings` |

## 5. Live API (WebSocket)

| Item | DA | VX |
|---|---|---|
| Endpoint | `wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent?key=...` (also `...BidiGenerateContentConstrained?access_token=` for ephemeral tokens; `v1alpha` variant appears in one guide) | `wss://{LOCATION}-aiplatform.googleapis.com/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent` with `Authorization: Bearer` |
| Model id | `setup.model` in the first client message | `setup.model` = `projects/{p}/locations/{l}/publishers/google/models/{id}` |
| Server message envelope | exactly one of `setupComplete`, `serverContent`, `toolCall`, `toolCallCancellation`, `goAway`, `sessionResumptionUpdate`, plus `usageMetadata` and `voiceActivity` | one of `setupComplete`, `serverContent`, `toolCall`, `toolCallCancellation`, `usageMetadata`, `goAway`, `sessionResumptionUpdate`, `inputTranscription`, `outputTranscription` (transcriptions top-level here) |
| Usage location | its own top-level `usageMetadata` message: `promptTokenCount`, `cachedContentTokenCount`, `responseTokenCount`, `toolUsePromptTokenCount`, `thoughtsTokenCount`, `totalTokenCount`, `promptTokensDetails[]`, `cacheTokensDetails[]`, `responseTokensDetails[]`, `toolUsePromptTokensDetails[]` (**`response*`, not `candidates*`**) | described as "a report of the number of tokens used by the session so far". Field names UNCONFIRMED; the VX v1 `UsageMetadata` message class has `candidatesTokenCount`, `candidatesTokensDetails`, `thoughtsTokenCount`, `cachedContentTokenCount`, `trafficType` etc. (client-library reference), but no page shows the Live JSON. |
| Cumulative vs per-turn | UNCONFIRMED. Doc: "The server will periodically send messages that include UsageMetadata"; pricing doc says every turn is billed for the whole accumulated context window. | "session so far" (cumulative) |
| Modality detail | audio/text/video via `*TokensDetails[].modality` (`AUDIO`, `TEXT`, `VIDEO`, `IMAGE`, `DOCUMENT`); doc example reads `usage.response_tokens_details` | same type |
| Turn signals | `serverContent.generationComplete`, `serverContent.turnComplete` (sent after playback finishes when playback reporting is on), `serverContent.interrupted` (an interrupted turn goes `interrupted` -> `turnComplete`, no `generationComplete`), `interactionStatus` (`IN_PROGRESS`/`IDLE`) | same fields (`interrupted`, `generation_complete`, `turn_complete`, `TurnCompleteReason`) |
| Session control | `sessionResumptionUpdate{newHandle,resumable}` (only if `sessionResumption` set; resumable false while generating or running calls; handle valid 2 h); `goAway{timeLeft}` before termination (connection ~10 min; audio-only session 15 min, audio+video 2 min without context-window compression) | same documented semantics |
| `totalTokenCount` text | "(prompt + response candidates)" - does not mention thoughts/tool-use | UNCONFIRMED |
| Usage absent | abrupt close without a `usageMetadata` message; after resumption whether counters restart is UNCONFIRMED | same |
| Fixtures | `gemini-live.ws.jsonl`, `gemini-live-interrupted.ws.jsonl` | `vertex-live.ws.jsonl`, `vertex-live-interrupted.ws.jsonl` |
| Source | `https://aistudio.google.com/api-reference/live`; `https://aistudio.google.com/docs/live-api/capabilities` ("Token count"); `https://aistudio.google.com/docs/live-api/session-management` | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/models/live-api`; `.../models/live-api/start-manage-session`; `https://docs.cloud.google.com/dotnet/docs/reference/Google.Cloud.AIPlatform.V1/latest/Google.Cloud.AIPlatform.V1.UsageMetadata` |

Audio transcription (`inputAudioTranscription`/`outputAudioTranscription`) text tokens are billed at the text output rate in addition to audio token costs (DA pricing section of the Live docs: `https://aistudio.google.com/docs/live-api/best-practices`). Proactive audio mode bills input the whole time it listens.

## 6. Interactions API

| Item | DA | VX |
|---|---|---|
| Endpoints | `POST /v1beta/interactions` (create, `stream`, `background`), `GET /v1beta/interactions/{id}`, `POST /v1beta/interactions/{id}/cancel`, `DELETE /v1beta/interactions/{id}` | `POST https://aiplatform.googleapis.com/v1/projects/{p}/locations/global/interactions`, `GET .../interactions/{id}` (`stream`, `last_event_id`), `POST .../{id}/cancel` |
| Model id | request body `model`; echoed in `interaction.model` | request body |
| Usage location | `interaction.usage` (`Usage`): `total_input_tokens`, `initial_input_tokens`, `input_tokens_by_modality[]`, `total_cached_tokens`, `cached_tokens_by_modality[]`, `total_output_tokens`, `output_tokens_by_modality[]`, `total_tool_use_tokens`, `tool_use_tokens_by_modality[]`, `total_thought_tokens`, `total_tokens`, `grounding_tool_count[{type: google_search|google_maps, count}]`. Modality items: `{modality: text|image|audio|video|document, tokens}` | same schema; documented example omits unused keys |
| Semantics | input includes cached; thoughts separate from output; `total_tokens` = prompt + responses + other internal tokens | same |
| Streaming | SSE events `interaction.created`, `interaction.status_update` (usage "up to this checkpoint", plus `continuation_token`), `step.start`, `step.delta`, `step.stop`, `interaction.completed` (usage), `error`, then `done` with `data: [DONE]`. Each event has `event_id` to resume. | same event names |
| Terminal signal | `interaction.completed` event (also fires on cancel or natural end); `status` one of `in_progress`, `requires_action`, `completed`, `failed`, `cancelled`, `incomplete`, `requires_continuation` | `interaction.completed`, `status: completed` |
| Background | `background=true`, poll with GET; runs up to 24 h; `store=false` incompatible. Cancelled/failed interactions are billed for completed work. | cancel only for in-progress background interactions; partial tokens billed |
| Errors | non-stream: HTTP status + `{error:{code:<snake_case>,message}}`; stream: `{event_type:"error", error:{code,message}}`. Codes include `rate_limit_exceeded`, `quota_exceeded`, `too_many_requests` (429), `payment_required` (402). | not read; UNCONFIRMED |
| Multi-hop | foreground long decode: each hop is a separate interaction, sum the final `usage` of each; `total_input_tokens` is the sum of hop inputs; context size = `initial_input_tokens` + `total_output_tokens` + `total_thought_tokens` | UNCONFIRMED |
| Fixtures | `gemini-interactions.json`, `gemini-interactions.sse`, `gemini-interactions-interrupted.sse` | `vertex-interactions.json`, `vertex-interactions.sse`, `vertex-interactions-interrupted.sse` |
| Source | `https://aistudio.google.com/api-reference/interactions`; `https://aistudio.google.com/docs/api-errors`; `https://aistudio.google.com/docs/long-decode`; `https://aistudio.google.com/docs/thinking` | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/models/interactions-api` |

## 7. OpenAI-compatible chat completions

| Item | DA | VX |
|---|---|---|
| Endpoint | `https://generativelanguage.googleapis.com/v1beta/openai/` base URL (`/chat/completions`) | `POST https://aiplatform.googleapis.com/v1/projects/{p}/locations/{l}/endpoints/openapi/chat/completions` (also `v1beta1` and regional hosts) |
| Model id | request body `model` (e.g. `gemini-...`) | request body `model` with `google/` prefix |
| Usage shape | `stream_options={'include_usage': True}` is shown; usage JSON shape UNCONFIRMED (not shown) | `usage: {prompt_tokens, completion_tokens, total_tokens, completion_tokens_details:{reasoning_tokens}, extra_properties:{google:{traffic_type}}}` |
| Semantics | UNCONFIRMED | `completion_tokens` EXCLUDES reasoning: documented stream `27 + 45 + 504 = 576`. `traffic_type` mirrors `trafficType`. No cached-token field documented. |
| Streaming | SSE | data-only SSE (`data: {...chat.completion.chunk...}`), final data line `data: [DONE]`. In the documented example, usage is on the chunk that has `finish_reason` (no separate usage-only chunk). |
| Google extensions | `extra_body.google.cached_content`; `extra_content` for thought signatures | `extra_body.google.thinking_config`, `reasoning_effort`, `extra_content.google.thought_signature` on tool-call deltas |
| Fixtures | none (shape unconfirmed) | `vertex-openai-chat.json`, `vertex-openai-chat.sse`, `vertex-openai-chat-interrupted.sse` |
| Source | `https://aistudio.google.com/docs/openai` | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/migrate/openai/examples`; `.../migrate/openai/overview` |

## 8. Vertex partner and open models

| Transport | Endpoint and model id | Usage location and semantics | Terminal / absent | Fixture | Source |
|---|---|---|---|---|---|
| Claude `rawPredict` | `POST .../publishers/anthropic/models/{model}:rawPredict`; model in path | Anthropic Messages body `usage`: documented `{input_tokens, output_tokens}`; Google's log schema also lists `cache_read_input_tokens`, `cache_creation_input_tokens`, `cache_creation.ephemeral_5m_input_tokens`, `cache_creation.ephemeral_1h_input_tokens`. `input_tokens` EXCLUDES cache read/write ("Total Claude input is input_tokens plus cache_read plus cache_creation"); `output_tokens` includes extended thinking; no modality breakdown; no total. | `stop_reason`; absent on error | `vertex-claude-rawpredict.json` | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/partner-models/claude/use-claude`; `.../models/capabilities/inference-metadata-logging` |
| Claude `streamRawPredict` | `...:streamRawPredict`, body `stream:true` | Same fields; event shapes (`message_start` with input and cache counters and placeholder `output_tokens`, `message_delta.usage.output_tokens` cumulative, `message_stop`) are from Anthropic's doc (secondary): `https://platform.claude.com/docs/en/build-with-claude/streaming`. Google confirms only that logs "merge the usage reported in the stream". | `message_stop`; interrupted stream leaves output unknown | `vertex-claude-streamrawpredict.sse`, `vertex-claude-streamrawpredict-interrupted.sse` | use-claude page above |
| Other partner `rawPredict` | generic `HttpBody`; response format "defined by the method" | No Google-defined usage; per-model | UNCONFIRMED | none | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.publishers.models/rawPredict` |
| MaaS chat (open models) | `POST https://aiplatform.googleapis.com/v1/projects/{p}/locations/global/endpoints/openapi/chat/completions`; model in body | `usage:{completion_tokens,prompt_tokens,total_tokens}`; streaming final chunk holds `delta.content`, `finish_reason:"stop"` and `usage` together, then `[DONE]`. Implicit caching (some models, pay-as-you-go only) reports "`cachedContentTokenCount` in your response's metadata": location in chat JSON UNCONFIRMED. | `finish_reason` | `vertex-maas-chat.json`, `vertex-maas-chat.sse` | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/maas/call-open-model-apis`; `.../models/maas/use-open-models` |
| Imagen / `:predict` media | `:predict` | No usage documented; logging doc says Imagen-style `predict` is not logged | - | none | inference-metadata-logging page |

Request-metadata logging (Cloud Logging, `aiplatform.googleapis.com/inference_request`) is the only place Google documents a unified per-request usage record: `generateContentDetails.usageMetadata` for Gemini, `rawPredictDetails.usageMetadata` for Claude, `trafficType`, and Provisioned Throughput fields `ptChargedInputTokenCount`, `ptChargedOutputTokenCount`, `accountingResource` (already burn-down adjusted). Not logged: chat completions, `embedContent`, `predict`, `countTokens`, tuned models, batch.

## 9. Batch

| Item | DA | VX |
|---|---|---|
| Endpoints | `POST /v1beta/models/{model}:batchGenerateContent`, `GET /v1beta/batches/{id}`, `POST .../{id}:cancel`, `DELETE`; embeddings `:asyncBatchEmbedContent` | batch inference jobs from Cloud Storage or BigQuery |
| Per-request usage | Inline: `output.inlinedResponses.inlinedResponses[]` items with `response` (a `GenerateContentResponse` including `usageMetadata`) or `error` (`Status`), plus `metadata`. File: `responsesFile` JSONL, "each line is either a `GenerateContentResponse` or a status object" (per-line wrapper/key UNCONFIRMED). Input lines carry `key` + `request`. | GCS JSONL line: `{status, processed_time, request, response}`; success has `status:""` and `response.usageMetadata` (`promptTokenCount`, `candidatesTokenCount`, `totalTokenCount`, and the same optional fields); failure has non-empty `status` and `response:{}`. BigQuery: `response` column. |
| Job-level totals | `batchStats` has only `requestCount`, `successfulRequestCount`, `failedRequestCount`, `pendingRequestCount`; states `BATCH_STATE_{PENDING,RUNNING,SUCCEEDED,FAILED,CANCELLED,EXPIRED}`; no token totals | not documented |
| Terminal signal | batch `state` | job state; completed rows are exported even if the job is terminated; only completed inferences are charged |
| Fixtures | `gemini-batch-inlined.json` | `vertex-batch-gcs.jsonl` |
| Source | `https://aistudio.google.com/api-reference/batch-api`; `https://aistudio.google.com/docs/batch-api` | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/capabilities/batch-inference/new-job-from-cloud-storage`; `.../new-job-from-bigquery` |

## 10. Context caching

| Item | DA | VX |
|---|---|---|
| Create/get/list | `POST|GET /v1beta/cachedContents[/{id}]`; response `usageMetadata.totalTokenCount` = tokens the cached content consumes | explicit cache resource `projects/{p}/locations/{l}/cachedContents/{id}`; `usageMetadata` on cache metadata (UNCONFIRMED shape) |
| Hit reporting | next `generateContent`/stream: `usageMetadata.cachedContentTokenCount` (subset of `promptTokenCount`), `cacheTokensDetails[]`; Interactions: `usage.total_cached_tokens`. Implicit caching: Gemini 2.5+; same field. Min inputs: 2,048 (2.5), 4,096 (3.x/4 on Interactions). Explicit cache disables implicit caching for that request. | same field names; "for both implicit and explicit caching, `cachedContentTokenCount` indicates the number of tokens in the cached part of your input"; 90% discount |
| Cache write | explicit creation billed at creation (storage billed separately); no cache-write counter on generate responses | no cache-write counter on generate responses; Claude reports `cache_creation_input_tokens` |
| Source | `https://aistudio.google.com/docs/caching`; `https://aistudio.google.com/api-reference/caching` | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/context-cache/context-cache-overview`; `.../context-cache-use` |

Fixtures: `gemini-cachedcontent-create.json`; hits in `gemini-generate.json`, `vertex-generate.json`, `gemini-interactions.json`.

## 11. Other transports

- gRPC: no page read documents any usage difference from REST. REST JSON uses camelCase; descriptions reference proto snake_case (`prompt_token_count`, ...). Bidi streaming is exposed as WebSocket (`...GenerativeService.BidiGenerateContent`, `...LlmBidiService/BidiGenerateContent`). Vertex lists `serverStreamingPredict`, `directPredict`, `directRawPredict` methods with no usage description read. **UNCONFIRMED.**
- Long-running predict (Veo video on DA): `POST /v1beta/models/{model}:predictLongRunning`, poll operation `done`, result at `response.generateVideoResponse.generatedSamples[].video.uri`; no usage in that result. Billing is per output second (pricing page). Fixture `gemini-veo-operation.json`. Vertex Veo/Imagen: UNCONFIRMED.
- Image models: Gemini image models via generateContent report image output tokens (pricing page gives per-resolution token counts, e.g. 1120 tokens for 1024x1024 on one model); `IMAGE` appears in `candidatesTokensDetails`.

## 12. Quota metadata

| Topic | DA | VX |
|---|---|---|
| 429 meaning | `429 RESOURCE_EXHAUSTED`: exceeded RPM, TPM (input), RPD, spend-based limit (rolling 10-minute window) etc. Limits are per project; any single dimension trips it. RPD resets midnight Pacific. | Pay-as-you-go: message `Resource exhausted, please try again later.` Provisioned Throughput: `Too many requests. Exceeded the Provisioned Throughput.` Under Standard PayGo "it doesn't indicate that you've hit a fixed quota" - shared-pool contention. Also acceleration limits on sharp usage ramps. |
| Body shape | `{error:{code:429,message,status:"RESOURCE_EXHAUSTED",details[]}}`; details contents for 429 (QuotaFailure, RetryInfo) **UNCONFIRMED**. Interactions API: `{error:{code:"rate_limit_exceeded"|"quota_exceeded"|"too_many_requests",message}}` | Body details **UNCONFIRMED**. Logs record gRPC code `8` for these. |
| Headers | none documented for quota (no `Retry-After`, no rate-limit headers). Only `x-gemini-service-tier` for priority. | none documented for responses. Request headers choose class: `X-Vertex-AI-LLM-Request-Type: dedicated|shared`, `X-Vertex-AI-LLM-Shared-Request-Type: priority|flex`. `rawPredict` responses carry `X-Vertex-AI-Endpoint-id`, `X-Vertex-AI-Deployed-Model-id`. |
| 402 | Prepay credits depleted: HTTP 402 with status `RESOURCE_EXHAUSTED` (legacy table) or `payment_required` (Interactions). Do not retry. | - |
| Retry guidance | retry 429, 408, 5xx with exponential backoff and jitter; never 400/402/403. SDK default up to 4 retries, 1 s initial, 60 s max. | truncated exponential backoff; use the `global` endpoint |
| Price/priority class | `serviceTier` standard/flex/priority; priority over limit is downgraded to standard (no error); flex is sheddable and fails with 503; priority default limits 0.3x standard | `trafficType` per response |
| Provisioned Throughput | n/a | Default: overage runs on-demand (spillover). `X-Vertex-AI-LLM-Request-Type: dedicated` makes overage return 429; `shared` bypasses PT. Response indicator: `trafficType: PROVISIONED_THROUGHPUT`; spillover = `ON_DEMAND` with no `provisionedThroughput` object when PT exists for model/region. Standard PT with unused capacity can turn would-be 429s into 5xx. |
| Dynamic shared quota | n/a | The docs now describe "Standard PayGo" shared capacity with spend-based usage tiers (30-day rolling spend, org-level TPM thresholds, excess burst throttled first, thresholds apply to `global` endpoint) and "Priority PayGo" (downgraded to `ON_DEMAND` only when no spare capacity; `trafficType` tells). The term "dynamic shared quota" was not found in the pages read; the old URL `.../learn/dynamic-shared-quota` redirects to Standard PayGo. |
| Live API | concurrent-session limits on the Developer API: UNCONFIRMED (not on the rate-limits page read) | concurrent session quota checked at session start, e.g. 1,000 per project by default for one native-audio model (start-manage-session page); error shape on rejection UNCONFIRMED |
| Source | `https://aistudio.google.com/docs/rate-limits`; `https://aistudio.google.com/docs/api-errors?generate-content=true`; `https://aistudio.google.com/docs/api-errors`; `https://aistudio.google.com/docs/troubleshooting`; `https://aistudio.google.com/docs/priority-inference`; `.../flex-inference` | `https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/deploy/error-code-429`; `.../models/standard-paygo`; `.../models/priority-paygo`; `.../models/flex-paygo`; `.../models/provisioned-throughput/use-provisioned-throughput`; `.../models/capabilities/inference-metadata-logging` |

Fixtures: `gemini-generate-error-429.json`, `vertex-generate-error-429.json`.

## UNCONFIRMED / open questions

1. DA and VX `streamGenerateContent`: exact SSE/array framing text and which chunks carry `usageMetadata` (docs only say DA usage is cumulative within a stream).
2. VX: whether stream `usageMetadata` is cumulative (assumed from DA).
3. DA Live: `usageMetadata` cumulative vs per-turn; whether `thoughtsTokenCount`/`toolUsePromptTokenCount` appear and are inside `totalTokenCount` (the Live ref says total = prompt + response); emission timing; counter reset after session resumption.
4. VX Live: JSON field names of the `usageMetadata` message (`candidates*` vs `response*`); trafficType presence.
5. 429 body `details[]` (QuotaFailure / RetryInfo) and any `Retry-After` or rate-limit headers on both DA and VX.
6. Proto3 zero-omission: whether absent counters (`cachedContentTokenCount`, `thoughtsTokenCount`) mean 0.
7. Billing of client-aborted streams and Live sessions closed before usage is sent.
8. DA OpenAI-compat usage shape; VX OpenAI-compat cached-token field and `stream_options` handling.
9. MaaS open models: where `cachedContentTokenCount` appears in chat JSON; whether `completion_tokens` includes thinking.
10. Partner Claude `streamRawPredict` event shapes from Google's own docs (only Anthropic's doc used); whether `cache_creation` sub-object is in the raw payload.
11. Batch file-output per-line wrapper on DA; VX `trafficType` in batch rows; job-level token totals.
12. Embeddings: per-item usage for DA batch embeddings; REST location of VX `billableCharacterCount`.
13. Vertex Interactions: error shape and multi-hop behaviour; whether `trafficType` exists there.
14. Veo/Imagen/Lyria on Vertex and any usage on long-running operation results.
15. gRPC-specific differences.
16. Billing class of `toolUsePromptTokenCount` and of cache creation on the Developer API.
17. Whether `promptFeedback`-blocked responses carry `usageMetadata`.

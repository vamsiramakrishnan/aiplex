# Anthropic: token usage and rate-limit formats

Audit reference for usage observers. Gathered 2026-11-09 from official docs (URLs under each section).
Fixtures: `pkg/modelapi/testdata/usage-formats/anthropic/` (see its README; `expected.json` has normalized totals).
Details that the provider's documentation does not state are marked **UNCONFIRMED**.

Hosting platforms differ in endpoint and wrapper, not (as far as documented) in the Messages `usage` object. Bedrock Converse is the exception: its own `usage` shape.

Base docs: `https://platform.claude.com/docs/en/...` (old `docs.claude.com` URLs 301 here).

## 1. Counter semantics (Messages API family)

Source: https://platform.claude.com/docs/en/api/messages/create (Usage schema), https://platform.claude.com/docs/en/build-with-claude/prompt-caching, https://platform.claude.com/docs/en/api/rate-limits

| JSON path (in `usage`) | Type / nullability | Meaning |
|---|---|---|
| `input_tokens` | number | Input tokens NOT read from and NOT used to create cache, i.e. tokens after the last cache breakpoint. Exclusive of cache. |
| `cache_creation_input_tokens` | number or null | Tokens written to cache. |
| `cache_read_input_tokens` | number or null | Tokens read from cache. |
| `cache_creation.ephemeral_5m_input_tokens` / `.ephemeral_1h_input_tokens` | object or null; each default 0 | Per-TTL breakdown. Docs: `cache_creation_input_tokens` equals the sum of the two. |
| `output_tokens` | number | Output tokens, inclusive of thinking. Non-zero even for empty text. |
| `output_tokens_details.thinking_tokens` | object or null | Subset of `output_tokens` spent on reasoning (includes thinking-block delimiter tokens), raw not summarized count, <= `output_tokens`. `output_tokens` is "the inclusive, authoritative total used for billing". |
| `server_tool_use.web_search_requests` / `.web_fetch_requests` | object or null | Per-request counters for server tools (not tokens). Code execution doc example also shows `server_tool_use.code_execution_requests` (not in the schema listing). |
| `service_tier` | `"standard"` / `"priority"` / `"batch"` / null | Tier used. |
| `speed` | `"standard"` / `"fast"` / null | Fast mode. Fast has its own rate-limit headers (`anthropic-fast-*`). |
| `inference_geo` | string or null | Region where inference ran. |
| `iterations[]` | array (seen only in the pre-warm example) | Per-iteration copies of the counters with `type: "message"`. Semantics for multi-iteration turns **UNCONFIRMED**; do not add to top-level. |

- Total input = `input_tokens + cache_creation_input_tokens + cache_read_input_tokens` (stated in both docs). Example: 100,000 read + 0 written + 50 = 100,050.
- Thinking: tokens are billed as output "even when the thinking text isn't returned", count toward `max_tokens`, and the visible thinking text is a summary, so billed output does not match visible token count. Reasoning is inside `output_tokens`; the separate `output_tokens_details.thinking_tokens` is a decomposition. When streaming, "this breakdown appears only on the final `message_delta` event" (extended-thinking page: https://platform.claude.com/docs/en/build-with-claude/extended-thinking, thinking page: https://platform.claude.com/docs/en/build-with-claude/thinking).
- Usage can be absent: batch example lines show `usage` with only `input_tokens`/`output_tokens`; the documented streaming-thinking example has no `usage` in `message_start` or `message_delta`; `message_delta.usage.input_tokens`, cache counters, `output_tokens_details`, `server_tool_use` are nullable. Whether the thinking example is real behavior or elision is **UNCONFIRMED**.
- Rate-limit accounting (https://platform.claude.com/docs/en/api/rate-limits): ITPM counts `input_tokens` + `cache_creation_input_tokens`, NOT `cache_read_input_tokens`; estimated at request start and adjusted; OTPM counted as tokens are produced and `max_tokens` does not factor in. Count-tokens has separate RPM limits.

## 2. Surfaces and transports

### 2.1 Messages API, JSON

Source: https://platform.claude.com/docs/en/api/messages/create

| Item | Value |
|---|---|
| Endpoint | `POST https://api.anthropic.com/v1/messages`, headers `x-api-key`, `anthropic-version` (docs examples show `2026-10-21`), `content-type: application/json` |
| Model id | request body `model` (also echoed in response `model`) |
| Usage location | response body `usage` (section 1) |
| Terminal signal | the response itself; `stop_reason` always non-null in non-streaming (`end_turn`, `max_tokens`, `stop_sequence`, `tool_use`, `pause_turn`, `refusal`, `model_context_window_exceeded`). Also `stop_details` (refusal) |
| Absent usage | not expected on success; errors (4xx/5xx) have no usage |
| Fixtures | `messages.json`, `messages-iterations.json`, `messages-code-execution.json` |

### 2.2 Messages API, SSE streaming

Source: https://platform.claude.com/docs/en/build-with-claude/streaming

Same path, body `"stream": true`. Event flow: `message_start` (Message with empty `content`) -> per block `content_block_start`, N x `content_block_delta`, `content_block_stop` -> one or more `message_delta` -> `message_stop`. Any number of `ping` events may appear anywhere. Unknown event types must be tolerated.

| Event | Usage content |
|---|---|
| `message_start` | `message.usage`: `input_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `cache_creation{...}` (per-TTL, per prompt-caching page "or `message_start` event if streaming"), and a PLACEHOLDER `output_tokens` (docs examples: 1, 2, 3). Docs show some streams where `usage` is missing entirely. |
| `message_delta` | `usage` with **cumulative** counts (docs: "The token counts shown in the `usage` field of the `message_delta` event are cumulative"). Schema (MessageDeltaUsage): `input_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens` (nullable), `output_tokens` (required), `output_tokens_details`, `server_tool_use`. No `cache_creation` object, no `service_tier` in the delta schema. Docs examples: delta with only `{"output_tokens":15}`; and web-search delta with input 10682 vs start 2679 (so delta input REPLACES start input). |
| `ping`, `content_block_*` | none |
| `message_stop` | none; terminal event. `message_delta.delta.stop_reason` is non-null here (null in `message_start`) |
| `error` | `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}` delivered after HTTP 200 (corresponds to a 529). No usage follows; stream has no `message_stop`. |

Observer rule: per field, final = last non-null value seen (delta overrides start); never sum start and delta. Interrupted stream (no `message_stop`): input-side counters from `message_start` are reliable; `output_tokens` is only a lower-bound placeholder.
Fixtures: `messages.sse`, `messages-server-tool.sse`, `messages-usage-absent.sse`, `messages-interrupted.sse`, `messages-error-midstream.sse`.
Error recovery docs (https://platform.claude.com/docs/en/build-with-claude/streaming#error-recovery): resuming from partial responses exists for older models; resumed requests are new requests with their own usage.

### 2.3 Token counting

Source: https://platform.claude.com/docs/en/build-with-claude/token-counting

`POST /v1/messages/count_tokens`, same body shape as Messages. Response `{"input_tokens": N}` only (docs examples 14, 403, 1028, 88, 2188). An ESTIMATE; free; "not billed for system-added tokens"; no caching logic is run, so no cache counters; no `usage` wrapper. Own RPM limits (Start 5,000 / Build 10,000 / Scale 20,000), independent of message creation limits. Rejects server tools, the MCP connector, and `url`/`file` image/document sources with `invalid_request_error`. A meter must not treat it as consumption. Fixture: `count-tokens.json`.

### 2.4 Message Batches

Sources: https://platform.claude.com/docs/en/build-with-claude/batch-processing, https://platform.claude.com/docs/en/api/messages/batches/results

- Create returns batch object with `processing_status`, `request_counts{processing,succeeded,errored,canceled,expired}`, `results_url` (null until ended).
- Results: `GET /v1/messages/batches/{id}/results`, `.jsonl`, one line per request, ARBITRARY order, key by `custom_id`.
- Line: `{"custom_id": "...", "result": {"type": "succeeded", "message": {<full Message incl. usage>}}}`. Other types: `errored` (`error` = ErrorResponse `{type:"error", error:{type,message}, request_id}`), `canceled`, `expired`. Only `succeeded` has usage; the other three are not billed.
- `usage.service_tier` can be `batch`. Prices 50%. Cache hits are best-effort in batches. Fast mode unavailable; `max_tokens: 0` unsupported.
- No batch-level usage total is documented (**UNCONFIRMED**; meter by summing lines).
- Interrupted download: a truncated last line is invalid JSON; discard it and reconcile against `request_counts`.
Fixtures: `batch-results.jsonl`, `batch-results-partial.jsonl`.

### 2.5 Other inference surfaces

| Surface | Usage reporting | Source |
|---|---|---|
| Server tools (web search / fetch) | same `usage` plus `server_tool_use.web_search_requests` / `web_fetch_requests`; streaming: in final `message_delta`; server tool results enlarge `input_tokens` (see `messages-server-tool.sse`). Prompt-caching page notes server tool results get cached automatically (can show unexpected `ephemeral_5m_input_tokens`). | streaming page, prompt-caching page |
| Code execution | same Messages `usage`; `server_tool_use.code_execution_requests` in doc example; execution time billed separately by container hours (not in `usage`) | https://platform.claude.com/docs/en/agents-and-tools/tool-use/code-execution-tool |
| MCP connector (beta, `mcp_servers` + `mcp_toolset` in `tools`) | rides the Messages response (`mcp_tool_use` / `mcp_tool_result` blocks). No MCP-specific usage counters documented: **UNCONFIRMED**. Not countable via count_tokens. | https://platform.claude.com/docs/en/agents-and-tools/mcp-connector |
| Files API | files referenced in Messages requests count as ordinary input tokens in the same `usage`; Files API has its own request-rate limit. Per-file token accounting: **UNCONFIRMED** (page not read). | rate-limits page |
| Fast mode | `usage.speed`; own headers `anthropic-fast-input-tokens-*`, `anthropic-fast-output-tokens-*` (limit/remaining/reset); 429 with `retry-after` | https://platform.claude.com/docs/en/build-with-claude/fast-mode |
| Managed Agents (beta header `managed-agents-2026-04-01`) | Session object `usage` (cumulative over all model calls): `input_tokens` (uncached), `output_tokens`, `cache_read_input_tokens`, `cache_creation{ephemeral_5m,ephemeral_1h}`, `list_cost{amount,currency}`, `active_seconds`, `server_tool_use`. Same snapshot arrives as a `session.usage` event on `GET /v1/sessions/{id}/events/stream?beta=true` (`accept: text/event-stream`), emitted just before idle and when a budget pause occurs. Snapshot, not delta. Event envelope **UNCONFIRMED**. Per-thread usage has `list_cost`/`active_seconds`; thread token fields **UNCONFIRMED**. Not available on Bedrock/Vertex. | https://platform.claude.com/docs/en/managed-agents/session-observability, .../events-and-streaming |
| WebSocket | no WebSocket inference transport is documented (**UNCONFIRMED**; AWS `InvokeModelWithBidirectionalStream` exists but is documented for other model families) | - |

## 3. Claude on Google Cloud / Vertex AI

Sources: https://platform.claude.com/docs/en/build-with-claude/claude-models-on-google-cloud/google-managed and .../anthropic-managed

Two offerings:

1. **Google managed** (Vertex-style). Unary `POST https://{host}/v1/projects/{P}/locations/{L}/publishers/anthropic/models/{MODEL}:rawPredict`; streaming `...:streamRawPredict`. Hosts: `aiplatform.googleapis.com` (L=`global`), `aiplatform.us.rep.googleapis.com` / `aiplatform.eu.rep.googleapis.com` (L=`us`/`eu`), `{region}-aiplatform.googleapis.com`. Auth `Authorization: Bearer <google token>`. **Model id is in the URL; omit `model` from the body.** Body must carry `"anthropic_version"`: `vertex-2023-10-16` or `2026-10-21` (body, not header). Model ids: `claude-opus-5-5`; older ones use `@` date (`claude-haiku-4-5@20251001`). Docs say a request is "nearly identical" to Messages; usage shape documented only by reference to Messages, so response body and SSE framing = Messages **UNCONFIRMED in Google's own docs** (not fetched). Not supported there: Batches, Files, code execution, web fetch, MCP connector, Managed Agents. Fast mode is supported. Rate limits/quotas managed by Google (https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/partner-models/claude/quotas, not fetched); whether `anthropic-ratelimit-*` headers are present: **UNCONFIRMED**. Fixtures: `vertex-raw.json`, `vertex-raw.sse`, `vertex-raw-interrupted.sse`.
2. **Anthropic managed**: `https://claude.googleapis.com/v1/projects/{P}/locations/global/workspaces/{W}/invoke/v1/messages`; this is the Claude API (model in body, `anthropic-version` header, standard SSE, `request-id` header, rate limits per the Claude API page). Usage identical to sections 1-2; no separate fixture.

## 4. Claude on Amazon Bedrock

Sources: https://platform.claude.com/docs/en/build-with-claude/claude-on-amazon-bedrock-legacy, https://platform.claude.com/docs/en/build-with-claude/claude-in-amazon-bedrock, AWS API reference (`API_runtime_InvokeModel`, `..._InvokeModelWithResponseStream`, `..._Converse`, `..._ConverseStream`, `..._TokenUsage` under https://docs.aws.amazon.com/bedrock/latest/APIReference/), https://smithy.io/2.0/aws/amazon-eventstream.html

Three generations of access: (a) InvokeModel/Converse on `bedrock-runtime` (legacy page; newer models are also reachable via InvokeModel per that page); (b) the newer Messages endpoint on `bedrock-mantle`.

| Transport | Path / model id | Usage location | Terminal | Fixture |
|---|---|---|---|---|
| InvokeModel | `POST /model/{modelId}/invoke`; modelId in PATH (base id, inference-profile id with `global.`/`us.`/`eu.`... prefix, or ARN); body `anthropic_version: "bedrock-2023-05-31"` | Body `usage` in Messages shape (**UNCONFIRMED** by AWS pages; Anthropic docs only show `response_body.get("content")`). Response headers per AWS API ref: `Content-Type`, `X-Amzn-Bedrock-PerformanceConfig-Latency`, `X-Amzn-Bedrock-Service-Tier`; NO token-count headers are documented | HTTP response | `bedrock-invoke.json` |
| InvokeModelWithResponseStream | `POST /model/{modelId}/invoke-with-response-stream`; response `Content-Type: application/vnd.amazon.eventstream`, true type in `X-Amzn-Bedrock-Content-Type`; request header `X-Amzn-Bedrock-Accept` | Each `chunk` frame payload `{"bytes": <base64 of JSON>}`; decoded JSON is the model-native event (Anthropic SSE event objects: `message_start`, `content_block_*`, `message_delta`, `message_stop`; these have `type` inside JSON but no SSE `event:` line). Anthropic docs: this API "does not stream plain server-sent events". `amazon-bedrock-invocationMetrics` in last chunk: **UNCONFIRMED** | `message_stop` chunk; errors are exception frames: `internalServerException` 500, `modelStreamErrorException` 424 (fields `originalMessage`, `originalStatusCode`), `modelTimeoutException` 408, `serviceUnavailableException` 503, `throttlingException` 429, `validationException` 400 | `bedrock-invoke-stream.jsonl`, `-interrupted.jsonl` |
| Converse | `POST /model/{modelId}/converse` | Top-level `usage` (TokenUsage): `inputTokens`, `outputTokens`, `totalTokens` (required ints); optional `cacheReadInputTokens`, `cacheWriteInputTokens`, `cacheDetails[{inputTokens, ttl}]` ("breakdown of cache writes by TTL. Empty if no cache creation occurred. Sorted by TTL duration (1h before 5m)"). `metrics.latencyMs`, `stopReason` (`end_turn`, `tool_use`, `max_tokens`, `stop_sequence`, `guardrail_intervened`, `content_filtered`, `malformed_model_output`, `malformed_tool_use`, `model_context_window_exceeded`). No reasoning-token field in TokenUsage | HTTP response | `bedrock-converse.json` |
| ConverseStream | `POST /model/{modelId}/converse-stream` | Event-stream union members: `messageStart{role}`, `contentBlockStart`, `contentBlockDelta`, `contentBlockStop`, `messageStop{stopReason, additionalModelResponseFields}`, `metadata{usage, metrics{latencyMs}, performanceConfig, serviceTier, trace}`. Usage only in `metadata`. Order of `metadata` relative to `messageStop` **UNCONFIRMED** | `metadata` event (treat `messageStop` as not-final for usage); exceptions as above | `bedrock-converse-stream.jsonl`, `-interrupted.jsonl` |
| Messages on `bedrock-mantle` | `POST https://bedrock-mantle.{region}.api.aws/anthropic/v1/messages`; model id with `anthropic.` prefix in BODY; SigV4 service `bedrock-mantle`; `anthropic-version` header | Same as sections 1-2 (standard SSE, "same request body shape as the Claude API") | `message_stop` | `bedrock-mantle.sse` |

Open usage-semantics points for Converse on Claude: whether `inputTokens` excludes cache tokens and whether `totalTokens` includes them are **UNCONFIRMED** (AWS: totalTokens is "the total of input tokens and tokens generated by the model"). `ttl` strings in `cacheDetails` are **UNCONFIRMED** literals. Not supported on Bedrock: Batches (use AWS batch), Files, server tools, MCP connector, Managed Agents.

### AWS event-stream framing (for parsers)

Source: https://smithy.io/2.0/aws/amazon-eventstream.html (AWS-published spec; all ints big-endian)

```
[ total_len u32 ][ headers_len u32 ][ prelude_crc u32 ]   12-byte prelude; prelude_crc = CRC over the first 8 bytes
[ headers ... ][ payload ... ][ message_crc u32 ]          message_crc = CRC over every preceding byte
```
- Header: `name_len u8` (1-255) + UTF-8 name + `type u8` + value. Types: 0 true, 1 false, 2 byte, 3 short, 4 int, 5 long, 6 byte-array (u16 len), 7 string (u16 len), 8 timestamp (ms since epoch), 9 UUID (16 bytes). Names unique per message.
- Required headers: events `:message-type`=`event` + `:event-type`; modeled errors `:message-type`=`exception` + `:exception-type`; unmodeled errors `:message-type`=`error` + `:error-code` + `:error-message`. `:content-type` optional.
- Limits: payload <= 24 MB (25,165,824), headers <= 128 kB (131,072); services enforce, clients must not. Both CRCs must be validated; invalid CRC => terminate the stream.
- Payload length = total_len - 12 - headers_len - 4. CRC algorithm is not named in the fetched text (**UNCONFIRMED**; commonly CRC-32).
- Bedrock specifics: InvokeModel stream payloads are `{"bytes": base64}` wrappers (decode before JSON); Converse stream payloads are the member JSON directly. A connection drop yields a short read, not a frame: treat any stream lacking its final frame (`message_stop` / `metadata`) as interrupted.

## 5. Rate-limit metadata

Sources: https://platform.claude.com/docs/en/api/rate-limits, https://platform.claude.com/docs/en/api/errors, https://platform.claude.com/docs/en/build-with-claude/fast-mode

Response headers (Claude API; also on Anthropic-managed Google Cloud, presumably; Bedrock/Vertex **UNCONFIRMED**):

| Header | Notes |
|---|---|
| `anthropic-ratelimit-requests-limit` / `-remaining` / `-reset` | RPM; reset is RFC 3339 |
| `anthropic-ratelimit-tokens-limit` / `-remaining` / `-reset` | most restrictive token limit in effect; if no workspace limit, total = input + output; remaining rounded to nearest thousand |
| `anthropic-ratelimit-input-tokens-limit` / `-remaining` / `-reset` | ITPM (uncached input only) |
| `anthropic-ratelimit-output-tokens-limit` / `-remaining` / `-reset` | OTPM |
| `anthropic-priority-input-tokens-*`, `anthropic-priority-output-tokens-*` | Priority Tier only |
| `anthropic-fast-input-tokens-*`, `anthropic-fast-output-tokens-*` | fast mode |
| `retry-after` | seconds; "Not every 429 carries it" and the spend-cap 429 never does; honored by SDK retries (2 by default) |
| `request-id` | also in error body `request_id` |
| `anthropic-workspace-id`, `anthropic-organization-id` | which workspace/org the request counted against |

Token-bucket (continuously replenished), so `-reset` is when the bucket is full again, not a window boundary. Acceleration limits can also produce 429s.

Error body (all HTTP errors): `{"type":"error","error":{"type":"<t>","message":"..."},"request_id":"req_..."}`.
- 429 `rate_limit_error`: rate limit, acceleration limit, spend cap, or Claude Code workspace limit. Spend cap adds `error.details.error_code = "enforced_spend_limit_reached"` and has no `retry-after`.
- 529 `overloaded_error`: `{"type":"overloaded_error","message":"Overloaded"}`; mid-stream the same object arrives as an SSE `error` event.
- Other: 400 `invalid_request_error` (also user-set spend limit), 401 `authentication_error`, 402 `billing_error`, 403 `permission_error`, 404 `not_found_error`, 409 `conflict_error`, 413 `request_too_large`, 500 `api_error`, 504 `timeout_error`.
- In batches an `errored` result embeds the same ErrorResponse (types incl. `rate_limit_error`, `overloaded_error`, `timeout_error`).

Bedrock: errors are AWS exceptions (`ThrottlingException` 429, `ServiceUnavailableException` 503, `ModelNotReadyException` 429, `ModelTimeoutException` 408, `ServiceQuotaExceededException` 400, ...). Documented quota statement: default 2M input TPM on the mantle endpoint, request up to 5M input TPM / 500k output TPM; RPM enforced by AWS. Rate-limit response headers on Bedrock: none documented by the provider. Vertex: Google quotas page not fetched; headers **UNCONFIRMED**.
Fixture: `ratelimit.json`.

## 6. UNCONFIRMED / open questions

1. Whether the streaming-thinking example's missing `usage` (start and delta) is real API behavior or docs elision.
2. `usage.iterations[]` semantics beyond the single-iteration pre-warm example (is top-level the sum? may multi-iteration responses omit top-level?).
3. Google-managed Vertex: exact `rawPredict` response body, `streamRawPredict` framing, error shapes, rate-limit headers (only Anthropic's description read, Google's pages not).
4. Bedrock InvokeModel(+stream) native response/chunk shape for current models, existence of `amazon-bedrock-invocationMetrics` and any token-count headers (AWS API ref shows none).
5. Converse on Claude: is `inputTokens` exclusive of cache; does `totalTokens` include cache; literal `ttl` values; whether text blocks emit `contentBlockStart`; whether `metadata` strictly follows `messageStop`; reasoning-token reporting (none in `TokenUsage`); `reasoningContent` delta shape (not fetched).
6. CRC algorithm name in the event-stream spec (fetched summary omitted it).
7. MCP connector and Files API: any dedicated usage counters (pages only grepped / not read).
8. Managed Agents: `session.usage` event envelope, per-thread token fields, event deltas transport, behavior when a stream is cut (no interrupted fixture for this reason).
9. Existence of any documented WebSocket inference transport for Claude (none found).
10. `server_tool_use` full key set (schema lists web_search/web_fetch; code execution doc shows `code_execution_requests`; tool-search/bash keys unknown).
11. Whether `message_delta` can emit multiple times with differing cumulative values (docs say "one or more"); the fixtures assume last-wins.
12. Docs model/version names in examples (e.g. `anthropic-version: 2026-10-21`) reflect the documentation retrieved 2026-11-09 and may move.

## Fixture notes (Message Batches and Managed Agents)

- `batch-results.jsonl` and `batch-results-partial.jsonl` are aggregated by the Message Batches results relay (`GET /v1/messages/batches/{id}/results`). A cache counter absent on one succeeded line leaves its sum unknown (the partial fixture's expectation already says so; the full fixture's cache_write sum is therefore unknown, not 2000).
- Managed Agents: https://platform.claude.com/docs/en/managed-agents/session-observability (retrieved 2026-11-10) documents the session `usage` snapshot fields and that `session.usage` carries "the same cumulative snapshot", emitted before idle and on a budget pause. It shows no event envelope, so Managed Agents usage is unsupported.

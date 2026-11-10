# Azure OpenAI: token usage formats

Audit reference for the Azure OpenAI usage observers. Source: Microsoft's
documentation, retrieved 2026-11-10. Details the documentation does not state are
marked **UNCONFIRMED**. Fixtures: `pkg/modelapi/testdata/usage-formats/azure-openai/`
(`expected.json` has the values); the generic OpenAI-compatible fixtures are in
`.../openai-compatible/` (section 5).

Sources:

- Chat Completions v1 reference: https://learn.microsoft.com/en-us/rest/api/microsoft-foundry/azureopenai/chat
  (reached from https://learn.microsoft.com/en-us/azure/ai-foundry/openai/latest)
- Responses v1 reference: https://learn.microsoft.com/en-us/rest/api/microsoft-foundry/azureopenai/responses
- Prompt caching: https://learn.microsoft.com/en-us/azure/ai-foundry/openai/how-to/prompt-caching
  (canonical https://learn.microsoft.com/en-us/azure/foundry/openai/how-to/prompt-caching)

## 1. Endpoints

| Surface | Path | Operation |
| --- | --- | --- |
| v1 Chat Completions | `POST {endpoint}/openai/v1/chat/completions` (`api-version` optional: `v1` default, or `preview`) | `azure.v1.chat-completions` |
| v1 Responses | `POST {endpoint}/openai/v1/responses` | `azure.v1.responses` |
| Deployment-scoped Chat | `POST {endpoint}/openai/deployments/{deployment}/chat/completions?api-version=...` | `azure.deployments.chat-completions` |

The model field carries the deployment name ("set `model` to your Azure model
deployment name").

## 2. Chat Completions usage (`OpenAI.CompletionUsage`)

| Field | Required | Meaning |
| --- | --- | --- |
| `prompt_tokens` | Yes | "Number of tokens in the prompt." |
| `completion_tokens` | Yes | "Number of tokens in the generated completion." |
| `total_tokens` | Yes | "Total number of tokens used in the request (prompt + completion)." |
| `prompt_tokens_details.cached_tokens`, `.audio_tokens` | No | cache reads; audio input |
| `prompt_tokens_details.cache_write_tokens` | (prompt-caching page) | cache writes on GPT-5.6+ Standard deployments |
| `completion_tokens_details.reasoning_tokens`, `.audio_tokens`, `.accepted_prediction_tokens`, `.rejected_prediction_tokens` | No | subsets of completion |

The details objects are optional, so a response without `prompt_tokens_details`
(the reference's own example: `{"completion_tokens":557,"prompt_tokens":33,"total_tokens":590}`)
has an unknown cache split: the strict `openai-text` profile fails closed on it
(`deployments-chat.json`). The prompt-caching page's example
(`prompt_tokens` 1566, `cached_tokens` 1408, `cache_write_tokens` 0,
`completion_tokens` 1518, `reasoning_tokens` 576, total 3084, `audio_tokens`
null) shows the OpenAI composition: total = prompt + completion, reasoning
inside completion, cache reads and writes inside prompt (`chat.json`).

Cache billing (prompt-caching page): "Models before the GPT-5.6 family don't
charge extra to write to the cache"; on GPT-5.6 and later "cache writes can
incur charges", reported in `cache_write_tokens`. An absent
`cache_write_tokens` therefore settles as 0 (written tokens stay in ordinary
input, which is how those models bill them). `prompt_cache_key` and
`prompt_cache_options` (`mode` `implicit`/`explicit`, `ttl` `30m` or `12h` on
supported Responses deployments) are accepted by the strict profile only when
the binding meters cache writes.

### Streaming

`stream_options.include_usage`: "an additional chunk will be streamed before
the `data: [DONE]` message. The `usage` field on this chunk shows the token
usage statistics for the entire request, and the `choices` field will always be
an empty array. All other chunks will also include a `usage` field, but with a
null value. **NOTE:** If the stream is interrupted, you may not receive the
final usage chunk" (`chat.sse`, `chat.interrupted.sse`). Whether Azure adds a
`prompt_filter_results` chunk before the first delta on the v1 API is
**UNCONFIRMED** in the documentation; the observer ignores chunks without usage.

## 3. Responses usage (`OpenAI.ResponseUsage`)

| Field | Required |
| --- | --- |
| `input_tokens` | Yes |
| `input_tokens_details.cached_tokens` | Yes |
| `output_tokens` | Yes |
| `output_tokens_details.reasoning_tokens` | Yes |
| `total_tokens` | Yes |

Usage is on the response object and, streaming, on `response.completed`
(`OpenAI.ResponseCompletedEvent` carries the `OpenAI.Response`). The v1 schema
lists no cache-write field for Responses; the prompt-caching page states
GPT-5.6+ Standard deployments report `cache_write_tokens` (location for
Responses **UNCONFIRMED**; the observer reads `input_tokens_details.cache_write_tokens`
like OpenAI). Fixtures: `responses.json`, `responses.sse`.

## 4. Not covered

Rate-limit headers, Realtime on Azure, Azure "On Your Data" (`data_sources`;
a Microsoft Q&A thread reports `stream_options` is rejected there,
**UNCONFIRMED** in official docs), batch, and embeddings are outside this page.

## 5. Generic OpenAI-compatible servers

There is no provider contract. Fixtures derived from `openai/chat.*` with the
variations real servers show: usage without `prompt_tokens_details`
(`chat.json`, cache split unknown, strict fails closed), usage on the
`finish_reason` chunk instead of a separate chunk (`chat.final-usage.sse`),
no `data: [DONE]` (`chat.no-done.sse`, never settled) and no usage at all
(`chat.nousage.sse`). A token profile applies only when the operator declares
`provider.usage_semantics: "openai"` on the surface; without it the quota host
refuses the token profile (`QUOTA_PROFILE_UNSUPPORTED`) and the operation is
request-count metered.

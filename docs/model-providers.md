# Model providers

LLMPlex can route native model requests to OpenAI, Anthropic, Azure OpenAI, Amazon
Bedrock and OpenAI-compatible endpoints, in addition to the Google Cloud surfaces it
already served. A provider surface is a saved model surface with a `provider` block. The
block pins the provider's native operations, credential scheme and stream-usage policy.
Callers reach the provider through AIPlex's native route, or through a client facade
that accepts the provider's own SDK paths. Requests are checked against the operation
catalog, so an operation that is not in the catalog fails closed.

This page lists the operations that are implemented, how each one streams and how each one
is metered. Wire formats and usage fields are in the reference pages linked below. The
surface model is in [model surfaces](model-surfaces.md), and the metering contracts are in
[model metering profiles](model-metering-profiles.md).

## Operations

Metering is one of two kinds. **Request count** counts managed exchanges. A **token
profile** additionally observes provider usage with an exact, tested mapping for that
operation's native format. A profile is assigned per operation in `pkg/modelapi/coverage.go`.
An operation without a profile is metered by request count only, and its tokens are
unknown to the ledger.

| Provider (`provider.id`) | Operation ID | Native method and path | Client protocol | Streaming transport | Metering |
| --- | --- | --- | --- | --- | --- |
| `openai` | `openai.v1.chat-completions` | `POST /v1/chat/completions` | `openai-chat` | SSE | Request count, `openai-text` token profile |
| `openai` | `openai.v1.responses` | `POST /v1/responses` | `openai-responses` | HTTP. SSE when the body sets `stream: true`. | Request count, `openai-text` token profile |
| `azure-openai` | `azure.v1.chat-completions` | `POST /openai/v1/chat/completions` | `openai-chat` | SSE | Request count, `openai-text` token profile |
| `azure-openai` | `azure.v1.responses` | `POST /openai/v1/responses` | `openai-responses` | HTTP. SSE when the body sets `stream: true`. | Request count, `openai-text` token profile |
| `azure-openai` | `azure.deployments.chat-completions` | `POST /openai/deployments/{model}/chat/completions` | `openai-chat` | SSE | Request count, `openai-text` token profile |
| `anthropic` | `anthropic.messages` | `POST /v1/messages` | `anthropic-messages` | SSE | Request count, `anthropic-text` token profile |
| `bedrock` | `bedrock.converse` | `POST /model/{model}/converse` | `bedrock-converse` | HTTP | Request count only |
| `bedrock` | `bedrock.converse-stream` | `POST /model/{model}/converse-stream` | `bedrock-converse` | AWS event stream | Request count only |
| `bedrock` | `bedrock.invoke` | `POST /model/{model}/invoke` | `bedrock-invoke` | HTTP | Request count only. The body is model-specific, so usage is unknown. |
| `bedrock` | `bedrock.invoke-stream` | `POST /model/{model}/invoke-with-response-stream` | `bedrock-invoke` | AWS event stream | Request count only. Usage is unknown. |
| `openai-compatible` | `compat.chat-completions` | `POST {base}/chat/completions`, `base` defaults to `/v1` | `openai-chat` | SSE | Request count only |

Google Cloud surfaces (no `provider` block) use the same metering model:

| Operation ID | Native method and path | Streaming transport | Metering |
| --- | --- | --- | --- |
| `models.generateContent` | `POST …:generateContent` | HTTP | Request count, `gemini-text` token profile |
| `models.streamGenerateContent` | `POST …:streamGenerateContent` | SSE | Request count, `gemini-text` token profile |
| `interaction.create` | `POST …/interactions` | HTTP (catalog default) | Request count, `gemini-interaction-text` token profile |
| `models.rawPredict` | `POST …:rawPredict` (Vertex Anthropic) | HTTP | Request count, `anthropic-text` token profile |
| `models.streamRawPredict` | `POST …:streamRawPredict` (Vertex Anthropic) | SSE | Request count, `anthropic-text` token profile |

Operations that the catalog lists without a token profile, such as `endpoints.rawPredict`,
`embedContent` and `countTokens`, are metered by request count only. Asynchronous
operations (batch, tuning, cache creation and long-running prediction) are request-count
only, and their later work is not metered. The full list is in `pkg/modelapi/coverage.go`.

Token profiles accept only complete, supported text usage. For output-inclusive providers,
an observation that has cache writes or server-tool calls fails closed for token metering.
For the OpenAI and Anthropic token profiles, reasoning tokens are diagnostic and already
included in `output_tokens`, so they are not added again.

## Reference pages

Wire-format and usage-field details are in these pages. They are not repeated here.

- [OpenAI](reference/usage-formats/openai.md)
- [Anthropic](reference/usage-formats/anthropic.md)
- [Google](reference/usage-formats/google.md)

No reference page exists for Bedrock. Bedrock operations have no token profile in
`pkg/modelapi/coverage.go`, so they are metered by request count only.

## Surface configuration

A provider surface is a `model-surface` resource with a `provider` block. Every field
is operator configuration. No caller value overrides it.

| Field | Used by | Meaning |
| --- | --- | --- |
| `id` | All | Provider ID: `openai`, `anthropic`, `azure-openai`, `bedrock` or `openai-compatible`. |
| `base_path` | `openai-compatible` | Prefix for operation paths. Default `/v1`. |
| `api_version` | `azure-openai` | Pinned `api-version` query value for deployment-scoped operations. |
| `region` | `bedrock` | Signing region. It must match the origin. |
| `organization`, `project` | `openai` | Pinned `OpenAI-Organization` and `OpenAI-Project` headers. |
| `anthropic_version` | `anthropic` | Pinned `anthropic-version` header. |
| `anthropic_betas` | `anthropic` | Allowlist of caller `anthropic-beta` values. |
| `stream_usage` | `openai`, `azure-openai`, `openai-compatible` | Stream-usage policy for Chat Completions. See below. |
| `limit_header_passthrough` | All | Forwards the provider's rate-limit headers to the caller. Off by default. |

Credentials are either an `auth` block that references a tenant secret, or an
`identity_ref` to an operator identity profile. Google surfaces accept `identity_ref`
only.

| Provider | Accepted credential |
| --- | --- |
| `openai` | `auth.type: bearer`, or identity type `bearer-env` |
| `anthropic` | `auth.type: api_key` in header `x-api-key`, with no prefix |
| `azure-openai` | `auth.type: api_key` in header `api-key`, with no prefix, or identity type `azure-default` (Entra) |
| `bedrock` | `auth.type: bearer` (Bedrock API key), or identity type `aws-default` (SigV4) |
| `openai-compatible` | `auth.type: bearer` or `none`, or identity type `bearer-env` |

### Stream usage

Chat Completions streams report usage only when the request asks for it. The
`stream_usage` policy controls that:

| Value | Effect |
| --- | --- |
| `require` | A streaming request without `stream_options.include_usage: true` is rejected. Default for `openai` and `azure-openai`. |
| `inject` | AIPlex sets `stream_options.include_usage` to `true`. This is the only body change AIPlex makes to a request. |
| `off` | The request is forwarded unchanged. Streaming usage may stay unknown. Default for `openai-compatible`. |

## Routes

| Route | Purpose |
| --- | --- |
| `GET /api/v1/llm/operations` | The native operation catalog. |
| `GET /api/v1/llm/surfaces/{id}` | One surface, including its `provider` block. Surface writes use the shared resource routes. |
| `/api/v1/llm/native/{id}/*` | Native provider requests through the surface. The path after `{id}` is the provider's own path. |
| `/api/v1/llm/gateway/{id}/*` | Client facade for unmodified provider SDKs. See below. |
| `GET /api/v1/llm/surfaces/{id}/capacity` | The last provider limit observation for this surface on this replica. Admin only. |

Client facade base URLs, relative to `/api/v1/llm/gateway/{id}`:

| Provider | SDK base setting | Paths the facade maps |
| --- | --- | --- |
| `openai` | `base_url` `…/gateway/{id}/v1` | `/v1/chat/completions`, `/v1/responses` |
| `anthropic` | `base_url` `…/gateway/{id}` | `/v1/messages` |
| `azure-openai` (v1) | `base_url` `…/gateway/{id}/openai/v1` | `/openai/v1/chat/completions`, `/openai/v1/responses` |
| `azure-openai` (deployments) | `azure_endpoint` `…/gateway/{id}` | `/openai/deployments/{deployment}/chat/completions` |
| `bedrock` | `endpoint_url` `…/gateway/{id}` | `/model/{model}/converse`, `converse-stream`, `invoke`, `invoke-with-response-stream` |
| `openai-compatible` | `base_url` `…/gateway/{id}{base}` | `{base}/chat/completions` |

The facade rewrites only the path. The body is not changed except as described under stream
usage. Operations that are not in the native allowlist fail closed.

### Capacity

`GET /api/v1/llm/surfaces/{id}/capacity` returns the last normalized limit observation for
a surface, when one exists, in this shape:

```json
{"surface_id": "…", "observed": true, "scope": "replica-local last observation; advisory", "limits": {…}}
```

The observation records the classification (`rate_limited`, `overloaded` or
`quota_exceeded`), retry-after, and any requests, tokens, input-tokens or output-tokens
windows the provider reported. The store is in memory, bounded, and kept per process.

## Limits

- **Nothing enforces on provider limit signals yet.** The capacity store is read by the
  capacity route only. No admission, scheduling or quota decision uses it. The narrow
  `CapacitySignal` interface is available for a scheduler to consume later.
- Limit observations describe the shared upstream credential at one instant. Restarts and
  other replicas do not share them.
- `limit_header_passthrough` forwards the upstream credential's limit headers to callers,
  which reveals shared-account capacity. It is off by default.
- Only the operations in the table are installed. Other provider operations fail closed.
- Request-count metering counts managed exchanges. It does not measure asynchronous job
  work, cached-content retention or internal tool or agent work.
- Token metering is limited to the operations with a token profile. Bedrock operations
  have none, and `bedrock.invoke` bodies are model-specific, so their usage is unknown.
- Anthropic token metering fails closed when the observation includes cache writes or
  server-tool calls.
- Provider credentials are references. Values are resolved at dispatch and are not
  returned by any route.

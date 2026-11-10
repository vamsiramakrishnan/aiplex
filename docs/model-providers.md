# Model providers

LLMPlex can route native model requests to OpenAI, Anthropic, Azure OpenAI, Amazon
Bedrock and OpenAI-compatible endpoints, in addition to the Google Cloud surfaces it
already served. A provider surface is a saved model surface with a `provider` block. The
block pins the provider's native operations, credential scheme and stream-usage policy.
Callers reach the provider through AIPlex's native route, or through a client facade
that accepts the provider's own SDK paths. Requests are checked against the operation
catalog, so an operation that is not in the catalog fails closed.

The authority on what is metered exactly, advisory, or not observed at all is the
generated [usage-format coverage matrix](reference/usage-formats/coverage.md). This page
lists the operations and their metering class. Where the two differ, the matrix wins.
Wire formats and usage fields are in the reference pages linked below. The surface model
is in [model surfaces](model-surfaces.md). The metering contracts are in
[model metering profiles](model-metering-profiles.md).

## Operations

Metering has two parts. **Quota metering** is either request count (one admitted exchange
per request) or a token profile (quota is charged on token exposure, with exact settlement
where the matrix says so). **Usage observation** is what the ledger records. It is either
`exact`, `advisory` (ledger telemetry only, never settled), or none. A profile is assigned
per operation in `pkg/modelapi/coverage.go`. An operation without a token profile is
request-count metered for quota.

| Provider (`provider.id`) | Operation ID | Native method and path | Client protocol | Streaming transport | Quota metering | Usage observation |
| --- | --- | --- | --- | --- | --- | --- |
| `openai` | `openai.v1.chat-completions` | `POST /v1/chat/completions` | `openai-chat` | SSE | Request count, `openai-text` token profile | exact |
| `openai` | `openai.v1.responses` | `POST /v1/responses` | `openai-responses` | HTTP. SSE when the body sets `stream: true`. | Request count, `openai-text` token profile | exact |
| `azure-openai` | `azure.v1.chat-completions` | `POST /openai/v1/chat/completions` | `openai-chat` | SSE | Request count, `openai-text` token profile | not listed in the matrix |
| `azure-openai` | `azure.v1.responses` | `POST /openai/v1/responses` | `openai-responses` | HTTP. SSE when the body sets `stream: true`. | Request count, `openai-text` token profile | not listed in the matrix |
| `azure-openai` | `azure.deployments.chat-completions` | `POST /openai/deployments/{model}/chat/completions` | `openai-chat` | SSE | Request count, `openai-text` token profile | not listed in the matrix |
| `anthropic` | `anthropic.messages` | `POST /v1/messages` | `anthropic-messages` | SSE | Request count, `anthropic-text` token profile | exact |
| `bedrock` | `bedrock.converse` | `POST /model/{model}/converse` | `bedrock-converse` | HTTP | Request count only | advisory |
| `bedrock` | `bedrock.converse-stream` | `POST /model/{model}/converse-stream` | `bedrock-converse` | AWS event stream | Request count only | advisory |
| `bedrock` | `bedrock.invoke` | `POST /model/{model}/invoke` | `bedrock-invoke` | HTTP | Request count only | advisory when the body is Messages-shaped; otherwise not observed |
| `bedrock` | `bedrock.invoke-stream` | `POST /model/{model}/invoke-with-response-stream` | `bedrock-invoke` | AWS event stream | Request count only | advisory when the chunks are Messages-shaped; otherwise not observed |
| `openai-compatible` | `compat.chat-completions` | `POST {base}/chat/completions`, `base` defaults to `/v1` | `openai-chat` | SSE | Request count only | not in the matrix; the operation has no token profile |

The matrix has no rows for Azure OpenAI or OpenAI-compatible surfaces, so their usage
observation is not stated here. The matrix marks the Bedrock rows as advisory and notes
that AWS has not confirmed the Anthropic-shaped InvokeModel body or the decoded chunk
shape. Treat those counts as telemetry.

Google Cloud surfaces (no `provider` block) use the same two-part model:

| Operation ID | Native method and path | Streaming transport | Quota metering | Usage observation |
| --- | --- | --- | --- | --- |
| `models.generateContent` | `POST …:generateContent` | HTTP | Request count, `gemini-text` token profile | exact (Vertex) |
| `models.streamGenerateContent` | `POST …:streamGenerateContent` | SSE | Request count, `gemini-text` token profile | exact with `alt=sse` (Vertex) |
| `interaction.create` | `POST …/interactions` | HTTP (catalog default) | Request count, `gemini-interaction-text` token profile | exact (Vertex) |
| `models.rawPredict` | `POST …:rawPredict` (Vertex Anthropic) | HTTP | Request count, `anthropic-text` token profile | exact (Vertex) |
| `models.streamRawPredict` | `POST …:streamRawPredict` (Vertex Anthropic) | SSE | Request count, `anthropic-text` token profile | exact (Vertex) |

Operations that the catalog lists without a token profile, such as `endpoints.rawPredict`,
`embedContent` and `countTokens`, are request-count metered only. Asynchronous operations
(batch, tuning, cache creation and long-running prediction) are request-count only, and
their later work is not metered. The full list is in `pkg/modelapi/coverage.go`. The
matrix lists each row's status and the reason for any gap.

Token profiles accept only complete, supported text usage. For output-inclusive providers,
an observation that has cache writes or server-tool calls fails closed for token metering.
For the OpenAI and Anthropic token profiles, reasoning tokens are diagnostic and already
included in `output_tokens`, so they are not added again.

## WebSocket sessions

Three session operations are implemented as WebSocket relays:

| Operation ID | Native path | Provider | Client protocol | Notes |
| --- | --- | --- | --- | --- |
| `openai.v1.realtime` | `GET /v1/realtime?model=…` | `openai` | `openai-realtime` | The model is chosen at connect time by the `model` query parameter. |
| `openai.v1.responses-websocket` | `GET /v1/responses` | `openai` | `openai-responses` | The model is chosen per `response.create` event. The connect request must not carry `model`. |
| `live.connect` | `GET /ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent` | Google Cloud (Vertex Live) | `google-genai` surface | The model is named in the `setup` message. |

The coverage matrix lists each session as `advisory`. How a session is metered:

- **Admission.** One request-count lease per session, prepared before the upstream dial.
  Only the request-count profile is accepted. A token-profile lease is aborted and the
  session is refused with `UNSUPPORTED_EXECUTION_TRANSPORT`.
- **Permit.** One concurrency permit for the session's lifetime.
- **Quota.** The session counts as one request against request budgets and rates. Tokens
  are not bounded by quota on these paths. Only the provider's own session limits and the
  duration bound limit them.
- **Duration.** The session is bounded by `MaxStreamDuration`. It is a Go option on
  `modelapi.Options` with no environment variable or surface field. Its default is 30
  minutes, and the accepted range is 1 second to 24 hours. Reaching the bound closes both
  sockets and labels the usage as a lower bound. The bound is applied as a context deadline
  to every gateway request, not only to sessions.
- **Usage.** Server usage events are forwarded to the ledger as advisory observations while
  the session runs. Live reports cumulative snapshots. Realtime and Responses report
  per-response usage, which the adapter sums over distinct response IDs.
  Input-transcription usage is a separate `transcription_*` meter and is not merged into
  the session's tokens.
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

Responses WebSocket mode has one session admission. Each `response.create` is re-authorized
for its model and for ownership of any `previous_response_id`, but it is not separately
admitted. Provider conversations are refused.

The session rules are documented in more detail in
[model metering profiles](model-metering-profiles.md#websocket-sessions). The execution
coverage matrix is in [QuotaPlex execution coverage](quotaplex-execution-coverage.md).

WebSocket sessions through an access point are described in
[access points](access-points.md#websocket-sessions). Responses WebSocket mode is not routed there.

## Reference pages

Wire-format and usage-field details are in these pages. They are not repeated here.

- [Usage-format coverage matrix](reference/usage-formats/coverage.md), the authority on
  exact, advisory and unsupported observation
- [OpenAI](reference/usage-formats/openai.md)
- [Anthropic](reference/usage-formats/anthropic.md)
- [Google](reference/usage-formats/google.md)

There is no separate reference page for Bedrock. Its formats are described in the matrix
rows above.

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
| `GET /api/v1/llm/surfaces/{id}/capacity` | The last provider limit observation for this surface on this replica, the surface's `capacity_policy` with defaults applied, and its cooldown state. Admin only. |

Client facade base URLs, relative to `/api/v1/llm/gateway/{id}`:

| Provider | SDK base setting | Paths the facade maps |
| --- | --- | --- |
| `openai` | `base_url` `…/gateway/{id}/v1` | `/v1/chat/completions`, `/v1/responses` (POST), `/v1/realtime` and `/v1/responses` (GET upgrade, for the WebSocket operations) |
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

### Capacity policy

A provider surface can act on the limit signals it observes. The `capacity_policy` block on
the surface spec is off by default. A surface without it, or with an empty block, behaves
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

A rejected request is answered in the calling protocol's native rate-limit error with a
`Retry-After` header. It makes no upstream call, acquires no credential, and charges no
quota, and WebSocket sessions are rejected at admission before the upgrade. The usage ledger
records it as a denied exchange with `error_class` `capacity_cooldown` or `capacity_shed`,
which are not provider errors.

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

## Limits

- Provider limit signals are enforced only when a surface opts in with a
  `capacity_policy` (see below), and then only as an additional, advisory gate. QuotaPlex
  remains the only accounting and quota authority.
- Limit observations describe the shared upstream credential at one instant. Restarts and
  other replicas do not share them.
- `limit_header_passthrough` forwards the upstream credential's limit headers to callers,
  which reveals shared-account capacity. It is off by default.
- Only the operations in the table are installed. Other provider operations fail closed.
- Request-count metering counts managed exchanges. It does not measure asynchronous job
  work, cached-content retention or internal tool or agent work.
- Token quota applies only to operations with a token profile. Advisory usage from Bedrock
  and from WebSocket sessions is recorded but does not bound or settle quota.
- Bedrock InvokeModel usage is observed only when the body has the Messages shape. Other
  InvokeModel bodies are model-specific and are not observed. AWS has not confirmed the
  Messages shape for InvokeModel, so treat those counts as advisory.
- Anthropic token metering fails closed when the observation includes cache writes or
  server-tool calls.
- WebSocket sessions are bounded by time, not by tokens. A session can run until
  `MaxStreamDuration` even when quota has no token headroom left.
- Provider credentials are references. Values are resolved at dispatch and are not
  returned by any route.

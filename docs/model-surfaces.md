# Native model surfaces

LLMPlex uses `pkg/modelapi` to expose explicit, protocol-native model operations.
A `Surface` binds an operator-approved origin, project, location, API version,
models/agents and operation allowlist. Operations contain method/path/transport,
state behavior and a primary-source documentation URL. Callers cannot supply an
arbitrary upstream URL or override the configured project/location.

The catalog is intentionally separate from availability: selecting an operation
asserts that the deployment operator has validated that model, region and account.
It is not evidence that every Model Garden model supports every operation. A new
model can be configured without changes to the transport, but no unpublished
November 17 product availability or API method is presumed.

## Native operations

`GoogleOperations` includes:

- Publisher-model generation, streaming generation, embeddings, token counting
  and token listing, predictions, raw predictions and streaming raw predictions.
- Deployed-endpoint generation/prediction/token operations.
- Interactions create/list/get/cancel/delete, background workflows, history,
  streamed creation and reconnect through native query parameters/event IDs.
- Cached-content create/list/get/update/delete.
- Batch prediction jobs create/list/get/cancel/delete.
- Tuning jobs create/list/get/cancel.
- Long-running predictions and `fetchPredictOperation`, plus operation retrieval,
  cancellation and deletion.
- Cloud OpenAI-compatible Chat Completions; opt-in Responses create/get/delete
  contract documented for supported partner models. This is not a claim that
  Gemini or future OpenAI models support Responses.
- A functional Live WebSocket relay with initial setup/model validation,
  resumption-handle ownership, bounded message size and session duration.

Cloud Interactions is supported on `global`, `us`, and `eu`, with the documented
origins. It is not mapped to a single region. The current Cloud reference uses
`v1`; a deployment may explicitly select another documented version as a separate
profile. Gemini Developer API Files is not fabricated as a Cloud `/files` route.
Cloud Storage media references and RAG media upload have distinct contracts.

## Authorization and credentials

`NewGateway` requires a trusted request-context resolver, an authorizer, a
server-side token source and a network-policy HTTP client. Request context must
bind `ResourceID` to the surface ID. The authorizer sees the exact path/body
model or agent, requested resource, referenced history/cache/operation resources,
and Cloud Storage URIs before credentials or upstream network activity.

Bodies and native response fields remain unchanged. Duplicate JSON keys,
ambiguous security field spellings and conflicting path/body models are denied.
The gateway strips inbound credentials and untrusted billing/identity headers,
injects its configured upstream token and optional administrator-pinned
`X-Goog-User-Project`/`X-Goog-Request-Params`, refuses redirects and does not retry
mutating calls. Use the narrowest server/agent credentials the upstream supports.

`Surface.DataPrefixes` explicitly permits Cloud Storage URI prefixes used by
media, batch input/output and tuning datasets. The default permits none. Prefix
comparison respects bucket/path boundaries and rejects ambiguous traversal. The
body validator conservatively treats JSON string values beginning `gs://` as
resource references, including a user text value consisting of such a URI.

Use `endpointpolicy.Policy.StreamingClient` for HTTP/SSE. `NewLiveDialer` accepts
the same policy's dial-time network boundary and an optional verified TLS config;
it never enables ambient proxies. A Live operation cannot be enabled without a
functional WebSocket transport. Stream lifetime defaults to 30 minutes and is
configurable from one second to 24 hours; long sessions reconnect explicitly.

## Stateful isolation

Stateful surfaces require an `Ownership` adapter. Bind/check keys include tenant,
principal, surface, resource kind and upstream ID. Successful creation responses
are bound before delivery; streamed creation IDs are bound before forwarding the
creation event. Reads, updates, deletion, cancellation, prior interaction/response
IDs, cached-content references, long-running prediction operation names and Live
resumption handles require matching ownership. Cross-tenant or cross-principal
resource reuse fails before upstream credential acquisition.

Native list endpoints expose shared provider resources, so they require an
explicit `AllowShared` authorization decision in addition to ordinary operation
authorization. Deployment-wide list visibility is not implied by owning one ID.

An ownership adapter must be durable for production. The in-memory test adapter
is a fixture, not a restart-safe ownership authority. If binding a newly created
provider ID fails, the response is withheld; operators need reconciliation for
the already-created upstream resource rather than pretending the create rolled
back. Background Interactions cancellation can still incur consumed-token cost.

## Usage observations

Observers receive upstream-reported counters only. Input/output/thought/cache
counters stay separate; missing usage is unknown rather than an invented zero.
Native SSE frames are preserved exactly. A disconnected stream without terminal
usage does not manufacture a completion or refund.

`Cumulative=true` marks stateful resource/session snapshots. Repeated retrieval
of the same interaction is not additional spend. An accounting consumer must
reconcile/deduplicate cumulative snapshots by resource and event semantics rather
than summing every observation. `Observe` is telemetry; it is not a quota ledger
or reservation protocol. QuotaPlex remains a separate future accounting module.

## Primary references

- https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/models/interactions-api
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/capabilities/interactions
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/start/openai
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/partner-models/grok/responses
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/models/live-api
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.publishers.models/embedContent
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.cachedContents

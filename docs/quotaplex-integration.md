# Managed QuotaPlex execution

QuotaPlex can now participate at the Go API's managed model and tool dispatch
boundaries. It pins verified identity, the saved connection revision, provider
authority, funding and policy before acquiring a single dispatch claim. Native
payloads and streaming frames retain their protocol semantics. The module is
opt-in; default module selection and existing gateway ownership are unchanged.

Read the [core guide](quotaplex-core.md) for accounting contracts and the
[readiness register](quotaplex-prerequisites.md) for deployment gates.
The [execution coverage and regression matrix](quotaplex-execution-coverage.md)
maps actual model/tool dispatch, identities, retries, uncertainty and scheduling
to implementation contracts and test evidence.

## Enable an explicit deployment

Set both the module and its operator-owned configuration file:

```sh
export AIPLEX_MODULES=mcplex,a2aplex,llmplex,skillsplex,clientplex,networking,quotaplex
export AIPLEX_QUOTA_FILE=/etc/aiplex/quota.json
export GCP_PROJECT=example-project
export FIRESTORE_DATABASE='(default)'
```

Production requires the configured shared durable Firestore quota store. A
process-local reference store is accepted only in explicit development mode.
Missing files, unknown/duplicate configuration fields, expired evidence and
incompatible policy/binding revisions fail startup rather than disabling controls.
Mount the file through the existing read-only [Helm configuration](helm-identity-config.md).
No cloud resources, provider limits or directory memberships are provisioned by
loading this file.

The [request-count example](../examples/quota/request-count.json) defines shared
request budgets, an independent seven-day principal cap, a tool-call calendar
budget, a tenant arrival rate and a concurrency limit. Its binding names must
match saved model surfaces/tool connections and resolved billing/region facts.
It contains no token or monetary enforcement claim.

JSON `time.Duration` fields use integer **nanoseconds**: `1000000000` is one
second; `300000000000` is five minutes. `recovery_interval` instead uses a Go
duration string and must be between `1s` and `1h`; its default is `5s`.

Each tenant supplies `host.tenants[].policy`, `activate` and, for an upgrade,
`expected_digest`. First activation uses revision 1 and no previous digest.
Reusing the exact active document is safe on restart. A changed binding, maximum
membership age or provider-bound authority requires a new policy revision and
the previous active policy digest. The durable host-configuration head prevents
old replicas from silently dispatching under replaced bindings. Deploy the same
revision across replicas; stale replicas fail readiness/dispatch checks.

## Exact coverage

| Path | Enabled coverage |
| --- | --- |
| `/api/v1/llm/native/{surface}/*` | Explicit operation coverage with request-count or supported native token profiles; see [model metering profiles](model-metering-profiles.md) |
| `/api/v1/llm/gateway/{surface}/*` | Client facade only when it resolves to the same supported native operation and surface; it reuses the same admission path |
| `/api/v1/mcp/connections/{connection}/invoke` | Allowlisted stateless JSON or bounded finite SSE `tools/call`, with operator or owner-bound delegated OAuth credentials |
| Other model operations | Unsupported operations fail closed. Request-count can cover cataloged job/cache exchanges; it does not meter their internal work. Live and unbounded token profiles remain unsupported. |
| `/api/v1/a2a/connections/{connection}/invoke` | Explicit `a2aplex` request-count bindings govern the native HTTP exchange; opaque internal model/tool work and job lifetime remain outside this profile |
| Direct provider, Envoy-deployed routes, external/native client modes and skill servers | No automatic coverage; their existing paths are not rewritten into this Go API integration |

An enabled host requires a matching binding for each managed invocation. Missing
coverage is not an advisory bypass. This also means enabling QuotaPlex can make
previously available managed operations unavailable until supported bindings exist.
Registration, WIF, registry metadata and a configured network route do not prove
that every billable call crosses the protected path.

The model adapter preserves exact upstream payloads, identity headers and native
JSON/SSE framing. It does not infer compatibility from a provider label. Installed operation/profile contracts now include Gemini generation, model-backed
Interactions, OpenAI Chat/Responses and Anthropic Messages. A protocol label alone
does not install a metering contract or establish model availability.

## Request-count and tool profiles

An operator binding selects `profile: "request-count"` and maps the normalized
`requests` counter to a policy meter with unit `requests`. Tool bindings may also
map `tool_calls` to a distinct `calls` meter. The request costs one accepted call;
HTTP errors do not imply a refund. An incomplete transport retains exposure.
With explicit per-call prices, monetary caps can enforce that configured tariff;
they do not measure an LLM provider's token-based bill. The example has no prices.

Request/s buckets debit accepted calls; token-exposure/s buckets debit the full
admitted bound. Their burst must fit the supported single-request demand, and
accepted rate debits are not refunded when actual usage is smaller. Concurrency
limits outstanding HTTP/RPC work and retained uncertain exposure. These controls
provide neither stream-token pacing, provider-wide reserved TPS nor minimum
throughput guarantees.

Managed MCP execution is separate from OAuth registration. The saved connection
needs an explicit `execution` object with matching `id`/`endpoint`, an approved
operator `identity_ref` or delegated OAuth mode, an explicit `mode` and `protocol_version`, and
`allowed_tools`. The endpoint must match `resource_url` and both network and
credential-origin policies. The adapter sends one JSON-RPC request and accepts a
bounded JSON response with its matching ID. The `stateless-streamable-http` mode additionally supports bounded finite native SSE.
Batch calls, notification-only invocations, initialization and `MCP-Session-Id` are
not inferred from registration. A valid JSON-RPC error still consumes a call. See
[delegated MCP runtime authorization](mcp-runtime-authorization.md).

## Token and monetary exposure

`profile: "gemini-text"` is a stricter, version-pinned generation subset. It needs
all five disjoint mappings: `requests`, `uncached_input_tokens`, `cached_tokens`,
`output_tokens` and `thought_tokens`. Monetary budgets additionally require the
core's explicit versioned prices for the enabled billable meters. Native total
tokens and their subsets are not added together.

The body must contain one text-only user content, optional text system
instruction and supported generation/safety settings. `maxOutputTokens` must be
positive; at most one candidate is supported. Text/JSON response formats and a
bounded thinking configuration are allowed. Media, external files, function/tool
work, explicit `cachedContent`/cache operations, multi-turn content and unknown
exposure-affecting fields are refused.
Implicit provider caching can still occur and is represented by `cached_tokens`.
Streaming requires exactly `alt=sse`; partial-response and unknown query options
are refused for this profile.

`provider_limits` supplies **operator attestations of provider-enforced bounds**.
Each entry pins tenant/binding, provider, model/model version, billing account,
region, input ceiling, combined-output ceiling, source reference and an explicit
issued/expiry interval. The deployment must verify those facts against its actual
provider/version contract. A configured maximum, prompt byte count, tokenizer
estimate or caller claim is not such evidence. The built-in adapter neither
discovers nor enforces an upstream provider limit by declaring one here.

The requested output limit must fit the attested combined output ceiling. The
reservation conservatively covers each input/output subset; terminal settlement
uses the provider's normalized disjoint actual counters. Authority identity,
version, expiry and configuration digest are checked again at dispatch. Missing,
expired or mismatched evidence blocks the protected token path.
These bounds can reserve substantially more than the eventual observed usage;
they are an exposure guarantee, not an estimate of typical request cost.

The actual response must establish the configured model version and terminal
usage. Model-version drift/missing proof or an observed exposure overrun durably
quarantines the binding generation. Known evidence is retained; the system does
not price a different model using the pinned old model's rates. Repeated calls
remain blocked for that generation. A new deployment revision can establish a
new verified binding, but does not correct or release an old attempt automatically.

## Identity, funding and retries

Quota memberships derive from verified signed `iat`/`exp` and an authoritative
group array. Missing/null/malformed/overage groups are not an empty membership.
Issuer and pool determine a source-qualified namespace; snapshots bind tenant
and canonical principal. Request arrival does not create membership freshness.
Verified request-bound mTLS can shorten usable expiry to the certificate expiry;
a certificate alone supplies no directory group authority.
Service-account/native OIDC credentials that omit complete signed groups still
need a principal membership authority; configure the signed principal-directory authority described below; enabling WIF
alone does not install one. Principal and actor queries stay independent.

`host.max_membership_age` defaults to five minutes and is bounded from one second
to 24 hours. Effective membership expiry is the earlier of that age and credential
expiry. A still-valid authentication token can therefore require refreshed group
evidence for quota admission. Reusing the request does not refresh its timestamp.

Delegated agent actors require an independent `actors` snapshot or an injected
authoritative actor resolver. Each configured actor has its own canonical ID,
source-qualified groups, reference and issued/expiry interval. The configured
`groups` array must be explicit; `[]` is valid, missing/null is not. Human groups never
supply an actor's funding eligibility. Configuration attests those groups; it is
not a live synchronization with a native cloud registry or IdP directory.

Claim-time checks reread saved connection/network/credential configuration and
re-resolve current manual/role grants from the registry using the verified caller.
Role/manual-grant refresh itself does not query an IdP. With a configured signed
directory authority, quota membership is separately refreshed within its bounded
cache/revocation contract. Missing access refresh on a protected host fails closed.

`X-Aiplex-Funding-Account` requests a payer; it cannot grant eligibility or override
an operator-pinned payer. `Idempotency-Key` identifies a billable attempt within
the verified subject/operation scope. A repeated key cannot dispatch again;
changed payload, configuration or immutable identity facts conflict. A fresh
billable retry/fallback uses a new key. `X-Aiplex-Operation-Id` links logical work
without combining separate provider charges. These headers are not forwarded as
provider authority. The response exposes the opaque `X-Aiplex-Attempt-ID`.

## Acknowledgement and recovery

Dispatch, observation and finish use the shared acknowledged execution lifecycle.
An uncertain post-claim error never becomes an undispatched abort. Known native
resource IDs are pinned before they can be reused. Terminal evidence is immutable
and bound to the attempt, producer, configuration and actual model proof.

The terminal outbox acknowledges evidence only after commit. `pending` or
`processing` means settlement acknowledgement is incomplete. Exposure is released
only by the ledger settlement, which may already have committed before the worker
acknowledges it; `settled` confirms that transaction completed. Settlement can retry after restart without
re-dispatching the provider. SSE terminal frames and terminal tool responses are
not released until their evidence is durably acknowledged. Cancellation uses a
bounded durability context rather than discarding already received usage.
For SSE, earlier frames/HTTP `200` headers may already be visible when final
evidence fails. The final frame is withheld; callers must treat an incomplete
stream as failure and inspect its attempt instead of relying on HTTP status alone.

The module-owned worker scans bounded pages, drains pending evidence, recovers
provably expired **undispatched** admissions and returns unused expired loans.
It advances process-local scan cursors through per-item failures and periodically
rescans; restart begins another scan without discarding durable evidence/work state.
Workers share fenced leases/backoff so duplicate work cannot consume or refund
twice. The built-in deployment supplies no native job-status resolver: uncertain
provider work without terminal evidence remains held. Token profiles never
manufacture a final usage report from EOF, timeout or a disconnected client.

Inspect a caller-owned attempt, or an administratively visible tenant attempt:

```sh
aiplex quota attempt attempt-<opaque-id>
# Equivalent API: GET /api/v1/quota/attempts/{attempt-id}
```

The SDK exposes `GetQuotaAttempt`. Reads preserve both human and agent ownership
boundaries. [Quota administration](quotaplex-administration.md) now provides scoped inspection,
policy validation/export, audited loans and reviewed reconciliation. Corrections
require exact state/digest/model proof and settle at original prices while
preserving original evidence. There is no arbitrary repricing or quarantine-clear
endpoint. A corrected attempt does not reopen its binding generation.

## Deployment gates

Verify real provider limits/prices, identity refresh, required grants, endpoint
coverage, shared-store permissions, recovery lag, unknown-hold alerts and restore
behavior before enabling a path. The local tests exercise mechanisms and native
protocol fixtures; they do not prove live billing equivalence or deployed routing.
Rolling budgets, minimum-throughput guarantees, native capacity reservation,
preemption, remote admission RPC, direct-route interception and additional
specialized model/tool/agent adapters remain explicit work.

## Additional foundational mechanisms

[Signed membership authority](membership-authority.md) can supply complete, expanded
source-qualified principal and actor snapshots independently of JWT group claims.
Configure `membership_authority` with its pinned HTTP signing keys, allowed sources,
endpoint, host identity reference and optional bounded cache. Resolver failure does
not fall back to claims. This custom authority protocol does not install a native
Google/Entra/AWS directory synchronizer or grant new execution scopes.

`administration.allow_self_approval` is operator policy, default false. The Console
module exposes policy/inventory inspection, reviewed draft export, loans and
reconciliation. Mutations retain stable operation IDs after uncertain responses;
browser-unsafe integers require the exact CLI/API path instead of rounding.

The optional `scheduling` array binds a `SchedulerConfig` to exact resource, operation
and selected payer routes. The host derives flow identity after funding eligibility
resolution; callers cannot select protected partitions. Queue selection, capacity
credit debit and ordinary quota admission commit atomically. Dispatch still rechecks
current grants, membership and capacity evidence before claiming once.

Each entry includes `config`, `routes`, optional `poll_interval` (default 100ms) and
`expected_digest` for evidence renewal. Routes contain `resource_id`, `operation_id`,
optional `payer_id` and `flow_id`; overlapping routes fail startup. Queue `max_wait`
bounds how long managed requests wait. Cancellation aborts only definitely
undispatched holds. The scheduler inventory exposes the previous scheduler digest.
Evidence renewal and quantity-only quota revisions preserve queue history and spent
credits; changes to funding eligibility, flow mapping or capacity structure require
explicit migration. Protected admission credits are not upstream reserved capacity,
stream token pacing or a minimum decode-throughput guarantee.

[Production qualification](production-qualification.md) includes real-emulator CI,
reference benchmarks and exact local billing-evidence comparison.

## Native cloud-agent exchanges

The native Google Runtime, Bedrock/AgentCore, Foundry and Copilot invoker uses the
same acknowledged execution lifecycle under an explicit `request-count` binding.
Set plane `a2aplex`, exact connection `resource_id`, native invocation
`operation_id` and provider identifier. The profile maps `requests`; it does not
accept token estimates for the opaque agent. Scope, saved target/governance/network
records and independent caller/actor memberships are rechecked before dispatch.
AWS credential acquisition and signing-account attestation occur after admission.

HTTP exchange completion settles one request. Native binary/SSE data streams
without transformation; the bounded transport tail/EOF waits for acknowledgement.
This is transport finality, not a guarantee that every earlier native business
final event is withheld or that an upstream background job is complete. Truncated
or cancelled exchanges retain unknown exposure. Controlled sessions separate
different actors delegated by the same human; legacy unprotected session
namespaces remain compatible.

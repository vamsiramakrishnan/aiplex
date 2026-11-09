# Connected Plexes

The reviewed design is [Connected Plexes](design/2026-11-09-connected-plexes.md).
Native identity directories, registry APIs, delegation brokers and reviewed
policy apply are documented in [Native agent governance](native-agent-governance.md).
Modules share trusted identity, revisioned resource storage and endpoint policy;
they retain ownership of their own domain objects.

| Object | Module | API |
|---|---|---|
| Model surface + identity/network references | LLMPlex | `/api/v1/llm/surfaces/{id}` |
| Native operation catalog | LLMPlex | `/api/v1/llm/operations` |
| Native provider HTTP/SSE/Live request | LLMPlex | `/api/v1/llm/native/{id}/...` |
| Client wire facade | LLMPlex | `/api/v1/llm/gateway/{id}/...` |
| Cloud agent connection | A2APlex | `/api/v1/a2a/connections/{id}` |
| Native agent invocation | A2APlex | `/api/v1/a2a/connections/{id}/invoke` |
| Tool endpoint + OAuth client profile | MCPlex | `/api/v1/mcp/connections/{id}` |
| Issuer-bound OAuth registration | MCPlex | `/api/v1/mcp/connections/{id}/registration` |
| Developer client registration/plan | ClientPlex | `/api/v1/clients/registrations/{id}` |
| Explicit external gateway destination | ClientPlex | `/api/v1/clients/destinations/{id}` |
| Native network provisioning profile | Networking | `/api/v1/network/profiles/{id}` |
| Missing-construct deployment intent | Networking | `/api/v1/network/deployment-intents/{id}` |
| Approved Anvil operation bindings | Networking | `/api/v1/network/deployment-providers/{provider}/bindings` |
| Saved intent → Anvil dry-run plan | Networking | `/api/v1/network/deployment-intents/{id}/plan` |

Resource PUT bodies are the module specification, not a caller-supplied tenant
wrapper. Create uses `If-Match: 0`; updates/deletes use the current revision.
Read returns `{tenant_id,kind,id,revision,spec}` plus ETag. Deleted identifiers
retain revision tombstones, so stale writers cannot overwrite a recreated
resource. Missing/cross-tenant references fail closed.

## Identity and provider configuration

`AIPLEX_TENANT_ID` is an operator-owned single-tenant binding. For multi-tenant
instances, verified `aiplex:tenant:<id>` grants supply tenancy; ambiguous multiple
tenant grants are rejected. Request bodies and custom headers never select a
tenant. Native invocation additionally requires resource/action grants, e.g.
`llm:surface:flash:models.generateContent` and `llm:model:gemini-example`.
Interactions selecting an agent also need `llm:agent:<id>`. Shared upstream list
operations need `llm:surface:<id>:shared-admin`; stateful resources bind to their
principal, tenant and surface revision.

Set `AIPLEX_CONNECTIONS_FILE` to an operator-owned configuration file:

```json
{
  "network": {
    "allowed_origins": ["https://aiplatform.googleapis.com"],
    "allow_private_cidrs": []
  },
  "identities": [{
    "id": "models-adc",
    "tenant_id": "example",
    "type": "gcp-adc",
    "allowed_origins": ["https://aiplatform.googleapis.com"],
    "scopes": ["https://www.googleapis.com/auth/cloud-platform"]
  }]
}
```

Credential adapters are Google ADC (including configured WIF external accounts),
AWS default SDK chain/SigV4, Azure DefaultAzureCredential with Foundry scope, and
explicit host bearer environment references. Each identity separately restricts
its credential destination origins; the global egress allowlist is not itself
permission to attach a credential. Credential caches are tenant/profile scoped,
use host-lifetime bounded transports, and reuse upstream HTTP connections.

All metadata/inference transports verify TLS, reject redirects, validate and pin
DNS at dial time, and prohibit cloud metadata addresses. Explicit corporate
private CIDRs can allow private endpoints; they never permit metadata endpoints.
Mount corporate CA roots through the operating-system trust configuration where
required. Configuring an endpoint does not provision its private DNS or routing.

Native Google Agent Identity is a distinct verified identity source. An identity
source can include `native_agent_identity` with exact STS issuer, audience,
trust domain, SPIFFE subject allowlist and explicit binding mode. Required bound
mode needs `TLS_CERT_FILE`, `TLS_KEY_FILE` and `CLIENT_CA_FILE`; the server verifies
actual mTLS chains and the token's certificate fingerprint. Mixed human and agent
clients can share that listener. Forwarded certificate headers are not proof.
GCP's automatic cert-bound outbound token acquisition is not implied by ordinary
Go ADC. Supply an appropriate native transport when that mode is required.

## Client compatibility

Set `AIPLEX_PUBLIC_URL` to the trusted Aiplex HTTPS origin before generating plans
for its facade. A model-surface destination resolves to the Aiplex facade, never
to the raw Google upstream. Supported facades adapt GenAI v1beta model paths,
OpenAI chat/Responses, native Vertex Anthropic paths and Anthropic Messages to
an explicitly compatible Cloud surface. Unknown protocols fail locally.

Aiplex accepts its configured verifier credentials, not arbitrary opaque gateway
keys. A ClientPlex credential reference targeting Aiplex must resolve to an
accepted JWT/native credential or an explicitly installed authentication adapter.
Client registration alone does not issue tokens. Client plans identify credential
bindings and emit reviewable fragments; they never overwrite workstation files.

Antigravity's `businessaicode.googleapis.com` and `cloudcode-pa.googleapis.com`
license/catalog traffic remains distinct from inference. Direct enterprise mode
uses the native client's supported sign-in; gateway mode preserves attribution
headers and uses the selected gateway billing mode. These control-plane APIs are
not fabricated as interchangeable model endpoints.

Codex gateway mode requires Responses; chat-only destinations are rejected.
Claude Apps Gateway uses the licensed native gateway service and OIDC/bootstrap
contract, separate from generic Anthropic Messages/Vertex proxies. ClientPlex can
render its native server configuration and client policy from complete approved
inputs. It does not reimplement the proprietary sign-in service.

Future model availability is not encoded as a current capability. Model names
and operation sets are administrator-controlled, and upstream availability must
be verified for the target project and release.

## MCP OAuth registration

Configure allowed resource/issuer/metadata origins and any public CIMD documents
in the provider file. Registration selects issuer-bound pre-registration, CIMD,
then advertised DCR, with PKCE S256 and exact resource/issuer/redirect binding.
The configured server hosts CIMD documents at their exact public URLs. It never
advertises an authorization-server registration endpoint it cannot actually issue.

Production DCR secrets go to Secret Manager as tenant/issuer/profile-bound
version-specific references. Only NotFound permits new registration; permission
or network failures fail closed. Concurrent calls coalesce within a host, while
immutable secret versions prevent cross-host client/secret confusion. User login
and token exchange are separate OAuth mechanisms. See `pkg/toolauth/README.md`.

## Native networking and Anvil fallback

`POST .../network/profiles/{id}/plan` renders validated executable native artifacts
for GKE agent identity, AWS PrivateLink, Azure Foundry private endpoint/DNS, or
Kubernetes egress isolation. A network reference records intended provisioning;
the effective Go outbound boundary remains the operator's endpoint policy.
Applying and observing the desired infrastructure are separate operations.

Use `deployment.Intent` when a native profile lacks a construct. Its provider and
binding reference must resolve to an operator-pinned Anvil bundle/operation whose
ResourceID equals the intent ID. Inputs come from the saved CAS-versioned intent.
`POST .../deployment-intents/{id}/plan` requires `Idempotency-Key` and an explicit
`aiplex:deployment:plan:<resource>` grant. Plans bind caller/actor, input bytes,
intent/network revisions, bundle snapshot, executable, Anvil build and expiry.
Public previews omit credentials and raw business payloads.

Anvil's own deploy/publish commands prepare connector artifacts; they do not prove
cloud apply or readiness. The HTTP integration exposes planning only. The public
provider library additionally supports execution with host-verified approval and
a durable receipt journal, disabled by default, and preserves ambiguous effects as
`unknown` for reconciliation. See [Anvil provider](anvil-deployment-provider.md).

## Validation scope

Hermetic tests cover signed JWT/mTLS, real HTTP/SSE/WebSocket relays, SDK SigV4,
OAuth discovery/registration, client wire compatibility, tenant/owner/revision
isolation, network artifacts, and real local Anvil CLI dry-run/hash compatibility.
Target cloud IAM, private connectivity, model billing/availability, production
IdP sign-in and licensed Claude gateway deployment require live environment
validation. No live cloud infrastructure was applied during implementation.
QuotaPlex allocation and accounting remain a separate module.
Its [prerequisite checklist](quotaplex-prerequisites.md) distinguishes reusable
foundations, pending host integrations and production enforcement gates.

# Connected Plexes: reviewed implementation design

Status: independently reviewed; approved for implementation with the conditions below.

## Review conditions accepted before implementation

1. Separate native invocation action authorization from control-plane CRUD roles;
   require explicit admission before credentials or upstream connections.
2. Derive tenants from verified context, bind every persisted resource/secret/
   session to tenant, and reject cross-tenant references.
3. Validate model selectors in paths and bodies. Bind interactions/caches/jobs
   to tenant and owner; list/shared mode requires an explicit shared-admin grant.
4. Require exact native Agent Identity issuer/audience/SPIFFE subject and trusted
   TLS chain/certificate binding. Never downgrade proof requirements.
5. Pin DNS at connection time, retain TLS hostname verification, and prohibit
   metadata IPs even with broad private CIDR configuration.
6. Bound streams per event/frame/session with cancellation/backpressure. Advertise
   Live, Responses and SigV4 only with functioning adapters.
7. Produce executable validated network artifacts, distinguishing desired from
   provisioned controls; metadata registration alone is not traffic interception.

Review outcome: no remaining architectural blocker with these conditions.


## Goal and ownership

Extend the module foundations with real integration contracts and testable native
adapters. Avoid equating registration metadata with actual governance. Existing
QuotaPlex allocation/accounting remains a separate implementation phase.

| Construct | Owner | Meaning |
|---|---|---|
| Principal / AgentIdentityProfile | Identity | Verified human, agent and delegation facts; native Google Agent Identity distinct from WIF |
| ClientProfile / ClientRegistration | ClientPlex | Client application, configuration and compatible connection mode |
| ToolConnection / OAuthRegistration | MCPlex + toolauth | Resource endpoint, AS discovery and issuer-scoped OAuth client registration |
| AgentConnection | A2APlex + cloudagents | Provider agent resource, native invocation adapter and declared control point |
| ModelSurface / Operation | LLMPlex + modelapi | Provider-native operations, transports and model/profile capability bounds |
| NetworkProfile / NetworkPlan | shared networking | Allowed endpoints, private connectivity and deterministic provisioning artifacts |
| PolicySnapshot / AdmissionReceipt | policy + enforcement | Published rules, trusted admission state and settlement hooks |

Client app identity, OAuth registration, authenticated principal and remote agent
identity are separate objects linked by references. Credentials are server-owned
references; persisted resources and public responses never contain access tokens
or registration secrets. Every resource is tenant-scoped, revisioned and owned
by its module. Disabled modules cannot use shared mutation paths.

## Shared network and request boundary

A shared endpoint policy restricts exact operator-approved origins, methods and
paths; HTTPS, bounded requests and responses; no automatic redirects; validated
DNS with connection-time pinning; metadata/private/link-local addresses denied
unless explicitly approved private networks. The same transport is reused by
OAuth metadata, remote agents, model surfaces and client metadata documents.

Native forwarding preserves unknown JSON fields, SSE framing and protocol error
responses. Caller Authorization and hop-by-hop headers are stripped. Auth is
injected by configured credential transport; no automatic retries of mutating
operations. Admission hooks remain explicit: a declared capability does not
silently attach enforcement or produce quota guarantees.

## Native cloud identities and remote agents

Google Agent Identity is a SPIFFE principal with pool-scoped STS issuer/JWKS,
not a service-account alias. Verify the issuer, allowed SPIFFE subject and, when
required, cnf.x5t#S256 against a verified mTLS leaf certificate. Never silently
fall back from certificate-bound to unbound identity. Render GKE identity and
registry annotations from validated configuration.

AgentConnection distinguishes Google Agent Runtime, Bedrock Agents Classic,
AgentCore runtime, Microsoft Foundry Agent Service and Copilot Studio. Each
adapter constructs documented native requests and uses an explicit credential
transport: Google ADC/native identity, AWS SigV4 or supported OAuth, Entra tokens,
or DirectLine credentials. Session and tenant context are validated. Native
streaming formats remain native. Invocation governance covers ingress; egress
requires a separately configured gateway/network control point. Managed agent
internals cannot be promised as universally intercepted.

Network plans render inspectable provider-specific desired state (GKE policies,
agent identity metadata, AWS private endpoint service requirements, Azure private
endpoint/DNS/subnet dependencies and egress allowlists). No live cloud apply is
performed without an organization-specific concrete plan and credentials.

## MCP OAuth registration

Keep MCPlex as the module; toolauth is a public library. Registration preference:
issuer-bound preregistration, advertised CIMD, advertised DCR, actionable manual
registration. DCR is OAuth client registration, not adding tools to a registry.
Implement protected-resource and AS metadata discovery with exact issuer checks,
multiple-AS selection, redirect URI allowlist, PKCE S256/resource requirements,
CIMD client_id/document identity binding and bounded caching. DCR writes secrets
to a credential-store interface and returns only a credential reference.

Host CIMD documents through a stable module endpoint. Inbound AS registration
support must call a real configured issuer adapter (Hydra where appropriate);
never advertise a DCR endpoint that only stores metadata. Registration does not
complete user authorization: authorization/token exchange remains its own flow.

## Native model operations

LLMPlex owns an opt-in operation catalog bound to validated ModelSurface profiles.
Support Cloud Interactions create/list/get/cancel/delete and streaming reconnect;
generate/stream/count/compute tokens; embeddings/predict/raw predict; cache CRUD;
batch and tuning jobs; long-running media operations; OpenAI-compatible chat and
Responses when actually exposed by the configured destination; Live as explicit
WebSocket transport. Cloud Storage/RAG media and Developer Files stay separate.
Provider versions, project, location and model availability are profile-owned.
Unsupported model/operation combinations fail locally. Preserve native payload,
SSE ids and terminal/provisional upstream usage provenance.

Future provider/model availability remains administrator-configurable and
requires a verified API contract; no unpublished endpoint or release claim is
baked into code.

## ClientPlex

Profiles cover Antigravity, Codex, Claude Code and Claude Apps Gateway separately.
A registration links principal, destination, project/location, model capability,
auth, subscription mode and network profile. Plans produce reviewable config
fragments and network requirements without overwriting existing client config.

Codex gateway compatibility requires Responses; chat-only endpoints are rejected.
Antigravity direct enterprise control-plane hosts businessaicode.googleapis.com
and cloudcode-pa.googleapis.com remain distinct from inference. Preserve Google
client attribution headers and streaming behavior. Claude Code Vertex mode and
Claude Apps Gateway have separate verified settings and authentication contracts.
Future/unpublished integrations are configurable with explicit support status.

## Integration and acceptance criteria

New public packages must run without importing internal Aiplex packages. Module
API adapters provide tenant-aware CRUD, validation and plan/registration/native
invocation endpoints using narrow storage interfaces. Persistence must support
memory tests and the configured Firestore backend. Console/CLI discover the new
module; SDK can manage the same resources. No fake enabled capability if the
required credential/network/transport adapter is absent.

Tests must cover signed identity and certificate binding, tenant isolation,
OAuth discovery/issuer/redirect/secret safety, DNS and redirect SSRF, native
operation routing and streaming preservation, client protocol compatibility,
network plan stability and disabled-module bypasses. Real cloud deployment and
provider billing remain live integration checks with user credentials.

## Sources

- https://docs.cloud.google.com/iam/docs/agent-identity-overview
- https://docs.cloud.google.com/iam/docs/auth-agent-own-identity-external
- https://docs.cloud.google.com/kubernetes-engine/security/request-agent-identity
- https://docs.aws.amazon.com/bedrock-agentcore/latest/APIReference/API_InvokeAgentRuntime.html
- https://learn.microsoft.com/en-us/azure/foundry/reference/foundry-project
- https://learn.microsoft.com/en-us/microsoft-copilot-studio/publication-connect-bot-to-custom-application
- https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/authorization/client-registration.mdx
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/models/interactions-api
- https://docs.cloud.google.com/gemini/enterprise/docs/ai-developer-tools/deploy-enterprise-gateway

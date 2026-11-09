# Google native agent governance

The `pkg/governance/google` adapters call Google's actual Agent Registry, IAP,
Agent Identity and Agent Identity Credentials APIs. They import public Aiplex
contracts only. Authentication identity, registry inventory, topology bindings,
and access policy are separate constructs.

| Construct | Meaning | Implementation |
| --- | --- | --- |
| Agent Identity | SPIFFE identity, native signed assertion and optional certificate binding | Existing inbound `pkg/agentidentity`; outbound `NewNativeClient` below |
| Agent Registry Service | User-managed registration resulting in a Google-generated Agent, MCP server or Endpoint resource | Native create/get/list/patch/delete |
| Registry Binding | Directed source Agent URN to target Agent/MCP/Endpoint URN; optional auth-provider association | Native create/get/list/patch/delete |
| Discovery resource | Google-assigned resource name and immutable registry identifier | Native Agent/MCP/Endpoint list/get using v1; Skill list/get using the documented v1alpha surface |
| Registry application policy | Administrative access to an `aiApplications` resource | Native get/set IAM policy |
| IAP destination policy | IAM authorization evaluated for registered-destination egress | Native get/set policy through `iap.googleapis.com` |
| Auth provider | Managed external OAuth/API-key credential configuration | Credential-sensitive Agent Identity v1 library driver |
| Delegated user credential | Credential requested for an explicitly authorized user by an explicitly authorized agent | Typed Credentials API retrieve/finalize library client |
| Agent Gateway | Routing/enforcement infrastructure that must actually be attached to workload traffic | Typed `GovernanceLink` reference; this package does not provision a gateway or claim that enforcement is active |

## Host configuration and native operations

Construct a `nativeapi.Client` with an explicit constrained HTTP transport and an
operator-selected credential adapter. Select the correct origin for each driver:

| Constructor | API origin | Configuration |
| --- | --- | --- |
| `google.New` | `https://agentregistry.googleapis.com` | Exact project ID/number and location |
| `google.NewIAP` | `https://iap.googleapis.com` | Exact numeric project number and registry resource location |
| `google.NewIdentityManager` | `https://agentidentity.googleapis.com` | Exact project/location; credential-sensitive operations only |
| `google.NewCredentialsClient` | `https://agentidentitycredentials.googleapis.com` | Exact project/location, exact allowed agent principals, mandatory delegated-user authorizer |

`Config` supports `project`, `location`, `allowed_identifiers`,
`allowed_agent_principals`, `allowed_roles`, and
`allow_non_atomic_mutations`. Allowlisted registry identifiers are exact URNs.
Allowlisted agent principals are exact `principal://agents.global.../resources/...`
identities. Allowlisted roles are exact predefined role names such as
`roles/iap.egressor`; project-custom roles are currently rejected.

`Registry.Operations()` exposes:

- `services.{list,get,create,patch,delete}` and the corresponding `bindings` operations;
- `agents.{list,get}`, `mcpServers.{list,get}`, `endpoints.{list,get}`, `skills.{list,get}`;
- `operations.get` to observe a returned long-running operation;
- `aiApplications.{getIamPolicy,setIamPolicy}`.

`IAP.Operations()` exposes `egress.getIamPolicy` and `egress.setIamPolicy`.
An IAP operation takes the canonical Registry resource name as `Resource`, for
example `projects/123456/locations/global/mcpServers/target`; the adapter derives
the documented IAP `iap_web/agentRegistry/mcpServers/target` path. It supports
per-destination policy only; it does not broaden a request to a registry-wide or
unregistered-destination policy.

All drivers implement pure `ValidateOperation` before any credentials or RPC.
Operation paths cannot select another configured project/location, another API
host, an encoded separator, or an RPC suffix. The host controller must authorize
the tenant, operation, saved resource and reviewed plan before executing a driver.
Opaque Agent Cards and MCP specifications must additionally pass the host's
public-payload secret filter; they are never credential configuration channels.

A create/patch payload uses a stable adapter envelope:

```json
{
  "resource": {
    "name": "projects/example/locations/global/services/my-agent",
    "displayName": "Example agent",
    "agentSpec": {"type": "NO_SPEC"}
  },
  "requestId": "531d05b9-c62c-4b72-9f4c-a6c77fa62d17"
}
```

Use the exact resource name as `Operation.Resource`. A create derives
`serviceId`/`bindingId` from the final path component. A patch also requires an
explicit writable `updateMask`, such as `displayName`. A delete accepts only an
optional `requestId` in its payload. List uses the exact
`projects/PROJECT/locations/LOCATION` parent and optional pageSize/pageToken/filter/orderBy.
Pagination is explicit; a single list call does not claim a complete inventory.
Mutations can return a native long-running Operation rather than a ready
resource; observe its outcome before declaring reconciliation successful.

## Concurrency and policy scope

Google's documented Service, Binding and AuthProvider schemas do not contain a
resource ETag. Their patch/delete contracts do not document an atomic `If-Match`
or ETag precondition. This adapter does not invent one.

Creates use caller-selected unique resource IDs, so an existing resource is not
silently overwritten. Existing-resource mutations are disabled by default. An
operator can explicitly enable `allow_non_atomic_mutations`; an update/delete
then requires the `sha256:` fingerprint returned in `Result.ETag` by the prior
get. The adapter re-reads and compares that canonical observation before the
write. This catches stale reviewed plans. It **cannot** prevent another writer
from changing the resource between the read and write. The durable journal
prevents duplicate execution of the same saved action; it does not serialize
independently created plans or external writers. Restrict concurrent writers
where this non-atomic mode is acceptable. This fingerprint is not a Google ETag.

IAM policies support real provider concurrency. A set-policy envelope contains
`policy` and an optional `updateMask`; `policy.etag` must equal `Operation.ETag`.
Conditional bindings require policy version 3. The driver obtains a fresh
version-3 policy, checks the same provider ETag, and compares grants individually.
New, removed or condition-changed grants require both an exact allowlisted agent
principal and an exact allowlisted role. Unchanged unmanaged grants can remain;
removing or changing unmanaged grants or audit configuration is rejected. The
native set includes the ETag so Google rejects a subsequent concurrent change.
Registration and registry topology bindings do not grant IAP egress permission.

## Auth manager and delegated users

`IdentityManager` calls the v1 auth-provider list/get/create/patch/delete,
enable/disable and IAM policy operations. Every operation is marked
`Credential: true`, including reads, and must remain outside generic public
governance HTTP routes. Secret-bearing provider parameters belong in an approved
credential workflow, never an inventory specification or plan preview.

`CredentialsClient` deliberately does not implement the generic Driver interface.
Its typed `Retrieve` and `Finalize` methods call the real
`/authProviders/PROVIDER/credentials:retrieve` and `credentials:finalize` APIs.
`Delegation.AgentPrincipal` and `Delegation.UserID` remain separate. The host must
derive the agent and user from trusted authentication/session state and supply
a mandatory authorizer for their relationship, provider and scopes before a
credential request. The request sends the documented `userId`; it does not
replace the agent's Google authentication token with the user's identity.

Retrieve returns success, pending, consent-required or consent-rejected states.
Consent metadata preserves `authorizationUri`, `consentNonce` and `uid`.
The host owns user-interactive consent, callback authentication and durable
nonce/state correlation. Finalize requires the validated state and nonce.
There is no automatic polling, token logging, public credential-response route
or automatic application of retrieved headers to an arbitrary destination.
Outbound adapters must bind the credential to its approved provider/destination
and verify that granted scopes meet their needs.

## Outbound certificate-bound native identity

`NewNativeClient` creates a transport for a single HTTPS origin and a single
native agent subject/audience. It requires an explicit constrained
`*http.Transport`, a client-certificate trust pool, a `CertificateProvider`, a
real `NativeTokenProvider`, and a cryptographic `agentidentity.ClaimsVerifier`.

The provider supplies a real native Google certificate-bound **JWT ID token**
for the requested audience and selected certificate. The adapter validates the
client certificate chain, issuer, audience, exact SPIFFE subject, `cnf`
certificate fingerprint and certificate URI SAN before sending anything. It
presents that certificate through the standard-library TLS client-certificate
hook with TLS 1.3. Certificates are reselected per request; connection pools are
isolated by certificate fingerprint so rotation cannot pair a new bound token
with an old authenticated TLS session. Redirects and ambient proxies are off.
`FileCertificateProvider` reloads PEM credentials; a host can inject a more
efficient runtime-aware rotating provider.

This package does not invent an STS exchange, discover pod credentials by
guessing paths, disable certificate binding, or turn ordinary ADC bearer tokens
into native agent credentials. The token provider must use the runtime's
documented mechanism. The JWT transport is appropriate for a receiving service
that accepts a native ID token and enforces binding. Google APIs such as Agent
Registry generally require OAuth access tokens: do not send a JWT ID token to
such an API as a substitute. Cert-bound OAuth access-token acquisition remains
the responsibility of the explicitly injected Google runtime credential adapter.

## Operational boundaries

The library provides real API calls and scoped policy updates, not an autonomous
fleet reconciler. It does not enable APIs, configure cross-project management
boundaries, apply the GKE registration patch, deploy Agent Gateway, attach
Service Extensions, install gateway trust roots in agent containers, or verify
that all workload traffic actually traverses the gateway. `GovernanceLink`
stores the relationships needed to connect those mechanisms without conflating
their identifiers. Provision/apply and observed readiness require the separate
network/deployment lifecycle and cloud integration tests with operator-provided
credentials. Unit tests use local native-protocol HTTP and real mTLS fixtures;
no live cloud changes were performed.

## Primary API contracts

- [Agent Registry REST reference](https://docs.cloud.google.com/agent-registry/reference/rest)
- [Service schema](https://docs.cloud.google.com/agent-registry/reference/rest/v1/projects.locations.services) and [patch contract](https://docs.cloud.google.com/agent-registry/reference/rest/v1/projects.locations.services/patch)
- [Binding schema](https://docs.cloud.google.com/agent-registry/reference/rest/v1/projects.locations.bindings)
- [Registry identity and identifiers](https://docs.cloud.google.com/agent-registry/identity-and-identifiers)
- [IAP policy API](https://docs.cloud.google.com/iap/docs/reference/rest/v1/TopLevel/setIamPolicy) and [Agent Gateway policy troubleshooting](https://docs.cloud.google.com/gemini-enterprise-agent-platform/troubleshooting/troubleshoot-agent-gateway)
- [Agent Identity v1 auth providers](https://docs.cloud.google.com/iam/docs/reference/agentidentity/rest/v1/projects.locations.authProviders)
- [Credential retrieval](https://docs.cloud.google.com/iam/docs/reference/agentidentitycredentials/rest/v1/projects.locations.authProviders.credentials/retrieve) and [finalization](https://docs.cloud.google.com/iam/docs/reference/agentidentitycredentials/rest/v1/projects.locations.authProviders.credentials/finalize)
- [Native agent identity for external services](https://docs.cloud.google.com/iam/docs/auth-agent-own-identity-external)

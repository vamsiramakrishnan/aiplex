# Microsoft Entra Agent ID governance

`pkg/governance/entra` implements a native Microsoft Graph adapter and a separate
Microsoft Entra ID Auth SDK sidecar broker. Neither imports Aiplex internals.
Graph operations manage directory objects; the broker acquires agent credentials.
A directory sponsor is not an authenticated user, an agent identity, or a funding
owner. The adapter keeps those identifiers separate.

## Incoming resource identity

`entra.NewIdentityVerifier(IdentityConfig, ClaimsVerifier)` wraps a cryptographic
verifier pinned to the Entra tenant issuer, the **Aiplex resource audience**, and
token lifetime. It processes the verified claims only. Outgoing Graph access
tokens remain opaque to the client; the host separately attests their native
tenant through `GET /v1.0/organization?$select=id` with the same token before
Graph operations. That host attestation requires `Organization.Read.All`, is
cached for the exact credential hash, and is repeated after token rotation.

An API identity source can enable native attribution explicitly:

```json
{
  "issuer": "https://login.microsoftonline.com/11111111-1111-1111-1111-111111111111/v2.0",
  "audience": "api://aiplex-agent-resource",
  "pool_id": "11111111-1111-1111-1111-111111111111",
  "native_entra_agent_id": {
    "tenant_id": "11111111-1111-1111-1111-111111111111",
    "allowed_agents": [{
      "object_id": "44444444-4444-4444-4444-444444444444",
      "app_id": "55555555-5555-5555-5555-555555555555"
    }],
    "allow_delegated": true,
    "required_scopes": ["aiplex.invoke"],
    "required_roles": ["Aiplex.Invoke"]
  }
}
```

The wrapper requires actor facet `11` and the exact allowed application-to-object
binding. Autonomous tokens also require application identity, subject facet `11`
and the agent object ID. Delegated tokens require explicit opt-in, a human user
object ID and delegated scopes. Agent-user account facet `13` is rejected.
Required scopes apply to delegation and required roles to autonomous access;
these are exact required-permission predicates, so extra permissions remain
allowed. Omitting required roles uses the configured agent list as an explicit
ACL. Native roles and scopes do not become Aiplex authorization grants.

Normalized `sub` is the native subject's tenant object ID; delegated `act.sub`
is the agent object ID, while `azp` is its application ID. The original subject
is retained as `entra_native_sub`. Raw actor maps are replaced for delegation
and removed for autonomous access. Parent blueprint `xms_par_app_azp` stays
audit-only; it never enrolls a child agent.

Enabling this normalization changes canonical principal IDs previously derived
from pairwise JWT `sub` values. Operators must explicitly regrant manual scopes
to the new tenant/object identities; there is no automatic alias or email merge.
Use a dedicated agent resource audience when human SSO shares the same Entra
issuer: duplicate issuer/audience source routing is intentionally rejected.

## Graph driver

Construct `entra.New(entra.Config, *nativeapi.Client)`. Pin the client to the
operator-approved global Microsoft Graph origin, `https://graph.microsoft.com`,
with a tenant-bound Graph authenticator and an endpoint-policy HTTP transport.
The client endpoint is the origin, without `/beta`; this driver supplies the
versioned paths. Caller payloads cannot select origins, paths, query expressions,
credential profiles, or raw Graph patches.

```json
{
  "tenant_id": "11111111-1111-1111-1111-111111111111",
  "api_version": "beta",
  "allowed_blueprints": [{
    "object_id": "22222222-2222-2222-2222-222222222222",
    "app_id": "33333333-3333-3333-3333-333333333333"
  }],
  "allow_blueprint_creation": false
}
```

This `tenant_id` is the Entra directory tenant GUID. Aiplex's tenant namespace
and authorization remain the host's separate boundary. Credentials supplied to
Graph must be issued for this configured Entra tenant and Graph audience.
Only the explicitly selected beta contract is implemented here. Microsoft
identifies beta APIs as subject to change; enabling this adapter does not assert
v1.0 parity or production support for that beta contract.

| Operation | Native API |
|---|---|
| `blueprint.create` | `POST /beta/applications/microsoft.graph.agentIdentityBlueprint` |
| `blueprint.get/update/delete` | `GET/PATCH/DELETE /beta/applications/{objectId}/microsoft.graph.agentIdentityBlueprint` |
| `blueprint.sponsors.list/add/remove` | Blueprint `/sponsors`, `/sponsors/$ref`, `/sponsors/{sponsorObjectId}/$ref` |
| `agent.create` | `POST /beta/servicePrincipals/microsoft.graph.agentIdentity` |
| `agent.list` | `GET /beta/servicePrincipals/microsoft.graph.agentIdentity`, with blueprint app-ID filter |
| `agent.get/update/enable/disable/delete` | `GET/PATCH/DELETE /beta/servicePrincipals/{objectId}/microsoft.graph.agentIdentity` |
| `agent.sponsors.list/add/remove` | Agent `/sponsors`, `/sponsors/$ref`, `/sponsors/{sponsorObjectId}/$ref` |
| `agent.appRoles.list` | `GET /beta/servicePrincipals/{objectId}/appRoleAssignments` |
| `agent.delegatedGrants.list` | `GET /beta/servicePrincipals/{objectId}/oauth2PermissionGrants` |

`Operation.Resource` is the immutable target **object ID**. For `agent.create`
and `agent.list`, the target is its blueprint object ID. For the separately
opted-in `blueprint.create`, it is the configured Entra tenant GUID; newly
created blueprints are not automatically enrolled in the management allowlist.

An agent operation's payload includes the explicit parent blueprint selector:

```json
{
  "tenant_id": "11111111-1111-1111-1111-111111111111",
  "blueprint_object_id": "22222222-2222-2222-2222-222222222222"
}
```

The driver checks native child parentage through a typed Graph read before
acting. `agentIdentityBlueprintId` is the blueprint's **application/client ID**,
not its application object ID. The configured object-to-app binding is also
checked against Graph for blueprint operations. `ValidateOperation` performs
local validation without contacting Graph; execution still performs the
provider-authoritative parentage checks.

Creation accepts `display_name` and one to 100 `sponsors`, each with an immutable
`object_id` and `type` of `user` or `group`. At most five may be groups. Graph
remains authoritative for supported group categories and retaining at least one
sponsor. Relationship operations accept one `sponsor`. Blueprint branding updates
accept `display_name` and optional `description`; agent updates accept only
`display_name`. Disable and enable send a native `accountEnabled` patch.
Credential creation, arbitrary `keyCredentials`/`passwordCredentials` changes,
permission grants and agent-user impersonation are not exposed.

Application-role assignments and delegated consent are inspected separately.
Blueprint `requiredResourceAccess` describes requested access; it does not prove
that consent or an assignment exists. Graph consent and directory roles must
already authorize the host principal. Inspect Graph's permissions tables for
AgentIdentity/AgentIdentityBlueprint lifecycle permissions, sponsor changes,
application-role reads and delegated-grant reads.

Responses preserve Graph pages; next links are not followed automatically.
There is no claim of a complete tenant-wide inventory. Credential-bearing
properties are removed from governance output, and upstream error payloads are
not embedded in errors. Conditional ETags are rejected because these selected
Agent ID operation contracts do not advertise conditional ETag writes.

## Privileged token broker

Microsoft recommends its approved SDK for the multi-stage agent OAuth protocols.
`NewTokenBroker(BrokerConfig, httpClient, hostAuthenticator)` implements the
published sidecar HTTP contract; it does not implement a generic client-secret
flow or manually assemble the two-hop token-exchange protocol.

The operator pins:

- Entra tenant, blueprint object ID and blueprint application/client ID.
- Allowed child agent object-to-application-ID bindings.
- Named downstream services configured inside the sidecar.
- Sidecar origin and host-owned authenticated/network-restricted transport.
- Explicit `agent_identity_overrides_enabled: true`, matching a sidecar
  configuration which honors `AgentIdentity` on the selected endpoints.

The sidecar itself must use the same tenant/blueprint, with managed-identity
federation or certificate credentials and configured downstream audiences/scopes.
Enable only the required SDK agent-identity override support on the pod-local or
otherwise authenticated sidecar boundary. The broker never sends caller-selected
scope, tenant, resource URL, FMI-path or credential overrides.

`AcquireAutonomous(ctx, TokenRequest)` calls:

```text
GET /AuthorizationHeaderUnauthenticated/{configuredService}?AgentIdentity={agentAppId}
```

The SDK performs the native blueprint exchange containing `fmi_path=agentAppId`
and the child agent exchange. An autonomous request containing a user object ID
is rejected; an agent-user identity is a distinct protocol that is not inferred.

`AcquireOnBehalfOf(ctx, TokenRequest, userAssertion)` calls:

```text
GET /Validate
Authorization: Bearer {userAssertion}

GET /AuthorizationHeader/{configuredService}?AgentIdentity={agentAppId}
Authorization: Bearer {sameUserAssertion}
```

Before the second call, the broker checks the authenticated sidecar result's
`tid`, user `oid`, and blueprint audience `aud` against the requested and
configured identities. This prevents substituting a sponsor, agent user, another
tenant or a token audienced to Graph for the required blueprint-audienced user
assertion. The assertion is sent only to the pinned sidecar.

OBO reserves `Authorization` for the incoming user assertion. Authenticate the
host to the sidecar through a separate header or mutual TLS; a host
authenticator that sets its own `Authorization` is rejected before network
access, including noncanonical header casing.

`Authorization.Header()` and `.BearerToken()` are explicit privileged accessors
for a host credential injector. Formatting and JSON serialization redact the
credential. The broker is a library API, not a public governance operation;
registering the Graph driver does not enable token minting through HTTP.
No readiness claim is derived solely from registering a sidecar URL. Live Entra
consent, credential configuration and SDK-version behavior require integration
validation with the target tenant and approved sidecar deployment.

## Validation

Hermetic request tests verify actual Graph methods, cast paths, sponsor `$ref`
payloads, application-versus-object IDs, native disable, permission inspection,
foreign-tenant/blueprint rejection before mutations, and error/response redaction.
Broker tests verify sidecar endpoint order, user/tenant/audience binding,
credential redaction, and denial before any network request for unapproved
agents, services or tenants.

## Primary protocol references

- [Create agentIdentityBlueprint](https://learn.microsoft.com/en-us/graph/api/agentidentityblueprint-post?view=graph-rest-beta)
- [Get agentIdentityBlueprint](https://learn.microsoft.com/en-us/graph/api/agentidentityblueprint-get?view=graph-rest-beta)
- [Create agentIdentity](https://learn.microsoft.com/en-us/graph/api/agentidentity-post?view=graph-rest-beta)
- [Update agentIdentity and accountEnabled](https://learn.microsoft.com/en-us/graph/api/agentidentity-update?view=graph-rest-beta)
- [Agent identity native properties and relationships](https://learn.microsoft.com/en-us/graph/api/resources/agentidentity?view=graph-rest-beta)
- [Sponsor relationships](https://learn.microsoft.com/en-us/graph/api/agentidentity-post-sponsors?view=graph-rest-beta)
- [Application-role assignments](https://learn.microsoft.com/en-us/graph/api/serviceprincipal-list-approleassignments?view=graph-rest-beta)
- [Delegated consent grants](https://learn.microsoft.com/en-us/graph/api/serviceprincipal-list-oauth2permissiongrants?view=graph-rest-beta)
- [Agent autonomous OAuth protocol](https://learn.microsoft.com/en-us/entra/agent-id/identity-platform/agent-autonomous-app-oauth-flow)
- [Agent OBO protocol](https://learn.microsoft.com/en-us/entra/agent-id/identity-platform/agent-on-behalf-of-oauth-flow)
- [Native agent token claims and attribution](https://learn.microsoft.com/en-us/entra/agent-id/agent-token-claims)
- [Approved Auth SDK sidecar HTTP endpoints](https://learn.microsoft.com/en-us/entra/msidweb/agent-id-sdk/endpoints)
- [Published sidecar OpenAPI](https://github.com/AzureAD/microsoft-identity-web/blob/master/src/Microsoft.Identity.Web.Sidecar/OpenAPI/Microsoft.Identity.Web.Sidecar.json)

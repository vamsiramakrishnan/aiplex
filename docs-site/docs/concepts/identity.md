---
title: Identity and authorization
description: One token across planes, the three limits that make up effective access, and where identity comes from.
---

# Identity and authorization

AIPlex verifies workforce, workload and native-agent credentials, resolves permissions and keeps principal and actor memberships separate. Every module uses the same identity layer, resource store and credential-destination policy.

## Effective access

Effective access is the intersection of three limits:

```text
agent ceiling ∩ user ceiling ∩ session consent = effective access
```

- The agent ceiling is the set of resources the registered agent may ever use.
- The user ceiling is the set of resources the user may use.
- Session consent is what the user approved for the current session.

The AIPlex API computes the intersection in its consent handler when Ory Hydra asks for consent, so the issued JWT carries only the effective scopes. The delegated agent is recorded in the RFC 8693 `act` claim, so audit records show both identities:

```json
{
  "sub": "student@school.edu",
  "azp": "tutor-agent",
  "act": { "sub": "spiffe://.../sa/tutor-agent" },
  "scope": "mcp:tools:search_curriculum a2a:task:research llm:model:gemini-2.5-flash"
}
```

## Enforcement

The gateway calls an `ext_authz` service for each request. The Rust service `aiplex-authz` verifies the JWT (RS256 signing keys only, pinned issuer, audience and JWKS URL) and checks the requested action against the token's scopes. The same checks are written as Rego in `policies/`. See [Authorization policy](../operations/authorization-policy.md).

## Sources of identity

| Source | Page |
| --- | --- |
| Workloads outside AIPlex (GitHub Actions, AWS IAM roles, Azure managed identities and others) | [Agent identity federation](../library/agent-identity-federation.md) |
| People (Google Workspace, Entra ID, Okta, Workforce pool, any OIDC issuer) | [User identity federation](../library/user-identity-federation.md) |
| Group membership | [Authoritative membership](../library/membership-authority.md) |
| Native cloud agent registries | [Native agent governance](../library/native-agent-governance.md) |
| Downstream tools that need a user's own authorization | [Tool connection auth](../library/tool-connection-auth.md) and [Owned MCP OAuth execution](../library/mcp-runtime-authorization.md) |

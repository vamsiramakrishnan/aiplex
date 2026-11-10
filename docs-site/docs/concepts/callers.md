---
title: Callers
description: "Agents, users and clients: how AIPlex classifies who is calling."
---

# Callers

A caller is whoever presents a credential to AIPlex. AIPlex classifies every caller as one of three kinds. The kind is derived from the verified identity; a request cannot set it.

| Kind | What it is | How it is recognized |
| --- | --- | --- |
| **Agent** | A registered workload or delegated agent, such as a tutor agent or a CI job | The token carries a delegated actor (RFC 8693 `act` claim), or the principal is a workload identity rather than a workforce user |
| **Client** | A registered developer client such as Claude, Codex or Antigravity | The token's authorized party (`azp`) equals the ID of a [ClientPlex](../guides/clientplex.md) registration |
| **User** | A person who signed in through a corporate identity provider | Anything else |

Precedence is agent, then client, then user. A delegated call keeps both identities: the principal (the user the call is on behalf of) and the actor (the agent), with separate membership views. Usage and audit records carry both.

## Where callers come from

- Agents authenticate with a token from their own platform, bound to a registered agent: [agent identity federation](../library/agent-identity-federation.md).
- Users sign in with Google Workspace, Microsoft Entra ID, Okta, a Google Workforce pool or any OIDC issuer: [user identity federation](../library/user-identity-federation.md).
- Groups come from verified claims or from a signed directory authority: [authoritative membership](../library/membership-authority.md).

See every caller and what each can reach with `aiplex who`, described in [Access points and the single pane](../guides/access-points.md).

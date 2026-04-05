---
sidebar_position: 2
title: Authentication
description: How AIPlex authenticates users and agents using Ory Hydra and Ory Kratos.
---

# Authentication

AIPlex uses **Ory Hydra** (OAuth 2.1 server) and **Ory Kratos** (identity management) for authentication. Together they handle user login, agent registration, token issuance, and social sign-in — all in ~100MB of memory.

## Architecture

```
User/Agent
    │
    ▼
┌─────────────────────────────────────────┐
│  Ory Kratos           Ory Hydra          │
│  (Identity)           (OAuth 2.1)        │
│                                          │
│  • User signup/login  • Token issuance   │
│  • OIDC brokering     • Client registry  │
│  • MFA                • Consent webhooks │
│  • Account recovery   • PKCE, DCR        │
│                                          │
│         AIPlex API                       │
│  • Consent handler (UI in Console)       │
│  • Token hook (act claim injection)      │
│  • Scope management                      │
└─────────────────────────────────────────┘
```

## Who Authenticates How

| Who | Method | OAuth Grant |
|-----|--------|-------------|
| **Human users** | Browser login via Kratos | Authorization Code + PKCE |
| **Internal agents** (same GKE) | SPIFFE mTLS | Client Credentials |
| **External agents** (AWS/Azure) | Workload Identity Federation | Client Credentials |
| **IDE plugins** (Cursor, VS Code) | User login + agent delegation | Authorization Code + PKCE |
| **CLI tools** (Claude Code) | Device flow | Device Grant (RFC 8628) |

## Token Format

Every AIPlex token is a JWT with the RFC 8693 actor claim:

```json
{
  "iss": "https://aiplex.example.com/auth/realms/aiplex",
  "sub": "student@school.edu",
  "azp": "tutor-agent",
  "act": {
    "sub": "spiffe://aiplex-prod.global.PROJECT.workload.id.goog/ns/a2aplex/sa/tutor-agent"
  },
  "scope": "mcp:tools:search_curriculum a2a:task:research llm:model:gemini-2.5-flash",
  "exp": 1714900800
}
```

| Claim | Meaning |
|-------|---------|
| `sub` | The user (human identity) |
| `azp` | The agent (OAuth client) |
| `act.sub` | The agent's infrastructure identity (SPIFFE ID) |
| `scope` | Effective permissions (intersection of A ∩ B ∩ C) |

The `act` claim is injected by AIPlex's **token hook** — a Go HTTP handler that Hydra calls before issuing each token. It maps the OAuth client to its SPIFFE identity.

## Consent Flow

When an agent acts on behalf of a user:

1. Agent redirects user to Hydra's `/oauth2/auth` endpoint
2. Hydra calls Kratos for user authentication (login screen)
3. Hydra redirects to AIPlex API's **consent handler**
4. AIPlex Console shows: "tutor-agent wants to access: search_curriculum, generate_quiz"
5. User selects which scopes to grant
6. AIPlex API calls Hydra Admin API to accept/reject consent
7. Hydra issues a token with only the approved scopes

AIPlex owns the consent UX entirely — it's a React page in the Console, not a templated server page.

## Social Sign-In

Ory Kratos brokers to external identity providers:

- Google Workspace
- Azure Active Directory
- Okta
- Any OIDC provider

Users authenticate with their existing corporate credentials. Kratos maps the external identity to an AIPlex user record.

## Why Ory, Not Keycloak?

| | Ory Hydra + Kratos | Keycloak |
|---|---|---|
| Image size | 30MB each | 500MB |
| Memory | ~100MB total | 1.5-2GB |
| Startup | < 1 second | 10-30 seconds |
| Language | Go | Java |
| Consent UX | Webhook → your React app | FreeMarker templates |
| Custom claims | Token hook (Go handler) | Java SPI JAR |
| OAuth 2.1 | Native | Partial |

Same compliance (OAuth 2.1, OIDC), 15x smaller footprint, and AIPlex controls the entire UX.

## Next

- [Scopes and Permissions](/docs/concepts/scopes-and-permissions) — how the three permission dimensions work
- [Identity and Zero Trust](/docs/concepts/identity-zero-trust) — SPIFFE, mTLS, and workload identity

---
sidebar_position: 3
title: Auth Deep Dive
description: Detailed architecture of Ory Hydra, Kratos, consent handling, and token hooks.
---

# Auth Deep Dive

This page covers the internal architecture of AIPlex's authentication and authorization system.

## Component Map

```
┌─────────────────────────────────────────────────────┐
│  Ory Kratos (Identity)                               │
│  • Self-service flows (login, register, recovery)    │
│  • OIDC brokering (Google, Azure AD, Okta)           │
│  • MFA (TOTP, WebAuthn)                              │
│  • Identity schema (custom fields)                   │
└─────────────────────┬───────────────────────────────┘
                      │ (user authenticated)
┌─────────────────────▼───────────────────────────────┐
│  Ory Hydra (OAuth 2.1)                               │
│  • Authorization server                              │
│  • Token issuance (JWT)                              │
│  • Client registry (agents)                          │
│  • Consent webhooks → AIPlex API                     │
│  • Token hooks → AIPlex API                          │
│  • DCR, PKCE, Device Grant                           │
└─────────────────────┬───────────────────────────────┘
                      │ (token with act claim)
┌─────────────────────▼───────────────────────────────┐
│  AIPlex API                                          │
│  • Consent handler (computes A ∩ B ∩ C)              │
│  • Token hook (injects act claim)                    │
│  • Scope registration (on deploy)                    │
│  • Agent registration (Hydra client creation)        │
└─────────────────────────────────────────────────────┘
```

## Consent Handler

Hydra delegates consent decisions to AIPlex via a webhook. This is where the three-dimensional permission model is computed.

### Flow

1. Agent redirects user to Hydra's `/oauth2/auth`
2. Hydra authenticates user via Kratos
3. Hydra calls AIPlex API's consent endpoint:
   ```
   GET /api/v1/auth/consent?consent_challenge=xyz
   ```
4. AIPlex API:
   - Fetches **dimension A** (agent's allowed scopes from Hydra client)
   - Fetches **dimension B** (user's allowed scopes from Firestore)
   - Computes **A ∩ B** = displayable scopes
   - Returns consent page data to Console
5. Console renders the consent UI showing only A ∩ B scopes
6. User approves subset = **dimension C**
7. Console calls:
   ```
   POST /api/v1/auth/consent
   { "challenge": "xyz", "granted_scopes": ["mcp:tools:search", ...] }
   ```
8. AIPlex API calls Hydra Admin API to accept consent
9. Hydra issues token with `scope` = A ∩ B ∩ C

### Code Path

```go
// Simplified consent handler
func handleConsent(w http.ResponseWriter, r *http.Request) {
    challenge := r.URL.Query().Get("consent_challenge")

    // Get consent request from Hydra
    consent, _ := hydra.GetConsentRequest(challenge)

    // Dimension A: agent's allowed scopes
    agentScopes := consent.Client.AllowedScopes

    // Dimension B: user's allowed scopes
    userScopes, _ := firestore.GetUserScopes(consent.Subject)

    // Displayable = A ∩ B
    displayable := intersect(agentScopes, userScopes)

    // Return to Console for user decision (dimension C)
    json.NewEncoder(w).Encode(ConsentData{
        Challenge: challenge,
        Client:    consent.Client.Name,
        Scopes:    displayable,
    })
}
```

## Token Hook

Hydra calls a token hook before issuing each token. AIPlex uses this to inject the RFC 8693 `act` claim.

```go
// Token hook: ~20 lines
func handleTokenHook(w http.ResponseWriter, r *http.Request) {
    var req TokenHookRequest
    json.NewDecoder(r.Body).Decode(&req)

    // Look up agent's SPIFFE ID from client metadata
    spiffeID := lookupSPIFFE(req.Session.ClientID)

    // Inject act claim
    resp := TokenHookResponse{
        Session: TokenSession{
            AccessToken: map[string]any{
                "act": map[string]string{
                    "sub": spiffeID,
                },
            },
        },
    }
    json.NewEncoder(w).Encode(resp)
}
```

No Java SPI. No Keycloak plugin. Just a Go HTTP handler.

## OAuth Flows

### Client Credentials (Machine-to-Machine)

```
Agent → Hydra: POST /oauth2/token
  grant_type=client_credentials
  client_id=...
  client_secret=...
  scope=mcp:tools:search

Hydra → Token Hook → AIPlex API (inject act claim)
Hydra → Agent: JWT with scopes + act claim
```

Used by: internal agents (same GKE), external agents (via WIF).

### Authorization Code + PKCE (User-Facing)

```
Agent → Hydra: GET /oauth2/auth
  response_type=code
  client_id=...
  scope=mcp:tools:search
  code_challenge=...

Hydra → Kratos: authenticate user
Hydra → AIPlex API: consent webhook
AIPlex Console → User: consent screen
User → AIPlex API: approve scopes
AIPlex API → Hydra: accept consent
Hydra → Agent: authorization code
Agent → Hydra: exchange code for token
Hydra → Token Hook → AIPlex API
Hydra → Agent: JWT
```

Used by: IDE plugins, web apps acting on user behalf.

### Device Grant (CLI)

```
CLI → Hydra: POST /oauth2/device/auth
Hydra → CLI: device_code + user_code + verification_uri

CLI shows: "Visit https://aiplex.example.com/device and enter: ABCD-1234"

User → Browser: authenticates + consents
Hydra → Token Hook → AIPlex API
CLI polls → Hydra: JWT
```

Used by: `aiplex login`, Claude Code, CLI agents.

## Hydra Configuration

Key Hydra settings for AIPlex:

```yaml
urls:
  consent: https://aiplex.example.com/api/v1/auth/consent
  login: https://aiplex.example.com/api/v1/auth/login
  self:
    issuer: https://aiplex.example.com/auth/realms/aiplex

oauth2:
  token_hook:
    url: https://aiplex.example.com/api/v1/auth/token-hook

strategies:
  jwt:
    scope_claim: scope
```

## Next

- [Deploy Engine](/docs/architecture/deploy-engine) — how instances are provisioned
- [Security Model](/docs/architecture/security-model) — threat model and defense-in-depth

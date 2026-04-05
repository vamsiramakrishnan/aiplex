# 02 — Auth: Ory Hydra + Kratos

## Overview

Ory Hydra is AIPlex's OAuth 2.1 / OIDC server. Ory Kratos is the identity manager. Together they handle authentication (who are you?), authorization (what can you do?), and consent (what did you agree to?). A single Hydra instance with one set of OAuth clients governs all three planes.

Hydra handles token issuance, consent challenges, and JWKS. Kratos handles user registration, login, password recovery, and social sign-in. AIPlex API sits between them as the consent handler and permission evaluator.

---

## OAuth Clients + Kratos Identities

### Hydra OAuth Clients (Agents)

```
Hydra OAuth Clients:
├── tutor-agent
├── assessment-agent
├── coding-agent
└── ... (registered via AIPlex API → Hydra Admin API)

Kratos Identities (Humans):
├── student@school.edu
├── teacher@school.edu
└── ... (via social sign-in or self-registration)

AIPlex API / Firestore:
├── Agent Allowed Scopes (Dimension A)
│   ├── tutor-agent → [mcp:tools:search_curriculum, ...]
│   └── assessment-agent → [mcp:tools:grade_submission, ...]
├── User Allowed Scopes (Dimension B)
│   ├── role:student → [mcp:tools:search_*, a2a:task:research, ...]
│   ├── role:teacher → [mcp:tools:*, a2a:task:*, llm:model:*]
│   └── role:admin → [*]
└── Scope Definitions
    ├── mcp:tools:search_curriculum
    ├── a2a:task:research
    ├── llm:model:gemini-2.5-flash
    └── ... (created dynamically during deploy)
```

> Decision: One Hydra instance, not one-per-tenant. Multi-tenancy is handled via Kratos identity traits and Firestore ownership, not separate OAuth issuers. This keeps the scope namespace unified and avoids cross-issuer token exchange.

---

## The Three Permission Dimensions

### Dimension A: Agent Ceiling

**What:** The maximum set of scopes an agent can ever request, regardless of which user is delegating.

**Configured by:** AIPlex admin (via Console or API)

**Stored in:** Hydra client `allowed_scopes` field (set via Hydra Admin API)

**Example:** `tutor-agent` can access at most:
```
mcp:tools:search_curriculum
mcp:tools:generate_quiz
mcp:tools:read_mastery
a2a:task:research
a2a:task:visualization
llm:model:gemini-2.5-flash
```

**Implementation:**
```python
# When admin configures agent ceiling
async def set_agent_ceiling(agent_id: str, scopes: list[str]):
    # Update the Hydra OAuth client's allowed scope list
    await hydra_admin.update_client(
        client_id=agent_id,
        allowed_scopes=scopes,  # Hydra enforces this ceiling at token issuance
    )

    # Persist in Firestore for Console display and audit
    await firestore.write("agent_ceilings", agent_id, {
        "scopes": scopes,
        "updated_at": utcnow(),
    })
```

### Dimension B: User Ceiling

**What:** The maximum set of scopes a user can delegate to any agent.

**Configured by:** AIPlex admin (via Console or API)

**Stored in:** AIPlex API / Firestore (role-based scope mappings)

**Example:** `student@school.edu` can delegate at most:
```
mcp:tools:search_curriculum     ✓ (read-only tools)
mcp:tools:generate_quiz         ✓
mcp:tools:modify_grades         ✗ (not for students)
a2a:task:research               ✓
llm:model:gemini-2.5-flash      ✓
llm:model:gpt-4o                ✗ (cost restricted)
```

**Implementation:**
```python
# User ceiling via Firestore role → scope mappings
# Role: student → scopes: [mcp:tools:search_*, a2a:task:research, llm:model:gemini-*]
# Role: teacher → scopes: [mcp:tools:*, a2a:task:*, llm:model:*]
# Role: admin   → scopes: [*]

async def set_user_ceiling(user_id: str, role: str):
    # Update Kratos identity traits with role
    identity = await kratos_admin.get_identity(user_id)
    identity["traits"]["role"] = role
    await kratos_admin.update_identity(user_id, identity)

    # Role → scope mapping stored in Firestore
    # Looked up by AIPlex consent handler at token issuance time
```

### Dimension C: Delegation (Consent)

**What:** The specific scopes a user has consented to for a particular agent in a particular session.

**Configured by:** User at runtime (consent screen rendered by AIPlex Console)

**Stored in:** Hydra consent decisions (persisted by Hydra when AIPlex consent handler accepts)

**Example:** When `student@school.edu` uses `tutor-agent`, AIPlex Console renders the consent screen:
```
┌─────────────────────────────────────────────┐
│  tutor-agent wants to:                       │
│                                              │
│  ☑ Search your curriculum materials          │
│    (mcp:tools:search_curriculum)             │
│  ☑ Generate quizzes for you                  │
│    (mcp:tools:generate_quiz)                 │
│  ☐ Read your mastery progress                │
│    (mcp:tools:read_mastery)                  │
│  ☑ Delegate research tasks                   │
│    (a2a:task:research)                       │
│  ☑ Use Gemini for reasoning                  │
│    (llm:model:gemini-2.5-flash)              │
│                                              │
│  [Allow]  [Deny]                             │
└─────────────────────────────────────────────┘
```

### Effective Permission = A ∩ B ∩ C

```
Agent ceiling (A):  {search, quiz, mastery, research, viz, gemini}
User ceiling (B):   {search, quiz, research, gemini}
User consent (C):   {search, quiz, research, gemini}
─────────────────────────────────────────────────
Effective:          {search, quiz, research, gemini}
```

The AIPlex API consent handler computes this intersection when Hydra delegates the consent challenge. Only the effective set is written into the Hydra consent accept response, and Hydra issues the JWT containing only those scopes.

---

## Token Lifecycle

### 1. Agent Registration (One-Time)

```
AIPlex Admin → Console → AIPlex API → Hydra Admin API + Kratos
                                        │
                                        ├── Create OAuth Client (agent_id)
                                        ├── Set grant_types (client_credentials / authorization_code)
                                        ├── Configure PKCE or client_secret or private_key_jwt
                                        ├── Set allowed_scopes (empty until admin configures Dim A)
                                        └── Return client_id + secret (or JWKS URI config)
```

### 2. Token Issuance (Per-Session)

**Internal Agent (Client Credentials):**
```
POST /oauth2/token
Content-Type: application/x-www-form-urlencoded

grant_type=client_credentials
&client_id=tutor-agent
&client_secret=<secret>
&scope=mcp:tools:search_curriculum a2a:task:research llm:model:gemini-2.5-flash
```

Hydra checks the requested scopes against the client's `allowed_scopes` (Dimension A). No consent flow for client_credentials — the token contains the intersection of requested and allowed scopes.

**IDE Agent (Authorization Code + PKCE):**
```
1. Agent redirects user to:
   /oauth2/auth
   ?response_type=code
   &client_id=cursor-plugin
   &redirect_uri=http://localhost:8765/callback
   &scope=mcp:tools:search_curriculum llm:model:gemini-2.5-flash
   &code_challenge=<S256 hash>
   &code_challenge_method=S256

2. Hydra redirects to Kratos login UI (user authenticates)
3. Hydra redirects to AIPlex consent endpoint (consent challenge)
4. AIPlex API computes A ∩ B, renders consent UI via AIPlex Console
5. User consents (Dimension C)
6. AIPlex API calls Hydra consent accept with effective scopes (A ∩ B ∩ C)
7. Hydra redirects with authorization code
8. Agent exchanges code for token
```

**CLI Agent (Device Grant — RFC 8628):**
```
1. POST /oauth2/device/auth
   client_id=claude-code-plugin
   scope=mcp:tools:search_curriculum

2. Response:
   device_code=<code>
   user_code=ABCD-EFGH
   verification_uri=https://aiplex.example.com/device

3. User visits URL, authenticates via Kratos, consents via AIPlex Console
4. Agent polls token endpoint until user completes flow
```

### 3. Token Format

```json
{
  "iss": "https://aiplex.example.com/",
  "sub": "student@school.edu",
  "azp": "tutor-agent",
  "act": {
    "sub": "spiffe://aiplex-prod.global.123456.workload.id.goog/ns/a2aplex/sa/tutor-agent"
  },
  "scope": "mcp:tools:search_curriculum mcp:tools:generate_quiz a2a:task:research llm:model:gemini-2.5-flash",
  "iat": 1714897200,
  "exp": 1714900800,
  "jti": "tok_abc123",
  "aud": "aiplex"
}
```

### 4. Token Refresh

- Access tokens: 1 hour expiry
- Refresh tokens: 8 hours expiry (sliding window)
- Offline tokens: for long-running background agents (admin-granted only, configured per Hydra client)

> Decision: 1-hour access token expiry balances security (short window) with usability (agents don't re-auth constantly). For streaming/SSE connections, the token is validated once at connection setup.

---

## Token Hook: `act` Claim

The `act` claim (RFC 8693) identifies which agent is acting on behalf of which user. This is added by a token hook — a Go HTTP handler that Hydra calls before issuing tokens.

### Go Token Hook (~30 lines)

```go
func tokenHook(w http.ResponseWriter, r *http.Request) {
    var req hydra.TokenHookRequest
    json.NewDecoder(r.Body).Decode(&req)

    clientID := req.Session.ClientID
    spiffeID := lookupSPIFFEID(clientID) // from Firestore or in-memory cache

    resp := hydra.TokenHookResponse{
        Session: hydra.TokenHookSession{
            AccessToken: map[string]interface{}{},
        },
    }

    if spiffeID != "" {
        resp.Session.AccessToken["act"] = map[string]string{
            "sub": spiffeID,
        }
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}
```

**Deployment:** A lightweight Go binary (~5MB), deployed as a sidecar or separate pod. Configured in Hydra via `oauth2.token_hook.url`. No Java, no JAR, no SPI.

### Why RFC 8693?

- Standard way to express "A acting as B"
- OPA can audit both identities from a single claim
- No custom claim parsing needed — well-understood format

---

## Identity: Kratos Social Sign-In

### Supported Identity Providers

| IdP | Protocol | Use Case |
|-----|----------|----------|
| Google Workspace | OIDC | Schools using Google |
| Azure AD | OIDC | Enterprise / Microsoft shops |
| Okta | OIDC | Enterprise SSO |
| Direct registration | Kratos native (email + password) | Individual users |

### First Login Flow

```
1. User clicks "Login with Google"
2. Kratos redirects to Google OIDC (configured as social sign-in connector)
3. Google authenticates user
4. Kratos receives id_token
5. Kratos self-service registration flow:
   a. Check if identity exists (by email trait)
   b. If not, create identity with Google attributes mapped to traits
   c. Set default role trait (looked up by AIPlex consent handler for Dim B)
   d. Kratos session established
6. Hydra picks up Kratos session, proceeds to consent flow
7. AIPlex consent handler renders consent screen
```

> Decision: Email is the linking attribute between providers and Kratos identities. If a user logs in with Google today and Azure AD tomorrow (same email), Kratos links them to the same identity with the same traits and permissions.

---

## Dynamic Client Registration (DCR)

External agents register via the Hydra Admin API (called by AIPlex API):

```
POST /admin/clients
Authorization: Bearer <aiplex-api-service-token>
Content-Type: application/json

{
  "client_name": "external-coding-agent",
  "grant_types": ["client_credentials"],
  "token_endpoint_auth_method": "private_key_jwt",
  "jwks_uri": "https://coding-agent.example.com/.well-known/jwks.json",
  "scope": ""
}
```

- AIPlex API wraps the Hydra Admin API with validation and audit logging
- DCR creates the client with empty `allowed_scopes` (no ceiling)
- Admin must explicitly set Dimension A before the agent can do anything
- This prevents self-registration from granting any access

---

## Consent Management

### Consent Flow (Hydra → AIPlex API → Console)

```
1. Hydra receives auth request with scopes
2. Hydra redirects to AIPlex API consent endpoint with consent_challenge
3. AIPlex API fetches challenge details from Hydra
4. AIPlex API computes A ∩ B (agent ceiling ∩ user ceiling)
5. AIPlex API returns consent UI data to AIPlex Console
6. Console renders scope checkboxes (only scopes in A ∩ B are shown)
7. User selects scopes (Dimension C)
8. Console POSTs selection to AIPlex API
9. AIPlex API calls Hydra consent accept with A ∩ B ∩ C
10. Hydra issues token with effective scopes
```

### Consent Storage

Hydra stores consent decisions per user-per-client:

```
Subject: student@school.edu
Client: tutor-agent
Granted scopes: [mcp:tools:search_curriculum, mcp:tools:generate_quiz, a2a:task:research, llm:model:gemini-2.5-flash]
Granted at: 2026-04-05T10:00:00Z
Remember: true (skip consent next time for same scopes)
```

### Consent Revocation

Users can revoke consent via the AIPlex Console:

```
AIPlex Console → AIPlex API → Hydra Admin API
  DELETE /admin/oauth2/auth/sessions/consent?subject=student@school.edu&client=tutor-agent
```

After revocation, the agent's existing tokens remain valid until expiry (up to 1 hour), but refresh fails. For immediate revocation, AIPlex API can call Hydra Admin API to revoke all active tokens for the client-subject pair.

### Incremental Consent

If an agent needs a new scope mid-session:

1. Agent requests authorization with additional scope
2. Hydra sends a new consent challenge to AIPlex API (previous consent doesn't cover the new scope)
3. AIPlex Console renders incremental consent screen (only the new scope)
4. User approves
5. New token issued with expanded scope set

---

## Ory Deployment

### Infrastructure

```yaml
# Ory Hydra on GKE
Helm chart: ory/hydra
Image size: ~10MB (Go static binary)
Database: Cloud SQL PostgreSQL (HA, automated backups)
Replicas: 2 (HA)
Memory: ~50MB per pod
TLS: Cloud Service Mesh mTLS (no separate cert)
Config: ConfigMap (hydra.yaml) + Secret (system secret, DB DSN)

# Ory Kratos on GKE
Helm chart: ory/kratos
Image size: ~15MB (Go static binary)
Database: Cloud SQL PostgreSQL (same instance, separate schema)
Replicas: 2 (HA)
Memory: ~50MB per pod
TLS: Cloud Service Mesh mTLS
Config: ConfigMap (kratos.yaml) + Secret (cookie/session secrets, DB DSN)

# Token Hook (act claim)
Image: custom Go binary (~5MB)
Memory: ~20MB per pod
Replicas: 2 (HA)
```

No Java. No FreeMarker templates. No Infinispan cache layer. No JVM heap tuning. Total memory footprint: ~240MB for the full auth stack (vs 1-2GB for Keycloak).

### Console Integration

Login and consent UIs are rendered by AIPlex Console (React):

```
console/src/pages/
├── Login.tsx              # Kratos self-service login flow
├── Registration.tsx       # Kratos self-service registration flow
├── Consent.tsx            # Hydra consent challenge UI
├── Recovery.tsx           # Kratos password recovery flow
└── Device.tsx             # Device grant user verification
```

AIPlex Console uses `@ory/client` SDK (or plain OIDC) to interact with Kratos and Hydra. No `keycloak-js` dependency.

### JWKS Endpoint

OPA and all token validators fetch signing keys from Hydra's JWKS endpoint:

```
GET /.well-known/jwks.json
```

Hydra manages key rotation automatically. OPA caches JWKS with a TTL and refreshes periodically.

---

## Edge Cases

### Token with no scopes
If A ∩ B ∩ C = empty set, the AIPlex consent handler accepts the consent with an empty scope list. Hydra issues a token with an empty scope string. OPA will deny all non-discovery requests. The agent can still call `tools/list` to see what's available but cannot invoke anything.

### Agent requests scope outside its ceiling
Hydra silently drops scopes that aren't in the client's `allowed_scopes`. The consent challenge only shows scopes that survived the A ∩ B intersection. The token contains only what the user actually consented to from that filtered set.

### User logs in from new IdP
If the email matches an existing Kratos identity, Kratos links the new social sign-in connector. If it's a new email, a new identity is created with default traits (empty role). Admin must assign role for Dimension B via AIPlex Console.

### Hydra or Kratos downtime
Existing JWTs remain valid (OPA validates locally using cached JWKS). No new tokens can be issued. Agents with refresh tokens will fail to refresh after access token expiry. Impact window = token TTL (1 hour). Hydra and Kratos are stateless Go binaries — restarts are sub-second.

### Clock skew
JWT validation allows 30-second clock skew (`nbf` and `exp` claims). Hydra, Kratos, and OPA pods use GKE node time (synced via NTP). Cloud SQL and Firestore use Google-managed time.

# 02 — Auth & Keycloak

## Overview

Keycloak is AIPlex's identity and access management layer. It handles authentication (who are you?), authorization (what can you do?), and consent (what did you agree to?). A single Keycloak realm governs all three planes.

---

## Realm Design

### Single Realm: `aiplex`

```
Realm: aiplex
├── Clients (agents)
│   ├── tutor-agent
│   ├── assessment-agent
│   ├── coding-agent
│   └── ... (registered via AIPlex API)
├── Users (humans)
│   ├── student@school.edu
│   ├── teacher@school.edu
│   └── ... (via OIDC brokering or direct)
├── Client Scopes
│   ├── mcp:tools:search_curriculum
│   ├── mcp:tools:generate_quiz
│   ├── a2a:task:research
│   ├── llm:model:gemini-2.5-flash
│   └── ... (created dynamically during deploy)
├── Resources
│   ├── knowledge-base-xyz (MCP server)
│   ├── research-agent (A2A agent)
│   ├── gemini-2.5-flash (LLM model)
│   └── ... (created dynamically during deploy)
├── Policies
│   ├── Agent ceiling policies (Dimension A)
│   ├── User ceiling policies (Dimension B)
│   └── Time-based, IP-based, custom policies
├── Permissions
│   └── Resource + scope + policy bindings
└── Identity Providers
    ├── google (OIDC)
    ├── azure-ad (OIDC)
    └── okta (OIDC)
```

> Decision: One realm, not one-per-tenant. Multi-tenancy is handled via Keycloak groups and resource ownership, not realm isolation. This keeps the scope namespace unified and avoids cross-realm token exchange.

---

## The Three Permission Dimensions

### Dimension A: Agent Ceiling

**What:** The maximum set of scopes an agent can ever request, regardless of which user is delegating.

**Configured by:** AIPlex admin (via Console or API)

**Stored in:** Keycloak client policies attached to the agent's client registration

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
    client = await keycloak.get_client(agent_id)
    
    # Set default client scopes (always included)
    # Set optional client scopes (available if user consents)
    await keycloak.update_client_scopes(
        client_id=client["id"],
        default_scopes=[],  # No scopes by default
        optional_scopes=scopes  # All available via consent
    )
    
    # Create a client policy that limits this agent
    await keycloak.create_client_policy(
        name=f"agent-ceiling-{agent_id}",
        clients=[client["id"]],
        scopes=scopes
    )
```

### Dimension B: User Ceiling

**What:** The maximum set of scopes a user can delegate to any agent.

**Configured by:** AIPlex admin (via Console or API)

**Stored in:** Keycloak user policies (role-based or attribute-based)

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
# User ceiling via Keycloak roles
# Role: student → scopes: [mcp:tools:search_*, a2a:task:research, llm:model:gemini-*]
# Role: teacher → scopes: [mcp:tools:*, a2a:task:*, llm:model:*]
# Role: admin   → scopes: [*]

async def set_user_ceiling(user_id: str, role: str):
    await keycloak.assign_role(user_id, role)
    # Role maps to scope sets via Keycloak role-scope mappings
```

### Dimension C: Delegation (Consent)

**What:** The specific scopes a user has consented to for a particular agent in a particular session.

**Configured by:** User at runtime (OAuth consent screen)

**Stored in:** OAuth token scopes (ephemeral, per-session)

**Example:** When `student@school.edu` uses `tutor-agent`, the consent screen shows:
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

Keycloak computes this intersection at token issuance. The JWT only contains the effective set.

---

## Token Lifecycle

### 1. Agent Registration (One-Time)

```
AIPlex Admin → Console → AIPlex API → Keycloak
                                        │
                                        ├── Create Client (agent_id)
                                        ├── Set client_credentials grant
                                        ├── Configure WIF or PKCE
                                        ├── Set optional client scopes
                                        └── Return client_id + secret (or WIF config)
```

### 2. Token Issuance (Per-Session)

**Internal Agent (Client Credentials):**
```
POST /auth/realms/aiplex/protocol/openid-connect/token
Content-Type: application/x-www-form-urlencoded

grant_type=client_credentials
&client_id=tutor-agent
&client_secret=<secret>
&scope=mcp:tools:search_curriculum a2a:task:research llm:model:gemini-2.5-flash
```

**IDE Agent (Authorization Code + PKCE):**
```
1. Agent redirects user to:
   /auth/realms/aiplex/protocol/openid-connect/auth
   ?response_type=code
   &client_id=cursor-plugin
   &redirect_uri=http://localhost:8765/callback
   &scope=mcp:tools:search_curriculum llm:model:gemini-2.5-flash
   &code_challenge=<S256 hash>
   &code_challenge_method=S256

2. User authenticates + consents
3. Keycloak redirects with code
4. Agent exchanges code for token
```

**CLI Agent (Device Grant — RFC 8628):**
```
1. POST /auth/realms/aiplex/protocol/openid-connect/auth/device
   client_id=claude-code-plugin
   scope=mcp:tools:search_curriculum

2. Response:
   device_code=<code>
   user_code=ABCD-EFGH
   verification_uri=https://aiplex.example.com/device

3. User visits URL, enters code, authenticates, consents
4. Agent polls token endpoint until user completes flow
```

### 3. Token Format

```json
{
  "iss": "https://aiplex.example.com/auth/realms/aiplex",
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
- Offline tokens: for long-running background agents (admin-granted only)

> Decision: 1-hour access token expiry balances security (short window) with usability (agents don't re-auth constantly). For streaming/SSE connections, the token is validated once at connection setup.

---

## Protocol Mapper: `act` Claim

The `act` claim (RFC 8693) identifies which agent is acting on behalf of which user. This is added by a custom Keycloak protocol mapper.

### Java SPI Implementation (~30 lines)

```java
public class ActorClaimMapper extends AbstractOIDCProtocolMapper
    implements OIDCAccessTokenMapper {

    @Override
    protected void setClaim(IDToken token, ProtocolMapperModel model,
                           UserSessionModel session, KeycloakSession keycloak,
                           ClientSessionContext context) {
        
        ClientModel client = context.getClientSession().getClient();
        String spiffeId = client.getAttribute("spiffe_id");
        
        if (spiffeId != null) {
            Map<String, String> actClaim = new HashMap<>();
            actClaim.put("sub", spiffeId);
            token.getOtherClaims().put("act", actClaim);
        }
    }
}
```

**Deployment:** Built as a JAR, placed in Keycloak's `providers/` directory. Configured as a protocol mapper on each client.

### Why RFC 8693?

- Standard way to express "A acting as B"
- OPA can audit both identities from a single claim
- No custom claim parsing needed — well-understood format

---

## Identity Brokering

### Supported IdPs

| IdP | Protocol | Use Case |
|-----|----------|----------|
| Google Workspace | OIDC | Schools using Google |
| Azure AD | OIDC | Enterprise / Microsoft shops |
| Okta | OIDC | Enterprise SSO |
| Direct registration | Keycloak native | Individual users |

### First Login Flow

```
1. User clicks "Login with Google"
2. Keycloak redirects to Google OIDC
3. Google authenticates user
4. Keycloak receives id_token
5. First-login flow:
   a. Check if user exists (by email)
   b. If not, create user with Google attributes
   c. Map Google groups → Keycloak roles → scope ceilings
   d. Redirect to original agent's consent screen
```

> Decision: Email is the linking attribute between IdPs and Keycloak users. If a user logs in with Google today and Azure AD tomorrow (same email), they get the same Keycloak user with the same permissions.

---

## Dynamic Client Registration (DCR)

External agents self-register via OAuth 2.0 DCR (RFC 7591):

```
POST /auth/realms/aiplex/clients-registrations/openid-connect
Authorization: Bearer <initial_access_token>
Content-Type: application/json

{
  "client_name": "external-coding-agent",
  "grant_types": ["client_credentials"],
  "token_endpoint_auth_method": "private_key_jwt",
  "jwks_uri": "https://coding-agent.example.com/.well-known/jwks.json",
  "scope": "mcp:tools:code_search mcp:tools:code_review"
}
```

- Initial access tokens are issued by AIPlex admins
- DCR creates the client with zero scopes (no ceiling)
- Admin must explicitly set Dimension A before the agent can do anything
- This prevents self-registration from granting any access

---

## Consent Management

### Consent Storage

Keycloak stores consent decisions per user-per-client:

```
User: student@school.edu
Client: tutor-agent
Consented scopes: [search_curriculum, generate_quiz, research, gemini-2.5-flash]
Consented at: 2026-04-05T10:00:00Z
```

### Consent Revocation

Users can revoke consent via the AIPlex Console:

```
AIPlex Console → Keycloak Account API
  DELETE /auth/realms/aiplex/account/applications/{client_id}/consent
```

After revocation, the agent's existing tokens remain valid until expiry (up to 1 hour), but refresh fails. For immediate revocation, AIPlex API can also call Keycloak Admin API to revoke all active sessions.

### Incremental Consent

If an agent needs a new scope mid-session:

1. Agent requests token with additional scope
2. Keycloak shows incremental consent screen (only the new scope)
3. User approves
4. New token issued with expanded scope set

---

## Keycloak Deployment

### Infrastructure

```yaml
# Keycloak on GKE with Cloud SQL
Helm chart: codecentric/keycloakx
Database: Cloud SQL PostgreSQL (HA, automated backups)
Replicas: 2 (HA within zone)
Cache: Infinispan (embedded, KUBE_PING discovery)
TLS: Cloud Service Mesh mTLS (no separate cert)
```

### Custom Theme

AIPlex-branded login and consent screens:

```
deploy/keycloak/theme/
├── login/
│   ├── theme.properties
│   ├── login.ftl          # Login form
│   ├── login-oauth-grant.ftl  # Consent screen
│   └── resources/
│       ├── css/aiplex.css
│       └── img/logo.svg
```

The consent screen is critical UX — it's where users understand what tools/agents/models they're granting access to. Human-readable descriptions come from the scope's `description` field (set during deploy).

---

## Edge Cases

### Token with no scopes
If A ∩ B ∩ C = ∅, the token is issued with an empty scope string. OPA will deny all non-discovery requests. The agent can still call `tools/list` to see what's available but cannot invoke anything.

### Agent requests scope outside its ceiling
Keycloak silently drops scopes that aren't in the client's optional scope list. The token is issued with whatever scopes survive the intersection. No error — the agent must check the token's actual scopes.

### User logs in from new IdP
If the email matches an existing user, Keycloak links the new IdP. If it's a new email, a new user is created with default (empty) permissions. Admin must assign role for Dimension B.

### Keycloak downtime
Existing JWTs remain valid (OPA validates locally using cached JWKS). No new tokens can be issued. Agents with refresh tokens will fail to refresh after access token expiry. Impact window = token TTL (1 hour).

### Clock skew
JWT validation allows 30-second clock skew (`nbf` and `exp` claims). Keycloak and OPA pods use GKE node time (synced via NTP). Cloud SQL and Firestore use Google-managed time.

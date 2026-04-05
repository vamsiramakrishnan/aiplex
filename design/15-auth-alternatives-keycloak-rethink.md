# 15 — Auth Alternatives: Rethinking Keycloak

## The Concern

Keycloak is a Java monolith (~500MB image, 1-2GB RAM, 10-30s startup). It's feature-rich but over-engineered for what AIPlex actually needs. In a system where every component is measured in milliseconds and megabytes, Keycloak is the elephant in the room.

**What AIPlex actually needs from auth:**
1. JWT issuance with custom claims (`act`, scopes)
2. Client (agent) registration + client credentials flow
3. User authentication (OIDC brokering to Google/Azure/Okta)
4. Consent management (user approves scopes per agent)
5. JWKS endpoint for OPA/ext_authz to validate tokens
6. Admin API for scope/resource/permission CRUD

**What Keycloak gives us that we DON'T need:**
- LDAP/Active Directory integration (we use OIDC brokering)
- SAML support
- Custom authentication flows (dozens of SPIs)
- Account management portal
- Fine-grained authorization services (we use OPA)
- WebAuthn/FIDO2 (nice-to-have, not critical)
- Session clustering (Infinispan)
- Custom themes engine
- ~200 REST admin API endpoints (we use ~15)

---

## Options Analysis

### Option 1: Keep Keycloak (Status Quo)

**Pros:**
- Battle-tested (10+ years, CNCF ecosystem)
- Every OAuth flow out of the box
- Identity brokering works immediately
- DCR (RFC 7591) built-in
- Admin console for debugging

**Cons:**
- 500MB+ image, 1-2GB RAM
- 10-30s cold start
- Java dependency (custom protocol mappers need Java SPI)
- Over-engineered for our use case
- Configuration complexity (realm export is 10K+ lines of JSON)
- Upgrade path is painful (major versions break configs)

**Verdict:** Works, but violates our "no over-engineering" principle.

---

### Option 2: Ory Stack (Hydra + Kratos + Oathkeeper)

**Ory Hydra** = OAuth 2.1 / OIDC server (Go, 30MB image, 50MB RAM)
**Ory Kratos** = Identity management (Go, 30MB image, 50MB RAM)
**Ory Oathkeeper** = Access proxy (Go, 20MB image — but we have Envoy+OPA)

```
                    ┌─────────┐
                    │ Ory     │
                    │ Hydra   │ ← OAuth 2.1 server (token issuance)
                    │ (Go)    │
                    └────┬────┘
                         │
               ┌─────────┼─────────┐
               ▼                    ▼
        ┌────────────┐      ┌────────────┐
        │ Ory Kratos │      │ AIPlex API │
        │ (user mgmt)│      │ (scope     │
        │            │      │  & agent   │
        └────────────┘      │  mgmt)     │
                            └────────────┘
```

**How it maps to AIPlex needs:**

| Need | Keycloak | Ory Hydra + Kratos |
|------|----------|-------------------|
| JWT issuance | Built-in | Hydra (native OAuth 2.1) |
| Custom claims (`act`) | Java SPI protocol mapper | Hydra webhook (no Java needed) |
| Client registration | Built-in DCR | Hydra Admin API |
| User authentication | Built-in | Kratos (self-service flows) |
| OIDC brokering | Built-in | Kratos social sign-in |
| Consent management | Built-in consent screen | Hydra consent flow (you build the UI) |
| JWKS endpoint | Built-in | Hydra (built-in) |
| Admin API | Keycloak Admin REST | Hydra Admin + Kratos Admin |
| Scope management | Client scopes entity | Hydra OAuth scopes |

**Pros:**
- Go-native (30MB images, 50MB RAM, < 1s startup)
- Cloud-native design (12-factor, K8s-first)
- OpenID certified
- Consent flow is a webhook — you own the UX completely
- Custom claims via token hook (no Java SPI)
- Simpler data model than Keycloak
- CNCF-adjacent (widely used in K8s ecosystem)

**Cons:**
- Two services instead of one (Hydra + Kratos)
- You must build the consent UI (but we already have the Console)
- No built-in admin console (but we have AIPlex Console)
- Ory Cloud is the commercial offering; open-source is maintained but prioritized lower
- Authorization services not included (but we use OPA)

**Resource comparison:**

| Metric | Keycloak | Ory Hydra + Kratos |
|--------|----------|-------------------|
| Total image size | ~500MB | ~60MB (30+30) |
| Total RAM | 1-2GB | 100-200MB |
| Cold start | 10-30s | < 1s |
| Language | Java | Go |
| Database | PostgreSQL (Cloud SQL) | PostgreSQL (same Cloud SQL) |

---

### Option 3: Dex + Custom Consent Service

**Dex** = Lightweight OIDC provider (Go, CNCF, 20MB image)

```
┌─────────┐
│   Dex   │ ← OIDC/OAuth, IdP brokering, JWT issuance
│  (Go)   │ ← K8s-native, CNCF project
└────┬────┘
     │
     ▼
┌─────────────────┐
│ AIPlex Auth Svc  │ ← Custom: consent, scopes, agent registration
│ (Go, lightweight)│ ← 100% tailored to AIPlex needs
└─────────────────┘
```

**Pros:**
- Dex is a CNCF sandbox project (used by ArgoCD, Kubernetes, Pomerium)
- Extremely lightweight (20MB, 30MB RAM)
- K8s-native (CRD-based connector config)
- Built-in OIDC brokering (Google, Azure, Okta, LDAP, GitHub)
- Static client config or custom connector

**Cons:**
- Dex is primarily an authenticator, not an authorization server
- No built-in consent flow
- No DCR (client registration is static or needs custom connector)
- No client credentials flow out of the box
- You'd need a custom OAuth server alongside Dex for agent auth
- Essentially building half of Keycloak yourself

**Verdict:** Too lightweight. Good for authentication-only scenarios (like ArgoCD login), but AIPlex needs full OAuth 2.1 with consent and client credentials.

---

### Option 4: Authelia

**Authelia** = Authentication and SSO portal (Go, 15MB image)

**Pros:** Very lightweight, good SSO portal
**Cons:** No OAuth 2.1 server, no consent flow, no client credentials, no DCR. Designed for reverse proxy auth (like Traefik/nginx), not API token issuance.

**Verdict:** Wrong tool. Authelia is an auth portal, not an OAuth server.

---

### Option 5: Zitadel

**Zitadel** = Identity management platform (Go, ~100MB image)

**Pros:**
- Go-native, cloud-native
- Full OAuth 2.1 / OIDC
- Built-in consent
- DCR support
- Modern API (gRPC + REST)
- Multi-tenancy built-in
- Actions (serverless hooks for custom claims)

**Cons:**
- Younger project than Keycloak/Ory (less battle-tested)
- CockroachDB or PostgreSQL required
- Smaller community
- Some features still maturing

**Resource comparison:**

| Metric | Keycloak | Zitadel | Ory Hydra+Kratos |
|--------|----------|---------|-----------------|
| Image size | ~500MB | ~100MB | ~60MB |
| RAM | 1-2GB | 200-400MB | 100-200MB |
| Cold start | 10-30s | 2-5s | < 1s |
| Language | Java | Go | Go |

---

### Option 6: Build Minimal Custom (on top of Go libraries)

Use Go OAuth libraries directly:

```go
// Build only what AIPlex needs:
// 1. JWT issuance (github.com/golang-jwt/jwt)
// 2. OIDC client (github.com/coreos/go-oidc)
// 3. PKCE flow (custom, ~200 lines)
// 4. Client credentials (custom, ~100 lines)
// 5. Consent management (custom, Firestore-backed)
// 6. JWKS endpoint (custom, ~50 lines)
```

**Pros:** Absolute minimum code, no dependencies, tiny footprint
**Cons:** Building an OAuth server is security-critical and easy to get wrong. Not recommended unless you're an auth expert.

**Verdict:** Too risky. OAuth is full of subtle security pitfalls.

---

## Recommendation: Ory Hydra + Kratos

### Why Ory Wins

1. **Right-sized.** Hydra is an OAuth 2.1 server. Kratos is identity management. Nothing more, nothing less. No Java, no Infinispan, no 200 unused SPIs.

2. **Go-native.** Same language as the AIPlex control plane. 30MB images. Sub-second startup. 50MB RAM. Fits the performance architecture.

3. **Consent is a webhook.** Hydra doesn't render consent screens — it calls YOUR endpoint. AIPlex Console already has the UI. This means the consent UX is 100% AIPlex-branded, not a Keycloak theme hack.

4. **Custom claims via token hook.** The `act` claim? No Java SPI. Hydra calls a webhook before issuing the token, and you return the custom claims:

```go
// AIPlex token hook handler
func tokenHook(w http.ResponseWriter, r *http.Request) {
    var req HydraTokenHookRequest
    json.NewDecoder(r.Body).Decode(&req)
    
    // Look up agent's SPIFFE ID
    spiffeID := getAgentSPIFFE(req.ClientID)
    
    // Return custom claims
    json.NewEncoder(w).Encode(HydraTokenHookResponse{
        Session: TokenSession{
            AccessToken: map[string]interface{}{
                "act": map[string]string{
                    "sub": spiffeID,
                },
            },
        },
    })
}
```

5. **K8s-native Helm charts.** Both Hydra and Kratos have production-ready Helm charts with PostgreSQL (same Cloud SQL we'd use for Keycloak).

6. **OpenID Certified.** Hydra is OpenID certified for all relevant profiles.

### Architecture with Ory

```
Agents / IDEs / CLIs
       │
       ▼
┌──────────────────────────────────────────────────────┐
│  Envoy AI Gateway                                     │
│  ext_authz → Rust authz (validates Hydra-issued JWTs) │
└───────────────────────┬──────────────────────────────┘
                        │
         ┌──────────────┼──────────────┐
         ▼              ▼              ▼
┌──────────────┐ ┌────────────┐ ┌────────────┐
│ Ory Hydra    │ │ Ory Kratos │ │ AIPlex API │
│ (OAuth 2.1)  │ │ (Identity) │ │ (Go)       │
│              │ │            │ │            │
│ • Token      │ │ • User     │ │ • Deploy   │
│   issuance   │ │   signup   │ │ • Catalog  │
│ • JWKS       │ │ • Login    │ │ • Consent  │
│ • DCR        │ │ • OIDC     │ │   handler  │
│ • Client     │ │   broker   │ │ • Token    │
│   credentials│ │ • MFA      │ │   hook     │
│ • PKCE       │ │ • Recovery │ │ • Scopes   │
│ • Device     │ │            │ │            │
│   grant      │ │            │ │            │
└──────────────┘ └────────────┘ └────────────┘
       │                              │
       └──────── Cloud SQL ───────────┘
```

### Key Difference: Consent Flow

**Keycloak consent:** Keycloak renders a themed consent page. You customize via FreeMarker templates. Limited control.

**Ory Hydra consent:** Hydra redirects to YOUR consent endpoint. AIPlex Console handles the UI:

```
1. Agent requests token with scopes
2. Hydra redirects to: https://aiplex.example.com/consent?challenge=xyz
3. AIPlex Console shows the consent screen (full React UI, AIPlex design)
4. User approves/denies scopes
5. Console calls AIPlex API → Hydra Admin API to accept/reject
6. Hydra issues token with approved scopes
```

This means the consent screen is a first-class part of the AIPlex Console, not a bolted-on Keycloak theme.

### Migration Path

If starting with Keycloak in Phase 1 (for speed), migration to Ory in Phase 2-3:

1. Ory Hydra uses the same JWKS format — OPA/ext_authz doesn't change
2. Agent credentials are re-issued (one-time migration script)
3. User identities move from Keycloak to Kratos (export/import)
4. Consent records migrate to AIPlex's Hydra consent handler
5. Scopes are recreated in Hydra (scripted, idempotent)

Alternatively, **start with Ory from Phase 1.** The Helm chart setup is comparable to Keycloak, and you avoid the migration.

---

## Updated Resource Comparison (Full Stack)

| Component | With Keycloak | With Ory |
|-----------|--------------|---------|
| Auth server | Keycloak: 500MB, 1.5GB RAM | Hydra: 30MB, 50MB RAM |
| Identity mgmt | Keycloak (same pod) | Kratos: 30MB, 50MB RAM |
| Custom claims | Java SPI JAR | Go webhook (in AIPlex API) |
| Consent UI | Keycloak theme (FreeMarker) | AIPlex Console (React) |
| Admin UI | Keycloak Admin Console | AIPlex Console |
| Database | Cloud SQL PostgreSQL | Cloud SQL PostgreSQL (same) |
| **Total auth footprint** | **~500MB image, ~1.5GB RAM** | **~60MB images, ~100MB RAM** |

**15x smaller. 15x less memory.** Same functionality for AIPlex's specific needs.

---

## What About SPIFFE/SPIRE for Service Auth?

For internal service-to-service authentication (not user-facing), we already have SPIFFE via GKE Managed Workload Identity. Some teams use SPIRE directly as the auth layer (bypassing OAuth entirely for internal traffic).

For AIPlex, the layering is:
- **SPIFFE/mTLS:** Infrastructure identity (which pod is this?)
- **Ory Hydra JWT:** Application identity (which user + which agent + what scopes?)
- **OPA/Rust ext_authz:** Policy enforcement (is this scope in the token?)

SPIFFE doesn't replace OAuth — they operate at different layers and both are needed.

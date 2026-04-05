# 12 — Security Model

## Overview

This document describes the threat model, defense layers, and security invariants of AIPlex. Security is not a layer — it's woven into the architecture at every level: network, identity, authorization, and audit.

---

## Defense-in-Depth Layers

```
Layer 1: Network (external)
  ├── GKE Gateway API: TLS termination, DDoS protection (Cloud Armor)
  ├── IAP (Phase 1): Google-managed identity check
  └── Rate limiting: Per-user request limits

Layer 2: Authentication
  ├── Ory Hydra: JWT issuance (OAuth 2.1, OIDC)
  ├── PKCE: No implicit grants, no client-side secrets
  └── WIF: Cross-cloud authentication without long-lived credentials

Layer 3: Authorization
  ├── OPA: JWT scope check (stateless, fail-closed)
  ├── Hydra + AIPlex API: Three-dimension permission model (A ∩ B ∩ C)
  └── Consent: User explicitly approves agent scope requests

Layer 4: Network (internal)
  ├── mTLS: All east-west traffic encrypted and authenticated
  ├── SPIFFE: Cryptographic workload identity
  ├── AuthorizationPolicy: Source identity check at mesh level
  └── NetworkPolicy: Pod-level ingress/egress control

Layer 5: Workload isolation
  ├── Separate namespaces: mcplex, a2aplex, llmplex
  ├── Per-pod security context: non-root, read-only FS, no capabilities
  ├── Resource limits: CPU/memory caps per pod
  └── GKE Autopilot: Node isolation, no SSH, no privileged containers

Layer 6: Audit
  ├── Access logs: Every request logged with user + agent identity
  ├── Deploy history: Append-only audit trail in Firestore
  ├── Traces: End-to-end request tracing across planes
  └── Policy denials: All denied requests logged with reason
```

---

## Threat Model

### Threat Actors

| Actor | Capability | Goal |
|-------|-----------|------|
| Malicious external agent | Has valid credentials (stolen or self-registered) | Access unauthorized tools/models, exfiltrate data |
| Compromised MCP server | Code execution within a pod | Lateral movement, access other services |
| Compromised A2A agent | Code execution within a pod | Escalate privileges, access unauthorized planes |
| Malicious insider (admin) | Hydra admin access | Grant unauthorized access, exfiltrate tokens |
| External attacker | Network access to public endpoint | DDoS, credential stuffing, token theft |
| Supply chain attack | Malicious container image in catalog | Code execution in cluster |

### Threat-Mitigation Matrix

| # | Threat | Impact | Likelihood | Mitigation |
|---|--------|--------|------------|------------|
| T1 | Stolen JWT used by unauthorized party | Unauthorized access | Medium | 1h token expiry, token binding to SPIFFE ID via `act` claim |
| T2 | Agent requests scopes beyond its ceiling | Privilege escalation | Low | Hydra silently drops unauthorized scopes at token issuance |
| T3 | MCP server makes outbound calls | Data exfiltration | Medium | NetworkPolicy denies all egress except DNS |
| T4 | MCP server calls other MCP servers | Lateral movement | Medium | Per-pod NetworkPolicy denies pod-to-pod traffic |
| T5 | A2A agent impersonates another agent | Identity spoofing | Low | mTLS with unique SPIFFE IDs; mesh verifies source identity |
| T6 | User consents to malicious scope | Unintended access | Medium | Consent screen shows human-readable descriptions; admin sets ceiling |
| T7 | Malicious image deployed via catalog | Code execution | Medium | SecurityContext restrictions; image scanning (future) |
| T8 | OPA bypass via malformed request | Authorization bypass | Low | Fail-closed policy; OPA denies if body unparseable |
| T9 | Hydra compromise | Full auth bypass | Low | Hydra on AlloyDB HA; RBAC for admin access; audit logs |
| T10 | Token replay across sessions | Session hijacking | Low | JTI claim for replay detection (future); short token TTL |
| T11 | Cost abuse via LLM calls | Financial damage | Medium | Per-user rate limits on LLMPlex; cost budgets (future) |
| T12 | Discovery enumeration | Information disclosure | Low | Discovery shows capability names, not data; rate limited |

---

## Security Invariants

These must always hold true:

1. **No request reaches a backend without passing OPA.** Envoy's ext_authz is applied to the Gateway, not individual routes. There is no path that bypasses OPA.

2. **No pod-to-pod communication bypasses mTLS.** PeerAuthentication STRICT mode on all namespaces. Plaintext connections are refused at the mesh level.

3. **No agent can access scopes outside A ∩ B ∩ C.** Hydra computes the intersection at token issuance. Even if OPA has a bug, the token itself cannot contain unauthorized scopes.

4. **No MCP server or A2A agent can reach any service except through Envoy.** AuthorizationPolicy + NetworkPolicy enforce this at two independent layers.

5. **Every request is traceable to a user AND an agent.** The JWT contains `sub` (user) and `act.sub` (agent SPIFFE ID). Audit logs record both.

6. **Deploy rollback leaves no orphaned resources with active access.** The deploy engine deletes Hydra scopes and resources before deleting K8s resources.

---

## Secrets Management

### Where Secrets Live

| Secret | Storage | Access |
|--------|---------|--------|
| Hydra DB credentials | Secret Manager | Hydra pod only (via KSA → GSA binding) |
| LLM provider API keys | Secret Manager → K8s Secret | Envoy pod only |
| Kratos DB credentials | Secret Manager | Kratos pod only |
| Agent client secrets | Hydra DB (encrypted) | Hydra Admin API only |
| Hydra signing keys | Hydra DB (encrypted) | Hydra process only |
| Firestore credentials | GKE Workload Identity | AIPlex API pod only (no key file) |

### No Secrets in:
- Firestore documents (config fields are non-sensitive)
- Environment variables of MCP servers / A2A agents (config is non-sensitive; secrets use K8s secrets)
- Git repository (no `.env` files, no hardcoded keys)
- Container images (secrets injected at runtime)

### Secret Rotation

| Secret | Rotation Frequency | Mechanism |
|--------|-------------------|-----------|
| SPIFFE certificates | 12 hours | Automatic (CAS) |
| Hydra signing keys | 90 days | Hydra key rotation (publishes both old + new) |
| LLM API keys | 90 days | Manual rotation in Secret Manager → K8s sync |
| Agent client secrets | On demand | Hydra Admin API |

---

## Container Security

### Pod SecurityContext

Every deployed MCP server and A2A agent runs with:

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 65534  # nobody
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities:
    drop: ["ALL"]
  seccompProfile:
    type: RuntimeDefault
```

### GKE Autopilot Restrictions

GKE Autopilot enforces additional restrictions:
- No privileged containers
- No host networking
- No host PID/IPC namespace
- No custom sysctls
- Resource limits mandatory

This means even if a pod's `securityContext` were misconfigured, GKE Autopilot would reject it.

---

## Network Security

### External Attack Surface

Only one public endpoint: the GKE Gateway API load balancer.

```
Internet → Cloud Armor (DDoS) → GKE Gateway (TLS) → Envoy AI Gateway
```

Everything else is internal:
- Hydra/Kratos: internal only (accessed via Envoy or cluster-internal)
- AIPlex API: internal only (accessed via Envoy)
- MCP servers: internal only
- A2A agents: internal only
- Firestore: VPC-internal + IAM
- AlloyDB: VPC-internal + IAM

### Cloud Armor Rules

```yaml
# Block common attack patterns
- priority: 1000
  action: deny(403)
  match:
    expr: "evaluatePreconfiguredExpr('sqli-stable')"

- priority: 1001
  action: deny(403)
  match:
    expr: "evaluatePreconfiguredExpr('xss-stable')"

- priority: 1002
  action: deny(403)
  match:
    expr: "evaluatePreconfiguredExpr('rce-stable')"

# Rate limit per IP
- priority: 2000
  action: rate_based_ban
  match:
    expr: "true"
  rateLimitOptions:
    rateLimitThreshold:
      count: 1000
      intervalSec: 60
    banThreshold:
      count: 5000
      intervalSec: 60
    banDurationSec: 600
```

---

## Supply Chain Security

### Container Images

| Source | Trust Level | Verification |
|--------|------------|-------------|
| Official MCP Registry | High (verified flag) | Image digest pinning |
| Google 1P servers | High | Google-signed images |
| MACH Registry | Medium | Community verification |
| Custom registries | Low | User responsibility |
| Local uploads | Low | Admin review required |

### Future: Binary Authorization

```yaml
# Require signed images for mcplex and a2aplex namespaces
apiVersion: binaryauthorization.googleapis.com/v1
kind: Policy
defaultAdmissionRule:
  evaluationMode: REQUIRE_ATTESTATION
  requireAttestationsBy:
    - projects/PROJECT/attestors/aiplex-verified
  enforcementMode: ENFORCED_BLOCK_AND_AUDIT_LOG
```

> Open: For v1, container images are not verified beyond what the source registry provides. Binary Authorization should be added in Phase 3 or later for high-security deployments.

---

## Incident Response

### Detection

| Signal | Source | Alert |
|--------|--------|-------|
| Unusual tool calls from an agent | Envoy metrics | Anomaly detection on `aiplex_tool_calls_total` |
| Policy denials spike | OPA logs | Alert on `aiplex_policy_denials_total` > threshold |
| Failed auth attempts | Hydra/Kratos logs | Alert on 401 rate increase |
| Unexpected egress traffic | NetworkPolicy logs | Alert on denied egress attempts |
| Pod restart loop | K8s events | Alert on CrashLoopBackOff |

### Response Playbook

**Compromised agent token:**
1. Revoke all agent sessions in Hydra
2. Rotate agent client secret
3. Review audit logs for unauthorized access
4. Notify affected users

**Compromised MCP server:**
1. Scale deployment to 0 (stop all pods)
2. Delete route CRD (remove from gateway)
3. Revoke scopes in Hydra
4. Investigate container image
5. Re-deploy from clean image after forensics

**Hydra compromise:**
1. Rotate all signing keys (forces all token re-issuance)
2. Invalidate all active sessions
3. Review admin audit logs
4. Restore from AlloyDB backup if DB is compromised

---

## Compliance Considerations

| Requirement | How AIPlex Addresses It |
|-------------|------------------------|
| Data encryption in transit | mTLS (internal) + TLS 1.3 (external) |
| Data encryption at rest | Firestore + AlloyDB encryption (Google-managed keys) |
| Access logging | Every request logged with user + agent identity |
| Principle of least privilege | Three-dimension scope model; agents get only what's needed |
| Identity management | Ory Kratos + Hydra with OIDC brokering to corporate IdPs |
| Incident detection | Real-time metrics + alerting on anomalies |
| Key management | Secret Manager + CAS; no plaintext keys in code or config |
| Network segmentation | Namespace isolation + NetworkPolicy + mesh AuthorizationPolicy |

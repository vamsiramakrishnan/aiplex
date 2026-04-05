---
sidebar_position: 6
title: Security Model
description: Defense-in-depth, threat model, and security invariants.
---

# Security Model

AIPlex implements defense-in-depth across network, authentication, authorization, encryption, and isolation layers.

## Defense-in-Depth Layers

```
Layer 1: Network     — GKE Gateway + IAP + Cloud Armor
Layer 2: TLS         — TLS 1.3 at ingress, mTLS east-west
Layer 3: AuthN       — JWT via Ory Hydra, SPIFFE via Cloud Service Mesh
Layer 4: AuthZ       — aiplex-authz (scope check), three-dimensional consent
Layer 5: Isolation   — Namespace separation, NetworkPolicy, AuthorizationPolicy
Layer 6: Audit       — OTel traces, deploy history, structured logging
```

## Security Invariants

These invariants must always hold:

1. **No request bypasses the policy engine** — ext_authz is in the critical path for every route
2. **No plaintext east-west traffic** — Cloud Service Mesh enforces strict mTLS
3. **No scope access outside A ∩ B ∩ C** — consent handler computes the intersection
4. **No pod-to-pod except through Envoy** — NetworkPolicy + AuthorizationPolicy
5. **No static service account keys** — WIF for external, SPIFFE for internal

## Threat Model

| Threat | Attack Vector | Mitigation |
|--------|--------------|------------|
| **Stolen JWT** | Token exfiltration | Short expiry (1h), audience validation, act claim ties to SPIFFE |
| **Privilege escalation** | Agent requests more scopes than allowed | Three-dim model: A ∩ B ∩ C computed at issuance |
| **Lateral movement** | Compromised pod reaches other namespaces | NetworkPolicy + mesh AuthorizationPolicy |
| **Data exfiltration** | MCP server leaks data | Per-tool scopes, audit logging, egress policies |
| **Identity spoofing** | Fake SPIFFE ID | CA Service-issued certs, mTLS verification |
| **Consent bypass** | Skip user approval | Hydra consent webhook is mandatory, not optional |
| **Token replay** | Reuse expired/revoked token | JWT expiry, token introspection for long sessions |
| **Supply chain** | Malicious MCP server image | Artifact Registry scanning, admission policies |
| **DDoS** | Overwhelm gateway | Cloud Armor, per-user rate limits, circuit breakers |
| **Key compromise** | API key leaked | Secret Manager, 90-day rotation, separate keys per provider |

## Container Security

All AIPlex pods run with:

```yaml
securityContext:
  runAsNonRoot: true
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities:
    drop: ["ALL"]
```

GKE Autopilot enforces additional restrictions (no privileged containers, no host networking).

## Secret Rotation

| Secret | Rotation Period | Mechanism |
|--------|----------------|-----------|
| SPIFFE certificates | 24 hours | CA Service automatic |
| Hydra signing keys | 90 days | Hydra key rotation |
| LLM API keys | 90 days | Secret Manager versioning |
| OAuth client secrets | On demand | Hydra client update |

## Incident Response

| Scenario | Response |
|----------|----------|
| Compromised MCP server | Delete instance (`aiplex rm`), revoke scopes, review audit logs |
| Stolen JWT | Rotate Hydra signing keys, short expiry limits blast radius |
| API key leaked | Rotate in Secret Manager, all new requests use new key |
| Suspicious delegation chain | Review `aiplex a2a delegations --chain`, revoke agent scopes |

## Next

- [Performance](/docs/architecture/performance) — Rust data path, Go control plane
- [Identity & Zero Trust](/docs/concepts/identity-zero-trust) — SPIFFE and mTLS details

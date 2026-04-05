---
sidebar_position: 4
title: Identity & Zero Trust
description: SPIFFE workload identity, mTLS, and Workload Identity Federation.
---

# Identity & Zero Trust

Every workload in AIPlex has a cryptographic identity. No network-level trust — every request is authenticated, authorized, and encrypted.

## SPIFFE Identity

Every pod gets a unique [SPIFFE](https://spiffe.io/) ID issued by GKE Managed Workload Identity:

```
Trust domain: aiplex-prod.global.PROJECT_NUMBER.workload.id.goog

aiplex-system:
  .../ns/aiplex-system/sa/aiplex-api
  .../ns/aiplex-system/sa/envoy-ai-gateway

mcplex:
  .../ns/mcplex/sa/knowledge-base-xyz
  .../ns/mcplex/sa/github-tools-abc

a2aplex:
  .../ns/a2aplex/sa/research-agent
  .../ns/a2aplex/sa/viz-agent
```

SPIFFE IDs are:
- **Automatically provisioned** when AIPlex deploys an instance
- **Cryptographically verified** via X.509 certificates
- **Rotated every 24 hours** by GKE's CA Service
- **Used for mTLS** between all services

## mTLS Everywhere

Cloud Service Mesh enforces **strict mTLS** across all namespaces:

```yaml
apiVersion: security.istio.io/v1
kind: PeerAuthentication
metadata:
  name: strict-mtls
  namespace: istio-system
spec:
  mtls:
    mode: STRICT
```

No plaintext east-west traffic. Every service-to-service call is encrypted and authenticated.

## Network Isolation

Each namespace has policies that restrict traffic:

1. **NetworkPolicy** — only Envoy AI Gateway can reach workloads
2. **AuthorizationPolicy** — validates the source SPIFFE identity
3. **Namespace separation** — mcplex, a2aplex, and llmplex are isolated

A compromised MCP server in `mcplex` cannot reach any pod in `a2aplex`.

## Bridging SPIFFE and OAuth

The `act` claim in the JWT bridges two identity systems:

| Layer | Identity System | Purpose |
|-------|----------------|---------|
| Infrastructure | SPIFFE (mTLS) | Pod-to-pod authentication |
| Application | OAuth 2.1 (JWT) | User and agent authorization |

AIPlex's token hook maps the OAuth client ID to its SPIFFE ID:

```json
{
  "sub": "student@school.edu",
  "azp": "tutor-agent",
  "act": {
    "sub": "spiffe://aiplex-prod.global.123.workload.id.goog/ns/a2aplex/sa/tutor-agent"
  }
}
```

Audit logs show both: which user authorized which agent to perform which action.

## External Agents (Workload Identity Federation)

Agents running outside GCP authenticate via [Workload Identity Federation](https://cloud.google.com/iam/docs/workload-identity-federation):

| Agent Location | Native Token | Exchange Path |
|----------------|-------------|---------------|
| AWS | IAM role credential | STS → GCP federated token → Hydra |
| Azure | Managed identity | STS → GCP federated token → Hydra |
| On-premises | OIDC from corporate IdP | STS → GCP federated token → Hydra |

No static service account keys. The external agent's native cloud identity is exchanged for an AIPlex JWT.

## Security Invariants

1. **No request bypasses the policy engine** — OPA/aiplex-authz is in the request path
2. **No plaintext east-west traffic** — Cloud Service Mesh strict mTLS
3. **No scope access outside A ∩ B ∩ C** — consent handler computes intersection
4. **No pod-to-pod except through Envoy** — NetworkPolicy + AuthorizationPolicy

## Next

- [Gateway Routing](/docs/concepts/gateway-routing) — how Envoy routes requests to the right plane
- [Security Model](/docs/architecture/security-model) — threat model and defense-in-depth

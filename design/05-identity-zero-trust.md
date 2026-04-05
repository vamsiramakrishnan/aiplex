# 05 — Identity & Zero Trust

## Overview

Every workload in AIPlex has a cryptographic identity (SPIFFE ID) issued by GKE's Managed Workload Identity. All east-west traffic is mTLS. External agents authenticate via Workload Identity Federation (WIF). This document covers the identity model, certificate lifecycle, and cross-cloud authentication.

---

## SPIFFE Identity Model

### Trust Domain

```
spiffe://aiplex-prod.global.{PROJECT_NUMBER}.workload.id.goog
```

This is a GKE-managed trust domain. Certificate Authority Service (CAS) issues the X.509-SVIDs.

### Identity Assignment

Every workload gets a SPIFFE ID derived from its Kubernetes service account:

```
Namespace: aiplex-system
  spiffe://.../ns/aiplex-system/sa/aiplex-api
  spiffe://.../ns/aiplex-system/sa/envoy-ai-gateway
  spiffe://.../ns/aiplex-system/sa/keycloak

Namespace: mcplex
  spiffe://.../ns/mcplex/sa/knowledge-base-xyz
  spiffe://.../ns/mcplex/sa/assessment-def
  spiffe://.../ns/mcplex/sa/progress-tracker-ghi

Namespace: a2aplex
  spiffe://.../ns/a2aplex/sa/research-agent
  spiffe://.../ns/a2aplex/sa/viz-agent
  spiffe://.../ns/a2aplex/sa/summarizer
```

### Identity Creation During Deploy

```python
# src/aiplex/deploy/identity.py

async def create_managed_identity(pool: str, namespace: str, id: str) -> str:
    """Create a GKE Managed Workload Identity for a deployed instance."""
    
    # 1. Create Kubernetes ServiceAccount
    sa = {
        "apiVersion": "v1",
        "kind": "ServiceAccount",
        "metadata": {
            "name": id,
            "namespace": namespace,
            "annotations": {
                "iam.gke.io/gcp-service-account": f"{id}@{PROJECT_ID}.iam.gserviceaccount.com"
            }
        }
    }
    await k8s_client.apply(sa)
    
    # 2. Create GCP Service Account (for external API access if needed)
    await iam_client.create_service_account(
        project=PROJECT_ID,
        account_id=id,
        display_name=f"AIPlex: {id}"
    )
    
    # 3. Bind KSA → GSA via Workload Identity
    await iam_client.set_iam_policy(
        resource=f"projects/{PROJECT_ID}/serviceAccounts/{id}@{PROJECT_ID}.iam.gserviceaccount.com",
        policy={
            "bindings": [{
                "role": "roles/iam.workloadIdentityUser",
                "members": [
                    f"serviceAccount:{PROJECT_ID}.svc.id.goog[{namespace}/{id}]"
                ]
            }]
        }
    )
    
    spiffe_id = f"spiffe://aiplex-prod.global.{PROJECT_NUMBER}.workload.id.goog/ns/{namespace}/sa/{id}"
    return spiffe_id
```

---

## mTLS Enforcement

### Strict PeerAuthentication

```yaml
apiVersion: security.istio.io/v1
kind: PeerAuthentication
metadata:
  name: strict-mtls
  namespace: aiplex-system  # Also applied to mcplex, a2aplex
spec:
  mtls:
    mode: STRICT
```

Applied to all three namespaces. No plaintext traffic allowed within the mesh.

### AuthorizationPolicy — Namespace Isolation

```yaml
# MCPlex: only Envoy can reach MCP servers
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: mcplex-ingress-only
  namespace: mcplex
spec:
  action: ALLOW
  rules:
    - from:
        - source:
            principals:
              - "spiffe://.../ns/aiplex-system/sa/envoy-ai-gateway"

---
# A2APlex: only Envoy can reach A2A agents
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: a2aplex-ingress-only
  namespace: a2aplex
spec:
  action: ALLOW
  rules:
    - from:
        - source:
            principals:
              - "spiffe://.../ns/aiplex-system/sa/envoy-ai-gateway"

---
# AIPlex system: only Envoy and internal services
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: aiplex-system-access
  namespace: aiplex-system
spec:
  action: ALLOW
  rules:
    - from:
        - source:
            namespaces: ["aiplex-system"]
    - from:
        - source:
            principals:
              - "spiffe://.../ns/aiplex-system/sa/envoy-ai-gateway"
```

### Per-Pod Network Policy

```yaml
# Each MCP server gets a NetworkPolicy that denies all egress
# except DNS and Envoy Gateway ingress
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: knowledge-base-xyz-netpol
  namespace: mcplex
spec:
  podSelector:
    matchLabels:
      app: knowledge-base-xyz
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: aiplex-system
          podSelector:
            matchLabels:
              app: envoy-ai-gateway
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system
          podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - protocol: UDP
          port: 53
    # Add specific egress rules if the MCP server needs external API access
```

---

## Certificate Lifecycle

### Automatic Rotation

GKE Managed Workload Identity handles certificate rotation automatically:

| Parameter | Value |
|-----------|-------|
| Certificate lifetime | 24 hours |
| Rotation trigger | 50% of lifetime (12 hours) |
| Rotation mechanism | SPIRE agent re-issues from CAS |
| Root CA rotation | Annual (managed by CAS) |

No application-level changes needed. The mesh sidecar handles certificate renewal transparently.

### Certificate Authority Service (CAS)

```
CA hierarchy:
  Root CA (CAS, HSM-backed, 10-year validity)
    └── Subordinate CA (CAS, 1-year validity)
          └── Workload certificates (24-hour validity, auto-rotated)
```

> Decision: GKE Managed Workload Identity + CAS instead of custom SPIRE deployment. GKE manages the SPIRE server, agent, and CAS integration. Zero operational burden for certificate management.

---

## Workload Identity Federation (WIF)

### What WIF Solves

External agents (running on AWS, Azure, on-prem) need to authenticate to AIPlex without long-lived credentials. WIF lets them exchange their native identity token for a GCP access token.

### Supported Providers

| Provider | Identity Token Source | WIF Pool Configuration |
|----------|----------------------|----------------------|
| AWS | EC2 instance metadata / EKS OIDC | AWS account ID + role ARN |
| Azure | Managed Identity / AKS OIDC | Azure AD tenant + app registration |
| On-prem | Custom OIDC IdP | IdP issuer URL + JWKS |
| GitHub Actions | OIDC token | GitHub org + repo |

### WIF Flow

```
External Agent (e.g., on AWS)
  │
  │  1. Get native identity token
  │     (AWS STS, Azure IMDS, GitHub OIDC)
  │
  │  2. Exchange for GCP federated token
  │     POST https://sts.googleapis.com/v1/token
  │     grant_type=urn:ietf:params:oauth:grant-type:token-exchange
  │     subject_token=<AWS/Azure/OIDC token>
  │     audience=//iam.googleapis.com/projects/{PROJECT_NUMBER}/locations/global/workloadIdentityPools/aiplex-prod/providers/{provider}
  │
  │  3. Use federated token to get Keycloak client credentials
  │     (WIF principal maps to a Keycloak client)
  │
  │  4. Get AIPlex JWT from Keycloak
  │     POST /auth/realms/aiplex/protocol/openid-connect/token
  │     grant_type=client_credentials
  │     client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer
  │     client_assertion=<GCP federated token>
  │
  │  5. Use AIPlex JWT for MCPlex/A2APlex/LLMPlex calls
  ▼
```

### WIF Pool Configuration (Terraform)

```hcl
# deploy/terraform/identity_pool.tf

resource "google_iam_workload_identity_pool" "aiplex" {
  workload_identity_pool_id = "aiplex-prod"
  display_name              = "AIPlex Agent Pool"
  description               = "Identity pool for external AI agents"
}

# AWS provider
resource "google_iam_workload_identity_pool_provider" "aws" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.aiplex.workload_identity_pool_id
  workload_identity_pool_provider_id = "aws-agents"
  display_name                       = "AWS Agents"

  aws {
    account_id = var.aws_account_id
  }

  attribute_mapping = {
    "google.subject"       = "assertion.arn"
    "attribute.aws_role"   = "assertion.arn.extract('assumed-role/{role}/')"
    "attribute.account_id" = "assertion.account"
  }

  attribute_condition = "assertion.arn.startsWith('arn:aws:sts::${var.aws_account_id}:assumed-role/aiplex-agent-')"
}

# Azure provider
resource "google_iam_workload_identity_pool_provider" "azure" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.aiplex.workload_identity_pool_id
  workload_identity_pool_provider_id = "azure-agents"
  display_name                       = "Azure Agents"

  oidc {
    issuer_uri = "https://login.microsoftonline.com/${var.azure_tenant_id}/v2.0"
  }

  attribute_mapping = {
    "google.subject"     = "assertion.sub"
    "attribute.tenant_id" = "assertion.tid"
    "attribute.app_id"   = "assertion.azp"
  }

  attribute_condition = "assertion.tid == '${var.azure_tenant_id}'"
}
```

### WIF Principal Validation

```python
# src/aiplex/access/wif.py

async def validate_wif_principal(principal: str) -> WIFIdentity:
    """Validate that a WIF principal is registered and authorized."""
    
    # Parse principal format
    # AWS: principalSet://iam.googleapis.com/projects/.../locations/global/workloadIdentityPools/aiplex-prod/attribute.aws_role/aiplex-agent-tutor
    # Azure: principal://iam.googleapis.com/projects/.../locations/global/workloadIdentityPools/aiplex-prod/subject/...
    
    parsed = parse_wif_principal(principal)
    
    # Verify pool matches
    if parsed.pool_id != "aiplex-prod":
        raise InvalidPrincipalError(f"Unknown pool: {parsed.pool_id}")
    
    # Verify provider is configured
    if parsed.provider_id not in ALLOWED_PROVIDERS:
        raise InvalidPrincipalError(f"Unknown provider: {parsed.provider_id}")
    
    # Check if this principal is registered as an agent in Keycloak
    client = await keycloak.find_client_by_attribute("wif_principal", principal)
    if not client:
        raise UnregisteredAgentError(f"Principal not registered: {principal}")
    
    return WIFIdentity(
        principal=principal,
        provider=parsed.provider_id,
        agent_id=client["clientId"],
        spiffe_equivalent=client.get("attributes", {}).get("spiffe_id")
    )
```

---

## The `act` Claim Bridge

The SPIFFE ID is embedded in the JWT's `act` claim, linking the cryptographic identity (SPIFFE) to the application-level identity (Keycloak client):

```json
{
  "sub": "student@school.edu",
  "azp": "tutor-agent",
  "act": {
    "sub": "spiffe://aiplex-prod.global.123456.workload.id.goog/ns/a2aplex/sa/tutor-agent"
  }
}
```

**Why both?**
- SPIFFE: infrastructure-level identity (mTLS, network policy). Verified by the mesh.
- Keycloak client: application-level identity (scopes, consent, audit). Verified by OPA.
- The `act` claim bridges them in audit logs: "user X's request, carried by agent Y (SPIFFE Z)"

---

## Threat Model

| Threat | Mitigation |
|--------|------------|
| Compromised MCP server impersonates another | Each server has unique SPIFFE ID; mTLS prevents impersonation |
| External agent uses stolen AWS credentials | WIF attribute conditions restrict to specific roles; Keycloak requires registered client |
| Man-in-the-middle between Gateway and backend | Strict mTLS; mesh sidecar on every pod |
| Lateral movement within namespace | Per-pod NetworkPolicy denies pod-to-pod traffic |
| Certificate theft from pod filesystem | 24-hour certificate lifetime limits window; in-memory only (tmpfs) |
| Root CA compromise | CAS uses HSM-backed keys; subordinate CA limits blast radius |

---

## Edge Cases

### Agent moves between clouds
If an agent migrates from AWS to Azure, it gets a new WIF principal. The AIPlex admin must register the new principal and deregister the old one. The Keycloak client (and its scopes) can be preserved.

### GKE node replacement
When GKE replaces a node, pods are rescheduled. New pods automatically get new certificates from the SPIRE agent (which runs as a DaemonSet). No manual intervention.

### Clock skew between clouds
WIF token exchange uses STS, which validates `exp` claims. External agents must have NTP-synced clocks (< 5 min skew). GKE nodes are always synced.

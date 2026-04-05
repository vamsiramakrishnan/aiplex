---
sidebar_position: 5
title: Platform Setup
description: Provision AIPlex infrastructure on GCP with a single command.
---

# Platform Setup

Set up the full AIPlex platform on GCP. One command, one GCP project, ~8 minutes.

## Prerequisites

- A GCP project with **Owner** role
- A browser (for OAuth login)

That's it. The CLI embeds Terraform, Helm, and kubectl — you don't install anything else.

## Setup

### 1. Login

```bash
aiplex login
```

Opens your browser for GCP authentication. Alternatively, if you have `gcloud` configured:

```bash
aiplex login --use-gcloud
```

### 2. Run Platform Setup

```bash
aiplex platform setup
```

The CLI asks three questions:

```
? GCP Project ID: my-project-123
? Region: us-central1
? Domain (for HTTPS): aiplex.example.com
```

Then it provisions everything:

```
Phase 1: Infrastructure (3-5 min)
  ✓ GKE Autopilot cluster
  ✓ Firestore database
  ✓ AlloyDB instance (for Ory Hydra/Kratos)
  ✓ Artifact Registry
  ✓ Cloud DNS zone

Phase 2: Platform Services (2-3 min)
  ✓ Ory Hydra (OAuth 2.1 server)
  ✓ Ory Kratos (identity management)
  ✓ AIPlex API
  ✓ AIPlex Console
  ✓ Envoy AI Gateway
  ✓ OPA policy engine

Phase 3: Security (1-2 min)
  ✓ Cloud Service Mesh (mTLS)
  ✓ Managed Workload Identity (SPIFFE)
  ✓ TLS certificates (Let's Encrypt)
  ✓ IAP configuration

✓ Platform ready at https://aiplex.example.com
  Console: https://aiplex.example.com/console
  API:     https://aiplex.example.com/api/v1
```

### 3. Verify

```bash
aiplex doctor
```

```
✓ GKE cluster healthy
✓ API server responding
✓ Hydra issuing tokens
✓ Kratos accepting logins
✓ OPA policy loaded
✓ Envoy routes configured
✓ mTLS enforced
✓ All checks passed
```

## What Gets Created

| GCP Resource | Purpose |
|-------------|---------|
| GKE Autopilot | Kubernetes cluster |
| Cloud Service Mesh | mTLS, service discovery |
| Firestore | Instance/template storage |
| AlloyDB | Ory Hydra/Kratos database |
| Artifact Registry | Container images |
| Cloud DNS | DNS records |
| Secret Manager | API keys, credentials |
| CA Service | SPIFFE certificate authority |

| K8s Namespace | Contents |
|--------------|----------|
| `aiplex-system` | API, Console, Hydra, Kratos, OPA |
| `mcplex` | MCP server instances |
| `a2aplex` | A2A agent instances |

## Resume on Failure

If setup fails partway through (network issue, quota limit):

```bash
aiplex platform setup --resume
```

The setup is idempotent — safe to re-run at any point.

## Upgrades

```bash
aiplex platform upgrade
```

Rolling updates, zero downtime. Backs up state before upgrading.

## Teardown

```bash
aiplex platform destroy
```

Removes all GCP resources. Requires confirmation.

## Advanced: Terraform Directly

For teams that manage infrastructure with Terraform:

```bash
cd deploy/terraform
terraform init
terraform plan
terraform apply
```

Then deploy platform services via Helm:

```bash
cd deploy/helm
helm install aiplex ./aiplex -n aiplex-system
```

See the [deploy/](https://github.com/vamsiramakrishnan/aiplex/tree/main/deploy) directory for all infrastructure-as-code.

## Next Steps

- [Quickstart](/docs/getting-started/quickstart) — deploy your first tool
- [Security Model](/docs/architecture/security-model) — understand the defense-in-depth approach
- [Observability](/docs/guides/observability) — set up monitoring and dashboards

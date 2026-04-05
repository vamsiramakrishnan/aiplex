---
sidebar_position: 4
title: Deploy Engine
description: How AIPlex provisions instances across all three planes.
---

# Deploy Engine

The deploy engine is the core of AIPlex. It handles the eight-phase deployment process that takes a template and config, and produces a running, governed instance.

## Eight-Phase Deploy

```
1. Generate ID     → instance-{template-slug}-{random}
2. Create Identity → SPIFFE identity (not for LLMPlex)
3. K8s Resources   → Deployment, Service, ServiceAccount, NetworkPolicy
4. Discover Caps   → tools/list (MCP), tasks/list (A2A), model_id (LLM)
5. Register Scopes → Create OAuth scopes in Hydra
6. Create Route    → MCPRoute / HTTPRoute / LLMRoute
7. Grant Access    → Owner gets all discovered scopes
8. Persist         → Write to Firestore
```

### Phase Details

**Phase 1: Generate ID**
```
Template: kb-search-server
Generated: kb-search-xyz123
```
DNS-safe, globally unique, human-readable prefix.

**Phase 2: Create Identity** (MCPlex, A2APlex only)
- Creates a GKE Managed Workload Identity
- Creates a Kubernetes ServiceAccount bound to the SPIFFE identity
- SPIFFE ID: `spiffe://trust-domain/ns/{namespace}/sa/{instance-id}`

**Phase 3: K8s Resources**
- Deployment with the template's container image
- Service for internal routing
- NetworkPolicy allowing only Envoy gateway ingress
- Resource limits from template spec

**Phase 4: Discover Capabilities**
- MCPlex: calls `tools/list` on the running MCP server
- A2APlex: calls `tasks/list` on the running A2A agent
- LLMPlex: uses the configured `model_id` directly

**Phase 5: Register Scopes**
- Creates OAuth scopes in Hydra for each discovered capability
- MCPlex: `mcp:tools:{tool_name}` for each tool
- A2APlex: `a2a:task:{task_type}` for each task
- LLMPlex: `llm:model:{model_id}`

**Phase 6: Create Route**
- Generates the appropriate Envoy route CRD
- Applies it to the cluster via server-side apply (idempotent)

**Phase 7: Grant Access**
- Adds all discovered scopes to the deploying user's permissions (dimension B)

**Phase 8: Persist**
- Writes the complete instance record to Firestore
- Appends to deploy_history (audit trail)

## Undeploy

Reverse of deploy, with cleanup:

```
1. Delete route CRD
2. Delete K8s resources (Deployment, Service, SA)
3. Delete SPIFFE identity
4. Remove scopes from Hydra
5. Update Firestore (status: terminated)
6. Append to deploy_history
```

## Rollback

If any phase fails, the engine runs **compensating transactions**:

```
Phase 5 failed (Hydra unreachable):
  → Delete route (if created)
  → Delete K8s resources
  → Delete identity
  → Mark as failed in Firestore
  → Log compensating actions
```

No distributed transactions needed. Each phase is independently reversible.

## Health Monitoring

After deploy, the engine monitors instance health:

| Check | Interval | What |
|-------|----------|------|
| K8s readiness probe | Continuous | Pod is ready to serve |
| K8s liveness probe | Continuous | Pod is alive |
| MCP ping | 60s | MCP server responds to ping |
| Route health | Envoy | Backend is reachable |

Status transitions: `provisioning` → `running` → `degraded` → `stopped` → `terminated`

## Idempotency

- K8s resources use **server-side apply** (SSA) — safe to re-apply
- Hydra scope registration is idempotent — creating an existing scope is a no-op
- Firestore writes use the instance ID as document key
- Deploy history is append-only

`aiplex apply -f aiplex.yaml` can be run repeatedly with the same result.

## Next

- [Data Model](/docs/architecture/data-model) — Firestore schema and consistency
- [Security Model](/docs/architecture/security-model) — threat model

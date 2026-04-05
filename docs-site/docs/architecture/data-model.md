---
sidebar_position: 5
title: Data Model
description: Firestore schema, collections, and consistency model.
---

# Data Model

AIPlex uses Firestore for persistent storage. Three collections, simple schemas, no transactions required.

## Collections

### `instances/{id}`

The primary record for every deployed tool, agent, or LLM route.

```json
{
  "id": "kb-search-xyz123",
  "plane": "mcplex",
  "template_id": "kb-search-server",
  "owner": "admin@school.edu",
  "namespace": "mcplex",
  "spiffe_id": "spiffe://aiplex-prod.global.123.workload.id.goog/ns/mcplex/sa/kb-search-xyz123",
  "scopes": [
    "mcp:tools:search_curriculum",
    "mcp:tools:get_document"
  ],
  "config": {
    "INDEX_PATH": "/data/curriculum"
  },
  "status": "running",
  "deployed_at": "2026-04-05T10:00:00Z",
  "updated_at": "2026-04-05T10:00:00Z"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique instance ID (generated) |
| `plane` | enum | `mcplex`, `a2aplex`, `llmplex` |
| `template_id` | string | Source template |
| `owner` | string | Deploying user |
| `namespace` | string | K8s namespace |
| `spiffe_id` | string | Workload identity |
| `scopes` | string[] | Discovered OAuth scopes |
| `config` | map | Instance configuration |
| `status` | enum | `provisioning`, `running`, `degraded`, `stopped`, `failed`, `terminated` |

### `templates/{id}`

Cached catalog entries from federated registries.

```json
{
  "id": "github-mcp-server",
  "plane": "mcplex",
  "name": "GitHub MCP Server",
  "description": "Search repos, read files, create issues",
  "image": "ghcr.io/modelcontextprotocol/github-server:latest",
  "source": "official-mcp-registry",
  "config_schema": { "type": "object", "properties": { "GITHUB_TOKEN": { "type": "string" } } },
  "resource_limits": { "cpu": "500m", "memory": "256Mi" },
  "cached_at": "2026-04-05T06:00:00Z"
}
```

Templates are cached with 24-hour staleness tolerance. The catalog aggregator refreshes from upstream registries periodically.

### `deploy_history/{auto-id}`

Append-only audit trail for all deployment actions.

```json
{
  "instance_id": "kb-search-xyz123",
  "action": "deploy",
  "actor": "admin@school.edu",
  "plane": "mcplex",
  "template_id": "kb-search-server",
  "config": { "INDEX_PATH": "/data/curriculum" },
  "scopes_registered": ["mcp:tools:search_curriculum", "mcp:tools:get_document"],
  "status": "success",
  "timestamp": "2026-04-05T10:00:00Z"
}
```

Retention: 1 year (TTL auto-delete after 365 days).

## What's NOT in Firestore

| Data | Stored In | Why |
|------|-----------|-----|
| OAuth clients (agents) | Ory Hydra | Hydra is the source of truth for OAuth |
| User identities | Ory Kratos | Kratos manages identity lifecycle |
| Consent records | Ory Hydra | Hydra tracks consent decisions |
| Agent ceiling (dim A) | Hydra `allowed_scopes` | Managed via Hydra client config |
| User ceiling (dim B) | Firestore (`user_scopes`) | AIPlex manages this |
| Effective permissions | JWT | Computed at token issuance |

## Indexes

```
instances: plane (ASC), status (ASC), deployed_at (DESC)
instances: owner (ASC), deployed_at (DESC)
templates: plane (ASC), source (ASC)
deploy_history: instance_id (ASC), timestamp (DESC)
deploy_history: actor (ASC), timestamp (DESC)
```

## Consistency Model

- Instance writes are single-document — no cross-document transactions needed
- Deploy engine's rollback handles partial failures via compensating transactions
- Template cache is eventually consistent (24h refresh cycle)
- Deploy history is append-only — no updates or deletes

## Next

- [Security Model](/docs/architecture/security-model) — threat model and defense-in-depth
- [Performance](/docs/architecture/performance) — why Rust for the data path

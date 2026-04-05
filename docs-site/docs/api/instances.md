---
sidebar_position: 3
title: Instances
description: API endpoints for deploying and managing instances.
---

# Instances API

## List Instances

```
GET /api/v1/instances
```

### Query Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `plane` | string | Filter: `mcplex`, `a2aplex`, `llmplex` |
| `status` | string | Filter: `running`, `degraded`, `stopped`, `failed` |
| `page` | int | Page number |
| `per_page` | int | Results per page |

### Response

```json
{
  "data": [
    {
      "id": "kb-search-xyz123",
      "plane": "mcplex",
      "template_id": "kb-search-server",
      "owner": "admin@school.edu",
      "scopes": ["mcp:tools:search_curriculum", "mcp:tools:get_document"],
      "status": "running",
      "endpoint": "https://aiplex.example.com/mcp/kb-search-xyz123",
      "deployed_at": "2026-04-05T10:00:00Z"
    }
  ],
  "meta": { "total": 5, "page": 1, "per_page": 20 }
}
```

## Get Instance

```
GET /api/v1/instances/{id}
```

Returns full instance details including config, SPIFFE ID, and namespace.

## Deploy Instance

```
POST /api/v1/instances
```

### Request Body

```json
{
  "template_id": "github-mcp-server",
  "name": "my-github-tools",
  "plane": "mcplex",
  "config": {
    "GITHUB_TOKEN": "ghp_..."
  }
}
```

### Response

```json
{
  "data": {
    "id": "my-github-tools",
    "plane": "mcplex",
    "status": "provisioning",
    "scopes": [],
    "endpoint": "https://aiplex.example.com/mcp/my-github-tools"
  },
  "message": "Deployment started"
}
```

Status transitions to `running` once all eight deploy phases complete.

## Undeploy Instance

```
DELETE /api/v1/instances/{id}
```

### Response

```json
{
  "message": "Instance my-github-tools terminated"
}
```

## Get Deploy History

```
GET /api/v1/instances/{id}/history
```

Returns the append-only audit trail for the instance.

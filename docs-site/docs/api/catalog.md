---
sidebar_position: 2
title: Catalog
description: API endpoints for browsing and managing templates.
---

# Catalog API

## List Templates

```
GET /api/v1/catalog/templates
```

### Query Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `plane` | string | Filter by plane: `mcplex`, `a2aplex`, `llmplex` |
| `search` | string | Search template names and descriptions |
| `source` | string | Filter by catalog source |
| `page` | int | Page number (default: 1) |
| `per_page` | int | Results per page (default: 20, max: 100) |

### Response

```json
{
  "data": [
    {
      "id": "github-mcp-server",
      "plane": "mcplex",
      "name": "GitHub MCP Server",
      "description": "Search repos, read files, create issues, manage PRs",
      "image": "ghcr.io/modelcontextprotocol/github-server:latest",
      "source": "official-mcp-registry",
      "config_schema": {
        "type": "object",
        "properties": {
          "GITHUB_TOKEN": { "type": "string", "description": "GitHub personal access token" }
        },
        "required": ["GITHUB_TOKEN"]
      },
      "resource_limits": { "cpu": "500m", "memory": "256Mi" }
    }
  ],
  "meta": { "total": 42, "page": 1, "per_page": 20 }
}
```

## Get Template

```
GET /api/v1/catalog/templates/{id}
```

### Response

```json
{
  "data": {
    "id": "github-mcp-server",
    "plane": "mcplex",
    "name": "GitHub MCP Server",
    "description": "Search repos, read files, create issues, manage PRs",
    "image": "ghcr.io/modelcontextprotocol/github-server:latest",
    "source": "official-mcp-registry",
    "config_schema": { ... },
    "resource_limits": { "cpu": "500m", "memory": "256Mi" }
  }
}
```

## Upload Template

```
POST /api/v1/catalog/templates
```

### Request Body

```json
{
  "id": "my-custom-server",
  "plane": "mcplex",
  "name": "My Custom Tools",
  "description": "Internal tools for our team",
  "image": "us-docker.pkg.dev/my-project/aiplex/my-server:latest",
  "config_schema": {
    "type": "object",
    "properties": {
      "API_KEY": { "type": "string" }
    },
    "required": ["API_KEY"]
  },
  "resource_limits": { "cpu": "500m", "memory": "256Mi" }
}
```

### Response

```json
{
  "data": { "id": "my-custom-server", ... },
  "message": "Template created"
}
```

---
sidebar_position: 1
title: API Overview
description: AIPlex REST API overview — authentication, versioning, and conventions.
---

# API Overview

The AIPlex API is a REST API built with Go (chi router). All endpoints are prefixed with `/api/v1/`.

## Base URL

```
https://aiplex.example.com/api/v1/
```

## Authentication

All requests require a Bearer token (JWT issued by Ory Hydra):

```bash
curl -H "Authorization: Bearer ${TOKEN}" \
  https://aiplex.example.com/api/v1/instances
```

## Response Format

All responses are JSON. Successful responses:

```json
{
  "data": { ... },
  "meta": {
    "total": 42,
    "page": 1,
    "per_page": 20
  }
}
```

Error responses:

```json
{
  "error": "FORBIDDEN",
  "message": "Scope mcp:tools:search not in token",
  "hint": "Request this scope during agent registration",
  "request_id": "req_abc123"
}
```

## Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| `UNAUTHORIZED` | 401 | Missing or invalid token |
| `FORBIDDEN` | 403 | Insufficient scopes |
| `NOT_FOUND` | 404 | Resource doesn't exist |
| `CONFLICT` | 409 | Resource already exists |
| `VALIDATION_ERROR` | 422 | Invalid request body |
| `DEPLOY_FAILED` | 500 | Deployment error |
| `RATE_LIMITED` | 429 | Too many requests |

## Rate Limits

| Endpoint Group | Limit |
|---------------|-------|
| Deploy/undeploy | 10/min |
| Catalog | 60/min |
| General | 200/min |

Rate limit headers included in responses:

```
X-RateLimit-Limit: 200
X-RateLimit-Remaining: 195
X-RateLimit-Reset: 1714900860
```

## Pagination

List endpoints support pagination:

```
GET /api/v1/instances?page=2&per_page=20
```

## Filtering

```
GET /api/v1/instances?plane=mcplex&status=running
GET /api/v1/catalog/templates?plane=mcplex&search=github
```

## Timestamps

All timestamps are ISO 8601 UTC:

```json
"deployed_at": "2026-04-05T10:00:00Z"
```

## Endpoints Summary

| Group | Endpoints |
|-------|-----------|
| [Catalog](/docs/api/catalog) | `GET /catalog/templates`, `GET /catalog/templates/{id}`, `POST /catalog/templates` |
| [Instances](/docs/api/instances) | `GET /instances`, `GET /instances/{id}`, `POST /instances`, `DELETE /instances/{id}` |
| [Agents](/docs/api/agents) | `GET /agents`, `GET /agents/{id}`, `POST /agents`, `DELETE /agents/{id}` |
| [Permissions](/docs/api/permissions) | `GET /users/{id}/scopes`, `PUT /users/{id}/scopes`, `GET /agents/{id}/permissions` |
| [LLM Routes](/docs/api/llm-routes) | `GET /llm/routes`, `PUT /llm/routes/{id}`, `DELETE /llm/routes/{id}` |
| [A2A](/docs/api/a2a) | `GET /a2a/cards`, `GET /a2a/delegations` |
| [Dashboard](/docs/api/dashboard) | `GET /dashboard/stats`, `GET /dashboard/denials` |
| [Auth](/docs/api/auth) | `GET /auth/consent`, `POST /auth/consent` |

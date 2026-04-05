# 11 — API Design

## Overview

The AIPlex API is the backend for the Console and the programmatic interface for automation. It's a FastAPI application exposing REST endpoints for catalog browsing, deployment, instance management, agent registration, and permissions.

---

## Base URL & Versioning

```
https://aiplex.example.com/api/v1/
```

- Version prefix in URL path (`v1`)
- Breaking changes → new version (`v2`)
- Non-breaking additions (new fields, new endpoints) within the same version

---

## Authentication

All API endpoints require a valid Keycloak JWT:

```
Authorization: Bearer <JWT>
```

The API validates the JWT locally (using Keycloak's JWKS) and extracts `sub` (user) and `azp` (client/agent) for authorization and audit.

Console users get their JWT from Keycloak's OIDC flow. Programmatic clients use client credentials.

---

## Endpoint Inventory

### Catalog

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `GET` | `/api/v1/catalog/{plane}` | Browse catalog for a plane | Any authenticated user |
| `GET` | `/api/v1/catalog/{plane}/{template_id}` | Get template details | Any authenticated user |
| `POST` | `/api/v1/catalog/{plane}/templates` | Upload custom template | Admin only |
| `DELETE` | `/api/v1/catalog/{plane}/templates/{id}` | Remove custom template | Admin + owner only |

**Query parameters for catalog browse:**

```
GET /api/v1/catalog/mcplex?query=search&category=rag&source=official-mcp&page=1&page_size=20
```

**Response:**
```json
{
  "templates": [
    {
      "id": "official-mcp:kb-search-server",
      "source": "official-mcp-registry",
      "plane": "mcplex",
      "name": "Knowledge Base Search",
      "description": "Search and retrieve documents from a knowledge base",
      "image": "ghcr.io/mcp-servers/kb-search:v2.1.0",
      "version": "v2.1.0",
      "category": "search",
      "tools": [
        {"name": "search_curriculum", "description": "Search curriculum documents"},
        {"name": "get_document", "description": "Retrieve a specific document"}
      ],
      "config_schema": { ... },
      "verified": true,
      "tags": ["search", "knowledge-base", "rag"]
    }
  ],
  "total": 42,
  "page": 1,
  "page_size": 20,
  "sources_queried": 5,
  "sources_failed": []
}
```

### Deploy & Instances

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `POST` | `/api/v1/deploy` | Deploy a template | Admin or authorized user |
| `GET` | `/api/v1/instances` | List instances | Any authenticated (filtered by ownership) |
| `GET` | `/api/v1/instances/{id}` | Get instance details | Owner or admin |
| `DELETE` | `/api/v1/instances/{id}` | Undeploy instance | Owner or admin |
| `PATCH` | `/api/v1/instances/{id}/config` | Update instance config | Owner or admin |
| `PATCH` | `/api/v1/instances/{id}/scale` | Scale instance replicas | Owner or admin |
| `POST` | `/api/v1/instances/{id}/restart` | Restart instance pods | Owner or admin |
| `GET` | `/api/v1/instances/{id}/health` | Get instance health | Any authenticated |
| `GET` | `/api/v1/instances/{id}/history` | Get deploy history | Owner or admin |

**Deploy request:**
```json
POST /api/v1/deploy
{
  "plane": "mcplex",
  "template_id": "official-mcp:kb-search-server",
  "config": {
    "project_id": "school-prod",
    "bucket": "curriculum-docs"
  },
  "display_name": "School Curriculum Search"
}
```

**Deploy response:**
```json
{
  "id": "kb-search-server-a1b2c3",
  "plane": "mcplex",
  "template_id": "official-mcp:kb-search-server",
  "owner": "admin@school.edu",
  "status": "provisioning",
  "scopes": [],
  "deployed_at": "2026-04-05T10:00:00Z",
  "url": "https://aiplex.example.com/mcp/kb-search-server-a1b2c3"
}
```

**Instance list with filters:**
```
GET /api/v1/instances?plane=mcplex&status=running&owner=admin@school.edu
```

### Agents

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `GET` | `/api/v1/agents` | List registered agents | Admin only |
| `GET` | `/api/v1/agents/{id}` | Get agent details (cross-plane view) | Admin only |
| `POST` | `/api/v1/agents` | Register new agent | Admin only |
| `DELETE` | `/api/v1/agents/{id}` | Deregister agent | Admin only |
| `GET` | `/api/v1/agents/{id}/permissions` | Get agent's effective permissions | Admin only |
| `PUT` | `/api/v1/agents/{id}/permissions` | Update agent ceiling (Dim A) | Admin only |

**Register agent:**
```json
POST /api/v1/agents
{
  "client_id": "tutor-agent",
  "display_name": "Tutor Agent",
  "auth_method": "client_credentials",
  "grant_types": ["client_credentials"],
  "wif_principal": "principalSet://iam.googleapis.com/projects/.../attribute.aws_role/aiplex-agent-tutor",
  "redirect_uris": [],
  "description": "AI tutor for K-12 students"
}
```

**Response:**
```json
{
  "client_id": "tutor-agent",
  "client_secret": "generated-secret-here",
  "display_name": "Tutor Agent",
  "auth_method": "client_credentials",
  "scopes": [],
  "registered_at": "2026-04-05T10:00:00Z"
}
```

**Cross-plane permissions view:**
```json
GET /api/v1/agents/tutor-agent/permissions
{
  "agent_id": "tutor-agent",
  "ceiling": {
    "mcplex": [
      {"scope": "mcp:tools:search_curriculum", "description": "Search curriculum"},
      {"scope": "mcp:tools:generate_quiz", "description": "Generate quizzes"}
    ],
    "a2aplex": [
      {"scope": "a2a:task:research", "description": "Delegate research tasks"}
    ],
    "llmplex": [
      {"scope": "llm:model:gemini-2.5-flash", "description": "Use Gemini Flash"}
    ]
  }
}
```

### Users & Permissions

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `GET` | `/api/v1/users` | List users | Admin only |
| `GET` | `/api/v1/users/{id}/permissions` | Get user ceiling (Dim B) | Admin or self |
| `PUT` | `/api/v1/users/{id}/permissions` | Update user ceiling (Dim B) | Admin only |
| `GET` | `/api/v1/users/{id}/consents` | List user's active consents (Dim C) | Admin or self |
| `DELETE` | `/api/v1/users/{id}/consents/{agent_id}` | Revoke consent | Admin or self |

### Subregistry (MCP Client Discovery)

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `GET` | `/v0.1/servers` | MCP Registry v0.1 compatible server list | Any authenticated |
| `GET` | `/a2a/{agent_id}/.well-known/agent.json` | A2A Agent Card | Any authenticated |

**MCP Subregistry response:**
```json
GET /v0.1/servers
{
  "servers": [
    {
      "id": "kb-search-server-a1b2c3",
      "name": "Knowledge Base Search",
      "url": "https://aiplex.example.com/mcp/kb-search-server-a1b2c3",
      "description": "Search and retrieve documents from a knowledge base",
      "tools": [
        {"name": "search_curriculum", "description": "Search curriculum documents"},
        {"name": "get_document", "description": "Retrieve a specific document"}
      ]
    }
  ]
}
```

### Dashboard & Metrics

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| `GET` | `/api/v1/dashboard/overview` | Summary metrics for all planes | Admin only |
| `GET` | `/api/v1/dashboard/costs` | LLM cost breakdown | Admin only |
| `GET` | `/api/v1/dashboard/denials` | Recent policy denials | Admin only |
| `GET` | `/api/v1/dashboard/sessions` | Active agent sessions | Admin only |

---

## FastAPI Implementation

```python
# src/aiplex/main.py

from fastapi import FastAPI, Depends, HTTPException
from fastapi.staticfiles import StaticFiles

app = FastAPI(
    title="AIPlex API",
    version="1.0.0",
    description="Unified control plane for AI agent interactions",
)

# Routes
app.include_router(catalog_router, prefix="/api/v1/catalog", tags=["catalog"])
app.include_router(deploy_router, prefix="/api/v1", tags=["deploy"])
app.include_router(instances_router, prefix="/api/v1/instances", tags=["instances"])
app.include_router(agents_router, prefix="/api/v1/agents", tags=["agents"])
app.include_router(users_router, prefix="/api/v1/users", tags=["users"])
app.include_router(dashboard_router, prefix="/api/v1/dashboard", tags=["dashboard"])
app.include_router(subregistry_router, tags=["subregistry"])

# Console static files (catch-all, must be last)
app.mount("/", StaticFiles(directory="console/static", html=True), name="console")


# Auth dependency
async def get_current_user(authorization: str = Header()) -> User:
    token = authorization.replace("Bearer ", "")
    try:
        claims = jwt.decode(token, jwks_client.get_signing_key_from_jwt(token).key,
                           algorithms=["RS256"], audience="aiplex",
                           issuer="https://aiplex.example.com/auth/realms/aiplex")
    except jwt.InvalidTokenError as e:
        raise HTTPException(401, detail=str(e))
    
    return User(
        sub=claims["sub"],
        azp=claims.get("azp"),
        roles=claims.get("realm_access", {}).get("roles", []),
        scopes=claims.get("scope", "").split(),
    )

async def require_admin(user: User = Depends(get_current_user)) -> User:
    if "admin" not in user.roles:
        raise HTTPException(403, detail="Admin role required")
    return user
```

---

## Error Handling

### Error Response Schema

```json
{
  "error": {
    "code": "DEPLOY_FAILED",
    "message": "Pod failed to become ready within 120s",
    "details": {
      "instance_id": "kb-search-server-a1b2c3",
      "reason": "ImagePullBackOff",
      "image": "ghcr.io/mcp-servers/kb-search:v99.0.0"
    },
    "request_id": "req_abc123",
    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736"
  }
}
```

### Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| `UNAUTHORIZED` | 401 | Missing or invalid JWT |
| `FORBIDDEN` | 403 | Valid JWT but insufficient role/scope |
| `NOT_FOUND` | 404 | Resource not found |
| `INVALID_CONFIG` | 400 | Deploy config doesn't match schema |
| `INVALID_PLANE` | 400 | Plane must be mcplex, a2aplex, or llmplex |
| `TEMPLATE_NOT_FOUND` | 404 | Template ID not in any catalog source |
| `DEPLOY_FAILED` | 500 | Deploy engine failed (with rollback) |
| `DEPLOY_TIMEOUT` | 504 | Pod didn't become ready in time |
| `INSTANCE_NOT_RUNNING` | 409 | Action requires running instance |
| `KEYCLOAK_ERROR` | 502 | Keycloak API call failed |
| `ALREADY_EXISTS` | 409 | Agent or resource already registered |

---

## Request/Response Conventions

- **Timestamps:** ISO 8601 UTC (`2026-04-05T10:00:00Z`)
- **IDs:** Lowercase alphanumeric + hyphens (`kb-search-server-a1b2c3`)
- **Pagination:** `page` (1-indexed) + `page_size` (default 50, max 200)
- **Filtering:** Query parameters (`?plane=mcplex&status=running`)
- **Sorting:** `?sort=deployed_at&order=desc` (default: most recent first)
- **Envelope:** Top-level is always the resource or `{"error": {...}}`
- **Empty lists:** Return `[]`, not null or 404

---

## Rate Limiting (API Level)

In addition to Envoy-level rate limiting, the API applies its own limits for expensive operations:

| Endpoint | Limit | Reason |
|----------|-------|--------|
| `POST /api/v1/deploy` | 10/min per user | Deploys are expensive (K8s + Keycloak) |
| `POST /api/v1/agents` | 5/min per user | Prevents agent registration spam |
| `GET /api/v1/catalog/*` | 60/min per user | External registry calls |
| All other endpoints | 200/min per user | General protection |

---

## OpenAPI Spec

FastAPI auto-generates OpenAPI 3.1 spec at `/api/v1/openapi.json`. The Console uses this for type generation:

```bash
# Generate TypeScript types from OpenAPI
npx openapi-typescript http://localhost:8000/api/v1/openapi.json -o console/src/types/api.ts
```

This ensures the Console's API client stays in sync with the backend.

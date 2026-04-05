---
sidebar_position: 4
title: Agents
description: API endpoints for registering and managing agents.
---

# Agents API

## List Agents

```
GET /api/v1/agents
```

### Response

```json
{
  "data": [
    {
      "id": "tutor-agent-a1b2c3",
      "name": "tutor-agent",
      "description": "AI tutor for student interactions",
      "client_id": "tutor-agent-a1b2c3",
      "scopes": ["mcp:tools:search_curriculum", "llm:model:gemini-2.5-flash"],
      "created_at": "2026-04-05T09:00:00Z"
    }
  ]
}
```

## Get Agent

```
GET /api/v1/agents/{id}
```

## Register Agent

```
POST /api/v1/agents
```

### Request Body

```json
{
  "name": "tutor-agent",
  "description": "AI tutor for student interactions",
  "scopes": [
    "mcp:tools:search_curriculum",
    "mcp:tools:generate_quiz",
    "a2a:task:research",
    "llm:model:gemini-2.5-flash"
  ]
}
```

### Response

```json
{
  "data": {
    "id": "tutor-agent-a1b2c3",
    "name": "tutor-agent",
    "client_id": "tutor-agent-a1b2c3",
    "client_secret": "ory_secret_...",
    "scopes": ["mcp:tools:search_curriculum", "mcp:tools:generate_quiz", "a2a:task:research", "llm:model:gemini-2.5-flash"],
    "token_url": "https://aiplex.example.com/oauth2/token"
  },
  "message": "Agent registered"
}
```

:::caution
The `client_secret` is only returned once at registration time. Store it securely.
:::

## Delete Agent

```
DELETE /api/v1/agents/{id}
```

## Get Agent Permissions

```
GET /api/v1/agents/{id}/permissions
```

Returns the cross-plane scope breakdown:

```json
{
  "data": {
    "mcplex": ["mcp:tools:search_curriculum", "mcp:tools:generate_quiz"],
    "a2aplex": ["a2a:task:research"],
    "llmplex": ["llm:model:gemini-2.5-flash"]
  }
}
```

---
sidebar_position: 7
title: A2A
description: API endpoints for A2A agent cards and delegations.
---

# A2A API

## List Agent Cards

```
GET /api/v1/a2a/cards
```

Returns A2A Agent Cards for all deployed A2A agents.

### Response

```json
{
  "data": [
    {
      "instance_id": "research-agent",
      "name": "research-agent",
      "description": "Research and summarize topics",
      "url": "https://aiplex.example.com/a2a/research-agent",
      "capabilities": {
        "tasks": ["research", "summarize", "fact-check"]
      }
    }
  ]
}
```

## Get Agent Card

```
GET /api/v1/a2a/cards/{instance_id}
```

## List Delegations

```
GET /api/v1/a2a/delegations
```

### Query Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `agent_id` | string | Filter by delegating agent |
| `target_id` | string | Filter by target agent |
| `task_type` | string | Filter by task type |

### Response

```json
{
  "data": [
    {
      "id": "del_abc123",
      "source_agent": "tutor-agent",
      "target_agent": "research-agent",
      "task_type": "research",
      "status": "completed",
      "created_at": "2026-04-05T10:30:00Z"
    }
  ]
}
```

## Get Delegation

```
GET /api/v1/a2a/delegations/{id}
```

## Get Delegation Chain

```
GET /api/v1/a2a/delegations/{id}/chain
```

Returns the full delegation chain (A → B → C):

```json
{
  "data": {
    "chain": [
      { "agent": "tutor-agent", "task": "research", "target": "research-agent" },
      { "agent": "research-agent", "task": "summarize", "target": "summarizer" }
    ]
  }
}
```

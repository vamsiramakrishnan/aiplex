---
sidebar_position: 8
title: Dashboard
description: API endpoints for metrics, stats, and policy denials.
---

# Dashboard API

## Get Stats

```
GET /api/v1/dashboard/stats
```

### Response

```json
{
  "data": {
    "total_instances": 12,
    "by_plane": {
      "mcplex": 7,
      "a2aplex": 3,
      "llmplex": 2
    },
    "by_status": {
      "running": 10,
      "degraded": 1,
      "stopped": 1
    },
    "total_agents": 5,
    "recent_denials": 3,
    "llm_cost_today_usd": 12.45,
    "requests_24h": {
      "mcplex": 4521,
      "a2aplex": 892,
      "llmplex": 1523
    }
  }
}
```

## List Policy Denials

```
GET /api/v1/dashboard/denials
```

### Query Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `plane` | string | Filter by plane |
| `agent_id` | string | Filter by agent |
| `period` | string | Time period: `1h`, `24h`, `7d` |

### Response

```json
{
  "data": [
    {
      "id": "deny_xyz789",
      "plane": "mcplex",
      "agent": "tutor-agent",
      "user": "student@school.edu",
      "action": "tools/call",
      "resource": "grade_assignment",
      "required_scope": "mcp:tools:grade_assignment",
      "reason": "scope_not_in_token",
      "timestamp": "2026-04-05T10:15:00Z"
    }
  ]
}
```

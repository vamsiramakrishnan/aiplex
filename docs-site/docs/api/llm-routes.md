---
sidebar_position: 6
title: LLM Routes
description: API endpoints for managing LLM routing, providers, and usage.
---

# LLM Routes API

## List Routes

```
GET /api/v1/llm/routes
```

### Response

```json
{
  "data": [
    {
      "name": "primary-models",
      "backends": [
        { "provider": "google", "model": "gemini-2.5-flash", "weight": 80 },
        { "provider": "anthropic", "model": "claude-sonnet-4-20250514", "weight": 20 }
      ],
      "fallback": [
        { "provider": "openai", "model": "gpt-4o" }
      ],
      "budget": {
        "daily_limit_usd": 50.00,
        "monthly_limit_usd": 1000.00
      }
    }
  ]
}
```

## Create/Update Route

```
PUT /api/v1/llm/routes/{name}
```

### Request Body

```json
{
  "backends": [
    { "provider": "google", "model": "gemini-2.5-flash", "weight": 80 },
    { "provider": "anthropic", "model": "claude-sonnet-4-20250514", "weight": 20 }
  ],
  "fallback": [
    { "provider": "openai", "model": "gpt-4o" }
  ],
  "budget": {
    "daily_limit_usd": 50.00,
    "monthly_limit_usd": 1000.00,
    "alert_threshold_percent": 80
  }
}
```

## Delete Route

```
DELETE /api/v1/llm/routes/{name}
```

## List Providers

```
GET /api/v1/llm/providers
```

## Record Usage

```
POST /api/v1/llm/usage
```

Called internally by Envoy to record token usage after each LLM request.

## Get Usage Summary

```
GET /api/v1/llm/usage
```

### Query Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `agent_id` | string | Filter by agent |
| `period` | string | Time period: `1d`, `7d`, `30d` |
| `provider` | string | Filter by provider |

### Response

```json
{
  "data": {
    "total_requests": 1523,
    "total_input_tokens": 2456000,
    "total_output_tokens": 891000,
    "total_cost_usd": 12.45,
    "by_provider": {
      "google": { "requests": 1200, "cost_usd": 8.40 },
      "anthropic": { "requests": 323, "cost_usd": 4.05 }
    }
  }
}
```

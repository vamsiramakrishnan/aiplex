---
sidebar_position: 3
title: "LLMPlex: Models"
description: Route model inference with failover, cost budgets, and access control.
---

# LLMPlex: Models

LLMPlex governs **Agent ↔ Model** interactions. Route inference requests to multiple providers with weighted distribution, automatic failover, cost tracking, and per-agent access control.

Unlike MCPlex and A2APlex, LLMPlex doesn't deploy pods. Envoy AI Gateway routes requests directly to provider APIs.

## Configure Providers

```yaml title="aiplex.yaml"
version: v1
routes:
  llm:
    - name: primary-models
      backends:
        - provider: google
          model: gemini-2.5-flash
          weight: 80
        - provider: anthropic
          model: claude-sonnet-4-20250514
          weight: 20
      fallback:
        - provider: openai
          model: gpt-4o
```

```bash
aiplex apply -f aiplex.yaml
```

### Supported Providers

| Provider | Models | Config |
|----------|--------|--------|
| Google | Gemini 2.5 Flash, Gemini 2.5 Pro | API key via Secret Manager |
| Anthropic | Claude Sonnet, Claude Opus | API key via Secret Manager |
| OpenAI | GPT-4o, GPT-4o-mini | API key via Secret Manager |
| AWS Bedrock | Claude, Titan, Llama | IAM role (WIF) |
| Ollama | Any local model | Endpoint URL |

## Weighted Routing

Distribute traffic across providers:

```yaml
backends:
  - provider: google
    model: gemini-2.5-flash
    weight: 80          # 80% of traffic
  - provider: anthropic
    model: claude-sonnet-4-20250514
    weight: 20          # 20% of traffic
```

## Automatic Failover

When a primary backend fails, traffic shifts to the fallback:

```yaml
backends:
  - provider: google
    model: gemini-2.5-flash
    weight: 100
fallback:
  - provider: anthropic
    model: claude-sonnet-4-20250514
  - provider: openai
    model: gpt-4o
```

Envoy handles failover automatically — circuit breaking, retry budgets, and health checks.

## Cost Budgets

Set spending limits per route, per agent, or globally:

```yaml
routes:
  llm:
    - name: primary-models
      backends:
        - provider: google
          model: gemini-2.5-flash
      budget:
        daily_limit_usd: 50.00
        monthly_limit_usd: 1000.00
        alert_threshold_percent: 80
```

### Per-Agent Budgets

```bash
# Set a daily budget for an agent
aiplex llm budget tutor-agent --daily 10.00 --monthly 200.00
```

When a budget is exceeded, requests are rejected with a clear error. Alert thresholds trigger notifications before hitting the hard limit.

## Access Control

```bash
# Grant model access to an agent
aiplex agents grant tutor-agent --scope llm:model:gemini-2.5-flash

# Grant capability-level access
aiplex agents grant tutor-agent --scope llm:capability:vision
```

## Making Requests

LLMPlex exposes an OpenAI-compatible endpoint:

```bash
curl -X POST https://aiplex.example.com/llm/v1/chat/completions \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -H "X-Model-Id: gemini-2.5-flash" \
  -d '{
    "messages": [
      {"role": "user", "content": "Explain projectile motion"}
    ],
    "model": "gemini-2.5-flash"
  }'
```

The `X-Model-Id` header tells the policy engine which model scope to check. Envoy routes to the configured backend.

## Usage Tracking

```bash
# View usage summary
aiplex llm usage --agent tutor-agent --period 7d

# View all LLM routes
aiplex llm routes

# Check provider health
aiplex llm providers
```

The Console dashboard shows real-time token usage, cost breakdown by provider, and budget utilization.

## Manage via CLI

```bash
# Add a new provider
aiplex llm provider add --name anthropic --api-key-secret anthropic-api-key

# Update routing weights
aiplex llm route update primary-models \
  --backend google:gemini-2.5-flash:60 \
  --backend anthropic:claude-sonnet-4-20250514:40

# Delete a route
aiplex llm route delete old-route
```

## Next

- [Agents](/docs/guides/agents) — register and manage agents across planes
- [Observability](/docs/guides/observability) — monitor LLM costs and usage

---
sidebar_position: 7
title: Observability
description: Monitor tool calls, agent delegations, model usage, costs, and policy denials.
---

# Observability

AIPlex provides unified observability across all three planes through OpenTelemetry, with metrics, traces, and logs flowing to Google Cloud Observability.

## Dashboard

The Console dashboard shows a unified view:

- **Tool calls** (MCPlex) — per-tool, per-agent, per-user
- **Agent delegations** (A2APlex) — delegation chains, task types
- **Model requests** (LLMPlex) — tokens, cost, latency by provider
- **Policy denials** — across all planes, with reasons
- **Active sessions** — concurrent agent connections

### CLI Dashboard

```bash
# Quick stats
aiplex dashboard

# Detailed view
aiplex dashboard --period 24h --plane mcplex
```

## Metrics

Key metrics exported via OTel:

| Metric | Plane | Description |
|--------|-------|-------------|
| `aiplex_requests_total` | All | Total requests by plane, status |
| `aiplex_tool_calls_total` | MCPlex | Tool invocations by tool name |
| `aiplex_a2a_delegations_total` | A2APlex | Task delegations by type |
| `aiplex_llm_requests_total` | LLMPlex | Model requests by provider |
| `aiplex_llm_input_tokens_total` | LLMPlex | Input tokens by model |
| `aiplex_llm_output_tokens_total` | LLMPlex | Output tokens by model |
| `aiplex_policy_denials_total` | All | Authorization failures by reason |
| `aiplex_request_duration_seconds` | All | Latency histogram |

## Cost Tracking

LLMPlex tracks token usage and computes costs per provider pricing:

```
cost = (input_tokens × price_per_input_token + output_tokens × price_per_output_token)
```

```bash
# View cost summary
aiplex llm usage --period 30d

# Per-agent breakdown
aiplex llm usage --agent tutor-agent --period 7d

# Set alerts
aiplex llm budget tutor-agent --daily 10.00 --alert-at 80
```

## Traces

Every request gets a W3C Trace Context header propagated across:

```
Client → Envoy → ext_authz → Backend (MCP/A2A/LLM)
```

View traces in Google Cloud Trace or any OTel-compatible backend.

## Logs

Structured JSON logs from all components:

```json
{
  "timestamp": "2026-04-05T10:30:00Z",
  "level": "info",
  "component": "envoy",
  "plane": "mcplex",
  "action": "tools/call",
  "tool": "search_curriculum",
  "user": "student@school.edu",
  "agent": "tutor-agent",
  "spiffe_id": "spiffe://.../sa/tutor-agent",
  "status": 200,
  "latency_ms": 45
}
```

```bash
# Stream logs for an instance
aiplex logs my-github-tools --follow

# Filter by level
aiplex logs my-github-tools --level error
```

## Alerts

| Severity | Condition |
|----------|-----------|
| Critical | All Envoy/OPA instances down |
| Critical | Error rate > 10% sustained 5min |
| Warning | Instance degraded health |
| Warning | p99 latency > 5s |
| Warning | LLM cost > 80% of daily budget |
| Info | New agent registered |
| Info | Permission change |

## Next

- [Declarative Config](/docs/guides/declarative-config) — manage everything as code
- [Architecture: Observability](/docs/architecture/overview) — system design details

# 10 — Observability

## Overview

AIPlex collects telemetry from three sources (Envoy AI Gateway, Cloud Service Mesh, AIPlex API), aggregates through a single OTel Collector, and exports to Google Cloud Observability (formerly Stackdriver). The Dashboard page in the Console provides a unified view.

---

## Telemetry Architecture

```
┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐
│  Envoy AI        │  │  Cloud Service   │  │  AIPlex API      │
│  Gateway         │  │  Mesh            │  │                  │
│                  │  │                  │  │                  │
│  • Request       │  │  • mTLS metrics  │  │  • Deploy events │
│    metrics       │  │  • Service       │  │  • Permission    │
│  • Access logs   │  │    topology      │  │    changes       │
│  • Traces        │  │  • L7 access     │  │  • Agent         │
│  • LLM token     │  │    logs          │  │    registrations │
│    counts        │  │                  │  │  • Custom        │
│                  │  │                  │  │    metrics       │
└────────┬─────────┘  └────────┬─────────┘  └────────┬─────────┘
         │                     │                      │
         │         OTLP/gRPC   │    OTLP/gRPC         │
         └─────────────────────┼──────────────────────┘
                               ▼
                 ┌──────────────────────┐
                 │  OTel Collector      │
                 │  (DaemonSet)         │
                 │                      │
                 │  Processors:         │
                 │  • Batch             │
                 │  • Attributes        │
                 │    (add plane label) │
                 │  • Filter            │
                 │    (drop health      │
                 │     checks)          │
                 └──────────┬───────────┘
                            │
                  ┌─────────┼──────────┐
                  ▼         ▼          ▼
         ┌──────────┐ ┌─────────┐ ┌──────────┐
         │ Cloud    │ │ Cloud   │ │ Cloud    │
         │ Monitoring│ │ Trace  │ │ Logging  │
         └──────────┘ └─────────┘ └──────────┘
```

---

## Metrics Taxonomy

### Envoy-Exported Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `aiplex_requests_total` | Counter | `plane`, `method`, `status`, `user` | Total requests per plane |
| `aiplex_tool_calls_total` | Counter | `server`, `tool`, `user`, `status` | MCPlex tool call count |
| `aiplex_a2a_delegations_total` | Counter | `source_agent`, `target_agent`, `task_type`, `status` | A2APlex delegation count |
| `aiplex_llm_requests_total` | Counter | `model`, `provider`, `user`, `status` | LLMPlex inference count |
| `aiplex_request_duration_seconds` | Histogram | `plane`, `method`, `status` | Request latency distribution |
| `aiplex_llm_input_tokens_total` | Counter | `model`, `provider`, `user` | LLM input token count |
| `aiplex_llm_output_tokens_total` | Counter | `model`, `provider`, `user` | LLM output token count |
| `aiplex_policy_denials_total` | Counter | `plane`, `reason`, `user`, `agent` | OPA policy denial count |
| `aiplex_rate_limited_total` | Counter | `plane`, `user` | Rate limit hits |

### AIPlex API Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `aiplex_deploys_total` | Counter | `plane`, `template`, `status` | Deploy operations |
| `aiplex_deploy_duration_seconds` | Histogram | `plane`, `template` | Deploy latency |
| `aiplex_undeploys_total` | Counter | `plane`, `reason` | Undeploy operations |
| `aiplex_instances_active` | Gauge | `plane`, `status` | Current instance count |
| `aiplex_permission_changes_total` | Counter | `dimension`, `plane` | Permission modifications |
| `aiplex_agent_registrations_total` | Counter | `auth_method` | Agent registrations |
| `aiplex_catalog_queries_total` | Counter | `plane`, `source` | Catalog searches |

### Mesh Metrics (Automatic)

| Metric | Source | Description |
|--------|--------|-------------|
| `istio_requests_total` | Cloud Service Mesh | All L7 requests with SPIFFE source/dest |
| `istio_tcp_connections_opened_total` | Cloud Service Mesh | mTLS connection count |
| `istio_request_duration_milliseconds` | Cloud Service Mesh | Service-to-service latency |

---

## OTel Collector Configuration

```yaml
# deploy/k8s/otel-collector.yaml

apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: otel-collector
  namespace: aiplex-system
spec:
  template:
    spec:
      containers:
        - name: otel-collector
          image: otel/opentelemetry-collector-contrib:0.96.0
          volumeMounts:
            - name: config
              mountPath: /etc/otelcol
      volumes:
        - name: config
          configMap:
            name: otel-collector-config
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: otel-collector-config
  namespace: aiplex-system
data:
  config.yaml: |
    receivers:
      otlp:
        protocols:
          grpc:
            endpoint: 0.0.0.0:4317
          http:
            endpoint: 0.0.0.0:4318

    processors:
      batch:
        timeout: 5s
        send_batch_size: 1024
      
      attributes:
        actions:
          - key: service.namespace
            value: aiplex
            action: upsert
      
      filter:
        metrics:
          exclude:
            match_type: strict
            metric_names:
              - envoy_health_check_*

    exporters:
      googlecloud:
        project: ${GCP_PROJECT_ID}
        metric:
          prefix: custom.googleapis.com/aiplex
        trace:
          attribute_mappings:
            - key: plane
              label: plane
            - key: user
              label: user
        log:
          default_log_name: aiplex

    service:
      pipelines:
        metrics:
          receivers: [otlp]
          processors: [batch, attributes, filter]
          exporters: [googlecloud]
        traces:
          receivers: [otlp]
          processors: [batch, attributes]
          exporters: [googlecloud]
        logs:
          receivers: [otlp]
          processors: [batch]
          exporters: [googlecloud]
```

---

## Trace Propagation

### End-to-End Trace

```
Agent request
  │
  ├── Span: envoy.ingress (Envoy Gateway)
  │     ├── Span: ext_authz.opa (OPA check)
  │     └── Span: envoy.upstream (to backend)
  │           └── Span: mesh.mtls (Service Mesh)
  │                 └── Span: mcp.tools_call (MCP server)
  │                       ├── Span: tool.search_curriculum
  │                       └── Span: tool.response
```

Trace context is propagated via W3C Trace Context headers (`traceparent`, `tracestate`). Envoy injects these if not present.

### Cross-Plane Tracing

When an A2A agent delegates to MCPlex tools, the trace continues:

```
Agent A → A2APlex → Agent B → MCPlex → Tool
  All under the same trace ID
  Each hop is a child span
```

This requires agents to forward trace context headers, which is documented in the agent SDK.

---

## Cost Tracking (LLMPlex)

### Token-Based Cost Calculation

```python
# Envoy AI Gateway exports token counts per request
# AIPlex API aggregates these into cost metrics

PRICING = {
    "gemini-2.5-flash": {"input": 0.15, "output": 0.60},     # per 1M tokens
    "gemini-2.5-pro":   {"input": 1.25, "output": 10.00},
    "claude-sonnet":    {"input": 3.00, "output": 15.00},
    "claude-opus":      {"input": 15.00, "output": 75.00},
    "gpt-4o":           {"input": 2.50, "output": 10.00},
}

def calculate_cost(model: str, input_tokens: int, output_tokens: int) -> float:
    prices = PRICING.get(model, {"input": 0, "output": 0})
    return (input_tokens * prices["input"] + output_tokens * prices["output"]) / 1_000_000
```

### Cost Dashboard Queries

```
# Total cost per user per day
sum(rate(aiplex_llm_input_tokens_total[1d])) by (user, model) * on(model) group_left PRICING_INPUT
+ sum(rate(aiplex_llm_output_tokens_total[1d])) by (user, model) * on(model) group_left PRICING_OUTPUT

# Cost by agent
sum(rate(aiplex_llm_input_tokens_total[1d])) by (agent, model) ...

# Cost trend (7-day rolling)
sum(increase(aiplex_llm_input_tokens_total[7d])) by (model) ...
```

### Cost Budgets (Future)

```python
# Per-user or per-agent cost limits
# Enforced by checking cumulative cost before allowing LLM requests
# Implementation: Envoy rate limiting with custom descriptor based on cost accumulator
```

> Open: Should cost budgets be enforced at the gateway level (Envoy) or the application level (AIPlex API middleware)? Gateway level is faster but harder to implement custom cost logic.

---

## Alerting Strategy

### Critical Alerts (Page)

| Alert | Condition | Severity |
|-------|-----------|----------|
| All Envoy replicas down | `up{job="envoy-ai-gateway"} == 0` for 1 min | Critical |
| OPA all instances down | `up{job="opa-ext-authz"} == 0` for 1 min | Critical |
| Keycloak down | `up{job="keycloak"} == 0` for 5 min | Critical |
| Error rate > 10% | `rate(aiplex_requests_total{status=~"5.."}[5m]) / rate(aiplex_requests_total[5m]) > 0.1` | Critical |

### Warning Alerts (Notify)

| Alert | Condition | Severity |
|-------|-----------|----------|
| Instance degraded | Instance health check failing for 5 min | Warning |
| High latency | p99 latency > 5s for 10 min | Warning |
| Rate limiting active | `rate(aiplex_rate_limited_total[5m]) > 1` | Warning |
| Deploy failure | `aiplex_deploys_total{status="failed"}` increase | Warning |
| LLM provider errors | Provider returning 5xx for 5 min | Warning |
| Cost spike | Daily cost > 2x 7-day average | Warning |

### Info Alerts (Dashboard Only)

| Alert | Condition |
|-------|-----------|
| Policy denial spike | `rate(aiplex_policy_denials_total[5m]) > 10` |
| New agent registered | `aiplex_agent_registrations_total` increase |
| Instance scaled | Replica count change |

---

## Dashboard Design

### Overview Cards

```
┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ Tool Calls   │ │ Delegations  │ │ LLM Requests │ │ Policy       │
│ 12,456/hr    │ │ 342/hr       │ │ 8,901/hr     │ │ Denials      │
│ ↑ 12%        │ │ ↑ 5%         │ │ ↓ 3%         │ │ 23/hr ↓ 15%  │
└──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘
```

### Request Flow Chart

```
Time series showing requests per plane over the last 24h
  ── MCPlex (blue)
  ── A2APlex (green)
  ── LLMPlex (purple)
```

### Cost Tracker

```
┌─────────────────────────────────────┐
│ LLM Cost — Today: $47.23           │
│ Budget: $100/day   [████████░░] 47% │
│                                     │
│ By Model:                           │
│   Gemini Flash    $12.30  (26%)     │
│   Claude Sonnet   $28.50  (60%)     │
│   GPT-4o          $6.43   (14%)     │
│                                     │
│ By User:                            │
│   tutor-agent     $31.20            │
│   research-agent  $16.03            │
└─────────────────────────────────────┘
```

### Policy Denial Viewer

```
┌─────────────────────────────────────────────────────────┐
│ Recent Policy Denials                                    │
│                                                          │
│ 10:05:23  student@school.edu / tutor-agent               │
│           DENIED: mcp:tools:modify_grades                │
│           Reason: Scope not in token                     │
│                                                          │
│ 10:04:11  unknown-agent                                  │
│           DENIED: Invalid JWT                            │
│           Reason: Token expired                          │
│                                                          │
│ 10:03:45  researcher@lab.edu / coding-agent              │
│           DENIED: llm:model:claude-opus                  │
│           Reason: Scope not in token (budget exceeded)   │
└─────────────────────────────────────────────────────────┘
```

---

## Structured Logging

### Log Format

```json
{
  "timestamp": "2026-04-05T10:05:23.456Z",
  "severity": "INFO",
  "message": "Tool call executed",
  "plane": "mcplex",
  "instance_id": "knowledge-base-xyz",
  "tool": "search_curriculum",
  "user": "student@school.edu",
  "agent": "tutor-agent",
  "duration_ms": 45,
  "status": "success",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7"
}
```

### Log Correlation

All logs include `trace_id`, enabling cross-component log correlation:

```
# Cloud Logging query: find all logs for a specific request
trace="projects/PROJECT/traces/4bf92f3577b34da6a3ce929d0e0e4736"
```

---

## Edge Cases

### High cardinality labels
User-based labels (`user`, `agent`) can create high cardinality. Mitigation: Cloud Monitoring handles this natively. For custom dashboards, aggregate by role or plane first.

### Missing trace context
If an agent doesn't forward trace context, Envoy generates a new trace. The upstream spans are orphaned. This is a documentation issue, not a system failure.

### OTel Collector backpressure
If Cloud Monitoring is slow, the batch processor queues up to 10,000 spans. Beyond that, spans are dropped (logged with `otelcol_exporter_send_failed_spans` metric). The collector itself is not a single point of failure — it runs as a DaemonSet.

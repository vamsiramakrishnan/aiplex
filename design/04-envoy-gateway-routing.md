# 04 — Envoy AI Gateway & Routing

## Overview

Envoy AI Gateway is the single ingress point for all three planes. It handles protocol-aware routing (MCPRoute, HTTPRoute, LLMRoute), delegates authorization to OPA, enforces rate limits, and exports telemetry. All route CRDs are generated dynamically by the AIPlex deploy engine.

---

## Gateway Resource

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: aiplex-gateway
  namespace: aiplex-system
  annotations:
    networking.gke.io/certmap: aiplex-cert-map
spec:
  gatewayClassName: gke-l7-global-external-managed
  listeners:
    - name: https
      protocol: HTTPS
      port: 443
      tls:
        mode: Terminate
        certificateRefs:
          - name: aiplex-tls-cert
    - name: http-redirect
      protocol: HTTP
      port: 80
      # Redirect all HTTP to HTTPS
  addresses:
    - type: Named
      value: aiplex-global-ip
```

> Decision: GKE Gateway API (not Istio Gateway). GKE Gateway integrates natively with Google Cloud Load Balancing, Certificate Manager, and Cloud Armor. The Envoy AI Gateway extends it with AI-specific CRDs (MCPRoute, LLMRoute).

---

## Route Types

### MCPRoute — Agent ↔ Tool

MCPRoute is an Envoy AI Gateway CRD that understands the MCP protocol (JSON-RPC over HTTP, SSE).

**Generated per MCP server deployment:**

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: MCPRoute
metadata:
  name: mcp-knowledge-base-xyz
  namespace: mcplex
  labels:
    aiplex.io/plane: mcplex
    aiplex.io/instance: knowledge-base-xyz
    aiplex.io/template: kb-search-server
spec:
  parentRefs:
    - name: aiplex-gateway
      namespace: aiplex-system
  path: "/mcp/knowledge-base-xyz"
  backendRefs:
    - name: knowledge-base-xyz
      namespace: mcplex
      path: "/mcp"
      port: 8080
  securityPolicy:
    oauth:
      issuer: "https://aiplex.example.com/auth/realms/aiplex"
```

**What MCPRoute handles:**
- Path rewriting: `/mcp/knowledge-base-xyz/mcp` → `/mcp` on the backend
- SSE support: configures Envoy for server-sent events (no response buffering)
- MCP session affinity: routes subsequent requests in an MCP session to the same backend pod
- Health checking: periodic MCP `ping` to verify server responsiveness

### HTTPRoute — Agent ↔ Agent (A2APlex)

Standard Kubernetes Gateway API HTTPRoute for A2A agent traffic:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: a2a-research-agent
  namespace: a2aplex
  labels:
    aiplex.io/plane: a2aplex
    aiplex.io/instance: research-agent
spec:
  parentRefs:
    - name: aiplex-gateway
      namespace: aiplex-system
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /a2a/research-agent
      backendRefs:
        - name: research-agent
          namespace: a2aplex
          port: 8080
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
      timeouts:
        request: 300s  # A2A tasks can be long-running
```

**A2A-specific considerations:**
- Longer timeouts: agent-to-agent delegation can involve multi-step reasoning
- No SSE (for v1): A2A uses request-response. Streaming delegation is a future feature.
- Agent Card discovery: `GET /a2a/research-agent/.well-known/agent.json` returns the A2A Agent Card

### LLMRoute — Agent ↔ Model

Envoy AI Gateway's native LLM routing with provider failover:

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: LLMRoute
metadata:
  name: llm-default
  namespace: aiplex-system
  labels:
    aiplex.io/plane: llmplex
spec:
  parentRefs:
    - name: aiplex-gateway
      namespace: aiplex-system
  rules:
    - matches:
        - headers:
            - name: x-model-id
              value: gemini-2.5-flash
      backendRefs:
        - name: gemini-flash-backend
          weight: 100
      fallback:
        - name: claude-haiku-backend
    - matches:
        - headers:
            - name: x-model-id
              value: claude-sonnet
      backendRefs:
        - name: claude-sonnet-backend
          weight: 100
      fallback:
        - name: gemini-pro-backend
    # Default rule: catch-all
    - backendRefs:
        - name: gemini-flash-backend
          weight: 80
        - name: claude-haiku-backend
          weight: 20
      fallback:
        - name: gpt-4o-mini-backend
```

**AIServiceBackend resources (one per provider):**

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: gemini-flash-backend
  namespace: aiplex-system
spec:
  provider:
    type: google
    google:
      model: gemini-2.5-flash
      apiVersion: v1
  backendRef:
    name: gemini-api  # ExternalName service pointing to generativelanguage.googleapis.com
  auth:
    apiKey:
      secretRef:
        name: gemini-api-key
        key: api-key
---
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: claude-sonnet-backend
  namespace: aiplex-system
spec:
  provider:
    type: anthropic
    anthropic:
      model: claude-sonnet-4-20250514
      apiVersion: "2024-06-01"
  auth:
    apiKey:
      secretRef:
        name: anthropic-api-key
        key: api-key
```

**LLMRoute features:**
- **Weighted routing:** Split traffic across providers (e.g., 80% Gemini / 20% Claude)
- **Automatic failover:** If primary returns 5xx or times out, fall through to next backend
- **Provider normalization:** Envoy translates between OpenAI-compatible format and provider-native formats
- **Semantic caching:** Identical prompts return cached responses (configurable TTL)
- **Token counting:** Envoy tracks input/output tokens per request for cost metering
- **Streaming:** Native support for SSE streaming responses from all providers

---

## Route Generation

The AIPlex deploy engine generates route CRDs dynamically:

```python
# src/aiplex/deploy/routes.py

async def apply_mcproute(instance_id: str, template: Template) -> None:
    route = {
        "apiVersion": "aigateway.envoyproxy.io/v1alpha1",
        "kind": "MCPRoute",
        "metadata": {
            "name": f"mcp-{instance_id}",
            "namespace": "mcplex",
            "labels": {
                "aiplex.io/plane": "mcplex",
                "aiplex.io/instance": instance_id,
                "aiplex.io/template": template.id,
            },
        },
        "spec": {
            "parentRefs": [{"name": "aiplex-gateway", "namespace": "aiplex-system"}],
            "path": f"/mcp/{instance_id}",
            "backendRefs": [
                {
                    "name": instance_id,
                    "namespace": "mcplex",
                    "path": "/mcp",
                    "port": 8080,
                }
            ],
        },
    }
    await k8s_client.apply(route)


async def apply_httproute(instance_id: str, template: Template) -> None:
    route = {
        "apiVersion": "gateway.networking.k8s.io/v1",
        "kind": "HTTPRoute",
        "metadata": {
            "name": f"a2a-{instance_id}",
            "namespace": "a2aplex",
            "labels": {
                "aiplex.io/plane": "a2aplex",
                "aiplex.io/instance": instance_id,
            },
        },
        "spec": {
            "parentRefs": [{"name": "aiplex-gateway", "namespace": "aiplex-system"}],
            "rules": [
                {
                    "matches": [{"path": {"type": "PathPrefix", "value": f"/a2a/{instance_id}"}}],
                    "backendRefs": [{"name": instance_id, "namespace": "a2aplex", "port": 8080}],
                    "filters": [
                        {
                            "type": "URLRewrite",
                            "urlRewrite": {"path": {"type": "ReplacePrefixMatch", "replacePrefixMatch": "/"}},
                        }
                    ],
                }
            ],
        },
    }
    await k8s_client.apply(route)


async def apply_llmroute(instance_id: str, template: Template) -> None:
    # LLMRoute updates are additive — add a new rule to the existing LLMRoute
    existing = await k8s_client.get("LLMRoute", "llm-default", "aiplex-system")
    
    new_rule = {
        "matches": [{"headers": [{"name": "x-model-id", "value": template.model_id}]}],
        "backendRefs": [{"name": f"{template.model_id}-backend", "weight": 100}],
    }
    
    if template.fallback_model_id:
        new_rule["fallback"] = [{"name": f"{template.fallback_model_id}-backend"}]
    
    existing["spec"]["rules"].insert(-1, new_rule)  # Before catch-all
    await k8s_client.apply(existing)
```

---

## Rate Limiting

### Per-User Rate Limits

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: global-rate-limit
  namespace: aiplex-system
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: aiplex-gateway
  rateLimit:
    type: Global
    global:
      rules:
        # Per-user overall limit
        - clientSelectors:
            - headers:
                - name: x-jwt-sub
                  type: Distinct
          limit:
            requests: 200
            unit: Minute

        # Per-user MCPlex limit (tool calls only)
        - clientSelectors:
            - headers:
                - name: x-jwt-sub
                  type: Distinct
                - name: ":path"
                  type: Prefix
                  value: "/mcp/"
          limit:
            requests: 100
            unit: Minute

        # Per-user LLMPlex limit (model calls — more restrictive for cost)
        - clientSelectors:
            - headers:
                - name: x-jwt-sub
                  type: Distinct
                - name: ":path"
                  type: Prefix
                  value: "/llm/"
          limit:
            requests: 30
            unit: Minute
```

> Decision: Rate limits are per-user (`x-jwt-sub`), not per-agent. A user running multiple agents shares one rate limit pool. This prevents circumventing limits by registering many agents. Per-agent limits can be added later if needed.

### Rate Limit Headers

Envoy returns standard rate limit headers:

```
X-RateLimit-Limit: 200
X-RateLimit-Remaining: 150
X-RateLimit-Reset: 1714897260
```

When rate limited:
```
HTTP/1.1 429 Too Many Requests
Retry-After: 30
X-RateLimit-Limit: 200
X-RateLimit-Remaining: 0
```

---

## Circuit Breaking

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: mcplex-circuit-breaker
  namespace: mcplex
spec:
  targetRefs:
    - group: aigateway.envoyproxy.io
      kind: MCPRoute
  circuitBreaker:
    maxConnections: 100
    maxPendingRequests: 50
    maxRequests: 100
    maxRetries: 3
  timeout:
    tcp:
      connectTimeout: 5s
    http:
      requestTimeout: 30s
      idleTimeout: 60s
  retry:
    numRetries: 2
    retryOn:
      - "5xx"
      - "reset"
      - "connect-failure"
    perRetry:
      timeout: 10s
      backOff:
        baseInterval: 100ms
        maxInterval: 1s
```

**Per-plane circuit breaker tuning:**

| Parameter | MCPlex | A2APlex | LLMPlex |
|-----------|--------|---------|---------|
| Request timeout | 30s | 300s | 120s |
| Max retries | 2 | 1 | 2 |
| Max connections per backend | 100 | 50 | 200 |
| Retry on | 5xx, reset | 5xx only | 5xx, reset |

> Decision: A2APlex has minimal retries because agent delegation is often non-idempotent. MCPlex and LLMPlex are typically idempotent reads, so retries are safe.

---

## TLS & Certificate Management

### External TLS (Client → Gateway)

```yaml
# Google-managed certificate
apiVersion: networking.gke.io/v1
kind: ManagedCertificate
metadata:
  name: aiplex-cert
  namespace: aiplex-system
spec:
  domains:
    - aiplex.example.com
    - "*.aiplex.example.com"
```

### Internal mTLS (Gateway → Backends)

Handled by Cloud Service Mesh automatically. No Envoy configuration needed — the mesh sidecar injects mTLS.

---

## Observability Integration

Envoy exports telemetry to the OTel Collector:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyProxy
metadata:
  name: aiplex-proxy-config
  namespace: aiplex-system
spec:
  telemetry:
    metrics:
      prometheus:
        enable: true
      sinks:
        - type: OpenTelemetry
          openTelemetry:
            host: otel-collector.aiplex-system.svc
            port: 4317
    accessLog:
      settings:
        - format:
            type: JSON
            json:
              start_time: "%START_TIME%"
              method: "%REQ(:METHOD)%"
              path: "%REQ(:PATH)%"
              response_code: "%RESPONSE_CODE%"
              duration: "%DURATION%"
              jwt_sub: "%REQ(x-jwt-sub)%"
              jwt_azp: "%REQ(x-jwt-azp)%"
              upstream: "%UPSTREAM_HOST%"
              trace_id: "%REQ(x-b3-traceid)%"
          sinks:
            - type: OpenTelemetry
              openTelemetry:
                host: otel-collector.aiplex-system.svc
                port: 4317
    tracing:
      provider:
        type: OpenTelemetry
        openTelemetry:
          host: otel-collector.aiplex-system.svc
          port: 4317
      customTags:
        plane:
          requestHeader:
            name: x-aiplex-plane
        user:
          requestHeader:
            name: x-jwt-sub
```

---

## Route Lifecycle

### Create (during deploy)

```
AIPlex API → deploy engine → routes.py → kubectl apply
  → Envoy picks up new route within seconds (watch-based)
  → New path is immediately routable
```

### Update (config change)

```
AIPlex API → deploy engine → routes.py → kubectl apply (patch)
  → Envoy hot-reloads route config (no downtime)
```

### Delete (during undeploy)

```
AIPlex API → deploy engine → routes.py → kubectl delete
  → Envoy removes route immediately
  → In-flight requests to that path get 404
```

> Decision: Route deletion is immediate, not graceful. If an MCP session is in progress when the server is undeployed, the session breaks. This is acceptable for v1 — the control plane should warn before undeploying servers with active connections (future enhancement).

---

## Edge Cases

### Route name collision
Instance IDs are generated with a random suffix (e.g., `knowledge-base-xyz`). Collision probability is negligible. If it occurs, the deploy fails at `kubectl apply` (409 Conflict) and the deploy engine retries with a new ID.

### Envoy Gateway restart
Route CRDs are persisted in etcd. When Envoy restarts, it re-reads all CRDs and rebuilds its routing table. Downtime during restart is mitigated by running multiple Envoy replicas behind the GKE load balancer.

### Path traversal
Envoy normalizes paths before routing (removes `..`, double slashes, etc.). A request to `/mcp/../a2a/research-agent` is normalized to `/a2a/research-agent` and routed to A2APlex, not MCPlex. OPA then checks A2A scopes, not MCP scopes.

### Large request bodies
MCPlex: tool call payloads are typically small (< 1KB). Max: 64KB (ext_authz limit).
A2APlex: task delegation payloads can be larger. Max: 1MB (configurable per route).
LLMPlex: prompt payloads can be very large. Max: 10MB (configurable). Note: ext_authz only sees the first 64KB, but the full body is forwarded to the backend.

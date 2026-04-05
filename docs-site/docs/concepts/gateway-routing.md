---
sidebar_position: 5
title: Gateway Routing
description: How Envoy AI Gateway routes requests across MCPlex, A2APlex, and LLMPlex.
---

# Gateway Routing

All traffic enters AIPlex through a single **Envoy AI Gateway**. It handles TLS termination, authentication, authorization, rate limiting, and routing to the correct plane.

## Route Types

Each plane uses a different Envoy route CRD:

| Plane | Route CRD | Path Pattern |
|-------|----------|--------------|
| MCPlex | `MCPRoute` | `/mcp/{instance-id}/*` |
| A2APlex | `HTTPRoute` | `/a2a/{instance-id}/*` |
| LLMPlex | `LLMRoute` | `/llm/*` |

### MCPRoute (MCP Protocol)

Handles JSON-RPC over HTTP and SSE (Server-Sent Events). Supports MCP session affinity.

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: MCPRoute
metadata:
  name: mcp-kb-search
  namespace: mcplex
spec:
  parentRefs:
    - name: aiplex-gateway
  path: "/mcp/kb-search"
  backendRefs:
    - name: kb-search
      path: "/mcp"
```

### HTTPRoute (A2A Protocol)

Standard HTTP routing for agent-to-agent communication.

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: a2a-research-agent
  namespace: a2aplex
spec:
  parentRefs:
    - name: aiplex-gateway
  rules:
    - matches:
        - path: { type: PathPrefix, value: /a2a/research-agent }
      backendRefs:
        - name: research-agent
          port: 8080
```

### LLMRoute (Model Inference)

Envoy AI Gateway's native LLM routing. Supports weighted distribution, automatic failover, and semantic caching.

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: LLMRoute
metadata:
  name: llm-route
spec:
  parentRefs:
    - name: aiplex-gateway
  rules:
    - backendRefs:
        - name: gemini-backend
          weight: 80
        - name: claude-backend
          weight: 20
      fallback:
        - name: gpt-backend
```

## Shared Security Policy

All planes share a single `ext_authz` policy:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: SecurityPolicy
metadata:
  name: aiplex-authz
spec:
  extAuth:
    grpc:
      backendRef: { name: opa-ext-authz, port: 9191 }
    withRequestBody: { maxRequestBytes: 65536 }
```

Every request — MCP tool call, A2A delegation, or LLM inference — passes through the same policy engine.

## Rate Limiting

Per-user rate limits enforced globally:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: global-rate-limit
spec:
  rateLimit:
    type: Global
    global:
      rules:
        - clientSelectors:
            - headers: [{ name: x-jwt-sub, type: Distinct }]
          limit: { requests: 200, unit: Minute }
```

Per-plane limits can be configured separately (e.g., 100 req/min for MCPlex, 30 req/min for LLMPlex).

## Request Flow

```
Client → Gateway (TLS) → ext_authz (JWT check) → Rate Limit → Backend (mTLS)
                                                                    │
                                    ┌───────────────────────────────┤
                                    │               │               │
                                  MCPlex         A2APlex         LLMPlex
                               (MCP server)   (A2A agent)    (Provider API)
```

Request overhead target: **< 20ms** total for auth + rate limit + mTLS.

## Next

- [MCPlex Guide](/docs/guides/mcplex) — deploy and manage MCP tools
- [Architecture: Request Flow](/docs/architecture/request-flow) — detailed request lifecycle

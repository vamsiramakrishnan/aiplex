# 01 — Architecture & Request Flow

## Overview

AIPlex is a unified control plane governing three interaction planes (MCPlex, A2APlex, LLMPlex) through a single gateway, auth stack, policy engine, and audit trail. This document traces the full lifecycle of a request from agent to backend and back.

---

## System Topology

```
                         Internet
                            │
                    ┌───────▼────────┐
                    │  Global HTTPS  │
                    │  Load Balancer │
                    │  (GKE Gateway  │
                    │   API + IAP)   │
                    └───────┬────────┘
                            │ TLS termination
                            │
                    ┌───────▼────────┐
                    │  Envoy AI      │
                    │  Gateway       │
                    │                │
                    │  • Route match │
                    │  • ext_authz   │
                    │  • Rate limit  │
                    │  • OTel export │
                    └───────┬────────┘
                            │ mTLS (Cloud Service Mesh)
                            │
              ┌─────────────┼─────────────┐
              │             │             │
      ┌───────▼──┐  ┌───────▼──┐  ┌───────▼──┐
      │ mcplex   │  │ a2aplex  │  │ llmplex  │
      │ namespace│  │ namespace│  │ namespace│
      │          │  │          │  │ (Envoy   │
      │ MCP      │  │ A2A      │  │  handles │
      │ servers  │  │ agents   │  │  directly)│
      └──────────┘  └──────────┘  └──────────┘
```

### Component Responsibilities

| Component | Responsibility | Failure Impact |
|-----------|---------------|----------------|
| GKE Gateway API | TLS termination, global routing, IAP enforcement | All traffic blocked |
| Envoy AI Gateway | Protocol-aware routing, auth delegation, rate limiting | All planes down |
| OPA sidecar | JWT scope validation | All authorized requests denied |
| Cloud Service Mesh | mTLS between all pods | East-west traffic fails |
| AIPlex API | Deploy, catalog, registry, access management | Control plane down, data plane unaffected |
| Keycloak | Token issuance, consent, identity brokering | No new tokens; existing tokens valid until expiry |
| Firestore | Instance metadata, templates, audit trail | Deploys fail; running instances unaffected |

---

## Request Flow: MCPlex Tool Call

This is the most common request path. An agent calls a tool on a deployed MCP server.

```
Agent (e.g., tutor-agent)
  │
  │  POST /mcp/knowledge-base-xyz/mcp
  │  Authorization: Bearer <JWT>
  │  Content-Type: application/json
  │  Body: {"jsonrpc":"2.0","method":"tools/call","params":{"name":"search_curriculum","arguments":{"query":"projectile motion"}}}
  │
  ▼
┌─────────────────────────────────────────────────────┐
│ Step 1: GKE Gateway API                              │
│                                                      │
│ • TLS termination                                    │
│ • IAP check (Phase 1 only, removed in Phase 2+)     │
│ • Forward to Envoy AI Gateway                        │
└──────────────────────┬──────────────────────────────┘
                       ▼
┌─────────────────────────────────────────────────────┐
│ Step 2: Envoy AI Gateway — Route Matching            │
│                                                      │
│ • Match path /mcp/knowledge-base-xyz against         │
│   MCPRoute CRDs                                      │
│ • Extract backend reference                          │
│ • Set x-forwarded headers, trace context             │
└──────────────────────┬──────────────────────────────┘
                       ▼
┌─────────────────────────────────────────────────────┐
│ Step 3: Envoy AI Gateway — ext_authz (OPA)           │
│                                                      │
│ • Send full request (headers + body) to OPA gRPC     │
│ • OPA decodes JWT, extracts scopes                   │
│ • OPA checks: "mcp:tools:search_curriculum" in       │
│   token scopes?                                      │
│ • Returns ALLOW or DENY (with 403 + reason)          │
│                                                      │
│ Latency budget: < 5ms (JWT decode is CPU-only)       │
└──────────────────────┬──────────────────────────────┘
                       ▼
┌─────────────────────────────────────────────────────┐
│ Step 4: Envoy AI Gateway — Rate Limiting             │
│                                                      │
│ • Extract x-jwt-sub from token claims                │
│ • Check against per-user rate limit                  │
│   (200 req/min default, configurable per plane)      │
│ • Return 429 if exceeded                             │
└──────────────────────┬──────────────────────────────┘
                       ▼
┌─────────────────────────────────────────────────────┐
│ Step 5: Cloud Service Mesh — mTLS                    │
│                                                      │
│ • Envoy sidecar initiates mTLS to backend            │
│ • Verify SPIFFE ID of knowledge-base-xyz             │
│ • AuthorizationPolicy check: source must be          │
│   envoy-ai-gateway SA                                │
│ • Establish encrypted channel                        │
└──────────────────────┬──────────────────────────────┘
                       ▼
┌─────────────────────────────────────────────────────┐
│ Step 6: MCP Server — Tool Execution                  │
│                                                      │
│ • Parse JSON-RPC request                             │
│ • Execute search_curriculum("projectile motion")     │
│ • Return JSON-RPC response                           │
└──────────────────────┬──────────────────────────────┘
                       ▼
Response flows back through the same chain (reverse)
```

### Timing Budget (p99 targets)

| Step | Target | Notes |
|------|--------|-------|
| GKE Gateway | < 2ms | TLS session reuse |
| Route matching | < 1ms | Static route table |
| OPA ext_authz | < 5ms | CPU-only JWT decode |
| Rate limit check | < 2ms | In-memory counters |
| mTLS handshake | < 10ms | Session resumption after first call |
| Tool execution | varies | Depends on MCP server implementation |
| **Overhead total** | **< 20ms** | Added by AIPlex infrastructure |

---

## Request Flow: A2APlex Task Delegation

```
Agent A (tutor-agent)
  │
  │  POST /a2a/research-agent
  │  Authorization: Bearer <JWT with a2a:task:research scope>
  │  Body: {"task_type":"research","input":{"topic":"projectile motion"}}
  │
  ▼
Same Steps 1-5 as MCPlex, but:
  • Route match uses HTTPRoute (not MCPRoute)
  • OPA checks: "a2a:task:research" in scopes
  • Backend is an A2A agent pod in a2aplex namespace
  │
  ▼
┌─────────────────────────────────────────────────────┐
│ A2A Agent (research-agent)                           │
│                                                      │
│ • Receives task                                      │
│ • May itself call MCPlex tools or LLMPlex models     │
│   using its OWN token (client_credentials grant)     │
│ • Returns task result                                │
└─────────────────────────────────────────────────────┘
```

### Chained Delegation

When Agent A delegates to Agent B, and Agent B needs to call tools or models:

1. Agent B uses its **own** client credentials token (not Agent A's token)
2. Agent B's token has its own scope ceiling (Dimension A)
3. Audit trail links: Agent A's request ID → Agent B's downstream calls
4. This prevents privilege escalation: Agent B cannot inherit Agent A's scopes

> Decision: No token forwarding between agents. Each agent authenticates independently. This is simpler and eliminates confused-deputy attacks at the cost of not supporting transitive user consent (acceptable for v1).

---

## Request Flow: LLMPlex Model Inference

```
Agent (tutor-agent)
  │
  │  POST /llm/v1/chat/completions
  │  Authorization: Bearer <JWT with llm:model:gemini-2.5-flash scope>
  │  x-model-id: gemini-2.5-flash
  │  Body: {"messages":[...]}
  │
  ▼
Same Steps 1-4 as MCPlex, but:
  • Route match uses LLMRoute
  • OPA checks: "llm:model:gemini-2.5-flash" in scopes
  • No Step 5 (mTLS to backend) — Envoy calls provider API directly
  │
  ▼
┌─────────────────────────────────────────────────────┐
│ Envoy AI Gateway — LLM Routing                       │
│                                                      │
│ • Weight-based backend selection (80/20 split)       │
│ • Attach provider API key from Secret Manager        │
│ • Semantic cache check (optional)                    │
│ • Forward to provider API (e.g., Gemini)             │
│ • If primary fails → automatic failover to fallback  │
│ • Stream response back to agent                      │
│ • Record token usage metrics                         │
└─────────────────────────────────────────────────────┘
```

> Decision: LLMPlex has no pods in the cluster. Envoy AI Gateway's native LLMRoute handles provider routing, API key injection, failover, and caching. This eliminates a proxy layer and reduces latency.

---

## Request Flow: Discovery (tools/list, agents/list, models/list)

Discovery requests are always allowed (no scope check) so agents can introspect available capabilities before requesting access.

```
Agent
  │
  │  POST /mcp/knowledge-base-xyz/mcp
  │  Authorization: Bearer <JWT>
  │  Body: {"jsonrpc":"2.0","method":"tools/list"}
  │
  ▼
OPA allows because method is in the discovery allowlist:
  {"initialize", "tools/list", "resources/list",
   "tasks/list", "agents/list", "models/list", "ping"}
```

> Open: Should discovery be rate-limited more aggressively than tool calls? An agent could enumerate all tools across all servers. Consider whether discovery should respect server-level scopes (`mcp:server:{id}`).

---

## SSE / Streaming Considerations

### MCP SSE Sessions

MCP supports Server-Sent Events for long-lived connections:

```
GET /mcp/knowledge-base-xyz/sse
Authorization: Bearer <JWT>
```

- Envoy must be configured for SSE passthrough (no response buffering)
- JWT validated once at connection establishment
- If token expires mid-session, the connection continues (token was valid at setup)
- Reconnection requires a fresh valid token

### LLM Streaming

LLM responses are typically streamed:

```
POST /llm/v1/chat/completions
Body: {"stream": true, ...}
```

- Envoy AI Gateway handles streaming natively for LLMRoute
- Token counting happens on the complete streamed response
- Failover mid-stream: connection drops, client must retry (no transparent failover for streaming)

---

## Error Handling

### Error Response Format

All error responses follow a consistent structure regardless of plane:

```json
{
  "error": {
    "code": "SCOPE_DENIED",
    "message": "Token missing required scope: mcp:tools:search_curriculum",
    "plane": "mcplex",
    "request_id": "req_abc123",
    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736"
  }
}
```

### Error Classification

| HTTP Status | Code | Meaning | Retryable |
|------------|------|---------|-----------|
| 401 | TOKEN_INVALID | JWT missing, malformed, or expired | No (re-auth needed) |
| 403 | SCOPE_DENIED | Valid JWT but missing required scope | No (request new consent) |
| 404 | INSTANCE_NOT_FOUND | MCP server / A2A agent not deployed | No |
| 429 | RATE_LIMITED | Per-user rate limit exceeded | Yes (with backoff) |
| 502 | BACKEND_UNAVAILABLE | MCP server / A2A agent not responding | Yes |
| 503 | ALL_BACKENDS_DOWN | LLMPlex: all providers failed | Yes (with backoff) |

---

## Namespace Isolation Model

```
┌─────────────────────────────────────────────┐
│ aiplex-system                                │
│ NetworkPolicy: allow from GKE Gateway        │
│ AuthzPolicy:   allow from envoy-ai-gateway   │
│                                              │
│ Pods: aiplex-api, keycloak, console          │
└──────────────────────────────────────────────┘

┌─────────────────────────────────────────────┐
│ mcplex                                       │
│ NetworkPolicy: allow from envoy-ai-gateway   │
│                DENY from a2aplex, llmplex     │
│ AuthzPolicy:   source SA = envoy-ai-gateway  │
│                                              │
│ Pods: individual MCP servers                 │
└──────────────────────────────────────────────┘

┌─────────────────────────────────────────────┐
│ a2aplex                                      │
│ NetworkPolicy: allow from envoy-ai-gateway   │
│                DENY from mcplex, llmplex      │
│ AuthzPolicy:   source SA = envoy-ai-gateway  │
│                                              │
│ Pods: individual A2A agents                  │
└──────────────────────────────────────────────┘
```

A compromised MCP server cannot:
- Call other MCP servers (no lateral movement within mcplex)
- Call A2A agents (cross-namespace denied)
- Call LLM providers (only Envoy has API keys)
- Call AIPlex API or Keycloak (cross-namespace denied)

> Decision: Each MCP server and A2A agent also gets a per-pod NetworkPolicy that denies egress to other pods in the same namespace. Lateral movement within a namespace is also blocked.

---

## Graceful Degradation

| Component Down | Impact | Mitigation |
|---------------|--------|------------|
| OPA sidecar | All requests denied (fail-closed) | OPA runs as DaemonSet; pod disruption budget = 0 |
| Keycloak | No new tokens; existing tokens work until expiry | Set token expiry to 1h; users can work during outage |
| AIPlex API | No deploys/unddeploys; running instances unaffected | Control plane / data plane separation |
| Firestore | No catalog browse, no deploy history | Catalog has in-memory cache; deploys queue for retry |
| Single MCP server | One tool unavailable | Other tools unaffected; client gets 502 for that server |
| LLM primary provider | Inference falls to secondary | LLMRoute failover is automatic and transparent |
| Cloud Service Mesh | mTLS enforcement lost | Fail-open concern; mitigated by OPA still checking JWTs |

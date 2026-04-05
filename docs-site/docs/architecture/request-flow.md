---
sidebar_position: 2
title: Request Flow
description: Step-by-step request lifecycle through AIPlex for all three planes.
---

# Request Flow

Every request to AIPlex follows the same pipeline, regardless of which plane it targets.

## Common Pipeline

```
1. TLS Termination (Gateway)
2. ext_authz (aiplex-authz / OPA)
   ├── Decode JWT
   ├── Verify issuer + signature
   ├── Extract scopes
   └── Match action → scope
3. Rate Limiting (per user, per plane)
4. Route to Backend (mTLS)
5. Backend Processing
6. Response (through Gateway)
7. Telemetry (OTel Collector → Cloud Observability)
```

**Total overhead target: < 20ms** for steps 1-4.

## MCPlex: Tool Call

```
Agent sends:
  POST /mcp/kb-search/mcp
  Authorization: Bearer <JWT>
  Body: {"jsonrpc":"2.0","method":"tools/call","params":{"name":"search_curriculum",...}}

Pipeline:
  1. Gateway → TLS termination
  2. ext_authz:
     - JWT valid? ✓
     - body.method == "tools/call" ✓
     - "mcp:tools:search_curriculum" in scopes? ✓
     - → ALLOW
  3. Rate limit: user under 200 req/min? ✓
  4. MCPRoute rewrites /mcp/kb-search/mcp → /mcp on backend
  5. mTLS to kb-search pod in mcplex namespace
  6. MCP server processes tool call
  7. Response back through gateway
  8. OTel: log tool call with user + agent identity
```

## A2APlex: Task Delegation

```
Agent sends:
  POST /a2a/research-agent
  Authorization: Bearer <JWT>
  Body: {"task_type":"research","input":{...}}

Pipeline:
  1. Gateway → TLS termination
  2. ext_authz:
     - JWT valid? ✓
     - path starts with /a2a/ ✓
     - "a2a:task:research" in scopes? ✓
     - → ALLOW
  3. Rate limit check ✓
  4. HTTPRoute forwards to research-agent:8080 in a2aplex namespace
  5. mTLS to research-agent pod
  6. A2A agent processes task
  7. Response back through gateway
  8. OTel: log delegation with chain context
```

## LLMPlex: Model Inference

```
Agent sends:
  POST /llm/v1/chat/completions
  Authorization: Bearer <JWT>
  X-Model-Id: gemini-2.5-flash
  Body: {"messages":[...],"model":"gemini-2.5-flash"}

Pipeline:
  1. Gateway → TLS termination
  2. ext_authz:
     - JWT valid? ✓
     - path starts with /llm/ ✓
     - header x-model-id = gemini-2.5-flash
     - "llm:model:gemini-2.5-flash" in scopes? ✓
     - → ALLOW
  3. Rate limit check ✓
  4. LLMRoute:
     - Weight: 80% Gemini, 20% Claude
     - Selected: Gemini (this request)
     - Budget check: under daily limit? ✓
  5. Envoy forwards to Google Gemini API
  6. Response with token counts
  7. OTel: log token usage, update cost tracking
  8. If Gemini fails → automatic failover to Claude or GPT
```

## Discovery (Always Allowed)

```
Agent sends:
  POST /mcp/kb-search/mcp
  Body: {"jsonrpc":"2.0","method":"tools/list"}

Pipeline:
  2. ext_authz:
     - body.method == "tools/list"
     - → ALLOW (discovery bypass)
```

Discovery methods (`initialize`, `tools/list`, `resources/list`, `tasks/list`, `agents/list`, `models/list`, `ping`) always pass authorization. Agents can discover what's available but can only use what their scopes allow.

## Error Handling

| Status | Meaning | Retryable |
|--------|---------|-----------|
| 401 | JWT missing, expired, or invalid | No (re-authenticate) |
| 403 | Scope not in token | No (request more scopes) |
| 429 | Rate limit exceeded | Yes (with backoff) |
| 502 | Backend unreachable | Yes (may trigger failover) |
| 503 | Circuit breaker open | Yes (after cooldown) |

All errors include structured JSON with error code and guidance:

```json
{
  "error": "FORBIDDEN",
  "message": "Scope mcp:tools:grade_assignment not in token",
  "hint": "Request this scope during agent registration or consent"
}
```

## Timing Budget

| Step | Target | Implementation |
|------|--------|---------------|
| TLS termination | 1ms | Hardware-accelerated at LB |
| ext_authz | 0.05ms (Rust), 1.2ms (OPA) | In-memory, no I/O |
| Rate limit | 1ms | Envoy native |
| mTLS handshake | 2ms | Cached session tickets |
| **Total overhead** | **< 5ms (Rust)** | Excludes backend processing |

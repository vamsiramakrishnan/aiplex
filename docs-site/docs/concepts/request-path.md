---
title: Request path
description: Where the managed API and the cluster gateway apply identity, routing and quota.
---

# Request path

## Managed API

`aiplex dev` runs the managed API. A team can expose models and tools through one access point. A native agent connection has its own invocation URL.

| Request | Entry point | What happens |
| --- | --- | --- |
| Generate a model response | e.g. `POST /ap/{id}/v1/chat/completions` | Admit the caller, resolve the model and check its grant. The model adapter handles the configured wire and reports observed usage. |
| Call a tool | `POST /ap/{id}/mcp` with `tools/call` | Resolve the exposed connection and tool, apply tool policy and call the upstream. Tool discovery lists the tools visible to the caller. |
| Invoke a native agent | `POST /api/v1/a2a/connections/{id}/invoke` | Check the grant for the connection and operation. A request-count binding can account for the exchange. The remote agent's internal work needs its own instrumentation. |
| Discover skills | `GET /ap/{id}/skills` | List the exposed manifests. Reported skill invocations have a separate audit history. |

For operations covered by [QuotaPlex](../guides/quotaplex.md), the API reserves exposure before dispatch. Trusted terminal evidence settles the attempt. An interrupted call can retain its hold. The [usage ledger](../library/usage-ledger.md) also runs with QuotaPlex disabled. [Order of checks](../library/request-lifecycle.md) · [Execution coverage](../library/quotaplex-execution-coverage.md)

## Cluster gateway

```text
Agents / IDEs / CLIs
       │
       ▼
┌──────────────────────────────┐
│  Envoy AI Gateway            │
│  /mcp/*  → MCPlex  (tools)   │
│  /a2a/*  → A2APlex (agents)  │
│  /llm/*  → LLMPlex (models)  │
│  ext_authz + rate limiting   │
└──────────────────────────────┘
       │ mTLS
       ▼
  K8s namespaces: mcplex, a2aplex, llmplex
```

In a cluster deployment Envoy uses `MCPRoute` for tools, `HTTPRoute` for agents and `LLMRoute` for models. These routes have their own scope authorization and rate limits. Traffic sent directly to Envoy or to a provider bypasses managed QuotaPlex accounting. [Authorization](../operations/authorization-policy.md) · [Install the platform](../getting-started/install-platform.md)

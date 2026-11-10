---
title: Request path
description: How a request travels from a caller through the gateway, authorization and the managed endpoints.
---

# Request path

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

Envoy AI Gateway routes by plane: `MCPRoute` for tools, `HTTPRoute` for agents and `LLMRoute` for models. One `ext_authz` check and one rate limiter apply across planes, and Cloud Service Mesh enforces mTLS between the gateway and the workloads.

The AIPlex API adds a second, managed path. Its `/ap/{id}/…` access-point endpoints and its managed model, tool and agent endpoints verify the caller, apply admission and, when QuotaPlex is enabled, quota before forwarding in the provider's native protocol. These calls write [usage ledger](../library/usage-ledger.md) records. Envoy routes and managed endpoints are separate paths; a request on one is not metered by the other.

Infrastructure: GKE Autopilot, Cloud Service Mesh, Firestore, AlloyDB and Secret Manager. See [Install the platform](../getting-started/install-platform.md) and [Operations](../operations/index.md).

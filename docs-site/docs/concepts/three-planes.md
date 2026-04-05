---
sidebar_position: 1
title: Three Planes
description: Understanding MCPlex, A2APlex, and LLMPlex — the three interaction planes governed by AIPlex.
---

# Three Planes

AIPlex governs three types of AI agent interactions. Each type is a **plane** — a dedicated namespace with its own protocol, route type, and scope format, unified by a single gateway and auth stack.

## The Planes

```
Agents / IDEs / CLIs
       │
       ▼
┌──────────────────────────────────┐
│  Envoy AI Gateway                │
│                                  │
│  /mcp/*  → MCPlex  (tools)       │
│  /a2a/*  → A2APlex (agents)      │
│  /llm/*  → LLMPlex (models)      │
│                                  │
│  Single auth + policy + audit    │
└──────────────────────────────────┘
```

### MCPlex — Agent ↔ Tool

**Protocol:** MCP (Model Context Protocol) over JSON-RPC + SSE

MCPlex governs how agents access tools. An MCP server exposes tools like `search_curriculum` or `create_issue`. AIPlex deploys these servers, discovers their tools, and creates per-tool OAuth scopes.

| What | Details |
|------|---------|
| Route type | `MCPRoute` |
| Scope format | `mcp:tools:{tool_name}` |
| K8s namespace | `mcplex` |
| Example | `mcp:tools:search_curriculum` |

**Example:** A tutor agent calls `search_curriculum` on a knowledge base MCP server. AIPlex verifies the agent's JWT includes `mcp:tools:search_curriculum`, rate-limits the request, and logs the tool call with both user and agent identity.

### A2APlex — Agent ↔ Agent

**Protocol:** A2A (Agent-to-Agent) over HTTP/JSON

A2APlex governs how agents delegate tasks to other agents. A research agent might accept `research` and `summarize` task types. AIPlex deploys these agents, discovers their capabilities via A2A Agent Cards, and creates per-task-type scopes.

| What | Details |
|------|---------|
| Route type | `HTTPRoute` |
| Scope format | `a2a:task:{task_type}` |
| K8s namespace | `a2aplex` |
| Example | `a2a:task:research` |

**Example:** A tutor agent delegates a research task to a research agent. AIPlex verifies `a2a:task:research` is in the token, ensures the delegation chain is audited, and the research agent gets its own scoped identity.

### LLMPlex — Agent ↔ Model

**Protocol:** Provider-specific APIs (OpenAI-compatible, Gemini, Bedrock)

LLMPlex governs how agents access language models. Unlike MCPlex and A2APlex, LLMPlex doesn't deploy pods — Envoy AI Gateway routes inference requests directly to providers with failover, load balancing, and cost tracking.

| What | Details |
|------|---------|
| Route type | `LLMRoute` + `AIServiceBackend` |
| Scope format | `llm:model:{model_id}` |
| K8s namespace | `llmplex` (no pods) |
| Example | `llm:model:gemini-2.5-flash` |

**Example:** A tutor agent requests `gemini-2.5-flash` for a response. AIPlex verifies `llm:model:gemini-2.5-flash`, routes to Google's API (with Claude as 20% traffic split and GPT-4o as fallback), tracks token usage against the agent's budget, and logs the request.

## One Gateway, One Token

All three planes share:

- **One Envoy AI Gateway** — single ingress, single TLS termination
- **One Ory Hydra** — single OAuth server, unified scope namespace
- **One OPA policy** — 20 lines of Rego covering all planes
- **One audit trail** — every request logged with user + agent identity

A single JWT carries scopes across all planes:

```json
{
  "sub": "student@school.edu",
  "azp": "tutor-agent",
  "act": { "sub": "spiffe://.../sa/tutor-agent" },
  "scope": "mcp:tools:search_curriculum a2a:task:research llm:model:gemini-2.5-flash"
}
```

No cross-service token exchange. No separate auth servers per plane.

## Namespace Isolation

Each plane runs in its own Kubernetes namespace with strict isolation:

- **Network policies** prevent pods in `mcplex` from reaching `a2aplex` or `llmplex`
- **Mesh AuthorizationPolicy** restricts ingress to Envoy AI Gateway only
- **SPIFFE identities** are scoped per namespace

A compromised MCP server can't reach A2A agents. A rogue A2A agent can't call models directly.

## Adding a Plane

Adding a new interaction type to AIPlex means:

1. Define a scope format (`newplane:resource:{name}`)
2. Add an `allow` rule to the OPA policy (~3 lines of Rego)
3. Create a K8s namespace
4. Add a route CRD type to the deploy engine
5. Add a tab to the Console

The auth, policy, identity, and audit infrastructure already exists. Each new plane is incremental.

## Next

- [Authentication](/docs/concepts/authentication) — how OAuth 2.1, Ory Hydra, and Ory Kratos work together
- [Scopes and Permissions](/docs/concepts/scopes-and-permissions) — the three-dimensional permission model

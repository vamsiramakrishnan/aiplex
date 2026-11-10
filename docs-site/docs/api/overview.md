---
title: API overview
description: Base paths, authentication, errors and the route groups of the AIPlex API.
---

# API overview

The AIPlex API is an HTTP/JSON service written in Go with the chi router. Management routes are under `/api/v1`. Client-facing routes (access-point model, MCP and skill endpoints, discovery) are at the server root.

## Authentication

Every `/api/v1` route requires a verified identity: a bearer token from a configured identity provider. Control-plane routes additionally require a role (for example admin for writes). In explicit development mode (`AIPLEX_DEV_MODE=true`, loopback listener only) requests proceed with development authentication.

```bash
curl -H "Authorization: Bearer $AIPLEX_TOKEN" https://aiplex.example.com/api/v1/instances
```

## Errors

Errors are JSON:

```json
{
  "code": "PUBLIC_URL_REQUIRED",
  "message": "...",
  "request_id": "..."
}
```

`code` and `message` are always present. `details`, `request_id` and `trace_id` appear when available. Updates to revisioned resources send the current revision (in `If-Match` for access points); a stale revision is rejected rather than overwritten.

## Route groups

| Prefix | Purpose | Details |
| --- | --- | --- |
| `/healthz`, `/readyz` | Liveness and readiness | |
| `/api/v1/catalog`, `/api/v1/instances`, `/healthz` and others | Template catalog, deployed instances, probes, dashboard | [Platform](./platform.md) |
| `/api/v1/agents` | Agent registration and permissions | [Identity: agents](./identity-agents.md) |
| `/api/v1/usage` | Usage ledger records, summary, prices | [Usage](./usage.md) |
| `/api/v1/access-points` | Access point management | [Access points](./access-points.md) |
| `/api/v1/overview` | Single pane: callers, access points, connect | [Overview pane](./overview-pane.md) |
| `/api/v1/iam` | Roles, role bindings, principal validation, native governance | [IAM](./iam.md), [Identity: agents](./identity-agents.md) |
| `/api/v1/llm` | Model surfaces, routes, providers, usage, batches | [Model surfaces](./model-surfaces.md), [Model batches](./model-batches.md), [LLM routes](./llm-routes.md) |
| `/api/v1/mcp/connections` | Tool connections and OAuth | [MCP connections](./mcp-connections.md) |
| `/api/v1/a2a`, `/api/v1/skills` | Agent cards, delegations, skill servers, invocations | [A2A connections](./a2a-connections.md), [Skills](./skills.md) |
| `/api/v1/clients` | ClientPlex registrations, destinations and plans | [Clients](./clients.md) |
| `/api/v1/network` | Deployment intents, plans and profiles | [Network](./network.md) |
| `/api/v1/dashboard` | Stats and policy denials | |
| `/api/v1/runs` | Tape-backed run projection and operator actions | [Runs](./runs.md), [Durable runs](../guides/runs.md) |
| `/api/v1/modules` | Enabled modules and their contracts | |
| `/api/v1/quota` | QuotaPlex inspection and administration (opt-in) | [Quota](./quota.md) |
| `/auth/consent`, `/auth/token-hook` | Hydra consent and token hook webhooks | |
| `/events/stream` | Server-sent events for the Console | |
| `/v0.1/servers` | MCP registry listing (MCPlex) | |
| `/a2a/{instanceId}/.well-known/agent.json` | Agent Card (A2APlex) | |
| `/skills/{instanceId}/.well-known/skills.json` | Skills manifest (SkillsPlex) | |
| `/ap/{id}/…` | Access-point endpoints | [Access points](../library/access-points.md#routes) |

The Go [SDK](../reference/sdk.md) wraps these routes.

---
slug: /
title: Overview
description: Choose a first task, identify the route your request takes, and find its policy and accounting contract.
---

# AIPlex documentation

Connect agents, models, tools and harnesses with AIPlex. [Start the local stack](./getting-started/local-development.md) to try each one without a cloud account.

| What do you want to do? | Where the request or configuration goes |
| --- | --- |
| [Invoke an agent](./guides/a2aplex.md) | Call a saved agent connection at `/api/v1/a2a/connections/{id}/invoke`. To deploy an A2A agent behind Envoy, use a cluster. |
| [Call a model](./guides/llmplex.md) | Give the SDK a team access point. AIPlex forwards the model's native protocol and records the usage the provider returns. |
| [Run a tool](./guides/mcplex.md) | Connect an MCP server. Expose tools at `/ap/{id}/mcp` and check each call against the tool policy. |
| [Connect a harness](./getting-started/connect-client.md) | Generate settings for Codex, Claude Code or Gemini CLI. The harness runs its own loop and sends requests to the configured endpoints. |

## Manage access and spend

- [Create a team access point](./guides/access-points.md) to choose which callers can reach its models, MCP tools and skill manifests.
- [Set a shared budget](./guides/quotaplex.md) to cap spend across supported managed calls. Try a model and a tool against the same allowance.
- [Inspect usage](./library/usage-ledger.md) to find who made a call, what it consumed and which costs are known.
- [Follow a request](./concepts/request-path.md) to see where the managed API and Envoy enforce policy.

## Reference

[CLI commands](./reference/cli/index.md) · [API routes](./api/overview.md) · [SDKs](./reference/sdk.md) · [Usage formats](./library/reference/usage-formats/coverage.md) · [Project status](./library/project-status.md)

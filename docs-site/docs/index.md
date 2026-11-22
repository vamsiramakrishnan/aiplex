---
slug: /
title: Overview
description: Choose a first task, identify the route your request takes, and find its policy and accounting contract.
---

# AIPlex documentation

AIPlex lets a platform team give developers a stable URL for models and MCP tools, control access to upstreams, and attribute usage to the caller. Start with a local request, then choose the surface you want to operate.

| I want to | Start here | What I get |
| --- | --- | --- |
| Make my first call | [Run locally](./getting-started/local-development.md) | A mock model reply and its matching ledger record |
| Connect a coding client | [Connect a client](./getting-started/connect-client.md) | Endpoint and auth configuration for an existing client |
| Give a team access | [Access points](./guides/access-points.md) | A named endpoint with model aliases, tools and caller policy |
| Control shared spend | [QuotaPlex](./guides/quotaplex.md) | A local model call and tool call drawing from one budget |
| Operate a deployment | [Operations](./operations/index.md) | Configuration, authorization, networking and qualification guides |

## Which gateway receives the call?

| Destination | Managed API | Cluster gateway |
| --- | --- | --- |
| [Model](./guides/llmplex.md) | Provider-native operations through a model surface or an access point | Envoy routes matching model traffic to an inference backend |
| [MCP tool](./guides/mcplex.md) | A saved tool connection or the MCP endpoint at `/ap/{id}/mcp` | A deployed MCP server |
| [Agent](./guides/a2aplex.md) | Invoke a saved native agent connection at `/api/v1/a2a/connections/{id}/invoke` | A deployed A2A workload |

An access point can also publish [skill manifests](./guides/skillsplex.md). [ClientPlex](./guides/clientplex.md) configures clients. [QuotaPlex](./guides/quotaplex.md) admits and accounts for supported managed operations. The [request path](./concepts/request-path.md) shows where each acts.

## Reference

[CLI commands](./reference/cli/index.md) · [API routes](./api/overview.md) · [SDKs](./reference/sdk.md) · [Usage formats](./library/reference/usage-formats/coverage.md) · [Project status](./library/project-status.md)

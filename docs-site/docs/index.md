---
slug: /
title: Overview
description: What AIPlex is, what it governs, and where to find each feature in these docs.
---

# AIPlex documentation

AIPlex is a control plane for agent interactions. It puts MCP, A2A and model traffic behind one gateway, one auth stack, one policy layer and one audit trail.

Three planes carry traffic:

| Plane | Protocol | Governs | Route resource |
| --- | --- | --- | --- |
| [MCPlex](./guides/mcplex.md) | MCP (JSON-RPC) | Agent to tool | `MCPRoute` |
| [A2APlex](./guides/a2aplex.md) | A2A (HTTP/JSON) | Agent to agent | `HTTPRoute` |
| [LLMPlex](./guides/llmplex.md) | Provider APIs | Agent to model | `LLMRoute` |

Four more modules share the same identity, permissions, revisioned storage and audit: [ClientPlex](./guides/clientplex.md) (developer client configuration), [SkillsPlex](./guides/skillsplex.md) (skill discovery and reported history), [QuotaPlex](./guides/quotaplex.md) (shared budgets and admission) and Networking (private connectivity plans, see [Networking and Anvil](./operations/networking.md)).

## How to read these docs

1. [Getting started](./getting-started/index.md) builds the CLI and API and runs them locally with one command, `aiplex dev`, then points to the Docker stack and the platform install.
2. [Concepts](./concepts/index.md) explains callers, resources, access points, identity, and quota and usage.
3. [Guides](./guides/index.md) cover each module and the identity features.
4. [Operations](./operations/index.md) covers configuration, identity configuration for Helm, networking and production qualification.
5. [Reference](./reference/index.md) holds the generated CLI reference, the SDK, the API route map, usage formats and metering profiles.

## What is verified

The pages under Guides, Operations and Reference that link into the `docs/` directory of the repository are rendered from those files at build time. They are the code-verified descriptions of each feature, including what is not implemented. Where this site and a code-verified page differ, the code-verified page wins.

AIPlex is under active development. Each feature page states its boundary; read the "Limits" section of a page before relying on a feature in production, and see [Production qualification](./library/production-qualification.md).

## Components

| Component | Language | Purpose |
| --- | --- | --- |
| AIPlex API | Go | REST API, deploy engine, consent handler, managed model, tool and access-point endpoints |
| AIPlex Console | React and TypeScript | Web UI |
| `aiplex-authz` | Rust | Scope authorization and JWT verification for the gateway `ext_authz` hook; see [Authorization policy](./operations/authorization-policy.md) |
| AIPlex CLI | Go | Command-line interface, see the [CLI reference](./reference/cli/index.md) |
| Ory Hydra and Kratos | Configured | OAuth token issuance, identity and social sign-in |
| Envoy AI Gateway | Configured | `MCPRoute`, `HTTPRoute` and `LLMRoute` routing |

The Terraform, Helm, Kubernetes and Ory configuration is under `deploy/` in the repository.

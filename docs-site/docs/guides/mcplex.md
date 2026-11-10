---
title: MCPlex
description: "Govern agent-to-tool access: deploy MCP servers, configure upstream tool auth and tool policy, and run delegated OAuth."
---

# MCPlex

MCPlex governs agent-to-tool traffic over MCP (JSON-RPC). It has two parts:

- **Deployed MCP servers.** Deploy a tool from the catalog; the platform creates the workload and an `MCPRoute` for it.
- **Managed tool connections.** A tool connection is a saved resource that tells AIPlex how to reach an upstream MCP server and how to authenticate to it. Callers reach connections through access points (`/ap/{id}/mcp`) or the managed endpoints, and the API executes stateless JSON and finite-SSE calls.

Registration alone does not execute or meter a tool. Metering of tool calls is described in [Quota and usage](../concepts/quota-and-usage.md).

## Deploy a server from the catalog

```bash
aiplex catalog list --plane mcplex
aiplex deploy TEMPLATE_ID --plane mcplex --name my-tool
aiplex get instance INSTANCE_ID
```

The API also serves the MCP registry listing for MCP clients at `GET /v0.1/servers`.

## Upstream auth and tool policy

A tool connection declares an `auth` block (how AIPlex authenticates upstream) and a `tools` block (allow and deny lists and read-only mode). Eight auth types cover API keys, static bearer and basic credentials, service OAuth, RFC 8693 token exchange, Google ID tokens and the per-user OAuth grant.

```bash
aiplex tools auth-types        # auth types, their fields and the tools policy
aiplex tools set-auth --help
```

Read [Tool connection auth and tool policy](../library/tool-connection-auth.md).

## Delegated user OAuth

When a tool needs a user's own authorization, the runtime service connects client registration (pre-registration, CIMD or DCR) to a verified user's authorization-code flow and a grant owned by the principal and actor:

```bash
aiplex tool-auth start --help
aiplex tool-auth complete --help
aiplex tool-auth revoke --help
```

Read [Owned MCP OAuth execution](../library/mcp-runtime-authorization.md) and the OAuth registration section of [Connected Plexes](../library/connected-plexes.md#mcp-oauth-registration).

## Related

- [Access points](./access-points.md) expose connections to a group of callers.
- [Usage ledger](../library/usage-ledger.md) records every managed tool call.
- API: [MCP connections](../api/mcp-connections.md).

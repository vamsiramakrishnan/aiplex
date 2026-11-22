---
title: MCPlex
description: "Govern agent-to-tool access: deploy MCP servers, configure upstream tool auth and tool policy, and run delegated OAuth."
---

# MCPlex

Connect an MCP server and choose which tools each caller can run. MCPlex handles upstream authentication and applies tool policy before forwarding the call.

- **Managed API.** Save a tool connection. Callers can reach it through a team access point at `/ap/{id}/mcp` or through the connection’s own API. The managed path supports stateless JSON and finite SSE responses. [Tool metering](../concepts/quota-and-usage.md)
- **Cluster gateway.** Deploy an MCP server from the catalog. AIPlex creates the workload and an Envoy `MCPRoute`.

## Deploy a server from the catalog

```bash
aiplex catalog list --plane mcplex
aiplex deploy TEMPLATE_ID --plane mcplex --name my-tool
aiplex get instance INSTANCE_ID
```

The API also serves the MCP registry listing for MCP clients at `GET /v0.1/servers`. To bring servers in from the official registry, see [Import from the MCP Registry](../library/mcp-registry-import.md).

## Upstream auth and tool policy

A tool connection declares an `auth` block (how AIPlex authenticates upstream) and a `tools` block (allow and deny lists and read-only mode). Eight auth types cover API keys, static bearer and basic credentials, service OAuth, RFC 8693 token exchange, Google ID tokens and the per-user OAuth grant.

```bash
aiplex tools auth-types        # auth types, their fields and the tools policy
aiplex tools set-auth --help
```

Read [Tool connection auth and tool policy](../library/tool-connection-auth.md).

## Policy rules on tool calls

Add `rules` to the tools block to allow or deny a call on its arguments, the tool's annotations, the caller or the time, with expressions that are type-checked when saved and refuse the call when they cannot be evaluated:

```bash
aiplex mcp connections rules add search --id no-etc --effect deny \
  --when "path_under(args.path, '/etc')" --message "files under /etc are off limits"
aiplex mcp connections rules test search --tool read_file --args '{"path":"/etc/hosts"}' --group eng
```

Read [Policy rules on tool calls](../library/tool-policy-rules.md).

## Import from the MCP Registry

```bash
aiplex mcp registry search github
aiplex mcp registry import ai.adside/adside --allow-tools list_campaigns --dry-run
```

Remote Streamable HTTP servers import as ordinary, read-only tool connections; local packages do not. Read [Import from the MCP Registry](../library/mcp-registry-import.md).

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

---
sidebar_position: 1
title: "MCPlex: Tools"
description: Deploy, manage, and govern MCP servers through AIPlex.
---

# MCPlex: Tools

MCPlex governs **Agent ↔ Tool** interactions. Deploy MCP servers, discover their tools, and control access with per-tool OAuth scopes.

## Deploy an MCP Server

### From the Catalog

```bash
# Browse available templates
aiplex catalog search --plane mcplex

# Deploy interactively
aiplex deploy
```

### From YAML

```yaml title="aiplex.yaml"
version: v1
instances:
  - name: my-github-tools
    template: github-mcp-server
    plane: mcplex
    config:
      GITHUB_TOKEN: "${GITHUB_TOKEN}"
      ALLOWED_REPOS: "org/repo1,org/repo2"
```

```bash
aiplex apply -f aiplex.yaml
```

### What Happens on Deploy

1. AIPlex creates a **SPIFFE identity** for the server
2. Deploys a **Kubernetes pod** in the `mcplex` namespace
3. Calls **`tools/list`** to discover available tools
4. Registers **OAuth scopes** in Hydra (`mcp:tools:search_repos`, etc.)
5. Creates an **MCPRoute** in Envoy AI Gateway
6. Grants the deployer access to all discovered scopes

## Manage Instances

```bash
# List all MCPlex instances
aiplex ls --plane mcplex

# Instance details
aiplex status my-github-tools

# Stream logs
aiplex logs my-github-tools --follow

# Update configuration
aiplex config my-github-tools --set ALLOWED_REPOS="org/repo1,org/repo2,org/repo3"

# Scale (if supported)
aiplex scale my-github-tools --replicas 3

# Remove
aiplex rm my-github-tools
```

## Tool-Level Access Control

Each tool gets its own scope. Grant or revoke access at the tool level:

```bash
# Grant an agent access to specific tools
aiplex agents grant tutor-agent \
  --scope mcp:tools:search_repos \
  --scope mcp:tools:read_file

# Revoke a specific tool
aiplex agents revoke tutor-agent --scope mcp:tools:create_issue

# Grant a user access
aiplex users grant student@school.edu \
  --scope mcp:tools:search_repos
```

Or use server-level scopes for bulk access:

```bash
aiplex agents grant tutor-agent --scope mcp:server:my-github-tools
```

## Connecting MCP Clients

After deploying, any MCP-compatible client can connect:

```json title="MCP client config"
{
  "mcpServers": {
    "my-github-tools": {
      "url": "https://aiplex.example.com/mcp/my-github-tools",
      "headers": {
        "Authorization": "Bearer ${AIPLEX_TOKEN}"
      }
    }
  }
}
```

Works with Claude Code, Claude Desktop, Cursor, and any MCP-compatible agent.

## Catalog Sources

MCPlex aggregates templates from multiple registries:

| Source | Description |
|--------|-------------|
| Official MCP Registry | Community MCP servers |
| MACH Alliance Registry | Enterprise integrations |
| Google 1P Servers | Google Cloud MCP tools |
| Custom Registries | Your private template repos |
| Local Templates | Uploaded directly to AIPlex |

Browse all of them in the Console or via CLI:

```bash
aiplex catalog search github --plane mcplex
aiplex catalog search database --plane mcplex
aiplex catalog search "google cloud" --plane mcplex
```

## Custom MCP Servers

Deploy your own MCP server by creating a template:

```yaml title="my-custom-server.yaml"
version: v1
templates:
  - id: my-custom-server
    plane: mcplex
    name: "My Custom Tools"
    description: "Internal tools for our team"
    image: "us-docker.pkg.dev/my-project/aiplex/my-mcp-server:latest"
    config_schema:
      type: object
      properties:
        API_KEY:
          type: string
          description: "API key for the backend service"
      required: ["API_KEY"]
    resource_limits:
      cpu: "500m"
      memory: "256Mi"
```

```bash
aiplex catalog upload -f my-custom-server.yaml
aiplex deploy --template my-custom-server
```

## MCP Subregistry

AIPlex exposes a standard MCP registry endpoint so other platforms can discover your deployed tools:

```
GET /v0.1/servers
```

This follows the [MCP Registry specification](https://spec.modelcontextprotocol.io/specification/2025-03-26/basic/index/) and returns all accessible MCP servers for the authenticated user.

## Next

- [A2APlex: Agents](/docs/guides/a2aplex) — deploy and govern A2A agents
- [Permissions](/docs/guides/permissions) — detailed permission management

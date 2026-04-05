---
sidebar_position: 6
title: Catalog
description: Browse, search, and manage templates across federated registries.
---

# Catalog

AIPlex federates templates from multiple registries into a single searchable catalog spanning all three planes.

## Browse the Catalog

### CLI

```bash
# Search across all planes
aiplex catalog search "github"

# Filter by plane
aiplex catalog search "database" --plane mcplex
aiplex catalog search "research" --plane a2aplex

# List all templates
aiplex catalog list --plane mcplex
```

### Console

Navigate to any plane tab (MCPlex, A2APlex, LLMPlex) and click **Browse Catalog**.

## Catalog Sources

| Source | Plane | Description |
|--------|-------|-------------|
| Official MCP Registry | MCPlex | Community MCP servers from `registry.modelcontextprotocol.io` |
| MACH Alliance Registry | MCPlex | Enterprise integration servers |
| Google 1P Servers | MCPlex | Google Cloud MCP tools |
| Built-in LLM Providers | LLMPlex | Gemini, Claude, GPT, Bedrock, Ollama |
| Local Templates | All | Your custom templates in Firestore |
| Custom Registries | MCPlex | Any URL serving the registry format |

Sources are queried in parallel. A failing source doesn't block results from healthy sources.

## Template Schema

Each template describes a deployable unit:

```yaml
id: github-mcp-server
plane: mcplex
name: "GitHub MCP Server"
description: "Search repos, read files, create issues, manage PRs"
image: "ghcr.io/modelcontextprotocol/github-server:latest"
config_schema:
  type: object
  properties:
    GITHUB_TOKEN:
      type: string
      description: "GitHub personal access token"
    ALLOWED_REPOS:
      type: string
      description: "Comma-separated list of allowed repositories"
  required: ["GITHUB_TOKEN"]
resource_limits:
  cpu: "500m"
  memory: "256Mi"
```

The `config_schema` is a JSON Schema. The CLI uses it for interactive prompts, and the Console generates forms from it automatically.

## Upload Custom Templates

```bash
aiplex catalog upload -f my-template.yaml
```

Or push a template programmatically via the API:

```bash
curl -X POST https://aiplex.example.com/api/v1/catalog/templates \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d @my-template.json
```

## Template Details

```bash
aiplex catalog get github-mcp-server
```

Shows the template's metadata, configuration schema, resource requirements, and source registry.

## Next

- [Declarative Configuration](/docs/guides/declarative-config) — manage everything with `aiplex.yaml`
- [Observability](/docs/guides/observability) — monitor your deployed instances

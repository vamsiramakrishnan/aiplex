---
sidebar_position: 1
title: Quickstart
description: Deploy your first MCP tool with AIPlex in 60 seconds.
---

# Quickstart

Deploy a governed MCP tool in under 60 seconds. No Kubernetes, no OAuth, no infrastructure vocabulary.

## Prerequisites

- A GCP project (or a running AIPlex instance)
- A terminal

## 1. Install the CLI

```bash
curl -fsSL https://get.aiplex.dev | sh
```

Or with Homebrew:

```bash
brew install vamsiramakrishnan/tap/aiplex
```

Or build from source:

```bash
go install github.com/vamsiramakrishnan/aiplex/cmd/aiplex-cli@latest
```

## 2. Login

```bash
aiplex login
```

This opens your browser for GCP authentication. The CLI stores credentials in `~/.aiplex/credentials.json`.

## 3. Deploy a Tool

The interactive deploy walks you through everything:

```bash
aiplex deploy
```

```
? What would you like to deploy? MCP Server (tool)
? Search the catalog: github
? Select a template: github-mcp-server
  GitHub tools: search repos, read files, create issues
? Instance name: my-github-tools
? Configuration:
  GITHUB_TOKEN: ghp_***
✓ Deployed my-github-tools to MCPlex
  Endpoint: https://aiplex.example.com/mcp/my-github-tools
  Tools: search_repos, read_file, create_issue, list_prs
```

## 4. Check Status

```bash
aiplex status my-github-tools
```

```
Instance:  my-github-tools
Plane:     MCPlex
Status:    ✓ Running
Endpoint:  https://aiplex.example.com/mcp/my-github-tools
Tools:     search_repos, read_file, create_issue, list_prs
Scopes:    mcp:tools:search_repos mcp:tools:read_file
           mcp:tools:create_issue mcp:tools:list_prs
Deployed:  2 minutes ago
```

## 5. Connect Your Agent

Add the tool to your MCP client config (Claude Code, Cursor, etc.):

```json title="claude_desktop_config.json"
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

Your agent can now call `search_repos`, `read_file`, `create_issue`, and `list_prs` — all governed by AIPlex.

---

## What Just Happened?

Behind the scenes, AIPlex:

1. **Created a SPIFFE identity** for the tool (`spiffe://.../ns/mcplex/sa/my-github-tools`)
2. **Deployed a Kubernetes pod** in the `mcplex` namespace with the MCP server image
3. **Discovered the tools** by calling `tools/list` on the running server
4. **Registered OAuth scopes** in Ory Hydra (`mcp:tools:search_repos`, etc.)
5. **Created an MCPRoute** in Envoy AI Gateway pointing to the pod
6. **Granted you access** to all discovered scopes

You didn't need to know any of that. But if you want to, see [Architecture: Deploy Engine](/docs/architecture/deploy-engine).

## Next Steps

- [Connect an agent](/docs/getting-started/connect-agent) with proper OAuth credentials
- [Deploy declaratively](/docs/guides/declarative-config) with `aiplex.yaml`
- [Add LLM access](/docs/guides/llmplex) for model inference
- [Set up agent-to-agent delegation](/docs/guides/a2aplex) with A2APlex

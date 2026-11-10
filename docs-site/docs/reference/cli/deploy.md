---
title: aiplex deploy
sidebar_label: deploy
description: "Deploy an instance from a catalog template"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Deploy an MCP server, A2A agent, or LLM provider from the catalog.

Examples:
  aiplex deploy kb-search --plane mcplex
  aiplex deploy research-agent --plane a2aplex --name "My Research Agent"
  aiplex deploy gemini-2.5-flash --plane llmplex

Usage:
  aiplex deploy <template-id> [flags]

Flags:
  -h, --help           help for deploy
  -n, --name string    Display name for the instance
  -p, --plane string   Target plane: mcplex, a2aplex, llmplex (required)

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


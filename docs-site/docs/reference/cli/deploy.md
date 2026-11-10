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

Examples:
  aiplex deploy github-mcp --plane mcplex --name github

Flags:
  -h, --help           help for deploy
  -n, --name string    Display name for the instance
  -p, --plane string   Target plane: mcplex, a2aplex, llmplex (required)

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```


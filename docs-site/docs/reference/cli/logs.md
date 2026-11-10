---
title: aiplex logs
sidebar_label: logs
description: "Stream logs from a deployed instance"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
View logs from a deployed MCP server, A2A agent, or LLM proxy.

Examples:
  aiplex logs knowledge-base-xyz              # last 100 lines
  aiplex logs knowledge-base-xyz -f           # follow/stream
  aiplex logs knowledge-base-xyz --tail 500   # last 500 lines

Usage:
  aiplex logs <instance-id> [flags]

Flags:
  -f, --follow     Follow log output
  -h, --help       help for logs
      --tail int   Number of recent log lines to show (default 100)

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


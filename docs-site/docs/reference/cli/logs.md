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

Examples:
  aiplex logs github-abc --follow
  aiplex logs github-abc --tail 100

Flags:
  -f, --follow     Follow log output
  -h, --help       help for logs
      --tail int   Number of recent log lines to show (default 100)

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


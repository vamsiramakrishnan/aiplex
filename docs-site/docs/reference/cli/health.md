---
title: aiplex health
sidebar_label: health
description: "Check that the API and its components answer"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Runs connectivity checks against the AIPlex API, auth services,
and gateway. Useful for diagnosing setup issues.

Checks:
  - API server reachability (/healthz)
  - Auth service (Hydra) availability
  - Kratos identity service availability
  - Gateway routing (MCPlex, A2APlex, LLMPlex paths)
  - DNS resolution for custom domain

Usage:
  aiplex health [flags]

Examples:
  aiplex health
  aiplex health --verbose

Flags:
  -h, --help      help for health
  -v, --verbose   Show detailed diagnostics

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


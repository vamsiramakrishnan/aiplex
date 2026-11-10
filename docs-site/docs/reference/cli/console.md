---
title: aiplex console
sidebar_label: console
description: "Serve the Console locally against the current context"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Starts a local web server serving the AIPlex console.
API requests are proxied to your configured AIPlex instance.

The console is embedded in the CLI binary — no separate install needed.

Usage:
  aiplex console [flags]

Examples:
  aiplex console
  aiplex console --port 9191

Flags:
  -h, --help       help for console
      --port int   Local port for the console (default 9090)

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


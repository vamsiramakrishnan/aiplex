---
title: aiplex login
sidebar_label: login
description: "Sign in to an AIPlex API and store the token for the current context"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Login using OAuth 2.0 Device Authorization Grant (RFC 8628).

This opens a browser for you to approve the CLI, then stores credentials
in ~/.aiplex/credentials.json. No browser? Use the displayed code.

Examples:
  aiplex login                      # login to current context
  aiplex login --context prod       # login to specific context
  aiplex login --token <token>      # store a token directly

Usage:
  aiplex login [flags]

Examples:
  aiplex login
  aiplex login --context prod

Flags:
      --context string   Context to login to (default: current)
  -h, --help             help for login

Global Flags:
      --debug           Show debug output, including raw API errors
      --no-color        Disable colour (also: NO_COLOR=1)
  -o, --output string   Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```


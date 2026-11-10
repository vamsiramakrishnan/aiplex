---
title: aiplex login
sidebar_label: login
description: "Authenticate with your AIPlex instance"
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

Flags:
      --context string   Context to login to (default: current)
  -h, --help             help for login

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


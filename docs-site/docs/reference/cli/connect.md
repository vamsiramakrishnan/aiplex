---
title: aiplex connect
sidebar_label: connect
description: "Print ready-to-paste configuration for pointing a client at an access point"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Shows the base URLs, MCP and skills URLs, how to obtain a token, and snippets
for a client. --client takes a client profile (for example claude-code, codex,
antigravity) or "generic" (the default) for environment variables and curl.

Snippets read the token from $AIPLEX_TOKEN; no token or secret is printed.

Usage:
  aiplex connect <access-point> [flags]

Examples:
  aiplex connect ide
  aiplex connect ide --client claude-code
  aiplex connect ide -o json

Flags:
      --client string   Client profile ID, or generic (default "generic")
  -h, --help            help for connect

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


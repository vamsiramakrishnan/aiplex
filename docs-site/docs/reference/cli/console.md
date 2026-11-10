---
title: aiplex console
sidebar_label: console
description: "Launch the AIPlex console locally"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Starts a local web server serving the AIPlex console.
API requests are proxied to your configured AIPlex instance.

The console is embedded in the CLI binary — no separate install needed.

Usage:
  aiplex console [flags]

Flags:
  -h, --help       help for console
      --port int   Local port for the console (default 9090)

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


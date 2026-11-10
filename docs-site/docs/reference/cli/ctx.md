---
title: aiplex ctx
sidebar_label: ctx
description: "Switch context (shorthand for config use-context)"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Switch to a named context. Without arguments, shows the current context.

Examples:
  aiplex ctx              # show current context
  aiplex ctx production   # switch to production

Usage:
  aiplex ctx [name] [flags]

Flags:
  -h, --help   help for ctx

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


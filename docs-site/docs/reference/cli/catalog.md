---
title: aiplex catalog
sidebar_label: catalog
description: "Browse the template catalog"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Browse the template catalog

Usage:
  aiplex catalog [command]

Available Commands:
  get         Get template details
  list        List catalog templates

Flags:
  -h, --help   help for catalog

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex catalog [command] --help" for more information about a command.
```

### `aiplex catalog get`

```text
Get template details

Usage:
  aiplex catalog get <template-id> [flags]

Flags:
  -h, --help   help for get

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex catalog list`

```text
List catalog templates

Usage:
  aiplex catalog list [flags]

Flags:
  -h, --help           help for list
  -p, --plane string   Filter by plane: mcplex, a2aplex, llmplex

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


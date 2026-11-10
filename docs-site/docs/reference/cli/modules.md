---
title: aiplex modules
sidebar_label: modules
description: "Discover enabled Aiplex modules"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Discover enabled Aiplex modules

Usage:
  aiplex modules [command]

Available Commands:
  inspect     Inspect a module's published contract
  list        List enabled modules and their capabilities

Flags:
  -h, --help   help for modules

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex modules [command] --help" for more information about a command.
```

### `aiplex modules inspect`

```text
Inspect a module's published contract

Usage:
  aiplex modules inspect <name> [flags]

Flags:
  -h, --help   help for inspect

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex modules list`

```text
List enabled modules and their capabilities

Usage:
  aiplex modules list [flags]

Flags:
  -h, --help   help for list

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


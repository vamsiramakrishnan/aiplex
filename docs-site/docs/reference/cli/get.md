---
title: aiplex get
sidebar_label: get
description: "Get details of a resource"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Get details of a resource

Usage:
  aiplex get [command]

Available Commands:
  agent       Get agent details and cross-plane permissions
  delegation  Get delegation details
  instance    Get instance details

Flags:
  -h, --help   help for get

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex get [command] --help" for more information about a command.
```

### `aiplex get agent`

```text
Get agent details and cross-plane permissions

Usage:
  aiplex get agent <client-id> [flags]

Flags:
  -h, --help   help for agent

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex get delegation`

```text
Get delegation details

Usage:
  aiplex get delegation <id> [flags]

Flags:
  -c, --chain   Show full delegation chain
  -h, --help    help for delegation

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex get instance`

```text
Get instance details

Usage:
  aiplex get instance <id> [flags]

Flags:
  -h, --help   help for instance

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


---
title: aiplex resources
sidebar_label: resources
description: "Manage connected module resources (model-surface, agent-connection, tool-connection, network-profile, client-destination, deployment-intent, agent-binding)"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Manage connected module resources (model-surface, agent-connection, tool-connection, network-profile, client-destination, deployment-intent, agent-binding)

Usage:
  aiplex resources [command]

Available Commands:
  delete      
  get         
  list        
  plan        
  put         

Flags:
  -h, --help   help for resources

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex resources [command] --help" for more information about a command.
```

### `aiplex resources delete`

```text
Usage:
  aiplex resources delete <kind> <id> <expected-revision> [flags]

Flags:
  -h, --help   help for delete

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex resources get`

```text
Usage:
  aiplex resources get <kind> <id> [flags]

Flags:
  -h, --help   help for get

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex resources list`

```text
Usage:
  aiplex resources list <kind> [flags]

Flags:
  -h, --help   help for list

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex resources plan`

```text
Usage:
  aiplex resources plan <kind> <id> [flags]

Flags:
  -h, --help                     help for plan
      --idempotency-key string   Stable deployment planning key (required for deployment-intent)

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex resources put`

```text
Usage:
  aiplex resources put <kind> <id> <json-file> <expected-revision> [flags]

Flags:
  -h, --help   help for put

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


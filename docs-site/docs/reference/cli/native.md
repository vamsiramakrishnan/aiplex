---
title: aiplex native
sidebar_label: native
description: "Inspect and review configured native cloud governance"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Inspect and review configured native cloud governance

Usage:
  aiplex native [command]

Available Commands:
  governance  Read native resources and apply separately reviewed plans

Flags:
  -h, --help   help for native

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex native [command] --help" for more information about a command.
```

### `aiplex native governance`

```text
Read native resources and apply separately reviewed plans

Usage:
  aiplex native governance [command]

Available Commands:
  apply       Apply a reviewed plan using its exact revision and digest
  get-plan    
  plan        Review a saved desired action without applying it
  providers   List configured native providers and their capabilities
  read        Read through a configured binding with an explicit revision
  receipt     

Flags:
  -h, --help   help for governance

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex native governance [command] --help" for more information about a command.
```

#### `aiplex native governance apply`

```text
Apply a reviewed plan using its exact revision and digest

Usage:
  aiplex native governance apply <plan-id> [flags]

Flags:
      --approval-digest string   Required digest of the reviewed plan
  -h, --help                     help for apply
      --plan-revision uint       Required saved plan revision

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex native governance get-plan`

```text
Usage:
  aiplex native governance get-plan <plan-id> [flags]

Flags:
  -h, --help   help for get-plan

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex native governance plan`

```text
Review a saved desired action without applying it

Usage:
  aiplex native governance plan <binding-id> [flags]

Flags:
      --binding-revision uint   Required current binding revision
      --desired-id string       Required desired action ID from the saved binding
  -h, --help                    help for plan
      --ttl duration            Plan validity duration (default 5m0s)

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex native governance providers`

```text
List configured native providers and their capabilities

Usage:
  aiplex native governance providers [flags]

Flags:
  -h, --help   help for providers

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex native governance read`

```text
Read through a configured binding with an explicit revision

Usage:
  aiplex native governance read <binding-id> <operation-json-file> [flags]

Flags:
      --binding-revision uint   Required current binding revision
  -h, --help                    help for read

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex native governance receipt`

```text
Usage:
  aiplex native governance receipt <plan-id> [flags]

Flags:
  -h, --help   help for receipt

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


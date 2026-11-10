---
title: aiplex infra
sidebar_label: infra
description: "Review, execute and inspect saved infrastructure plans"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Review, execute and inspect saved infrastructure plans

Usage:
  aiplex infra [command]

Available Commands:
  approve     
  execute     
  observation 
  observe     
  receipt     

Flags:
  -h, --help   help for infra

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex infra [command] --help" for more information about a command.
```

### `aiplex infra approve`

```text
Usage:
  aiplex infra approve <plan-id> [flags]

Flags:
      --digest string   Exact reviewed plan digest
  -h, --help            help for approve
      --reason string   Approval reason

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex infra execute`

```text
Usage:
  aiplex infra execute <plan-id> [flags]

Flags:
      --approval string   Saved approval record ID
      --digest string     Exact reviewed plan digest
  -h, --help              help for execute

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex infra observation`

```text
Usage:
  aiplex infra observation <plan-id> [flags]

Flags:
  -h, --help                     help for observation
      --idempotency-key string   Stable observation attempt ID

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex infra observe`

```text
Usage:
  aiplex infra observe <plan-id> [flags]

Flags:
  -h, --help                     help for observe
      --idempotency-key string   Stable observation attempt ID

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex infra receipt`

```text
Usage:
  aiplex infra receipt <plan-id> [flags]

Flags:
  -h, --help   help for receipt

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


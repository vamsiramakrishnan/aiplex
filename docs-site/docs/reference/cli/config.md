---
title: aiplex config
sidebar_label: config
description: "Manage CLI configuration and contexts"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Manage named contexts for different AIPlex environments.

Each context stores a URL, GCP project, region, and domain.
Credentials are stored separately in ~/.aiplex/credentials.json.

Examples:
  aiplex config set-context dev --url http://localhost:8080
  aiplex config set-context prod --url https://aiplex.example.com --project my-gcp-project
  aiplex config use-context prod
  aiplex config show

Usage:
  aiplex config [command]

Available Commands:
  current-context Show the active context name
  delete-context  Delete a named context
  set-context     Create or update a named context
  show            Show current configuration
  use-context     Switch to a different context

Flags:
  -h, --help   help for config

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex config [command] --help" for more information about a command.
```

### `aiplex config current-context`

```text
Show the active context name

Usage:
  aiplex config current-context [flags]

Flags:
  -h, --help   help for current-context

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex config delete-context`

```text
Delete a named context

Usage:
  aiplex config delete-context <name> [flags]

Flags:
  -h, --help   help for delete-context

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex config set-context`

```text
Create or update a named context

Usage:
  aiplex config set-context <name> [flags]

Flags:
      --domain string    Custom domain
  -h, --help             help for set-context
      --project string   GCP project ID
      --region string    GCP region
      --url string       API URL

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
```

### `aiplex config show`

```text
Show current configuration

Usage:
  aiplex config show [flags]

Flags:
  -h, --help   help for show

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex config use-context`

```text
Switch to a different context

Usage:
  aiplex config use-context <name> [flags]

Flags:
  -h, --help   help for use-context

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


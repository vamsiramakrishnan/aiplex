---
title: aiplex clients
sidebar_label: clients
description: "Register developer clients and review connection plans"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Register developer clients and review connection plans

Usage:
  aiplex clients [command]

Available Commands:
  delete      
  get         
  list        List registered clients
  plan        Render reviewed configuration fragments without applying them
  profiles    List supported client profiles
  register    Create or update a client registration

Flags:
  -h, --help   help for clients

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex clients [command] --help" for more information about a command.
```

### `aiplex clients delete`

```text
Usage:
  aiplex clients delete <id> [flags]

Flags:
      --expected-revision uint   Required current revision
  -h, --help                     help for delete

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex clients get`

```text
Usage:
  aiplex clients get <id> [flags]

Flags:
  -h, --help   help for get

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex clients list`

```text
List registered clients

Usage:
  aiplex clients list [flags]

Flags:
  -h, --help   help for list

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex clients plan`

```text
Render reviewed configuration fragments without applying them

Usage:
  aiplex clients plan <id> [flags]

Flags:
  -h, --help   help for plan

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex clients profiles`

```text
List supported client profiles

Usage:
  aiplex clients profiles [flags]

Flags:
  -h, --help   help for profiles

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex clients register`

```text
Create or update a client registration

Usage:
  aiplex clients register [flags]

Flags:
      --expected-revision uint   Expected revision; zero creates a new registration
  -f, --file string              Registration JSON file
  -h, --help                     help for register

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


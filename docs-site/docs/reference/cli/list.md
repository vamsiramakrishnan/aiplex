---
title: aiplex list
sidebar_label: list
description: "List resources"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
List resources

Usage:
  aiplex list [command]

Available Commands:
  agents      List registered agents
  delegations List A2A delegations
  denials     List policy denials
  instances   List deployed instances

Flags:
  -h, --help   help for list

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex list [command] --help" for more information about a command.
```

### `aiplex list agents`

```text
List registered agents

Usage:
  aiplex list agents [flags]

Aliases:
  agents, agent, a

Flags:
  -h, --help             help for agents
      --sort-by string   Sort results by field (client_id, name, status)
  -w, --watch            Watch for changes (refresh every 5s)

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex list delegations`

```text
List A2A delegations

Usage:
  aiplex list delegations [flags]

Aliases:
  delegations, del, d

Flags:
  -h, --help             help for delegations
      --sort-by string   Sort results by field (id, caller, callee, status, task)
  -w, --watch            Watch for changes (refresh every 5s)

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex list denials`

```text
List policy denials

Usage:
  aiplex list denials [flags]

Aliases:
  denials, deny

Flags:
  -h, --help             help for denials
      --sort-by string   Sort results by field (id, plane, agent, action, time)
  -w, --watch            Watch for changes (refresh every 5s)

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex list instances`

```text
List deployed instances

Usage:
  aiplex list instances [flags]

Aliases:
  instances, inst, i

Flags:
  -h, --help             help for instances
  -p, --plane string     Filter by plane
      --sort-by string   Sort results by field (id, plane, status, owner)
  -w, --watch            Watch for changes (refresh every 5s)

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


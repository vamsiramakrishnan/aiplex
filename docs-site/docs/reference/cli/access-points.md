---
title: aiplex access-points
sidebar_label: access-points
description: "Access points: one URL that enforces identity, access, QoS and quota"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
An access point is one stable URL (/ap/<id>/...) in front of models, tools and
skills. It decides which kinds of callers (agents, users, clients) may use it,
and carries a QoS class that is rendered into quota controls.

  aiplex access-points list
  aiplex access-points apply -f ide.yaml
  aiplex access-points quota-plan ide
  aiplex connect ide --client claude-code

Usage:
  aiplex access-points [command]

Aliases:
  access-points, access-point, ap

Available Commands:
  apply       Create or replace an access point from a JSON or YAML file
  delete      Delete an access point
  get         Show one access point
  list        List access points
  quota-plan  Render the access point's QoS block as quota controls for review

Flags:
  -h, --help   help for access-points

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex access-points [command] --help" for more information about a command.
```

### `aiplex access-points apply`

```text
Create the access point named by the file's id, or replace it when it already
exists (the current revision is read first, so a concurrent change is not
overwritten). The file may be the bare specification or the record printed by
'access-points get -o yaml'. Use '-f -' to read stdin.

Usage:
  aiplex access-points apply -f <file> [flags]

Flags:
  -f, --file string   JSON or YAML file, or - for stdin
  -h, --help          help for apply

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex access-points delete`

```text
Delete an access point

Usage:
  aiplex access-points delete <id> [flags]

Flags:
  -h, --help   help for delete

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex access-points get`

```text
Show one access point

Usage:
  aiplex access-points get <id> [flags]

Flags:
  -h, --help   help for get

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex access-points list`

```text
List access points

Usage:
  aiplex access-points list [flags]

Flags:
  -h, --help   help for list

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex access-points quota-plan`

```text
Shows the quota controls and scheduler flow that the access point's qos block would add. Nothing is applied.

Usage:
  aiplex access-points quota-plan <id> [flags]

Flags:
  -h, --help   help for quota-plan

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


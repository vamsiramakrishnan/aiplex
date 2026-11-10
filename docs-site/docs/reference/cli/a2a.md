---
title: aiplex a2a
sidebar_label: a2a
description: "A2APlex — manage agent delegation and discovery"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
A2APlex — manage agent delegation and discovery

Usage:
  aiplex a2a [command]

Available Commands:
  agents      List running A2A agents
  card        Get the Agent Card for a deployed A2A agent
  delegate    Record a new agent-to-agent delegation

Flags:
  -h, --help   help for a2a

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex a2a [command] --help" for more information about a command.
```

### `aiplex a2a agents`

```text
List running A2A agents

Usage:
  aiplex a2a agents [flags]

Flags:
  -h, --help   help for agents

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex a2a card`

```text
Get the Agent Card for a deployed A2A agent

Usage:
  aiplex a2a card <instance-id> [flags]

Flags:
  -h, --help   help for card

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex a2a delegate`

```text
Record a new agent-to-agent delegation

Usage:
  aiplex a2a delegate [flags]

Flags:
      --callee string   Callee agent ID (required)
      --caller string   Caller agent ID (required)
  -h, --help            help for delegate
  -t, --task string     Task type (required)
  -u, --user string     User ID

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


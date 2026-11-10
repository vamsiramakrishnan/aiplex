---
title: aiplex tool-auth
sidebar_label: tool-auth
description: "Authorize downstream tools for the verified principal and actor"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Authorize downstream tools for the verified principal and actor

Usage:
  aiplex tool-auth [command]

Available Commands:
  complete    Exchange a callback from a private file, keeping codes out of argv
  revoke      Revoke this owner's downstream tool grant
  start       Prepare a registered PKCE authorization URL
  status      

Flags:
  -h, --help   help for tool-auth

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex tool-auth [command] --help" for more information about a command.
```

### `aiplex tool-auth complete`

```text
Exchange a callback from a private file, keeping codes out of argv

Usage:
  aiplex tool-auth complete <connection-id> <private-response.json> [flags]

Flags:
  -h, --help   help for complete

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex tool-auth revoke`

```text
Revoke this owner's downstream tool grant

Usage:
  aiplex tool-auth revoke <connection-id> [flags]

Flags:
  -h, --help   help for revoke

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex tool-auth start`

```text
Prepare a registered PKCE authorization URL

Usage:
  aiplex tool-auth start <connection-id> [flags]

Flags:
  -h, --help                  help for start
      --redirect-uri string   Exact operator-registered redirect receiver

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex tool-auth status`

```text
Usage:
  aiplex tool-auth status <connection-id> [flags]

Flags:
  -h, --help   help for status

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


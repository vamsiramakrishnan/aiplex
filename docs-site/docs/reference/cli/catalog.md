---
title: aiplex catalog
sidebar_label: catalog
description: "Browse templates you can deploy"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Browse templates you can deploy

Usage:
  aiplex catalog [flags]
  aiplex catalog [command]

Examples:
  aiplex catalog list --plane mcplex

Available Commands:
  list        List catalog templates
  get         Get template details

Flags:
  -h, --help   help for catalog

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex catalog [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex catalog list`

```text
List catalog templates one page at a time. --page is zero-based and
--page-size is 1 to 100 (default 20). When another page exists the command
prints the exact command for the next one.

Usage:
  aiplex catalog list [flags]

Aliases:
  list, ls

Examples:
  aiplex catalog list
  aiplex catalog list --plane llmplex --page-size 50
  aiplex catalog list --page 2 -o json

Flags:
  -h, --help            help for list
      --page int        Page to show, zero-based
      --page-size int   Templates per page (1 to 100) (default 20)
  -p, --plane string    Filter by plane: mcplex, a2aplex, llmplex

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex catalog get`

```text
Get template details

Usage:
  aiplex catalog get <template-id> [flags]

Aliases:
  get, show, describe

Examples:
  aiplex catalog get github-mcp

Flags:
  -h, --help   help for get

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```


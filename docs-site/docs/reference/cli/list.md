---
title: aiplex list
sidebar_label: list
description: "List instances, agents, delegations or policy denials"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
List instances, agents, delegations or policy denials

Usage:
  aiplex list [flags]
  aiplex list [command]

Examples:
  aiplex list instances
  aiplex list denials

Available Commands:
  instances   List deployed instances
  agents      List registered agents
  delegations List A2A delegations
  denials     List policy denials

Flags:
  -h, --help   help for list

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex list [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex list instances`

```text
List deployed instances

Usage:
  aiplex list instances [flags]

Aliases:
  instances, inst, i

Examples:
  aiplex list instances --plane mcplex

Flags:
  -h, --help             help for instances
  -p, --plane string     Filter by plane
      --sort-by string   Sort results by field (id, plane, status, owner)
  -w, --watch            Watch for changes (refresh every 5s)

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

### `aiplex list agents`

```text
List registered agents

Usage:
  aiplex list agents [flags]

Aliases:
  agents, agent, a

Examples:
  aiplex list agents

Flags:
  -h, --help             help for agents
      --sort-by string   Sort results by field (client_id, name, status)
  -w, --watch            Watch for changes (refresh every 5s)

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

### `aiplex list delegations`

```text
List A2A delegations

Usage:
  aiplex list delegations [flags]

Aliases:
  delegations, del, d

Examples:
  aiplex list delegations

Flags:
  -h, --help             help for delegations
      --sort-by string   Sort results by field (id, caller, callee, status, task)
  -w, --watch            Watch for changes (refresh every 5s)

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

### `aiplex list denials`

```text
List policy denials

Usage:
  aiplex list denials [flags]

Aliases:
  denials, deny

Examples:
  aiplex list denials

Flags:
  -h, --help             help for denials
      --sort-by string   Sort results by field (id, plane, agent, action, time)
  -w, --watch            Watch for changes (refresh every 5s)

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


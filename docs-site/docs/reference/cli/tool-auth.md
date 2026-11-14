---
title: aiplex tool-auth
sidebar_label: tool-auth
description: "Authorize a downstream tool for yourself (OAuth sign-in to the tool)"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Authorize a downstream tool for yourself (OAuth sign-in to the tool)

Usage:
  aiplex tool-auth [flags]
  aiplex tool-auth [command]

Examples:
  aiplex tool-auth status github
  aiplex tool-auth start github

Available Commands:
  register    Register the connection's OAuth client with its authorization server (operators)
  start       Prepare a registered PKCE authorization URL
  complete    Exchange a callback from a private file, keeping codes out of argv
  status      Show this owner's downstream tool grant status
  revoke      Revoke this owner's downstream tool grant

Flags:
  -h, --help   help for tool-auth

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex tool-auth [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex tool-auth register`

```text
Registers the OAuth client for a connection that uses per-user OAuth
(oauth_user), using the client profile and issuer saved on the connection. The
request carries no body and no secret: a client secret is held as a reference
in secure credential storage, never given on the command line. Needs the admin
or deployer role. Run it before: aiplex tool-auth start

Usage:
  aiplex tool-auth register <connection-id> [flags]

Examples:
  aiplex tool-auth register search
  aiplex tool-auth register search -o json

Flags:
  -h, --help   help for register

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

### `aiplex tool-auth start`

```text
Prepare a registered PKCE authorization URL

Usage:
  aiplex tool-auth start <connection-id> [flags]

Examples:
  aiplex tool-auth start github
  aiplex tool-auth start github --redirect-uri http://127.0.0.1:8765/callback

Flags:
  -h, --help                  help for start
      --redirect-uri string   Exact operator-registered redirect receiver

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

### `aiplex tool-auth complete`

```text
Exchange a callback from a private file, keeping codes out of argv

Usage:
  aiplex tool-auth complete <connection-id> <private-response.json> [flags]

Examples:
  aiplex tool-auth complete github response.json

Flags:
  -h, --help   help for complete

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

### `aiplex tool-auth status`

```text
Show this owner's downstream tool grant status

Usage:
  aiplex tool-auth status <connection-id> [flags]

Examples:
  aiplex tool-auth status github

Flags:
  -h, --help   help for status

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

### `aiplex tool-auth revoke`

```text
Revoke this owner's downstream tool grant

Usage:
  aiplex tool-auth revoke <connection-id> [flags]

Examples:
  aiplex tool-auth revoke github

Flags:
  -h, --help   help for revoke

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


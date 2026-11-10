---
title: aiplex auth
sidebar_label: auth
description: "Credential helpers for tools that fetch a token by running a command"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Credential helpers for tools that fetch a token by running a command

Usage:
  aiplex auth [flags]
  aiplex auth [command]

Examples:
  aiplex auth print-token

Available Commands:
  print-token Print the access token this CLI would use, for a client's credential helper

Flags:
  -h, --help   help for auth

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex auth [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex auth print-token`

```text
Prints the current access token on stdout and nothing else, so a client can run
it as a credential helper: Claude Code's apiKeyHelper and Codex's
model_providers.<id>.auth.command do. "aiplex connect --write --token-helper"
configures exactly that, so the token never has to be written into a file.

The token comes from --token, AIPLEX_TOKEN or the current context, refreshed
when it is about to expire. Exits 3 when there is none or it has expired.
Treat the output as a secret.

Usage:
  aiplex auth print-token [flags]

Examples:
  aiplex auth print-token
  aiplex --context prod auth print-token

Flags:
  -h, --help   help for print-token

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


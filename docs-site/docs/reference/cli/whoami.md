---
title: aiplex whoami
sidebar_label: whoami
description: "Show which API and identity the CLI is using, and your roles"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Shows where the CLI sends requests and with which token (and where each came
from: a flag, AIPLEX_URL / AIPLEX_TOKEN, or the context), then asks the API who
that token is: subject, email, roles and grants.

--server (or -o json|yaml) prints only the API's answer, for scripts.

Usage:
  aiplex whoami [flags]

Examples:
  aiplex whoami
  aiplex whoami --server -o json
  aiplex --context prod whoami

Flags:
  -h, --help     help for whoami
      --server   Print only the API's identity answer (principal ID, groups, roles, scopes)

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


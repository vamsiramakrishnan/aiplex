---
title: aiplex validate
sidebar_label: validate
description: "Check an AIPlex configuration file without applying it"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Runs every check "aiplex apply -f" makes before it writes anything, and stops
there: the file must parse (unknown fields are errors), 'version' must be v1 or
omitted, at least one of instances, agents or routes must be present, and each
entry needs its required fields:

  instances  name, plane (mcplex, a2aplex, llmplex or skillsplex), template
  agents     client_id; authorization_code agents need redirect_uris
  routes     model_id and at least one backend

It does not contact a server, so it cannot know whether a template, secret or
fallback route exists.

Example:
  aiplex validate deploy.yaml
  aiplex validate config.json

Usage:
  aiplex validate <file.yaml|file.json> [flags]

Examples:
  aiplex validate aiplex.yaml

Flags:
  -h, --help   help for validate

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


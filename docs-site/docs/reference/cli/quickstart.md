---
title: aiplex quickstart
sidebar_label: quickstart
description: "Create a cloud deployment end to end in one command"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Runs the full setup pipeline end-to-end:

  1. aiplex init      — configure project and install tools
  2. platform apply   — deploy infrastructure + application
  3. deploy example   — deploy quickstart workload
  4. open console     — open the AIPlex console in browser

This is the fastest way to go from nothing to a running AIPlex platform.

Usage:
  aiplex quickstart [flags]

Examples:
  aiplex quickstart --project my-project --region us-central1

Flags:
  -h, --help             help for quickstart
      --project string   GCP project ID (skips interactive prompt)
      --region string    GCP region
  -y, --yes              Auto-approve Terraform changes

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


---
title: aiplex doctor
sidebar_label: doctor
description: "Diagnose your setup and say what to fix"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Runs a comprehensive diagnostic of your AIPlex setup:
  - CLI configuration and credentials
  - GCP project access and permissions
  - Kubernetes cluster connectivity
  - API server health
  - Auth services (Hydra/Kratos)
  - Certificate and DNS status
  - Common permission and networking issues

Usage:
  aiplex doctor [flags]

Examples:
  aiplex doctor
  aiplex --context prod doctor

Flags:
  -h, --help   help for doctor

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


---
title: aiplex upgrade
sidebar_label: upgrade
description: "Upgrade a deployed AIPlex platform"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Re-runs the full deployment pipeline to upgrade infrastructure and application.
This is an alias for "aiplex platform apply".

Usage:
  aiplex upgrade [flags]

Examples:
  aiplex upgrade --yes

Flags:
  -h, --help            help for upgrade
      --skip-deploy     Skip Helm (only run Terraform)
      --skip-infra      Skip Terraform (only run Helm)
  -f, --values string   Helm values file override
  -y, --yes             Auto-approve Terraform changes

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


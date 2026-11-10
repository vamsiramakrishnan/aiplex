---
title: aiplex quickstart
sidebar_label: quickstart
description: "Zero to running platform in one command"
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

Flags:
  -h, --help             help for quickstart
      --project string   GCP project ID (skips interactive prompt)
      --region string    GCP region
  -y, --yes              Auto-approve Terraform changes

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


---
title: aiplex upgrade
sidebar_label: upgrade
description: "Upgrade the AIPlex platform (alias for platform apply)"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Re-runs the full deployment pipeline to upgrade infrastructure and application.
This is an alias for "aiplex platform apply".

Usage:
  aiplex upgrade [flags]

Flags:
  -h, --help            help for upgrade
      --skip-deploy     Skip Helm (only run Terraform)
      --skip-infra      Skip Terraform (only run Helm)
  -f, --values string   Helm values file override
  -y, --yes             Auto-approve Terraform changes

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


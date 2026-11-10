---
title: aiplex doctor
sidebar_label: doctor
description: "Diagnose common issues and suggest fixes"
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

Flags:
  -h, --help   help for doctor

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


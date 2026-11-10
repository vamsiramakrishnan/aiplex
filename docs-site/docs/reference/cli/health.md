---
title: aiplex health
sidebar_label: health
description: "Check connectivity to all AIPlex components"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Runs connectivity checks against the AIPlex API, auth services,
and gateway. Useful for diagnosing setup issues.

Checks:
  - API server reachability (/healthz)
  - Auth service (Hydra) availability
  - Kratos identity service availability
  - Gateway routing (MCPlex, A2APlex, LLMPlex paths)
  - DNS resolution for custom domain

Usage:
  aiplex health [flags]

Flags:
  -h, --help      help for health
  -v, --verbose   Show detailed diagnostics

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


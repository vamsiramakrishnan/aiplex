---
title: aiplex validate
sidebar_label: validate
description: "Validate an AIPlex manifest file"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Validate a YAML or JSON manifest against the expected AIPlex schema.

The manifest must contain a top-level 'version' field and at least one of:
  instances, agents, routes

Example:
  aiplex validate deploy.yaml
  aiplex validate config.json

Usage:
  aiplex validate <file.yaml|file.json> [flags]

Flags:
  -h, --help   help for validate

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


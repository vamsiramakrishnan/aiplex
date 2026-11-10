---
title: aiplex apply
sidebar_label: apply
description: "Apply a declarative configuration file"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Apply instances, agents, and routes from a YAML/JSON file.

Example aiplex.yaml:
  version: v1
  instances:
    - name: knowledge-base
      plane: mcplex
      template: kb-search-server
    - name: research-agent
      plane: a2aplex
      template: research-agent
  agents:
    - client_id: tutor-agent
      display_name: Tutor Agent
      auth_method: client_credentials
      grant_types: [client_credentials]
      allowed_scopes:
        - mcp:tools:search_curriculum
        - a2a:task:research
        - llm:model:gemini-2.5-flash
  routes:
    - model_id: gemini-2.5-flash
      backends:
        - provider: google
          model_id: gemini-2.5-flash
          weight: 80
          enabled: true
        - provider: anthropic
          model_id: claude-sonnet-4-20250514
          weight: 20
          enabled: true
      budget:
        max_daily_cost_usd: 50

Usage:
  aiplex apply -f aiplex.yaml
  aiplex apply -f instances.json

Usage:
  aiplex apply -f <file> [flags]

Flags:
  -f, --file string   Path to manifest file (YAML or JSON)
  -h, --help          help for apply

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


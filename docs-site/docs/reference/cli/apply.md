---
title: aiplex apply
sidebar_label: apply
description: "Apply a declarative AIPlex configuration file"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Apply instances, agents, and routes from a YAML/JSON file. The file is checked
first with the same checks as "aiplex validate"; nothing is applied if it fails.

Apply converges: running it again with the same file changes nothing. Each
object is reported as created (it was absent), updated (it differed and could
be replaced), unchanged (it already matched) or skipped, then a summary. The
exit status is non-zero only when an object failed. --dry-run prints the same
plan and writes nothing.

  routes     Replaced at the revision just read (If-Match), keeping a saved
             cache TTL and rate limit the file does not mention. A revision
             written in the file is a precondition: apply fails if the saved
             route is at a different revision.
  agents     Created when absent. The API has no update for an agent (its OAuth
             client is issued once), so an agent that differs fails and names
             the field; delete it and apply again.
  instances  Matched by plane and name. A different template or configuration
             fails the same way; undeploy it first.

Routes are applied in file order, so a route named as a fallback must come
before the route that lists it. Under "aiplex dev", instances are skipped
(they need Kubernetes and a template catalog).

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

Examples:
  aiplex apply -f aiplex.yaml

Flags:
      --dry-run       Show what would be created, updated, or left unchanged, and write nothing
  -f, --file string   Path to manifest file (YAML or JSON)
  -h, --help          help for apply

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


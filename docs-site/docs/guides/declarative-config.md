---
sidebar_position: 8
title: Declarative Configuration
description: Manage all AIPlex resources with a single aiplex.yaml file.
---

# Declarative Configuration

AIPlex supports a single YAML file that describes your entire setup — instances, agents, routes, and permissions across all three planes.

## The `aiplex.yaml` File

```yaml title="aiplex.yaml"
version: v1

# MCPlex and A2APlex instances
instances:
  - name: kb-search
    template: kb-search-server
    plane: mcplex
    config:
      INDEX_PATH: "/data/curriculum"

  - name: github-tools
    template: github-mcp-server
    plane: mcplex
    config:
      GITHUB_TOKEN: "${GITHUB_TOKEN}"

  - name: research-agent
    template: research-agent
    plane: a2aplex
    config:
      SEARCH_API_KEY: "${SEARCH_API_KEY}"

# LLMPlex routing
routes:
  llm:
    - name: default
      backends:
        - provider: google
          model: gemini-2.5-flash
          weight: 80
        - provider: anthropic
          model: claude-sonnet-4-20250514
          weight: 20
      fallback:
        - provider: openai
          model: gpt-4o
      budget:
        daily_limit_usd: 50.00
        monthly_limit_usd: 1000.00

# Agent registrations with cross-plane permissions
agents:
  - name: tutor-agent
    description: "AI tutor for student interactions"
    grant:
      - mcp:tools:search_curriculum
      - mcp:tools:get_document
      - a2a:task:research
      - llm:model:gemini-2.5-flash

  - name: assessment-agent
    description: "Automated assessment grading"
    grant:
      - mcp:tools:generate_quiz
      - mcp:tools:grade_assignment
      - llm:model:gemini-2.5-flash
```

## Apply

```bash
aiplex apply -f aiplex.yaml
```

AIPlex processes the file in dependency order: instances first, then routes, then agents (which reference instance scopes).

## Preview Changes

```bash
aiplex diff -f aiplex.yaml
```

Shows what would be created, updated, or deleted:

```
+ Instance kb-search (mcplex) — will deploy
~ Instance github-tools (mcplex) — config changed: GITHUB_TOKEN
+ Instance research-agent (a2aplex) — will deploy
+ Route default (llmplex) — will create
~ Agent tutor-agent — adding scope: a2a:task:research
  Agent assessment-agent — no changes
```

## Environment Variables

Use `${VAR_NAME}` syntax for secrets:

```yaml
config:
  GITHUB_TOKEN: "${GITHUB_TOKEN}"
  API_KEY: "${MY_API_KEY}"
```

The CLI resolves these from your shell environment at apply time. Never commit secrets in plain text.

## Progressive Disclosure

AIPlex supports four layers of configuration complexity:

| Layer | Approach | For |
|-------|----------|-----|
| **0** | `aiplex deploy` (interactive) | First-time users |
| **1** | Simple YAML (name + template + config) | Most deployments |
| **2** | Full YAML (instances + routes + agents + budgets) | Production |
| **3** | Escape hatch (raw K8s overrides) | Platform engineers |

### Layer 3: K8s Overrides

For advanced users who need direct K8s resource control:

```yaml
version: v1
instances:
  - name: high-traffic-tool
    template: search-server
    plane: mcplex
    config:
      INDEX: "production"
    overrides:
      replicas: 3
      resources:
        cpu: "2"
        memory: "1Gi"
      tolerations:
        - key: "dedicated"
          operator: "Equal"
          value: "tools"
```

## Init a New Config

```bash
aiplex init
```

Interactive wizard that generates a starter `aiplex.yaml`:

```
? What do you need? (select all that apply)
  ☑ MCP tools
  ☑ LLM routing
  ☐ A2A agents

? Select MCP tools to deploy:
  ☑ GitHub
  ☑ PostgreSQL
  ☐ Slack

? Primary LLM provider: Google (Gemini)
? Fallback provider: Anthropic (Claude)

✓ Generated aiplex.yaml
  Review and run: aiplex apply -f aiplex.yaml
```

## Validate

```bash
aiplex validate -f aiplex.yaml
```

Checks schema validity, template existence, scope format, and configuration completeness without deploying.

## Next

- [CLI Reference](/docs/reference/cli) — all CLI commands
- [Configuration Reference](/docs/reference/configuration) — full YAML schema

---
sidebar_position: 2
title: Configuration Reference
description: Complete schema for aiplex.yaml configuration files.
---

# Configuration Reference

The `aiplex.yaml` file is the declarative configuration format for AIPlex. This page documents the complete schema.

## Top-Level Structure

```yaml
version: v1                 # Required. Schema version.

instances: []               # MCPlex and A2APlex instances
routes:                     # LLMPlex routing configuration
  llm: []
agents: []                  # Agent registrations and permissions
```

## `instances`

Each instance deploys a tool (MCPlex) or agent (A2APlex).

```yaml
instances:
  - name: string            # Required. Unique instance name (DNS-safe)
    template: string        # Required. Template ID from catalog
    plane: string           # Required. "mcplex" | "a2aplex"
    config:                 # Template-specific configuration
      KEY: "value"
      SECRET: "${ENV_VAR}"  # Environment variable substitution
    overrides:              # Optional. K8s resource overrides (Layer 3)
      replicas: number
      resources:
        cpu: string         # e.g., "500m", "2"
        memory: string      # e.g., "256Mi", "1Gi"
      tolerations: []       # K8s tolerations
```

### Field Details

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | DNS-safe instance identifier. Must be unique. |
| `template` | string | Yes | Template ID from the catalog. Use `aiplex catalog search` to find IDs. |
| `plane` | enum | Yes | `"mcplex"` for tools, `"a2aplex"` for agents. |
| `config` | map | No | Key-value pairs passed to the container as environment variables. |
| `overrides` | object | No | Kubernetes resource overrides for advanced users. |

### Environment Variable Substitution

Values matching `${VAR_NAME}` are resolved from the shell environment at `aiplex apply` time:

```yaml
config:
  GITHUB_TOKEN: "${GITHUB_TOKEN}"       # From env
  STATIC_VALUE: "hardcoded"              # Literal
  WITH_DEFAULT: "${API_KEY:-default}"    # With default
```

## `routes.llm`

LLM routing configuration for LLMPlex.

```yaml
routes:
  llm:
    - name: string          # Required. Route name
      backends:             # Required. Provider backends
        - provider: string  # Required. Provider name
          model: string     # Required. Model identifier
          weight: number    # Optional. Traffic weight (default: 100)
      fallback:             # Optional. Fallback backends
        - provider: string
          model: string
      budget:               # Optional. Cost limits
        daily_limit_usd: number
        monthly_limit_usd: number
        alert_threshold_percent: number  # Default: 80
```

### Supported Providers

| Provider Value | Service |
|---------------|---------|
| `google` | Google Gemini |
| `anthropic` | Anthropic Claude |
| `openai` | OpenAI GPT |
| `bedrock` | AWS Bedrock |
| `ollama` | Local Ollama |

### Weight Distribution

Weights are relative. These are equivalent:

```yaml
# Explicit percentages
backends:
  - provider: google
    model: gemini-2.5-flash
    weight: 80
  - provider: anthropic
    model: claude-sonnet-4-20250514
    weight: 20

# Same ratio, different numbers
backends:
  - provider: google
    model: gemini-2.5-flash
    weight: 4
  - provider: anthropic
    model: claude-sonnet-4-20250514
    weight: 1
```

## `agents`

Agent registrations with cross-plane permissions.

```yaml
agents:
  - name: string            # Required. Agent name (becomes OAuth client ID prefix)
    description: string     # Optional. Human-readable description
    grant:                  # Required. List of scopes (dimension A: agent ceiling)
      - "mcp:tools:{name}"
      - "a2a:task:{type}"
      - "llm:model:{id}"
```

### Scope Format

```
{plane}:{resource_type}:{resource_name}
```

| Plane | Scope Examples |
|-------|---------------|
| MCPlex | `mcp:tools:search_curriculum`, `mcp:server:kb-search` |
| A2APlex | `a2a:task:research`, `a2a:agent:research-agent` |
| LLMPlex | `llm:model:gemini-2.5-flash`, `llm:capability:vision` |

## Complete Example

```yaml title="aiplex.yaml"
version: v1

instances:
  - name: kb-search
    template: kb-search-server
    plane: mcplex
    config:
      INDEX_PATH: "/data/curriculum"
      MAX_RESULTS: "20"

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

## Validation

```bash
# Validate syntax and references
aiplex validate -f aiplex.yaml

# Preview what would change
aiplex diff -f aiplex.yaml
```

---
sidebar_position: 3
title: Your First Deploy
description: Detailed walkthrough of deploying tools, agents, and models with AIPlex.
---

# Your First Deploy

This guide walks through deploying across all three planes — tools (MCPlex), agents (A2APlex), and models (LLMPlex).

## Deploy an MCP Tool (MCPlex)

### Option A: Interactive CLI

```bash
aiplex deploy
```

The CLI guides you through template selection, configuration, and deployment. No YAML required.

### Option B: Declarative YAML

Create `aiplex.yaml`:

```yaml title="aiplex.yaml"
version: v1
instances:
  - name: my-github-tools
    template: github-mcp-server
    plane: mcplex
    config:
      GITHUB_TOKEN: "${GITHUB_TOKEN}"
```

Apply it:

```bash
aiplex apply -f aiplex.yaml
```

### Option C: Web Console

1. Open the AIPlex Console
2. Navigate to **MCPlex** tab
3. Click **Browse Catalog**
4. Search for "github", click **Deploy**
5. Fill in configuration, click **Deploy**

## Deploy an A2A Agent (A2APlex)

A2A agents are other AI agents that your agents can delegate tasks to.

```yaml title="aiplex.yaml"
version: v1
instances:
  - name: research-agent
    template: research-agent
    plane: a2aplex
    config:
      SEARCH_API_KEY: "${SEARCH_API_KEY}"
      MAX_RESULTS: "10"
```

```bash
aiplex apply -f aiplex.yaml
```

After deployment, the agent publishes an [A2A Agent Card](https://google.github.io/A2A/) describing its capabilities:

```bash
aiplex status research-agent
```

```
Instance:  research-agent
Plane:     A2APlex
Status:    ✓ Running
Endpoint:  https://aiplex.example.com/a2a/research-agent
Tasks:     research, summarize, fact-check
Scopes:    a2a:task:research a2a:task:summarize a2a:task:fact-check
```

## Configure LLM Routing (LLMPlex)

LLMPlex routes model inference requests with failover and cost control. No pods — Envoy AI Gateway handles routing directly.

```yaml title="aiplex.yaml"
version: v1
routes:
  llm:
    - name: primary-models
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
```

```bash
aiplex apply -f aiplex.yaml
```

## Combine All Three Planes

Here's a complete `aiplex.yaml` that deploys tools, an agent, LLM routing, and connects them:

```yaml title="aiplex.yaml"
version: v1

instances:
  # MCPlex: Tools
  - name: kb-search
    template: kb-search-server
    plane: mcplex
    config:
      INDEX_PATH: "/data/curriculum"

  # A2APlex: Agents
  - name: research-agent
    template: research-agent
    plane: a2aplex
    config:
      SEARCH_API_KEY: "${SEARCH_API_KEY}"

# LLMPlex: Model routing
routes:
  llm:
    - name: default
      backends:
        - provider: google
          model: gemini-2.5-flash
          weight: 100
      budget:
        daily_limit_usd: 25.00

# Agent registration with cross-plane access
agents:
  - name: tutor-agent
    grant:
      - mcp:tools:search_curriculum
      - mcp:tools:get_document
      - a2a:task:research
      - llm:model:gemini-2.5-flash
```

```bash
aiplex apply -f aiplex.yaml
```

One file. All three planes. Full governance.

## Verify Everything Works

```bash
# List all running instances
aiplex ls

# Check a specific instance
aiplex status kb-search

# View logs
aiplex logs kb-search --follow

# Run diagnostics
aiplex doctor
```

## Next Steps

- [Connect an agent](/docs/getting-started/connect-agent) to use your deployed tools
- [Manage permissions](/docs/guides/permissions) to control who can access what
- [Set up observability](/docs/guides/observability) for monitoring and cost tracking

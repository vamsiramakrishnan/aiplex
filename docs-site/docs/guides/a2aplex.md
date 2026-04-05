---
sidebar_position: 2
title: "A2APlex: Agents"
description: Deploy and govern agent-to-agent delegation with AIPlex.
---

# A2APlex: Agents

A2APlex governs **Agent ↔ Agent** interactions. Deploy AI agents that other agents can delegate tasks to, with identity and consent at every hop.

## Deploy an A2A Agent

```yaml title="aiplex.yaml"
version: v1
instances:
  - name: research-agent
    template: research-agent
    plane: a2aplex
    config:
      SEARCH_API_KEY: "${SEARCH_API_KEY}"
      MAX_RESULTS: "10"
      OUTPUT_FORMAT: "markdown"
```

```bash
aiplex apply -f aiplex.yaml
```

### What Happens on Deploy

1. Creates a **SPIFFE identity** for the agent
2. Deploys a **Kubernetes pod** in the `a2aplex` namespace
3. Calls **`tasks/list`** to discover task types (research, summarize, etc.)
4. Registers **OAuth scopes** (`a2a:task:research`, `a2a:task:summarize`)
5. Creates an **HTTPRoute** in Envoy AI Gateway
6. Publishes an **A2A Agent Card** for discovery

## A2A Agent Cards

Every deployed A2A agent publishes a discovery document following the [A2A specification](https://google.github.io/A2A/):

```bash
# View an agent's card
aiplex a2a card research-agent
```

```json
{
  "name": "research-agent",
  "description": "Research and summarize topics from web and academic sources",
  "url": "https://aiplex.example.com/a2a/research-agent",
  "capabilities": {
    "tasks": ["research", "summarize", "fact-check"]
  },
  "authentication": {
    "schemes": ["bearer"]
  }
}
```

Other agents discover available agents via:

```bash
# List all A2A agents
aiplex a2a list

# List all agent cards
curl https://aiplex.example.com/a2a/.well-known/agent-cards \
  -H "Authorization: Bearer ${TOKEN}"
```

## Task Delegation

An agent delegates a task to another agent via HTTP:

```bash
curl -X POST https://aiplex.example.com/a2a/research-agent \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "task_type": "research",
    "input": {
      "topic": "projectile motion",
      "depth": "comprehensive",
      "sources": ["academic", "web"]
    }
  }'
```

AIPlex verifies `a2a:task:research` is in the token before forwarding.

## Delegation Chains

When agent A delegates to agent B, which delegates to agent C, AIPlex tracks the full chain:

```bash
# View delegation chain
aiplex a2a delegations --chain
```

```
tutor-agent → research-agent (research)
  └→ research-agent → summarizer (summarize)
```

Each hop is audited with both the originating user and the delegating agent's identity.

## Access Control

```bash
# Grant an agent permission to delegate to another agent
aiplex agents grant tutor-agent \
  --scope a2a:task:research \
  --scope a2a:agent:research-agent

# Grant by task type (any agent that offers this task)
aiplex agents grant tutor-agent --scope a2a:task:research

# Grant by specific agent (all task types)
aiplex agents grant tutor-agent --scope a2a:agent:research-agent
```

## Multi-Agent Orchestration

Deploy multiple agents that work together:

```yaml title="aiplex.yaml"
version: v1
instances:
  - name: research-agent
    template: research-agent
    plane: a2aplex
    config:
      SEARCH_API_KEY: "${SEARCH_API_KEY}"

  - name: viz-agent
    template: visualization-agent
    plane: a2aplex
    config:
      RENDER_ENGINE: "d3"

  - name: summarizer
    template: summarizer-agent
    plane: a2aplex

agents:
  - name: tutor-agent
    grant:
      - a2a:task:research
      - a2a:task:visualize
      - a2a:task:summarize
      - mcp:tools:search_curriculum
      - llm:model:gemini-2.5-flash
```

The tutor agent can now orchestrate: research a topic, visualize data, summarize findings — all governed.

## Next

- [LLMPlex: Models](/docs/guides/llmplex) — route and govern model inference
- [Permissions](/docs/guides/permissions) — manage cross-plane access

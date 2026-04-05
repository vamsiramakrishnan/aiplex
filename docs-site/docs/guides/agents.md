---
sidebar_position: 4
title: Agents
description: Register, manage, and monitor agents with cross-plane access.
---

# Agents

An **agent** in AIPlex is any software that interacts with tools, other agents, or models. Each agent is an OAuth client in Ory Hydra with a defined set of allowed scopes.

## Register an Agent

### CLI

```bash
aiplex agents register \
  --name tutor-agent \
  --description "AI tutor for student interactions" \
  --grant mcp:tools:search_curriculum \
  --grant mcp:tools:generate_quiz \
  --grant a2a:task:research \
  --grant llm:model:gemini-2.5-flash
```

### YAML

```yaml title="aiplex.yaml"
agents:
  - name: tutor-agent
    description: "AI tutor for student interactions"
    grant:
      - mcp:tools:search_curriculum
      - mcp:tools:generate_quiz
      - a2a:task:research
      - llm:model:gemini-2.5-flash
```

### Console

Navigate to **Agents** tab, click **Register Agent**.

## Cross-Plane View

The Agents tab in the Console shows a unified view of what each agent can access:

```bash
aiplex agents get tutor-agent
```

```
Agent: tutor-agent
Description: AI tutor for student interactions

MCPlex (Tools):
  ✓ mcp:tools:search_curriculum
  ✓ mcp:tools:generate_quiz

A2APlex (Agents):
  ✓ a2a:task:research

LLMPlex (Models):
  ✓ llm:model:gemini-2.5-flash
```

## Manage Permissions

```bash
# Grant additional scopes
aiplex agents grant tutor-agent --scope mcp:tools:grade_assignment

# Revoke a scope
aiplex agents revoke tutor-agent --scope mcp:tools:generate_quiz

# List all agents
aiplex agents list

# Delete an agent
aiplex agents delete old-agent
```

## Agent Credentials

After registration, get the OAuth credentials:

```bash
aiplex agents credentials tutor-agent
```

Use these for the appropriate OAuth grant:

- **Client Credentials** — machine-to-machine (internal agents)
- **Authorization Code + PKCE** — user-facing agents (IDE plugins)
- **Device Grant** — CLI agents

## Monitoring Agent Activity

```bash
# View recent activity
aiplex agents activity tutor-agent

# Policy denials for an agent
aiplex agents denials tutor-agent
```

The Dashboard shows per-agent metrics: tool calls, delegations, model requests, and policy denials.

## Next

- [Permissions](/docs/guides/permissions) — the full permission management guide
- [Connect an Agent](/docs/getting-started/connect-agent) — integration patterns

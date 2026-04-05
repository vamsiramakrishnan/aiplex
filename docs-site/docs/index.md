---
slug: /
sidebar_position: 1
title: Welcome to AIPlex
---

# Welcome to AIPlex

**AIPlex is a unified control plane for AI agent interactions.** It governs three planes through a single gateway, auth stack, policy engine, and audit trail.

| Plane | Protocol | What It Governs | Scope Format |
|-------|----------|-----------------|--------------|
| **MCPlex** | MCP (JSON-RPC) | Agent ↔ Tool | `mcp:tools:{tool_name}` |
| **A2APlex** | A2A (HTTP/JSON) | Agent ↔ Agent | `a2a:task:{task_type}` |
| **LLMPlex** | Provider APIs | Agent ↔ Model | `llm:model:{model_id}` |

## How It Works

Your AI agents interact with tools, other agents, and language models. AIPlex sits in the middle — a single Envoy AI Gateway that authenticates, authorizes, rate-limits, and audits every interaction.

A single JWT carries scopes across all three planes:

```json
{
  "sub": "student@school.edu",
  "azp": "tutor-agent",
  "scope": "mcp:tools:search_curriculum a2a:task:research llm:model:gemini-2.5-flash"
}
```

## Start Here

<div className="quickstart-steps">
  <div className="quickstart-step">
    <div className="step-number">1</div>
    <h4>New to AIPlex?</h4>
    <p><a href="/docs/getting-started/quickstart">Quickstart Guide</a> — deploy your first tool in 60 seconds.</p>
  </div>
  <div className="quickstart-step">
    <div className="step-number">2</div>
    <h4>Understand the Model</h4>
    <p><a href="/docs/concepts/three-planes">Three Planes</a> — learn how MCPlex, A2APlex, and LLMPlex work together.</p>
  </div>
  <div className="quickstart-step">
    <div className="step-number">3</div>
    <h4>Go Deeper</h4>
    <p><a href="/docs/architecture/overview">Architecture</a> — the full system design for platform engineers.</p>
  </div>
</div>

## Three Ways to Use AIPlex

### CLI (Fastest)

```bash
# Interactive — guided prompts, zero YAML
aiplex deploy

# Declarative — single file, full control
aiplex apply -f aiplex.yaml
```

### Console (Visual)

A React-based web UI for browsing catalogs, deploying instances, managing permissions, and monitoring usage across all three planes.

### SDK (Programmatic)

```go
client := aiplex.NewClient("https://aiplex.example.com")
client.SetToken(token)

instance, err := client.Deploy(aiplex.DeployRequest{
    TemplateID: "kb-search-server",
    Plane:      "mcplex",
    Config:     map[string]any{"index": "curriculum"},
})
```

## Documentation Structure

| Section | For | What You'll Learn |
|---------|-----|-------------------|
| [Getting Started](/docs/getting-started/quickstart) | Everyone | Install, deploy, connect in minutes |
| [Core Concepts](/docs/concepts/three-planes) | Everyone | The mental model behind AIPlex |
| [Guides](/docs/guides/mcplex) | Operators | Plane-specific how-tos |
| [Reference](/docs/reference/cli) | Developers | CLI, API, SDK, config format |
| [Architecture](/docs/architecture/overview) | Platform Engineers | System design, deep dives |
| [API Reference](/docs/api/overview) | Integrators | Every endpoint, request/response |

---
sidebar_position: 4
title: Connect an Agent
description: Register an AI agent and connect it to AIPlex-governed tools, agents, and models.
---

# Connect an Agent

An **agent** in AIPlex is any software that calls tools, delegates to other agents, or queries models. This could be a chatbot, an IDE extension, a CLI tool, or another AI agent.

## Agent Types and Auth Flows

| Agent Type | Example | Auth Method |
|-----------|---------|-------------|
| Internal (same GKE) | Microservice in your cluster | SPIFFE mTLS, Client Credentials |
| External (cloud) | AWS Lambda, Azure Function | Workload Identity Federation |
| IDE Plugin | Cursor, VS Code, Claude Code | Authorization Code + PKCE |
| CLI | Claude Code, custom scripts | Device Grant (RFC 8628) |
| With user delegation | Any of the above | Authorization Code + PKCE |

## Register an Agent

### Via CLI

```bash
aiplex agents register \
  --name tutor-agent \
  --description "Tutoring agent for student interactions" \
  --grant mcp:tools:search_curriculum \
  --grant mcp:tools:generate_quiz \
  --grant a2a:task:research \
  --grant llm:model:gemini-2.5-flash
```

### Via YAML

```yaml title="aiplex.yaml"
agents:
  - name: tutor-agent
    description: "Tutoring agent for student interactions"
    grant:
      - mcp:tools:search_curriculum
      - mcp:tools:generate_quiz
      - a2a:task:research
      - llm:model:gemini-2.5-flash
```

### Via Console

Navigate to **Agents** tab, click **Register Agent**, fill in the form.

## Get Credentials

After registration, AIPlex creates an OAuth client in Ory Hydra:

```bash
aiplex agents credentials tutor-agent
```

```
Client ID:     tutor-agent-a1b2c3
Client Secret: ory_secret_***
Token URL:     https://aiplex.example.com/oauth2/token
Scopes:        mcp:tools:search_curriculum mcp:tools:generate_quiz
               a2a:task:research llm:model:gemini-2.5-flash
```

## Connect from Your Code

### Get a Token

```bash
# Client credentials grant (machine-to-machine)
curl -X POST https://aiplex.example.com/oauth2/token \
  -d grant_type=client_credentials \
  -d client_id=tutor-agent-a1b2c3 \
  -d client_secret=ory_secret_*** \
  -d scope="mcp:tools:search_curriculum llm:model:gemini-2.5-flash"
```

### Call an MCP Tool

```bash
curl -X POST https://aiplex.example.com/mcp/kb-search/mcp \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "tools/call",
    "params": {
      "name": "search_curriculum",
      "arguments": {"query": "projectile motion"}
    },
    "id": 1
  }'
```

### Call an LLM

```bash
curl -X POST https://aiplex.example.com/llm/v1/chat/completions \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -H "X-Model-Id: gemini-2.5-flash" \
  -d '{
    "messages": [{"role": "user", "content": "Explain projectile motion"}],
    "model": "gemini-2.5-flash"
  }'
```

### Delegate to Another Agent

```bash
curl -X POST https://aiplex.example.com/a2a/research-agent \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "task_type": "research",
    "input": {"topic": "projectile motion", "depth": "comprehensive"}
  }'
```

## Connect MCP Clients

### Claude Code / Claude Desktop

```json title="claude_desktop_config.json"
{
  "mcpServers": {
    "aiplex-tools": {
      "url": "https://aiplex.example.com/mcp/kb-search",
      "headers": {
        "Authorization": "Bearer ${AIPLEX_TOKEN}"
      }
    }
  }
}
```

### Cursor

```json title=".cursor/mcp.json"
{
  "mcpServers": {
    "aiplex-tools": {
      "url": "https://aiplex.example.com/mcp/kb-search",
      "headers": {
        "Authorization": "Bearer ${AIPLEX_TOKEN}"
      }
    }
  }
}
```

## User Delegation (Consent)

When an agent needs to act on behalf of a user (not just as itself), AIPlex uses the OAuth authorization code flow with Hydra's consent mechanism:

1. Agent redirects user to AIPlex login
2. User authenticates via Ory Kratos (Google, Azure AD, Okta, or local)
3. AIPlex Console shows a consent screen: "tutor-agent wants to access: search_curriculum, generate_quiz"
4. User approves specific scopes
5. Token is issued with `sub` = user, `act.sub` = agent's SPIFFE ID

The **effective permission** is always: Agent Ceiling ∩ User Ceiling ∩ Consent.

See [Scopes and Permissions](/docs/concepts/scopes-and-permissions) for the full model.

## Next Steps

- [Platform Setup](/docs/getting-started/platform-setup) — provision AIPlex on GCP
- [Manage Permissions](/docs/guides/permissions) — fine-tune agent and user access
- [Observability](/docs/guides/observability) — monitor agent activity

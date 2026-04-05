---
sidebar_position: 3
title: Scopes & Permissions
description: The three-dimensional permission model — agent ceiling, user ceiling, and consent.
---

# Scopes & Permissions

AIPlex uses a **three-dimensional permission model**. Every token's effective permissions are the intersection of three independently managed dimensions.

## The Three Dimensions

| Dimension | What | Who Configures | Stored In |
|-----------|------|----------------|-----------|
| **A: Agent Ceiling** | Maximum tools/tasks/models an agent can ever use | Admin | Hydra client `allowed_scopes` |
| **B: User Ceiling** | Maximum tools/tasks/models a user can access | Admin | AIPlex API (Firestore) |
| **C: Delegation** | What the user actually consented to for this session | User at runtime | Hydra consent + token |

**Effective permission = A ∩ B ∩ C**

### Example

```
Agent Ceiling (A): search_curriculum, generate_quiz, grade_assignment
User Ceiling  (B): search_curriculum, generate_quiz
User Consent  (C): search_curriculum

Effective:         search_curriculum
```

The agent *could* use `grade_assignment` (dimension A), but the user doesn't have access to it (dimension B). The user *could* use `generate_quiz` (dimensions A and B), but didn't consent to it this session (dimension C).

## Scope Namespace

All scopes follow a unified naming convention:

```
{plane}:{resource_type}:{resource_name}
```

### MCPlex Scopes

```
mcp:tools:{tool_name}          # Tool-level access
mcp:server:{server_id}         # Server-level access (all tools)
```

### A2APlex Scopes

```
a2a:task:{task_type}           # Task delegation type
a2a:agent:{agent_id}           # Direct agent access
```

### LLMPlex Scopes

```
llm:model:{model_id}           # Model access
llm:capability:{capability}    # Capability-level (vision, code-exec)
```

### Example Token

```json
{
  "scope": "mcp:tools:search_curriculum mcp:tools:generate_quiz a2a:task:research llm:model:gemini-2.5-flash"
}
```

This token grants access to two tools, one A2A task type, and one model — spanning all three planes.

## Managing Permissions

### Dimension A: Agent Ceiling

Set when registering an agent:

```bash
# Grant specific scopes to an agent
aiplex agents register \
  --name tutor-agent \
  --grant mcp:tools:search_curriculum \
  --grant mcp:tools:generate_quiz

# Update later
aiplex agents grant tutor-agent mcp:tools:grade_assignment
aiplex agents revoke tutor-agent mcp:tools:generate_quiz
```

### Dimension B: User Ceiling

Set by an admin for each user:

```bash
# Grant scopes to a user
aiplex users grant student@school.edu \
  --scope mcp:tools:search_curriculum \
  --scope mcp:tools:generate_quiz \
  --scope llm:model:gemini-2.5-flash
```

### Dimension C: Delegation (Consent)

Happens at runtime during the OAuth authorization code flow. The AIPlex Console shows a consent screen listing the requested scopes. The user checks the ones they approve.

This dimension is not configured ahead of time — it's a real-time user decision.

## How It's Enforced

```
Request arrives at Envoy AI Gateway
    │
    ▼
ext_authz → aiplex-authz (Rust)
    │
    ├── Decode JWT
    ├── Extract scopes from token
    ├── Match action against scopes:
    │     tools/call "search_curriculum"
    │       → needs mcp:tools:search_curriculum
    │       → token has it? ✓ Allow
    │
    └── No match? → 403 Forbidden
```

The policy engine only sees the JWT. The three-dimension intersection was computed at token issuance time by AIPlex's consent handler. At enforcement time, it's a simple set membership check.

## Discovery Bypass

Discovery operations always pass authorization:

- `initialize`, `tools/list`, `resources/list` (MCP)
- `tasks/list`, `agents/list` (A2A)
- `models/list` (LLM)
- `ping`

Agents can discover what's available, but can only *use* what their scopes allow.

## Next

- [Identity and Zero Trust](/docs/concepts/identity-zero-trust) — SPIFFE IDs and mTLS
- [Gateway Routing](/docs/concepts/gateway-routing) — how Envoy routes across planes

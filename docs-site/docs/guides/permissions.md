---
sidebar_position: 5
title: Permissions
description: Manage the three dimensions of access control across all planes.
---

# Permissions

AIPlex permissions work in three dimensions. This guide covers how to manage each one.

## Quick Reference

| Dimension | Controls | Managed By | How |
|-----------|----------|------------|-----|
| **A: Agent Ceiling** | Max scopes an agent can ever have | Admin | `aiplex agents grant/revoke` |
| **B: User Ceiling** | Max scopes a user can access | Admin | `aiplex users grant/revoke` |
| **C: Delegation** | What user consented to this session | User | Consent screen at login |

**Effective = A ∩ B ∩ C**

## Dimension A: Agent Ceiling

Set the maximum capabilities for each agent:

```bash
# At registration
aiplex agents register --name my-agent \
  --grant mcp:tools:search \
  --grant llm:model:gemini-2.5-flash

# Add later
aiplex agents grant my-agent --scope a2a:task:research

# Remove
aiplex agents revoke my-agent --scope mcp:tools:search

# View current ceiling
aiplex agents permissions my-agent
```

### Via Console

**Agents** tab → select agent → **Edit Permissions** → check/uncheck scopes.

## Dimension B: User Ceiling

Control what tools, tasks, and models each user can access:

```bash
# Grant user access to specific scopes
aiplex users grant student@school.edu \
  --scope mcp:tools:search_curriculum \
  --scope mcp:tools:generate_quiz \
  --scope llm:model:gemini-2.5-flash

# Revoke
aiplex users revoke student@school.edu \
  --scope mcp:tools:generate_quiz

# View user permissions
aiplex users permissions student@school.edu
```

### Via Console

**Permissions** page → select user → manage scopes by plane.

## Dimension C: Delegation (Consent)

When an agent initiates an OAuth flow on behalf of a user, the AIPlex Console shows a consent screen:

```
┌──────────────────────────────────────────┐
│  tutor-agent wants to:                    │
│                                           │
│  ☑ Search curriculum (MCPlex)             │
│  ☑ Generate quizzes (MCPlex)              │
│  ☐ Grade assignments (MCPlex)             │
│  ☑ Use Gemini 2.5 Flash (LLMPlex)        │
│                                           │
│  Only scopes you have access to are shown │
│                                           │
│  [Approve]  [Deny]                        │
└──────────────────────────────────────────┘
```

The consent screen only shows scopes that pass **both** dimension A (agent can use it) and dimension B (user has access). Users can selectively approve.

## Bulk Permission Management

### Via YAML

```yaml title="aiplex.yaml"
agents:
  - name: tutor-agent
    grant:
      - mcp:tools:search_curriculum
      - mcp:tools:generate_quiz
      - a2a:task:research
      - llm:model:gemini-2.5-flash

  - name: assessment-agent
    grant:
      - mcp:tools:grade_assignment
      - mcp:tools:generate_quiz
      - llm:model:gemini-2.5-flash
```

```bash
aiplex apply -f aiplex.yaml
```

### Preview Changes

```bash
aiplex diff -f aiplex.yaml
```

Shows what would change before applying.

## Audit Trail

Every permission change is logged:

```bash
# View permission change history
aiplex history --type permission

# View for a specific agent
aiplex history --agent tutor-agent
```

## Next

- [Catalog](/docs/guides/catalog) — browse and manage templates
- [Observability](/docs/guides/observability) — monitor policy denials

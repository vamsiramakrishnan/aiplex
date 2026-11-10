---
title: Access points and the single pane
description: Create access points, connect clients to them, and see every caller and what it can reach.
---

# Access points and the single pane

An [access point](../concepts/access-points.md) is one stable URL in front of models, tools and skills. The single pane is the overview API plus the `aiplex who` and `aiplex connect` commands.

## Manage access points

```bash
aiplex access-points list
aiplex access-points apply -f ide.yaml
aiplex access-points get ide-platform-team
aiplex access-points quota-plan ide-platform-team
aiplex access-points delete ide-platform-team
```

`apply` creates the access point named by the file's `id`, or replaces it using the current revision so a concurrent change is not overwritten. `quota-plan` renders the QoS block as QuotaPlex controls for review and activates nothing. Specification fields and rules: [Access points and the single pane](../library/access-points.md#specification).

## Connect a client

```bash
aiplex connect ide-platform-team --client claude-code
```

## See every caller

```bash
aiplex who --kind agent
aiplex who agent tutor-agent
aiplex who user PRINCIPAL_ID --since 7d
```

`aiplex who` shows how a caller authenticates, the models, tools and skills it can reach with the reason for each, quota coverage and recent usage. Unknown is shown as "unknown", never as zero. There is no user directory: known users are those named by an access point or seen in the usage ledger.

## Requirements

Model routes need `llmplex`, MCP routes need `mcplex`, skills need `skillsplex`, QoS needs QuotaPlex, and `connect` needs `AIPLEX_PUBLIC_URL`. Details: [Enable](../library/access-points.md#enable). Callers authenticate through [agent identity federation](../library/agent-identity-federation.md) or [user identity federation](../library/user-identity-federation.md), and every exchange is recorded in the [usage ledger](../library/usage-ledger.md).

API: [Access points](../api/access-points.md) and [Overview pane](../api/overview-pane.md).

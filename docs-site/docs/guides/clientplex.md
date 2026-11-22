---
title: Harnesses and ClientPlex
description: Configure a coding harness for an access point or review a registered client's configuration plan.
---

# Harnesses and ClientPlex

A harness runs a coding agent’s work session. To point an installed harness at a model and MCP tools, use `aiplex connect` with an access point. For example, preview the settings Codex would use with the local stack.

```bash
aiplex connect local --client codex --write --dry-run
```

The CLI shows the file diff and any shell variables you need to set. Apply the settings with `--write`, then run a turn in the harness. [Connect a harness](../getting-started/connect-client.md)

## Review a client registration

Use ClientPlex when an operator needs to save a client’s profile, principal binding and destination for review. Profiles cover coding harnesses and other clients, including Claude Desktop with its native Claude apps gateway sign-in.

```bash
aiplex clients profiles
aiplex clients register -f registration.json
aiplex clients plan REGISTRATION_ID
```

Choose a profile first, then supply its registration JSON. In gateway mode, ClientPlex resolves the destination in your tenant and checks protocol compatibility. In direct mode, the client uses its native cloud authentication. The plan returns configuration fragments, credential references and required hosts for review. A plan marked `configured-unverified` still needs a connection test from the installed client. [Profile and mode compatibility](../library/clientplex.md#client-contracts) · [Registration and revision rules](../library/clientplex.md#api-and-persistence)

---
title: A2APlex
description: "Govern agent-to-agent access: deploy A2A agents, publish Agent Cards, connect native cloud agents and read delegation history."
---

# A2APlex

A2APlex governs agent-to-agent traffic (A2A, HTTP/JSON). It has three parts:

- **Deployed A2A agents.** Deploy an agent from the catalog; the platform creates the workload and an `HTTPRoute`. Each deployed agent publishes an Agent Card at `/a2a/{instanceId}/.well-known/agent.json`.
- **Native cloud agents.** Google, AWS, Foundry and Copilot agents are reached through tenant-scoped agent connections with actor-separated sessions.
- **Reported delegation history.** Delegation records show who delegated to whom.

Opaque agent-internal model and tool calls need their own instrumentation to appear in history.

## Commands

```bash
aiplex catalog list --plane a2aplex
aiplex deploy TEMPLATE_ID --plane a2aplex --name my-agent
aiplex a2a agents              # running A2A agents
aiplex a2a card INSTANCE_ID    # Agent Card for a deployed agent
aiplex get delegation ID
```

`aiplex a2a delegate` records a delegation. The record is reported history: it does not execute a task or authenticate the reported subject, and it does not prove a policy decision. Read [A2A and Skills discovery and reported history](../library/a2a-skills-audit.md).

## Native agent connections

Native agent invocation is the `agent-connection` resource under `/api/v1/a2a/connections`; see [Connected Plexes](../library/connected-plexes.md). Native identity and registries (Google Agent Registry, AWS AgentCore Identity, Microsoft Entra Agent ID) belong to Identity: [Native agent governance](../library/native-agent-governance.md).

## Related

- [Identity guides](./identity.md) for how agents authenticate.
- [Durable runs](./runs.md) for Tape-backed run history.

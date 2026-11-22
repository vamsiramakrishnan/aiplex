---
title: A2APlex
description: "Govern agent-to-agent access: deploy A2A agents, publish Agent Cards, connect native cloud agents and read delegation history."
---

# A2APlex

Use A2APlex to invoke an existing agent, deploy one in a cluster or inspect a reported delegation.

- **Deployed A2A agents.** Deploy an agent from the catalog; the platform creates the workload and an `HTTPRoute`. Each deployed agent publishes an Agent Card at `/a2a/{instanceId}/.well-known/agent.json`.
- **Native cloud agents.** Call Google, AWS, Foundry and Copilot agents through tenant-scoped connections. Each actor gets a separate session.
- **Reported delegation history.** Delegation records show who delegated to whom.

## Try the local agent

With `aiplex dev` running, invoke the seeded agent and inspect its usage:

```bash
aiplex a2a invoke local-agent --operation query
aiplex usage records --plane a2aplex
```

The mock runtime replies `mock-agent: hello`. The ledger records the managed invocation. Instrument a remote agent separately to observe its internal calls.

## Commands

```bash
aiplex catalog list --plane a2aplex
aiplex deploy TEMPLATE_ID --plane a2aplex --name my-agent
aiplex a2a agents              # running A2A agents
aiplex a2a card INSTANCE_ID    # Agent Card for a deployed agent
aiplex get delegation ID
```

Delegation history, with filters and updates:

```bash
aiplex a2a delegations list --agent AGENT_ID --limit 20
aiplex a2a delegations get ID
aiplex a2a delegations chain ID
aiplex a2a delegations update ID --status failed --error 'upstream timeout'
```

`update` replaces the stored error text, so a record that already has one needs `--error TEXT` or `--clear-error`. A completed or failed record cannot change.

`aiplex a2a delegate` records a delegation. The record is reported history: it does not execute a task or authenticate the reported subject, and it does not prove a policy decision. Read [A2A and Skills discovery and reported history](../library/a2a-skills-audit.md).

## Native agent connections

Native agent invocation is the `agent-connection` resource under `/api/v1/a2a/connections`; see [Connected Plexes](../library/connected-plexes.md). Create a connection with `aiplex resources apply agent-connection ID -f connection.yaml`, then:

```bash
aiplex a2a connections list
aiplex a2a invoke CONNECTION --operation invoke --payload '{"input":"hello"}'
aiplex a2a invoke CONNECTION --operation invoke --payload-file request.json -o json
aiplex a2a connections delete CONNECTION --yes
```

The reply is written as it arrives, so a streamed reply appears incrementally; `-o json` waits for it and prints the status, content type, request id and body. Select an operation the provider supports. The caller needs the grant `a2a:agent:CONNECTION:OPERATION`. AIPlex forwards the payload without logging it. Live cloud agent invocations still require qualification. Native identity and registries (Google Agent Registry, AWS AgentCore Identity, Microsoft Entra Agent ID) belong to Identity: [Native agent governance](../library/native-agent-governance.md).

## Related

- [Identity guides](./identity.md) for how agents authenticate.
- [Durable runs](./runs.md) for Tape-backed run history.

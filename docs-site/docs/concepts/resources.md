---
title: Resources
description: "How AIPlex represents agents, models, tools and skills."
---

# Resources

Agents can act as callers and as destinations. A harness uses its configured endpoints to reach the resources its caller may access.

| Resource | Module | How it is represented |
| --- | --- | --- |
| **Agent** | [A2APlex](../guides/a2aplex.md) | A deployed A2A agent or a native cloud-agent connection. |
| **Model** | [LLMPlex](../guides/llmplex.md) | A saved model surface: an operator-approved provider origin, credential and an allowlist of native operations and models. See [Native model surfaces](../library/model-surfaces.md) and [Model providers](../library/model-providers.md). |
| **Tool** | [MCPlex](../guides/mcplex.md) | A managed MCP tool connection, with an upstream `auth` block and a `tools` policy. See [Tool connection auth](../library/tool-connection-auth.md). |
| **Skill** | [SkillsPlex](../guides/skillsplex.md) | A deployed skill instance with a manifest of capabilities. |

## Scopes

Access to a resource is expressed as scopes in the caller's token. The prefixes that the authorization policy checks are:

```text
mcp:tools:{tool_name}      tool-level access
a2a:task:{task_type}       agent task delegation types
llm:model:{model_id}       model access
skill:invoke:{skill_name}  skill invocation
aiplex:runs:{action}       durable run inspection and operator actions
```

The policy that checks them is in [`policies/aiplex_authz.rego`](https://github.com/vamsiramakrishnan/aiplex/blob/main/policies/aiplex_authz.rego); see [Authorization policy](../operations/authorization-policy.md).

## Catalog and instances

Tools, agents and model providers can be deployed from a template catalog (`aiplex catalog list`, `aiplex deploy`). A deployed template is an instance in a plane. Declarative files (`aiplex apply -f`) describe instances, agents and model routes together; see [Deploy a tool](../getting-started/first-deploy.md).

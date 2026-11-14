---
title: Identity
description: Agent federation, user federation and directories, and native cloud governance.
---

# Identity

Identity is shared by every module. These guides cover how callers authenticate and how native cloud identity is governed.

## Agents

[Agent identity federation](../library/agent-identity-federation.md) lets a workload running outside AIPlex authenticate as a registered agent with a token its own cloud or cluster issued. A binding ties one external subject (a GitHub Actions ref, an AWS IAM role ARN or an Azure managed identity object ID) to one registered agent client. Presets derive the exact issuer, JWKS URL, audience, subject and required claims, so no wildcard trust is written by hand.

```bash
aiplex agents register --help
aiplex agents identity --help
aiplex agents permissions AGENT_ID
```

## Users and directories

[User identity federation](../library/user-identity-federation.md) lets people sign in with a token from Google Workspace, Microsoft Entra ID, Okta, a Google Workforce pool or any OIDC issuer that signs RS256, ES256 or ES384 tokens. [Authoritative membership](../library/membership-authority.md) adds a signed directory that proves a subject's complete group view.

```bash
aiplex identity user-providers --help
```

## Native cloud governance

Identity connects to Google Agent Registry and IAM, AWS AgentCore Identity and Microsoft Entra Agent ID through scoped inventory, reviewed policy plans and durable apply receipts.

| Page | Covers |
| --- | --- |
| [Native agent governance](../library/native-agent-governance.md) | Providers, credential brokers, review and apply, limits |
| [Google](../library/google-agent-governance.md) | Google Agent Registry and IAM |
| [AWS](../library/aws-agent-governance.md) | AWS AgentCore Identity |
| [Microsoft Entra](../library/entra-agent-governance.md) | Entra Agent ID |
| [Native governance controller](../library/native-governance-controller.md) | The controller that drives reviewed apply |

```bash
aiplex native governance providers
aiplex native governance plan --help
```

## Configuration

The API requires pinned identity configuration before startup: [Mount identity and connection configuration](../library/helm-identity-config.md).

API: [Identity: agents](../api/identity-agents.md) and [Identity: users](../api/identity-users.md).

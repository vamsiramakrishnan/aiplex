# Native cloud agent connections

These adapters authorize a tenant/principal/resource/action before obtaining
credentials or performing network calls. Callers supply the trusted request
context, a policy-constrained HTTP client, and a refresh-capable authenticator.
Responses retain native status, headers and streaming bodies.

| Provider | Invocation surface | Authentication |
|---|---|---|
| Google Agent Runtime | reasoningEngines query / streamQuery | Injected Google OAuth or native Agent Identity transport |
| Bedrock Agents Classic | agent/alias/session InvokeAgent | Official AWS SDK SigV4 signer |
| Bedrock AgentCore Runtime | ARN invocation and native session header | Official AWS SDK SigV4 signer or configured OAuth bearer |
| Microsoft Foundry | project Responses or named-agent Responses endpoint | Entra token for `https://ai.azure.com/.default` |
| Copilot Studio | DirectLine conversation/activity API | Configured DirectLine credential source |

`GovernanceMode=invocation` is explicit. Registering a connection governs calls
through this adapter; it does not intercept the managed agent's internal tools,
models, or egress. Native gateways and networking must be configured for those
additional enforcement points.

Bedrock and AgentCore logical sessions are scoped to tenant, principal and
connection using deterministic hashed native IDs. Copilot Studio's server-issued
conversation IDs use the mandatory `SessionStore`.
`CopilotTokenCredentials` retrieves a token from the published Copilot token
endpoint **only for start-conversation**. It refuses send/get continuation before
contacting that endpoint, because a freshly generated token would belong to a
different conversation. Continuation requires a host-owned authenticator which
resolves the stored token for the already-authorized conversation (and handles
refresh), or an approved DirectLine server secret. SessionStore tracks ownership;
it is not itself a secret store or token refresher. A production store should
persist ownership; the provided MemorySessionStore fails closed on unknown
conversations after restart. Never trust an arbitrary conversation ID as proof
of ownership. Named Foundry agent connections reject payload selectors which
would choose another agent or model.

Native Google Agent Identity is provided by `pkg/agentidentity` and the receiving
API adapter in `internal/auth/native_agent.go`. It verifies pinned STS pool
issuer, audience, allowlisted SPIFFE subject and (when bound) the actual verified
mTLS client certificate fingerprint and URI SAN. Unbound mode is explicit;
forwarded certificate headers are not accepted. GKE provisioning is rendered by
`pkg/networking`. Ordinary Go ADC must not be described as automatically issuing
bound Agent Identity tokens; supply the appropriate native token/mTLS transport.

`pkg/networking` emits reviewable Kubernetes patch/policy JSON and Terraform
JSON. It never applies resources. Plans identify their coverage: AWS interface
endpoints, Azure Foundry private ingress/DNS, GKE identity registration, or
Kubernetes CIDR egress. Private connectivity by itself is not authorization or
an assurance that agent-internal traffic traverses Aiplex.

Official contracts:

- https://docs.cloud.google.com/iam/docs/agent-identity-overview
- https://docs.cloud.google.com/iam/docs/auth-agent-own-identity-external
- https://docs.cloud.google.com/kubernetes-engine/security/request-agent-identity
- https://docs.cloud.google.com/kubernetes-engine/security/authenticate-agent-identity
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.reasoningEngines/query
- https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.reasoningEngines/streamQuery
- https://docs.aws.amazon.com/bedrock/latest/APIReference/API_agent-runtime_InvokeAgent.html
- https://docs.aws.amazon.com/bedrock-agentcore/latest/APIReference/API_InvokeAgentRuntime.html
- https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/vpc-interface-endpoints.html
- https://learn.microsoft.com/en-us/azure/foundry/reference/foundry-project
- https://learn.microsoft.com/en-us/azure/foundry/agents/how-to/virtual-networks
- https://learn.microsoft.com/en-us/microsoft-copilot-studio/publication-connect-bot-to-custom-application
- https://learn.microsoft.com/en-us/azure/bot-service/rest-api/bot-framework-rest-direct-line-3-0-api-reference

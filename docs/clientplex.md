# ClientPlex

ClientPlex owns developer-client registrations and configuration planning.
`Registry.Register(Profile, Renderer)` adds trusted host-owned client adapters per
composition. Duplicate profiles fail, metadata is copied, and no registry API
loads code from remote metadata. API hosts can select a registry with
`ClientHandler.WithProfiles`; built-in registration remains the default. It is
separate from A2APlex agent execution, LLMPlex model operations, and identity
verification. Profiles describe client-native protocols; registrations bind a
profile to a principal, a cloud project or a tenant-owned destination reference.

## API and persistence

- `GET /api/v1/clients/profiles`
- `GET /api/v1/clients/registrations`
- `GET|PUT|DELETE /api/v1/clients/registrations/{id}`
- `POST /api/v1/clients/registrations/{id}/plan`

The module stores `client-registration` records through `pkg/resources.Store`.
Tenant selection comes from the host's verified context, never the request body.
Optional `network_profile_ref` must resolve to a network profile in that same
tenant at registration and again when planning. A deleted profile blocks plans.
PUT creation uses revision zero; updates use `If-Match: "<revision>"`. DELETE
requires the current revision. Configuration plans resolve destination references
in that same tenant, validate protocol compatibility and render fragments. They
do not execute commands, fetch credentials or write workstation files. Secret
references and target environment names are returned separately from artifacts.
The CLI's separate, opt-in `aiplex connect --write` is what edits a client's config
file ([harness onboarding](harness-onboarding.md)); ClientPlex plans never do.

The Console provides profile discovery, revision-aware registration editing and
plan review. The SDK and `aiplex clients` commands use the same API.

## Client contracts

An Anthropic Messages destination can expose an installed native Messages
operation or explicit Vertex rawPredict operations. The native operation keeps
its body/path/headers; Vertex translation validates the original JSON before
re-encoding it. Protocol compatibility checks include transport, action and
resource ownership semantics, not just an operation name.

Console plan/save responses stay bound to their original review/editor intent.
A late result cannot replace a newer plan or reopen a discarded editor. Completed
mutations still refresh the saved registration inventory.

| Profile | Direct Google Cloud | Gateway |
|---|---|---|
| Antigravity App/CLI | ADC or enterprise workforce sign-in | GenAI or Chat Completions |
| Antigravity ACP | Enterprise client flow | GenAI only |
| Codex | Built-in `gcp` provider, project/location, optional ADC file | Responses-compatible endpoint |
| Claude Code | Vertex provider configuration with ADC | Anthropic Messages, Vertex rawPredict, or native Claude apps gateway |
| Claude Desktop | Native Claude apps gateway bootstrap | Native gateway only |

Codex's GCP provider and custom authentication are documented in
[official OpenAI configuration docs](https://learn.chatgpt.com/docs/config-file/config-advanced).
A configurable model name does not prove launch availability or project access.
The November 17 announcement is not an activation switch in this implementation.

Antigravity gateway mode uses a centralized service account and consumption
billing, separately from seat-based included quotas. Plans preserve the required
attribution headers and identify streaming requirements. Direct enterprise plans
include global `businessaicode.googleapis.com` and `cloudcode-pa.googleapis.com`
client-service dependencies even when inference is regional. These are not
invented model inference APIs. See [enterprise setup](https://antigravity.google/docs/enterprise/)
and [gateway deployment](https://docs.cloud.google.com/gemini/enterprise/docs/ai-developer-tools/deploy-enterprise-gateway).

Claude Code's generic gateway [protocol guide](https://code.claude.com/docs/en/llm-gateway-protocol)
requires provider-native paths, streaming events and beta/version headers.
[Claude apps gateway](https://code.claude.com/docs/en/claude-apps-gateway) has its
own sign-in and bootstrap protocol. CLI managed login settings and Desktop's
`bootstrapUrl` are rendered separately. Browser sign-in is required; unattended
CI should use provider credentials directly.

## Claude apps gateway server artifacts

A native gateway registration can include a complete `gateway_server` spec.
`RenderClaudeGateway` then renders `gateway.yaml`, the command arguments, secret
bindings and network requirements for the separately supplied Claude binary.
The renderer requires a pinned corporate OIDC issuer, group claim and scopes,
distinct environment references for secrets, a private ingress allowlist,
PostgreSQL hostname, explicit model mappings, group rules and HTTPS OTLP collector.
The Vertex upstream uses ADC. Metrics are enabled; logs/traces require explicit
opt-in. Partial configurations produce errors rather than runnable-looking YAML.

The server artifact follows [Anthropic's gateway config](https://code.claude.com/docs/en/claude-apps-gateway-config)
and [Google Cloud deployment example](https://code.claude.com/docs/en/claude-apps-gateway-on-gcp).
Aiplex does not distribute the licensed binary or emulate its sign-in service.
Actual installed-version schema, group matching, IAM and model access must be
verified before deployment. Google Workspace ID tokens do not carry groups;
organizations must supply a reviewed directory integration when using that IdP.

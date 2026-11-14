# Project status: what is implemented, and what is not

AIPlex is **pre-1.0**. Interfaces, flags and output formats can change between releases. This page is the inventory behind the short status summary in the [README](../README.md): what exists, where each part stops, and what has and has not been run against live services.

For the meaning of the module names used below (LLMPlex, MCPlex and so on), see [naming and glossary](naming.md).

## Components

| Component | Language | State |
| --- | --- | --- |
| AIPlex API (`cmd/aiplex-api`) | Go | Runs locally in memory with `aiplex dev`. Firestore-backed storage is covered by tests against the Firestore emulator; it has not been run against managed Firestore. |
| AIPlex CLI (`cmd/aiplex-cli`) | Go | Builds and passes tests. Includes `aiplex dev`. |
| Console (`console/`) | React and TypeScript | Embedded in the CLI binary when built first (`make console`). Its screens are tested against a mocked API. |
| `aiplex-authz` (`authz/`) | Rust | Scope authorization for the gateway `ext_authz` hook; has its own tests in CI. |
| Go SDK (`sdk/`) | Go | Client for the API. |
| Python and TypeScript SDKs (`sdk/python/`, `sdk/typescript/`) | Python, TypeScript | Unpublished clients for the API: reads, and the admin writes the Go SDK has (budgets, model surfaces, tool connections, hook policies, usage configuration). Run against canned servers and the local development stack only (`make sdk-test`); `examples/` has one example for each. |
| Deployment (`deploy/`) | Terraform, Helm, Kubernetes, Ory config | Helm charts are linted in CI. No deployment to a live cloud project is recorded in this repository. |
| Docs site (`docs-site/`) | Docusaurus | Builds with broken links as errors. |

## Modules

| Module | What it does | Boundary |
| --- | --- | --- |
| **MCPlex** | Connect tools, register OAuth clients, authorize users and invoke configured tools | Tool connections, delegated OAuth, eight upstream auth types, allow/deny and read-only tool policy, and stateless JSON/finite-SSE execution. Registration alone does not execute or meter a tool. |
| **A2APlex** | Invoke native cloud agents and inspect delegation history | Google, AWS, Foundry and Copilot connections, actor-separated sessions and reported delegation chains. Opaque agent-internal model/tool calls need their own instrumentation. |
| **LLMPlex** | Configure model routes/surfaces and forward native model operations | HTTP/SSE/Live protocols, client wire facades, OpenAI/Anthropic/Azure/Bedrock/OpenAI-compatible/Gemini Developer provider surfaces, media and [batch](model-batches.md) operations, and stateful resource ownership. Available protocols and models are explicit operator configuration. |
| **ClientPlex** | Connect Antigravity, Codex and Claude clients to supported model endpoints | Client profiles, saved registrations, protocol checks and reviewed configuration fragments. ClientPlex itself writes no workstation files, installs no clients, issues no credentials and grants no model access; the CLI's opt-in `aiplex connect --write` edits the client's config file ([harness onboarding](harness-onboarding.md)). A rendered plan stays `configured-unverified` until the installed client, provider access and deployment are qualified. |
| **SkillsPlex** | Discover deployed skills and inspect invocation history | Skill manifests, capabilities and attributed audit records. |
| **QuotaPlex** | Share budgets and admission controls across supported execution paths | Allocations, prices, funding, budget holds, admitted rates, permits, scheduling and settlement. Opt-in; only explicitly covered paths are protected. |
| **Networking** | Prepare private connectivity, identity and egress configuration | Native network artifacts and optional reviewed Anvil execution. Saved intent, successful apply and observed readiness are separate states. |

The shared identity layer verifies workforce, workload and native-agent credentials and keeps principal and actor memberships separate. Agent tokens verify with RS256, ES256 and ES384, each bound to its key type; the gateway authorizer `aiplex-authz` verifies RS256 only. Identity also connects native Google Agent Registry/IAM, AWS AgentCore Identity and Microsoft Entra Agent ID through scoped inventory, reviewed policy plans and durable apply receipts ([native governance](native-agent-governance.md)). See [module contracts](modules.md).

## Gateway features

| Feature | What it does | Guide |
| --- | --- | --- |
| Request hooks | Redact, size-limit or refuse prompts, responses and tool calls; HTTP sidecar hooks; buffered inspection of streamed Chat Completions, Messages, Responses and Gemini responses | [request hooks](request-hooks.md) |
| Routing, retry, fallback, cache | Priority, weighted or lowest-cost backend order; retries and fallback; circuit breakers; opt-in exact-match response cache | [reliability and routing](reliability-and-routing.md) |
| Budgets | Team and project limits above per-caller quota; a cost budget can opt in to counting priced managed tool calls | [budgets](budgets.md) |
| Tool policy rules | Expressions over tool calls, with a dry run | [policy rules on tool calls](tool-policy-rules.md) |
| MCP Registry import | Import streamable-HTTP remotes from the official registry as tool connections | [MCP Registry import](mcp-registry-import.md) |
| Harness telemetry | Claude Code, Codex and Gemini CLI usage over OTLP, recorded as `reported` ledger records | [harness onboarding](harness-onboarding.md), [observability](observability.md) |
| Price catalogue | Two pinned snapshots (LiteLLM, models.dev); backfill prices records written before a price existed | [usage ledger](usage-ledger.md) |
| Antigravity | `connect --write` writes its MCP server list; model traffic is print-only | [harness onboarding](harness-onboarding.md) |
| Python and TypeScript SDKs | Unpublished clients for the API | [Python](../sdk/python/README.md), [TypeScript](../sdk/typescript/README.md) |
| Request lifecycle | The order of checks for models and tools | [request lifecycle](request-lifecycle.md) |

## QuotaPlex

QuotaPlex supports unequal token, money and request allocations, independent human and agent attribution, fixed, anchored and calendar periods, explicit budget loans, atomic admission, rate limits, concurrency and weighted scheduling. LLMPlex and MCPlex keep their native protocols; QuotaPlex owns admission and accounting. A request-count profile measures the managed exchange; it does not measure an async job's lifetime or an agent's internal work.

| Capability | Implemented | Still missing |
| --- | --- | --- |
| Membership | Verified group claims, a signed authority, and on-demand directory group lookups for user identity providers (Entra, Google Cloud Identity, Okta); independent principal/actor views | Native directory synchronization and distributed revocation event consumers |
| Allocations | Unequal budgets, exact prices, supported periods and explicit loans | Rolling budgets, split charging, currency conversion, automatic lending and population rebalance |
| Model/tool metering | Supported native text profiles, with each provider format's observation status in the [usage-format coverage matrix](reference/usage-formats/coverage.md); media-unit and batch-usage bindings ([batches](model-batches.md)); stateless JSON/finite-SSE tool call counts; WebSocket sessions as one request-count admission with advisory usage, or per-response and allowance-window token metering on opt-in surfaces ([details](model-providers.md#websocket-sessions)); live voice sessions by session seconds on opt-in surfaces ([details](model-metering-profiles.md)); cache-write tokens per time-to-live | Async job billing, Gemini Live audio by time, cache and server-tool profiles, tool rows/bytes/duration, and token metering for non-Messages Bedrock InvokeModel bodies |
| Recovery | Durable evidence, restart-safe settlement and reviewed reconciliation | Native provider status/usage lookup adapters; long-running renewal and acknowledged cancellation |
| Enforcement coverage | Exact bindings on supported managed API paths; access-point QoS that fails closed without a covering control | Automatic Envoy/direct-route interception and authenticated remote admission/settlement RPC |
| Operations | Policy preview/export, attempt/balance inspection, loans and corrections in API/CLI/Console; a report of attempts left in the unknown state (count, age and exposure held, as an API filter, a CLI command, gauges and a threshold warning) | Isolated shadow evaluation, runtime coverage qualification and notification delivery for aging unknown exposure |
| Throughput | Shared admitted-demand rates, concurrency and atomic weighted capacity admission; opt-in provider-limit cooldown and headroom gate per surface and replica ([details](model-providers.md#capacity-policy)) | Provider capacity reservation, stream token pacing, preemption and provider minimum-throughput guarantees |

Existing Envoy routes and direct provider calls do not automatically pass through this quota path. Admitted tokens/s and requests/s are separate from actual provider generation throughput. Hard limits need verified provider bounds, prices, identity sources and routing for each enabled profile.

## Usage coverage

The [usage-format coverage matrix](reference/usage-formats/coverage.md) lists, per provider, surface and transport, whether usage is `exact`, `advisory` or `unsupported`. It has 49 exact rows, 10 advisory and 4 unsupported. "Exact" means the gateway reproduces the recorded fixture; the fixtures are recorded or documented provider responses, not live calls.

## Validation

What has been validated, from the repository's own tests and CI:

- Go unit and integration tests, an end-to-end suite that runs the real `aiplex-api` and `aiplex` binaries against local mock upstreams (`make e2e`), and a scripted first run (`make demo`).
- Provider usage formats replayed from fixtures built from provider documentation.
- Firestore transactions and recovery against the official Firestore emulator, and budgets on the Firestore stores with two engines sharing them (no overshoot under concurrent admission, one settlement or release per hold, one threshold event, unknown cost counted as unknown, the same balances after a restart).
- Console tests and build, Helm lint, Rust tests, and JSON schema checks.
- Config merge, diff, backup and undo for the three client files, the stdio MCP server, OpenTelemetry export to an in-process OTLP receiver, the Google Cloud preflight against a fake Google API, and the price catalogue import. Each uses isolated homes, fixtures or fakes.

Exercised against live services ([details](model-providers.md#exercised-against-live-providers), [harness onboarding](harness-onboarding.md#validation-status)):

- The Gemini Developer API and z.ai (as `openai-compatible` and through the `anthropic` provider). Ledger usage matched the provider's responses.
- Claude Code, Gemini CLI and Codex loading the files `aiplex connect --write` writes. Codex was run against the mock model only. Antigravity CLI loaded its MCP server entry; a model turn was not observed.
- Claude Code telemetry sent to the OTLP receiver and recorded as reported usage.

Not validated:

- Any run against live OpenAI or Anthropic first-party endpoints, or against Azure, Bedrock or Vertex.
- Any comparison of AIPlex's usage or cost figures against a provider invoice, and the Presidio and LLM Guard adapters against the real services.
- A deployment to a live cloud project, and load, availability or backup-restore behaviour. The Google Cloud permissions table, the preflight, the custom deployer role and the narrowed runtime grants have not been run against a live project.
- macOS and Windows; the container image build and push; the Homebrew cask; budgets on managed Firestore (they have run on the emulator only).
- Features built from provider documentation and run only against fixtures and fake upstreams: Gemini Live on the Gemini Developer API (API-key header authentication on that endpoint was not observed working), DeepSeek usage semantics, live voice metering by seconds against OpenAI's billing, ES384 agent tokens against a live AWS account, and buffered inspection of Gemini and Responses streams.
- Identity federation against a real identity provider through `aiplex dev`: its three issuers are loopback stand-ins. Console screens against a live deployment. `aiplex a2a invoke` against a connected cloud agent, and `aiplex tool-auth register` against a live authorization server.
- OpenTelemetry export to a real collector, Jaeger, Cloud Trace or Cloud Monitoring. The telemetry overhead has not been measured.
- The price catalogue is a third-party snapshot (LiteLLM, retrieved 2026-11-10; see [`THIRD_PARTY_NOTICES.md`](../THIRD_PARTY_NOTICES.md)). Its prices have not been checked against provider price pages, and discounts and commitments are not reflected.
- Cost shown by `aiplex dev` is from placeholder prices. The local development server is unauthenticated and must stay on loopback.

See [SECURITY.md](../SECURITY.md) for the security model's current limits.

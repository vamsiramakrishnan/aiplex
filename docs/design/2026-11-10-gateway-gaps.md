# Closing the gateway gaps

Status: plan, in execution. Date: 2026-11-10. Follows
[harness onboarding, telemetry, price catalogue and least-privilege setup](2026-11-10-onboarding-telemetry-least-privilege.md).

The existing invariants hold for all of it: fail closed, unknown is never zero,
secrets are references, tenant from the verified identity, every route has a
policy entry, and docs say what was not validated.

## Wave 1 (parallel)

| Work | What | Who |
| --- | --- | --- |
| Request hooks | A hook chain on model and tool requests and responses: built-in redaction rules and an HTTP sidecar hook (Presidio and LLM Guard shaped). A hook that is configured and unreachable refuses the request. Decisions are recorded in the ledger and spans without content. | Opus |
| Live validation | Run real requests through Gemini, OpenAI and an OpenAI/Anthropic-compatible provider with real keys; load the files `connect --write` produces in real Claude Code, Gemini CLI and Antigravity; add Antigravity `--write` if its config location can be established. Fix what breaks; record what was run. | Sonnet |
| Deployment | In-cluster deploys through the Kubernetes API instead of `kubectl`; idempotent `aiplex apply`; `platform apply` applies the Gateway manifest; the authz sidecar reports denials to an endpoint that accepts them; Terraform placeholders become variables. | Sonnet |
| Telemetry and ledger | OTLP receiver that records harness-reported usage in the ledger as `source=reported`; `span_id` in the Console; trace context in MCP `_meta`; agent-plane token metrics; feedback and experiment tags on records. | Sonnet |
| SDKs | Python and TypeScript clients for the API, with tests against `aiplex dev`. | Gemini (gemini-4-argon), reviewed |
| Packaging | Homebrew formula and container images from the release workflow. | GLM via omp (z.ai), reviewed |

## Wave 2 (after request hooks land, same request path)

| Work | What | Who |
| --- | --- | --- |
| Reliability and routing | Runtime retry and fallback across backends with budgets and idempotency rules; cost-aware backend choice; an exact-match response cache that is off by default and never crosses principals unless configured. | Opus |
| Budgets | Team and project budgets above per-caller quota. | Sonnet |
| Metering | Context-length price tiers and modality token rates in price resources; models.dev as a second catalogue source; server-tool usage in the ledger; back-estimation of cost for records that had no price. | Sonnet |
| Tools | Policy expressions on tool calls; import from the MCP Registry. | Sonnet |
| Docs | Guides, CLI reference, README, changelog. | Haiku |

Work delegated to outside models runs in its own worktree without repository
credentials, and is reviewed and tested before it is merged.

## Not planned here

Google Agent Registry and Agent Gateway (pinned); Vertex batch and Veo usage;
Anthropic Managed Agents; moving an existing GKE cluster to another network.

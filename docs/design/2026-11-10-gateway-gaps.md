# Closing the gateway gaps

Status: implemented, with the deviations below. Date: 2026-11-10. Nothing here has been run against a live cloud project or a real collector (see [project status](../../README.md#project-status)). Follows
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

## What landed

| Work | Where it is written up |
| --- | --- |
| Request hooks: redact, size limit, HTTP sidecar (`aiplex.hooks.v1`), Presidio and LLM Guard adapter shapes | [request hooks](../request-hooks.md) |
| Reliability and routing: priority, weighted and lowest-cost order; retries and fallback; circuit breakers; exact-match response cache, off by default | [reliability and routing](../reliability-and-routing.md) |
| Budgets: team and project limits above per-caller quota | [budgets](../budgets.md) |
| Tool policy expressions and MCP Registry import | [policy rules on tool calls](../tool-policy-rules.md), [MCP Registry import](../mcp-registry-import.md) |
| Harness telemetry over OTLP into the ledger as `source=reported`; span ids; trace context in MCP `_meta`; feedback and tags | [observability](../observability.md), [harness onboarding](../harness-onboarding.md), [usage ledger](../usage-ledger.md) |
| Price catalogue from two pinned sources; context-length tiers; service tiers; backfill | [usage ledger](../usage-ledger.md) |
| Kubernetes deploys through the API; idempotent `aiplex apply`; Gateway in the Helm chart | [Google Cloud permissions](../gcp-permissions.md) |
| Python and TypeScript SDKs, unpublished | [Python](../../sdk/python/README.md), [TypeScript](../../sdk/typescript/README.md) |
| Homebrew cask (macOS) in the release workflow; container images built by the release workflow, none published yet | [README](../../README.md#install) |
| Order of checks, written once | [request lifecycle](../request-lifecycle.md) |

## Deviations from the plan

- **Homebrew is a cask for macOS only.** Casks do not install on Linux; Linux uses the install script or the archive.
- **Kubernetes deploys use a small built-in REST client,** not `client-go`, and find resource names through API discovery.
- **CEL adds about 6.2 MB (19%)** to the `aiplex-api` binary.
- **Rolling budget windows are not offered.** QuotaPlex refuses them; use calendar or tumbling windows.
- **The response cache is in memory, per process.** It is not shared between replicas.
- **Response hooks buffer streams for two wires only** (OpenAI Chat and Anthropic Messages SSE). Other streams under a response hook are refused, or pass uninspected only where the policy says so.
- **Price tiers for Azure OpenAI, Bedrock, xAI, Gemini and Vertex Claude are not imported** because their threshold rules were not confirmed.
- **Antigravity gets MCP configuration only.** Model and gateway settings stay print-only.
- **Live validation is narrower than planned.** The Gemini Developer API and z.ai were run; OpenAI and Anthropic first-party endpoints were not (the OpenAI key had no credit). Codex was run against the mock only.

## Not planned here

Google Agent Registry and Agent Gateway (pinned); Vertex batch and Veo usage;
Anthropic Managed Agents; moving an existing GKE cluster to another network.

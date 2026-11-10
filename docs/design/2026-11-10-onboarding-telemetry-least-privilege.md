# Harness onboarding, telemetry, price catalogue and least-privilege setup

Status: implemented. Date: 2026-11-10. The guides are [observability](../observability.md), [harness onboarding](../harness-onboarding.md), [Google Cloud permissions](../gcp-permissions.md) and the [usage ledger](../usage-ledger.md#price-catalogue). Nothing here has been run against a live Google Cloud project, a real OpenTelemetry collector or an installed coding harness.

## What a survey of the repository found

| Area | Today |
| --- | --- |
| Telemetry | No OpenTelemetry in the code. `OTEL_ENDPOINT` is read by nothing, `/metrics` is routed but not served, Helm `otel.enabled` changes nothing, trace context is not forwarded upstream, the ledger `trace_id` is set only when the caller sent one. |
| Harness onboarding | `aiplex connect` prints configuration and writes nothing. No plugin, skill, `AGENTS.md` or management MCP server. Go SDK only. |
| Prices | Operator-supplied only. A new user sees unknown cost for every real model until they write price files by hand. |
| GCP setup | `aiplex init` warns (does not block) unless the account is Owner or Editor; Editor cannot apply the Terraform. Runtime service account has project-wide `secretmanager.secretAccessor` and `datastore.user`. No permission preflight before `platform apply`. |
| Request path | No hook chain, guardrails, runtime retry or response cache. |

## What a survey of comparable projects found

Licences were read from each project's LICENSE file on 2026-11-10.

| Adopt | Source | Licence | How |
| --- | --- | --- | --- |
| Model price and context-window data | LiteLLM `model_prices_and_context_window.json` | MIT (not `enterprise/`) | Vendor a pinned snapshot as data, notice kept |
| Second catalogue for cross-checking | models.dev | MIT | Later |
| Span, metric and attribute names | OpenTelemetry GenAI and MCP semantic conventions | Apache-2.0, status Development | Emit; names are versioned in one file because they will change |
| OTel Go SDK, `otelhttp`, OTLP exporters | OpenTelemetry | Apache-2.0 | Depend |
| Tool discovery format | MCP Registry `server.json` | Apache-2.0 / MIT | Later: import |
| Budget hierarchy, plugin chain | Bifrost (Go) | Apache-2.0 root; per-path licence unverified | Idea only until verified |
| Per-tool policy, token rate limits at the proxy | agentgateway, Envoy AI Gateway | Apache-2.0 | Idea only |
| Guardrail sidecars | Presidio, LLM Guard | MIT | Later: hook, fail closed |

Nothing under an enterprise or commercial licence is copied.

## This increment

### A. Telemetry (Opus)

- OpenTelemetry SDK in `aiplex-api`: OTLP traces and metrics, configured by the standard `OTEL_*` variables; off unless an endpoint is set. `aiplex dev --otel <endpoint>` passes it through.
- A server span per request and a client span per upstream model, tool and agent call, named and attributed per the GenAI and MCP conventions (`gen_ai.operation.name`, `gen_ai.provider.name`, `gen_ai.request.model`, usage counts, `mcp.method.name`, `gen_ai.tool.name`), plus AIPlex attributes (`aiplex.access_point.id`, `aiplex.caller.kind`, `aiplex.principal.id`, quota decision, cost and its basis). Unknown usage is an absent attribute, never zero. No prompt, completion, tool argument or credential content.
- W3C trace context is continued from the caller or started, and forwarded upstream. Every ledger record gets `trace_id` and `span_id`.
- Metrics: request duration, token usage, cost, quota denials; a Prometheus `/metrics` endpoint with a route policy entry.
- Helm `otel.enabled` actually wires the endpoint; the collector image is pinned.

### B. Harness self-configuration (Sonnet)

- `aiplex connect <ap> --client <c> --write`: writes the harness's own configuration (Claude Code `~/.claude/settings.json` `env`, Codex `~/.codex/config.toml`, Gemini CLI settings) with a diff shown first, a timestamped backup, `--dry-run`, and `aiplex connect --undo`. Merges; never replaces a file. Tokens are never written: configuration references `AIPLEX_TOKEN` or a helper command.
- `--with-telemetry` also sets the harness's own OpenTelemetry export variables.
- `aiplex mcp serve`: a stdio MCP server exposing read-only AIPlex tools (who am I, what can I reach, usage, connect configuration) with the caller's own token, so a coding agent can inspect and configure itself.
- A Claude Code plugin (`integrations/claude-code/`: manifest, skills, MCP entry), a Gemini CLI extension, and an `AGENTS.md` describing the onboarding steps for any harness.

### C. Least-privilege GCP setup (Opus)

- `aiplex platform preflight`: checks the exact permissions the Terraform needs with `testIamPermissions`, lists what is missing and the narrowest role that grants each. `platform apply` runs it and refuses on a failure.
- A documented deployer role set (and an optional custom role file) replacing "Owner or Editor".
- Runtime service account: `secretAccessor` bound per secret, Firestore access narrowed where IAM allows, with conditions otherwise.
- No new broad grants; a test fails the build if a project-level admin role or wildcard RBAC appears under `deploy/`.

### D. Price catalogue (Sonnet)

- A pinned, trimmed snapshot of the LiteLLM price data under `pkg/pricecatalog/` with its MIT notice and source commit.
- `aiplex usage prices catalog search|show|import`: the operator chooses models and imports them as ordinary price resources, marked with source and snapshot date. Nothing is priced automatically; an unlisted model stays unknown. Cost basis stays `estimated`.

### E. Docs (Haiku)

Guides for A–D, the CLI reference, README capability table.

## What landed

Verified by reading the guides and the code they describe.

- **A. Telemetry.** As planned: OTLP traces and metrics configured by the standard `OTEL_*` variables and off unless an endpoint is set, `aiplex dev --otel`, SERVER and CLIENT spans, `trace_id` and `span_id` on every ledger record, `GET /metrics` with a route policy entry, and the Helm endpoint wiring. Deviations: upstream trace propagation is on by default (`AIPLEX_TRACE_PROPAGATE_UPSTREAM=false` turns it off); `params._meta.traceparent` is not added to MCP messages; no child span per upstream attempt; `OTEL_ENDPOINT` is kept as a deprecated alias; `/metrics` needs `AIPLEX_METRICS_TOKEN` (503 when unset outside development mode) and is not routed by the public gateway, since a role rule would need an expiring identity token. Agent token usage is on spans and in the ledger, not in the token counters.
- **B. Harness self-configuration.** `aiplex connect --write` for `claude-code`, `codex` and `gemini-cli`, with `--dry-run`, `--undo`, `--with-telemetry` and `--token-helper`; `aiplex mcp serve` with six read-only tools; the Claude Code plugin, the Gemini CLI extension and `AGENTS.md`. Added beyond the plan: `aiplex auth print-token`, which the token helper calls. Deviations: the Gemini CLI base URL is printed as a remaining step, not written, because Gemini CLI has no settings key for it; Antigravity is print-only, since its config location is not documented; the Claude Code credential is an environment variable or `apiKeyHelper`, never a value; Codex edits are confined to delimited blocks; `--with-telemetry` is refused with `--scope project` for Claude Code, which ignores those variables in project settings.
- **C. Least-privilege GCP setup.** `aiplex platform preflight` (`testIamPermissions`, enabled APIs, billing); `platform apply` runs it and refuses on failure; the deployer role table in `internal/gcpsetup/requirements.go` is checked against the Terraform; an optional custom deployer role file; `aiplex init` no longer asks for Owner or Editor; runtime grants narrowed. Deviations: no deny-all egress rule (the API must reach operator-chosen providers on the internet); a custom role for secret writes (`aiplexApiSecrets`) and one for Firestore exports (`aiplexFirestoreExporter`), where predefined roles were broader than needed; Firestore is narrowed to the default database rather than to collections; the Gateway and its Cloud Armor attachment stay manual; the in-cluster deploy engine still shells out to `kubectl`, which the distroless image lacks.
- **D. Price catalogue.** A pinned, trimmed LiteLLM snapshot (MIT, commit `52cd9ef`, retrieved 2026-11-10, 780 models) under `pkg/pricecatalog/`, with its notices, and `aiplex usage prices catalog search|show|import`. Deviations: only token-priced entries are in the snapshot, so per-image, per-second, per-character, per-page and per-query entries are excluded; import refuses pricing it cannot express unless `--accept-partial` is given; models.dev is not used (listed as later).

## Next, not in this increment

Request hook chain (guardrails, redaction, fail closed); runtime retry and fallback; OTLP receiver that maps harness telemetry into the ledger; MCP Registry import; Python and TypeScript SDKs; Homebrew and container image; SPIFFE JWT-SVID preset checks; budget hierarchy (team, project); Google Agent Registry and Agent Gateway (pinned).

# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project intends to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once it reaches 1.0. Until then, minor versions may change interfaces.

## [Unreleased]

There has been no tagged release yet. This section summarises what exists in the repository, grouped by area. Nothing here has been validated against live model providers, provider invoices or a live cloud project; see the README's project status.

### Local development

- `aiplex dev` starts the API in memory on loopback with a built-in mock provider and MCP server, seeds an access point `local`, signs you in as a local admin, and prints next steps. The mock provider has four models, one per wire: `mock-gpt` (OpenAI Chat Completions), `mock-responses` (OpenAI Responses), `mock-claude` (Anthropic Messages) and `mock-gemini` (Gemini generateContent). `aiplex dev env` exports `AIPLEX_URL`, `AIPLEX_TOKEN` and, with the mock provider, the base-URL and key variables read by the OpenAI, Anthropic and Gemini SDKs. `--provider openai|anthropic|gemini --api-key-env NAME` forwards to a real provider.
- `aiplex dev token --user NAME [--role none|deployer|admin] [--groups a,b]` prints a one-hour token for another local caller, `NAME@localhost`, to see what a caller with no role, a deployer or another admin gets. In development mode, a request to `/api/...` with a credential that does not verify is refused with `401 INVALID_CREDENTIAL`.
- `aiplex dev` can admit a second caller with `--allow-users name@localhost`.
- `make demo` scripts a first run; `make e2e` runs the real binaries against local mock upstreams; `make deps` reports the tools each workflow needs.
- Loopback JWKS issuer and a local agent registry in development mode.
- `aiplex dev --detach` runs the local stack in the background, with its output in `~/.aiplex/dev.log`. `aiplex dev status` says whether it runs and where, and `aiplex dev stop` stops it. A second `aiplex dev` is refused. `--allow-origin` lets the local API call one more upstream origin.
- The `aiplex dev` summary ends with numbered "Try next" steps, and says plainly when the Console is not built into the binary.
- Release binaries embed the built Console; a source build says how to build it (`make console && make build`).

### Harness onboarding

- `aiplex connect <access-point> --client claude-code|codex|gemini-cli --write` merges the connection settings into the client's own config file after showing a diff, with a timestamped backup under `~/.aiplex/connect-backups`, `--dry-run`, `--yes` for scripts and `aiplex connect --undo`. It refuses files that are not valid JSON or TOML and never writes a token value (`--token-helper` uses `aiplex auth print-token`). `--with-telemetry <otlp-endpoint>` sets the client's own OpenTelemetry export, never prompt logging. See `docs/harness-onboarding.md`.
- `aiplex mcp serve`: a stdio MCP server with read-only tools (`whoami`, `list_access_points`, `get_connect_config`, `list_models`, `usage_summary`, `usage_records`) using your own token.
- `aiplex auth print-token` for client credential helpers.
- `integrations/claude-code` (plugin with three skills), `integrations/gemini-cli` (extension) and a root `AGENTS.md`; a test checks that every `aiplex` command in them exists.

### Access points and usage

- Access points: one stable URL per audience (`/ap/{id}/...`) in front of models, MCP tools and skills, with caller-kind admission, model aliases, optional QoS scope, native error shapes, and wires for OpenAI (chat, responses, embeddings, images, audio), Anthropic Messages, Gemini, Azure OpenAI (including `api-key` header and path shapes), Bedrock and WebSocket sessions.
- A single-pane overview of callers and what each can reach (`aiplex who`, `aiplex access-points`, `aiplex connect`).
- A tenant-scoped usage ledger for managed and reported exchanges, with filters and grouping (by model, user, source, error class, usage bound and batch), explicit usage bounds, and non-token units and modality splits. Unknown values stay unknown.
- A price catalogue: a pinned, trimmed snapshot of LiteLLM's public list prices (MIT, third-party data, see `THIRD_PARTY_NOTICES.md`) embedded in the CLI. `aiplex usage prices catalog search|show|import` creates ordinary price resources, with an optional `source` label, from the models you choose. Nothing is priced automatically; imported cost is `estimated`, a snapshot that can be stale, and pricing a price cannot express (context-length tiers, audio or image token rates) needs `--accept-partial`. `make update-price-catalog` refreshes the pin.
- Guided creation from flags: `aiplex access-points create`, `aiplex mcp connections create|list|get|test|delete`, `aiplex llm surfaces create`, and `aiplex usage prices set <model> --input-per-mtok ... --output-per-mtok ...`. The file forms (`apply -f`, `prices set -f`) remain for full specifications.
- Usage tables fit the terminal width, and show the caller by a readable name (`principal_display`, `actor_display`) instead of a principal hash. `aiplex usage records -o wide` adds a `PRINCIPAL ID` column, and `summary -o wide` adds the columns the default tables leave out. Summaries label empty groups `(no model)` and `(no tool)`.
- Cost objects carry `amount_decimal`, the exact decimal form of `amount`. `tools/list` exchanges are recorded with operation `tools/list`.
- `GET /api/v1/dashboard/stats` totals the last 24 hours from the usage ledger. Cost and tokens are `null` when unknown, with `daily_unknown_cost_records`, `daily_unknown_usage_records` and `usage_source` alongside.
- `aiplex connect <access-point>` works for any caller the access point admits. Callers without an operator role get a reduced response. `--client claude-desktop` is refused with a reason, because that client needs a Claude apps gateway server.
- `aiplex validate` runs the same checks as `aiplex apply`, and `apply` validates first. Under `aiplex dev`, instances in a manifest are skipped with a message.
- `aiplex llm surfaces get` and `aiplex clients get` print a readable summary by default. Use `-o json` or `-o yaml` for the full specification.
- Destructive commands (`delete`, `undeploy`, `delete-context`) refuse to run without a terminal unless `--yes` is given.
- CLI errors name the cause and give hints, such as `Did you mean` for a mistyped ID. Every command uses fixed exit codes (0 to 6, listed in `aiplex --help`), `--debug` shows the raw API error, and destructive commands ask on a terminal unless `--yes` is given.

### Models (LLMPlex)

- Native provider surfaces for OpenAI, Anthropic, Azure OpenAI, Bedrock, Gemini Developer, Google Cloud and OpenAI-compatible endpoints, over HTTP, SSE and WebSocket, with credentials as references.
- Usage and rate-limit observation per provider format, covered by a conformance harness over fixtures built from provider documentation (see the [coverage matrix](docs/reference/usage-formats/coverage.md)).
- Media operations and model batches with settlement, provider-capacity cooldown and headroom gate, and Envoy `LLMRoute` generation for gateway routing.

### Tools, agents, skills and clients

- MCPlex: tool connections with eight upstream auth types, delegated OAuth, allow/deny and read-only tool policy, and stateless JSON and finite-SSE execution.
- A2APlex: connections to Google, AWS, Foundry and Copilot agents, with actor-separated sessions and reported delegation history; Agent Card discovery.
- SkillsPlex: skill discovery and reported invocation history.
- ClientPlex: client profiles and reviewed configuration plans for Antigravity, Codex and Claude clients.

### Identity and authorization

- Agent identity federation with cross-cloud presets, bindings and a token dry run; workforce user identity federation with directory group lookups; authoritative membership; native agent governance for Google, AWS and Microsoft Entra.
- Default-deny route policy by mounted pattern; tenant taken from the verified identity only; hardened CORS and request IDs.
- Forwarded-address headers (`X-Forwarded-For`, `Forwarded`, `X-Real-IP`) are ignored unless `TRUSTED_PROXY_HOPS` names the number of proxies in front of the API. In development mode, an anonymous request that carries one of them is refused with `403 PROXIED_DEVELOPMENT_REQUEST`.
- `aiplex-authz`, a Rust `ext_authz` service, and a Rego scope policy.

### Quota (QuotaPlex)

- Opt-in budgets and admission for covered managed paths: unequal token, money and request allocations, periods, loans, admitted rates, concurrency, weighted scheduling, durable evidence and restart-safe settlement. Hard limits need qualification per deployment.

### Platform and deployment

- Deploy engine and catalog federation, declarative `aiplex apply`, Helm chart, Terraform modules, Kubernetes manifests and Ory configuration under `deploy/`. A Firestore emulator lane in CI covers registry and quota storage.
- Networking plans with optional reviewed Anvil execution.

### Console and SDK

- React Console covering the above, embedded in the CLI when built first; role-aware, so each view shows what the signed-in roles may do; brand identity and design tokens applied.
- Go SDK for the API.

### Documentation, brand and project files

- Code-verified guides under `docs/`, a Docusaurus site that renders them with broken links as errors, a generated CLI reference, API reference pages, the brand system (logo, tokens, icons, launch graphics, architecture diagram), and `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md` and issue and pull request templates.
- Examples under `examples/`, run by `make examples-test` against `aiplex dev`.
- Release mechanics: GoReleaser builds `aiplex` and `aiplex-api` archives with checksums and a changelog; `install.sh` installs both.

[Unreleased]: https://github.com/vamsiramakrishnan/aiplex/commits/main

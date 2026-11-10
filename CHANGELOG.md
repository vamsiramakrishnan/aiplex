# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project intends to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once it reaches 1.0. Until then, minor versions may change interfaces.

## [Unreleased]

There has been no tagged release yet. This section summarises what exists in the repository, grouped by area. Nothing here has been validated against live model providers, provider invoices or a live cloud project; see the README's project status.

### Local development

- `aiplex dev` starts the API in memory on loopback with a built-in mock model and MCP server, seeds an access point `local`, signs you in as a local admin, and prints next steps. `aiplex dev env` exports `AIPLEX_URL`, `AIPLEX_TOKEN` and the base-URL and key variables read by OpenAI, Anthropic and Gemini SDKs. `--provider openai|anthropic|gemini --api-key-env NAME` forwards to a real provider.
- `make demo` scripts a first run; `make e2e` runs the real binaries against local mock upstreams; `make deps` reports the tools each workflow needs.
- Loopback JWKS issuer and a local agent registry in development mode.

### Access points and usage

- Access points: one stable URL per audience (`/ap/{id}/...`) in front of models, MCP tools and skills, with caller-kind admission, model aliases, optional QoS scope, native error shapes, and wires for OpenAI (chat, responses, embeddings, images, audio), Anthropic Messages, Gemini, Azure OpenAI (including `api-key` header and path shapes), Bedrock and WebSocket sessions.
- A single-pane overview of callers and what each can reach (`aiplex who`, `aiplex access-points`, `aiplex connect`).
- A tenant-scoped usage ledger for managed and reported exchanges, with filters and grouping (by model, user, source, error class, usage bound and batch), explicit usage bounds, and non-token units and modality splits. Unknown values stay unknown.

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
- `aiplex-authz`, a Rust `ext_authz` service, and a Rego scope policy.

### Quota (QuotaPlex)

- Opt-in budgets and admission for covered managed paths: unequal token, money and request allocations, periods, loans, admitted rates, concurrency, weighted scheduling, durable evidence and restart-safe settlement. Hard limits need qualification per deployment.

### Platform and deployment

- Deploy engine and catalog federation, declarative `aiplex apply`, Helm chart, Terraform modules, Kubernetes manifests and Ory configuration under `deploy/`. A Firestore emulator lane in CI covers registry and quota storage.
- Networking plans with optional reviewed Anvil execution.

### Console and SDK

- React Console covering the above, embedded in the CLI when built first; brand identity and design tokens applied.
- Go SDK for the API.

### Documentation, brand and project files

- Code-verified guides under `docs/`, a Docusaurus site that renders them with broken links as errors, a generated CLI reference, API reference pages, the brand system (logo, tokens, icons, launch graphics, architecture diagram), and `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md` and issue and pull request templates.
- Examples under `examples/`, run by `make examples-test` against `aiplex dev`.
- Release mechanics: GoReleaser builds `aiplex` and `aiplex-api` archives with checksums and a changelog; `install.sh` installs both.

[Unreleased]: https://github.com/vamsiramakrishnan/aiplex/commits/main

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="brand/logo/aiplex-lockup-horizontal-reversed.svg">
  <img src="brand/logo/aiplex-lockup-horizontal.svg" width="190" alt="AIPlex">
</picture>

# AIPlex

Give each team one endpoint for its models and MCP tools. AIPlex checks who is calling, routes the request, and records what the caller used. Operators keep the upstream credentials and can set a shared spending limit across supported calls.

[Documentation][docs] · [Local development][docs-local] · [Quota and budgets][docs-quota] · [Project status](docs/project-status.md)

## Quick start

Build from source with Go 1.25+ and Make. From one terminal:

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex
make build
bin/aiplex dev --detach
bin/aiplex dev try
```

The last command sends a real HTTP request through `/ap/local` to the built-in mock model, then finds that request in the usage ledger. The reply, caller, token counts and estimated cost come from the running stack. You can try it without a cloud account or provider key.

```bash
bin/aiplex connect local       # inspect the SDK and coding-client configuration
bin/aiplex modules list        # see what this deployment exposes
bin/aiplex dev stop            # stop the local processes and discard in-memory state
```

Local development accepts unauthenticated requests as admin. Keep it on loopback. For the SDK environment, foreground mode, or a real provider, see [Run it locally][docs-local]. To exercise streaming, MCP and client setup in one scripted run, use `make demo`.

## How it works

An **access point** belongs to an audience, such as the engineering team or a CI workload. The operator chooses its model aliases, tool connections and admitted callers. A coding client can use its existing protocol and authenticate with an AIPlex credential. The verified identity determines the caller kind. A delegated call can retain both the person and the agent.

```text
People · coding clients · agents
    │
    ├── AIPlex API
    │   ├── /ap/{id}/…                         model calls, MCP tools, skill discovery
    │   └── /api/v1/a2a/connections/{id}/invoke native agent calls
    │       Managed execution → optional QuotaPlex admission → upstream → usage ledger
    │
    └── Envoy AI Gateway                         cluster deployment
        ├── /mcp/*  → MCPlex
        ├── /a2a/*  → A2APlex                    ext_authz + rate limiting
        └── /llm/*  → LLMPlex                    mTLS to deployed workloads
```

QuotaPlex acts on configured managed operations. Envoy and direct provider traffic sit outside that accounting boundary. The [request path][docs-path] and [execution coverage matrix](docs/quotaplex-execution-coverage.md) describe which controls apply.

## How it is named

| Module | What you can do | Start here |
| --- | --- | --- |
| **LLMPlex** | Expose models under stable names and forward provider-native HTTP, streaming or supported WebSocket traffic. | [Model surfaces](docs/model-surfaces.md) · `aiplex llm` |
| **MCPlex** | Give callers one MCP endpoint over tool connections with upstream authentication and per-tool policy. | [Tool connections](docs/tool-connection-auth.md) · `aiplex mcp` |
| **A2APlex** | Invoke a connected native agent or deploy an A2A agent behind a cluster route. Inspect reported delegations. | [Connected agents](docs/connected-plexes.md) · `aiplex a2a` |
| **SkillsPlex** | Discover deployed skill manifests and inspect reported invocations. | [Skill discovery and history](docs/a2a-skills-audit.md) · `aiplex skills` |
| **ClientPlex** | Generate reviewed configuration for coding clients. The CLI can apply supported file changes with a diff, backup and undo. | [Client onboarding](docs/harness-onboarding.md) · `aiplex connect` |
| **QuotaPlex** | Reserve budget before dispatch and settle from trusted usage. Share limits across callers and managed operations. | [Quota and budgets][docs-quota] · `aiplex budgets` · `aiplex quota` |

## Try a shared budget

From a stopped local stack, run the example with Python 3 and curl:

```bash
export PATH="$PWD/bin:$PATH"
aiplex dev --quota-tariff --detach
bash examples/budgets/shared-spend.sh
aiplex dev stop
```

A USD 0.003 budget covers a USD 0.001 model request and a USD 0.002 MCP call. The next tool call is refused. Both prices are local fixtures. In a deployment, the operator configures the price book and the operations it covers. Incomplete terminal evidence keeps exposure reserved. [Budget rules](docs/budgets.md) · [Quota administration](docs/quotaplex-administration.md)

## Where to go next

| Task | First step |
| --- | --- |
| Give a team its own endpoint | `aiplex access-points create team --model mock-gpt=local-models --tool local-tools` on the local stack. [Access points](docs/access-points.md) |
| Point an IDE or harness at it | `aiplex connect local --client claude-code --write --dry-run` previews the proposed edit. [Harness onboarding](docs/harness-onboarding.md) |
| Find what a caller used | `aiplex usage records` or `aiplex usage summary --by model`. Missing usage stays unknown. [Usage ledger](docs/usage-ledger.md) |
| Run the Console | `make console && make build` with Node 22, then restart `aiplex dev`. [Local development][docs-local] |
| Explore the API and SDKs | [CLI reference][docs-cli] · [API reference][docs-api] · [Runnable examples](examples/README.md) |

## Install

The source build produces `bin/aiplex` and `bin/aiplex-api`. The first tagged release will enable the packaged distribution paths. For GKE, follow [Install the platform][docs-install] and run `aiplex platform preflight` to check the deployer's permissions.

## Project status

AIPlex is pre-1.0. The repository records live model calls to the Gemini Developer API and z.ai. Local suites cover managed execution with mock upstreams, and Firestore transactions are exercised on the emulator. Live cloud deployment, first-party OpenAI and Anthropic endpoints, and invoice reconciliation still need qualification. [Validation inventory](docs/project-status.md) · [Production qualification](docs/production-qualification.md)

## Documentation

[Documentation site][docs] · [Naming and glossary](docs/naming.md) · [Security model](SECURITY.md) · [Contributing](CONTRIBUTING.md) · [Code of conduct](CODE_OF_CONDUCT.md) · [Changelog](CHANGELOG.md)

## License

[Apache License 2.0](LICENSE). The price catalogue includes third-party data. [Third-party notices](THIRD_PARTY_NOTICES.md)

[docs]: https://vamsiramakrishnan.github.io/aiplex/
[docs-local]: https://vamsiramakrishnan.github.io/aiplex/docs/getting-started/local-development
[docs-quota]: https://vamsiramakrishnan.github.io/aiplex/docs/guides/quotaplex
[docs-path]: https://vamsiramakrishnan.github.io/aiplex/docs/concepts/request-path
[docs-cli]: https://vamsiramakrishnan.github.io/aiplex/docs/reference/cli/
[docs-api]: https://vamsiramakrishnan.github.io/aiplex/docs/api/overview
[docs-install]: https://vamsiramakrishnan.github.io/aiplex/docs/getting-started/install-platform

## Related projects

- [anvil](https://github.com/vamsiramakrishnan/anvil). Generate MCP tools, CLIs and SDKs from existing API contracts.

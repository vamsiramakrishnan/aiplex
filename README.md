<picture>
  <source media="(prefers-color-scheme: dark)" srcset="brand/logo/aiplex-lockup-horizontal-reversed.svg">
  <img src="brand/logo/aiplex-lockup-horizontal.svg" width="190" alt="AIPlex">
</picture>

# AIPlex

**Connect agents, models, tools and harnesses.** When an agent calls a model, runs a tool or invokes another agent, you need to know who made the call and what it used. AIPlex checks the caller’s permissions and records managed usage in one ledger. Connect the harness once, then change its team’s model routes and tool permissions on the server.

[Documentation][docs] · [Local development][docs-local] · [Quota and budgets][docs-quota] · [Project status](docs/project-status.md)

## Quick start

Build the CLI and start the local stack with Git, Make and Go 1.25+. The agent, models and MCP server run on your machine without provider keys.

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex
make build
export PATH="$PWD/bin:$PATH"
aiplex dev --detach
```

| Try it | Run | What to look for |
| --- | --- | --- |
| **Agents** | `aiplex a2a invoke local-agent --operation query` | The connected agent replies to the query. |
| **Models** | `aiplex dev try` | The model replies. Its usage record shows the caller, tokens and estimated cost. |
| **Tools** | `aiplex mcp connections call local-tools echo --args '{"text":"hello"}'` | The MCP server returns `hello`. |
| **Harnesses** | `aiplex connect local --client codex` | The CLI prints the model and MCP configuration for Codex. |

Run `aiplex dev stop` when finished. Local development accepts unauthenticated requests as admin. Keep it on loopback. To run all four checks in an isolated local stack, use `make demo`. [Local development][docs-local]

## How it works

A harness such as Codex runs the agent loop. It sends a prompt to a model and can call a tool when the model requests one. With AIPlex, the operator chooses which models and tools to expose and who can call them. AIPlex checks each managed request and records the caller and observed usage. A native agent has its own invocation endpoint.

```text
Coding harness or agent workload
    │
    ├── AIPlex API
    │   ├── /api/v1/a2a/connections/{id}/invoke  → connected agent
    │   ├── /ap/{id}/v1/…                        → model (OpenAI wire shown)
    │   └── /ap/{id}/mcp                         → MCP tool server
    │
    └── Envoy AI Gateway (cluster)
        ├── /a2a/*  → deployed agents
        ├── /llm/*  → model backends
        └── /mcp/*  → deployed tool servers
```

ClientPlex prepares the harness configuration. QuotaPlex can reserve budget before a managed call. It settles the charge when it confirms usage, such as provider-reported tokens or a completed tool call. Envoy applies its own authorization and rate limits. [Request paths][docs-path] · [Quota coverage](docs/quotaplex-execution-coverage.md)

<a id="how-it-is-named"></a>

## Modules

| Module | What you use it for |
| --- | --- |
| **A2APlex** | Invoke a native agent through a saved connection, deploy an A2A agent or inspect delegation history. [Connected agents](docs/connected-plexes.md) |
| **LLMPlex** | Expose named models and call them using their provider protocols. [Model surfaces](docs/model-surfaces.md) |
| **MCPlex** | Connect MCP servers and decide which tools each caller may run. [Tool connections](docs/tool-connection-auth.md) |
| **ClientPlex** | Configure a harness for the models and tools it can use. Preview supported file edits before applying them. [Harness onboarding](docs/harness-onboarding.md) |
| **QuotaPlex** | Share a budget across supported managed calls and inspect what each call reserved or spent. [Quota and budgets][docs-quota] |
| **SkillsPlex** | Discover skill manifests and inspect reported invocations. [Skill discovery and history](docs/a2a-skills-audit.md) |

## Try a shared budget

From a stopped local stack, run the example with Python 3 and curl:

```bash
export PATH="$PWD/bin:$PATH"
aiplex dev --quota-tariff --detach
bash examples/budgets/shared-spend.sh
aiplex dev stop
```

A USD 0.003 budget covers a USD 0.001 model request and a USD 0.002 MCP call. AIPlex blocks the next tool call before dispatch. Both prices are local fixtures. In a deployment, the operator configures the price book and the operations it covers. Incomplete terminal evidence keeps exposure reserved. [Budget rules](docs/budgets.md) · [Quota administration](docs/quotaplex-administration.md)

## Where to go next

| Task | First step |
| --- | --- |
| Give a team its own endpoint | `aiplex access-points create team --model mock-gpt=local-models --tool local-tools` on the local stack. [Access points](docs/access-points.md) |
| Point an IDE or harness at it | `aiplex connect local --client claude-code --write --dry-run` previews the proposed edit. [Harness onboarding](docs/harness-onboarding.md) |
| Find what a caller used | `aiplex usage records` or `aiplex usage summary --by model`. Missing usage stays unknown. [Usage ledger](docs/usage-ledger.md) |
| Run the Console | `make console && make build` with Node 22, then restart `aiplex dev`. [Local development][docs-local] |
| Explore the API and SDKs | [CLI reference][docs-cli] · [API reference][docs-api] · [Runnable examples](examples/README.md) |

## Install

The source build produces `bin/aiplex` and `bin/aiplex-api`. Build from source until the first tagged release. For GKE, follow [Install the platform][docs-install] and run `aiplex platform preflight` to check the deployer's permissions.

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

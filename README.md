# AIPlex

AIPlex is a control plane for agent interactions. It puts MCP, A2A, and model traffic behind one gateway, one auth stack, one policy layer, and one audit trail.

| Plane | Protocol | Governs | Route CRD |
| --- | --- | --- | --- |
| MCPlex | MCP (JSON-RPC) | Agent to tool | `MCPRoute` |
| A2APlex | A2A (HTTP/JSON) | Agent to agent | `HTTPRoute` |
| LLMPlex | Provider APIs | Agent to model | `LLMRoute` |

## Quick start

```bash
curl -fsSL https://get.aiplex.dev | sh

aiplex quickstart
```

Or run the setup steps yourself:

```bash
aiplex login
aiplex deploy
aiplex status my-tool
```

Operator surfaces:

```bash
aiplex tui      # terminal dashboard
aiplex console  # local web console
```

[Documentation](https://docs.aiplex.dev) · [Quickstart guide](https://docs.aiplex.dev/docs/getting-started/quickstart)

## Request path

```text
Agents / IDEs / CLIs
       │
       ▼
┌──────────────────────────────┐
│  Envoy AI Gateway            │
│  /mcp/*  → MCPlex  (tools)   │
│  /a2a/*  → A2APlex (agents)  │
│  /llm/*  → LLMPlex (models)  │
│  ext_authz + rate limiting   │
└──────────────────────────────┘
       │ mTLS
       ▼
  K8s namespaces: mcplex, a2aplex, llmplex
```

A JWT carries scopes across the three planes:

```json
{
  "sub": "student@school.edu",
  "azp": "tutor-agent",
  "act": { "sub": "spiffe://.../sa/tutor-agent" },
  "scope": "mcp:tools:search_curriculum a2a:task:research llm:model:gemini-2.5-flash"
}
```

## Declarative deployment

```yaml
# aiplex.yaml
version: v1
instances:
  - name: kb-search
    template: kb-search-server
    plane: mcplex
    config:
      INDEX_PATH: "/data/curriculum"

routes:
  llm:
    - name: default
      backends:
        - provider: google
          model: gemini-2.5-flash
          weight: 80
        - provider: anthropic
          model: claude-sonnet-4-20250514
          weight: 20

agents:
  - name: tutor-agent
    grant:
      - mcp:tools:search_curriculum
      - a2a:task:research
      - llm:model:gemini-2.5-flash
```

Apply it with:

```bash
aiplex apply -f aiplex.yaml
```

## Components

| Component | Language | Purpose |
|-----------|----------|---------|
| **AIPlex API** | Go | REST API, deploy engine, consent handler |
| **AIPlex Console** | React/TS | Web UI for all three planes |
| **aiplex-authz** | Rust | Scope authorization and JWT verification |
| **AIPlex CLI** | Go | Command-line interface |
| **Ory Hydra** | Go (configured) | OAuth 2.1 token issuance |
| **Ory Kratos** | Go (configured) | Identity, social sign-in |
| **Envoy AI Gateway** | C++ (configured) | MCPRoute, HTTPRoute, LLMRoute |

Infrastructure: GKE Autopilot, Cloud Service Mesh with mTLS, Firestore, AlloyDB, and Secret Manager.

## Authorization model

Effective access is the intersection of three limits:

```text
Agent ceiling ∩ user ceiling ∩ session consent = effective access
```

- Agent ceiling: resources the agent may access.
- User ceiling: resources the user may access.
- Session consent: resources the user approved for the current session.

The authorization path is implemented by [`policies/aiplex_authz.rego`](policies/aiplex_authz.rego) and the Rust `ext_authz` service.

## Connected Plex modules

ClientPlex registers developer clients; LLMPlex preserves native model APIs; MCPlex handles OAuth registration and configured tool execution; A2APlex invokes native cloud agents. Shared networking plans can use an optional Anvil provider for missing infrastructure constructs. See [configuration and API ownership](docs/connected-plexes.md).

Identity also connects native Google Agent Registry/IAM, AWS AgentCore Identity and Microsoft Entra Agent ID through scoped inventory, reviewed policy plans and durable apply receipts. See [native governance](docs/native-agent-governance.md) for credential brokers, provider limits and configuration.

## Shared quotas with QuotaPlex

QuotaPlex is an opt-in control module shared by the existing planes. It supports unequal token, money and request allocations, independent human/agent attribution, fixed/anchored/calendar periods, explicit budget loans, atomic admission, rate limits, concurrency and weighted scheduling.

For configured calls through the Go API's managed endpoints:

```text
Verified principal (+ independent delegated actor)
                        |
                        v
          Managed model / tool / agent endpoint
                        |
                        v
 QuotaPlex: budget hold + admitted-rate debit + permit
                        |
                        v
             Claim once -> native provider
                                |
                                v
                    Trusted terminal evidence
                                |
                         +------+------+
                         |             |
                       known        unknown
                         |             |
                    settle once   retain holds
```

LLMPlex and MCPlex keep their native HTTP, SSE and tool protocols. QuotaPlex owns admission and accounting. A request-count profile measures the managed exchange; it does not measure an async job's lifetime or an agent's internal model/tool work.

| Capability | Implemented | Still missing |
| --- | --- | --- |
| Membership | Verified group claims or a signed authority; independent principal/actor views | Native directory synchronization and distributed revocation event consumers |
| Allocations | Unequal budgets, exact prices, supported periods and explicit loans | Rolling budgets, split charging, currency conversion, automatic lending and population rebalance |
| Model/tool metering | Supported native text profiles; stateless JSON/finite-SSE tool call counts | Live/async job billing, broader media/cache/server-tool profiles, tool rows/bytes/duration |
| Recovery | Durable evidence, restart-safe settlement and reviewed reconciliation | Native provider status/usage lookup adapters; long-running renewal and acknowledged cancellation |
| Enforcement coverage | Exact bindings on supported managed API paths | Automatic Envoy/direct-route interception and authenticated remote admission/settlement RPC |
| Operations | Policy preview/export, attempt/balance inspection, loans and corrections in API/CLI/Console | Isolated shadow evaluation, runtime coverage qualification and alerts for aging unknown exposure |
| Throughput | Shared admitted-demand rates, concurrency and atomic weighted capacity admission | Provider capacity reservation, stream token pacing, preemption and provider minimum-throughput guarantees |

Existing Envoy routes and direct provider calls do not automatically pass through this quota path. Admitted tokens/s and requests/s are separate from actual provider generation throughput. Hard limits require verified provider bounds, prices, identity sources and routing for each enabled profile; live billing, load and backup-restore qualification remain deployment work.

Read the [core guide](docs/quotaplex-core.md), [managed execution guide](docs/quotaplex-integration.md), [execution coverage and regression matrix](docs/quotaplex-execution-coverage.md), or [full readiness register](docs/quotaplex-prerequisites.md). The [foundation design](docs/quotaplex-foundation-design.md) records allocation, metering and scheduling contracts.

## Module foundations

Built-in Plex modules can be selected independently; external services register through versioned descriptors. See [module contracts](docs/modules.md) and [deployment migration and bugbash](docs/foundations-bugbash.md) before upgrading an existing deployment.

[Foundation completion](docs/foundation-completion.md) connects revisioned configuration, directory membership, native model profiles, delegated MCP authorization, quota operations, atomic scheduling and reviewed infrastructure execution. [Production qualification](docs/production-qualification.md) supplies emulator CI, reference benchmarks and billing-evidence comparison.

The [feature cohesion bugbash](docs/feature-cohesion-bugbash.md) records repairs to existing OAuth, routing, persistence, audit, SDK/CLI and Console handoffs, including deployment compatibility.

## Local Development

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex

./setup.sh
```

Manual setup:

```bash
make deps
make docker-up
make build
make run-local
make console-dev
```

Useful CLI commands:

```bash
aiplex whoami
aiplex completion bash >> ~/.bashrc
```

Go, Terraform, Helm, kubectl, and Node versions are pinned in `.mise.toml`.

See [Installation](https://docs.aiplex.dev/docs/getting-started/installation) for the full setup path.

## Repository layout

```text
cmd/           Go binaries: API server and CLI
internal/      Core packages: API, auth, catalog, deploy, registry
authz/         Rust ext_authz service
console/       React application
sdk/           Go SDK
deploy/        Terraform, Helm, Kubernetes manifests, Ory config
policies/      OPA/Rego authorization policy
examples/      Example aiplex.yaml files
design/        Architecture documents
docs-site/     Docusaurus documentation
```

## License

Apache 2.0

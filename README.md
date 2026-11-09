# AIPlex

**Unified control plane for AI agent interactions.**

AIPlex governs three interaction planes through a single gateway, auth stack, policy engine, and audit trail:

| Plane | Protocol | What It Governs | Route CRD |
|-------|----------|-----------------|-----------|
| **MCPlex** | MCP (JSON-RPC) | Agent ↔ Tool | MCPRoute |
| **A2APlex** | A2A (HTTP/JSON) | Agent ↔ Agent | HTTPRoute |
| **LLMPlex** | Provider APIs | Agent ↔ Model | LLMRoute |

## Quick Start

```bash
# Install
curl -fsSL https://get.aiplex.dev | sh

# Zero to running platform in one command
aiplex quickstart

# Or step-by-step:
aiplex login
aiplex deploy
aiplex status my-tool

# Terminal dashboard
aiplex tui

# Local console
aiplex console
```

**[Full Documentation](https://docs.aiplex.dev)** | **[Quickstart Guide](https://docs.aiplex.dev/docs/getting-started/quickstart)**

## How It Works

```
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

A single JWT carries scopes across all planes:

```json
{
  "sub": "student@school.edu",
  "azp": "tutor-agent",
  "act": { "sub": "spiffe://.../sa/tutor-agent" },
  "scope": "mcp:tools:search_curriculum a2a:task:research llm:model:gemini-2.5-flash"
}
```

## Deploy Declaratively

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

```bash
aiplex apply -f aiplex.yaml
```

## Architecture

| Component | Language | Purpose |
|-----------|----------|---------|
| **AIPlex API** | Go | REST API, deploy engine, consent handler |
| **AIPlex Console** | React/TS | Web UI for all three planes |
| **aiplex-authz** | Rust | Scope authorization and JWT verification |
| **AIPlex CLI** | Go | Command-line interface |
| **Ory Hydra** | Go (configured) | OAuth 2.1 token issuance |
| **Ory Kratos** | Go (configured) | Identity, social sign-in |
| **Envoy AI Gateway** | C++ (configured) | MCPRoute, HTTPRoute, LLMRoute |

**Infrastructure:** GKE Autopilot, Cloud Service Mesh (mTLS), Firestore, AlloyDB, Secret Manager

## Three-Dimensional Permissions

```
Agent Ceiling (A)  ∩  User Ceiling (B)  ∩  User Consent (C)  =  Effective
```

- **A**: What the agent can ever access (admin-configured)
- **B**: What the user can ever access (admin-configured)
- **C**: What the user approved this session (runtime consent)

Enforced by a [20-line Rego policy](policies/aiplex_authz.rego) / Rust ext_authz.

## Connected Plex modules

ClientPlex registers developer clients; LLMPlex preserves native model APIs; MCPlex handles OAuth registration; A2APlex invokes native cloud agents. Shared networking plans can use an optional Anvil provider for missing infrastructure constructs. See [configuration and API ownership](docs/connected-plexes.md).

Identity also connects native Google Agent Registry/IAM, AWS AgentCore Identity and Microsoft Entra Agent ID through scoped inventory, reviewed policy plans and durable apply receipts. See [native governance](docs/native-agent-governance.md) for credential brokers, provider limits and configuration.

## Module foundations

Built-in Plex modules can be selected independently; external services register through versioned descriptors. See [module contracts](docs/modules.md) and [deployment migration and bugbash](docs/foundations-bugbash.md) before upgrading an existing deployment.

[QuotaPlex prerequisites and readiness](docs/quotaplex-prerequisites.md) lists the contracts, accounting rules and integration gates needed to add quotas while preserving this flow.

[QuotaPlex foundation design](docs/quotaplex-foundation-design.md) defines the shared control core for unequal allocations, model/tool meters, rate and burst limits, concurrency and scheduled throughput.

The [QuotaPlex core guide](docs/quotaplex-core.md) documents the Go library, exact accounting, atomic admission, Firestore adapter, budget loans and weighted queue primitive. The [opt-in managed execution guide](docs/quotaplex-integration.md) covers supported model/tool dispatch, terminal usage and recovery. Broader provider coverage and throughput guarantees retain explicit integration gates.

[Foundation completion](docs/foundation-completion.md) connects revisioned configuration, directory membership, native model profiles, delegated MCP authorization, quota operations, atomic scheduling and reviewed infrastructure execution. [Production qualification](docs/production-qualification.md) supplies emulator CI, reference benchmarks and billing-evidence comparison.

The [feature cohesion bugbash](docs/feature-cohesion-bugbash.md) records repairs to existing OAuth, routing, persistence, audit, SDK/CLI and Console handoffs, including deployment compatibility.

## Local Development

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex

# One-command setup (installs mise + all tools)
./setup.sh

# Or manually:
make deps        # Check prerequisites
make docker-up   # Start backing services
make build       # Build API + CLI
make run-local   # Start API server
make console-dev # Start React dev server

# View current context
aiplex whoami

# Enable shell completion
aiplex completion bash >> ~/.bashrc
```

Tool versions (Go, Terraform, Helm, kubectl, Node) are pinned in `.mise.toml`.

See [Installation](https://docs.aiplex.dev/docs/getting-started/installation) for details.

## Project Structure

```
cmd/           Go binaries (API server, CLI)
internal/      Core packages (api, auth, catalog, deploy, registry)
authz/         Rust ext_authz service
console/       React SPA
sdk/           Go SDK
deploy/        Terraform, Helm, K8s manifests, Ory config
policies/      OPA/Rego authorization policy
examples/      Example aiplex.yaml configurations
design/        Architecture design documents
docs-site/     Documentation (Docusaurus)
```

## License

Apache 2.0

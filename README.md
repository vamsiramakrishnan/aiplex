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

# Login
aiplex login

# Deploy your first tool
aiplex deploy

# Check status
aiplex status my-tool
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
| **aiplex-authz** | Rust | ext_authz (0.05ms p50 JWT validation) |
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

## Local Development

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex

make deps        # Check prerequisites
make docker-up   # Start backing services
make build       # Build API + CLI
make run-local   # Start API server
make console-dev # Start React dev server
```

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

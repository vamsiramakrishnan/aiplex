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

| Component | Language | Responsibility |
| --- | --- | --- |
| AIPlex API | Go | REST API, deploy engine, consent handler |
| AIPlex Console | React/TypeScript | Operator UI for all three planes |
| `aiplex-authz` | Rust | `ext_authz`; JWT validation |
| AIPlex CLI | Go | Command-line interface |
| Ory Hydra | Go, configured | OAuth 2.1 token issuance |
| Ory Kratos | Go, configured | Identity and social sign-in |
| Envoy AI Gateway | C++, configured | `MCPRoute`, `HTTPRoute`, `LLMRoute` |

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

## Local development

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

# AIPlex Documentation Site & README — Design Spec

**Date:** 2026-04-05
**Status:** Approved

## Goal

Create a Docusaurus-based documentation site (`/docs-site/`) and a repo-root `README.md` to improve developer experience for three audiences: platform admins, agent developers, and tool/server operators.

## Principles

1. **Generated documents first** — CLI reference, API reference, config schemas, and scope tables are auto-generated from source code. Hand-curated content layers on top.
2. **Task-first navigation** — docs are organized by what users are trying to do, not by which subsystem they're in. Planes (MCPlex/A2APlex/LLMPlex) appear as badges/tags, not top-level sections.
3. **Role-based funnels** — the landing page routes users to quickstarts based on their role (admin, developer, operator).

## Deliverables

### 1. Docusaurus Site (`/docs-site/`)

**Framework:** Docusaurus 3.x (React, MDX, Mermaid plugin, dark/light mode)

**Directory structure:**

```
docs-site/
├── docusaurus.config.ts
├── package.json
├── sidebars.ts
├── scripts/
│   └── generate-docs.sh            # Runs all doc generators
├── static/
│   └── img/
├── src/
│   └── pages/
│       └── index.tsx                # Landing page with role funnels
├── docs/
│   ├── getting-started/
│   │   ├── overview.md              # What is AIPlex, the three planes
│   │   ├── quickstart-admin.md      # Platform admin: Terraform + Helm + first deploy
│   │   ├── quickstart-developer.md  # Agent dev: register agent, get token, call tool
│   │   └── quickstart-operator.md   # Operator: deploy MCP server via CLI/Console
│   ├── guides/
│   │   ├── deploy-mcp-server.md
│   │   ├── deploy-a2a-agent.md
│   │   ├── configure-llm-routing.md
│   │   ├── register-agent.md
│   │   ├── manage-permissions.md
│   │   ├── oauth-flows.md
│   │   ├── cross-plane-access.md
│   │   └── declarative-apply.md
│   ├── concepts/
│   │   ├── architecture.md
│   │   ├── three-planes.md
│   │   ├── auth-model.md
│   │   ├── scopes.md
│   │   ├── identity.md
│   │   └── policy.md
│   ├── reference/
│   │   ├── cli/
│   │   │   └── _generated/          # One .md per CLI command
│   │   ├── api/
│   │   │   └── _generated/          # One .md per API route group
│   │   ├── config/
│   │   │   └── _generated/          # Config schema docs
│   │   └── scopes-table.md          # Auto-generated scope reference
│   └── examples/
│       ├── quickstart-yaml.md
│       ├── multi-agent.md
│       └── ember-case-study.md
```

### 2. Auto-Generation Pipeline

| Generated Doc | Source | Method |
|---|---|---|
| CLI reference | `cmd/aiplex-cli/` cobra commands | Go script parses cobra help → markdown |
| API reference | `internal/api/` handlers + routes | Go script extracts routes, types → markdown |
| Config schema | `.env.example`, `deploy/ory/*.yaml`, `deploy/helm/aiplex/values.yaml` | Shell script extracts keys/defaults → markdown |
| Scopes table | Scope prefixes in `internal/models/` | Script extracts patterns → markdown table |
| Examples | `/examples/*.yaml` | Copied with annotation frontmatter |

- `scripts/generate-docs.sh` orchestrates all generators
- Output lands in `_generated/` directories (gitignored)
- `npm run build` calls the generator before Docusaurus build
- `npm run generate` available standalone

### 3. Landing Page

- Hero with tagline: "One control plane for every AI interaction"
- Three-plane visual cards (MCPlex / A2APlex / LLMPlex)
- Role-based CTA funnels → admin / developer / operator quickstarts
- Feature highlights: unified auth, scopes, declarative config

### 4. DX Design Choices

- Dark/light mode (Docusaurus built-in)
- Minimal custom CSS — Infima defaults
- Code blocks with copy buttons, language tabs (YAML/JSON)
- `<PlaneBadge plane="mcplex" />` MDX component for tagging guides
- Mermaid diagrams for architecture and auth flows
- Planes as cross-cutting tags, not navigation sections

### 5. README.md (repo root)

- 30-second pitch: what AIPlex is, the three planes
- Quick local dev setup (make dev, docker-compose)
- Links into docs site for deeper content
- Badges: license, docs link
- Not a duplicate of the docs — concise entry point

## What's NOT in scope

- Versioned docs (premature — API is pre-v1)
- Search integration (Docusaurus local search is sufficient for now)
- Custom Docusaurus plugins beyond Mermaid
- Blog section
- Internationalization

## Audience Mapping

| Audience | Entry Point | Key Guides |
|---|---|---|
| Platform Admin | quickstart-admin | architecture, auth-model, identity, policy |
| Agent Developer | quickstart-developer | register-agent, oauth-flows, scopes, cross-plane-access |
| Tool/Server Operator | quickstart-operator | deploy-mcp-server, deploy-a2a-agent, configure-llm-routing, declarative-apply, manage-permissions |

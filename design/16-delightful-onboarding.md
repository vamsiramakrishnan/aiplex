# 16 — Delightful Onboarding: Zero-to-Running for Non-Experts

## The Problem

AIPlex is built on SPIFFE, mTLS, Ory Hydra, OPA, Envoy AI Gateway, GKE Autopilot, and Cloud Service Mesh. Our end users should never encounter any of these terms. They should think:

> "I have AI agents. I want to give them governed access to tools, other agents, and models. AIPlex does that."

Period. No infrastructure vocabulary. No YAML that looks like Kubernetes. No "configure your OIDC provider." If a user needs to Google a term to use AIPlex, we failed.

---

## Benchmark: Best-in-Class Onboarding

| Product | First value in... | How they do it |
|---------|-------------------|----------------|
| **Vercel** | 30 seconds | `git push` → deployed. Zero config. Framework detection. |
| **Supabase** | 60 seconds | Click "New Project" → instant Postgres + auth + API. |
| **Railway** | 90 seconds | Connect repo → auto-detect → deploy. One click. |
| **Fly.io** | 2 minutes | `fly launch` → answers 2 questions → global deploy. |
| **Clerk** | 2 minutes | Drop in `<SignIn />` component. Copy-paste integration. |
| **Stripe** | 5 minutes | Copy API key → paste in code → accept payments. |
| **Firebase** | 5 minutes | `firebase init` → pick features → `firebase deploy`. |
| **AIPlex (target)** | **60 seconds** | `aiplex deploy` → pick tool → answer 2 questions → governed access. |

### What they all share:
1. **No infrastructure vocabulary.** Vercel never says "Kubernetes." Supabase never says "PostgreSQL connection pooling." Stripe never says "PCI DSS compliance."
2. **Instant gratification.** Something works within 60 seconds. Not documentation — a working thing.
3. **Progressive complexity.** Simple by default, configurable when needed. The happy path has zero options.
4. **Copy-pasteable examples.** Not "refer to the API docs." Actual code you paste and it works.

---

## The AIPlex Language

### Terms We Use (User-Facing)

| Our Term | What It Actually Is | Why This Term |
|----------|-------------------|---------------|
| **Tool** | MCP server | Users think in tools, not protocols |
| **Agent** | OAuth client + SPIFFE workload | Users think in agents, not auth primitives |
| **Model** | LLM provider endpoint | Users think in models, not routing configs |
| **Access rule** | Hydra scope + OPA policy | Users think in permissions, not scopes |
| **Deploy** | K8s Deployment + Service + SA + MCPRoute + Hydra scopes | Users think "make it available" |
| **Connect** | Agent registration + WIF + client credentials | Users think "let this agent use that tool" |

### Terms We Never Expose

| Infrastructure Term | Where It Lives | User Sees |
|--------------------|---------------|-----------|
| SPIFFE | Identity layer | Nothing (automatic) |
| mTLS | Service mesh | "Encrypted" badge in dashboard |
| Ory Hydra | Auth server | "Login" and "Permissions" |
| OPA / Rego | Policy engine | "Access rules" |
| Envoy | Gateway | Nothing (it's plumbing) |
| MCPRoute / HTTPRoute | K8s CRDs | Nothing (generated automatically) |
| NetworkPolicy | K8s isolation | "Isolated" badge in dashboard |
| ServiceAccount | K8s identity | Nothing (automatic) |
| JWT / OAuth scope | Token format | "Permissions" |
| GKE Autopilot | Compute platform | "Cloud" or nothing |
| Cloud Service Mesh | mTLS layer | Nothing |
| Firestore | Database | Nothing |
| ext_authz | Auth check | Nothing |

---

## Onboarding Flows

### Flow 1: "I want to give my agent access to a tool" (60 seconds)

This is the #1 use case. The user has an AI agent (in Cursor, Claude Code, a custom app) and wants it to call tools.

```
$ aiplex deploy

  Welcome to AIPlex! Let's set up a tool for your agents.

? What kind of tool? (search to filter)
  ❯ Search & Knowledge
    Code & Development
    Data & Analytics
    Communication
    Custom (bring your own)

? Pick a tool:
  ❯ Document Search       "Search documents in Cloud Storage"
    Curriculum Search     "Search curriculum materials"
    Web Search            "Search the web via Google"

? Where are your documents stored?
  GCP Project: my-project
  Bucket: my-documents

  Deploying Document Search...
  ✓ Tool running
  ✓ 3 tools available: search, get_document, list_documents
  ✓ Access granted to you

  Your tool is ready!
  URL: https://aiplex.example.com/mcp/document-search-a1b2c3

  To connect an agent:
    aiplex connect tutor-agent document-search-a1b2c3

  To use in Claude Code:
    Add this to your MCP config:
    {
      "mcpServers": {
        "document-search": {
          "url": "https://aiplex.example.com/mcp/document-search-a1b2c3"
        }
      }
    }
```

**What happened behind the scenes (user never sees this):**
1. Created K8s ServiceAccount with SPIFFE identity
2. Created K8s Deployment from template image
3. Created K8s Service + NetworkPolicy
4. Called `tools/list` to discover tool names
5. Registered scopes in Hydra (`mcp:tools:search`, etc.)
6. Created MCPRoute in Envoy
7. Granted owner access in Firestore
8. Wrote instance record

**User's mental model:** "I deployed a tool. I can connect agents to it."

### Flow 2: "I want to connect my agent" (30 seconds)

```
$ aiplex connect

? What's your agent's name? tutor-agent

? How does your agent connect?
  ❯ IDE plugin (Cursor, VS Code, etc.)
    CLI tool (Claude Code, etc.)
    Backend service (API key)
    Running on another cloud (AWS, Azure)

? Which tools should tutor-agent access?
  ☑ Document Search (search, get_document, list_documents)
  ☑ Quiz Generator (generate_quiz, validate_answer)
  ☐ Grade Manager (modify_grades, view_grades)

  Connecting tutor-agent...
  ✓ Agent registered
  ✓ Access configured for 2 tools (5 capabilities)

  For IDE integration, add this to your agent config:
    Authorization URL: https://aiplex.example.com/oauth2/auth
    Client ID: tutor-agent
    Client Secret: ak_live_xxxxxxxxxxxx (save this — shown once)

  Or use the AIPlex SDK:
    pip install aiplex
    aiplex.connect("tutor-agent", "ak_live_xxxxxxxxxxxx")
```

**User's mental model:** "I connected my agent and chose what it can access."

### Flow 3: "I want to let my agent call an LLM" (20 seconds)

```
$ aiplex connect tutor-agent --model gemini-flash

  ✓ tutor-agent can now use Gemini 2.5 Flash
  
  Endpoint: https://aiplex.example.com/llm/v1/chat/completions
  Header: x-model-id: gemini-2.5-flash
  Auth: Same credentials as tool access

  Usage is tracked. Current limit: $100/day.
  Dashboard: https://aiplex.example.com/dashboard/costs
```

### Flow 4: Console Web UI (for non-CLI users)

```
┌─────────────────────────────────────────────────────────────┐
│  AIPlex                                        [admin@co.edu]│
│                                                              │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │  Welcome! Let's get your first tool running.             │ │
│  │                                                          │ │
│  │  1. [Browse Tools]  Pick from 200+ pre-built tools       │ │
│  │  2. [Connect Agent] Give your agent access               │ │
│  │  3. [Add Model]     Enable LLM access for agents         │ │
│  │                                                          │ │
│  │  Or try a quick start:                                   │ │
│  │  [Deploy Document Search in 30 seconds →]                │ │
│  └─────────────────────────────────────────────────────────┘ │
│                                                              │
│  Your Tools (0)    Your Agents (0)    Your Models (0)        │
│  ─────────────────────────────────────────────────────       │
│  No tools deployed yet. [Browse catalog →]                   │
└─────────────────────────────────────────────────────────────┘
```

---

## Platform Setup: One Command, No GKE Vocabulary

### For the platform admin (one-time setup)

```
$ aiplex platform setup

  Welcome! Let's set up AIPlex on Google Cloud.

? GCP Project ID: my-project-123
  ✓ Project found: My Project (us-central1)

? Custom domain (optional, Enter to skip): aiplex.mycompany.com
  ✓ DNS instructions will be shown at the end

? Admin email: admin@mycompany.com

  Setting up AIPlex...

  ☐ Creating infrastructure          (this takes ~5 minutes)
    ├── ☐ Compute cluster
    ├── ☐ Database
    ├── ☐ Auth services
    ├── ☐ Gateway
    └── ☐ Monitoring

  [====================                    ] 50% Creating auth services...
```

No mention of:
- GKE Autopilot (it's "compute cluster")
- AlloyDB (it's "database")
- Ory Hydra + Kratos (it's "auth services")
- Envoy AI Gateway (it's "gateway")
- OTel Collector (it's "monitoring")

```
  ✓ AIPlex is ready!

  Console: https://aiplex.mycompany.com
  API:     https://aiplex.mycompany.com/api/v1
  
  Quick start:
    aiplex login admin@mycompany.com
    aiplex deploy
  
  DNS setup needed:
    Add a CNAME record:
      aiplex.mycompany.com → 34.110.xxx.xxx.bc.googleusercontent.com
```

### Behind the scenes (admin never sees):

```
Terraform apply:
  ✓ google_container_cluster.aiplex (GKE Autopilot)
  ✓ google_sql_database_instance.aiplex (AlloyDB)
  ✓ google_firestore_database.aiplex
  ✓ google_certificate_manager_certificate.aiplex
  ✓ google_iam_workload_identity_pool.aiplex

Kubectl apply:
  ✓ namespaces (aiplex-system, mcplex, a2aplex)
  ✓ ory-hydra (Helm)
  ✓ ory-kratos (Helm)
  ✓ aiplex-api (Deployment)
  ✓ aiplex-console (Deployment)
  ✓ envoy-ai-gateway (Gateway + SecurityPolicy)
  ✓ opa-ext-authz (DaemonSet)
  ✓ otel-collector (DaemonSet)
  ✓ mesh config (PeerAuthentication + AuthorizationPolicy)
```

---

## The SDK: Copy-Paste Integration

### Python SDK

```python
# pip install aiplex

import aiplex

# Connect to AIPlex (one line)
client = aiplex.Client("https://aiplex.example.com", api_key="ak_live_xxx")

# Call a tool (feels like a function call)
results = client.tools.search("document-search-a1b2c3", query="projectile motion")

# Call an LLM (OpenAI-compatible)
response = client.llm.chat(
    model="gemini-flash",
    messages=[{"role": "user", "content": "Explain projectile motion"}]
)

# Delegate to another agent
task = client.agents.delegate("research-agent", task_type="research", input={"topic": "physics"})
```

### TypeScript SDK

```typescript
// npm install @aiplex/sdk

import { AIPlex } from '@aiplex/sdk';

const client = new AIPlex({
  url: 'https://aiplex.example.com',
  apiKey: 'ak_live_xxx',
});

// Call a tool
const results = await client.tools.call('document-search-a1b2c3', 'search', {
  query: 'projectile motion',
});

// Call an LLM (OpenAI-compatible)
const response = await client.llm.chat({
  model: 'gemini-flash',
  messages: [{ role: 'user', content: 'Explain projectile motion' }],
});
```

### MCP Client Config (for Claude Code, Cursor, etc.)

```json
{
  "mcpServers": {
    "my-tools": {
      "url": "https://aiplex.example.com/mcp/document-search-a1b2c3",
      "headers": {
        "Authorization": "Bearer ak_live_xxx"
      }
    }
  }
}
```

One JSON block. Copy. Paste. Done.

---

## Error Messages: Human, Not Technical

### Bad (infrastructure leaks)

```
Error: ext_authz denied request: OPA policy evaluation failed:
  default allow := false, no matching rule for input.attributes.request.http
```

### Good (user-centric)

```
✗ Access denied

  Your agent "tutor-agent" tried to use the tool "modify_grades"
  but doesn't have permission.

  Currently allowed tools:
    ✓ search_curriculum
    ✓ generate_quiz

  To grant access:
    aiplex allow tutor-agent modify_grades
```

### Bad (infrastructure leaks)

```
Error: Pod kb-search-server-a1b2c3-7f8d9c-xk2pm failed readiness probe:
  HTTP probe failed with statuscode: 503
  Back-off restarting failed container
```

### Good (user-centric)

```
✗ Tool "Document Search" is having trouble starting

  The tool started but isn't responding to health checks.
  This usually means the configuration is incorrect.

  Check your config:
    aiplex status document-search-a1b2c3

  Common fixes:
    • Verify the GCP project ID is correct
    • Verify the bucket name exists and is accessible
    • Check if the service account has Storage Object Viewer role

  Need help? https://docs.aiplex.dev/troubleshooting/tool-startup
```

### Bad (auth jargon)

```
Error: OAuth2 client_credentials grant failed: invalid_scope
  requested scope "mcp:tools:search" not in client allowed_scopes
```

### Good (user-centric)

```
✗ Agent "tutor-agent" can't access "search"

  This tool isn't in the agent's allowed list yet.

  To fix:
    aiplex allow tutor-agent search

  Or in the Console:
    Agents → tutor-agent → Permissions → Add "search"
```

---

## Progressive Disclosure Layers

### Layer 0: Zero Config (default)

```
aiplex deploy    # Interactive, answers everything for you
aiplex connect   # Interactive, guides you through
```

No YAML. No config files. No flags. Just questions with sensible defaults.

### Layer 1: Simple YAML (power users)

```yaml
# aiplex.yaml
kind: Tool
name: curriculum-search
template: kb-search-server
config:
  project_id: school-prod
  bucket: curriculum-docs
access:
  agents: [tutor-agent]
```

5 lines of meaningful config. No K8s vocabulary.

### Layer 2: Full YAML (operators)

```yaml
kind: Tool
name: curriculum-search
template: kb-search-server
config:
  project_id: school-prod
  bucket: curriculum-docs
access:
  agents: [tutor-agent, assessment-agent]
  users: [teachers, students]
scaling:
  min: 1
  max: 5
  target_cpu: 70
resources:
  cpu: 500m
  memory: 512Mi
monitoring:
  alerts:
    error_rate: 5%
    latency_p99: 2s
```

Still no K8s vocabulary. `scaling`, `resources`, `monitoring` are universal concepts.

### Layer 3: Escape Hatch (platform engineers)

```yaml
kind: Tool
name: curriculum-search
template: kb-search-server
config: { ... }
access: { ... }

# Override any K8s resource directly (experts only)
overrides:
  deployment:
    spec:
      template:
        spec:
          containers:
            - name: main
              env:
                - name: CUSTOM_VAR
                  valueFrom:
                    secretKeyRef:
                      name: my-secret
                      key: value
```

Only Layer 3 exposes K8s primitives, and only in an `overrides` block that's clearly marked as "you're going off-road."

---

## Documentation Structure

### Not This

```
Getting Started
├── Prerequisites
│   ├── Install gcloud CLI
│   ├── Configure GKE cluster
│   ├── Set up AlloyDB
│   ├── Install Helm
│   ├── Deploy Ory Hydra
│   ├── Deploy Ory Kratos
│   ├── Configure Envoy AI Gateway
│   └── Set up OPA policies
├── Authentication
│   ├── OAuth 2.1 overview
│   ├── OIDC configuration
│   └── ...
```

### This

```
Get Started (2 minutes)
├── Install AIPlex CLI
├── Set Up Your Platform (one command)
└── Deploy Your First Tool (one command)

Guides
├── Give an Agent Access to Tools
├── Let an Agent Call an LLM
├── Set Up Team Permissions
├── Connect Agents from AWS/Azure
└── Monitor Usage & Costs

Reference (for when you need details)
├── CLI Reference
├── YAML Reference
├── API Reference
└── Architecture (for platform engineers)
```

The architecture docs exist for platform engineers who want to understand the internals. Regular users never need them.

---

## Metrics for DX Success

| Metric | Target | How to Measure |
|--------|--------|----------------|
| Time to first tool deployed | < 60 seconds | CLI telemetry (opt-in) |
| Time to first agent connected | < 30 seconds after tool | CLI telemetry |
| Setup completion rate | > 90% | Track `aiplex platform setup` completions |
| Support questions about infrastructure | < 5% of total | Support ticket tagging |
| "How do I..." searches for internal terms | 0 | Docs search analytics |
| NPS score | > 50 | Post-onboarding survey |

---

## Design Principles (Recap)

1. **Users think in tools, agents, and models.** Never SPIFFE, OAuth, or Kubernetes.
2. **60-second first value.** Something works before they've read any docs.
3. **Interactive by default, scriptable when needed.** CLI asks questions; YAML automates.
4. **Errors explain what to do, not what went wrong internally.** Every error includes a fix command.
5. **Progressive disclosure.** Zero config → simple YAML → full YAML → escape hatch.
6. **Copy-paste integration.** SDK snippets, MCP configs, and CLI commands that work on first paste.
7. **Infrastructure is invisible.** If a user types "SPIFFE" in our search bar, something is wrong.

# 13 — Developer Experience: Obsessively Elegant DX

## Philosophy

AIPlex should feel like magic. A developer should go from "I want to deploy this MCP server" to a running, identity-bound, governed instance in under 60 seconds — with a single YAML file, a single CLI command, or a 3-question form. No Kubernetes knowledge. No OAuth expertise. No Envoy configuration.

**The DX bar we're chasing:**
- Vercel: `git push` → deployed
- Railway: one YAML → full stack
- Supabase: dashboard → instant backend
- Fly.io: `fly launch` → answers 3 questions → running globally

We take the same relentless simplicity and apply it to governed AI agent infrastructure.

---

## The Three Deployment Surfaces

### 1. The YAML File (`aiplex.yaml`)

One file. Declarative. Everything AIPlex needs to deploy, govern, and monitor.

```yaml
# aiplex.yaml — this is all you need

kind: MCPServer                    # or A2AAgent or LLMProvider
name: curriculum-search
template: kb-search-server         # from catalog, or inline image

config:
  project_id: school-prod
  bucket: curriculum-docs

access:
  agents: [tutor-agent, assessment-agent]
  users: [teachers, students]       # Kratos role names
  
# That's it. Everything below is optional.

scaling:                            # Optional: defaults to 1 replica, no autoscale
  replicas: 2
  autoscale:
    min: 1
    max: 5
    target_cpu: 70

resources:                          # Optional: defaults from template
  cpu: 500m
  memory: 512Mi

monitoring:                         # Optional: defaults to standard alerts
  alerts:
    error_rate: 5%
    latency_p99: 2s
```

**What happens when you `aiplex apply -f aiplex.yaml`:**

1. Validates YAML against schema (instant feedback)
2. Resolves template from catalog (or uses inline image)
3. Creates identity, K8s resources, discovers scopes
4. Registers scopes in Hydra
5. Grants access to specified agents and user roles
6. Creates route, persists to Firestore
7. Prints: `✓ curriculum-search deployed → https://aiplex.example.com/mcp/curriculum-search-a1b2c3`

**One file replaces:**
- K8s Deployment YAML
- K8s Service YAML
- K8s NetworkPolicy YAML
- K8s ServiceAccount YAML
- MCPRoute / HTTPRoute CRD
- Hydra scope creation API calls
- Hydra resource registration API calls
- Hydra permission policy updates
- Firestore instance record

> Decision: `aiplex.yaml` is NOT a K8s CRD. It's an AIPlex-native format that compiles down to multiple K8s resources, Hydra configs, and Firestore records. Users never touch the underlying primitives.

### 2. The CLI (`aiplex` command)

Interactive, guided, zero-config-required deployment.

```bash
$ aiplex deploy

? What do you want to deploy?
  ❯ MCP Server (tools for agents)
    A2A Agent (agent that other agents can call)
    LLM Provider (model endpoint)

? Search the catalog: curriculum
  ❯ Knowledge Base Search    (official, verified ✓)
    Curriculum RAG Server    (community)
    Document Q&A             (google-1p, verified ✓)

? Configure Knowledge Base Search:
  project_id: school-prod
  bucket: curriculum-docs

? Who should have access?
  Agents: tutor-agent, assessment-agent
  User roles: teachers, students

Deploying curriculum-search...
  ✓ Identity created (spiffe://...curriculum-search-a1b2c3)
  ✓ Pod running (1 replica, healthy)
  ✓ Tools discovered: search_curriculum, get_document
  ✓ Scopes registered in Hydra
  ✓ Access granted to 2 agents, 2 roles
  ✓ Route active: /mcp/curriculum-search-a1b2c3

✓ Done in 12s
  URL: https://aiplex.example.com/mcp/curriculum-search-a1b2c3
  Tools: search_curriculum, get_document
```

**CLI subcommands:**

```
aiplex deploy              Interactive guided deploy
aiplex apply -f file.yaml  Declarative deploy from YAML
aiplex ls                  List all instances (across planes)
aiplex ls --plane mcplex   List MCPlex instances only
aiplex status <id>         Instance health, tools, config
aiplex logs <id>           Stream instance logs
aiplex scale <id> 3        Scale to 3 replicas
aiplex config <id> set key=val  Update config
aiplex rm <id>             Undeploy
aiplex catalog search <q>  Search catalog
aiplex agents ls           List registered agents
aiplex agents grant <agent> <scope>  Quick scope grant
aiplex init                Generate aiplex.yaml interactively
aiplex validate -f file.yaml  Validate without deploying
aiplex diff -f file.yaml   Show what would change
```

### 3. The Console (Web UI)

Three-click deploy:

```
1. Click "Deploy" → Catalog opens
2. Click template → Config form appears (auto-generated from JSON Schema)
3. Fill 2-3 fields → Click "Deploy"
   → Real-time progress bar: Identity → Pod → Scopes → Route → Done
```

The Console's deploy form is auto-generated from the template's `config_schema`. No form code is written per template. Add a template to the catalog and the Console instantly knows how to deploy it.

---

## The Questionnaire System

For templates with complex configuration, AIPlex supports a `questionnaire` field — a guided, conversational config experience that works in CLI, Console, and YAML.

### Template with Questionnaire

```yaml
# In the template definition (catalog entry)
questionnaire:
  - id: project_id
    ask: "Which GCP project should this connect to?"
    type: string
    validate: "^[a-z][a-z0-9-]{4,28}[a-z0-9]$"
    hint: "Your GCP project ID (e.g., school-prod)"
    
  - id: bucket
    ask: "Which Cloud Storage bucket has your documents?"
    type: string
    hint: "The bucket name (e.g., curriculum-docs)"
    
  - id: max_results
    ask: "How many results should search return by default?"
    type: integer
    default: 10
    range: [1, 100]
    hint: "More results = slower but more complete"
    
  - id: auth_mode
    ask: "How should agents authenticate to this server?"
    type: choice
    choices:
      - value: managed
        label: "AIPlex Managed (recommended)"
        description: "AIPlex handles identity and auth automatically"
      - value: api_key
        label: "API Key"
        description: "You provide an API key (stored in Secret Manager)"
    default: managed
    
  - id: api_key
    ask: "Enter the API key:"
    type: secret
    when: "auth_mode == 'api_key'"
    hint: "This will be stored in Secret Manager, never in plain text"
```

### How the questionnaire renders:

**CLI:**
```
$ aiplex deploy kb-search-server

? Which GCP project should this connect to?
  (Your GCP project ID, e.g., school-prod)
  > school-prod

? Which Cloud Storage bucket has your documents?
  (The bucket name, e.g., curriculum-docs)
  > curriculum-docs

? How many results should search return by default? [10]
  > 10

? How should agents authenticate to this server?
  ❯ AIPlex Managed (recommended)
    API Key
```

**Console:** Same questions render as a step-by-step wizard with progress indicator.

**YAML:** Questions map directly to `config:` keys — users who know what they want skip the questionnaire entirely:

```yaml
config:
  project_id: school-prod
  bucket: curriculum-docs
  max_results: 10
  auth_mode: managed
```

---

## Infrastructure Bootstrap: One-Command Setup

The entire AIPlex platform should bootstrap from a single command.

### `aiplex platform init`

```bash
$ aiplex platform init

? GCP Project ID: my-project-123
? Region: us-central1
? Domain: aiplex.mycompany.com
? Admin email: admin@mycompany.com

Bootstrapping AIPlex platform...
  ✓ Terraform init (GKE, Firestore, AlloyDB, Secret Manager)
  ✓ GKE Autopilot cluster created
  ✓ Namespaces created (aiplex-system, mcplex, a2aplex)
  ✓ Cloud Service Mesh enabled
  ✓ Ory Hydra + Kratos deployed
  ✓ OPA policy deployed
  ✓ Envoy AI Gateway configured
  ✓ AIPlex API deployed
  ✓ Console deployed
  ✓ DNS configured (aiplex.mycompany.com)
  ✓ TLS certificate provisioned

Platform ready in 8m 23s
  Console: https://aiplex.mycompany.com
  API:     https://aiplex.mycompany.com/api/v1
  Auth:     https://aiplex.mycompany.com/oauth2
```

**Under the hood:**
1. Runs Terraform (`deploy/terraform/`) with user-provided variables
2. Waits for GKE cluster ready
3. Applies K8s manifests (`deploy/k8s/`)
4. Configures Ory Hydra + Kratos (`deploy/ory/`)
5. Validates end-to-end connectivity

### Idempotent Re-runs

```bash
# Running init again is safe — it only updates what changed
$ aiplex platform init
  ✓ GKE cluster: no changes
  ✓ Ory Hydra/Kratos: no changes
  ✓ AIPlex API: updated (new image tag)
  ✓ Console: updated (new build)
```

This is critical. `aiplex platform init` is not a "run once" script. It's a convergence loop. Run it 100 times, get the same result. Terraform + `kubectl apply` + Hydra/Kratos config all support idempotent operations.

---

## aiplex.yaml Advanced Patterns

### Multi-Instance File

```yaml
# aiplex.yaml — deploy everything at once

---
kind: MCPServer
name: curriculum-search
template: kb-search-server
config:
  project_id: school-prod
  bucket: curriculum-docs
access:
  agents: [tutor-agent]

---
kind: MCPServer
name: quiz-generator
template: quiz-gen-server
config:
  subject: mathematics
access:
  agents: [tutor-agent, assessment-agent]

---
kind: A2AAgent
name: research-agent
template: web-research-agent
config:
  search_provider: google
access:
  agents: [tutor-agent]

---
kind: LLMProvider
name: gemini-flash
template: gemini-2.5-flash
config:
  api_key_secret: gemini-api-key  # Reference to Secret Manager
access:
  agents: [tutor-agent, research-agent]
```

```bash
$ aiplex apply -f aiplex.yaml
  ✓ curriculum-search (MCPServer) → deployed
  ✓ quiz-generator (MCPServer) → deployed
  ✓ research-agent (A2AAgent) → deployed
  ✓ gemini-flash (LLMProvider) → configured
  
4 instances deployed in 34s
```

### Dry Run / Diff

```bash
$ aiplex diff -f aiplex.yaml

curriculum-search:
  ~ config.bucket: curriculum-docs → curriculum-v2-docs  (config update)
  
quiz-generator:
  (no changes)
  
research-agent:
  + scaling.replicas: 1 → 3  (scale up)
  
new-instance:
  + kind: MCPServer, template: doc-qa-server  (new deploy)
```

### GitOps: `aiplex.yaml` in Version Control

```yaml
# .github/workflows/aiplex-deploy.yml
on:
  push:
    branches: [main]
    paths: [aiplex.yaml]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: aiplex/setup-cli@v1
      - run: aiplex apply -f aiplex.yaml --yes
        env:
          AIPLEX_TOKEN: ${{ secrets.AIPLEX_TOKEN }}
```

Push your `aiplex.yaml` → CI applies it → infrastructure converges. Same workflow as Terraform or Pulumi, but for governed AI agent infrastructure.

---

## Error Messages: Helpful, Not Cryptic

Bad:
```
Error: deploy failed: rpc error: code = Unknown desc = admission webhook denied
```

Good:
```
✗ Deploy failed: curriculum-search

  The container image ghcr.io/mcp/kb-search:v99 doesn't exist.
  
  Did you mean one of these?
    ghcr.io/mcp/kb-search:v2.1.0  (latest)
    ghcr.io/mcp/kb-search:v2.0.0
  
  Fix: Update the image tag in your aiplex.yaml or use 'template: kb-search-server' 
  to pull the latest from the catalog.
```

Bad:
```
Error: 403 Forbidden
```

Good:
```
✗ Access denied: tutor-agent cannot use tool 'modify_grades'

  The agent 'tutor-agent' doesn't have scope 'mcp:tools:modify_grades'.
  
  Current scopes: search_curriculum, generate_quiz
  Missing scope: modify_grades
  
  To fix:
    aiplex agents grant tutor-agent mcp:tools:modify_grades
    
  Or add it to aiplex.yaml:
    access:
      agents: [tutor-agent]
      scopes: [modify_grades]  # ← add this
```

---

## Design Principles

### 1. Sensible Defaults, Full Override

Everything has a default. Nothing is mandatory except the absolute minimum.

| Field | Default | Override |
|-------|---------|---------|
| Replicas | 1 | `scaling.replicas: N` |
| CPU/Memory | From template, or 500m/512Mi | `resources:` block |
| Access | Owner only | `access:` block |
| Monitoring | Standard alerts | `monitoring:` block |
| Auth mode | AIPlex managed | `config.auth_mode:` |

### 2. Progressive Disclosure

- **Simple case:** 5-line YAML, 3-question CLI
- **Medium case:** 15-line YAML with access and scaling
- **Full control:** 30-line YAML with resources, monitoring, custom alerts
- **Escape hatch:** Raw K8s manifests in `advanced:` block (never recommended)

### 3. Instant Feedback

- `aiplex validate` returns in < 1 second (no API calls, schema-only)
- `aiplex diff` returns in < 3 seconds (compares desired vs actual)
- `aiplex deploy` shows real-time progress (not a spinner for 60 seconds)

### 4. Composable, Not Monolithic

- Deploy one thing or twenty things from the same YAML
- Each instance is independent — removing one doesn't affect others
- `aiplex.yaml` files can be split across directories and composed: `aiplex apply -f mcplex/ -f agents/`

### 5. No Hidden State

- `aiplex ls` always shows the truth (reads from Firestore + K8s)
- `aiplex status <id>` shows everything: config, health, scopes, route, pods
- `aiplex diff` shows exactly what will change before you apply

---

## The 60-Second Promise

From zero to governed AI tool access in 60 seconds:

```
0s   $ aiplex deploy
5s   (select template from catalog)
15s  (answer 2-3 config questions)
20s  (select which agents get access)
25s  → Deploy starts
35s  → Pod running, tools discovered
45s  → Scopes registered, access granted
55s  → Route active
60s  ✓ Done. URL printed. Agent can call tools immediately.
```

If we can't hit 60 seconds, we've failed at DX. The infrastructure complexity (SPIFFE, mTLS, Ory, OPA, Envoy) must be completely invisible.

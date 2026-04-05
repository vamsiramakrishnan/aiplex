# CLAUDE.md — AIPlex

## What This Is

AIPlex is a unified control plane for AI agent interactions. It governs three planes through a single gateway, auth stack, policy engine, and audit trail:

|Plane      |Protocol       |What It Governs|Route CRD|
|-----------|---------------|---------------|---------|
|**MCPlex** |MCP (JSON-RPC) |Agent ↔ Tool   |MCPRoute |
|**A2APlex**|A2A (HTTP/JSON)|Agent ↔ Agent  |HTTPRoute|
|**LLMPlex**|Provider APIs  |Agent ↔ Model  |LLMRoute |

You build two things: the AIPlex API (Python/FastAPI) and the AIPlex Console (React). Everything else is configuration.

**Build**: AIPlex API (Go), AIPlex Console (React), aiplex-authz (Rust)
**Configure**: Ory Hydra + Kratos (auth), Envoy AI Gateway (routing), aiplex-authz (scope check)
**Managed**: GKE Autopilot, Cloud Service Mesh, Firestore, AlloyDB, Secret Manager

-----

## Architecture

```
Agents / IDEs / CLIs / Other Agents
       │
       ▼
┌──────────────────────────────────────────────────────────────────┐
│  GKE Autopilot                                                    │
│                                                                   │
│  GKE Gateway API (Global HTTPS LB + IAP)                          │
│       │                                                           │
│       ▼                                                           │
│  Envoy AI Gateway                                                 │
│  ┌────────────────────────────────────────────────────────────┐   │
│  │                                                            │   │
│  │  /mcp/*   → MCPRoute  (tool calls, SSE, sessions)          │   │
│  │  /a2a/*   → HTTPRoute (agent delegation, task routing)     │   │
│  │  /llm/*   → LLMRoute  (model inference, failover, cache)  │   │
│  │                                                            │   │
│  │  ext_authz → OPA (JWT scope check, 20 lines of Rego)      │   │
│  │  Rate limiting + circuit breaking (per plane)              │   │
│  │  OTel traces + metrics → Cloud Observability               │   │
│  │                                                            │   │
│  └────────────────────────────────────────────────────────────┘   │
│       │ mTLS (Cloud Service Mesh)                                 │
│       │                                                           │
│  ┌────┴───────────────────────────────────────────────────────┐   │
│  │  namespace: aiplex-system                                   │   │
│  │                                                             │   │
│  │  AIPlex API      Ory Hydra       AIPlex Console            │   │
│  │  (deploy, catalog,(OAuth 2.1,    (React SPA)               │   │
│  │   registry,       token issue)                              │   │
│  │   access,                                                   │   │
│  │   consent handler) Ory Kratos                               │   │
│  │                   (identity,                                │   │
│  │                    OIDC broker)                              │   │
│  └─────────────────────────────────────────────────────────────┘   │
│       │ mTLS                                                      │
│  ┌────┴───────────────────────────────────────────────────────┐   │
│  │  namespace: mcplex      (MCP servers — tools)               │   │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐                    │   │
│  │  │github-abc│ │assess-def│ │search-ghi│  each: SPIFFE id   │   │
│  │  └──────────┘ └──────────┘ └──────────┘                    │   │
│  ├─────────────────────────────────────────────────────────────┤   │
│  │  namespace: a2aplex     (A2A agents — delegatable agents)   │   │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐                    │   │
│  │  │research  │ │viz-agent │ │summarizer│  each: SPIFFE id   │   │
│  │  └──────────┘ └──────────┘ └──────────┘                    │   │
│  ├─────────────────────────────────────────────────────────────┤   │
│  │  namespace: llmplex     (LLM provider proxies)              │   │
│  │  Envoy AI Gateway handles directly — no pods needed.        │   │
│  │  Routes to: Gemini, Claude, GPT, Bedrock, Ollama            │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                   │
│  External: Firestore │ AlloyDB │ Secret Mgr │ CA Service        │
└───────────────────────────────────────────────────────────────────┘
```

-----

## The Unified Scope Namespace

One Ory Hydra OAuth server, one token format, three planes of authorization:

```
mcp:tools:{tool_name}          MCPlex    — tool-level access
mcp:server:{server_id}                   — server-level access

a2a:task:{task_type}           A2APlex   — task delegation types
a2a:agent:{agent_id}                     — which agents you can call

llm:model:{model_id}          LLMPlex   — model access
llm:capability:{cap}                     — capability-level (vision, code-exec)
```

A single JWT carries scopes across all planes:

```json
{
  "iss": "https://aiplex.example.com/auth/realms/aiplex",
  "sub": "vamsi@example.com",
  "azp": "tutor-agent",
  "act": { "sub": "spiffe://.../ns/a2aplex/sa/tutor-agent" },
  "scope": "mcp:tools:search_curriculum mcp:tools:generate_quiz a2a:task:research llm:model:gemini-2.5-flash",
  "exp": 1714900800
}
```

-----

## Auth — Ory Hydra + Kratos

### Auth Architecture (Two Go Services)

**Ory Hydra** = OAuth 2.1 / OIDC server (Go, 30MB image, 50MB RAM). Issues JWTs, handles client credentials, PKCE, device grant. Consent is a webhook — AIPlex API owns the consent UX.

**Ory Kratos** = Identity management (Go, 30MB image, 50MB RAM). User signup/login, OIDC brokering to Google/Azure/Okta, MFA, account recovery.

|Component      |AIPlex Mapping                                                          |
|---------------|------------------------------------------------------------------------|
|Hydra Clients  |Registered agents (tutor-agent, assessment-agent, external coding-agent)|
|Kratos Users   |Humans (students, teachers, admins, parents)                            |
|Hydra Scopes   |Tool permissions, task types, model access                              |
|AIPlex API     |Consent handler, scope management, resource registration, permissions   |
|Kratos Social  |Corporate IdP via OIDC (Azure AD, Okta, Google)                         |

### Three Dimensions (All Planes)

|Dimension           |What                                          |Who Configures |Stored In                  |
|--------------------|----------------------------------------------|---------------|---------------------------|
|**A: Agent ceiling**|Which tools/tasks/models an agent can ever use|AIPlex admin   |Hydra client allowed scopes|
|**B: User ceiling** |Which tools/tasks/models a user can access    |AIPlex admin   |AIPlex API (Firestore)     |
|**C: Delegation**   |What user consented to for this specific agent|User at runtime|Hydra consent + token      |

**Effective permission = A ∩ B ∩ C**, computed by AIPlex API's consent handler during Hydra's consent webhook. The JWT carries only the intersection.

### Token Format (RFC 8693 Actor Claim)

```json
{
  "sub": "student@school.edu",
  "azp": "tutor-agent",
  "act": { "sub": "spiffe://.../sa/tutor-agent" },
  "scope": "mcp:tools:search_curriculum mcp:tools:generate_quiz a2a:task:research llm:model:gemini-2.5-flash"
}
```

`sub` = user. `act.sub` = agent. `scope` = intersection of A ∩ B ∩ C. Audit logs show both identities.

### OAuth Flows

|Agent Type          |AuthN Method                   |Grant Type               |
|--------------------|-------------------------------|-------------------------|
|Internal (same GKE) |SPIFFE (mTLS direct)           |Client Credentials       |
|External (AWS/Azure)|WIF → GCP federated token      |Client Credentials       |
|IDE (Cursor/Copilot)|User login via Kratos + Hydra  |Authorization Code + PKCE|
|CLI (Claude Code)   |User approval                  |Device Grant (RFC 8628)  |
|With user delegation|Any of the above + user consent|Authorization Code + PKCE|

### Token Hook: `act` Claim (~20 Lines Go)

Hydra calls a token hook on AIPlex API before issuing each token. The hook injects the agent’s SPIFFE ID as an RFC 8693 actor claim. No Java, no SPI JARs — just a Go HTTP handler.

-----

## OPA — Unified Policy (JWT-Only, No OPAL)

OPA’s only job: parse the JWT and check that the requested action is in the token’s scopes. Stateless. No external data. The JWT is the policy.

```rego
package aiplex.authz
import rego.v1

default allow := false

token := io.jwt.decode_verify(
    input.attributes.request.http.headers.authorization,
    {"iss": "https://aiplex.example.com/auth/realms/aiplex"}
)
claims := token[2]
scopes := split(claims.scope, " ")
body := json.unmarshal(input.attributes.request.http.body)
path := input.attributes.request.http.path

# ── MCPlex: tool calls ──
allow if {
    body.method == "tools/call"
    sprintf("mcp:tools:%s", [body.params.name]) in scopes
}

# ── A2APlex: agent-to-agent task delegation ──
allow if {
    startswith(path, "/a2a/")
    sprintf("a2a:task:%s", [body.task_type]) in scopes
}

# ── LLMPlex: model inference ──
allow if {
    startswith(path, "/llm/")
    model := input.attributes.request.http.headers["x-model-id"]
    sprintf("llm:model:%s", [model]) in scopes
}

# ── Discovery (all planes) ──
allow if {
    body.method in {"initialize", "tools/list", "resources/list",
                    "tasks/list", "agents/list", "models/list", "ping"}
}
```

Twenty lines. Covers all three planes. Deployed as a ConfigMap.

-----

## Envoy AI Gateway — Three Route Types

### MCPlex Routes (Agent ↔ Tool)

Generated by AIPlex deploy engine per MCP server:

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: MCPRoute
metadata:
  name: mcp-knowledge-base-xyz
  namespace: mcplex
spec:
  parentRefs:
    - name: aiplex-gateway
  path: "/mcp/knowledge-base-xyz"
  backendRefs:
    - name: knowledge-base-xyz
      path: "/mcp"
  securityPolicy:
    oauth:
      issuer: "https://aiplex.example.com/auth/realms/aiplex"
```

### A2APlex Routes (Agent ↔ Agent)

Generated per deployed A2A agent:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: a2a-research-agent
  namespace: a2aplex
spec:
  parentRefs:
    - name: aiplex-gateway
  rules:
    - matches:
        - path: { type: PathPrefix, value: /a2a/research-agent }
      backendRefs:
        - name: research-agent
          port: 8080
```

### LLMPlex Routes (Agent ↔ Model)

Envoy AI Gateway’s native LLM routing — no custom pods. Provider failover, load balancing, semantic caching:

```yaml
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: LLMRoute
metadata:
  name: llm-route
  namespace: aiplex-system
spec:
  parentRefs:
    - name: aiplex-gateway
  rules:
    - backendRefs:
        - name: gemini-backend
          weight: 80
        - name: claude-backend
          weight: 20
      fallback:
        - name: gpt-backend
---
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: gemini-backend
spec:
  provider: google
  apiKey:
    secretRef: { name: gemini-api-key }
```

### Shared ext_authz + Rate Limiting

```yaml
# OPA for all planes
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: SecurityPolicy
metadata:
  name: aiplex-authz
spec:
  extAuth:
    grpc:
      backendRef: { name: opa-ext-authz, port: 9191 }
    withRequestBody: { maxRequestBytes: 65536 }

# Per-user rate limits
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: global-rate-limit
spec:
  rateLimit:
    type: Global
    global:
      rules:
        - clientSelectors:
            - headers: [{ name: x-jwt-sub, type: Distinct }]
          limit: { requests: 200, unit: Minute }
```

-----

## Identity — SPIFFE Per Workload

```
Trust domain: aiplex-prod.global.PROJECT_NUMBER.workload.id.goog

aiplex-system:
  .../ns/aiplex-system/sa/aiplex-api
  .../ns/aiplex-system/sa/envoy-ai-gateway

mcplex:
  .../ns/mcplex/sa/knowledge-base-xyz
  .../ns/mcplex/sa/assessment-def
  .../ns/mcplex/sa/progress-tracker-ghi

a2aplex:
  .../ns/a2aplex/sa/research-agent
  .../ns/a2aplex/sa/viz-agent
  .../ns/a2aplex/sa/summarizer
```

Cloud Service Mesh enforces strict mTLS. AuthorizationPolicy restricts each namespace to Envoy AI Gateway ingress only.

External agents (AWS, Azure, on-prem) authenticate via Workload Identity Federation.

-----

## Deploy Engine — Unified Across Planes

```python
async def deploy(plane: str, template: Template, config: dict, owner: str) -> Instance:
    """
    Deploy to any plane. Same flow, different namespace and route type.
    plane: "mcplex" | "a2aplex" | "llmplex"
    """
    instance_id = generate_id(template.id)
    namespace = plane  # mcplex, a2aplex

    # 1. SPIFFE identity (not for llmplex — Envoy handles models directly)
    if plane != "llmplex":
        spiffe_id = await create_managed_identity(
            pool="aiplex-prod", namespace=namespace, id=instance_id)
        await create_k8s_service_account(instance_id, spiffe_id, namespace)
        await create_k8s_deployment(instance_id, template.image, config, namespace)
        await create_k8s_service(instance_id, namespace)
        await create_network_policy(instance_id, namespace)

    # 2. Discover capabilities
    if plane == "mcplex":
        tools = await call_tools_list(instance_id, namespace)
        scopes = [f"mcp:tools:{t.name}" for t in tools]
    elif plane == "a2aplex":
        tasks = await call_tasks_list(instance_id, namespace)
        scopes = [f"a2a:task:{t.type}" for t in tasks]
    elif plane == "llmplex":
        scopes = [f"llm:model:{template.model_id}"]

    # 3. Register scopes in Hydra
    for scope in scopes:
        await hydra.create_scope(name=scope, description=template.description)

    # 4. Register as OAuth resource
    await hydra.create_resource(name=instance_id, scopes=scopes)

    # 5. Generate route CRD
    if plane == "mcplex":
        await apply_mcproute(instance_id, template)
    elif plane == "a2aplex":
        await apply_httproute(instance_id, template)
    elif plane == "llmplex":
        await apply_llmroute(instance_id, template)

    # 6. Grant owner access
    await grant_user_access(owner, instance_id, scopes)

    # 7. Persist
    instance = Instance(id=instance_id, plane=plane, template_id=template.id,
        owner=owner, scopes=scopes, status="running")
    await firestore.write("instances", instance_id, instance.dict())

    return instance
```

-----

## Catalog Federation

Three catalog types, one aggregator:

```python
class CatalogAggregator:
    sources = {
        "mcplex": [
            OfficialMCPRegistry("https://registry.modelcontextprotocol.io"),
            MACHRegistry("https://registry.machalliance.org"),
            CloudAPIRegistry(),  # Google 1P MCP servers
            CustomRegistries(config.CUSTOM_REGISTRY_URLS),
            LocalTemplates(firestore, plane="mcplex"),
        ],
        "a2aplex": [
            LocalTemplates(firestore, plane="a2aplex"),
            # A2A registries as they emerge
        ],
        "llmplex": [
            BuiltInProviders(["gemini", "claude", "gpt", "bedrock", "ollama"]),
            LocalTemplates(firestore, plane="llmplex"),
        ],
    }
```

AIPlex exposes `GET /v0.1/servers` (MCPlex subregistry) so MCP clients can discover deployed tools. A2A discovery follows the A2A Agent Card specification.

-----

## AIPlex Console — Unified UI

```
┌─────────────────────────────────────────────────────────────┐
│  AIPlex Console                                              │
│                                                              │
│  ┌──────┐ ┌──────┐ ┌──────┐ ┌────────┐ ┌──────┐            │
│  │MCPlex│ │A2APl.│ │LLMPl.│ │ Agents │ │Dashb.│            │
│  └──┬───┘ └──┬───┘ └──┬───┘ └───┬────┘ └──┬───┘            │
│     │        │        │         │          │                │
│     ▼        ▼        ▼         ▼          ▼                │
│  ┌─────────────────────────────────────────────────────┐     │
│  │  MCPlex Tab:                                        │     │
│  │  Catalog: browse + deploy MCP servers (tools)       │     │
│  │  Instances: running MCP servers, health, tools list │     │
│  │  Permissions: user + agent tool-level access        │     │
│  ├─────────────────────────────────────────────────────┤     │
│  │  A2APlex Tab:                                       │     │
│  │  Catalog: browse + deploy A2A agents                │     │
│  │  Instances: running agents, task types, Agent Cards │     │
│  │  Permissions: which agents can call which agents    │     │
│  ├─────────────────────────────────────────────────────┤     │
│  │  LLMPlex Tab:                                       │     │
│  │  Providers: configure model endpoints + API keys    │     │
│  │  Routing: failover rules, weights, cost budgets     │     │
│  │  Permissions: which agents can use which models     │     │
│  ├─────────────────────────────────────────────────────┤     │
│  │  Agents Tab:                                        │     │
│  │  Registered agents (all planes): identity, perms    │     │
│  │  Cross-plane view: what can tutor-agent access?     │     │
│  │  → Tools: search_curriculum, generate_quiz          │     │
│  │  → Agents: research-agent, viz-agent                │     │
│  │  → Models: gemini-2.5-flash                         │     │
│  ├─────────────────────────────────────────────────────┤     │
│  │  Dashboard:                                         │     │
│  │  Unified metrics: tool calls + delegations + LLM    │     │
│  │  Policy denials across all planes                   │     │
│  │  Cost tracking (LLMPlex token usage)                │     │
│  │  Active delegations + agent sessions                │     │
│  └─────────────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────────────┘
```

### Console → Backend Mapping

|UI Action           |AIPlex API               |Ory Hydra/Kratos            |K8s                    |Envoy                      |
|--------------------|-------------------------|----------------------------|-----------------------|---------------------------|
|Deploy MCP server   |Firestore write          |Create scopes in Hydra      |Deployment, Service, SA|MCPRoute                   |
|Deploy A2A agent    |Firestore write          |Create scopes in Hydra      |Deployment, Service, SA|HTTPRoute                  |
|Add LLM provider    |Firestore write          |Create scopes in Hydra      |—                      |LLMRoute + AIServiceBackend|
|Register agent      |Validate WIF             |Create client in Hydra      |—                      |—                          |
|Edit agent perms (A)|Update Firestore         |Update client allowed scopes|—                      |—                          |
|Set user perms (B)  |Update Firestore         |—                           |—                      |—                          |
|User consent (C)    |Consent handler (webhook)|Hydra consent accept/reject |—                      |—                          |
|Undeploy anything   |Delete Firestore         |Delete scopes in Hydra      |Delete K8s resources   |Delete route CRD           |

-----

## Project Structure

```
aiplex/
├── CLAUDE.md
├── pyproject.toml
├── Dockerfile
├── deploy/
│   ├── terraform/
│   │   ├── gke.tf                    # GKE Autopilot + Cloud Service Mesh
│   │   ├── identity_pool.tf          # SPIFFE pool + WIF providers
│   │   ├── ory.tf                    # AlloyDB + Ory Hydra/Kratos Helm
│   │   ├── gateway.tf                # HTTPS LB + IAP + Envoy AI Gateway
│   │   ├── firestore.tf
│   │   └── artifact_registry.tf
│   ├── k8s/
│   │   ├── namespaces.yaml           # aiplex-system, mcplex, a2aplex
│   │   ├── aiplex-api.yaml
│   │   ├── aiplex-console.yaml
│   │   ├── hydra-config.yaml          # Ory Hydra configuration
│   │   ├── opa-config.yaml           # 20-line Rego policy
│   │   ├── mesh/
│   │   │   ├── peer-authentication.yaml
│   │   │   └── authorization-policy.yaml
│   │   └── otel-collector.yaml
│   └── ory/
│       ├── hydra.yaml                 # Hydra server config
│       ├── kratos.yaml                # Kratos identity config
│       ├── kratos-identity-schema.json # User identity schema
│       └── social-providers/          # OIDC provider configs
├── src/
│   └── aiplex/
│       ├── main.py                    # FastAPI entrypoint
│       ├── config.py
│       ├── models/
│       │   ├── template.py
│       │   ├── instance.py            # Unified: plane field = mcplex|a2aplex|llmplex
│       │   └── agent.py
│       ├── catalog/
│       │   ├── sources.py             # CatalogSource ABC + aggregator
│       │   ├── official_mcp.py        # Official MCP Registry v0.1
│       │   ├── google_1p.py
│       │   ├── mach.py
│       │   ├── providers.py           # Built-in LLM provider list
│       │   └── local.py               # Firestore-backed (all planes)
│       ├── registry/
│       │   ├── store.py               # Firestore CRUD
│       │   └── subregistry.py         # v0.1 MCP Registry + A2A Agent Cards
│       ├── deploy/
│       │   ├── engine.py              # Unified deploy (plane-aware)
│       │   ├── identity.py            # Managed Workload Identity
│       │   ├── routes.py              # MCPRoute / HTTPRoute / LLMRoute generation
│       │   └── manifests.py           # K8s manifest generation
│       ├── access/
│       │   ├── hydra_client.py        # Ory Hydra Admin API wrapper
│       │   ├── kratos_client.py       # Ory Kratos Admin API wrapper
│       │   ├── consent.py             # Hydra consent webhook handler
│       │   ├── token_hook.py          # Hydra token hook (act claim injection)
│       │   ├── agents.py              # Agent registration + Dim A
│       │   ├── users.py               # User permissions + Dim B
│       │   └── wif.py                 # WIF principal validation
│       └── console/
│           └── static/                # React build output
├── console/                           # React SPA source
│   └── src/
│       ├── App.tsx
│       ├── pages/
│       │   ├── MCPlex.tsx             # Tool catalog + instances + permissions
│       │   ├── A2APlex.tsx            # Agent catalog + instances + permissions
│       │   ├── LLMPlex.tsx            # Provider config + routing + permissions
│       │   ├── Agents.tsx             # Cross-plane agent view
│       │   ├── Deploy.tsx             # Unified deploy form (plane-aware)
│       │   ├── Permissions.tsx        # Unified permission editor
│       │   └── Dashboard.tsx          # Unified observability
│       └── components/
│           ├── PlaneSelector.tsx       # MCPlex / A2APlex / LLMPlex tabs
│           ├── ScopeSelector.tsx       # Checkbox list (tools / tasks / models)
│           ├── AgentCard.tsx
│           └── StatusBadge.tsx
├── policies/
│   └── aiplex_authz.rego             # 20-line unified OPA policy
└── tests/
    ├── test_deploy_mcplex.py
    ├── test_deploy_a2aplex.py
    ├── test_deploy_llmplex.py
    ├── test_access.py
    ├── test_catalog.py
    └── test_subregistry.py
```

-----

## Firestore Schema

```
instances/{id}
{
  "id": "knowledge-base-xyz",
  "plane": "mcplex",                    # "mcplex" | "a2aplex" | "llmplex"
  "template_id": "kb-search-server",
  "owner": "admin@school.edu",
  "namespace": "mcplex",
  "spiffe_id": "spiffe://...",
  "scopes": ["mcp:tools:search_curriculum", "mcp:tools:get_document"],
  "status": "running",
  "deployed_at": "2026-04-05T10:00:00Z"
}

templates/{id}                           # Cached catalog entries (all planes)
deploy_history/{id}                      # Append-only audit trail
```

Permissions are NOT in Firestore (except user ceilings / Dim B). Ory Hydra is the source of truth for OAuth clients and consent. OPA/aiplex-authz enforces at runtime.

-----

## Observability

Three telemetry sources, one OTel Collector, one Cloud Observability destination:

|Source            |Metrics                                                                                                                |
|------------------|-----------------------------------------------------------------------------------------------------------------------|
|Envoy AI Gateway  |`aiplex_tool_calls_total`, `aiplex_a2a_delegations_total`, `aiplex_llm_requests_total`, latency histograms, error rates|
|Cloud Service Mesh|mTLS handshakes, service topology, L7 access logs with SPIFFE identities                                               |
|AIPlex API        |deploy events, permission changes, agent registrations                                                                 |

Dashboard shows unified view: tool calls + agent delegations + model inference + policy denials + cost tracking.

-----

## Phased Delivery

### Phase 1: MCPlex (4 weeks)

```
Build:  AIPlex API (deploy engine for MCPlex), Console (catalog + deploy)
Deploy: GKE Autopilot, Firestore, basic Envoy Gateway
Auth:   IAP only (no Ory, no OPA)
```

Working product: browse MCP catalog, click-to-deploy, access via IAP.

### Phase 2: Auth + Tool-Level (3 weeks)

```
Add:    Ory Hydra + Kratos (Helm), AlloyDB, OPA/aiplex-authz, Envoy AI Gateway
Build:  Hydra config, consent handler, scope registration, agent registration UI, permissions UI
```

Working product: OAuth login, agent registration, tool-level consent.

### Phase 3: Zero-Trust (2 weeks)

```
Add:    Cloud Service Mesh, Managed Workload Identity, WIF providers
Build:  SPIFFE in deploy engine, act claim token hook
```

Working product: mTLS, per-server SPIFFE, cross-cloud agents.

### Phase 4: LLMPlex (2 weeks)

```
Add:    LLMRoute CRDs, AIServiceBackend configs, provider API keys
Build:  LLMPlex Console tab, model permission scopes, cost tracking
```

Working product: governed model inference with failover and cost control.

### Phase 5: A2APlex (2 weeks)

```
Add:    A2A routing (HTTPRoute), Agent Card discovery
Build:  A2APlex Console tab, task-type scopes, agent-to-agent permissions
```

Working product: governed agent delegation with identity and audit.

### Phase 6: Observability + Polish (1 week)

```
Add:    OTel Collector, unified dashboard
Build:  Custom metrics, policy denial viewer, cost dashboards
```

**Total: ~14 weeks for all three planes.**
MCPlex alone ships in 9 weeks (Phases 1-3). Each additional plane is 2 weeks of incremental work because the auth, policy, and routing infrastructure already exists.

-----

## Key Design Decisions

### Why one gateway for all three planes?

Envoy AI Gateway already handles MCP (MCPRoute), LLM inference (LLMRoute with provider failover), and arbitrary HTTP (HTTPRoute for A2A). One gateway = one auth check, one rate limiter, one audit trail. Adding a plane is adding a route CRD, not a new system.

### Why Ory Hydra + Kratos, not Keycloak?

Go-native (30MB images vs Keycloak’s 500MB), 50MB RAM vs 1.5GB, sub-second startup vs 30s. Hydra’s consent webhook means AIPlex owns the consent UX entirely (React in the Console, not a Keycloak FreeMarker theme). Token hook for custom claims — no Java SPI. Same OAuth 2.1 / OIDC compliance, 15x smaller footprint.

### Why one Hydra instance for all planes?

An agent’s permissions span planes: the tutor agent calls tools (MCPlex), delegates to other agents (A2APlex), and calls models (LLMPlex). A single OAuth server with unified scopes means one token carries all three. No cross-service token exchange.

### Why unified OPA policy?

The Rego policy is 20 lines because the pattern is identical: parse the JWT, check if the requested action’s scope is in the token’s claims. Different scope prefixes (`mcp:`, `a2a:`, `llm:`) route to different `allow` rules, but the mechanism is the same.

### Why separate K8s namespaces per plane?

Blast radius isolation. A compromised MCP server can’t reach A2A agents. A rogue A2A agent can’t call models. Network policies + mesh AuthorizationPolicy enforce this at the infrastructure level.

### Why not custom auth?

Auth is commodity plumbing. Ory Hydra gives OAuth 2.1 token issuance, DCR, PKCE, device grant. Ory Kratos gives login, MFA, OIDC brokering, account recovery. Both are Go, cloud-native, and lightweight. Every hour saved on auth goes into the deploy UX, catalog federation, and governance that differentiate AIPlex.

-----

## Example: Ember (Aristocratic Tutoring Platform)

Ember uses all three planes through AIPlex:

```
Student asks: "Why does a ball follow a parabolic path?"

Tutor Agent (AIPlex token: mcp + a2a + llm scopes)
    │
    ├── LLMPlex: POST /llm/v1/chat/completions
    │   Model: gemini-2.5-flash
    │   "Plan a Socratic dialogue about projectile motion"
    │
    ├── MCPlex: POST /mcp/knowledge-base-xyz/mcp
    │   tools/call: search_curriculum("projectile motion")
    │
    ├── A2APlex: POST /a2a/physics-viz-agent
    │   Task: generate interactive parabola simulation
    │
    ├── MCPlex: POST /mcp/progress-tracker/mcp
    │   tools/call: read_mastery("projectile-motion")
    │
    └── Delivers Socratic response with simulation link
```

One agent, three planes, one token, full audit trail.

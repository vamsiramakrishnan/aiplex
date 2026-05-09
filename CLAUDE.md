# CLAUDE.md — AIPlex

## What This Is

**AIPlex is the capability OS for delegated AI.**

Every action an agent can take — calling a tool, invoking another agent, querying a model, running a workflow, reading or writing memory — is a typed, addressable, governable, revocable **Capability** bound to the human who delegated it. One primitive across every kind. Vendor-portable. Locally hostable. Verifiable.

This is what makes AIPlex categorically different from AWS Bedrock AgentCore (AWS-native, multiple subsystems) and Google Vertex AI Agent Engine (GCP-native, runtime-coupled). They are *vendor* managed agent stacks. AIPlex is the *user-owned* substrate beneath whatever runtime the agent uses. See [design/24](design/24-agent-and-workflow-as-cap.md) for the agent-as-cap architecture and concrete scenarios.

| Kind       | What it governs                              | Provider                  | Default namespace |
|------------|----------------------------------------------|---------------------------|--------------------|
| `tool`     | MCP tool call                                | MCP server                | `mcplex`           |
| `task`     | A2A task delegation                          | A2A agent                 | `a2aplex`          |
| `model`    | LLM inference                                | Envoy LLMRoute            | `aiplex-system`    |
| `skill`    | Skill bundle execution                       | Skill server              | `skillsplex`       |
| `memory`   | Memory namespace operation                   | Memory broker (in-process)| `memplex`          |
| `agent`    | Hosted agent runtime (ADK, LangGraph, Letta) | External HTTP endpoint    | `agentplex`        |
| `workflow` | Declarative cap chain                        | Workflow executor (in-process) | `aiplex-system`    |
| `meta`     | AIPlex itself (deploy, govern)               | AIPlex API                | `aiplex-system`    |

Adding the next kind costs ~200 LOC, not 2,000. The plane proliferation tax is gone.

You build three things: the **AIPlex API** (Go), the **AIPlex Console** (React), and **aiplex-authz** (Rust). Everything else is configuration.

**Managed**: GKE Autopilot, Cloud Service Mesh, Firestore, AlloyDB, Secret Manager.

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
│  │  /cap/<kind>/<name>@<version>  → CapabilityRoute           │   │
│  │                                                            │   │
│  │  capability resolver  → ext_authz (single-rule OPA)        │   │
│  │  constraint filter    (rate, budget, tenant, key prefix)   │   │
│  │  Rate limiting + circuit breaking (per kind)               │   │
│  │  OTel traces + metrics → Cloud Observability               │   │
│  │                                                            │   │
│  └────────────────────────────────────────────────────────────┘   │
│       │ mTLS (Cloud Service Mesh)                                 │
│       │                                                           │
│  ┌────┴───────────────────────────────────────────────────────┐   │
│  │  namespace: aiplex-system                                   │   │
│  │  AIPlex API   Ory Hydra    Ory Kratos    AIPlex Console     │   │
│  ├─────────────────────────────────────────────────────────────┤   │
│  │  namespace: mcplex      (kind=tool)                         │   │
│  │  namespace: a2aplex     (kind=task)                         │   │
│  │  namespace: skillsplex  (kind=skill)                        │   │
│  │  namespace: memplex     (kind=memory)                       │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                   │
│  External: Firestore │ AlloyDB │ Secret Mgr │ CA Service        │
└───────────────────────────────────────────────────────────────────┘
```

-----

## The Capability URI

```
cap://<kind>/<name>[/<sub-path>]@<version>
```

- `kind`   = `tool` | `task` | `model` | `skill` | `memory` | `meta`
- `name`   = bare name; may contain `/` for hierarchical/tenanted names
- `version`= `v1`, `v1.2`, `v1.2.3`, or `latest`

Examples:

```
cap://tool/search_curriculum@v1
cap://task/research@v1.2
cap://model/gemini-2.5-flash@v1
cap://skill/code-review/review_pr@v1
cap://memory/students/alice/profile@v1
cap://meta/deploy@v1
```

The URI is the universal identifier. It is the unit of catalog browsing, deployment, routing, policy, audit, and SDK call. Every cross-cutting concern keys on the URI.

-----

## The `caps` Claim

The JWT carries a structured `caps` array, not opaque scope strings:

```json
{
  "iss": "https://aiplex.example.com/auth/realms/aiplex",
  "sub": "vamsi@example.com",
  "azp": "tutor-agent",
  "act": { "sub": "spiffe://.../ns/a2aplex/sa/tutor-agent" },
  "caps": [
    {
      "uri": "cap://tool/search_curriculum@v1",
      "actions": ["call"],
      "constraints": { "rate_per_min": 30 }
    },
    {
      "uri": "cap://model/gemini-2.5-flash@v1",
      "actions": ["complete"],
      "constraints": { "monthly_token_budget": 1000000 }
    }
  ]
}
```

Constraints travel with the cap. The gateway enforces them. The receipt records them.

-----

## Auth — Ory Hydra + Kratos

|Component      |AIPlex Mapping                                                 |
|---------------|---------------------------------------------------------------|
|Hydra Clients  |Registered agents (tutor-agent, assessment-agent, …)           |
|Kratos Users   |Humans (students, teachers, admins, parents)                   |
|AIPlex API     |Consent handler, agent registry, user cap (Dimension B), token hook |
|Kratos Social  |Corporate IdP via OIDC (Azure AD, Okta, Google)                |

### Three Dimensions

|Dimension           |What                                          |Stored In                    |
|--------------------|----------------------------------------------|-----------------------------|
|**A: Agent ceiling**|Capabilities the agent can ever request       |`models.Agent.AllowedCaps`   |
|**B: User ceiling** |Capabilities the user has access to           |Firestore `user_caps/{userId}` |
|**C: Delegation**   |Caps the user consented to for this agent     |Hydra consent + token        |

**Effective permission = A ∩ B ∩ C**, computed by the consent handler. The JWT carries only the intersection.

### OAuth Flows

|Agent Type          |AuthN Method                   |Grant Type               |
|--------------------|-------------------------------|-------------------------|
|Internal (same GKE) |SPIFFE (mTLS direct)           |Client Credentials       |
|External (AWS/Azure)|WIF → GCP federated token      |Client Credentials       |
|IDE (Cursor/Copilot)|User login via Kratos + Hydra  |Authorization Code + PKCE|
|CLI (Claude Code)   |User approval                  |Device Grant (RFC 8628)  |
|With user delegation|Any of the above + user consent|Authorization Code + PKCE|

The token hook (~30 lines Go) injects the agent's SPIFFE ID into the `act` claim (RFC 8693).

-----

## OPA — Single Rule, All Kinds

The capability resolver (Envoy ext_proc filter, Rust) inspects the request and writes `(uri, action)` into filter metadata. OPA's only job: confirm the `caps` claim grants what was requested.

```rego
package aiplex.authz
import rego.v1

default allow := false

token := payload if {
    [valid, _, payload] := io.jwt.decode_verify(
        input.attributes.request.http.headers.authorization,
        {"iss": "https://aiplex.example.com/auth/realms/aiplex"}
    )
    valid
}

requested_uri    := input.attributes.metadata_context.filter_metadata["aiplex.cap"].uri
requested_action := input.attributes.metadata_context.filter_metadata["aiplex.cap"].action

cap_grants(c, uri, action) if {
    c.uri == uri
    action in c.actions
}

matching_cap := c if {
    some c in token.caps
    cap_grants(c, requested_uri, requested_action)
}

allow if matching_cap

discovery_actions := {"initialize", "discover", "describe", "ping", "health"}
allow if {
    requested_action in discovery_actions
    token
}
```

One rule. Every kind. Adding a kind doesn't touch OPA.

-----

## Envoy AI Gateway — One CRD

`CapabilityRoute` (`aiplex.dev/v1alpha1`) is the only route CRD AIPlex emits. Underneath, Envoy AI Gateway primitives (LLMRoute, MCPRoute) are still used as data-plane backends for `kind=model` etc., but operators see one shape.

```yaml
apiVersion: aiplex.dev/v1alpha1
kind: CapabilityRoute
metadata:
  name: cap-search-curriculum-v1
  namespace: aiplex-system
spec:
  capability:
    uri: cap://tool/search_curriculum@v1
    kind: tool
    name: search_curriculum
    version: v1
    provider:
      spiffeId: spiffe://aiplex/.../sa/knowledge-base-xyz
      kind: KubernetesService
      name: knowledge-base-xyz
      namespace: mcplex
      port: 8080
    auth:
      requiredActions: [call]
  routing:
    parentRefs:
      - name: aiplex-gateway
        namespace: aiplex-system
    pathTemplate: /cap/tool/search_curriculum@v1
    timeout: 30s
  kindOverrides:
    model:                       # only used when kind=model
      modelId: "gemini-2.5-flash"
      provider: "google"
      backendRef: { name: <inst-id>-backend }
```

The deploy engine emits one `CapabilityRoute` per cap the instance provides. For `kind=model` it also emits an Envoy `AIServiceBackend` for provider routing.

-----

## Identity — SPIFFE Per Workload

```
Trust domain: aiplex-prod.global.PROJECT_NUMBER.workload.id.goog

aiplex-system:
  .../ns/aiplex-system/sa/aiplex-api
  .../ns/aiplex-system/sa/envoy-ai-gateway

mcplex (kind=tool):
  .../ns/mcplex/sa/<instance-id>

a2aplex (kind=task):
  .../ns/a2aplex/sa/<instance-id>

skillsplex (kind=skill):
  .../ns/skillsplex/sa/<instance-id>

memplex (kind=memory):
  .../ns/memplex/sa/<memory-broker>
```

Cloud Service Mesh enforces strict mTLS. AuthorizationPolicy restricts each namespace to gateway ingress only. External agents (AWS, Azure, on-prem) authenticate via Workload Identity Federation.

-----

## Deploy Engine — One Path, Six Kinds

```go
func (e *Engine) Deploy(ctx context.Context, kind capability.Kind, templateID string,
                        config map[string]any, owner, displayName string) (*models.Instance, error) {

    tmpl, _ := e.store.GetTemplate(ctx, templateID)
    if kind == "" { kind = tmpl.Kind }

    instanceID := generateID(templateID)
    namespace  := kind.Namespace()                       // mcplex|a2aplex|skillsplex|memplex|aiplex-system

    // 1. SPIFFE identity (skip for kind=model and kind=meta)
    spiffeID := ""
    if kind != capability.KindModel && kind != capability.KindMeta {
        spiffeID = fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", e.trustDomain, namespace, instanceID)
    }

    // 2. Seed capabilities from template
    inst := &models.Instance{
        ID: instanceID, Kind: kind, Namespace: namespace, SpiffeID: spiffeID,
        Capabilities: tmpl.CapSet(), TemplateID: templateID, Owner: owner, ...
    }
    e.store.PutInstance(ctx, inst)

    // 3. Apply K8s manifests (SA, Deployment, Service, NetworkPolicy)
    for _, m := range GenerateManifests(inst, tmpl, e.trustDomain) { e.k8s.Apply(ctx, m) }

    // 4. Discover real caps post-deploy (kind-specific):
    //    tool → MCP tools/list, skill → skills/list, task → A2A Agent Card / tasks/list
    if discovered := e.discover(ctx, inst, logger); len(discovered) > 0 {
        inst.Capabilities = discovered
    }

    // 5. Apply CapabilityRoute manifests (one per capability the instance provides)
    for _, m := range GenerateRoute(inst, tmpl, e.gatewayName) { e.k8s.Apply(ctx, m) }

    // 6. Grant the owner the caps just exposed
    existing, _ := e.store.GetUserCaps(ctx, owner)
    e.store.SetUserCaps(ctx, owner, existing.Union(inst.Capabilities))

    inst.Status = models.StatusRunning
    e.store.PutInstance(ctx, inst)
    return inst, nil
}
```

One function. One persistence path. One route generator. The `kind` discriminator drives namespace and discovery; everything else is shared.

-----

## Catalog Federation

One `CapabilitySource` interface, sources grouped by kind:

```go
type Source interface {
    Name() string
    Kind() capability.Kind
    Fetch(ctx context.Context) ([]models.Template, error)
}
```

Built-in sources:

| Source                        | Kind     | What                                          |
|-------------------------------|----------|-----------------------------------------------|
| `OfficialMCPSource`           | `tool`   | Official MCP Registry (registry.modelcontextprotocol.io) |
| `BuiltInProviders`            | `model`  | Gemini, Claude, GPT, Bedrock, Ollama          |
| `BuiltInSkills`               | `skill`  | Curated bundles (code-review, research, writing) |
| `LocalSource(store, kind)`    | any      | Firestore-backed templates per kind           |

Adding a federated registry for tasks or memory is one new file plus a registration in `cmd/aiplex-api/main.go`.

-----

## AIPlex Console — Capability Graph (proposed) + Per-Kind Views (current)

The Console exposes a per-kind tab view today (Tools, Tasks, Models, Skills, Memory). The next iteration replaces tabs with a unified Capability Graph (force-directed: agents → caps → providers, animated by live receipts). Per-kind tabs become saved filters.

### Console → Backend Mapping

|UI Action            |AIPlex API                |Ory Hydra/Kratos             |K8s                 |Envoy            |
|---------------------|--------------------------|-----------------------------|--------------------|-----------------|
|Deploy (any kind)    |Firestore write           |—                            |Deployment, SA, NP  |CapabilityRoute  |
|Add LLM provider     |Firestore write           |—                            |—                   |+AIServiceBackend|
|Register agent       |Validate WIF              |Create client in Hydra       |—                   |—                |
|Edit agent caps (A)  |`agent.AllowedCaps`       |Update client allowed scopes |—                   |—                |
|Set user caps (B)    |Firestore `user_caps/{u}` |—                            |—                   |—                |
|User consent (C)     |Consent handler webhook   |Hydra `caps` claim emit      |—                   |—                |
|Undeploy             |Delete Firestore          |—                            |Delete K8s          |Delete CR        |

-----

## Project Structure

```
aiplex/
├── CLAUDE.md                           # this doc
├── design/                             # 18-22 cover Capability Mesh in depth
├── go.mod
├── pyproject.toml
├── deploy/
│   ├── terraform/                      # GKE, AlloyDB, Hydra/Kratos, gateway, identity
│   ├── k8s/                            # namespaces, gateway, OPA, mesh, otel
│   └── helm/aiplex/                    # one chart, one CRD definition
├── policies/
│   └── aiplex_authz.rego               # one rule, all kinds
├── authz/
│   └── cap-resolver/                   # Rust ext_proc — request → (uri, action)
├── internal/
│   ├── capability/                     # primitive: URI, Kind, Cap claim, Capability
│   ├── models/                         # Instance, Template, Agent, Delegation, …
│   ├── registry/                       # Store interface + Firestore + MemoryStore
│   ├── catalog/                        # Source interface + per-kind sources
│   ├── deploy/                         # engine, manifests, routes, discovery
│   ├── auth/                           # Hydra, Kratos, consent, WIF, token hook
│   ├── api/                            # HTTP handlers
│   └── secrets/                        # Secret Manager validation
├── cmd/
│   ├── aiplex-api/main.go              # entrypoint + router
│   └── aiplex-cli/                     # cobra CLI
├── sdk/aiplex/                         # Go SDK
└── console/                            # React SPA
```

-----

## Firestore Schema

```
instances/{id}
{
  "id": "knowledge-base-xyz",
  "kind": "tool",
  "template_id": "kb-search-server",
  "owner": "admin@school.edu",
  "namespace": "mcplex",
  "spiffe_id": "spiffe://...",
  "capabilities": [
    {"uri": "cap://tool/search_curriculum@v1", "actions": ["call"]},
    {"uri": "cap://tool/get_document@v1", "actions": ["call"]}
  ],
  "status": "running",
  "deployed_at": "2026-04-05T10:00:00Z"
}

templates/{id}                            # Cached catalog entries (all kinds)
deploy_history/{id}                       # Append-only audit trail
user_caps/{userId}                        # Dimension B: structured cap ceiling
agents/{clientId}                         # AllowedCaps = Dimension A
```

Permissions are NOT in Firestore (except `user_caps`). Hydra is the source of truth for OAuth clients and consent. OPA enforces at runtime.

-----

## Observability

|Source            |Metrics                                                                                                                |
|------------------|-----------------------------------------------------------------------------------------------------------------------|
|Envoy AI Gateway  |`aiplex_cap_invocations_total{kind,uri}`, latency histograms, error rates                                              |
|Cloud Service Mesh|mTLS handshakes, service topology, L7 access logs with SPIFFE identities                                               |
|AIPlex API        |deploy events, permission changes, agent registrations                                                                 |

Dashboard surfaces a unified view: cap invocations + denials + cost tracking, broken down by `kind`. The next iteration ships a Capability Graph render with live receipt animation (see design/22).

-----

## Phased Delivery

The four-plane phasing has been replaced by the [Capability Mesh roadmap](design/22-roadmap-100x.md). Today's working slice:

- **Done:** Capability primitive · single CapabilityRoute generator · single OPA rule · capability-aware deploy engine · `KindHook` extension point · Hydra `caps` token hook · per-kind catalog sources · structured user-caps Dimension B · MemPlex (`kind=memory`) with PII redaction and pluggable backends · agent-as-cap (`kind=agent`) wrapping external runtimes · workflow-as-cap (`kind=workflow`) chaining caps with token threading · console & CLI capability-aware
- **Next:** `aiplex up` single-binary local stack · personal cap vault (`aiplex vault export/import`) · live receipt streams in the Console · code interpreter as built-in cap · capability resolver (Rust ext_proc) · JIT step-up consent · signed-receipt trust ledger · unified Capability Graph in Console — see design/22 + design/24.

-----

## Key Design Decisions

### Why one primitive?

Four planes carried seven duplicated implementation surfaces each. Adding a fifth would be linear-cost extension dressed as elegance. Collapsing to one primitive (Capability) makes future kinds ~200 LOC additions, not multi-week projects. See design/18.

### Why one CRD?

Operators think "what can this agent do" — they don't think "MCPRoute vs LLMRoute." The CRD shape was 90% identical across the four old types; the 10% that differs lives in `spec.kindOverrides`. See design/19.

### Why structured `caps` instead of scope strings?

Constraints travel with the grant. Rate, budget, key-prefix, tenant — all enforced uniformly by one constraint filter. Type-safe at every boundary instead of string-prefix archaeology in OPA, Console, and deploy engine. See design/18.

### Why Ory Hydra + Kratos, not Keycloak?

Go-native (30MB image vs Keycloak's 500MB), 50MB RAM vs 1.5GB, sub-second startup vs 30s. Hydra's consent webhook means AIPlex owns the consent UX entirely (React in Console, not a Keycloak FreeMarker theme). Token hook for `caps` claim — no Java SPI. Same OAuth 2.1 / OIDC compliance, 15× smaller footprint.

### Why not custom auth?

Auth is commodity plumbing. Ory Hydra gives OAuth 2.1, DCR, PKCE, device grant. Ory Kratos gives login, MFA, OIDC brokering, account recovery. Both are Go and lightweight. Every hour saved on auth goes into the deploy UX, catalog federation, and capability mesh that differentiate AIPlex.

-----

## Example: Ember (Aristocratic Tutoring Platform)

```
Student asks: "Why does a ball follow a parabolic path?"

Tutor Agent (token caps: tool + task + model + memory)
    │
    ├── cap://model/gemini-2.5-flash@v1 [complete]
    │   "Plan a Socratic dialogue about projectile motion"
    │
    ├── cap://tool/search_curriculum@v1 [call]
    │   query: "projectile motion"
    │
    ├── cap://task/visualize@v1 [invoke]
    │   "Generate interactive parabola simulation"
    │
    ├── cap://memory/students/alice/profile@v1 [read, write]
    │   read prior mastery, write new concept
    │
    └── Delivers Socratic response with simulation link
```

One agent, four capability kinds, one structured `caps` claim, full receipt trail.

# 19 — CapabilityRoute: One CRD

> **Status:** Proposed. Replaces the per-plane CRD layer (`MCPRoute`, `HTTPRoute`, `LLMRoute`) with a single `CapabilityRoute` resource and a small **capability resolver** Envoy extension.
> **Companion to:** [18 — Capability Mesh](18-capability-mesh.md), [22 — Roadmap](22-roadmap-100x.md).

---

## Problem

Today the deploy engine generates one of four CRD types based on the plane:

```go
switch plane {
case mcplex:    applyMCPRoute(...)
case a2aplex:   applyHTTPRoute(...)
case llmplex:   applyLLMRoute(...)
case skillsplex: applyHTTPRoute(...)  // + custom annotations
}
```

Four CRDs means:
- Four reconcilers in the gateway operator
- Four sets of admission webhooks
- Four Console schemas to render
- Four backup/restore paths
- Four sets of tests

The route shapes are 90% identical: `parentRefs`, `rules.matches[].path`, `backendRefs`, security policy. The 10% that differs (LLM provider failover, MCP session affinity) belongs in the **filter chain**, not in the CRD type.

---

## Design

### The CRD

```yaml
apiVersion: aiplex.dev/v1alpha1
kind: CapabilityRoute
metadata:
  name: cap-search-curriculum-v1
  namespace: aiplex-system
spec:
  capability:
    uri: cap://tool/search_curriculum@v1
    kind: tool                              # tool | task | model | skill | memory | meta
    provider:
      kind: KubernetesService
      name: knowledge-base-xyz
      namespace: mcplex
      port: 8080
    schema:
      configMapRef:                         # JSON Schema for input/output
        name: cap-search-curriculum-v1-schema
    attrs:
      side_effect: read
      data_class: public
      cost_tier: free
      latency_budget_ms: 800
    auth:
      required_actions: [call]
      audience: ["aiplex-gateway"]

  # How requests reach this capability. Most fields default.
  routing:
    parentRefs:
      - name: aiplex-gateway
        namespace: aiplex-system
    pathTemplate: "/cap/{kind}/{name}@{version}"   # default; overridable
    timeout: 30s
    retries:
      maxAttempts: 2
      retryOn: [5xx, gateway-error]

  # kind-specific overrides. Empty for most kinds.
  kindOverrides:
    model:
      failover:
        - { capability: cap://model/claude-sonnet-4-6@v1, weight: 100 }
      semanticCache:
        ttl: 1h
        similarityThreshold: 0.95
    tool:
      sessionAffinity: header
      sessionHeader: x-mcp-session-id

status:
  observedGeneration: 1
  conditions:
    - type: Ready
      status: "True"
      reason: Reconciled
      lastTransitionTime: "2026-05-09T12:00:00Z"
  resolvedProvider: spiffe://aiplex-prod/.../sa/knowledge-base-xyz
  effectivePath: "/cap/tool/search_curriculum@v1"
  receiptStream: "projects/.../logs/aiplex.cap.tool.search_curriculum.v1"
```

### Why this shape

1. **`spec.capability` is the single source of truth.** It mirrors the Capability record in [doc 18](18-capability-mesh.md) one-to-one. The Capability's URI uniquely identifies the route; the resolver derives `(uri, action)` from `pathTemplate` matches.
2. **`spec.routing` is plane-agnostic.** Every kind needs `parentRefs`, path, timeout, retries. Defaults reduce boilerplate to near-zero.
3. **`spec.kindOverrides` carries the 10%.** Only set fields for the relevant kind. The reconciler ignores irrelevant overrides. New kinds add a new sub-struct, not a new CRD.

---

## The Reconciler

`internal/deploy/routes/reconciler.go` (new package):

```go
type Reconciler struct {
    K8s       client.Client
    Envoy     EnvoyAdmin       // applies XDS resources
    Resolver  ResolverConfig   // capability resolver registration
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    var route aiplexv1.CapabilityRoute
    if err := r.K8s.Get(ctx, req.NamespacedName, &route); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 1. Validate the capability URI and schema.
    cap, err := capability.Parse(route.Spec.Capability.URI)
    if err != nil { return ctrl.Result{}, r.fail(&route, "InvalidURI", err) }

    // 2. Render the gateway-API HTTPRoute (or LLMRoute for kind=model).
    base := r.renderHTTPRoute(&route, cap)
    if cap.Kind == capability.KindModel {
        base = r.augmentForLLM(base, &route)
    }

    // 3. Apply the resolver registration so the capability resolver knows
    //    how to map this path back to (uri, action) at runtime.
    resolverEntry := capresolver.Entry{
        PathTemplate: route.Spec.Routing.PathTemplate,
        URI:          cap.URI,
        Actions:      cap.Auth.RequiredActions,
        Provider:     cap.Provider,
    }
    if err := r.Resolver.Upsert(ctx, resolverEntry); err != nil {
        return ctrl.Result{}, err
    }

    // 4. Apply the underlying gateway resource.
    if err := r.applyManaged(ctx, &route, base); err != nil {
        return ctrl.Result{}, err
    }

    // 5. Update status.
    return ctrl.Result{}, r.markReady(&route, cap)
}
```

The reconciler has one branch — `kind == model` — and even that is a small augmentation, not a different code path. Adding `kind: memory` (doc 20) requires zero changes to this file.

---

## The Capability Resolver

A small Envoy filter that runs **before** ext_authz. Its job: take the request and write the `(capability_uri, action)` pair into filter metadata so OPA can read it.

### Filter chain

```
incoming request
   │
   ▼
[1] capability_resolver           ← writes filter_metadata["aiplex.cap"]
   │
   ▼
[2] ext_authz (OPA)               ← reads filter_metadata, checks token.caps
   │
   ▼
[3] capability_constraints        ← enforces structured constraints (rate, budget, prefix)
   │
   ▼
[4] kind-specific filter chain    ← MCP framing, LLM provider routing, etc.
   │
   ▼
upstream cluster
```

### Implementation

`authz/cap-resolver/` (Rust, alongside the existing `aiplex-authz`):

```rust
// authz/cap-resolver/src/main.rs (sketch)
struct Resolver {
    routes: Arc<RwLock<RouteTable>>,    // synced from CapabilityRoute reconciler
}

impl Resolver {
    fn resolve(&self, req: &HttpRequest) -> Option<CapInvocation> {
        let entry = self.routes.read().longest_prefix_match(req.path())?;
        let action = match entry.uri.kind {
            Kind::Tool => self.tool_action(req)?,           // body.method == tools/call → "call"
            Kind::Task => self.task_action(req)?,           // method/path → "invoke" | "cancel"
            Kind::Model => Some("complete".into()),
            Kind::Skill => self.skill_action(req)?,
            Kind::Memory => self.memory_action(req)?,       // POST /read → "read"
            Kind::Meta => self.meta_action(req)?,
        };
        Some(CapInvocation {
            uri: entry.uri.clone(),
            action,
            request_size: req.body_len(),
            tenant: extract_tenant(req),
        })
    }
}

// gRPC ext_proc handler writes:
// filter_metadata["aiplex.cap"] = {"uri": "...", "action": "call", "request_size": 1234}
```

The resolver is declarative — it's a route table keyed by path template, populated from CapabilityRoute objects. ~80 LOC of Rust including tests.

### Why a separate resolver, not done in OPA?

OPA stays stateless and policy-only. Knowing how to parse an MCP body to extract `tools/call` is data-plane logic, not policy. Splitting them keeps each piece small and testable.

---

## Constraint Enforcement

The third filter (`capability_constraints`) reads:
1. The matched cap claim (passed forward by ext_authz in dynamic metadata)
2. The request attributes

…and enforces structured constraints. Examples:

| Constraint key            | Filter behavior                                                    |
|---------------------------|--------------------------------------------------------------------|
| `rate_per_min: 30`        | Token bucket per `(user, agent, capability)`. 429 on exhaustion.   |
| `max_input_bytes: 65536`  | Reject request body over limit. 413.                               |
| `monthly_token_budget`    | Decrement on response (LLM usage). 402 on exhaustion.              |
| `key_prefix: "lesson-*"`  | Validate request body's `key` field against glob. 403 on miss.    |
| `read_only: true`         | Reject write actions. 403.                                         |
| `tenant: "acme"`          | Validate request's tenant header matches. 403.                     |

The filter is generic — it dispatches by constraint key to a registered handler. New constraint types register a handler in ~20 LOC.

---

## Migration from Existing CRDs

### Compatibility window

Both old and new CRD types reconcile during transition:

| Phase | Old CRDs | New CRDs | Notes                                          |
|-------|----------|----------|------------------------------------------------|
| 0     | active   | absent   | Today                                          |
| 1     | active   | active   | Deploy engine writes both for new instances    |
| 2     | active   | active   | `aiplex migrate routes` converts existing      |
| 3     | dormant  | active   | Old reconciler stops; CRDs remain readable     |
| 4     | removed  | active   | Old CRDs deleted in next minor                 |

### The migration tool

```bash
aiplex migrate routes --dry-run
# Prints conversion plan:
#   MCPRoute/mcp-knowledge-base-xyz -> CapabilityRoute/cap-search-curriculum-v1
#   MCPRoute/mcp-knowledge-base-xyz -> CapabilityRoute/cap-get-document-v1
#   LLMRoute/llm-route -> 5 CapabilityRoutes (one per model in failover)

aiplex migrate routes --apply
# Creates CapabilityRoute objects, leaves old CRDs intact.

aiplex migrate routes --finalize
# Removes the old CRDs after verifying the new ones are Ready.
```

### Conversion rules (one-pager)

| Old field                                  | New field                                   |
|--------------------------------------------|---------------------------------------------|
| `MCPRoute.spec.path`                       | `CapabilityRoute.spec.routing.pathTemplate` |
| `MCPRoute.spec.backendRefs`                | `spec.capability.provider`                  |
| `HTTPRoute.spec.rules[].matches[].path`    | `spec.routing.pathTemplate`                 |
| `LLMRoute.spec.rules[].backendRefs[]`      | `spec.kindOverrides.model.failover[]`       |
| `LLMRoute.spec.semanticCache`              | `spec.kindOverrides.model.semanticCache`    |
| Implicit (per-plane scope prefix)          | `spec.capability.uri`                       |
| Implicit (per-plane action set)            | `spec.capability.auth.requiredActions`      |

The migration tool generates the explicit fields from the implicit per-plane conventions.

---

## Validation & Admission

```go
// internal/api/admission/capabilityroute.go
func (v *Validator) Validate(ctx context.Context, route *aiplexv1.CapabilityRoute) error {
    cap, err := capability.Parse(route.Spec.Capability.URI)
    if err != nil { return err }

    // Kind exists.
    kindSpec, ok := capability.Kinds[cap.Kind]
    if !ok { return fmt.Errorf("unknown kind: %s", cap.Kind) }

    // Provider reachable.
    if err := v.checkProvider(ctx, route.Spec.Capability.Provider); err != nil {
        return err
    }

    // Schema (if provided) is valid JSON Schema.
    if ref := route.Spec.Capability.Schema.ConfigMapRef; ref != nil {
        if err := v.validateSchemaConfigMap(ctx, ref); err != nil { return err }
    }

    // Constraints in auth.required_actions are members of kindSpec.AllowedActions.
    for _, a := range route.Spec.Capability.Auth.RequiredActions {
        if !slices.Contains(kindSpec.AllowedActions, a) {
            return fmt.Errorf("kind %q does not support action %q", cap.Kind, a)
        }
    }

    // kindOverrides only set for matching kind.
    if err := v.checkKindOverrides(cap.Kind, route.Spec.KindOverrides); err != nil {
        return err
    }

    return nil
}
```

Admission rejects malformed routes early. The reconciler trusts what admission accepts.

---

## What Goes Away

| Today                                              | Tomorrow                                       |
|----------------------------------------------------|------------------------------------------------|
| `internal/deploy/routes.go` (per-plane branching)  | `internal/deploy/routes/reconciler.go` (unified) |
| `MCPRoute` + `LLMRoute` + custom annotations       | `CapabilityRoute`                              |
| Plane-specific tests in `tests/`                   | `tests/capability_route_test.go` table-driven  |
| Console route detail panels per plane              | One `CapabilityRoutePanel` component           |

Net code change at the CRD/route layer: **~−1500 LOC, +700 LOC**.

---

## Open Questions

> **Open:** Should `CapabilityRoute` be cluster-scoped or namespaced? Decision: **namespaced, lives in `aiplex-system`**, references providers across namespaces. Avoids RBAC explosions and matches the Gateway API convention.

> **Open:** Do we wrap Envoy's `LLMRoute` underneath, or implement LLM filters ourselves? Decision: **wrap for v1**, own the filter chain when LLMRoute can't express what we need (e.g., per-tenant budget enforcement).

> **Open:** Versioning of the CRD itself. Decision: ship as `aiplex.dev/v1alpha1`, promote to `v1beta1` after one release with no breaking changes, `v1` after the migration completes.

---

## See Also

- [18 — Capability Mesh](18-capability-mesh.md)
- [20 — MemPlex](20-memplex-memory-plane.md) — the first kind to ship native to this CRD
- [04 — Envoy Gateway & Routing](04-envoy-gateway-routing.md) — current state being replaced
- [06 — Deploy Engine](06-deploy-engine.md) — for the apply path

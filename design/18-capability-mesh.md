# 18 — The Capability Mesh: One Primitive, Every Plane

> **Status:** Proposed. Successor design that subsumes the per-plane architecture in docs 02–10.
> **Migration is additive** — every existing plane keeps working through the cutover (see [22 — Roadmap](22-roadmap-100x.md)).

---

## The Elegance Critique

AIPlex today has four planes: MCPlex, A2APlex, LLMPlex, SkillsPlex. Each plane carries the same seven implementation surfaces:

| Surface                | MCPlex                    | A2APlex                | LLMPlex                  | SkillsPlex              |
|------------------------|---------------------------|------------------------|--------------------------|-------------------------|
| Scope prefix           | `mcp:tools:*`             | `a2a:task:*`           | `llm:model:*`            | `skill:*`               |
| Route CRD              | `MCPRoute`                | `HTTPRoute`            | `LLMRoute`               | `HTTPRoute` + custom    |
| Console tab            | `MCPlex.tsx`              | `A2APlex.tsx`          | `LLMPlex.tsx`            | `SkillsPlex.tsx`        |
| Catalog source         | `OfficialMCPSource`       | `LocalSource`          | `BuiltInProviders`       | `BuiltInSkills`         |
| Deploy engine branch   | `if plane == "mcplex"`    | `… == "a2aplex"`       | `… == "llmplex"`         | `… == "skillsplex"`     |
| OPA policy block       | `tools/call` rule         | `/a2a/` rule           | `/llm/` rule             | `skills/exec` rule      |
| Test file              | `test_deploy_mcplex.py`   | `test_deploy_a2aplex…` | `test_deploy_llmplex…`   | `skills_test.go`        |

**Adding the 5th plane (Memory) the current way costs seven new surfaces.** The cost is linear in plane count. That is not 100x. That is paying the same architectural rent over and over.

The 100x move is structural: **find the shared primitive and collapse the four planes into one.**

---

## The Primitive: A `Capability`

> A **Capability** is a typed, addressable, governable unit of agent action.

Every plane today already implements a Capability — they just don't share the type:

- An MCP tool (`search_curriculum`) is a Capability.
- An A2A task (`research`) is a Capability.
- An LLM model (`gemini-2.5-flash`) is a Capability.
- A skill (`code-review`) is a Capability.
- A memory namespace (`student-profile`) **will be** a Capability.

If we name the primitive once, every other surface (scope, route, Console, catalog, policy, deploy, audit) becomes one implementation parameterised by `kind`.

### Definition

```
Capability := (uri, provider, schema, attrs, auth)

uri      := cap://<kind>/<name>@<version>
kind     := tool | task | model | skill | memory | meta | …
provider := SPIFFE ID of the workload that serves this capability
schema   := JSON Schema for input + output (per action)
attrs    := { cost_tier, side_effect, data_class, latency_budget, … }
auth     := { required_actions, step_up_rules, audience }
```

`kind` is open-ended on purpose. New planes mean new `kind` values, not new code paths.

### Examples

```yaml
# An MCP tool
uri: cap://tool/search_curriculum@v1
provider: spiffe://aiplex-prod/ns/mcplex/sa/knowledge-base-xyz
schema:
  call:
    input: { query: string, top_k: integer }
    output: { results: array }
attrs:
  side_effect: read
  data_class: public
  cost_tier: free
  latency_budget_ms: 800
auth:
  required_actions: [call]

# An A2A task
uri: cap://task/research@v1
provider: spiffe://aiplex-prod/ns/a2aplex/sa/research-agent
schema:
  invoke:
    input: { topic: string, depth: enum[shallow, deep] }
    output: { artifact_url: string }
attrs:
  side_effect: read
  data_class: internal
  cost_tier: standard
  latency_budget_ms: 30000
auth:
  required_actions: [invoke, cancel]

# An LLM model
uri: cap://model/gemini-2.5-flash@v1
provider: spiffe://aiplex-prod/ns/aiplex-system/sa/envoy-ai-gateway
schema:
  complete:
    input:  { messages: array, max_tokens: integer }
    output: { text: string, usage: object }
attrs:
  side_effect: external
  data_class: regulated      # data leaves the boundary → flag
  cost_tier: pay-per-token
  latency_budget_ms: 5000
auth:
  required_actions: [complete]
  step_up_rules:
    - when: data_class == regulated
      require: user_present_in_last_minutes(5)

# A memory namespace (new — see doc 20)
uri: cap://memory/student-profile@v1
provider: spiffe://aiplex-prod/ns/memplex/sa/memory-broker
schema:
  read:   { input: { key: string }, output: { value: any } }
  write:  { input: { key: string, value: any }, output: {} }
  search: { input: { embedding: array, top_k: integer }, output: { hits: array } }
attrs:
  side_effect: write
  data_class: pii
  retention_days: 365
auth:
  required_actions: [read, write, search]
```

### Why this works

1. **Cardinality lives in data, not code.** Adding a plane = adding a `kind` value, schemas, and capability records. The deploy engine, gateway, policy, console, catalog, and audit code stays the same.
2. **Attributes enable expressive policy.** "Allow any read-side-effect tool on data_class:public" replaces hand-rolled scope lists.
3. **Type-safe everywhere.** `cap://tool/foo@v1` parses to `{kind: tool, name: foo, version: v1}`. No string-prefix archaeology in OPA, the Console, or the deploy engine.
4. **Versionable.** `@v1` is part of the URI; `@v2` deploys side-by-side; deprecation is a graph operation.
5. **Inspectable.** Every capability is discoverable via `GET /capabilities/{uri}` — schema, provider SPIFFE, attrs, audit history. One endpoint replaces four plane-specific listings.

---

## The Capability URI Grammar

```
capability-uri = "cap://" kind "/" name [ "/" sub-path ] "@" version
kind           = 1*( ALPHA / DIGIT / "-" )
name           = segment *( "/" segment )                ; allows hierarchical names
segment        = 1*( ALPHA / DIGIT / "-" / "_" )
sub-path       = "/" segment *( "/" segment )            ; tenant scoping, multi-namespace
version        = "v" 1*DIGIT [ "." 1*DIGIT [ "." 1*DIGIT ] ] / "latest"
```

### Examples by pattern

```
cap://tool/search_curriculum@v1
cap://task/research@v1.2
cap://model/gemini-2.5-flash@v1
cap://memory/students/alice/profile@v1     # tenant-scoped via sub-path
cap://meta/deploy@v1                       # AIPlex governs itself
cap://skill/code-review@v2.1.0
```

### Reserved kinds (v1)

| kind     | semantics                                  | provider                 |
|----------|--------------------------------------------|--------------------------|
| `tool`   | MCP tool call                              | MCP server (mcplex ns)   |
| `task`   | A2A task invocation                        | A2A agent (a2aplex ns)   |
| `model`  | LLM inference                              | Envoy LLMRoute backend   |
| `skill`  | Skill bundle execution                     | Skill runtime            |
| `memory` | Memory namespace operation                 | Memory broker (memplex)  |
| `meta`   | AIPlex itself (deploy, register, govern)   | AIPlex API               |

New kinds require: a name reservation in `internal/capability/kinds.go`, a JSON Schema for the action set, and a route handler entry. Nothing else.

---

## The Token: `caps` Replaces `scope`

Today's JWT carries an opaque scope string:

```json
{
  "sub": "vamsi@example.com",
  "azp": "tutor-agent",
  "act": { "sub": "spiffe://.../sa/tutor-agent" },
  "scope": "mcp:tools:search_curriculum mcp:tools:generate_quiz a2a:task:research llm:model:gemini-2.5-flash"
}
```

The new claim is structured:

```json
{
  "sub": "vamsi@example.com",
  "azp": "tutor-agent",
  "act": { "sub": "spiffe://aiplex-prod/.../sa/tutor-agent" },
  "caps": [
    {
      "uri": "cap://tool/search_curriculum@v1",
      "actions": ["call"],
      "constraints": { "rate_per_min": 30 }
    },
    {
      "uri": "cap://task/research@v1",
      "actions": ["invoke"]
    },
    {
      "uri": "cap://model/gemini-2.5-flash@v1",
      "actions": ["complete"],
      "constraints": { "max_tokens_per_call": 8192, "monthly_token_budget": 1000000 }
    },
    {
      "uri": "cap://memory/students/alice/profile@v1",
      "actions": ["read", "write"],
      "constraints": { "key_prefix": "lesson-*" }
    }
  ],
  "scope": "mcp:tools:search_curriculum mcp:tools:generate_quiz a2a:task:research llm:model:gemini-2.5-flash"
}
```

> **Decision:** Both `caps` and `scope` are emitted for one full release cycle. `caps` is authoritative; `scope` is a compatibility shim for older clients and the existing OPA policy. After the deprecation window, `scope` is dropped.

### Cap entry schema

```go
// internal/capability/claim.go
type Cap struct {
    URI         string            `json:"uri"`                   // cap://kind/name@ver
    Actions     []string          `json:"actions"`               // e.g. ["call"], ["read","write"]
    Constraints map[string]any    `json:"constraints,omitempty"` // structured, kind-specific
    NotBefore   int64             `json:"nbf,omitempty"`         // epoch seconds
    NotAfter    int64             `json:"exp,omitempty"`         // epoch seconds (per-cap, overrides token exp)
}
```

`Constraints` is intentionally a free-form map — kind-specific code interprets it. Examples:

| kind   | constraint keys                                              |
|--------|--------------------------------------------------------------|
| tool   | `rate_per_min`, `max_input_bytes`                            |
| task   | `max_concurrent`, `priority_ceiling`                         |
| model  | `max_tokens_per_call`, `monthly_token_budget`, `temperature_max` |
| memory | `key_prefix`, `tenant`, `read_only`                          |

### Why structured constraints in the token

Today, "200 tool calls per minute" lives in Envoy `BackendTrafficPolicy`. "1M tokens/month for this user" lives in a separate LLM budget controller. Each is configured out-of-band.

With cap claims, the **token itself carries the constraint**. The gateway enforces it. The constraint travels with the user-agent pair and is auditable in the receipt (see [21 — Trust Ledger](21-runtime-consent-and-trust-ledger.md)). One mechanism replaces three.

---

## The OPA Policy: Single Rule

Today's policy is 20 lines but contains four parallel `allow if { … }` blocks (one per plane). The new policy is one rule:

```rego
package aiplex.authz
import rego.v1

default allow := false

# Verify and decode the JWT against Hydra's JWKS.
token := payload if {
    [valid, _, payload] := io.jwt.decode_verify(
        input.attributes.request.http.headers.authorization,
        {"iss": "https://aiplex.example.com/auth/realms/aiplex"}
    )
    valid
}

# The gateway populates these via a small request-routing extension
# (the "capability resolver") that turns request path + body into a
# (capability_uri, action) pair before ext_authz fires.
requested_uri    := input.metadata_context.filter_metadata["aiplex.cap"]["uri"]
requested_action := input.metadata_context.filter_metadata["aiplex.cap"]["action"]

# Find a matching cap claim.
matching_cap := c if {
    some c in token.caps
    cap_matches(c.uri, requested_uri)
    requested_action in c.actions
    not_expired(c)
}

allow if {
    matching_cap
    constraints_ok(matching_cap.constraints, input)
}

# Discovery / introspection actions are allowed for any authenticated caller.
allow if {
    requested_action in {"discover", "describe", "ping", "health"}
}
```

Three things changed:

1. **One rule, all kinds.** No plane-specific branching.
2. **Capability resolution moved out of policy.** A small Envoy extension (the *capability resolver*) inspects the request and writes `(uri, action)` to filter metadata. OPA reads metadata. Policy stays stateless and trivial.
3. **Constraint enforcement is co-located.** `constraints_ok` checks rate, budget, prefix, etc. against request attributes (size, timestamp, headers). One place, all planes.

The capability resolver is ~80 lines of Go — see [19 — CapabilityRoute](19-capability-route-crd.md).

---

## The Console: One View

Four plane tabs become one **Capability Graph**:

- **Nodes:** users, agents, capabilities (coloured by `kind`), providers.
- **Edges:** "has cap" (token), "called" (audit), "delegates to" (act-claim chains).
- **Filters:** by kind (tool/task/model/skill/memory), by attribute (data_class, side_effect), by time window.
- **Pivots:** "what can `tutor-agent` do?" (agent → capabilities), "who's calling `cap://tool/grade@v1`?" (capability → callers), "what touched `student-profile` in the last hour?" (capability → audit).

The plane tabs become saved filters: "kind = tool" is the MCPlex view. Same data, less UI.

---

## Mapping the Old World to the New

| Old concept                      | New concept                                          |
|----------------------------------|------------------------------------------------------|
| Plane (mcplex/a2aplex/llmplex)   | Capability `kind` tag                                |
| Scope `mcp:tools:foo`            | Cap claim `{uri: cap://tool/foo@v1, actions:[call]}` |
| MCPRoute / HTTPRoute / LLMRoute  | Single `CapabilityRoute` CRD (doc 19)                |
| Plane-specific catalog source    | `CapabilitySource` interface returning `Capability`s |
| Plane-specific Firestore field   | `instances/{id}.capabilities[]`                      |
| 4 OPA `allow` blocks             | 1 OPA rule + capability resolver                     |
| 4 Console tabs                   | 1 Capability Graph + saved filters                   |

---

## Migration Strategy

The migration is a strict superset, never a breaking change. See doc 22 for phasing. The contract:

1. Token issuer adds `caps` while keeping `scope` for one release window (≥90 days).
2. Capability resolver runs in **shadow mode** first — it computes `(uri, action)` and emits a metric, but OPA still uses the legacy scope-string check. We compare and alert on divergence for two weeks before flipping.
3. Old route CRDs continue to reconcile during the transition. New deploys default to `CapabilityRoute`. `aiplex migrate routes` performs the conversion.
4. Old catalog sources and tests are kept until the new `CapabilitySource` interface is at parity.

No flag day. No down-tools rewrite.

---

## What This Buys

| Concern                              | Before                          | After                                |
|--------------------------------------|---------------------------------|--------------------------------------|
| Adding a 5th plane (Memory)          | 7 new surfaces, ~3 weeks        | 1 schema + ~200 LOC, ~2 days         |
| New constraint (per-tenant rate)     | New CRD field + Envoy filter    | Add to `constraints` map             |
| "What can this agent do?" query      | Join across 4 systems           | One Capability Graph traversal       |
| Audit a capability across planes     | 4 different log shapes          | One receipt schema (doc 21)          |
| Policy authoring                     | 4 plane-specific Rego blocks    | One rule + structured constraints    |
| OAuth client config                  | 4 scope namespaces              | 1 `caps` claim                       |
| SDK surface                          | Per-plane methods               | `client.invoke(cap_uri, args)`       |

The win compounds. Every future feature ships once.

---

## Open Questions

> **Open:** Do we expose `cap://` URIs to end users in the Console, or hide them behind friendly names? Decision: show them in tooltips and CLI; hide in primary UI. URIs are for power users and audit.

> **Open:** Should `version` support semver ranges (`@^v1`)? Decision: not in v1 — pinning prevents drift. Range support can come later if needed.

> **Open:** Should constraints be JSON Schema-validated per kind? Decision: yes — `internal/capability/kinds.go` registers a constraint schema per kind; the token issuer validates before signing.

---

## See Also

- [19 — CapabilityRoute CRD](19-capability-route-crd.md) — the K8s API surface
- [20 — MemPlex](20-memplex-memory-plane.md) — the proof: a new plane built native to this abstraction
- [21 — Runtime Consent & Trust Ledger](21-runtime-consent-and-trust-ledger.md) — JIT step-up + signed audit
- [22 — The 100x Roadmap](22-roadmap-100x.md) — phased delivery

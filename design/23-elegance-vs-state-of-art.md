# 23 — Elegance vs. State of the Art

> **Status:** Reference. What we've actually built that's better than what's
> shipping today, with concrete code citations. Not aspirational.

This doc names the unfair advantages AIPlex has now that the [Capability
Mesh](18-capability-mesh.md) has landed and [MemPlex](20-memplex-memory-plane.md)
is the first kind built natively on it. Each section: the competitor pattern,
what's different in AIPlex, where to find it.

If we hold these properties as the bar, every future feature has to clear them
or admit we made it worse.

---

## The unfair advantage in one paragraph

Every other agent control plane treats Identity, Routing, Catalog, Memory,
Audit, and Tools as **separate subsystems** — separate APIs, separate CRDs,
separate SDKs, separate auth checks. AIPlex collapses them around a single
primitive: a **Capability** with a `cap://kind/name@version` URI. The same
URI is the catalog ID, the deploy target, the route name, the OPA scope, the
audit subject, and the SDK call. Adding the next surface (`kind`) is a 200-LOC
addition to one file, not a parallel subsystem. Nothing else in the field is
shaped this way.

---

## 1. Tool / MCP gateways

### Solo.io agentgateway

| Property                          | Solo agentgateway                  | AIPlex                                       |
|-----------------------------------|------------------------------------|----------------------------------------------|
| Protocols handled                 | MCP, A2A (separate filter chains)  | One pipeline; kind tags discriminate        |
| Authz unit                        | Per-protocol scopes                | Single Capability URI; one OPA rule (`policies/aiplex_authz.rego`, 25 lines) |
| Memory governance                 | Out of scope                       | Same primitive (`cap://memory/...`); no separate plane |
| Adding a new protocol             | New filter chain                   | New `Kind` value + `KindHook`               |

**Borrow:** Solo's data-plane Rust implementation is fast; we should benchmark
the capability resolver against it.

**Don't borrow:** the protocol-as-axis split. We'd be re-introducing the plane tax.

### Docker MCP Toolkit

Docker's `docker mcp` ships a polished CLI but the trust model is
container-only — no tenant/user scoping, no consent UX, no cross-tool
constraint enforcement. `aiplex deploy --kind tool` produces the same
"60-second deploy" experience but with structured caps + tenant-bound access
out of the gate (`internal/deploy/engine.go:Deploy`).

### Smithery / Glama / PulseMCP

These are catalog directories with their own APIs. AIPlex aggregates them
through a single `Source` interface (`internal/catalog/sources.go`):

```go
type Source interface {
    Name() string
    Kind() capability.Kind
    Fetch(ctx context.Context) ([]models.Template, error)
}
```

Adding Smithery as a federated source is **one new file** (~80 LOC) returning
`Capability` records. The aggregator (`Aggregator.Fetch`) merges by kind. The
existing `OfficialMCPSource` (`internal/catalog/official_mcp.go`) is the
reference implementation.

**Operator wins:**
- One catalog endpoint covers every registry.
- Trust signals (verified, pricing, attrs) ride on the unified `Template` type.
- Adding a registry is read-only and never touches the deploy or auth path.

### IBM MCP Context Forge

Closest open-source competitor on the gateway+catalog axis. Differs because:

- Context Forge has separate route types per protocol; AIPlex has one
  `CapabilityRoute` (`internal/deploy/routes.go:GenerateRoute`).
- Context Forge bakes Hydra/Keycloak knowledge into every operator; AIPlex
  hides Hydra entirely behind the consent webhook + `caps` claim — operators
  edit one Cap claim, not OAuth scopes.

---

## 2. Agent runtimes (Bedrock AgentCore, Letta, LangGraph)

### AWS Bedrock AgentCore

AgentCore is the closest in scope: Identity, Gateway, Memory, Code Interpreter,
Observability — five separate primitives. Each has its own API surface, IAM
policy shape, and SDK path.

AIPlex compresses the same surface into **one primitive with five `kind`
values**:

| AgentCore subsystem      | AIPlex equivalent                          |
|--------------------------|--------------------------------------------|
| Identity (per-agent)     | Cap claim + SPIFFE in `act` (RFC 8693)     |
| Gateway (MCP routing)    | `kind=tool` with `CapabilityRoute`         |
| Memory                   | `kind=memory` (`internal/memplex/`)        |
| Code Interpreter         | `kind=tool` cap; standard MCP server       |
| Observability            | Same OTel exporter; per-`kind` rollups      |

Operationally this means:

- **One IAM model**, not five. `agent.AllowedCaps` covers every surface.
- **One SDK shape**: `client.Memory(uri)`, `client.Tool(uri)`,
  `client.Model(uri)`, `client.Skill(uri)` — all return a typed namespace
  handle on the same client (`sdk/aiplex/client.go`, see `MemoryNamespace`).
- **One audit query** answers "what did this user do" across surfaces, because
  every receipt carries the same `(agent, user, cap_uri, action)` shape.

**Borrow:** AgentCore's Code Interpreter is genuinely useful. We should ship
a built-in `cap://tool/code-interpreter@v1` MCP server template alongside
the existing `BuiltInProviders` and `BuiltInSkills` (similar shape to
`BuiltInMemory` we just shipped — see `internal/catalog/builtin_memory.go`).

### Letta (formerly MemGPT)

Letta makes memory a first-class agent runtime. AIPlex makes memory a first-class
*capability*. The key difference: Letta couples memory to a runtime; AIPlex
treats memory as a substrate every agent runtime can use, regardless of who
runs the agent.

Concrete proof: `internal/memplex/broker.go` is **228 LOC** of Go and ships
read/write/search/list/subscribe across pluggable backends — it doesn't know
or care what calls it. The deploy hook (`internal/memplex/hook.go`, 60 LOC)
plugs into the engine via the kind-agnostic `KindHook` interface
(`internal/deploy/engine.go`):

```go
type KindHook interface {
    OnRegister(ctx context.Context, inst *models.Instance,
               cap capability.Cap, attrs capability.Attrs) error
    OnUnregister(ctx context.Context, inst *models.Instance,
                 cap capability.Cap) error
}
```

Letta's value moves to AIPlex as **another backend** behind the `MemoryBackend`
interface, not as a competing runtime. Same for Vertex Vector Search,
AlloyDB+pgvector, and Firestore. One namespace migrates from one backend to
another with a config change; the cap URI never moves.

---

## 3. LLM gateways (LiteLLM, Portkey, Kong AI, Higress)

### LiteLLM

LiteLLM is the de-facto open-source LLM gateway. Its strength is *breadth*
of provider coverage. Its weakness is being a separate concern from tool
governance — operators end up with one auth/audit story for tools and a
different one for models.

In AIPlex, models are `kind=model` capabilities. The same `caps` claim that
lets `tutor-agent` call `cap://tool/search_curriculum@v1` also gates
`cap://model/gemini-2.5-flash@v1`. The same OPA rule. The same constraint
filter. The same audit log.

**Constraint co-location is the key win.** LiteLLM's per-model token budget
lives in a separate config file. AIPlex carries it inside the cap claim:

```json
{
  "uri": "cap://model/gemini-2.5-flash@v1",
  "actions": ["complete"],
  "constraints": { "monthly_token_budget": 1000000, "max_tokens_per_call": 8192 }
}
```

The token bucket is the agent×user×cap budget, not a global model budget. This
is uniquely expressible because the constraint travels with the cap, not with
the deployment (see `internal/capability/claim.go:Cap.Constraints`).

**Borrow:** semantic prompt cache. LiteLLM's is good; we should ship one as
a constraint-handler in the constraint filter (the same way TTL is implemented
in `internal/memplex/local_backend.go`).

### Portkey / Kong AI Gateway

Both are commercial AI gateways with strong dashboards. AIPlex's dashboard is
sparser today but the data model is richer: a Capability invocation receipt
covers every kind, so dashboards for tools/tasks/models/skills/memory all draw
from one schema.

---

## 4. Identity / consent (Auth0 GenAI, WorkOS AuthKit, Stytch, Descope)

These vendors are productizing the patterns AIPlex ships natively:

| Vendor pattern                                  | AIPlex equivalent                              |
|-------------------------------------------------|------------------------------------------------|
| RFC 8693 actor-claim for delegated agents       | Token hook injects `act` (`internal/api/auth.go:TokenHook`) |
| Async / step-up consent on missing scope        | Designed in [doc 21](21-runtime-consent-and-trust-ledger.md) — JIT 401 challenge |
| Vault for delegated agent credentials           | `agent.ClientSecret` returned only on registration; never persisted |
| Cross-domain trust via OIDC federation          | WIF (`internal/auth/wif.go`) — works for any cloud |

**The real elegance gap is at the *type* level.** Auth0 / WorkOS still treat
"agent identity" as a tag on top of OAuth client. AIPlex defines `act` as a
SPIFFE ID and `caps` as structured permissions, both first-class JWT claims.
Code that consumes a token doesn't ask "is this an agent?" — it asks "does
the cap allow this URI/action?" (`policies/aiplex_authz.rego`, 25 lines, all
kinds).

---

## 5. Service mesh / zero-trust (Tetrate Agent Operating Director)

Tetrate's value is Istio expertise repackaged for agents. AIPlex ships the
same SPIFFE-everywhere mesh in `deploy/k8s/mesh/` but with one critical
addition: the **identity is the cap claim**. Tetrate carries identity through
the mesh; AIPlex carries identity *and* authorization. There's no separate
"who is this and what can they do" lookup downstream — the JWT has both, and
OPA's job is a one-line check.

---

## 6. Audit / verifiable receipts

Nobody we surveyed (Solo, Tetrate, IBM, AWS, Smithery, Letta, LangGraph)
ships **verifiable** audit. They all ship "trust the logs."

AIPlex's design (doc 21) makes every cap invocation produce a receipt signed
by gateway + provider, chained per-stream, optionally anchored in sigstore
Rekor. The data structure is uniform across every kind because **invocations
are uniform** — a tool call, a memory write, an LLM completion all have the
same `(agent, user, cap_uri, action, request_hash, response_hash)` shape.

This is feasible for AIPlex specifically because the Capability is the unit
of audit — there's nothing to invent at the schema layer. Other systems would
have to design 4-6 receipt shapes (one per subsystem) and merge them.

---

## 7. CLI / DX (Docker MCP, gh CLI, fly.io, vercel)

Docker MCP and fly.io set the bar for "60 seconds from zero to running." AIPlex
matches it on `aiplex deploy --kind tool` thanks to one-shot deploy
(`internal/deploy/engine.go`) and the `aiplex apply -f manifest.yaml`
declarative path (`cmd/aiplex-cli/cmd_apply.go`).

The differentiator: a *single* CLI surface for every kind. `aiplex deploy
--kind memory --template scratch` works the same way as `--kind tool`,
because the deploy engine is kind-agnostic and the manifest schema is uniform:

```yaml
version: v1
instances:
  - name: knowledge-base
    kind: tool
    template: kb-search-server
  - name: alice-scratch
    kind: memory
    template: scratch
```

In Docker MCP / fly / vercel, each new resource type adds CLI vocabulary. In
AIPlex, the vocabulary is constant and the `--kind` flag changes meaning at
the data layer, not the verb layer.

---

## 8. Concrete things we built that nobody else has shipped

These are not "designed in a doc" — they exist in code today on
`claude/aiplex-next-steps-IQxqX`:

| Property                                            | File                                               | LOC  |
|-----------------------------------------------------|----------------------------------------------------|------|
| Single Capability primitive (URI + Kind + Cap)      | `internal/capability/`                             | ~250 |
| Single CapabilityRoute generator (replaces 4)       | `internal/deploy/routes.go:GenerateRoute`          | ~140 |
| Single OPA rule across all kinds                    | `policies/aiplex_authz.rego`                       | 25   |
| KindHook lifecycle abstraction in the deploy engine | `internal/deploy/engine.go:KindHook`               | 8    |
| Memory broker with 6 ops + 5-backend pluggability   | `internal/memplex/broker.go`                       | ~230 |
| PII redaction on top of every memory backend        | `internal/memplex/pii.go`                          | ~80  |
| Templated memory URIs (`{tenant}`, `{user}`)        | `internal/capability/uri.go:segmentPattern`        | 1 line change |
| Structured `caps` JWT claim with constraints        | `internal/capability/claim.go`                     | ~125 |
| Cross-kind dashboard rollup                         | `internal/api/dashboard.go:GetStats`               | ~60  |
| Capability-aware Go SDK (`client.Memory(uri)…`)     | `sdk/aiplex/client.go:MemoryNamespace`             | ~120 |

Total addition for the new memory plane after the Capability Mesh landed:
**~600 LOC including tests**. For comparison, equivalent capability in
AgentCore is several services across multiple repositories; in Letta it's
~10k LOC of agent-runtime-coupled code; in Solo agentgateway memory is out
of scope.

---

## 9. Where competitors are still ahead

Honesty matters more than positioning. Things we don't have yet:

- **Semantic prompt cache** for `kind=model`. LiteLLM and Portkey ship this;
  we plan to (doc 22 phase 4) but haven't.
- **Code Interpreter as a built-in capability**. AgentCore ships one. We don't.
- **Browser / pluggable LLM-side guardrails**. Kong AI Gateway has a richer
  guardrail story.
- **Hosted catalog UX**. Smithery has install counts, last-tested timestamps,
  signature trust; our catalog has just `Verified bool`.
- **Async consent UX in the Console**. Auth0 has push-notification consent;
  ours is designed (doc 21) but not built.
- **JetBrains/VS Code/Cursor IDE integrations**. They exist for AgentCore and
  some MCP tools; ours is on the roadmap.

The Capability Mesh makes each of these easier to add — they're all
constraint-handler or built-in template additions, not architectural changes.

---

## 10. The test for "is this elegant"

For every new feature, ask:

1. **Does it require a new `Kind`?** → If yes, it's a 200-LOC addition.
2. **Does it require a new constraint key?** → If yes, it's a 20-LOC handler
   in the constraint filter.
3. **Does it require a new CRD?** → If yes, **stop**. We've regressed.
4. **Does it require a new top-level Console tab?** → If yes, **stop**. It's
   a saved filter on the capability graph.
5. **Does it require a parallel auth/audit schema?** → If yes, **stop**. The
   feature should slot into the existing receipt and `caps` types.

If a feature can't pass tests 3-5, it's worth re-shaping until it can. The
moat is the abstraction; protecting it is more valuable than shipping fast.

---

## See also

- [18 — Capability Mesh](18-capability-mesh.md) — the primitive
- [19 — CapabilityRoute](19-capability-route-crd.md) — one CRD, all kinds
- [20 — MemPlex](20-memplex-memory-plane.md) — the first plane on the new abstraction (now shipped)
- [21 — Runtime Consent & Trust Ledger](21-runtime-consent-and-trust-ledger.md) — JIT consent + receipts
- [22 — Roadmap](22-roadmap-100x.md) — phased delivery

# 20 — MemPlex: Memory as a Native Capability Kind

> **Status:** Proposed. The first new plane shipped natively on the [Capability Mesh](18-capability-mesh.md). Acts as the proof that the abstraction holds — memory adds **zero new architectural surfaces**.
> **Closes the gap with:** AWS Bedrock AgentCore, Letta, LangGraph Cloud — all of which treat memory as a first-class governed surface.

---

## Why a Memory Plane

Today AIPlex governs three interaction shapes — calling tools, delegating to agents, calling models. **Continuity** is missing. Real agentic systems need:

- **Working memory** within a session (current task, scratchpad).
- **Long-term memory** across sessions (user preferences, prior decisions, embeddings).
- **Shared memory** across agents (a research agent's notes consumed by a writer agent).
- **Tenant-isolated memory** (one tutor agent serving 10,000 students must never leak Alice's notes to Bob).

Without governance, agents end up with bring-your-own state — a Postgres here, a vector DB there, no audit, no scoping, no consent. **Memory is the most sensitive surface** (PII, regulated data) and the least governed today.

> **Decision:** Memory is a Capability `kind`. No new CRD. No new policy. No new Console tab.

---

## Capability Surface

Memory namespaces are addressed by capability URIs:

```
cap://memory/<namespace>[@version]              # global namespace
cap://memory/<tenant>/<namespace>[@version]     # tenant-scoped (sub-path)
cap://memory/<tenant>/<user>/<namespace>[@v]    # per-user
```

### Standard actions

| Action     | Semantics                                                       |
|------------|-----------------------------------------------------------------|
| `read`     | Get a value by key                                              |
| `write`    | Put a value at a key (with TTL, optional schema)                |
| `delete`   | Forget a key (soft-delete with retention)                       |
| `search`   | Vector or full-text search over the namespace                   |
| `list`     | Enumerate keys (paginated, prefix-filtered)                     |
| `subscribe`| Stream changes (SSE) — for cross-agent shared memory           |

A cap claim grants a subset:

```json
{
  "uri": "cap://memory/students/alice/profile@v1",
  "actions": ["read", "write"],
  "constraints": {
    "key_prefix": "lesson-*",
    "max_value_bytes": 65536,
    "ttl_seconds_max": 31536000,
    "tenant": "school-acme"
  }
}
```

---

## The Memory Broker

Memory is served by a single workload — the **memory broker** — running in `namespace: memplex` with its own SPIFFE ID. The broker:

1. Receives requests via the gateway (CapabilityRoute → broker).
2. Routes to a backend based on namespace metadata (Firestore, Vertex Vector Search, Postgres+pgvector, AlloyDB, Letta).
3. Enforces constraints already pre-checked by the gateway, plus content-aware checks (PII redaction, schema validation).
4. Emits a receipt (doc 21) with the request hash, key, and backend response signature.

```
agent
  │
  ▼  (gateway → ext_authz → constraint filter → broker)
memory-broker (namespace: memplex)
  ├── routes to backend by namespace.backend
  │     ├── firestore                 (transactional KV, low-latency)
  │     ├── postgres-pgvector         (vector search, cheap)
  │     ├── vertex-vector-search      (managed vector, scalable)
  │     ├── alloydb-omni              (transactional + vector)
  │     └── letta                     (managed agent memory)
  ├── PII redaction filter (configurable per namespace)
  ├── schema validator (per-namespace JSON Schema)
  └── receipt emitter
```

The broker is ~1500 LOC of Go. Backends are pluggable via a `MemoryBackend` interface:

```go
type MemoryBackend interface {
    Read(ctx context.Context, ns Namespace, key string) (Value, error)
    Write(ctx context.Context, ns Namespace, key string, val Value, opts WriteOpts) error
    Delete(ctx context.Context, ns Namespace, key string) error
    Search(ctx context.Context, ns Namespace, q Query) ([]Hit, error)
    List(ctx context.Context, ns Namespace, prefix string, page Page) (Listing, error)
    Subscribe(ctx context.Context, ns Namespace, prefix string) (<-chan Change, error)
}
```

---

## Namespace Definition

A memory namespace is a CapabilityRoute (doc 19) with `kind: memory`:

```yaml
apiVersion: aiplex.dev/v1alpha1
kind: CapabilityRoute
metadata:
  name: cap-memory-students-profile-v1
  namespace: aiplex-system
spec:
  capability:
    uri: cap://memory/students/{user}/profile@v1
    kind: memory
    provider:
      kind: KubernetesService
      name: memory-broker
      namespace: memplex
      port: 8080
    schema:
      configMapRef:
        name: memory-students-profile-schema    # JSON Schema for value
    attrs:
      side_effect: write
      data_class: pii
      retention_days: 365
      backend: firestore
    auth:
      required_actions: [read, write, search, list]

  routing:
    pathTemplate: "/cap/memory/students/{user}/profile@v1"

  kindOverrides:
    memory:
      backend:
        kind: Firestore
        project: school-prod
        collection: student_profiles
      piiRedaction:
        enabled: true
        rules:
          - field: ssn
            action: reject
          - field: email
            action: hash
      contentSchema:
        configMapRef:
          name: memory-students-profile-schema
      replication:
        kind: none                                # or "regional" | "multi-region"
      tenancy:
        isolationKey: "{user}"                    # template var resolved from URI
```

Note `{user}` in the URI and `pathTemplate`: the resolver extracts it from the request path and the constraint filter enforces it matches the JWT's `sub`. **Tenant isolation is structural**, not bolted on.

---

## Tenancy Model

| Pattern              | URI shape                                  | Isolation                              |
|----------------------|--------------------------------------------|----------------------------------------|
| Global               | `cap://memory/curriculum@v1`               | None — shared by all callers           |
| Per-tenant           | `cap://memory/{tenant}/notes@v1`           | `tenant` extracted from JWT claim      |
| Per-user             | `cap://memory/{tenant}/{user}/profile@v1`  | `tenant` + `user` from JWT             |
| Per-agent (sandbox)  | `cap://memory/agents/{azp}/scratch@v1`     | `azp` from JWT (the agent client_id)   |

Template variables in the URI map to JWT claims at the constraint filter:

```yaml
constraints:
  tenant_from: claims.organization      # or claims.sub, claims.azp
  user_from: claims.sub
```

A request whose JWT claims don't match the path's template variables gets a 403 — not because of policy, but because the **constraint filter rejects the substitution**.

---

## Backends

### 1. Firestore (default for KV)

- Strongly-consistent reads/writes.
- Use cases: user preferences, working memory, last-N items.
- Retention: TTL fields with Cloud Scheduler cleanup, or Firestore TTL policies.

### 2. AlloyDB Omni (transactional + vector)

- Postgres with `pgvector`.
- Use cases: shared knowledge bases, RAG over agent notes.
- One-shot SQL for "give me top-10 nearest neighbours where tenant='acme'".

### 3. Vertex Vector Search

- Managed vector index, scalable.
- Use cases: large-scale embedding search.
- Async write semantics — broker queues, returns optimistic ack.

### 4. Letta (managed agent memory)

- Optional integration. Call `cap://memory/agent/{azp}/letta@v1` to get full Letta memory primitives (archival memory, recall memory).
- Letta becomes a backend, not a parallel system.

### 5. Local KV (test/dev)

- In-memory, single-node. For `aiplex up --local`.

The backend choice is a namespace-time decision; the cap URI is backend-agnostic. Migrating a namespace from Firestore to AlloyDB is a broker config change + a copy job — agents and tokens are unaffected.

---

## Schema & Validation

Each namespace can attach a JSON Schema for values:

```json
{
  "type": "object",
  "required": ["lesson_id", "score"],
  "properties": {
    "lesson_id": { "type": "string", "pattern": "^lesson-[0-9]+$" },
    "score":     { "type": "number", "minimum": 0, "maximum": 100 },
    "notes":     { "type": "string", "maxLength": 4096 }
  },
  "additionalProperties": false
}
```

The broker rejects writes that don't match. **Type-safe memory** is the difference between governed continuity and a JSON dumping ground.

---

## PII & Data Class

Namespaces carry `attrs.data_class`: `public | internal | pii | regulated`. The broker enforces:

| data_class  | Behavior                                                                |
|-------------|-------------------------------------------------------------------------|
| `public`    | No PII detection. Caching enabled.                                      |
| `internal`  | Drop logs of values. Cache OK.                                          |
| `pii`       | Run PII detector on writes. Encrypt-at-rest with CMEK. No body in logs. |
| `regulated` | All of `pii` + step-up consent (doc 21) + audit signed receipts only.   |

The PII detector is pluggable: regex rules, Vertex DLP, or custom. Match action is `reject`, `hash`, or `redact`.

---

## SDK Surface

```python
# Python SDK (illustrative — Go and TS analogous)
from aiplex import Client

client = Client()  # picks up token from environment

mem = client.memory("cap://memory/students/{user}/profile@v1", user="alice")
mem.write("lesson-42", {"lesson_id": "lesson-42", "score": 88, "notes": "strong on parabolas"})
hits = mem.search(embedding=embed("how is alice doing on physics?"), top_k=5)
```

```typescript
// TypeScript SDK
const mem = client.memory("cap://memory/students/{user}/profile@v1", { user: "alice" });
await mem.write("lesson-42", { lesson_id: "lesson-42", score: 88 });
const hits = await mem.search({ embedding, topK: 5 });
```

```go
// Go SDK
mem := client.Memory("cap://memory/students/{user}/profile@v1", aiplex.With("user", "alice"))
mem.Write(ctx, "lesson-42", map[string]any{"lesson_id": "lesson-42", "score": 88})
hits, _ := mem.Search(ctx, embedding, aiplex.TopK(5))
```

The same SDK shape works for any future Capability kind — `client.tool(...)`, `client.task(...)`, `client.model(...)`, `client.skill(...)`, `client.memory(...)`. Five kinds, one method pattern.

---

## What Adding MemPlex Costs

| Surface                         | LOC added | Notes                                                       |
|---------------------------------|-----------|-------------------------------------------------------------|
| Capability kind registration    | ~30       | `internal/capability/kinds.go` adds `KindMemory` entry      |
| Memory broker (Go)              | ~1500     | New service in `internal/memplex/broker/`                   |
| Backends (Firestore + AlloyDB)  | ~800      | Pluggable `MemoryBackend` impls                             |
| Capability resolver memory rule | ~30       | `authz/cap-resolver/src/kinds/memory.rs`                    |
| Constraint filter handlers      | ~80       | `key_prefix`, `tenant`, `read_only`                         |
| Helm chart for broker           | ~50       | `deploy/helm/aiplex/templates/memory-broker.yaml`           |
| SDK helper (Python/TS/Go)       | ~150 each | `memory(uri).read/write/search`                             |
| Console: nothing new            | 0         | Memory shows in the unified Capability Graph automatically  |
| OPA policy: nothing new         | 0         | Single rule covers all kinds                                |
| New CRD: none                   | 0         | Uses CapabilityRoute (doc 19)                               |
| New scope namespace: none       | 0         | Uses cap claims (doc 18)                                    |

**Total: ~3000 LOC for a fully-governed memory plane**, vs. an estimated 7000+ LOC if we copied the SkillsPlex pattern.

---

## Threat Model

| Threat                                       | Mitigation                                                    |
|----------------------------------------------|---------------------------------------------------------------|
| Cross-tenant read                            | Path template `{tenant}` validated against JWT claim          |
| Replay of stale writes                       | Broker enforces monotonic write versions; receipts (doc 21)   |
| PII leak via search results                  | `data_class: pii` enables redaction on response               |
| Backend compromise leaks all data            | CMEK per namespace; broker only decrypts on authorized read   |
| Agent gains write to other agent's scratch   | `cap://memory/agents/{azp}/...` — `azp` template enforced     |
| Long-lived stale grant                       | `nbf`/`exp` per cap claim; default TTL 24h for write actions  |

---

## Open Questions

> **Open:** Should we expose memory as MCP tools (`memory.read`, `memory.write`) so existing MCP clients see it? Decision: yes — provide an MCP shim server in front of the broker for compatibility, but the canonical surface is the capability URI.

> **Open:** Cross-region replication semantics. Decision: namespace-level config; default `none`, opt-in `regional` (replicated within a GCP region) or `multi-region`. No automatic global replication — too much risk for `pii` data.

> **Open:** Vector backend choice when both AlloyDB and Vertex Vector Search are available. Decision: AlloyDB for ≤10M vectors, Vertex for larger. Auto-recommend in `aiplex deploy` based on expected cardinality.

---

## See Also

- [18 — Capability Mesh](18-capability-mesh.md)
- [19 — CapabilityRoute](19-capability-route-crd.md)
- [21 — Runtime Consent & Trust Ledger](21-runtime-consent-and-trust-ledger.md) — memory operations produce signed receipts
- [12 — Security Model](12-security-model.md) — how MemPlex slots into the existing threat model

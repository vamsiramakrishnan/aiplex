# 22 — The 100x Roadmap

> **Status:** Proposed delivery plan for the [Capability Mesh](18-capability-mesh.md) reorganisation.
> **Spirit:** Strict superset, never breaking. Every existing plane keeps working through every phase. No flag day.

---

## North Star

> **One primitive. Every plane is a tag. Every feature ships once.**

Twelve months from today, AIPlex has one Capability type, one CRD, one OPA rule, one Console graph, one catalog interface, one SDK shape — and supports six kinds (tool, task, model, skill, memory, meta) instead of four planes. Adding the seventh kind takes a week, not a quarter.

---

## Phase Order — Why This Sequence

We sequence by **leverage**, not feature attractiveness:

1. The **type system** must land first — every other phase consumes it.
2. The **CRD unification** lands second — it depends on the type system and unblocks every new kind.
3. **MemPlex** lands third — it's the smallest new kind and acts as the proof.
4. **JIT consent** lands fourth — it's the most user-visible feature and validates the elevation primitive across all kinds.
5. **Trust ledger** lands fifth — it's the audit story; needed before regulated customers can adopt.
6. **Console graph** lands sixth — UX win; rides on top of the now-uniform data model.
7. **Self-host** is the bow on top — AIPlex governs itself.

---

## Phase 0 — Foundation: The Capability Type System (2 weeks)

**Goal:** ship the type system with zero behavior change. Both old and new claim formats coexist. Old policy, old CRDs, old code paths all continue to run.

### Deliverables

- `internal/capability/` package
  - `uri.go` — parse/render `cap://kind/name@version`
  - `kinds.go` — registry of kinds, allowed actions per kind, constraint schemas
  - `claim.go` — `Cap` struct + JSON marshalling
  - `equivalence.go` — translate between old scope strings and new cap claims
- Hydra token hook augmented to emit **both** `scope` (legacy) and `caps` (new) for every token. Cap claims are derived from registered scopes via `equivalence.go`.
- `internal/auth/scopes.go` extended: every existing scope registered today auto-maps to a cap URI:
  - `mcp:tools:foo` → `cap://tool/foo@v1` actions=[call]
  - `a2a:task:bar` → `cap://task/bar@v1` actions=[invoke]
  - `llm:model:baz` → `cap://model/baz@v1` actions=[complete]
  - `skill:qux` → `cap://skill/qux@v1` actions=[exec]
- Unit tests for URI parsing, claim serialization, equivalence round-trip.
- No Console changes. No CRD changes. No OPA changes.

### Exit criteria

- Every issued token contains `caps` matching its `scope` (verified by a `tests/equivalence_test.go` that parses 1000 tokens from staging and checks both forms).
- A new SDK helper (`sdk/python/aiplex/_caps.py`) can read either form.

---

## Phase 1 — Capability Resolver in Shadow Mode (2 weeks)

**Goal:** ship the [capability resolver](19-capability-route-crd.md#the-capability-resolver) Envoy filter, but **don't enforce** with it yet. Compare its decisions against legacy OPA and alert on divergence.

### Deliverables

- `authz/cap-resolver/` Rust crate (Envoy ext_proc filter).
- Filter chain wiring: resolver runs **before** ext_authz, writes `(uri, action)` to filter metadata.
- OPA reads metadata if present, but **falls back to legacy scope-string check**. Both run; if they disagree, log + metric `aiplex_authz_divergence_total{path,kind}` and **trust legacy**.
- Dashboard panel: divergence rate over time, broken down by kind.
- Two-week observation window.

### Exit criteria

- Divergence rate < 0.01% in a 7-day window.
- All divergences explained (dashboard has zero unknown root causes).

---

## Phase 2 — CapabilityRoute CRD (3 weeks)

**Goal:** ship `CapabilityRoute`, run it alongside existing CRDs. New deploys use it; existing CRDs continue to reconcile.

### Deliverables

- CRD definition (`deploy/k8s/crds/capabilityroute.yaml`).
- Reconciler in `internal/deploy/routes/reconciler.go`.
- Admission webhook (`internal/api/admission/capabilityroute.go`).
- Deploy engine: branch on a feature flag — when `AIPLEX_USE_CAPROUTE=true`, write `CapabilityRoute` for new instances; otherwise keep current behavior.
- `aiplex migrate routes --dry-run | --apply | --finalize` CLI command.
- Documentation: [doc 19](19-capability-route-crd.md), updated [doc 04](04-envoy-gateway-routing.md), updated [doc 06](06-deploy-engine.md).

### Exit criteria

- A reference deploy creates a `CapabilityRoute` end-to-end (cap registered → route applied → request routes correctly → ext_authz allows → response returned).
- `aiplex migrate routes --dry-run` produces a clean plan for the staging cluster.
- All existing tests pass; new test file `tests/capability_route_test.go` covers both kinds (tool + task) in table-driven form.

---

## Phase 3 — Flip OPA to Capability-First (1 week)

**Goal:** OPA primarily evaluates `caps`; legacy scope check becomes the fallback.

### Deliverables

- New OPA policy file `policies/aiplex_authz_v2.rego` (the unified rule from [doc 18](18-capability-mesh.md#the-opa-policy-single-rule)).
- ConfigMap rollout: 50% canary → 100% over 7 days.
- Auto-rollback on `aiplex_authz_v2_errors_total > 0` for 5 minutes.
- Old `policies/aiplex_authz.rego` retained as `aiplex_authz_legacy.rego` for one release.

### Exit criteria

- Canary at 100% for 72 hours with no errors.
- Divergence metric (still running) at 0%.

---

## Phase 4 — MemPlex (3 weeks)

**Goal:** ship the memory broker as the first new kind native to the Capability Mesh. Validates that adding a kind is cheap.

### Deliverables

- `internal/memplex/broker/` Go service.
- Backend implementations: Firestore, AlloyDB+pgvector. Vertex Vector Search and Letta deferred to Phase 4.5.
- `authz/cap-resolver/src/kinds/memory.rs` — request → action mapping for memory operations.
- Constraint filter handlers: `key_prefix`, `tenant`, `read_only`, `max_value_bytes`.
- Helm chart: `deploy/helm/aiplex/templates/memory-broker.yaml`.
- SDK helpers: Python, TS, Go (`client.memory(uri)` with `read/write/search/list/delete/subscribe`).
- Sample namespace: `cap://memory/students/{user}/profile@v1` deployed in the staging cluster.
- Documentation: [doc 20](20-memplex-memory-plane.md).

### Exit criteria

- Deploy a memory namespace via `aiplex deploy --kind memory ...` in under 60s.
- Read/write/search round-trip works in all three SDKs.
- PII redaction policy verified by integration test that writes a fake SSN and confirms it's rejected/hashed.
- Tenant isolation verified by integration test that tries cross-tenant access and gets 403.
- **Total LOC added under 3500.** (Hard budget — if we exceed it, the abstraction isn't paying off and we revisit doc 18.)

---

## Phase 5 — JIT Consent (2 weeks)

**Goal:** ship inline elevation across all kinds.

### Deliverables

- Gateway returns 401 with `WWW-Authenticate: AIPlex …` challenge on missing cap (resolver path).
- AIPlex API:
  - `POST /auth/elevate`
  - `GET /auth/elevate/{id}/status`
  - `WS /auth/elevate/{id}/stream`
- Console: elevation modal + WebSocket subscription.
- WebPush registration in Console; email fallback.
- Pre-authorized auto-approval rules (`internal/auth/elevation_rules.go`).
- SDK auto-elevation helper: `client.with_elevation(...)` retries the call with the new token.
- Documentation: [doc 21 part 1](21-runtime-consent-and-trust-ledger.md#part-1--just-in-time-consent).

### Exit criteria

- A live demo flow: agent calls a capability it lacks → user gets push notification → approves → agent retries successfully — under 10 seconds end-to-end with the user on a phone.
- Auto-approval rules enforced and audited separately in receipts.

---

## Phase 6 — Verifiable Trust Ledger (3 weeks)

**Goal:** ship signed receipts for every capability invocation.

### Deliverables

- Receipt struct + canonicalisation (`internal/audit/receipt.go`).
- Gateway signing: Envoy filter that, on response, emits a receipt to a sidecar.
- Provider signing: helper library shipped to MCP/A2A/skill servers (`sdk/{lang}/audit`).
- Receipt sink: BigQuery streaming insert + GCS Coldline batch + optional Rekor anchoring.
- Chain verification: receipt N includes hash of receipt N-1 in its stream.
- `aiplex audit verify` CLI (separate binary, off-cluster runnable).
- `aiplex audit export --since 90d` for compliance.
- Documentation: [doc 21 part 2](21-runtime-consent-and-trust-ledger.md#part-2--verifiable-trust-ledger).

### Exit criteria

- 99.9% of invocations produce a receipt with both gateway + provider signatures.
- `aiplex audit verify` against 1000 random receipts passes.
- A deliberately-tampered receipt is detected by chain verification within 1 link.
- Rekor anchoring (when enabled) achieves inclusion in < 90s p99.

---

## Phase 7 — Console Capability Graph (2 weeks)

**Goal:** replace the four plane tabs with one unified graph view. Plane tabs become saved filters.

### Deliverables

- `console/src/pages/Graph.tsx` — force-directed graph (D3 or Reaflow).
- Nodes: users, agents, capabilities (coloured by kind), providers.
- Edges: `has_cap`, `called`, `delegates_to` (act-claim chains).
- Saved filters: "MCPlex" = `kind=tool`, etc.
- Pivots: agent-centric, capability-centric, time-window-centric.
- Live mode: WebSocket stream of receipts animates the graph.
- Old plane tabs remain reachable via deep links for two releases, then deprecated.

### Exit criteria

- Operators report (in user-test interviews) that the graph view is faster than the tabbed view for "what can this agent do?" and "who's calling this?" questions.
- Performance: graph renders ≤500ms for 10k nodes / 50k edges.

---

## Phase 8 — Self-Host (1 week)

**Goal:** AIPlex governs itself.

### Deliverables

- Register AIPlex API endpoints as `cap://meta/*` capabilities:
  - `cap://meta/deploy@v1` (action: create, read, delete)
  - `cap://meta/instance@v1` (action: read, scale, restart)
  - `cap://meta/agent@v1` (action: register, revoke)
  - `cap://meta/grant@v1` (action: grant, revoke, list)
- Bootstrap Hydra clients for the AIPlex API and console with appropriate `cap://meta/*` claims.
- An MCP server in `mcplex` namespace (`aiplex-meta-mcp`) that exposes these as MCP tools so agents can deploy/govern other agents via standard MCP.
- Reference flow: an admin agent deploys a new MCP server using only `cap://meta/deploy@v1`.

### Exit criteria

- An external coding agent (Claude Code, Cursor) can deploy a new MCP server via the AIPlex MCP-meta endpoint, with full audit and policy enforcement.

---

## Cross-Cutting Workstreams

These run in parallel with the phases above.

### Documentation (continuous)

- Update [00-overview.md](00-overview.md) as docs land.
- Per phase, ship a "What Changed" entry in `CHANGELOG.md`.
- Migration guide for self-hosted operators (one consolidated doc).

### SDK Parity (Phase 1+)

- Python, TypeScript, Go SDKs maintained at parity.
- One method pattern: `client.<kind>(uri).<action>(args)`.
- New kinds added to all three SDKs in the same release.

### Performance Budget (Phase 1+)

| Layer                         | p50 budget | p99 budget |
|-------------------------------|------------|------------|
| Capability resolver           | 0.2 ms     | 1 ms       |
| OPA (single rule)             | 0.3 ms     | 2 ms       |
| Constraint filter             | 0.2 ms     | 1.5 ms     |
| Receipt signing (when on)     | 0.5 ms     | 3 ms       |
| **Total added per request**   | **1.2 ms** | **7.5 ms** |

If any phase blows the budget, the phase doesn't ship until it's fixed.

### Backwards Compatibility (Phase 0–7)

- Both `scope` and `caps` claim forms emitted until end of Phase 7.
- Old CRDs reconcile until end of Phase 7.
- Old Console plane tabs reachable until end of Phase 7.
- Phase 8 starts the deprecation window. Phase 9 (next release after 8) removes the legacy code paths.

### Telemetry (continuous)

Every phase adds explicit metrics to the dashboard so we can see if it's working:

| Phase | Metric                                             |
|-------|----------------------------------------------------|
| 0     | `aiplex_token_caps_emitted_total`                  |
| 1     | `aiplex_authz_divergence_total{path,kind}`         |
| 2     | `aiplex_capability_route_reconciliations_total`    |
| 3     | `aiplex_authz_v2_decision_total{decision,kind}`    |
| 4     | `aiplex_memplex_ops_total{action,backend}`         |
| 5     | `aiplex_elevation_requests_total{outcome,channel}` |
| 6     | `aiplex_receipts_emitted_total{kind,signed}`       |
| 7     | `aiplex_console_graph_render_ms`                   |
| 8     | `aiplex_meta_capability_calls_total{action}`       |

---

## Risk Register

| Risk                                                         | Likelihood | Mitigation                                                    |
|--------------------------------------------------------------|------------|---------------------------------------------------------------|
| Capability resolver introduces unacceptable latency          | medium     | Shadow mode + budgets + load tests in Phase 1 before flip     |
| OPA v2 policy has subtle differences from legacy             | medium     | Two-week divergence observation + auto-rollback in Phase 3    |
| Two-CRD operation period creates stale routes                | low        | Single source of truth in Firestore; reconciler dedups        |
| Memory broker becomes a bottleneck                           | medium     | Backends are pluggable; broker is horizontally scalable       |
| Elevation flow surprises users (notification spam)           | medium     | Rate limiting + auto-approval rules + UX testing in Phase 5   |
| Receipt signing dependency on Rekor availability             | low        | Local sink primary; Rekor anchoring is async + best-effort    |
| Phase 4 LOC budget (3500) exceeded                           | low        | Hard stop — revisit doc 18 if exceeded                        |

---

## Open Questions

> **Open:** Should Phase 7 (Console graph) move earlier? It would help validate the abstraction visually. Decision: **no** — graph view is more credible once MemPlex is in the data, otherwise it just shows the same four planes. Keep Phase 7 after Phase 4.

> **Open:** Cut Phase 8 entirely and ship as a follow-up release? Decision: **keep it on the roadmap**, but flag it as the candidate to drop if any prior phase slips by ≥2 weeks.

> **Open:** Do we publish all five new design docs to the public docs-site at the same time, or stage them by phase? Decision: **publish all five together as a "100x" announcement** when Phase 0 ships, so external readers see the full picture.

---

## Total Time

| Phase | Duration | Calendar (assuming start now) |
|-------|----------|-------------------------------|
| 0     | 2 weeks  | wk 1–2                        |
| 1     | 2 weeks  | wk 3–4                        |
| 2     | 3 weeks  | wk 5–7                        |
| 3     | 1 week   | wk 8                          |
| 4     | 3 weeks  | wk 9–11                       |
| 5     | 2 weeks  | wk 12–13                      |
| 6     | 3 weeks  | wk 14–16                      |
| 7     | 2 weeks  | wk 17–18                      |
| 8     | 1 week   | wk 19                         |

**~19 weeks**, with cross-cutting documentation and SDK work folded in. Phases 0–6 are the critical path; Phase 7–8 can move if needed.

---

## See Also

- [18 — Capability Mesh](18-capability-mesh.md)
- [19 — CapabilityRoute](19-capability-route-crd.md)
- [20 — MemPlex](20-memplex-memory-plane.md)
- [21 — Runtime Consent & Trust Ledger](21-runtime-consent-and-trust-ledger.md)
- [CLAUDE.md](../CLAUDE.md) — top-level architecture this roadmap evolves

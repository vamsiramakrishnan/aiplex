# AIPlex ↔ Tape Integration — Phase 0 Survey (AIPlex side)

Status: survey only. No behavioural changes are made by this document.
Companion survey: `durable-agents/tape/docs/integrations/aiplex.md`.

This note inventories the AIPlex surfaces that an upcoming integration
with [Tape](https://github.com/vamsiramakrishnan/durable-agents) will
touch. AIPlex will treat Tape as a managed durable-runtime backend:
AIPlex governs identity, scopes, consent, routing, catalog, deployment
and policy; Tape governs run journals, model decisions, effects,
replay, leases, gates, timers, budgets, reconciliation and
compensation.

The guiding invariant:

> AIPlex decides whether an agent is allowed to act.
> Tape proves what happened when it acted.

AIPlex never writes Tape's journal directly. All durable-runtime actions
flow through Tape's gRPC / admin surface. Tape's outbox events flow back
into AIPlex audit storage idempotently.

Both projects are pre-users. This integration takes the full target
shape from day one — no `*RuntimeConfig` pointers to hedge optionality,
no compatibility wrappers on the `Instance` struct, no "Phase 1 / Phase
2 / Phase 3" toggles that defer the real decision. Required fields are
required.

---

## 1. Agent deployment / config model

- File: `internal/models/instance.go`
- Type: `Instance` (lines 28–45) with `ID`, `Plane`, `TemplateID`,
  `Owner`, `Namespace`, `SpiffeID`, `Config map[string]any`,
  `Scopes []string`, `Labels map[string]string`, `Status InstanceStatus`,
  `ResourceVersion`, `DeployedAt`, `DeployedBy`.
- `InstanceStatus` enum (lines 16–24): `Provisioning`, `Running`,
  `Degraded`, `Stopped`, `Failed`, `Terminated`.
- YAML form: see `examples/quickstart.yaml`. AIPlex deployments are
  Firestore-backed, expressed as YAML manifests rather than Kubernetes
  CRDs.

PR 4 surface: add a `Runtime RuntimeConfig` (value, not pointer) on
`Instance`. The runtime config has validation rules that need their own
type and an "always-set" position — every durable plane (A2APlex,
MCPlex with non-idempotent tools) carries a `Runtime` describing how
its agents execute. For planes that have no durable component the
field still exists but is set to `RuntimeConfig{Engine: "none"}` —
explicit absence over nil. Mirror in the YAML schema. New
`RuntimeConfig` struct lives next to `Instance`. `Config map[string]any`
keeps its template-specific role; runtime config never leaks into it.

## 2. Deployment engine / manifest generation

- `internal/deploy/engine.go` (lines 17–130+): `Engine.Deploy(ctx,
  plane, templateID, config, owner, displayName)`.
- `internal/deploy/manifests.go`: `GenerateManifests()` (lines 21–40)
  returns `[]Manifest{APIVersion, Kind, Name, Namespace, YAML}`.
- `internal/deploy/routes.go`: `GenerateRoute()` (lines 13–26),
  switches on plane to emit MCPRoute / HTTPRoute / LLMRoute /
  AIServiceBackend.
- No template engine — raw `fmt.Sprintf` strings.
- K8s apply via `K8sClient` interface, instantiated in
  `cmd/aiplex-api/main.go` (~line 89) as `NewLiveK8sClientConfigured`
  or `NewNoOpK8sClient`.

PR 5 surface: when `inst.Runtime.Engine == "tape"`, extend
`GenerateManifests` to emit `tape-server` Deployment + Service,
`tape-reactors` Deployment, NetworkPolicy and ServiceAccount, all
templated from values that point at the existing Helm chart in
`durable-agents/tape/deploy/gcp/k8s/chart/tape/`. Inject env vars into
the agent pod: `TAPE_URL`, `AIPLEX_TENANT_ID`, `AIPLEX_AGENT_ID`,
`AIPLEX_ACTOR`, `AIPLEX_SUBJECT`, `AIPLEX_ROUTE`, `AIPLEX_INSTANCE_ID`,
`AIPLEX_SCOPES` — all of them, every time. The Tape SDK refuses to
start without `AIPLEX_TENANT_ID`, `AIPLEX_AGENT_ID` and `AIPLEX_ACTOR`,
so the deployment engine must populate them, not treat them as
opt-in. One `tape-server` per environment by default; per-tenant or
per-agent topologies are explicit overrides on `RuntimeConfig`.

## 3. API service

- Entry: `cmd/aiplex-api/main.go`. Router: `github.com/go-chi/chi/v5`
  (lines 12, 132).
- Middleware stack (lines 144–148): `Recover`, `RequestID`, `Logger`,
  `CORS`, `MaxBody`, `WIFAuth`, `AuditLog`, `RateLimit`, `Compress`.
- Routes registered under `r.Route("/api/v1", ...)` (lines 155–223).
- Handler pattern: per-domain struct with `Store` field, one per plane
  plus cross-cutting (auth, dashboard, IAM). See
  `internal/api/{instances,agents,llm,a2a,skills,dashboard,iam}.go`.

PR 6 surface: add `internal/api/runs.go::RunsHandler` exposing
`GET /api/v1/runs`, `GET /api/v1/runs/{id}`,
`GET /api/v1/runs/{id}/events`, `.../effects`, `.../obligations`,
`.../budgets`. Add `POST /internal/tape/events` outside `/api/v1` for
Tape outbox ingestion, sibling of the `GET /events/stream` SSE
precedent (~line 236).

## 4. Authz service

- Directory: `authz/` (Rust). Entry: `authz/src/main.rs`.
- Scope format: space-separated strings in JWT `scope` claim
  (`CLAUDE.md` line 104).
- Scope patterns: `mcp:tools:{name}`, `mcp:server:{id}`,
  `a2a:task:{type}`, `a2a:agent:{id}`, `llm:model:{id}`,
  `llm:capability:{cap}`, `skill:invoke:{name}`,
  `skill:bundle:{name}`.
- Scopes are generated per deployment, not declared as central
  constants. See `internal/deploy/engine.go` lines 72–90.

PR 9 surface: add a new family `aiplex:runs:{read,redrive,reconcile,
cancel,signal,compensate}` and check it in the Rust authz path
(~lines 88–100 where `tools/call` is checked today). Operator actions
must require these scopes and be appended to AIPlex audit.

## 5. Storage layer

- `internal/registry/store.go::Store` interface (lines 14–80).
- `internal/registry/firestore.go`: Firestore implementation.
- Append-only methods already present: `AppendHistory`, `AppendUsage`,
  `AppendDelegation`, `AppendSkillInvocation`, `AppendPolicyDenial`.
- Existing collections (per `CLAUDE.md` 589–604): `instances`,
  `templates`, `deploy_history`, `agents`, `user_scopes`,
  `route_configs`, `provider_configs`, `usage_records`, `delegations`,
  `skill_invocations`, `policy_denials`, `role_bindings`.

PR 6 surface: add collections `execution_runs`, `execution_events`,
`execution_effects`, `execution_obligations`, `execution_budgets`.
Extend the `Store` interface with `AppendExecutionEvent`,
`UpsertExecutionRun`, list methods for the projections. Reuse the
append-only pattern already established for `deploy_history` and
`usage_records`.

## 6. Console model types

- `console/src/pages/{MCPlex,A2APlex,LLMPlex,Agents,Dashboard,
  InstanceDetail,Deploy}.tsx`.
- API client: `console/src/api/client.ts` (axios wrapper).
- Shared components: `console/src/components/`.
- Test setup: `console/src/test-setup.ts`, vitest config at
  `console/vitest.config.ts`. Existing test:
  `console/src/__tests__/client.test.ts`.

PR 8 surface: add `console/src/pages/Runs.tsx` (or a `Runs` tab on the
agent detail), plus `console/src/components/RunDetail.tsx` for the
timeline. Fixtures live in a new `console/src/__fixtures__/runs.ts`.
Empty state copy: "Enable Tape runtime to see durable execution
timelines."

## 7. Route generation for the planes

- `internal/deploy/routes.go::GenerateRoute` switches on `inst.Plane`
  to emit MCPRoute (MCPlex), HTTPRoute (A2APlex), LLMRoute +
  AIServiceBackend (LLMPlex).
- Gateway name configurable; default `aiplex-gateway`
  (`cmd/aiplex-api/main.go` ~line 95).

PR 5 surface: route generation does not change for Tape itself — Tape
runs in-cluster behind a Service, not the public Envoy gateway. But the
agent's existing route must inject env vars indicating its Tape URL and
AIPlex identity context.

## 8. Existing audit ingestion

- Audit middleware: `internal/api/audit.go` (~35 lines).
- Append model: `internal/models/api.go::DeployHistory` (lines 14–27)
  with `Action`, `PerformedBy`, `Owner`, `Timestamp`, `DurationMs`,
  `Success`, `Error`.
- Firestore append: `FirestoreStore.AppendHistory` writes to
  `deploy_history` collection.
- Idempotency today: relies on `RequestID` middleware (line 134),
  no per-event key.

PR 6 surface: `/internal/tape/events` adopts the same append pattern
with `(run_id, seq)` as the idempotency key — duplicates become
no-ops, out-of-order events still land and the projection catches up.
Unknown agents go to a `quarantine_execution_events` collection rather
than failing the whole batch. The ingestion endpoint authenticates the
caller via mesh identity (SPIFFE SVID) — there is no shared-secret
fallback to support older callers, because there are no older callers.

## 9. Docs site

- `docs-site/` is Docusaurus 3+. Config: `docusaurus.config.ts`,
  `sidebars.ts`. Content under `docs-site/docs/{getting-started,
  architecture, api, concepts, reference, guides}`.

PR 4 surface: add `docs-site/docs/runtime/tape.md` (new "Runtime"
section in `sidebars.ts`) describing `runtime.engine=tape` and how
AIPlex manages a Tape deployment.

## 10. Examples directory

- `examples/quickstart.yaml`, `mcplex-only.yaml`, `multi-agent.yaml`,
  `llm-routing.yaml`. Format:

  ```yaml
  version: v1
  instances: [ ... ]
  agents: [ ... ]
  routes: [ ... ]
  ```

PR 4 surface: add `examples/runtime/tape-agent.yaml`. PR 9 adds the
full demo under `examples/aiplex-tape-treasury/`.

## 11. Tests

- Go: stdlib `testing` package; ~16 `*_test.go` files. Pattern:
  MemoryStore + `httptest.Server` (e.g. `internal/api/api_test.go`
  lines 19–50).
- Console: vitest, currently one file
  (`console/src/__tests__/client.test.ts`).

Each PR adds tests next to the code it changes. Notably:

- PR 4: `internal/models/instance_test.go` for runtime config
  validation.
- PR 5: `internal/deploy/tape_test.go` for golden manifest comparison.
- PR 6: `internal/api/runs_test.go` for handler + ingestion idempotency.
- PR 8: `console/src/__tests__/runs.test.ts`.

## 12. Existing CRDs

AIPlex does **not** define any Kubernetes CRDs of its own today.
Routes use Envoy AI Gateway CRDs (`aigateway.envoyproxy.io/v1alpha1`)
and Gateway API CRDs (`gateway.networking.k8s.io/v1`):

- `MCPRoute`, `LLMRoute`, `AIServiceBackend`, `HTTPRoute`,
  `SecurityPolicy`.

Instances live in Firestore, not as K8s objects. The Phase 7 proposal
of an `AgentRuntime` CRD changes that — for a greenfield product
that's a deliberate architectural choice, not a precedent break.
Decision deferred to its own PR (10) so PRs 4–9 can land on
`Instance.Runtime` first and the CRD can express the same shape once
it's been exercised end-to-end.

## 13. CLAUDE.md conventions

Notable conventions extracted from the 33 KB root `CLAUDE.md`:

- Commit style observed in `git log`: `feat(scope): ...`,
  `fix(scope): ...` with scope in parens (e.g. `feat(skillsplex): ...`,
  `fix(terraform): ...`, `feat(api): ...`).
- Tests are expected next to code (no isolated test suite outside
  `tests/` package roots).
- Design docs live in `design/` for large features.
- Secrets: never inline in YAML, referenced by name; production uses
  Google Secret Manager (`internal/secrets/`, `main.go` line 111).
- Multi-plane code is uniformly plane-aware (`switch inst.Plane` is
  the standard pattern).
- Branch naming for AI-driven work: `claude/<task>` — current branch
  follows this.

For this integration, all PRs will use `feat(runtime): ...` or
`feat(tape): ...` commit prefixes and add tests alongside code.

## PR breakdown (AIPlex side)

The integration plan splits into seven AIPlex PRs, all landing on
`claude/aiplex-tape-integration-odwFR`:

4. **Runtime config model** — `internal/models/instance.go`, validation,
   docs page, example YAML.
5. **Managed Tape deployment** — manifest generation, env injection,
   golden manifest tests.
6. **Tape event ingestion** — `/internal/tape/events`, new Firestore
   collections, idempotent projection.
7. **Run timeline API** — read endpoints under `/api/v1/runs/...`.
8. **Console Runs tab** — agent-detail Runs view with failure-first
   highlights.
9. **E2E treasury demo** — `examples/aiplex-tape-treasury/` and the
   `aiplex dev up --with-tape` flow.
10. **Operator actions** — Tape admin client + `aiplex:runs:*` scopes +
    guarded console actions, audited end-to-end.

PR numbering here matches the integration plan's PR numbering for the
benefit of cross-references; on the branch each lands as a separate
commit on `claude/aiplex-tape-integration-odwFR` so the user can review
them one at a time.

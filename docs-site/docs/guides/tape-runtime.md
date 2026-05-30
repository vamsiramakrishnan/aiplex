---
id: tape-runtime
title: Durable runtime — Tape
sidebar_label: Tape runtime
---

# Durable runtime — Tape

AIPlex agents can opt into a **durable runtime** that journals every
model decision and every external effect. The Tape substrate then
guarantees: crash mid-effect, resume from the journal, no duplicate
side effects, and every denied call leaves an auditable trail.

> AIPlex decides whether an agent is allowed to act.
> Tape proves what happened when it acted.

This guide covers the AIPlex side: declaring a runtime on an Instance,
the validation rules, and what the deploy engine will (eventually)
generate from it. For the Tape-side wire contract — `RunIdentity`, the
`scope` parameter on `@tape.effect`, the `kind="policy"` journal entry —
see [`tape/docs/integrations/aiplex.md`](https://github.com/vamsiramakrishnan/durable-agents/blob/main/tape/docs/integrations/aiplex.md)
in the `durable-agents` repo.

## Declaring a runtime on an Instance

Every `Instance` in AIPlex carries a `Runtime` field. The zero value
(`{Engine: "none"}`) is the v1 path — no durable runtime, the agent
runs as a plain pod. To opt into Tape, set `engine: tape` in the
instance config:

```yaml
instances:
  - name: Treasury Agent
    plane: a2aplex
    template: treasury-agent
    config:
      runtime:
        engine: tape
        durable: true
        replayable: true
        store:
          type: alloydb
          secret_ref: tape-store-url
        reactors:
          recovery: true
          reconciler: true
          timers: true
          outbox: true
          compensation: true
        outbox:
          sink: pubsub
          topic: aiplex-tape-events
```

The full worked example lives at
[`examples/durable-tape-runtime.yaml`](https://github.com/vamsiramakrishnan/aiplex/blob/main/examples/durable-tape-runtime.yaml).

## Fields

| Field | Type | Notes |
| --- | --- | --- |
| `engine`             | `none` &#124; `tape`                          | The execution substrate. `none` is the v1 path; `tape` provisions a tape-server + reactors. |
| `durable`            | `bool`                                        | Required `true` when `engine=tape`. |
| `replayable`         | `bool`                                        | Hints to the Console that re-drive is supported (Tape's contract is always replayable; this flag is for UX). |
| `store.type`         | `sqlite` &#124; `postgres` &#124; `alloydb` &#124; `bigtable` | The journal/projection backend. `sqlite` is dev-only — rejected in prod. |
| `store.secret_ref`   | `string`                                      | K8s Secret name carrying the connection URL. Required for postgres/alloydb/bigtable — connection strings are never inlined in manifests. |
| `reactors.*`         | `bool` (one per loop)                         | Toggles each of Tape's reactor loops independently. |
| `outbox.sink`        | `log` &#124; `webhook` &#124; `pubsub`        | Where the runtime's outbox stream is fanned out. AIPlex's audit ingestion (PR 6) reads from this. |
| `outbox.topic`       | `string`                                      | Required when `sink=pubsub`. |

## Validation rules

The runtime config is validated by `models.RuntimeConfig.Validate` at
admission time. The rules are intentionally strict — they catch
misconfigurations at deploy, not at first failure:

- `engine=tape` implies `durable=true`. A "non-durable durable runtime"
  is a configuration smell.
- `store.type=sqlite` is **rejected in production**. SQLite lives
  in-pod and doesn't survive a pod restart the way the Tape contract
  assumes.
- Non-sqlite stores require `store.secret_ref`. Connection strings
  must come from a K8s Secret, never inlined in a manifest.
- `outbox.sink=pubsub` requires `outbox.topic`.

`engine=none` (the v1 default) short-circuits validation — there's
nothing to check.

## What this enables, in order of PR

| PR | Lands | What it does |
| --- | --- | --- |
| **4** ✅ | Done    | `Instance.Runtime` field + validation + the example above. |
| **5** ✅ | Done    | The deploy engine reads `Instance.Runtime` and emits `tape-server` + `tape-reactors` manifests in `aiplex-system` (env-scoped, one per environment), and injects `TAPE_URL` + `AIPLEX_*` env vars onto the agent pod. |
| **6** ✅ | Done    | `POST /internal/tape/events` ingests Tape's outbox into AIPlex audit storage with `(run_id, seq)` idempotency; unknown agents quarantined; projects per-run summary on `ExecutionRun`. |
| **7** ✅ | Done    | `GET /api/v1/runs[/{id}[/{events,effects,obligations,budgets}]]` read API with tenant / agent / `has_unknown_effects` / `has_obligations` filters. |
| **8** ✅ | Done    | Console **Runs** tab: filterable run list (tenant / agent / has-UNKNOWN / has-obligations) + per-run timeline with kind-coloured event rows, auto-refresh every 3–5s. |
| **9** ✅ | Done    | End-to-end treasury demo: `examples/aiplex-tape-treasury/treasury.yaml` deploy manifest + `make e2e-aiplex-tape` smoke test asserting the headline claim (no duplicate wire after a mid-flight crash + reconcile). |
| **10** ✅ | Done    | Operator actions: `POST /api/v1/runs/{id}/{redrive,reconcile,cancel,signal,compensate}` through a `TapeAdmin` interface (real `GRPCTapeAdmin` in PR 11). |
| **11** ✅ | Done    | Polish round — see the [PR 11 matrix](#pr-11-no-half-measures-cleanup) below. |
| **12** ✅ | Done    | Prerequisites for retention: identity validation in the ADK (`AIPLEX_REQUIRE_IDENTITY`), wire-level effect-scope refusal in tape-server's store, Go SDK pinned to real pseudo-version with `go.work` for local override. |
| **13** ✅ | This PR | Compaction + retention: tape-server gets `CompactRun` + `ListCompactableRuns` RPCs that zero bulky payloads (request_json/response_json/error_json) while preserving the audit envelope; a Tape reactor (`tape-server --compact`) drives the loop with a per-instance `hot/compact/delete` window; AIPlex's `RetentionReactor` mirrors the policy across the projection (`Compacted=true`, `CompactedAt`, `RetainedUntil`), the Console renders an **archived** badge on compacted runs and disables operator actions, and `POST /api/v1/runs/{id}/compact` exposes the manual override (`aiplex runs compact <id>`). |

## Local development against an unpublished Tape SDK

By default `go.mod` pins the Tape Go SDK to a published pseudo-version. If
you're working in both repos at once (e.g. testing a Tape change before
publishing), drop a `go.work` next to `go.mod` to override locally:

```go
// go.work (DO NOT commit)
go 1.25

use (
    .
    ../durable-agents/tape/sdk/go
)
```

`go.work` is gitignored by default and overrides `go.mod`'s require for
the named module. CI on a clean clone uses the pinned version.

## PR 11 (no half measures cleanup)

After PR 10 several rough edges surfaced (stubs, missing authn, the
audit-vs-journal muddle, polling-not-streaming). PR 11 closes them.

| # | Item | Status |
| --- | --- | :---: |
| 1  | `AIPlexSink` in `tape/sdk/python/tape/sinks.py` (the missing outbox sink) | ✅ |
| 2  | Verify Tape admin RPCs cover every operator action | ✅ (all exist, no proto change needed) |
| 3  | `GRPCTapeAdmin` — real gRPC adapter replacing `NoopTapeAdmin` | ✅ |
| 4  | Bearer-token auth on `/internal/tape/events` | ✅ |
| 5  | OPA Rego scope rules for `aiplex:runs:*` | ✅ |
| 6  | Multi-tenant enforcement (filter ⇒ scope intersection) | ✅ |
| 7  | `Idempotency-Key` middleware on operator actions | ✅ |
| 8  | Separate `OperatorAudit` collection (off the Tape journal) | ✅ |
| 9  | `POST /internal/projections/rebuild/{run_id}` | ✅ |
| 10 | Console operator-action toolbar + audit timeline panel | ✅ |
| 11 | SSE streaming for run timelines (with polling fallback) | ✅ |
| 12 | `aiplex runs` CLI subcommand | ✅ |
| 13 | Diagnostic empty-state checklist on the Runs page | ✅ |
| 14 | `make dev-tape` (builds + runs tape-server alongside) | ✅ |
| 15 | Per-IP token-bucket rate limit on ingestion | ✅ |
| 16 | Refuse runtime mutation in place (409 Conflict) | ✅ |
| 17 | `tape-server` reference counting + health endpoint | ✅ |
| 18 | Design doc: [embedded tier is dev-only](../architecture/embedded-tier-decision.md) | ✅ |

See the architectural survey at
[`docs/integration/aiplex-tape-survey.md`](https://github.com/vamsiramakrishnan/aiplex/blob/main/docs/integration/aiplex-tape-survey.md)
for the full file-paths and shapes of each PR.

## Rollback

The Tape integration spans 13 PRs across two repositories. If something goes
wrong, you can disable layers independently — top-down, smallest blast radius
first.

| What broke | Knob | Effect |
| --- | --- | --- |
| Retention reactor is mis-compacting or hammering Tape | `AIPLEX_RETENTION_ENABLED=0` on the AIPlex API pod, then restart | Reactor stops on the next tick. Already-compacted runs stay compacted (Tape has the authoritative state). |
| Operator action gRPC calls are failing or wrong | unset `TAPE_URL` on the AIPlex API pod | `GRPCTapeAdmin` falls back to `NoopTapeAdmin`; operator-action buttons in the Console still write to `operator_audit` but never call Tape. Manual fixes go through `tape-cli` directly. |
| AIPlexSink is flooding `/internal/tape/events` | set `TAPE_OUTBOX_SINK=` (empty) on the agent pod | The sink stops POSTing. Events queue in the outbox; resume later by setting the var back. |
| A single Tape-backed Instance is misbehaving | edit the Instance, set `runtime.engine: none`, redeploy | The pod restarts without `TAPE_*` env vars; the next run uses the v1 (non-durable) path. Existing runs in Tape are unaffected. |
| The whole integration is regressing in prod | helm rollback to the pre-Tape revision in `aiplex-system` | Removes `tape-server`, the reactors deployment, and the retention env vars in one shot. Agent pods continue with whatever `Runtime` they were last deployed with — Set `engine: none` on each before rolling back if you want a hard cut. |
| The retention compactor zeroed payloads you needed back | n/a (one-way) | Compaction is destructive. The audit envelope (kind, seq, scope, tool, business_key, status) survives forever; request/response bodies don't. Lengthen `compact_after_days` ahead of time for instances you care about. |

The order matters: `AIPLEX_RETENTION_ENABLED=0` is the smallest disable and
should be the first move. `engine: none` is the largest at the per-instance
level. Full helm rollback is the nuclear option.

What rollback **doesn't** do: it can't unwind Tape's journal. Once a `BeginRun`
has landed, the run exists in `tape_runs` and the journal entries for it stay
there until retention deletes them (or you `tape-cli runs purge`). This is by
design — the journal is the auditable truth.

## Why a value, not a pointer

`Instance.Runtime` is a `RuntimeConfig` value rather than `*RuntimeConfig`
on purpose. Pointer-or-nil forces every consumer to branch on absence;
a zero-value `RuntimeConfig{Engine: "none"}` means "no durable runtime"
unambiguously and validates cleanly. The
[`models.NoneRuntime()`](https://github.com/vamsiramakrishnan/aiplex/blob/main/internal/models/runtime.go)
helper returns the canonical empty value.

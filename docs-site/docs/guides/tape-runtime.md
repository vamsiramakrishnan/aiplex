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
| **4** ✅ | This PR | `Instance.Runtime` field + validation + the example above. The field round-trips through storage and the API but does not yet change deploy behavior. |
| **5**   | Next   | The deploy engine reads `Instance.Runtime` and emits `tape-server` + `tape-reactors` manifests + env-var injection (`AIPLEX_*`, `TAPE_URL`) on the agent pod. |
| **6**   | Soon   | `/internal/tape/events` ingestion endpoint reads Tape's outbox into AIPlex audit storage with `(run_id, seq)` idempotency. |
| **7**   | Soon   | `/api/runs/...` read API on top of the ingested events. |
| **8**   | Soon   | Console "Runs" tab projecting the run timeline. |
| **9**   | Demo   | End-to-end treasury demo (`aiplex dev up --with-tape`). |
| **10**  | Ops    | Operator actions (redrive / reconcile / cancel / signal / compensate) under new `aiplex:runs:*` scopes. |

See the architectural survey at
[`docs/integration/aiplex-tape-survey.md`](https://github.com/vamsiramakrishnan/aiplex/blob/main/docs/integration/aiplex-tape-survey.md)
for the full file-paths and shapes of each PR.

## Why a value, not a pointer

`Instance.Runtime` is a `RuntimeConfig` value rather than `*RuntimeConfig`
on purpose. Pointer-or-nil forces every consumer to branch on absence;
a zero-value `RuntimeConfig{Engine: "none"}` means "no durable runtime"
unambiguously and validates cleanly. The
[`models.NoneRuntime()`](https://github.com/vamsiramakrishnan/aiplex/blob/main/internal/models/runtime.go)
helper returns the canonical empty value.

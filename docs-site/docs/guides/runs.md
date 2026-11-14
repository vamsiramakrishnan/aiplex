---
title: Durable runs
description: Inspect Tape-backed agent runs and perform operator actions.
---

# Durable runs

AIPlex projects the journal of Tape, a durable agent runtime, into its audit store. The API exposes the projection read-only under `/api/v1/runs`, plus operator actions that require their own scopes.

```bash
aiplex runs list
aiplex runs inspect RUN_ID
aiplex runs events RUN_ID
aiplex runs effects RUN_ID
aiplex runs obligations RUN_ID
aiplex runs budgets RUN_ID
aiplex runs health
aiplex runs audit RUN_ID
```

All of these accept `-o json` and `-o yaml`, which print the API's records unchanged.

## The durable-run backend is required

Runs exist only when a Tape pipeline delivers journal events to the API. Under `aiplex dev` the `/api/v1/runs` routes are served, but nothing delivers events, so `aiplex runs list` is empty and `aiplex runs health` reports `Last ingest: none recorded`. Nothing in the local mode creates a run; these commands were checked there only for that empty state, and against a mock API in tests. They have not been validated against a live Tape deployment.

## See what you are about to reconcile

`aiplex runs list --has-unknown` finds runs with effects whose outcome is unknown. Look at them before acting:

```bash
aiplex runs effects RUN_ID
```

The `OUTCOME` column is `started`, `confirmed`, `failed`, `duplicate` or `unknown`. `unknown` means the backend could not tell whether the effect happened. It is not success, failure or zero, and it stays `unknown` in the table and in `-o json` and `-o yaml` (as `"kind": "effect.unknown"`). `aiplex runs reconcile RUN_ID` asks the runtime to resolve those effects.

`aiplex runs obligations RUN_ID` lists the follow-up work the run owes (what `aiplex runs compensate` drains). `aiplex runs budgets RUN_ID` lists budget charges and a total. A charge whose event carries no amount is shown as `unknown` and the total says how many charges it leaves out. If every charge is unknown the total is `unknown`, not `$0.00`; with no charges there is no total.

`effects`, `obligations` and `budgets` are not paged: the API reads at most the first 10000 events of the run and filters them, so events beyond that are not shown.

`aiplex runs health` shows the last ingest time (`none recorded` if the API has none, or could not read it), whether any run is known, and how many agent instances use the Tape runtime.

## Page through events

```bash
aiplex runs events RUN_ID --from-seq 1 --limit 200
```

`--from-seq` is inclusive and `--limit` defaults to 1000. When a page is full the command ends with the exact next command, for example `More: aiplex runs events RUN_ID --from-seq 201 --limit 200`. A page that is exactly full and also the last one prints that line once; the next call returns no events. In Go, `ListRunEvents(ctx, runID, aiplex.ListRunEventsOpts{FromSeq: 201, Limit: 200})` does the same, and `ListRunEffects`, `ListRunObligations`, `ListRunBudgets` and `GetRunsHealth` cover the other routes.

Operator actions each require the matching `aiplex:runs:{action}` scope at the gateway authorization layer:

| Command | Action scope |
| --- | --- |
| `aiplex runs redrive` | `aiplex:runs:redrive` |
| `aiplex runs reconcile` | `aiplex:runs:reconcile` |
| `aiplex runs cancel` | `aiplex:runs:cancel` |
| `aiplex runs signal` | `aiplex:runs:signal` |
| `aiplex runs compensate` | `aiplex:runs:compensate` |

Reading runs requires `aiplex:runs:read`. `aiplex runs compact` also exists.

Tape delivers journal events to `POST /internal/tape/events`, which is protected by a bearer token (`TAPE_INGEST_TOKEN`) and a per-IP rate limit (`TAPE_INGEST_RPS`, default 1000). `make dev-tape` starts a local pipeline when a sibling `durable-agents` checkout is present. See `examples/aiplex-tape-treasury/` in the repository.

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
aiplex runs audit RUN_ID
```

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

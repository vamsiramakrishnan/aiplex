---
title: aiplex runs
sidebar_label: runs
description: "Inspect + operate on Tape-backed runs"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Browse, inspect, and operate on durable agent runs surfaced from
Tape's journal into AIPlex's audit projection. Operator actions
(redrive, reconcile, cancel, signal, compensate) require the
corresponding aiplex:runs:{action} scope.

Usage:
  aiplex runs [command]

Available Commands:
  audit       Show the operator-action audit trail for a run
  cancel      Cancel a run (cooperative — agent checks at next boundary)
  compact     Compact this run now — zero bulky payloads, keep the audit envelope
  compensate  Kick the compensator to drain pending obligations on this run
  events      Show the ordered timeline for a run
  inspect     Show the projected summary for one run
  list        List the most recent runs
  reconcile   Kick the reconciler to resolve UNKNOWN effects on this run
  redrive     Re-drive a stuck or terminal run via the Tape admin RPC
  signal      Send a signal to a parked run (releases the named gate)

Flags:
  -h, --help   help for runs

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex runs [command] --help" for more information about a command.
```

### `aiplex runs audit`

```text
Show the operator-action audit trail for a run

Usage:
  aiplex runs audit <run_id> [flags]

Flags:
  -h, --help   help for audit

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex runs cancel`

```text
Cancel a run (cooperative — agent checks at next boundary)

Usage:
  aiplex runs cancel <run_id> [flags]

Flags:
  -h, --help            help for cancel
      --reason string   human-readable cancellation reason

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex runs compact`

```text
compact triggers Tape's CompactRun out of band. The retention reactor
handles the scheduled, policy-driven path; this command is the manual
override for ops who want to reclaim payload bytes before the window
expires. Tape will refuse if the run isn't yet settled (open obligations
or UNKNOWN effects).

Usage:
  aiplex runs compact <run_id> [flags]

Flags:
  -h, --help   help for compact

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex runs compensate`

```text
Kick the compensator to drain pending obligations on this run

Usage:
  aiplex runs compensate <run_id> [flags]

Flags:
  -h, --help   help for compensate

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex runs events`

```text
Show the ordered timeline for a run

Usage:
  aiplex runs events <run_id> [flags]

Flags:
  -h, --help   help for events

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex runs inspect`

```text
Show the projected summary for one run

Usage:
  aiplex runs inspect <run_id> [flags]

Flags:
  -h, --help   help for inspect

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex runs list`

```text
List the most recent runs

Usage:
  aiplex runs list [flags]

Flags:
      --agent string      filter by agent_id
      --has-obligations   only runs with obligations
      --has-unknown       only runs with UNKNOWN effects
  -h, --help              help for list
      --limit int         result cap (default 50)
      --tenant string     filter by tenant_id

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex runs reconcile`

```text
Kick the reconciler to resolve UNKNOWN effects on this run

Usage:
  aiplex runs reconcile <run_id> [flags]

Flags:
  -h, --help   help for reconcile

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex runs redrive`

```text
Re-drive a stuck or terminal run via the Tape admin RPC

Usage:
  aiplex runs redrive <run_id> [flags]

Flags:
  -h, --help   help for redrive

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex runs signal`

```text
Send a signal to a parked run (releases the named gate)

Usage:
  aiplex runs signal <run_id> [flags]

Flags:
      --gate-name string    name of the gate to signal (required)
  -h, --help                help for signal
      --resolution string   JSON-encoded resolution payload

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


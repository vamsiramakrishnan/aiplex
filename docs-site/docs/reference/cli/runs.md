---
title: aiplex runs
sidebar_label: runs
description: "Inspect and operate durable agent runs"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Browse, inspect, and operate on durable agent runs surfaced from
Tape's journal into AIPlex's audit projection. Operator actions
(redrive, reconcile, cancel, signal, compensate) require the
corresponding aiplex:runs:{action} scope.

Usage:
  aiplex runs [flags]
  aiplex runs [command]

Examples:
  aiplex runs list --limit 20
  aiplex runs inspect run-0123

Available Commands:
  list        List the most recent runs
  inspect     Show the projected summary for one run
  events      Show the ordered timeline for a run
  audit       Show the operator-action audit trail for a run
  redrive     Re-drive a stuck or terminal run via the Tape admin RPC
  reconcile   Kick the reconciler to resolve UNKNOWN effects on this run
  cancel      Cancel a run (cooperative — agent checks at next boundary)
  signal      Send a signal to a parked run (releases the named gate)
  compensate  Kick the compensator to drain pending obligations on this run
  compact     Compact this run now — zero bulky payloads, keep the audit envelope

Flags:
  -h, --help   help for runs

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex runs [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex runs list`

```text
List the most recent runs

Usage:
  aiplex runs list [flags]

Examples:
  aiplex runs list --limit 20
  aiplex runs list --has-unknown

Flags:
      --agent string      filter by agent_id
      --has-obligations   only runs with obligations
      --has-unknown       only runs with UNKNOWN effects
  -h, --help              help for list
      --limit int         result cap (default 50)
      --tenant string     filter by tenant_id

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex runs inspect`

```text
Show the projected summary for one run

Usage:
  aiplex runs inspect <run_id> [flags]

Examples:
  aiplex runs inspect run-0123

Flags:
  -h, --help   help for inspect

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex runs events`

```text
Show the ordered timeline for a run

Usage:
  aiplex runs events <run_id> [flags]

Examples:
  aiplex runs events run-0123

Flags:
  -h, --help   help for events

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex runs audit`

```text
Show the operator-action audit trail for a run

Usage:
  aiplex runs audit <run_id> [flags]

Examples:
  aiplex runs audit run-0123

Flags:
  -h, --help   help for audit

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex runs redrive`

```text
Re-drive a stuck or terminal run via the Tape admin RPC

Usage:
  aiplex runs redrive <run_id> [flags]

Examples:
  aiplex runs redrive run-0123

Flags:
  -h, --help   help for redrive

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex runs reconcile`

```text
Kick the reconciler to resolve UNKNOWN effects on this run

Usage:
  aiplex runs reconcile <run_id> [flags]

Examples:
  aiplex runs reconcile run-0123

Flags:
  -h, --help   help for reconcile

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex runs cancel`

```text
Cancel a run (cooperative — agent checks at next boundary)

Usage:
  aiplex runs cancel <run_id> [flags]

Examples:
  aiplex runs cancel run-0123 --reason "superseded"

Flags:
  -h, --help            help for cancel
      --reason string   human-readable cancellation reason

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex runs signal`

```text
Send a signal to a parked run (releases the named gate)

Usage:
  aiplex runs signal <run_id> [flags]

Examples:
  aiplex runs signal run-0123 --gate-name approval --resolution approved

Flags:
      --gate-name string    name of the gate to signal (required)
  -h, --help                help for signal
      --resolution string   JSON-encoded resolution payload

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex runs compensate`

```text
Kick the compensator to drain pending obligations on this run

Usage:
  aiplex runs compensate <run_id> [flags]

Examples:
  aiplex runs compensate run-0123

Flags:
  -h, --help   help for compensate

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
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

Examples:
  aiplex runs compact run-0123

Flags:
  -h, --help   help for compact

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```


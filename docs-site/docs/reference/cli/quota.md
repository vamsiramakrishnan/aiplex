---
title: aiplex quota
sidebar_label: quota
description: "Inspect quota policy, balances, loans and admission decisions"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Inspect quota policy, balances, loans and admission decisions

Usage:
  aiplex quota [flags]
  aiplex quota [command]

Examples:
  aiplex quota policy
  aiplex quota attempt qa_0123

Available Commands:
  attempt         Read an owned or administratively visible quota attempt
  policy          Inspect the active and configured quota policy
  policy-validate Validate a policy export without activating it
  preview         Simulate admission without reserving or dispatching
  inventory       Read a bounded quota administration page (kinds: budgets, loans, attempts, reconciliations, audit, terminal-work)
  balance         Read a tenant-authorized budget balance
  loan            Inspect a quota allocation loan
  loan-grant      Grant a policy-authorized allocation loan
  loan-return     Return only currently unused borrowed allocation
  evidence        Read immutable native terminal facts and resolution state
  reconcile-plan  Prepare an expiring terminal reconciliation for review
  reconcile       Inspect a reconciliation plan and immutable receipt
  reconcile-apply Apply the exact reviewed terminal reconciliation

Flags:
  -h, --help   help for quota

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex quota [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex quota attempt`

```text
Read an owned or administratively visible quota attempt

Usage:
  aiplex quota attempt <attempt-id> [flags]

Examples:
  aiplex quota attempt qa_0123

Flags:
  -h, --help   help for attempt

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

### `aiplex quota policy`

```text
Inspect the active and configured quota policy

Usage:
  aiplex quota policy [flags]

Examples:
  aiplex quota policy
  aiplex quota policy -o json

Flags:
  -h, --help   help for policy

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

### `aiplex quota policy-validate`

```text
Validate a policy export without activating it

Usage:
  aiplex quota policy-validate <policy.json> [flags]

Examples:
  aiplex quota policy-validate policy.json

Flags:
      --expected-digest string   Current active policy digest
  -h, --help                     help for policy-validate

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

### `aiplex quota preview`

```text
Simulate admission without reserving or dispatching

Usage:
  aiplex quota preview <demand.json> [flags]

Examples:
  aiplex quota preview demand.json

Flags:
  -h, --help   help for preview

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

### `aiplex quota inventory`

```text
Read a bounded quota administration page (kinds: budgets, loans, attempts, reconciliations, audit, terminal-work)

Usage:
  aiplex quota inventory <kind> [flags]

Examples:
  aiplex quota inventory budgets --limit 50

Flags:
      --after string   Continue strictly after this record ID
  -h, --help           help for inventory
      --limit int      Page size, 1 to 64 (default 20)

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

### `aiplex quota balance`

```text
Read a tenant-authorized budget balance

Usage:
  aiplex quota balance <key> [flags]

Examples:
  aiplex quota balance team-eng

Flags:
  -h, --help   help for balance

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

### `aiplex quota loan`

```text
Inspect a quota allocation loan

Usage:
  aiplex quota loan <id> [flags]

Examples:
  aiplex quota loan loan-1

Flags:
  -h, --help   help for loan

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

### `aiplex quota loan-grant`

```text
Grant a policy-authorized allocation loan

Usage:
  aiplex quota loan-grant <loan.json> [flags]

Examples:
  aiplex quota loan-grant loan.json --idempotency-key loan-1-v1

Flags:
  -h, --help                     help for loan-grant
      --idempotency-key string   Stable administration operation key

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

### `aiplex quota loan-return`

```text
Return only currently unused borrowed allocation

Usage:
  aiplex quota loan-return <id> [flags]

Examples:
  aiplex quota loan-return loan-1 --idempotency-key loan-1-return

Flags:
  -h, --help                     help for loan-return
      --idempotency-key string   Stable administration operation key

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

### `aiplex quota evidence`

```text
Read immutable native terminal facts and resolution state

Usage:
  aiplex quota evidence <event-id> [flags]

Examples:
  aiplex quota evidence ev_0123

Flags:
  -h, --help   help for evidence

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

### `aiplex quota reconcile-plan`

```text
Prepare an expiring terminal reconciliation for review

Usage:
  aiplex quota reconcile-plan <proposal.json> [flags]

Examples:
  aiplex quota reconcile-plan proposal.json --idempotency-key rec-2026-11

Flags:
  -h, --help                     help for reconcile-plan
      --idempotency-key string   Stable administration operation key

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

### `aiplex quota reconcile`

```text
Inspect a reconciliation plan and immutable receipt

Usage:
  aiplex quota reconcile <plan-id> [flags]

Examples:
  aiplex quota reconcile plan-0123

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

### `aiplex quota reconcile-apply`

```text
Apply the exact reviewed terminal reconciliation

Usage:
  aiplex quota reconcile-apply <plan-id> [flags]

Examples:
  aiplex quota reconcile-apply plan-0123 --digest sha256:0123 --idempotency-key rec-2026-11-apply

Flags:
      --digest string            Exact reviewed plan digest
  -h, --help                     help for reconcile-apply
      --idempotency-key string   Stable approval operation key

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


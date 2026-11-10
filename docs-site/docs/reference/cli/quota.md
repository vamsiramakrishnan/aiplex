---
title: aiplex quota
sidebar_label: quota
description: "Inspect managed quota execution"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Inspect managed quota execution

Usage:
  aiplex quota [command]

Available Commands:
  attempt         Read an owned or administratively visible quota attempt
  balance         Read a tenant-authorized budget balance
  evidence        Read immutable native terminal facts and resolution state
  inventory       Read a bounded quota administration page
  loan            Inspect a quota allocation loan
  loan-grant      Grant a policy-authorized allocation loan
  loan-return     Return only currently unused borrowed allocation
  policy          Inspect the active and configured quota policy
  policy-validate Validate a policy export without activating it
  preview         Simulate admission without reserving or dispatching
  reconcile       Inspect a reconciliation plan and immutable receipt
  reconcile-apply Apply the exact reviewed terminal reconciliation
  reconcile-plan  Prepare an expiring terminal reconciliation for review

Flags:
  -h, --help   help for quota

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex quota [command] --help" for more information about a command.
```

### `aiplex quota attempt`

```text
Read an owned or administratively visible quota attempt

Usage:
  aiplex quota attempt <attempt-id> [flags]

Flags:
  -h, --help   help for attempt

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex quota balance`

```text
Read a tenant-authorized budget balance

Usage:
  aiplex quota balance <key> [flags]

Flags:
  -h, --help   help for balance

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex quota evidence`

```text
Read immutable native terminal facts and resolution state

Usage:
  aiplex quota evidence <event-id> [flags]

Flags:
  -h, --help   help for evidence

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex quota inventory`

```text
Read a bounded quota administration page

Usage:
  aiplex quota inventory <kind> [flags]

Flags:
      --after string   Continue strictly after this record ID
  -h, --help           help for inventory
      --limit int      Page size, 1 to 64 (default 20)

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex quota loan`

```text
Inspect a quota allocation loan

Usage:
  aiplex quota loan <id> [flags]

Flags:
  -h, --help   help for loan

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex quota loan-grant`

```text
Grant a policy-authorized allocation loan

Usage:
  aiplex quota loan-grant <loan.json> [flags]

Flags:
  -h, --help                     help for loan-grant
      --idempotency-key string   Stable administration operation key

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex quota loan-return`

```text
Return only currently unused borrowed allocation

Usage:
  aiplex quota loan-return <id> [flags]

Flags:
  -h, --help                     help for loan-return
      --idempotency-key string   Stable administration operation key

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex quota policy`

```text
Inspect the active and configured quota policy

Usage:
  aiplex quota policy [flags]

Flags:
  -h, --help   help for policy

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex quota policy-validate`

```text
Validate a policy export without activating it

Usage:
  aiplex quota policy-validate <policy.json> [flags]

Flags:
      --expected-digest string   Current active policy digest
  -h, --help                     help for policy-validate

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex quota preview`

```text
Simulate admission without reserving or dispatching

Usage:
  aiplex quota preview <demand.json> [flags]

Flags:
  -h, --help   help for preview

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex quota reconcile`

```text
Inspect a reconciliation plan and immutable receipt

Usage:
  aiplex quota reconcile <plan-id> [flags]

Flags:
  -h, --help   help for reconcile

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex quota reconcile-apply`

```text
Apply the exact reviewed terminal reconciliation

Usage:
  aiplex quota reconcile-apply <plan-id> [flags]

Flags:
      --digest string            Exact reviewed plan digest
  -h, --help                     help for reconcile-apply
      --idempotency-key string   Stable approval operation key

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex quota reconcile-plan`

```text
Prepare an expiring terminal reconciliation for review

Usage:
  aiplex quota reconcile-plan <proposal.json> [flags]

Flags:
  -h, --help                     help for reconcile-plan
      --idempotency-key string   Stable administration operation key

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


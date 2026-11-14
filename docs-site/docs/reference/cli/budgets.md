---
title: aiplex budgets
sidebar_label: budgets
description: "Budgets: team and project limits above per-caller quota"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
A budget caps cost, tokens or requests for a team, project, access point or
other set of callers, over a calendar month, week or day (or tumbling N-hour
windows). Budgets form a tree: a request draws from its own budget and every
parent, and is refused when any one is exhausted. QuotaPlex enforces them, so
QuotaPlex must be enabled (aiplex dev --quota does this locally).

  aiplex budgets create platform --name "Platform team" --unit cost --currency USD --amount 250.00 --window month --group platform-eng
  aiplex budgets status platform
  aiplex budgets preview --group platform-eng --access-point ide

Cost budgets need a price for every model they count. A model with no price is
refused by default (--on-unknown-cost deny); allow_and_flag admits it and counts
it as unknown cost instead.

A cost budget counts model calls only, unless it is created or updated with
--include-tool-calls: then managed tool calls draw their price too, and a tool
call with no price is treated like a model with no price. Deployment actions are
not tool calls for this purpose: they are never charged or refused by it.

Usage:
  aiplex budgets [flags]
  aiplex budgets [command]

Aliases:
  budgets, budget

Examples:
  aiplex budgets list
  aiplex budgets status platform

Available Commands:
  list        List budgets (admin or deployer)
  get         Show one budget and where it stands
  create      Create (or with --update replace) a budget
  update      Change a budget's limit, window, parent or members
  delete      Delete a budget
  status      Show spent, reserved and remaining for the current window
  preview     Which budgets would a request draw from, and why (admin; changes nothing)

Flags:
  -h, --help   help for budgets

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex budgets [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex budgets list`

```text
List budgets (admin or deployer)

Usage:
  aiplex budgets list [flags]

Aliases:
  list, ls

Examples:
  aiplex budgets list
  aiplex budgets list -o json

Flags:
  -h, --help   help for list

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

### `aiplex budgets get`

```text
Show one budget and where it stands

Usage:
  aiplex budgets get <id> [flags]

Aliases:
  get, show, describe

Examples:
  aiplex budgets get platform
  aiplex budgets get platform -o yaml > budget.yaml

Flags:
  -h, --help   help for get

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

### `aiplex budgets create`

```text
Creates a budget, or replaces it with --update. A budget needs a limit (--unit,
--amount, and --currency for cost), a window and, usually, members. Without
members a budget draws only through its child budgets.

Group names are qualified with the identity provider you are signed in to. Use
'aiplex budgets create -f budget.yaml' to give the full specification.

Usage:
  aiplex budgets create <id> [flags]

Examples:
  aiplex budgets create platform --name "Platform team" --unit cost --currency USD --amount 250.00 --window month --group platform-eng
  aiplex budgets create engineering --unit tokens --amount 50000000 --window month
  aiplex budgets create platform-chat --parent engineering --unit tokens --amount 10000000 --window week --access-point ide
  aiplex budgets create ci --unit requests --amount 5000 --window 6h --agent spiffe://example/ci

Flags:
      --access-point strings     Member access point ID, repeatable
      --agent strings            Member agent ID, repeatable
      --amount string            Limit as an exact decimal, such as 250.00 (cost) or 1000000 (tokens, requests)
      --client strings           Member client registration ID, repeatable
      --currency string          Currency for a cost budget (for example USD); must match a money meter of the quota policy
  -f, --file string              JSON or YAML specification, or - for stdin (instead of the flags)
      --group strings            Member group name from your identity provider, repeatable
  -h, --help                     help for create
      --include-tool-calls       Cost budgets: also count managed tool calls at their price (off by default: model calls only; deployment actions are never counted by it); --include-tool-calls=false turns it off
      --name string              Display name
      --on-unknown-cost string   Cost budgets: deny (default) refuses a model with no price; allow_and_flag admits it and counts it as unknown cost
      --parent string            Parent budget ID (a request also draws from every ancestor)
      --project strings          Member project label (set on an access point with its project field), repeatable
      --threshold ints           Alert threshold in percent, repeatable (default 50, 80, 100)
      --timezone string          IANA time zone of a calendar window (default UTC)
      --unit string              What the budget caps: cost, tokens or requests
      --update                   Replace the budget if it already exists
      --user strings             Member user (principal id, or name@localhost in aiplex dev), repeatable
      --window string            month, week (starts Monday), day, or a tumbling window such as 6h

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

### `aiplex budgets update`

```text
Reads the budget, applies the flags you give and saves it with the revision it
read, so a concurrent change is not overwritten. Members are changed with
--add KIND=VALUE and --remove KIND=VALUE, where KIND is group, user, agent,
client, access-point or project. Changing the unit, currency or window starts a
new counter; changing only the amount keeps the spend so far.

Usage:
  aiplex budgets update <id> [flags]

Examples:
  aiplex budgets update platform --amount 400.00
  aiplex budgets update platform --add group=data-eng --remove project=old
  aiplex budgets update platform --include-tool-calls
  aiplex budgets update platform -f budget.yaml

Flags:
      --add stringArray          Add a member: KIND=VALUE (group, user, agent, client, access-point, project), repeatable
      --amount string            Limit as an exact decimal, such as 250.00 (cost) or 1000000 (tokens, requests)
      --currency string          Currency for a cost budget (for example USD); must match a money meter of the quota policy
  -f, --file string              Replace the specification from a JSON or YAML file, or - for stdin
  -h, --help                     help for update
      --include-tool-calls       Cost budgets: also count managed tool calls at their price (off by default: model calls only; deployment actions are never counted by it); --include-tool-calls=false turns it off
      --name string              Display name
      --on-unknown-cost string   Cost budgets: deny (default) refuses a model with no price; allow_and_flag admits it and counts it as unknown cost
      --parent string            Parent budget ID (a request also draws from every ancestor)
      --remove stringArray       Remove a member: KIND=VALUE, repeatable
      --threshold ints           Alert threshold in percent, repeatable (default 50, 80, 100)
      --timezone string          IANA time zone of a calendar window (default UTC)
      --unit string              What the budget caps: cost, tokens or requests
      --window string            month, week (starts Monday), day, or a tumbling window such as 6h

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

### `aiplex budgets delete`

```text
Stops the budget from being enforced. Its counters stay in the quota store until their windows end, so recreating it with the same unit and window resumes them. A budget with children cannot be deleted.

Usage:
  aiplex budgets delete <id> [flags]

Aliases:
  delete, rm, remove

Examples:
  aiplex budgets delete platform --yes

Flags:
  -h, --help   help for delete
  -y, --yes    Do not ask for confirmation (required when there is no terminal)

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

### `aiplex budgets status`

```text
Members of a budget see its spent, reserved and remaining amounts. Operators also
see unknown-cost requests, threshold events, each child budget and, with the
admin role, the top spenders by principal from the usage ledger.

Usage:
  aiplex budgets status <id> [flags]

Examples:
  aiplex budgets status platform
  aiplex budgets status platform -o json

Flags:
  -h, --help   help for status

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

### `aiplex budgets preview`

```text
Answers, for a hypothetical caller and access point, which budgets a request would
draw from, whether each matches the caller directly or only through a child, and
which selector matched. With -f a draft budget is validated as if saved and
included, so you can review a change before applying it with create or update.

The caller is hypothetical: nothing in it is verified. A real request uses the
groups of its verified token.

Usage:
  aiplex budgets preview [flags]

Examples:
  aiplex budgets preview --group platform-eng --access-point ide
  aiplex budgets preview --user alice@localhost --plane mcplex
  aiplex budgets preview --group platform-eng -f new-budget.yaml

Flags:
      --access-point string   Access point the request enters through (its project label applies)
      --agent strings         Caller's agent ID
      --client string         Caller's client registration ID
  -f, --file string           A draft budget (JSON or YAML) to validate and include; not saved
      --group strings         Caller's group name, repeatable
  -h, --help                  help for preview
      --plane string          llmplex (default), mcplex or a2aplex
      --user string           Caller's user (principal id or name@localhost)

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


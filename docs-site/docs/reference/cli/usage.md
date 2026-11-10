---
title: aiplex usage
sidebar_label: usage
description: "Usage ledger — what each request cost in requests, tokens and dollars"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Query the usage ledger: one record per managed request on any plane, by any
client or agent. Unknown usage or cost is shown as "unknown", never as zero,
and cost always carries its basis (settled or estimated). Non-administrators
see only their own records.

Usage:
  aiplex usage [command]

Available Commands:
  get         Show every field of one usage record
  prices      Manage model prices used to estimate cost (writes need the admin role)
  records     List usage records, newest first
  summary     Totals of requests, tokens and cost, optionally grouped

Flags:
  -h, --help   help for usage

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex usage [command] --help" for more information about a command.
```

### `aiplex usage get`

```text
Show every field of one usage record

Usage:
  aiplex usage get <id> [flags]

Examples:
  aiplex usage get ul_0123456789abcdef

Flags:
  -h, --help   help for get

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex usage prices`

```text
Without a configured price a record's cost is unknown unless quota settles it.
Prices are revisioned resources: replacing or deleting one needs its current
revision, so concurrent edits are never silently overwritten.

Usage:
  aiplex usage prices [command]

Available Commands:
  delete      Delete a price (uses the current revision unless --revision is given)
  get         Show one price and its current revision
  list        List configured model prices
  set         Create a price, or replace one with --revision

Flags:
  -h, --help   help for prices

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex usage prices [command] --help" for more information about a command.
```

#### `aiplex usage prices delete`

```text
Delete a price (uses the current revision unless --revision is given)

Usage:
  aiplex usage prices delete <id> [flags]

Examples:
  aiplex usage prices delete gemini-flash --revision 4

Flags:
  -h, --help            help for delete
      --revision uint   Expected revision

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex usage prices get`

```text
Show one price and its current revision

Usage:
  aiplex usage prices get <id> [flags]

Examples:
  aiplex usage prices get gemini-flash

Flags:
  -h, --help   help for get

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex usage prices list`

```text
List configured model prices

Usage:
  aiplex usage prices list [flags]

Examples:
  aiplex usage prices list

Flags:
  -h, --help   help for list

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex usage prices set`

```text
Reads a price specification (id, model_id, provider?, currency, scale, meters,
effective_from?, effective_until?) and writes it. Without --revision the
price must not exist yet; to replace one, pass the revision shown by
"usage prices get". A stale revision fails with a conflict and changes nothing.

Usage:
  aiplex usage prices set -f <price.json> [flags]

Examples:
  aiplex usage prices set -f gemini-flash.json
  aiplex usage prices set -f gemini-flash.json --revision 3

Flags:
  -f, --file string     Price specification JSON file
  -h, --help            help for set
      --revision uint   Current revision to replace (0 creates)

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex usage records`

```text
List usage records, newest first

Usage:
  aiplex usage records [flags]

Examples:
  aiplex usage records --since 24h --model gemini-2.5-flash
  aiplex usage records --agent tutor-agent --status error --limit 50
  aiplex usage records --access-point ide-team --caller-kind agent --since 24h
  aiplex usage records --since 7d --all -o json

Flags:
      --access-point string   Access point ID (access_point_id)
      --agent string          Agent / actor ID (actor_id)
      --all                   Follow every page
      --caller-kind string    Caller kind: agent, user or client (caller_kind)
      --client string         Client ID (client_id)
      --cursor string         Continue from a next_cursor value
  -h, --help                  help for records
      --limit int             Page size, 1 to 1000 (default 100)
      --model string          Model ID (model_id)
      --plane string          Plane: llmplex, mcplex or a2aplex
      --provider string       Provider
      --since string          Start of window: duration like 24h, 7d, or an RFC3339 time
      --source string         Source: managed or reported
      --status string         Status: ok, error, denied or unknown
      --tool string           Tool name (tool_name)
      --until string          End of window (exclusive): duration ago like 1h, or an RFC3339 time
      --user string           User / principal ID (principal_id)

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex usage summary`

```text
Aggregate usage. --by takes plane, provider, model, tool, agent, user, client,
access-point, caller-kind or status (aliases: model=model_id, agent=actor_id,
user=principal_id, client=client_id, tool=tool_name, access-point=access_point_id,
caller-kind=caller_kind). Records whose usage is bounded (lower_bound, ambiguous
or partial) are included in the sums and counted in the BOUNDED column; their
totals are not final. Costs are never summed across currencies
or bases. When the scan limit is reached the totals cover only the newest
records and the command says so.

Usage:
  aiplex usage summary [flags]

Examples:
  aiplex usage summary --by model,agent --since 7d
  aiplex usage summary --by access-point,caller-kind --since 7d
  aiplex usage summary --by model --bucket day --since 30d -o json

Flags:
      --access-point string   Access point ID (access_point_id)
      --agent string          Agent / actor ID (actor_id)
      --bucket string         Time bucket: hour or day
      --by strings            Group by (comma separated): plane,provider,model,tool,agent,user,client,status
      --caller-kind string    Caller kind: agent, user or client (caller_kind)
      --client string         Client ID (client_id)
  -h, --help                  help for summary
      --model string          Model ID (model_id)
      --plane string          Plane: llmplex, mcplex or a2aplex
      --provider string       Provider
      --since string          Start of window: duration like 24h, 7d, or an RFC3339 time
      --source string         Source: managed or reported
      --status string         Status: ok, error, denied or unknown
      --tool string           Tool name (tool_name)
      --until string          End of window (exclusive): duration ago like 1h, or an RFC3339 time
      --user string           User / principal ID (principal_id)

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


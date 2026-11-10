---
title: aiplex usage
sidebar_label: usage
description: "See what each request cost: records, totals and model prices"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Query the usage ledger: one record per managed request on any plane, by any
client or agent. Unknown usage or cost is shown as "unknown", never as zero,
and cost always carries its basis (settled or estimated). Non-administrators
see only their own records.

Usage:
  aiplex usage [flags]
  aiplex usage [command]

Examples:
  aiplex usage records --since 24h
  aiplex usage summary --by model,user --since 7d
  aiplex usage prices set gpt-4o-mini --input-per-mtok 0.15 --output-per-mtok 0.60

Available Commands:
  records     List usage records, newest first
  get         Show every field of one usage record
  summary     Totals of requests, tokens and cost, optionally grouped
  prices      Manage model prices used to estimate cost (writes need the admin role)

Flags:
  -h, --help   help for usage

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex usage [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
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
      --access-point string     Access point ID (access_point_id)
      --agent string            Agent / actor ID (actor_id)
      --all                     Follow every page
      --batch string[="true"]   Provider batch records only (true) or excluding them (false)
      --bound string            Usage bound: lower_bound, ambiguous, partial or none (usage_bound)
      --caller-kind string      Caller kind: agent, user or client (caller_kind)
      --client string           Client ID (client_id)
      --cursor string           Continue from a next_cursor value
      --error-class string      Error class (error_class)
  -h, --help                    help for records
      --limit int               Page size, 1 to 1000 (default 100)
      --model string            Model ID (model_id)
      --plane string            Plane: llmplex, mcplex or a2aplex
      --provider string         Provider
      --since string            Start of window: duration like 24h, 7d, or an RFC3339 time
      --source string           Source: managed or reported
      --status string           Status: ok, error, denied or unknown
      --tool string             Tool name (tool_name)
      --until string            End of window (exclusive): duration ago like 1h, or an RFC3339 time
      --user string             User / principal ID (principal_id)

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

### `aiplex usage summary`

```text
Aggregate usage. --by takes plane, provider, model, tool, agent, user, client,
access-point, caller-kind, status, source, error-class, bound (usage_bound)
or batch (aliases: model=model_id, agent=actor_id,
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
      --access-point string     Access point ID (access_point_id)
      --agent string            Agent / actor ID (actor_id)
      --batch string[="true"]   Provider batch records only (true) or excluding them (false)
      --bound string            Usage bound: lower_bound, ambiguous, partial or none (usage_bound)
      --bucket string           Time bucket: hour or day
      --by strings              Group by (comma separated): plane,provider,model,tool,agent,user,client,access-point,caller-kind,status,source,error-class,bound,batch
      --caller-kind string      Caller kind: agent, user or client (caller_kind)
      --client string           Client ID (client_id)
      --error-class string      Error class (error_class)
  -h, --help                    help for summary
      --model string            Model ID (model_id)
      --plane string            Plane: llmplex, mcplex or a2aplex
      --provider string         Provider
      --since string            Start of window: duration like 24h, 7d, or an RFC3339 time
      --source string           Source: managed or reported
      --status string           Status: ok, error, denied or unknown
      --tool string             Tool name (tool_name)
      --until string            End of window (exclusive): duration ago like 1h, or an RFC3339 time
      --user string             User / principal ID (principal_id)

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

### `aiplex usage prices`

```text
Without a configured price a record's cost is unknown unless quota settles it.
Prices are revisioned resources: replacing or deleting one needs its current
revision, so concurrent edits are never silently overwritten.

Usage:
  aiplex usage prices [flags]
  aiplex usage prices [command]

Examples:
  aiplex usage prices list
  aiplex usage prices set gpt-4o-mini --input-per-mtok 0.15 --output-per-mtok 0.60

Available Commands:
  list        List configured model prices
  get         Show one price and its current revision
  set         Set a model's price per million tokens (or -f for a full spec)
  delete      Delete a price (uses the current revision unless --revision is given)
  catalog     Look up list prices in the bundled catalogue and import the ones you choose

Flags:
  -h, --help   help for prices

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex usage prices [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex usage prices list`

```text
List configured model prices

Usage:
  aiplex usage prices list [flags]

Examples:
  aiplex usage prices list
  aiplex usage prices list -o wide

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

#### `aiplex usage prices get`

```text
Show one price and its current revision

Usage:
  aiplex usage prices get <id> [flags]

Examples:
  aiplex usage prices get mock-gpt
  aiplex usage prices get mock-gpt -o json

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

#### `aiplex usage prices set`

```text
Sets the price used to estimate cost for one model. Prices are given per
million tokens (or per request) as decimals and converted to the exact rational
form the API stores: no floating point, no rounding. With aiplex dev the mock
model already has a placeholder price.

When the price exists, only the rates you pass change (the revision is read for
you; a concurrent change fails with a conflict). Changing --scale or
--currency of an existing price needs every rate it should keep.

-f reads a full specification (id, model_id, currency, scale, meters, ...),
creating it or replacing the existing one. --revision pins the revision you
expect instead of reading it.

Usage:
  aiplex usage prices set <model> --input-per-mtok <price> --output-per-mtok <price> [--currency USD] [flags]

Examples:
  aiplex usage prices set gpt-4o-mini --input-per-mtok 0.15 --output-per-mtok 0.60
  aiplex usage prices set claude-sonnet-4 --input-per-mtok 3 --output-per-mtok 15 --cache-read-per-mtok 0.30 --currency USD
  aiplex usage prices set gpt-4o-mini --output-per-mtok 0.50
  aiplex usage prices set -f gemini-flash.json

Flags:
      --cache-read-per-mtok string    Price per million cached input tokens read
      --cache-write-per-mtok string   Price per million cache-write tokens
      --currency string               Three-letter currency code (default "USD")
  -f, --file string                   Full price specification (JSON or YAML)
  -h, --help                          help for set
      --input-per-mtok string         Price per million input tokens, e.g. 3 or 0.075
      --output-per-mtok string        Price per million output tokens
      --per-request string            Price per request
      --provider string               Only apply to records from this provider
      --reasoning-per-mtok string     Price per million reasoning tokens, charged on top of output (only for providers that report them separately)
      --revision uint                 Expected current revision (default: read it for you)
      --scale uint8                   Decimal places costs are computed to (6 = millionths) (default 6)

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

#### `aiplex usage prices delete`

```text
Delete a price (uses the current revision unless --revision is given)

Usage:
  aiplex usage prices delete <id> [flags]

Examples:
  aiplex usage prices delete gemini-flash --yes

Flags:
  -h, --help            help for delete
      --revision uint   Expected revision
  -y, --yes             Do not ask for confirmation (required when there is no terminal)

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

#### `aiplex usage prices catalog`

```text
The binary carries a pinned snapshot of a third-party price list (LiteLLM's
model_prices_and_context_window.json, MIT licence). Nothing is priced from it
automatically: a model stays unknown until you import its price, which creates
an ordinary price resource marked with its source and snapshot date. The cost
basis stays "estimated": these are list prices, not invoices.

Usage:
  aiplex usage prices catalog [flags]
  aiplex usage prices catalog [command]

Examples:
  aiplex usage prices catalog search claude --provider anthropic
  aiplex usage prices catalog show openai/gpt-4o-mini
  aiplex usage prices catalog import openai/gpt-4o-mini --dry-run

Available Commands:
  search      Search catalogue models by id
  show        Show one catalogue model and what importing it would write
  import      Create price resources from catalogue models (shows exactly what is written first)

Flags:
  -h, --help   help for catalog

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex usage prices catalog [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

##### `aiplex usage prices catalog search`

```text
Lists catalogue models whose <provider>/<model> contains every word of the
text (case-insensitive substring match only). Prices are USD per million tokens.
Providers: anthropic, azure-openai, bedrock, deepseek, gemini-developer, google, groq, mistral, openai, vertex-anthropic, xai.

Usage:
  aiplex usage prices catalog search <text> [--provider p] [flags]

Examples:
  aiplex usage prices catalog search gpt-4o
  aiplex usage prices catalog search claude --provider anthropic
  aiplex usage prices catalog search "gemini flash" -o json

Flags:
  -h, --help              help for search
      --limit int         Most rows to show (0 for all) (default 50)
      --provider string   Only this catalogue provider

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

##### `aiplex usage prices catalog show`

```text
Show one catalogue model and what importing it would write

Usage:
  aiplex usage prices catalog show <provider>/<model> [flags]

Examples:
  aiplex usage prices catalog show openai/gpt-4o-mini
  aiplex usage prices catalog show bedrock/anthropic.claude-sonnet-4-6 -o json

Flags:
  -h, --help   help for show

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

##### `aiplex usage prices catalog import`

```text
Creates ordinary model-price resources from catalogue entries, marked with
their source (catalogue, commit, snapshot date, source URL). It shows every meter
as an exact decimal before writing anything, and never overwrites an existing
price unless --replace is given. Cost from these prices has the basis "estimated".

Meters the catalogue does not state stay unset, so cost for usage of that kind
stays unknown. Fields an AIPlex price cannot express are listed. When one of
them changes what a request costs (context-length tiers, audio or image token
rates), the import is refused unless you pass --accept-partial. Reasoning tokens
are never priced from the catalogue; add --reasoning-included only when you know
the provider's output tokens already include them (OpenAI, Anthropic), otherwise
records that report reasoning tokens stay unknown cost.

--all-for-access-point resolves each model on the access point to its provider
and native model and imports those the catalogue lists; the rest are listed.

Usage:
  aiplex usage prices catalog import <provider>/<model>... [--all-for-access-point <ap>] [flags]

Examples:
  aiplex usage prices catalog import openai/gpt-4o-mini --dry-run
  aiplex usage prices catalog import anthropic/claude-haiku-4-5 --reasoning-included --yes
  aiplex usage prices catalog import --all-for-access-point team --yes
  aiplex usage prices catalog import gemini-developer/gemini-2.5-flash --accept-partial --replace

Flags:
      --accept-partial                Import base rates even when the model has pricing a price cannot express
      --all-for-access-point string   Import every model of this access point that the catalogue lists
      --currency string               Currency (the catalogue is USD only) (default "USD")
      --dry-run                       Show what would be written and write nothing
  -h, --help                          help for import
      --reasoning-included            Declare reasoning tokens as included in output tokens (free meter); only when the provider does
      --replace                       Replace an existing price with the same id (never done without this flag)
  -y, --yes                           Do not ask for confirmation (required when there is no terminal)

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


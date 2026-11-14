---
title: aiplex usage
sidebar_label: usage
description: "See what each request cost: records, totals and model prices"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Query the usage ledger: one record per managed request on any plane, by any
client or agent. Unknown usage or cost is shown as "unknown", never as zero,
and cost always carries its basis (settled, estimated or reported). Non-administrators
see only their own records.

Records have a source. "managed" records are requests that went through an
access point. "reported" records come from a harness's own telemetry
(Claude Code, Codex, Gemini CLI) sent to AIPlex over OTLP; their cost, when the
harness reported one, has the basis "reported". The two sources can describe the
same request, so a summary keeps them apart and never adds them silently.

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
  feedback    Rate a usage record (append-only), or show its feedback history
  prices      Manage model prices used to estimate cost (writes need the admin role)
  config      Show the deployment's usage settings

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
  aiplex usage records --client-request-id by-123   # exit 0 means the answer is complete; exit 1 means the scan stopped early

Flags:
      --access-point string        Access point ID (access_point_id)
      --agent string               Agent / actor ID (actor_id)
      --all                        Follow every page
      --batch string[="true"]      Provider batch records only (true) or excluding them (false)
      --bound string               Usage bound: lower_bound, ambiguous, partial or none (usage_bound)
      --budget string              Tenant budget ID: exchanges that drew from that budget
      --caller-kind string         Caller kind: agent, user or client (caller_kind)
      --client string              Client ID (client_id)
      --client-request-id string   The record of one call, by the caller's X-Client-Request-Id
      --cost-basis string          Cost basis: settled, estimated, reported, cached or unknown (cost_basis)
      --cursor string              Continue from a next_cursor value
      --error-class string         Error class (error_class)
      --harness string             Reported records from one harness: claude-code, codex or gemini-cli
  -h, --help                       help for records
      --hook-decision string       Request-hook decision: allow, deny, transform, error, skipped or none (hook_decision)
      --limit int                  Page size, 1 to 1000 (default 100)
      --model string               Model ID (model_id)
      --plane string               Plane: llmplex, mcplex or a2aplex
      --provider string            Provider
      --request-id string          The record of one call, by AIPlex's request id (the X-Aiplex-Request-Id response header)
      --since string               Start of window: duration like 24h, 7d, or an RFC3339 time
      --source string              Source: managed or reported
      --status string              Status: ok, error, denied or unknown
      --tag stringArray            Records carrying a tag, key=value (repeatable)
      --tool string                Tool name (tool_name)
      --trace-id string            Records of one trace, by W3C trace id (32 hexadecimal characters)
      --until string               End of window (exclusive): duration ago like 1h, or an RFC3339 time
      --user string                User / principal ID (principal_id)

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

Aliases:
  get, show, describe

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
access-point, caller-kind, budget, status, source, error-class, bound (usage_bound)
harness, batch or tag:<key> (a record tag, for example tag:experiment) (aliases: model=model_id, agent=actor_id,
user=principal_id, client=client_id, tool=tool_name, access-point=access_point_id,
caller-kind=caller_kind). Records whose usage is bounded (lower_bound, ambiguous
or partial) are included in the sums and counted in the BOUNDED column; their
totals are not final. Costs are never summed across currencies
or bases. When the scan limit is reached the totals cover only the newest
records and the command says so.

Managed and reported records are separate rows (a SOURCE column) unless you
filter with --source. Reported records that share a trace, client request id
or request id with a managed record are left out, because they describe the same
request; the command says how many. --merge-sources adds the two together and
says that it cannot rule out double counting. With -o wide the FEEDBACK column
shows the average score of records that have feedback and how many do not.

Usage:
  aiplex usage summary [flags]

Examples:
  aiplex usage summary --by model,agent --since 7d
  aiplex usage summary --by access-point,caller-kind --since 7d
  aiplex usage summary --by model --bucket day --since 30d -o json
  aiplex usage summary --by tag:experiment --since 7d -o wide
  aiplex usage summary --source reported --by harness,model --since 7d

Flags:
      --access-point string        Access point ID (access_point_id)
      --agent string               Agent / actor ID (actor_id)
      --batch string[="true"]      Provider batch records only (true) or excluding them (false)
      --bound string               Usage bound: lower_bound, ambiguous, partial or none (usage_bound)
      --bucket string              Time bucket: hour or day
      --budget string              Tenant budget ID: exchanges that drew from that budget
      --by strings                 Group by (comma separated): plane,provider,model,tool,agent,user,client,access-point,caller-kind,budget,status,source,error-class,bound,batch,harness,tag:<key>
      --caller-kind string         Caller kind: agent, user or client (caller_kind)
      --client string              Client ID (client_id)
      --client-request-id string   The record of one call, by the caller's X-Client-Request-Id
      --cost-basis string          Cost basis: settled, estimated, reported, cached or unknown (cost_basis)
      --error-class string         Error class (error_class)
      --harness string             Reported records from one harness: claude-code, codex or gemini-cli
  -h, --help                       help for summary
      --hook-decision string       Request-hook decision: allow, deny, transform, error, skipped or none (hook_decision)
      --merge-sources              Add managed and reported records together (may count a request twice)
      --model string               Model ID (model_id)
      --plane string               Plane: llmplex, mcplex or a2aplex
      --provider string            Provider
      --request-id string          The record of one call, by AIPlex's request id (the X-Aiplex-Request-Id response header)
      --since string               Start of window: duration like 24h, 7d, or an RFC3339 time
      --source string              Source: managed or reported
      --status string              Status: ok, error, denied or unknown
      --tag stringArray            Records carrying a tag, key=value (repeatable)
      --tool string                Tool name (tool_name)
      --trace-id string            Records of one trace, by W3C trace id (32 hexadecimal characters)
      --until string               End of window (exclusive): duration ago like 1h, or an RFC3339 time
      --user string                User / principal ID (principal_id)

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

### `aiplex usage feedback`

```text
Add feedback to a usage record you can see: your own, or any record if you are
an operator. --score is a number from -1 (worst) to 1 (best). Feedback is
append-only: the newest entry is the record's current feedback and earlier ones
stay in the history (--history). Free-text --comment is stored only when the
operator enabled comments (AIPLEX_FEEDBACK_COMMENTS); otherwise it is refused.
Summaries show feedback with -o wide.

Usage:
  aiplex usage feedback <record-id> [flags]

Examples:
  aiplex usage feedback ul_0123456789abcdef --score 1 --label helpful
  aiplex usage feedback ul_0123456789abcdef --score -1
  aiplex usage feedback ul_0123456789abcdef --history

Flags:
      --comment string   Free-text comment (only if the operator enabled comments)
  -h, --help             help for feedback
      --history          Show the feedback history instead of adding feedback
      --label string     Short label, lowercase (for example helpful)
      --score float      Score from -1 (worst) to 1 (best)

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
  catalog     Look up list prices in the bundled catalogues and import the ones you choose
  backfill    Price records that were written before a price existed (admin)

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

Aliases:
  list, ls

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

Aliases:
  get, show, describe

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

Aliases:
  delete, rm, remove

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
The binary carries pinned snapshots of two third-party price lists: LiteLLM's
model_prices_and_context_window.json (the default) and the models.dev model
database (both MIT licence). Nothing is priced from them automatically: a model
stays unknown until you import its price, which creates an ordinary price
resource marked with its source and snapshot date. The cost basis stays
"estimated": these are list prices, not invoices.

When the two sources state different values for the same model and meter, search
and show flag it, and an import needs --source to say which one to believe.

Usage:
  aiplex usage prices catalog [flags]
  aiplex usage prices catalog [command]

Examples:
  aiplex usage prices catalog search claude --provider anthropic
  aiplex usage prices catalog show openai/gpt-4o-mini
  aiplex usage prices catalog import openai/gpt-4o-mini --dry-run
  aiplex usage prices catalog import mistral/open-mistral-nemo --source models.dev --dry-run

Available Commands:
  search      Search catalogue models by id
  show        Show one catalogue model in each source and what importing it would write
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
text (case-insensitive substring match only), from both sources. Prices are USD
per million tokens, from LiteLLM when it lists the model, otherwise from
models.dev. A model both sources list with different values for the same meter
is flagged. Providers: anthropic, azure-openai, bedrock, deepseek, gemini-developer, google, groq, mistral, openai, vertex-anthropic, xai.

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
Shows the model as each bundled source states it, what importing it would
write, and every meter on which the two sources disagree.

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
their source (catalogue, commit, snapshot date, source URL). The default source
is litellm; --source models.dev imports from the models.dev snapshot instead.
When both sources list a model and state different values for the same meter,
the import is refused until --source names the one to use, and both values are
printed. It shows every meter
as an exact decimal before writing anything, and never overwrites an existing
price unless --replace is given. Cost from these prices has the basis "estimated".

Meters the catalogue does not state stay unset, so cost for usage of that kind
stays unknown. Fields an AIPlex price cannot express are listed. When one of
them changes what a request costs (context-length tiers whose billing rule is
not confirmed for the provider, token rates for modalities the ledger does not
hold), the import is refused unless you pass --accept-partial. Reasoning tokens
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
  aiplex usage prices catalog import mistral/open-mistral-nemo --source models.dev --yes

Flags:
      --accept-partial                Import base rates even when the model has pricing a price cannot express
      --all-for-access-point string   Import every model of this access point that the catalogue lists
      --currency string               Currency (the catalogue is USD only) (default "USD")
      --dry-run                       Show what would be written and write nothing
  -h, --help                          help for import
      --reasoning-included            Declare reasoning tokens as included in output tokens (free meter); only when the provider does
      --replace                       Replace an existing price with the same id (never done without this flag)
      --source string                 Catalogue source: litellm, models.dev (needed when the two disagree on a model) (default "litellm")
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

#### `aiplex usage prices backfill`

```text
Records written when no price covered them have an unknown cost. This computes it
now, for records whose cost is unknown and whose usage is final and fully known,
using the price effective at each record's own start time (a price created later
with an effective window covering the record applies; a price outside its window
never does).

The cost becomes "estimated" and carries backfilled_at (the record was unknown
when written) and the id of the price used. A settled or estimated cost is never
changed, nothing is overwritten, and running it twice changes nothing. The
original usage and every other field stay as they were. Records that no single
price can cost exactly stay unknown.

--dry-run reports counts and the total per currency without writing. Without it
the command shows the dry-run result first and asks before writing (or needs
--yes when there is no terminal). The work is done in bounded batches.

Usage:
  aiplex usage prices backfill [--since 7d] [--until 1h] [--model m] [--provider p] [--dry-run] [--yes] [flags]

Examples:
  aiplex usage prices backfill --dry-run
  aiplex usage prices backfill --since 30d --model gemini-2.5-flash --yes
  aiplex usage prices backfill --provider anthropic --until 2026-11-01T00:00:00Z --dry-run -o json

Flags:
      --dry-run           Report counts and totals and write nothing
  -h, --help              help for backfill
      --model string      Only this model ID (model_id)
      --provider string   Only this provider
      --since string      Start of window: duration like 24h, 7d, or an RFC3339 time
      --until string      End of window (exclusive): duration ago like 1h, or an RFC3339 time
  -y, --yes               Do not ask for confirmation (required when there is no terminal)

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

### `aiplex usage config`

```text
Show the usage settings the deployment serves: the trace link template,
whether feedback and feedback comments are accepted, and the tag keys a caller
may set. A template that is not configured is shown as "not set".

Usage:
  aiplex usage config [flags]

Examples:
  aiplex usage config
  aiplex usage config -o json

Flags:
  -h, --help   help for config

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


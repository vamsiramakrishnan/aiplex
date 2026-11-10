---
title: aiplex llm
sidebar_label: llm
description: "Connect model providers (surfaces), and inspect routes, batches and capacity"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Connect model providers (surfaces), and inspect routes, batches and capacity

Usage:
  aiplex llm [flags]
  aiplex llm [command]

Examples:
  aiplex llm surfaces list
  aiplex llm providers openai

Available Commands:
  routes      List LLM routing rules
  providers   List model providers and their operations (from the server's catalog)
  surfaces    Manage model surfaces (provider endpoints, operations, auth references)
  capacity    Show a surface's last observed provider limits and cooldown state (admin)
  batches     Provider batch jobs created through the gateway
  costs       Show model usage and cost totals from the usage ledger
  usage       List recorded model usage from the usage ledger

Flags:
  -h, --help   help for llm

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex llm [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex llm routes`

```text
List LLM routing rules

Usage:
  aiplex llm routes [flags]

Examples:
  aiplex llm routes

Flags:
  -h, --help   help for routes

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

### `aiplex llm providers`

```text
Shows each supported provider with its protocols, credential schemes and
operations, as the server defines them. Surfaces must list operations exactly as
this catalog does. With a provider id, lists that provider's operations.
--configured lists the legacy provider route configuration instead.

Usage:
  aiplex llm providers [provider] [flags]

Examples:
  aiplex llm providers
  aiplex llm providers bedrock

Flags:
      --configured   List the legacy provider route configuration instead of the catalog
  -h, --help         help for providers

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

### `aiplex llm surfaces`

```text
A model surface binds a provider (OpenAI, Anthropic, Azure OpenAI, Bedrock,
OpenAI-compatible, Gemini Developer) or a Google Cloud endpoint to the operations
and models callers may use. Credentials are references (a secret reference or an
identity reference), never values. Surfaces are revisioned: replace and delete
check the revision so concurrent edits are never silently overwritten.

Usage:
  aiplex llm surfaces [flags]
  aiplex llm surfaces [command]

Examples:
  aiplex llm surfaces list
  aiplex llm surfaces add openai --provider openai --models gpt-4o-mini --secret-ref openai-key

Available Commands:
  list        List model surfaces
  get         Show one surface and its revision (-o json or -o yaml for the full specification)
  create      Connect a model provider from flags (or use apply -f for a full spec)
  apply       Create a surface, or replace it with the file's content
  delete      Delete a surface (at its current revision unless --revision is given)

Flags:
  -h, --help   help for surfaces

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex llm surfaces [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex llm surfaces list`

```text
List model surfaces

Usage:
  aiplex llm surfaces list [flags]

Examples:
  aiplex llm surfaces list

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

#### `aiplex llm surfaces get`

```text
Show one surface and its revision (-o json or -o yaml for the full specification)

Usage:
  aiplex llm surfaces get <id> [flags]

Examples:
  aiplex llm surfaces get openai-prod
  aiplex llm surfaces get openai-prod -o yaml > openai-prod.yaml

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

#### `aiplex llm surfaces create`

```text
Creates a model surface: one provider endpoint, the models callers may use on
it, the operations (API calls) it allows and a secret REFERENCE for its key.

--provider fills in the endpoint, protocol, credential header and a sensible
set of operations from the server's provider catalog:
  openai             chat completions, responses, embeddings (bearer key)
  anthropic          messages, count tokens (x-api-key)
  gemini             generateContent, streamGenerateContent, countTokens (x-goog-api-key)
  openai-compatible  chat completions on --origin (Ollama, vLLM, LM Studio, ...)
Override them with --operations (IDs from: aiplex llm providers <provider>).

--secret-ref names the secret holding the provider key; the key itself is
never passed to the CLI. With aiplex dev, a reference NAME resolves from the
environment variable AIPLEX_TOOL_SECRET_<NAME> (upper case, '-' as '_') of
the shell that started aiplex dev, and the provider's origin must be allowed:
aiplex dev --allow-origin https://api.openai.com.

Then expose the models on an access point:
  aiplex access-points create team --model gpt-4o-mini=openai --allow-groups eng

Usage:
  aiplex llm surfaces create <id> --provider <provider> --models <a,b> [--secret-ref <name>] [flags]

Aliases:
  create, add

Examples:
  aiplex llm surfaces add openai --provider openai --models gpt-4o-mini,gpt-4o --secret-ref openai-key
  aiplex llm surfaces add claude --provider anthropic --models claude-sonnet-4-5 --secret-ref anthropic-key
  aiplex llm surfaces add local-ollama --provider openai-compatible --origin http://127.0.0.1:11434 --models llama3.2
  aiplex llm surfaces add openai --provider openai --models gpt-4o-mini --operations openai.v1.chat-completions --secret-ref openai-key --update

Flags:
  -h, --help                 help for create
      --models strings       Model names callers may use (comma separated)
      --operations strings   Operation IDs to allow instead of the defaults (see: aiplex llm providers <provider>)
      --origin string        Provider base URL (default: the provider's public API; required for openai-compatible)
      --provider string      Provider: openai, anthropic, gemini or openai-compatible
      --secret-ref string    Secret REFERENCE name holding the provider API key (never the key)
      --update               Replace the surface if it exists

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

#### `aiplex llm surfaces apply`

```text
Reads a surface specification (JSON or YAML) and writes it under surface.id.
When the surface exists it is replaced at the revision read just before the
write; pass --revision to require a specific revision instead. A stale revision
fails with a conflict and changes nothing. The server validates the whole
specification (provider block, operations, websocket and capacity_policy).
Operations may be listed by ID (for example "openai.v1.chat-completions"); the
CLI fills each one in from the server's catalog (aiplex llm providers <id>).
"-f -" reads standard input.

Usage:
  aiplex llm surfaces apply -f <surface.yaml|json> [flags]

Examples:
  aiplex llm surfaces apply -f openai-prod.yaml
  aiplex llm surfaces apply -f openai-prod.yaml --revision 3

Flags:
  -f, --file string     Surface specification file (JSON or YAML)
  -h, --help            help for apply
      --revision uint   Require this current revision (0 requires that the surface does not exist)

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

#### `aiplex llm surfaces delete`

```text
Delete a surface (at its current revision unless --revision is given)

Usage:
  aiplex llm surfaces delete <id> [flags]

Aliases:
  delete, remove, rm

Examples:
  aiplex llm surfaces delete openai-prod --yes

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

### `aiplex llm capacity`

```text
Shows the last rate-limit observation the server that answered made for this
surface, and the capacity policy and cooldown phase. The observation is advisory
and local to one server replica; the policy is enforced per surface and per
replica. Values the provider did not report are shown as "unknown".

Usage:
  aiplex llm capacity <surface> [flags]

Examples:
  aiplex llm capacity openai-prod

Flags:
  -h, --help   help for capacity

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

### `aiplex llm batches`

```text
Provider batch jobs created through the gateway

Usage:
  aiplex llm batches [flags]
  aiplex llm batches [command]

Examples:
  aiplex llm batches list

Available Commands:
  list        List batches (admin)

Flags:
  -h, --help   help for batches

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex llm batches [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex llm batches list`

```text
Lists provider batches the gateway created. An OPEN batch holds its admitted
quota exposure and a concurrency slot until its results are fetched through
AIPlex; its usage stays unknown until then.

Usage:
  aiplex llm batches list [flags]

Examples:
  aiplex llm batches list --state open

Flags:
      --all              Follow every page
      --cursor string    Continue from a next_cursor value
  -h, --help             help for list
      --limit int        Page size, 1 to 500 (default 100)
      --state string     open or settled (default all)
      --surface string   Only this surface

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

### `aiplex llm costs`

```text
Totals of model requests, tokens and cost over a period, read from the usage
ledger (the same data as "aiplex usage summary --plane llm"). A cost or token
count the ledger does not have is shown as "unknown", never as zero, and the
output says how many records it excludes.

Usage:
  aiplex llm costs [flags]

Examples:
  aiplex llm costs --period week

Flags:
      --agent string    Filter by agent ID
  -h, --help            help for costs
      --model string    Filter by model ID
  -p, --period string   Time period: day, week, month (default "day")

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

### `aiplex llm usage`

```text
Model requests from the usage ledger, newest first (the same data as
"aiplex usage records --plane llm"). Costs and token counts the ledger does not
have are shown as "unknown".

Usage:
  aiplex llm usage [flags]

Examples:
  aiplex llm usage --since 24h --limit 20

Flags:
      --agent string   Filter by agent ID
  -h, --help           help for usage
      --limit int      Maximum records (1–1000) (default 100)
      --model string   Filter by model ID
      --since string   Start of window: duration like 24h, 7d, or an RFC3339 time

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


---
title: aiplex llm
sidebar_label: llm
description: "LLMPlex — manage model routes, providers, and costs"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
LLMPlex — manage model routes, providers, and costs

Usage:
  aiplex llm [command]

Available Commands:
  batches     Provider batch jobs created through the gateway
  capacity    Show a surface's last observed provider limits and cooldown state (admin)
  costs       Show LLM usage and cost summary
  providers   List model providers and their operations (from the server's catalog)
  routes      List LLM routing rules
  surfaces    Manage model surfaces (provider endpoints, operations, auth references)
  usage       List recorded model usage

Flags:
  -h, --help   help for llm

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex llm [command] --help" for more information about a command.
```

### `aiplex llm batches`

```text
Provider batch jobs created through the gateway

Usage:
  aiplex llm batches [command]

Available Commands:
  list        List batches (admin)

Flags:
  -h, --help   help for batches

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex llm batches [command] --help" for more information about a command.
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
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
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
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex llm costs`

```text
Show LLM usage and cost summary

Usage:
  aiplex llm costs [flags]

Flags:
      --agent string    Filter by agent ID
  -h, --help            help for costs
      --model string    Filter by model ID
  -p, --period string   Time period: day, week, month (default "day")

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
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
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex llm routes`

```text
List LLM routing rules

Usage:
  aiplex llm routes [flags]

Flags:
  -h, --help   help for routes

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex llm surfaces`

```text
A model surface binds a provider (OpenAI, Anthropic, Azure OpenAI, Bedrock,
OpenAI-compatible, Gemini Developer) or a Google Cloud endpoint to the operations
and models callers may use. Credentials are references (a secret reference or an
identity reference), never values. Surfaces are revisioned: replace and delete
check the revision so concurrent edits are never silently overwritten.

Usage:
  aiplex llm surfaces [command]

Available Commands:
  apply       Create a surface, or replace it with the file's content
  delete      Delete a surface (at its current revision unless --revision is given)
  get         Show one surface and its revision
  list        List model surfaces

Flags:
  -h, --help   help for surfaces

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex llm surfaces [command] --help" for more information about a command.
```

#### `aiplex llm surfaces apply`

```text
Reads a surface specification (JSON or YAML) and writes it under surface.id.
When the surface exists it is replaced at the revision read just before the
write; pass --revision to require a specific revision instead. A stale revision
fails with a conflict and changes nothing. The server validates the whole
specification (provider block, operations, websocket and capacity_policy).
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
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex llm surfaces delete`

```text
Delete a surface (at its current revision unless --revision is given)

Usage:
  aiplex llm surfaces delete <id> [flags]

Examples:
  aiplex llm surfaces delete openai-prod --revision 4

Flags:
  -h, --help            help for delete
      --revision uint   Expected revision

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex llm surfaces get`

```text
Show one surface and its revision

Usage:
  aiplex llm surfaces get <id> [flags]

Examples:
  aiplex llm surfaces get openai-prod -o yaml

Flags:
  -h, --help   help for get

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
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
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex llm usage`

```text
List recorded model usage

Usage:
  aiplex llm usage [flags]

Flags:
      --agent string   Filter by agent ID
  -h, --help           help for usage
      --limit int      Maximum records (1–1000) (default 100)
      --model string   Filter by model ID
      --since string   RFC3339 start timestamp

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


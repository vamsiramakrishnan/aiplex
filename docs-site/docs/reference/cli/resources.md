---
title: aiplex resources
sidebar_label: resources
description: "Low-level access to any resource kind (prefer the guided commands)"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Read and write any connected module resource by kind and id. Kinds:
model-surface, tool-connection, agent-connection, agent-binding,
network-profile, client-destination, deployment-intent.

Prefer the guided commands where they exist: aiplex llm surfaces,
aiplex mcp connections, aiplex access-points. Revisions are handled for you:
apply reads the current revision and writes on top of it, so a concurrent
change fails with a conflict instead of being overwritten.

Usage:
  aiplex resources [flags]
  aiplex resources [command]

Examples:
  aiplex resources list tool-connection
  aiplex resources apply tool-connection docs -f docs.json

Available Commands:
  list        List resources of a kind (JSON or YAML)
  get         Show one resource and its revision
  apply       Create or replace a resource from a JSON or YAML file
  delete      Delete a resource (its current revision is read for you)
  plan        Show the deployment plan for a resource

Flags:
  -h, --help   help for resources

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex resources [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex resources list`

```text
List resources of a kind (JSON or YAML)

Usage:
  aiplex resources list <kind> [flags]

Examples:
  aiplex resources list tool-connection

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

### `aiplex resources get`

```text
Show one resource and its revision

Usage:
  aiplex resources get <kind> <id> [flags]

Examples:
  aiplex resources get tool-connection local-tools -o yaml

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

### `aiplex resources apply`

```text
Creates the resource, or replaces it when it exists: the current revision is
read for you. --revision pins the revision you expect (0 = must not exist).

The legacy form "resources put <kind> <id> <file> <revision>" still works.

Usage:
  aiplex resources apply <kind> <id> -f <file> [flags]

Aliases:
  apply, put

Examples:
  aiplex resources apply tool-connection docs -f docs.json
  aiplex resources apply model-surface openai -f openai.yaml --revision 3

Flags:
  -f, --file string     JSON or YAML specification, or - for stdin
  -h, --help            help for apply
      --revision uint   Expected current revision (default: read it; 0 = must not exist)

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

### `aiplex resources delete`

```text
Delete a resource (its current revision is read for you)

Usage:
  aiplex resources delete <kind> <id> [flags]

Examples:
  aiplex resources delete tool-connection docs --yes

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

### `aiplex resources plan`

```text
Show the deployment plan for a resource

Usage:
  aiplex resources plan <kind> <id> [flags]

Examples:
  aiplex resources plan deployment-intent web --idempotency-key web-v1

Flags:
  -h, --help                     help for plan
      --idempotency-key string   Stable deployment planning key (required for deployment-intent)

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


---
title: aiplex mcp
sidebar_label: mcp
description: "Connect MCP tool servers and test them through the gateway"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
An MCP tool connection pins one MCP server URL, the credential the gateway
uses to call it (a secret reference, never a value) and which of its tools
callers may use. Expose it to callers by adding it to an access point:

  aiplex mcp connections add search --url https://mcp.example.com/mcp --auth bearer --secret-ref search-token
  aiplex mcp connections test search
  aiplex access-points create team --tool search --allow-groups eng

Usage:
  aiplex mcp [flags]
  aiplex mcp [command]

Examples:
  aiplex mcp connections list
  aiplex mcp connections add docs --url http://127.0.0.1:3000/mcp

Available Commands:
  connections Add, list, inspect, test and delete MCP tool connections

Flags:
  -h, --help   help for mcp

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex mcp [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex mcp connections`

```text
Add, list, inspect, test and delete MCP tool connections

Usage:
  aiplex mcp connections [flags]
  aiplex mcp connections [command]

Aliases:
  connections, connection, conn

Examples:
  aiplex mcp connections list
  aiplex mcp connections test local-tools

Available Commands:
  list        List MCP tool connections
  get         Show one MCP tool connection
  create      Add an MCP tool connection from flags (or -f for a full spec)
  delete      Delete an MCP tool connection
  test        Send a real tools/list through the gateway and print the tools

Flags:
  -h, --help   help for connections

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex mcp connections [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex mcp connections list`

```text
List MCP tool connections

Usage:
  aiplex mcp connections list [flags]

Aliases:
  list, ls

Examples:
  aiplex mcp connections list
  aiplex mcp connections list -o json

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

#### `aiplex mcp connections get`

```text
Show one MCP tool connection

Usage:
  aiplex mcp connections get <id> [flags]

Aliases:
  get, show, describe

Examples:
  aiplex mcp connections get local-tools
  aiplex mcp connections get local-tools -o yaml

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

#### `aiplex mcp connections create`

```text
Adds a tool connection that the gateway uses to call one MCP server.

Credentials are secret REFERENCES (--secret-ref, --client-secret-ref): names in
the tenant secret namespace, never secret values. With aiplex dev, a reference
NAME resolves from the environment variable AIPLEX_TOOL_SECRET_<NAME> of the
local API process.

--tools lists the tools callers may use. Without it, the tools are discovered
by calling tools/list on the server from this machine (only for --auth none;
pass --tools for a server that needs a credential).

After the connection is written, a tools/list is sent through the gateway to
check it works (skip with --no-test). With --update an existing connection is
replaced (the revision is read for you); without it the id must be new.

Usage:
  aiplex mcp connections create <id> --url <mcp-url> [--auth none|api-key|bearer|basic|oauth-client-credentials] [flags]

Aliases:
  create, add

Examples:
  aiplex mcp connections add docs --url http://127.0.0.1:3000/mcp
  aiplex mcp connections add search --url https://mcp.example.com/mcp --auth bearer --secret-ref search-token --tools search,fetch
  aiplex mcp connections add crm --url https://crm.example.com/mcp --auth api-key --header-name X-Api-Key --secret-ref crm-key --tools lookup
  aiplex mcp connections add files --url https://files.example.com/mcp --auth oauth-client-credentials --token-endpoint https://idp.example.com/oauth2/token --client-id svc-files --client-secret-ref files-secret --scope files.read --tools list_files
  aiplex mcp connections add search -f search.yaml

Flags:
      --allow strings               Tools policy: only these tools or prefixes ending in '*'
      --auth string                 How the gateway authenticates to the server: none, api-key, bearer, basic or oauth-client-credentials (default "none")
      --auth-in string              api-key: header or query (default "header")
      --auth-prefix string          api-key: scheme written before the key, e.g. Token
      --client-auth-method string   oauth-client-credentials: client_secret_basic or client_secret_post (default "client_secret_basic")
      --client-id string            oauth-client-credentials: client ID
      --client-secret-ref string    oauth-client-credentials: client secret REFERENCE name
      --deny strings                Tools policy: never these tools or prefixes ending in '*'
  -f, --file string                 Full tool-connection specification (JSON or YAML; '-' for stdin) instead of flags
      --header-name string          api-key: header (or query parameter) that carries the key (default "X-Api-Key")
  -h, --help                        help for create
      --mode string                 Server transport: streamable-http or json (default "streamable-http")
      --no-test                     Do not run tools/list through the gateway after writing
      --protocol-version string     MCP protocol version to send (default "2025-06-18")
      --read-only                   Tools policy: only tools annotated readOnlyHint=true
      --scope strings               oauth-client-credentials: scope to request (repeatable)
      --secret-ref string           Secret REFERENCE name holding the API key, bearer token or password (never the value)
      --token-endpoint string       oauth-client-credentials: token endpoint URL
      --tools strings               Tools callers may use (comma separated); default: discovered from the server
      --update                      Replace the connection if it exists
      --url string                  MCP server URL (streamable HTTP endpoint, e.g. https://host/mcp)
      --username string             basic: username

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex mcp connections delete`

```text
Delete an MCP tool connection

Usage:
  aiplex mcp connections delete <id> [flags]

Aliases:
  delete, remove, rm

Examples:
  aiplex mcp connections delete docs --yes

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

#### `aiplex mcp connections test`

```text
Calls tools/list on the connection's server through the gateway, with the
connection's credential and tools policy applied, and prints the tools callers
can see. A failure here is what a client would hit.

Usage:
  aiplex mcp connections test <id> [flags]

Examples:
  aiplex mcp connections test local-tools
  aiplex mcp connections test local-tools -o json

Flags:
  -h, --help   help for test

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


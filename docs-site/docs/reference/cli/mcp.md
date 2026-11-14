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
  registry    Search the MCP Registry and import a remote server as a tool connection
  serve       Run a read-only MCP server on stdio that lets a coding agent inspect AIPlex

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
  call        Call one tool through the gateway and print the result
  rules       Policy rules on a connection's tool calls

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

#### `aiplex mcp connections call`

```text
Sends one tools/call through the gateway with the connection's credential and
policy rules applied, and the caller's mcp:tools:<tool> grant checked, exactly
as a client's call would be. Text content prints as text; other content prints
as its type. -o json prints the raw result.

Arguments are a JSON (or YAML) object from --args, or from --args-file (use -
for stdin). A policy denial exits 3 and names the rule. A tool that ran and
reported a failure prints its text and exits 1, as does a tool server error.

Calls are real: a tool with side effects runs.

Usage:
  aiplex mcp connections call <id> <tool> [--args JSON | --args-file <file>|-] [flags]

Examples:
  aiplex mcp connections call local-tools echo --args '{"text":"hi"}'
  aiplex mcp connections call search query --args-file query.json -o json
  echo '{"a":1,"b":2}' | aiplex mcp connections call local-tools add --args-file -

Flags:
      --args string        Tool arguments as a JSON object
      --args-file string   Read the tool arguments from a JSON or YAML file ('-' for stdin)
  -h, --help               help for call

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

#### `aiplex mcp connections rules`

```text
Rules decide allow or deny on a tool call from structured facts: the tool and
its annotations, the call's arguments, the caller and the request time. They
are CEL boolean expressions, type-checked when saved.

Order: the connection's tools allow and deny lists, the caller's mcp:tools
scope and read_only are checked first. Then the rules run in order. The first
matching deny refuses. If the connection has any allow rule, a call that
matches none of them is refused. A rule that cannot be evaluated refuses the
call (tool_policy_error). Rules never change a call's content.

Variables: tool.name, tool.connection, tool.annotations (read_only,
destructive, idempotent, open_world; absent when unknown), args, caller.kind,
caller.principal, caller.groups (absent unless caller.groups_known),
caller.agent, caller.client, access_point.id, access_point.labels,
request.time. Helpers: path_under, url_host, any_in, lower, matches (literal
RE2 pattern), startsWith, endsWith.

Usage:
  aiplex mcp connections rules [flags]
  aiplex mcp connections rules [command]

Examples:
  aiplex mcp connections rules list search
  aiplex mcp connections rules add search --id no-etc --effect deny --when "path_under(args.path, '/etc')" --message "files under /etc are off limits"
  aiplex mcp connections rules test search --tool read_file --args '{"path":"/etc/hosts"}' --group eng

Available Commands:
  list        List a connection's policy rules in evaluation order
  add         Add a policy rule (appended; use --before to place it earlier)
  remove      Remove a policy rule
  set         Replace all of a connection's rules from a JSON or YAML file
  test        Dry-run the rules against a sample call (nothing is stored)

Flags:
  -h, --help   help for rules

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex mcp connections rules [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

##### `aiplex mcp connections rules list`

```text
List a connection's policy rules in evaluation order

Usage:
  aiplex mcp connections rules list <connection> [flags]

Aliases:
  list, ls

Examples:
  aiplex mcp connections rules list search
  aiplex mcp connections rules list search -o yaml

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

##### `aiplex mcp connections rules add`

```text
Adds a rule to the connection's tools policy. The expression is compiled and
type-checked by the gateway when the connection is saved; an expression that
does not check is rejected and nothing changes. Test candidates first with:

  aiplex mcp connections rules test search --tool read_file --args '{"path":"/tmp/a"}'

Usage:
  aiplex mcp connections rules add <connection> --id <rule-id> --effect deny|allow --when <expression> [flags]

Examples:
  aiplex mcp connections rules add search --id no-etc --effect deny --when "path_under(args.path, '/etc')" --message "files under /etc are off limits"
  aiplex mcp connections rules add search --id eng-only --effect allow --when "caller.groups_known && 'eng' in caller.groups"
  aiplex mcp connections rules add search --id no-force --effect deny --when "has(args.force) && args.force == true" --before eng-only

Flags:
      --before string        Insert before this rule id instead of appending
      --description string   What the rule is for (operators only)
      --effect string        deny or allow
  -h, --help                 help for add
      --id string            Rule id: lower-case letters, digits, '-' or '_'
      --message string       Message shown to the caller when the rule refuses a call (safe to publish)
      --replace              Replace a rule with the same id
      --when string          CEL boolean expression

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

##### `aiplex mcp connections rules remove`

```text
Remove a policy rule

Usage:
  aiplex mcp connections rules remove <connection> <rule-id> [flags]

Aliases:
  remove, rm, delete

Examples:
  aiplex mcp connections rules remove search no-etc --yes

Flags:
  -h, --help   help for remove
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

##### `aiplex mcp connections rules set`

```text
Replaces the whole rules list from a file: a list of rules, or an object with a
"rules" member. Replacing removes rules that are not in the file, so it asks
for confirmation (--yes without a terminal). An empty list removes every rule.

Usage:
  aiplex mcp connections rules set <connection> -f <file> [flags]

Examples:
  aiplex mcp connections rules set search -f rules.yaml --yes
  aiplex mcp connections rules list search -o yaml > rules.yaml

Flags:
  -f, --file string   Rules as JSON or YAML ('-' for stdin)
  -h, --help          help for set
  -y, --yes           Do not ask for confirmation (required when there is no terminal)

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

##### `aiplex mcp connections rules test`

```text
Evaluates the connection's rules against a sample call and prints each rule's
result (matched, not_matched, error, not_evaluated) and the decision. Only
rules are evaluated: the tool name lists, the caller's scope, read_only, quota
and request hooks are not part of this test. The sample is not stored or
recorded, and arguments are not logged. Operators only.

--rules-file tries candidate rules in place of the saved ones, without saving.
Without --group the subject's group membership is unknown (caller.groups_known
is false); pass --group for each group, or --groups-known=false to be explicit.

Usage:
  aiplex mcp connections rules test <connection> --tool <name> [--args JSON] [--group g ...] [flags]

Examples:
  aiplex mcp connections rules test search --tool read_file --args '{"path":"/etc/hosts"}' --group eng
  aiplex mcp connections rules test search --tool delete --annotation destructive=true --kind agent
  aiplex mcp connections rules test search --tool read_file --args-file call.json --rules-file candidate.yaml

Flags:
      --access-point string   Also apply this access point's rules for the connection
      --agent string          Caller agent id
      --annotation strings    Simulated tool annotation, name=true|false (read_only, destructive, idempotent, open_world; repeatable)
      --args string           Sample arguments, a JSON object
      --args-file string      Sample arguments from a JSON or YAML file ('-' for stdin)
      --client string         Caller client registration id
      --group strings         Caller group (repeatable); sets caller.groups_known
      --groups-unknown        The caller's group membership is unknown
  -h, --help                  help for test
      --kind string           Caller kind: user, agent or client
      --principal string      Caller principal id
      --rules-file string     Candidate rules (JSON or YAML) used instead of the saved ones
      --time string           request.time as RFC 3339 (default: now)
      --tool string           Tool name of the sample call

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

### `aiplex mcp registry`

```text
Reads the official MCP Registry (https://registry.modelcontextprotocol.io) or
a private registry that implements its API, and imports a server's remote
endpoint as an ordinary tool connection.

The registry is fetched by this command, from this machine. The AIPlex API
never contacts a registry, so the registry host must be reachable from here,
not from the API. Set another registry with --registry or AIPLEX_MCP_REGISTRY.

Registry content is untrusted input. It is validated strictly, redirects are
not followed, and nothing it says is approved for you: the server's origin
must still be approved by the API's network policy, secrets are references you
create, and the imported connection starts read-only.

Only remote servers using Streamable HTTP can be imported. Servers that are
local packages (npm, pypi, oci, nuget, mcpb) run on the user's machine; the
gateway cannot run them, so they are listed as not importable.

Usage:
  aiplex mcp registry [flags]
  aiplex mcp registry [command]

Examples:
  aiplex mcp registry search github
  aiplex mcp registry show ai.adside/adside
  aiplex mcp registry import ai.adside/adside --allow-tools list_campaigns --dry-run

Available Commands:
  search      Search the registry by server name
  show        Show a registry server: remotes, packages and what is importable
  import      Create a tool connection from a registry server's remote endpoint

Flags:
  -h, --help              help for registry
      --registry string   Registry base URL (default https://registry.modelcontextprotocol.io; or AIPLEX_MCP_REGISTRY)

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex mcp registry [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex mcp registry search`

```text
Lists the latest version of registry servers whose name contains the text.
IMPORT says what AIPlex can do with the entry: "remote" can be imported;
local packages and legacy SSE servers cannot.

Usage:
  aiplex mcp registry search <text> [flags]

Examples:
  aiplex mcp registry search github --limit 10
  aiplex mcp registry search '' --limit 5 -o json

Flags:
      --cursor string   Continue from a cursor printed by an earlier search
  -h, --help            help for search
      --limit int       Number of results (1 to 100) (default 20)

Global Flags:
      --context string    Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug             Show debug output, including raw API errors
      --no-color          Disable colour (also: NO_COLOR=1)
  -o, --output string     Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --registry string   Registry base URL (default https://registry.modelcontextprotocol.io; or AIPLEX_MCP_REGISTRY)
      --token string      Bearer token (env: AIPLEX_TOKEN)
      --url string        AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex mcp registry show`

```text
Prints a server.json: its remotes with the headers and variables they declare,
and its packages. Remotes using Streamable HTTP are importable; everything
else is marked "not importable" with the reason. The registry's text is
printed with control characters removed.

Usage:
  aiplex mcp registry show <name>[@version] [flags]

Examples:
  aiplex mcp registry show ai.adside/adside
  aiplex mcp registry show ai.adside/adside@1.0.0 -o json

Flags:
  -h, --help   help for show

Global Flags:
      --context string    Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug             Show debug output, including raw API errors
      --no-color          Disable colour (also: NO_COLOR=1)
  -o, --output string     Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --registry string   Registry base URL (default https://registry.modelcontextprotocol.io; or AIPLEX_MCP_REGISTRY)
      --token string      Bearer token (env: AIPLEX_TOKEN)
      --url string        AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex mcp registry import`

```text
Creates an ordinary tool connection through the API from a server's remote
endpoint (Streamable HTTP only; local packages cannot be imported).

What is proposed from the registry entry, and what is not:
  URL        the remote's URL, with its variables filled from --var, fixed values
             or defaults (values are checked for the part of the URL they land in)
  Auth       a proposal mapped from the declared headers: a secret Authorization
             header becomes bearer, another secret header becomes api_key. Secrets
             are REFERENCES you create; the command prints which. Fixed header
             values and headers that are not secret are not imported and are listed.
             --auth none drops the declared credential
  Provenance registry, name, version, retrieval time and the SHA-256 of the
             server.json, stored on the connection

What it does not do: it does not approve the server's origin (the API's network
policy decides which origins it calls; the command prints the origin and how to
approve it), does not create secrets, and does not expose the connection on any
access point.

The registry does not list a server's tools. Choose them with --allow-tools, or
with --probe, which sends tools/list to the server from this machine without a
credential (only for servers that need none) and shows what it found. The
connection is saved read-only: only tools annotated readOnlyHint=true can be
called, after the tools are listed through the gateway once. --read-write
removes that restriction.

Use --dry-run to see everything above without creating anything.

Usage:
  aiplex mcp registry import <name>[@version] [--id <connection>] --allow-tools <tools> | --probe [flags]

Examples:
  aiplex mcp registry import ai.adside/adside --allow-tools list_campaigns,get_report --dry-run
  aiplex mcp registry import ai.adside/adside --id adside --allow-tools list_campaigns --secret-ref adside-token --yes
  aiplex mcp registry import ai.biel/biel-ai --var project_slug=my-docs --probe

Flags:
      --allow-tools strings   Tools the connection may call (comma separated)
      --auth string           auto (map the declared credential) or none (send no credential) (default "auto")
      --dry-run               Show the proposed connection, origin and secrets; create nothing
  -h, --help                  help for import
      --id string             Connection id (default: derived from the server name)
      --probe                 Discover the tools by sending tools/list to the server from this machine (no credential is sent)
      --read-write            Do not restrict the connection to tools annotated readOnlyHint=true
      --remote int            Which remote of the entry to import (index from 'registry show'; default: the first importable) (default -1)
      --secret-ref string     Secret REFERENCE name for the credential (never the value); default: proposed
      --update                Replace the connection if it exists
      --var stringArray       URL template variable, name=value (repeatable)
  -y, --yes                   Do not ask for confirmation (required when there is no terminal)

Global Flags:
      --context string    Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug             Show debug output, including raw API errors
      --no-color          Disable colour (also: NO_COLOR=1)
  -o, --output string     Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --registry string   Registry base URL (default https://registry.modelcontextprotocol.io; or AIPLEX_MCP_REGISTRY)
      --token string      Bearer token (env: AIPLEX_TOKEN)
      --url string        AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex mcp serve`

```text
Serves the Model Context Protocol on stdin and stdout. Point an MCP client
(Claude Code, Codex, Gemini CLI) at the command "aiplex mcp serve".

Tools, all read-only and made with your own token and context:
  whoami                who AIPlex sees you as, with roles and scopes
  list_access_points    the access points you can use
  get_connect_config    what "aiplex connect" prints, for an access point and client
  list_models           the models an access point offers
  usage_summary         totals of your own usage
  usage_records         a bounded page of your own usage records

Nothing is changed through this server. Errors carry the API's message; the
token is never included. Diagnostics go to stderr, never to stdout.

Usage:
  aiplex mcp serve [flags]

Examples:
  claude mcp add aiplex -- aiplex mcp serve
  aiplex --context prod mcp serve

Flags:
  -h, --help   help for serve

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


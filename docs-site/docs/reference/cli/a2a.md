---
title: aiplex a2a
sidebar_label: a2a
description: "Discover agents and delegate tasks between them (A2A)"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
A2APlex reaches agents two ways. Deployed A2A agents are listed with 'agents'
and 'card'. Cloud agents (Google, AWS, Foundry, Copilot Studio) are reached
through an agent connection, which pins the agent's endpoint and the identity
reference used to call it:

  aiplex resources apply agent-connection bedrock -f connection.yaml
  aiplex a2a connections list
  aiplex a2a invoke bedrock --operation invoke --payload '{"input":"hello"}'

Delegations are reported history: a record says an agent reported handing a task
to another, not that the call was authorized or that the reporter is who it says.

Usage:
  aiplex a2a [flags]
  aiplex a2a [command]

Examples:
  aiplex a2a agents
  aiplex a2a connections list
  aiplex a2a delegations list --agent research-agent

Available Commands:
  agents      List running A2A agents
  delegate    Record a new agent-to-agent delegation
  card        Get the Agent Card for a deployed A2A agent
  invoke      Invoke the cloud agent behind an agent connection
  delegations List, read and update the delegation history
  connections List, inspect and delete agent connections

Flags:
  -h, --help   help for a2a

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex a2a [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex a2a agents`

```text
List running A2A agents

Usage:
  aiplex a2a agents [flags]

Examples:
  aiplex a2a agents

Flags:
  -h, --help   help for agents

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

### `aiplex a2a delegate`

```text
Record a new agent-to-agent delegation

Usage:
  aiplex a2a delegate [flags]

Examples:
  aiplex a2a delegate --caller tutor-agent --callee research-agent --task research

Flags:
      --callee string   Callee agent ID (required)
      --caller string   Caller agent ID (required)
  -h, --help            help for delegate
  -t, --task string     Task type (required)
  -u, --user string     User ID

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

### `aiplex a2a card`

```text
Get the Agent Card for a deployed A2A agent

Usage:
  aiplex a2a card <instance-id> [flags]

Examples:
  aiplex a2a card research-agent

Flags:
  -h, --help   help for card

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

### `aiplex a2a invoke`

```text
Sends one invocation through the gateway to the agent an agent connection
points at, with the connection's identity reference. The reply is the provider's
own and is written to stdout as it arrives, so a streamed (server-sent events)
reply appears incrementally. The payload is sent as given and is not logged.

The operation is the provider's: 'invoke' for Google Agent Runtime and AWS
AgentCore Runtime, 'agent-responses' for Foundry agents, 'start-conversation'
and others for Copilot Studio. The caller needs the grant
a2a:agent:<connection>:<operation>; without it the call is refused (exit 3).

With -o json (or yaml) the whole reply is read first and printed as one object
with the HTTP status, content type, request id and body.

Usage:
  aiplex a2a invoke <connection> --operation <operation> [--payload '<json>' | --payload-file <file>|-] [flags]

Examples:
  aiplex a2a invoke bedrock --operation invoke --payload '{"input":"hello"}'
  aiplex a2a invoke vertex --operation invoke --session s-1 --payload-file request.json
  echo '{"input":"hi"}' | aiplex a2a invoke bedrock --operation invoke --payload-file - -o json

Flags:
      --accept string         Reply type to ask the provider for
      --content-type string   Content type of the payload, when the provider needs one
  -h, --help                  help for invoke
      --operation string      The provider's operation, such as invoke (required)
      --payload string        JSON payload, sent as given
      --payload-file string   Read the JSON payload from a file, or - for stdin
      --qualifier string      Provider qualifier, such as an AgentCore runtime version
      --session string        Provider session ID to continue
      --watermark string      Provider watermark to resume a conversation from

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

### `aiplex a2a delegations`

```text
Delegations are reported history. Delegations are reported history, not proof: a record does not show that the call was authorized or that the reporting agent is who it says.

Usage:
  aiplex a2a delegations [flags]
  aiplex a2a delegations [command]

Aliases:
  delegations, delegation, del

Examples:
  aiplex a2a delegations list --agent research-agent --limit 20
  aiplex a2a delegations chain d-1

Available Commands:
  list        List recent delegations, optionally for one agent
  get         Show one delegation
  chain       Show a delegation and every delegation made under it
  update      Move a reported delegation to a new status

Flags:
  -h, --help   help for delegations

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex a2a delegations [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex a2a delegations list`

```text
List recent delegations, optionally for one agent

Usage:
  aiplex a2a delegations list [--agent <id>] [--limit N] [flags]

Aliases:
  list, ls

Examples:
  aiplex a2a delegations list
  aiplex a2a delegations list --agent research-agent --limit 20 -o json

Flags:
      --agent string   Only delegations where this agent is the caller or callee
  -h, --help           help for list
      --limit int      How many to list (1-1000; the API's default is 50)

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

#### `aiplex a2a delegations get`

```text
Show one delegation

Usage:
  aiplex a2a delegations get <id> [flags]

Aliases:
  get, show

Examples:
  aiplex a2a delegations get d-1
  aiplex a2a delegations get d-1 -o yaml

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

#### `aiplex a2a delegations chain`

```text
Shows the delegation and all its descendants, parents before children. The
API refuses a chain it cannot return whole (more than 10,000 spans, or a loop)
instead of returning part of it. Delegations are reported history, not proof: a record does not show that the call was authorized or that the reporting agent is who it says.

Usage:
  aiplex a2a delegations chain <id> [flags]

Examples:
  aiplex a2a delegations chain d-1 -o json

Flags:
  -h, --help   help for chain

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

#### `aiplex a2a delegations update`

```text
Sets the status of a delegation record. Pending or running records may move to
a later state; a completed or failed record is final (exit 5 if you try to change
it). Completing or failing a record also stamps its end time and duration.

The update replaces the record's error text. A record that already has one is
therefore refused until you say what to do with it: --error <text> to set new
text, or --clear-error to remove it. Delegations are reported history, not proof: a record does not show that the call was authorized or that the reporting agent is who it says.

Usage:
  aiplex a2a delegations update <id> --status <pending|running|completed|failed> [--error <text> | --clear-error] [flags]

Examples:
  aiplex a2a delegations update d-1 --status completed
  aiplex a2a delegations update d-2 --status failed --error 'upstream timeout'

Flags:
      --clear-error     Remove the stored error text
      --error string    Error text to store with the record
  -h, --help            help for update
      --status string   New status: pending, running, completed or failed (required)

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

### `aiplex a2a connections`

```text
An agent connection pins a cloud agent's endpoint and the identity reference
used to call it. Create or replace one with the generic resource command, then
invoke it with 'aiplex a2a invoke':

  aiplex resources apply agent-connection bedrock -f connection.yaml

Usage:
  aiplex a2a connections [flags]
  aiplex a2a connections [command]

Aliases:
  connections, connection, conn

Examples:
  aiplex a2a connections list
  aiplex a2a connections get bedrock

Available Commands:
  list        List agent connections
  get         Show one agent connection
  delete      Delete an agent connection

Flags:
  -h, --help   help for connections

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex a2a connections [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex a2a connections list`

```text
List agent connections

Usage:
  aiplex a2a connections list [flags]

Aliases:
  list, ls

Examples:
  aiplex a2a connections list
  aiplex a2a connections list -o json

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

#### `aiplex a2a connections get`

```text
Show one agent connection

Usage:
  aiplex a2a connections get <id> [flags]

Aliases:
  get, show, describe

Examples:
  aiplex a2a connections get bedrock
  aiplex a2a connections get bedrock -o yaml

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

#### `aiplex a2a connections delete`

```text
Delete an agent connection

Usage:
  aiplex a2a connections delete <id> [flags]

Aliases:
  delete, remove, rm

Examples:
  aiplex a2a connections delete bedrock --yes

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


---
title: aiplex agents
sidebar_label: agents
description: "Register agents, bind their cloud identities and set their permissions"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Register agents, bind their cloud identities and set their permissions

Usage:
  aiplex agents [flags]
  aiplex agents [command]

Examples:
  aiplex agents register tutor-agent --name "Tutor agent" --scopes llm:model:mock-gpt
  aiplex agents identity presets

Available Commands:
  register    Register a new agent
  delete      Delete a registered agent
  permissions Show agent permissions across all planes
  user-scopes Manage user scopes (Dimension B)
  identity    Bind external cloud workload identities to an agent

Flags:
  -h, --help   help for agents

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex agents [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex agents register`

```text
Register an AI agent as an OAuth client.

Examples:
  aiplex agents register tutor-agent --name "Tutor Agent" --auth client_credentials \
    --scopes mcp:tools:search,a2a:task:research,llm:model:gemini-2.5-flash

Usage:
  aiplex agents register <client-id> [flags]

Examples:
  aiplex agents register tutor-agent --name "Tutor agent" --scopes llm:model:mock-gpt,mcp:tools:local-tools__echo

Flags:
      --auth string          Auth method: client_credentials, authorization_code, device_code (default "client_credentials")
      --description string   Description
      --grants strings       Grant types (default derives from --auth)
  -h, --help                 help for register
  -n, --name string          Display name
      --scopes strings       Allowed scopes (Dimension A)

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

### `aiplex agents delete`

```text
Delete a registered agent

Usage:
  aiplex agents delete <client-id> [flags]

Examples:
  aiplex agents delete tutor-agent

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

### `aiplex agents permissions`

```text
Show agent permissions across all planes

Usage:
  aiplex agents permissions <client-id> [flags]

Examples:
  aiplex agents permissions tutor-agent

Flags:
  -h, --help   help for permissions

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

### `aiplex agents user-scopes`

```text
Manage user scopes (Dimension B)

Usage:
  aiplex agents user-scopes [flags]
  aiplex agents user-scopes [command]

Examples:
  aiplex agents user-scopes get principal-0123abcd
  aiplex agents user-scopes set principal-0123abcd --scopes llm:model:mock-gpt

Available Commands:
  get         Get user scopes
  set         Set user scopes

Flags:
  -h, --help   help for user-scopes

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex agents user-scopes [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex agents user-scopes get`

```text
Get user scopes

Usage:
  aiplex agents user-scopes get <principal-id> [flags]

Examples:
  aiplex agents user-scopes get principal-0123abcd

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

#### `aiplex agents user-scopes set`

```text
Set user scopes

Usage:
  aiplex agents user-scopes set <principal-id> [flags]

Examples:
  aiplex agents user-scopes set principal-0123abcd --scopes llm:model:mock-gpt,mcp:tools:*

Flags:
  -h, --help             help for set
      --scopes strings   Scopes to set

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

### `aiplex agents identity`

```text
Let an agent running in another cloud (GCP, AWS, Azure, GitHub Actions,
Kubernetes, SPIFFE, any OIDC issuer) authenticate to AIPlex with its native
workload identity instead of a stored secret.

Typical flow:
  aiplex agents identity presets
  aiplex agents identity bind tutor-agent gh-ci --preset github-actions \
    --input repository=acme/tutor --input ref=refs/heads/main
  aiplex agents identity plan tutor-agent gh-ci
  aiplex agents identity verify --agent tutor-agent --binding gh-ci --token-file token.txt

Usage:
  aiplex agents identity [flags]
  aiplex agents identity [command]

Aliases:
  identity, identities

Examples:
  aiplex agents identity presets
  aiplex agents identity list tutor-agent

Available Commands:
  presets     List identity presets and the inputs each one needs
  list        List identity bindings of an agent
  get         Show one identity binding
  bind        Bind an external workload identity to an agent
  delete      Remove an identity binding
  plan        Show the setup steps to run in the other cloud
  verify      Dry-run a workload token against identity bindings

Flags:
  -h, --help   help for identity

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex agents identity [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex agents identity presets`

```text
List identity presets and the inputs each one needs

Usage:
  aiplex agents identity presets [flags]

Examples:
  aiplex agents identity presets
  aiplex agents identity presets -o json

Flags:
  -h, --help   help for presets

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

#### `aiplex agents identity list`

```text
List identity bindings of an agent

Usage:
  aiplex agents identity list <agent> [flags]

Examples:
  aiplex agents identity list tutor-agent

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

#### `aiplex agents identity get`

```text
Show one identity binding

Usage:
  aiplex agents identity get <agent> <id> [flags]

Examples:
  aiplex agents identity get tutor-agent ci

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

#### `aiplex agents identity bind`

```text
Create an identity binding. <id> is a lower-case DNS label (for example gh-ci).
Run 'aiplex agents identity presets' to see each preset's inputs. The server
derives the exact issuer, subject and JWKS URL from the inputs.

Use --replace to update an existing binding (the current revision is read
and sent as If-Match, so concurrent edits are rejected, not overwritten).

Examples:
  aiplex agents identity bind tutor-agent gh-ci --preset github-actions \
    --input repository=acme/tutor --input ref=refs/heads/main
  aiplex agents identity bind tutor-agent aws-prod --preset aws-sts-web-identity \
    --input issuer=https://abc123-def456-ghi789-jkl012.tokens.sts.global.api.aws \
    --input role_arn=arn:aws:iam::123456789012:role/aiplex-agent --input region=us-east-1 \
    --input audience=https://aiplex.example.com

The audience input may be omitted when the server has a default audience.
If a user identity provider of this tenant trusts the same issuer and
audience the write is refused; pass --acknowledge-overlap to save it anyway
(the server reports a warning, which is printed).

Usage:
  aiplex agents identity bind <agent> <id> --preset <preset> --input key=value ... [flags]

Examples:
  aiplex agents identity bind tutor-agent ci --preset github-actions --input repository=acme/app
  aiplex agents identity bind tutor-agent k8s --preset kubernetes --input issuer=https://oidc.example.com --input jwks_url=https://oidc.example.com/keys --input namespace=agents --input service_account=tutor

Flags:
      --acknowledge-overlap   Save even though a user identity provider trusts the same issuer and audience (not persisted; repeat on every write)
      --claim stringArray     Extra exact-match token claim as key=value (repeatable)
      --disabled              Create the binding disabled
      --display-name string   Human-readable name
  -h, --help                  help for bind
      --input stringArray     Preset input as key=value (repeatable)
      --preset string         Identity preset (see 'identity presets')
      --replace               Replace an existing binding with this id

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

#### `aiplex agents identity delete`

```text
Remove an identity binding

Usage:
  aiplex agents identity delete <agent> <id> [flags]

Examples:
  aiplex agents identity delete tutor-agent ci

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

#### `aiplex agents identity plan`

```text
Show the setup steps to run in the other cloud

Usage:
  aiplex agents identity plan <agent> <id> [flags]

Examples:
  aiplex agents identity plan tutor-agent ci

Flags:
  -h, --help   help for plan

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

#### `aiplex agents identity verify`

```text
Check what AIPlex would make of a token acquired in the other cloud, without
authenticating anything. Every check is listed with its reason; the exit
status is non-zero when the token is not valid.

The token is read from a file or stdin only. It is never accepted as a flag,
never printed, and never logged. Without --agent/--binding the token is
matched against all bindings; with them it is checked against that binding.
(The global --token flag is your own AIPlex API bearer token, not the
workload token being tested.)

Examples:
  aiplex agents identity verify --agent tutor-agent --binding gh-ci --token-file token.txt
  cat token.txt | aiplex agents identity verify --token-file -

Usage:
  aiplex agents identity verify [--agent <agent> --binding <id>] --token-file <path|-> [flags]

Examples:
  aiplex agents identity verify --agent tutor-agent --binding ci --token-file token.jwt
  cat token.jwt | aiplex agents identity verify --token-file -

Flags:
      --agent string        Agent client ID to pin the check to (requires --binding)
      --binding string      Binding ID to pin the check to (requires --agent)
  -h, --help                help for verify
      --token-file string   File containing the token, or - for stdin

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


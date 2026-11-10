---
title: aiplex identity
sidebar_label: identity
description: "Let people sign in with your corporate identity provider"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Let people sign in with your corporate identity provider

Usage:
  aiplex identity [flags]
  aiplex identity [command]

Examples:
  aiplex identity user-providers presets
  aiplex identity user-providers list

Available Commands:
  user-providers User identity providers (Okta, Entra ID, Google Workspace, any OIDC issuer)

Flags:
  -h, --help   help for identity

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex identity [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex identity user-providers`

```text
A user identity provider pins the issuer, audience, subject claim and group
claim of a corporate identity provider. Verified people get canonical
principal IDs and, when the provider verifies groups, memberships usable by
role bindings and quota eligibility.

  aiplex identity user-providers presets
  aiplex identity user-providers apply -f okta.yaml
  aiplex identity user-providers plan okta
  aiplex identity user-providers verify --token-file token.txt

Usage:
  aiplex identity user-providers [flags]
  aiplex identity user-providers [command]

Aliases:
  user-providers, user-provider

Examples:
  aiplex identity user-providers presets
  aiplex identity user-providers apply -f okta.yaml

Available Commands:
  presets     List provider presets and the inputs each one needs
  list        List user identity providers
  get         Show one user identity provider
  apply       Create or replace a user identity provider from a JSON or YAML file
  delete      Remove a user identity provider
  plan        Show the setup steps for a user identity provider
  verify      Dry-run a user token against the identity providers

Flags:
  -h, --help   help for user-providers

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex identity user-providers [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex identity user-providers presets`

```text
List provider presets and the inputs each one needs

Usage:
  aiplex identity user-providers presets [flags]

Examples:
  aiplex identity user-providers presets

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

#### `aiplex identity user-providers list`

```text
List user identity providers

Usage:
  aiplex identity user-providers list [flags]

Examples:
  aiplex identity user-providers list

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

#### `aiplex identity user-providers get`

```text
Show one user identity provider

Usage:
  aiplex identity user-providers get <id> [flags]

Examples:
  aiplex identity user-providers get okta

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

#### `aiplex identity user-providers apply`

```text
The file holds the provider id and the request fields:

  id: okta
  preset: okta
  display_name: Acme Okta
  inputs:
    issuer: https://acme.okta.com/oauth2/default
    jwks_url: https://acme.okta.com/oauth2/default/v1/keys
    audience: api://aiplex
    groups_from: claim

Each preset's inputs are listed by: aiplex identity user-providers presets

Fields this CLI does not know about are sent to the server unchanged; for
example "acknowledge_overlap: true" saves a provider even though an agent
identity binding trusts the same issuer and audience (the server's warning is
printed). The
provider is created, or replaced at its current revision when it exists.
Use '-f -' to read stdin.

Usage:
  aiplex identity user-providers apply -f <file> [flags]

Examples:
  aiplex identity user-providers apply -f okta.yaml
  aiplex identity user-providers get okta -o yaml | aiplex identity user-providers apply -f -

Flags:
  -f, --file string   JSON or YAML file, or - for stdin
  -h, --help          help for apply

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

#### `aiplex identity user-providers delete`

```text
Remove a user identity provider

Usage:
  aiplex identity user-providers delete <id> [flags]

Examples:
  aiplex identity user-providers delete okta

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

#### `aiplex identity user-providers plan`

```text
Show the setup steps for a user identity provider

Usage:
  aiplex identity user-providers plan <id> [flags]

Examples:
  aiplex identity user-providers plan okta

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

#### `aiplex identity user-providers verify`

```text
Check what AIPlex would make of a token issued by your identity provider,
without authenticating anything. Every check is listed with its reason; the
exit status is non-zero when the token is not valid.

The token is read from a file or stdin only. It is never accepted as a flag,
never printed, and never logged. (The global --token flag is your own AIPlex
API bearer token, not the user token being tested.)

Examples:
  aiplex identity user-providers verify --token-file token.txt
  cat token.txt | aiplex identity user-providers verify --token-file - --provider okta

Usage:
  aiplex identity user-providers verify --token-file <path|-> [--provider <id>] [flags]

Examples:
  aiplex identity user-providers verify --provider okta --token-file token.jwt

Flags:
  -h, --help                help for verify
      --provider string     Provider ID to pin the check to
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


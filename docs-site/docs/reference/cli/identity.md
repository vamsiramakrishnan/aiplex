---
title: aiplex identity
sidebar_label: identity
description: "Workforce identity: let people sign in with their corporate identity provider"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Workforce identity: let people sign in with their corporate identity provider

Usage:
  aiplex identity [command]

Available Commands:
  user-providers User identity providers (Okta, Entra ID, Google Workspace, any OIDC issuer)

Flags:
  -h, --help   help for identity

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex identity [command] --help" for more information about a command.
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
  aiplex identity user-providers [command]

Aliases:
  user-providers, user-provider

Available Commands:
  apply       Create or replace a user identity provider from a JSON or YAML file
  delete      Remove a user identity provider
  get         Show one user identity provider
  list        List user identity providers
  plan        Show the setup steps for a user identity provider
  presets     List provider presets and the inputs each one needs
  verify      Dry-run a user token against the identity providers

Flags:
  -h, --help   help for user-providers

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex identity user-providers [command] --help" for more information about a command.
```

#### `aiplex identity user-providers apply`

```text
The file holds the provider id and the request fields:

  id: okta
  preset: okta
  inputs: { domain: acme.okta.com }
  display_name: Acme Okta

Fields this CLI does not know about are sent to the server unchanged; for
example "acknowledge_overlap: true" saves a provider even though an agent
identity binding trusts the same issuer and audience (the server's warning is
printed). The
provider is created, or replaced at its current revision when it exists.
Use '-f -' to read stdin.

Usage:
  aiplex identity user-providers apply -f <file> [flags]

Flags:
  -f, --file string   JSON or YAML file, or - for stdin
  -h, --help          help for apply

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex identity user-providers delete`

```text
Remove a user identity provider

Usage:
  aiplex identity user-providers delete <id> [flags]

Flags:
  -h, --help   help for delete

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex identity user-providers get`

```text
Show one user identity provider

Usage:
  aiplex identity user-providers get <id> [flags]

Flags:
  -h, --help   help for get

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex identity user-providers list`

```text
List user identity providers

Usage:
  aiplex identity user-providers list [flags]

Flags:
  -h, --help   help for list

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex identity user-providers plan`

```text
Show the setup steps for a user identity provider

Usage:
  aiplex identity user-providers plan <id> [flags]

Flags:
  -h, --help   help for plan

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

#### `aiplex identity user-providers presets`

```text
List provider presets and the inputs each one needs

Usage:
  aiplex identity user-providers presets [flags]

Flags:
  -h, --help   help for presets

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
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

Flags:
  -h, --help                help for verify
      --provider string     Provider ID to pin the check to
      --token-file string   File containing the token, or - for stdin

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```


---
title: aiplex iam
sidebar_label: iam
description: "Map identity-provider groups to roles (admin, deployer, viewer) with role bindings"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
A role binding maps an identity-provider group to an AIPlex role (admin,
deployer, viewer or agent) and optionally to the default scopes of that role. It
can be restricted to one issuer and pool. Role bindings decide who can change
configuration, so every command except 'roles' needs the admin role.

  aiplex iam roles
  aiplex iam role-bindings list
  aiplex iam role-bindings apply -f binding.yaml
  aiplex iam role-bindings delete rb-1 --yes

Changing or deleting a binding is checked against its revision, so a stale
edit is refused (exit code 5) rather than overwriting a newer one.

Usage:
  aiplex iam [flags]
  aiplex iam [command]

Examples:
  aiplex iam roles
  aiplex iam role-bindings list

Available Commands:
  roles              List the roles and the scopes each grants by default
  role-bindings      Group-to-role mappings (admin)
  validate-principal Check a workload or workforce identity principal string (admin)

Flags:
  -h, --help   help for iam

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex iam [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex iam roles`

```text
List the roles and the scopes each grants by default

Usage:
  aiplex iam roles [flags]

Examples:
  aiplex iam roles
  aiplex iam roles -o json

Flags:
  -h, --help   help for roles

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

### `aiplex iam role-bindings`

```text
Group-to-role mappings (admin)

Usage:
  aiplex iam role-bindings [flags]
  aiplex iam role-bindings [command]

Aliases:
  role-bindings, role-binding, bindings

Examples:
  aiplex iam role-bindings list
  aiplex iam role-bindings apply -f binding.yaml

Available Commands:
  list        List role bindings (admin)
  get         Show one role binding
  apply       Create or replace a role binding from a JSON or YAML file (admin)
  delete      Delete a role binding (admin)

Flags:
  -h, --help   help for role-bindings

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex iam role-bindings [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex iam role-bindings list`

```text
List role bindings (admin)

Usage:
  aiplex iam role-bindings list [flags]

Aliases:
  list, ls

Examples:
  aiplex iam role-bindings list
  aiplex iam role-bindings list --group platform-eng -o json

Flags:
      --group string   Only bindings for this group
  -h, --help           help for list

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

#### `aiplex iam role-bindings get`

```text
Show one role binding

Usage:
  aiplex iam role-bindings get <id> [flags]

Aliases:
  get, show, describe

Examples:
  aiplex iam role-bindings get local-admin
  aiplex iam role-bindings get local-admin -o yaml > binding.yaml

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

#### `aiplex iam role-bindings apply`

```text
The file holds the binding:

  id: platform-admins          # optional; derived from the binding when omitted
  group: platform-eng          # the identity-provider group (required)
  role: admin                  # admin, deployer, viewer or agent (required)
  provider: https://idp.example.com   # optional: only this issuer
  pool_id: workforce           # optional: only this pool
  description: Platform engineers
  scopes: [mcp:tools:*]        # the scopes granted; see below

When the binding is created, omitting scopes (or writing null) gives it the
role's default scopes ('aiplex iam roles' lists them) and 'scopes: []' grants
none. With an id that exists, the binding is replaced at its current revision:
group, role, provider, pool_id and description are set to the file's values
(leave one out to clear it) and scopes must be stated, as a list or as [] for
none; a file that omits them is refused (exit 2) rather than keeping scopes the
file does not show. A file written by 'get -o yaml' applies as it is; its
revision is checked, and the apply is refused (exit 5) when the binding has
changed since. Without an id the binding is created, and applying the same file
again is refused as already existing.
Use '-f -' to read stdin.

Usage:
  aiplex iam role-bindings apply -f <file> [flags]

Examples:
  aiplex iam role-bindings apply -f binding.yaml
  aiplex iam role-bindings get platform-admins -o yaml | aiplex iam role-bindings apply -f -

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

#### `aiplex iam role-bindings delete`

```text
People who held a role only through this binding lose it on their next request. Deleting the binding that makes you an administrator can lock you out; the bootstrap role bindings file is the way back in.

Usage:
  aiplex iam role-bindings delete <id> [flags]

Aliases:
  delete, rm, remove

Examples:
  aiplex iam role-bindings delete platform-admins --yes

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

### `aiplex iam validate-principal`

```text
Checks the syntax of a Google Cloud principal:// or principalset:// URI without
creating anything. Exits 1 when the principal is not valid.

Usage:
  aiplex iam validate-principal <principal> [flags]

Examples:
  aiplex iam validate-principal 'principalSet://iam.googleapis.com/locations/global/workforcePools/acme/*'

Flags:
  -h, --help   help for validate-principal

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


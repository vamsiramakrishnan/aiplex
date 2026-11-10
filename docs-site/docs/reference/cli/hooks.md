---
title: aiplex hooks
sidebar_label: hooks
description: "Request hooks — inspect, mask or refuse prompts, responses and tool calls"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Request hooks run on model and tool traffic before it reaches the upstream
and before the answer reaches the caller. A hook policy binds an ordered list
of hooks (redact, size_limit, http sidecar) to a scope: the tenant, an access
point, a model surface or a tool connection. Narrower scopes add hooks; they
cannot remove a broader one. Hooks fail closed: a hook that cannot decide
refuses the request unless it is explicitly fail_open.

Redaction is pattern matching. It is not a guarantee that personal data or
secrets are removed. See docs/request-hooks.md.

Usage:
  aiplex hooks [flags]
  aiplex hooks [command]

Examples:
  aiplex hooks policies set -f examples/hooks/policy.yaml
  aiplex hooks policies test pii-guard --text "mail jane@example.com"
  aiplex usage records --hook-decision deny -o wide

Available Commands:
  policies    Manage hook policies (operators only)

Flags:
  -h, --help   help for hooks

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex hooks [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex hooks policies`

```text
Hook policies are revisioned resources: replacing or deleting one needs its
current revision, so concurrent edits are never silently overwritten.

Usage:
  aiplex hooks policies [flags]
  aiplex hooks policies [command]

Examples:
  aiplex hooks policies list
  aiplex hooks policies set -f examples/hooks/policy.yaml
  aiplex hooks policies delete pii-guard --yes

Available Commands:
  list        List hook policies
  get         Show one hook policy and its current revision
  set         Create or replace a hook policy from a file
  delete      Delete a hook policy (uses the current revision unless --revision is given)
  test        Dry-run a saved policy on sample text (never stored)

Flags:
  -h, --help   help for policies

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex hooks policies [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

#### `aiplex hooks policies list`

```text
List hook policies

Usage:
  aiplex hooks policies list [flags]

Examples:
  aiplex hooks policies list
  aiplex hooks policies list -o wide
  aiplex hooks policies list -o json

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

#### `aiplex hooks policies get`

```text
Show one hook policy and its current revision

Usage:
  aiplex hooks policies get <id> [flags]

Examples:
  aiplex hooks policies get pii
  aiplex hooks policies get pii -o yaml

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

#### `aiplex hooks policies set`

```text
Reads a full policy specification (YAML or JSON). Unknown fields and unknown
hook types are refused, every regex is compiled, and sidecar endpoints must be
approved by the deployment's outbound endpoint policy. The current revision is
read for you; --revision pins the revision you expect instead.

Usage:
  aiplex hooks policies set -f <policy.yaml> [flags]

Examples:
  aiplex hooks policies set -f examples/hooks/policy.yaml
  aiplex hooks policies set -f pii.json --revision 3

Flags:
  -f, --file string     Policy specification (YAML or JSON)
  -h, --help            help for set
      --revision uint   Expected current revision (default: read it for you)

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

#### `aiplex hooks policies delete`

```text
Delete a hook policy (uses the current revision unless --revision is given)

Usage:
  aiplex hooks policies delete <id> [flags]

Examples:
  aiplex hooks policies delete pii --yes

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

#### `aiplex hooks policies test`

```text
Runs the policy's hooks, whatever its scope, over the sample texts and shows
each hook's decision, the rule identifiers that matched with counts, and the
output after masking. http hooks call their sidecar. Nothing is recorded.

Usage:
  aiplex hooks policies test <id> --text <sample> [--text <sample>...] [flags]

Examples:
  aiplex hooks policies test pii --text "mail jane@example.com"
  aiplex hooks policies test pii --phase response --text "model output" -o json

Flags:
  -h, --help               help for test
      --phase string       Phase: request or response (default "request")
      --plane string       Plane passed to sidecars: llmplex or mcplex (default "llmplex")
      --text stringArray   Sample text (repeatable)

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


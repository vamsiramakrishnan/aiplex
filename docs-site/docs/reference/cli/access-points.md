---
title: aiplex access-points
sidebar_label: access-points
description: "Create and manage access points: one URL with identity, access and quota"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
An access point is one stable URL (/ap/<id>/...) in front of models, tools and
skills. It decides which kinds of callers (agents, users, clients) may use it,
and carries a QoS class that is rendered into quota controls.

  aiplex access-points create ide --model gpt-4o-mini=openai --tool search --allow-groups eng
  aiplex access-points apply -f ide.yaml
  aiplex access-points quota-plan ide
  aiplex connect ide --client claude-code

Usage:
  aiplex access-points [flags]
  aiplex access-points [command]

Aliases:
  access-points, access-point, ap

Examples:
  aiplex access-points list
  aiplex access-points create team --model mock-gpt=local-models --tool local-tools

Available Commands:
  list        List access points (administrators see all; others see the ones that admit them)
  get         Show one access point
  create      Create an access point from flags (or apply -f for a full spec)
  apply       Create or replace an access point from a JSON or YAML file
  delete      Delete an access point
  quota-plan  Render the access point's QoS block as quota controls for review
  routing     Show the live routing state of an access point's models (operators)
  purge-cache Remove cached answers of an access point (admins)

Flags:
  -h, --help   help for access-points

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex access-points [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex access-points list`

```text
List access points (administrators see all; others see the ones that admit them)

Usage:
  aiplex access-points list [flags]

Aliases:
  list, ls

Examples:
  aiplex access-points list
  aiplex access-points list -o json

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

### `aiplex access-points get`

```text
Show one access point

Usage:
  aiplex access-points get <id> [flags]

Aliases:
  get, show, describe

Examples:
  aiplex access-points get local
  aiplex access-points get local -o yaml > ap.yaml

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

### `aiplex access-points create`

```text
Creates an access point: one URL (/ap/<id>/...) that serves the models and
tools you list to the callers you allow.

--model name=surface[:native] exposes a model of a model surface under a
client-facing name (native defaults to name). --tool exposes every permitted
tool of a tool connection (named <connection>__<tool> to callers).

Who may call: with no --allow-* flag every verified caller of the allowed
kinds (--callers, default user,agent,client) is admitted. Otherwise a caller
must match one of:
  --allow-users   a subject at your identity provider (as in the token's sub),
                  a known user's email (from the usage ledger), or a
                  principal-... ID (see: aiplex who --kind user)
  --allow-groups  a group of your identity provider (e.g. eng)
  --allow-agents  an agent ID (see: aiplex agents identity list <agent>)
--allow-clients additionally requires a registered client ID.

Routing (see docs/reliability-and-routing.md): --backend name=surface[:native]
adds a backend to model <name>; repeat it to list backends in priority order
(the first is tried first; the others are fallbacks). --strategy
name=priority|weighted|lowest_cost picks the order, --retry name turns on
retries on the same backend with the defaults, and --cache name=ttl[:scope]
turns on the exact-match response cache (scope principal by default;
access_point shares cached answers between callers: only for prompts that
hold no personal data).

The access point is created new; --update replaces an existing one (its
revision is read for you). For QoS classes, expose lists, weights and the
other routing settings use apply -f.

Usage:
  aiplex access-points create <id> --model <name=surface[:native]> --tool <connection> [--allow-users ...] [flags]

Aliases:
  create, add

Examples:
  aiplex access-points create team --model mock-gpt=local-models --tool local-tools
  aiplex access-points create ide --model gpt-4o-mini=openai --model claude=claude:claude-sonnet-4-5 --allow-groups eng
  aiplex access-points create ci --model gpt-4o-mini=openai --callers agent --allow-agents ci-bot
  aiplex access-points create bob-only --model mock-gpt=local-models --allow-users bob --update
  aiplex access-points create ha --backend gpt=openai:gpt-4o-mini --backend gpt=azure:gpt-4o-mini --retry gpt --cache gpt=10m

Flags:
      --allow-agents strings    Agent IDs admitted (comma separated)
      --allow-clients strings   Registered client IDs required (comma separated)
      --allow-groups strings    Groups admitted (comma separated)
      --allow-users strings     Users admitted (subject, email or principal ID; comma separated)
      --backend stringArray     Backend of a routed model: name=surface[:native-model] (repeatable, in priority order)
      --cache stringArray       Exact-match response cache for a model: name=ttl[:principal|access_point] (repeatable)
      --callers strings         Caller kinds admitted: user, agent, client (default [user,agent,client])
  -h, --help                    help for create
      --model stringArray       Model to expose: name=surface[:native-model] (repeatable)
      --name string             Display name
      --retry stringArray       Turn on same-backend retries (defaults) for a model name (repeatable)
      --strategy stringArray    Routing strategy of a model: name=priority|weighted|lowest_cost (repeatable)
      --tool stringArray        Tool connection to expose (repeatable)
      --update                  Replace the access point if it exists

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

### `aiplex access-points apply`

```text
Create the access point named by the file's id, or replace it when it already
exists (the current revision is read first, so a concurrent change is not
overwritten). The file may be the bare specification or the record printed by
'access-points get -o yaml'. Use '-f -' to read stdin.

Usage:
  aiplex access-points apply -f <file> [flags]

Examples:
  aiplex access-points apply -f ide.yaml
  aiplex access-points get local -o yaml | aiplex access-points apply -f -

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

### `aiplex access-points delete`

```text
Delete an access point

Usage:
  aiplex access-points delete <id> [flags]

Aliases:
  delete, rm, remove

Examples:
  aiplex access-points delete team --yes

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

### `aiplex access-points quota-plan`

```text
Shows the quota controls and scheduler flow that the access point's qos block would add. Nothing is applied.

Usage:
  aiplex access-points quota-plan <id> [flags]

Examples:
  aiplex access-points quota-plan ide

Flags:
  -h, --help   help for quota-plan

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

### `aiplex access-points routing`

```text
Shows each routed model of the access point: its strategy and, per backend,
the circuit breaker (closed, open or half_open), consecutive failures and the
last outcomes, plus the response-cache occupancy.

The state belongs to the API process that answered: breakers and the cache are
kept in memory per process, so another replica may show different state, and a
restart starts every breaker closed and the cache empty.

Usage:
  aiplex access-points routing <id> [flags]

Examples:
  aiplex access-points routing ha
  aiplex access-points routing ha -o json

Flags:
  -h, --help   help for routing

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

### `aiplex access-points purge-cache`

```text
Removes the response-cache entries of the access point, or of one model, in the API process that answers. Other replicas keep their own cache.

Usage:
  aiplex access-points purge-cache <id> [--model <name>] [flags]

Examples:
  aiplex access-points purge-cache ha
  aiplex access-points purge-cache ha --model gpt

Flags:
  -h, --help           help for purge-cache
      --model string   Only this model's cached answers

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


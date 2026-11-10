---
title: aiplex dev
sidebar_label: dev
description: "Run AIPlex on this machine with a mock model, mock tools and the Console"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Starts a local AIPlex in development mode and shows it working end to end.

What runs, all on 127.0.0.1 and in memory (nothing persists after Ctrl+C):
  - aiplex-api in development mode (in-memory stores, loopback only)
  - a local token issuer: you are "developer@localhost" with the admin role
  - a built-in mock model provider with four deterministic models, one per
    wire: mock-gpt (OpenAI Chat Completions), mock-responses (OpenAI
    Responses), mock-claude (Anthropic Messages) and mock-gemini (Gemini
    generateContent), so Claude Code, Codex and the OpenAI, Anthropic and
    Google SDKs all work with no key (or a real provider with --provider and
    --api-key-env, which exposes one model)
  - a built-in MCP server with three tools (echo, add, reset_counter)
  - the Console, when this binary carries a built Console

What is created through the normal APIs:
  - model surfaces "local-models", "local-responses", "local-messages" and
    "local-gemini", access point "local", tool connection "local-tools" and,
    for the mock models, placeholder prices

To see role-based behaviour, "aiplex dev token --user bob [--role deployer]" mints
a token for a second caller (bob@localhost) from the same local issuer; use it
as AIPLEX_TOKEN. Only your own developer token can ask for one, and only from
this machine.

The CLI context "local" is created and made current while this runs. The
previous context is restored when you stop it.

--detach runs it in the background; stop it with
"aiplex dev stop" and check it with "aiplex dev status". Two logs are kept:
~/.aiplex/dev-api.log holds what aiplex-api prints (requests, errors) and, for
a background run, ~/.aiplex/dev.log holds what aiplex dev itself prints (this
summary and its messages). When aiplex dev stops
or is killed, the local API stops with it.

The local API calls only loopback upstreams and the public OpenAI, Anthropic
and Gemini APIs; allow another origin with --allow-origin https://host.

A real provider key is read from the environment variable you name with
--api-key-env. It is passed only to the local API process's environment; it
is never printed, accepted as a flag value, or written to disk.

Usage:
  aiplex dev [flags]
  aiplex dev [command]

Examples:
  aiplex dev
  aiplex dev --detach && aiplex dev status
  aiplex dev --provider openai --api-key-env OPENAI_API_KEY
  aiplex dev --provider anthropic --api-key-env ANTHROPIC_API_KEY --model claude-haiku-4-5
  eval "$(aiplex dev env)"    # in another terminal: AIPLEX_URL, AIPLEX_TOKEN, OPENAI_BASE_URL, ...

Available Commands:
  env         Print shell exports for the running `aiplex dev` (use: eval "$(aiplex dev env)")
  status      Show whether aiplex dev is running, where, and the Console URL
  stop        Stop the running aiplex dev (and its local API)
  token       Mint a local token for another caller, to see role-based behaviour

Flags:
      --allow-origin strings   Extra upstream origin the local API may call, e.g. https://mcp.example.com (repeatable)
      --api-bin string         Path to the aiplex-api binary (default: next to this binary, then PATH; built from source when run in the repository)
      --api-key-env string     Name of the environment variable that holds the provider API key (not the key itself)
      --console-port int       Console port on 127.0.0.1 (default 9090)
      --context string         CLI context to create for the local API (default "local")
  -d, --detach                 Run in the background; stop with: aiplex dev stop
  -h, --help                   help for dev
      --model string           Model to expose (default: mock-gpt, gpt-4o-mini, claude-haiku-4-5 or gemini-2.5-flash)
      --no-console             Do not serve the Console
      --no-tools               Do not start the built-in MCP server's tool connection
      --open                   Open the Console in a browser
      --port int               API port on 127.0.0.1 (default 8080)
      --provider string        Model provider: mock, openai, anthropic or gemini (default "mock")

Global Flags:
      --debug           Show debug output, including raw API errors
      --no-color        Disable colour (also: NO_COLOR=1)
  -o, --output string   Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex dev [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex dev env`

```text
Prints AIPLEX_URL and AIPLEX_TOKEN for the running local API, and the base URL
and key variables that OpenAI and Anthropic SDKs read, pointed at the local
access point. The token is the local developer token; it is accepted only by
the local API that issued it and expires when that API stops.

Usage:
  aiplex dev env [flags]

Examples:
  eval "$(aiplex dev env)"
  aiplex dev env --context local

Flags:
  -h, --help   help for env

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

### `aiplex dev status`

```text
Prints the running local AIPlex: API URL, pid, uptime, access point, model and Console. Exits 1 when none is running.

Usage:
  aiplex dev status [flags]

Examples:
  aiplex dev status
  aiplex dev status -o json

Flags:
  -h, --help   help for status

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

### `aiplex dev stop`

```text
Stop the running aiplex dev (and its local API)

Usage:
  aiplex dev stop [flags]

Examples:
  aiplex dev stop

Flags:
  -h, --help   help for stop

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

### `aiplex dev token`

```text
Prints a token for a second local caller, signed by the same local issuer as your
own, so you can see what a caller with no role, a deployer or another admin gets.

The token is printed on stdout (nothing else), so it can be captured; what it
is for goes to stderr. It is valid for an hour, only for the local API that
issued it, and is minted by the local token issuer (loopback only), which
accepts the request only with your own developer token.

  --role none      (default) no role: the caller sees only the access points
                   that admit it. It still needs model and tool grants (the
                   "agents user-scopes set" command, with the principal ID
                   this command prints)
  --role deployer  configuration screens and commands, no admin-only actions
  --role admin     the same role as you
  --groups         extra identity-provider groups in the token (for access
                   points that admit a group)

The caller's email is <name>@localhost, and its subject is <name>, so
"aiplex access-points create team --allow-users <name>@localhost ..." admits it.

Usage:
  aiplex dev token --user <name> [--role none|deployer|admin] [--groups a,b] [flags]

Examples:
  aiplex dev token --user bob
  aiplex dev token --user dana --role deployer
  aiplex dev token --user carol --groups eng,research

Flags:
      --groups string   Extra groups for the token, comma separated
  -h, --help            help for token
      --role string     Role: none, deployer or admin (default "none")
      --user string     Name of the caller to mint a token for (its email is <name>@localhost)

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


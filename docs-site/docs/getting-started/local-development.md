---
title: Run it locally in a minute
sidebar_label: Run it locally in a minute
description: Build AIPlex and start a local API with a mock model, a mock MCP server and a usage ledger using one command, aiplex dev.
---

# Run it locally in a minute

`aiplex dev` starts the API on your machine with a built-in mock model and a built-in MCP server, creates an access point, signs you in as a local admin and prints what to try next. You need Go 1.25 or newer and `curl`. You do not need Docker, a cloud account or an API key.

## 1. Build and start

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex
make build
export PATH="$PWD/bin:$PATH"
aiplex dev
```

`make build` writes `bin/aiplex` and `bin/aiplex-api`. `aiplex dev` starts the API from the binary next to it, in memory on `127.0.0.1:8080`, and prints:

```text
  AIPlex local development

  API            http://127.0.0.1:8080  (development mode, in memory, loopback only)
  You            developer@localhost  (admin; token from a local issuer, valid 12h)
  Model          mock-gpt  via the built-in mock provider (OpenAI Chat Completions wire, no API key)
  Tools          local-tools: echo, add, reset_counter  (built-in MCP server)
  Access point   local  ->  http://127.0.0.1:8080/ap/local
  Price          mock-gpt  (placeholder rates, so estimated cost shows in usage)
  Quota          off. QuotaPlex needs an operator quota policy file (AIPLEX_QUOTA_FILE); see docs/quotaplex-administration.md
  CLI context    "local" (current)
  Console        not built into this binary. Build it with `make console && make build` (needs Node 22), or run
                 `cd console && npm ci && npm run dev` (it proxies /api to http://localhost:8080)

  WARNING: development mode serves unauthenticated requests as admin; never expose, tunnel or proxy 127.0.0.1:8080.
```

(Trimmed: the full output also names the API log file and repeats the commands below.) Keep this terminal open. Ctrl+C stops everything and discards the state, and the CLI context you had before is restored.

:::warning
Development mode is unauthenticated and loopback-only. The API refuses to start in it on a non-loopback address. Do not expose, tunnel or proxy it.
:::

## 2. Send a request

In a second terminal, from the same directory:

```bash
export PATH="$PWD/bin:$PATH"
eval "$(aiplex dev env)"
```

`aiplex dev env` exports `AIPLEX_URL` and `AIPLEX_TOKEN` for the CLI and for curl, and the base URL and key variables that OpenAI, Anthropic and Gemini SDKs read (`OPENAI_BASE_URL` and `OPENAI_API_KEY` for the mock model). The token is accepted only by the local API that issued it and expires when that API stops.

```bash
curl -s "$AIPLEX_URL/ap/local/v1/chat/completions" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" -H "Content-Type: application/json" \
  -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Hello"}]}'
```

```json
{"choices":[{"finish_reason":"stop","index":0,"message":{"content":"Hello from the AIPlex mock model. You said: Hello","role":"assistant"}}],"created":1794283277,"id":"chatcmpl-mock-1794283277328460091","model":"mock-gpt","object":"chat.completion","usage":{"completion_tokens":9,"prompt_tokens":1,"total_tokens":10}}
```

## 3. See who used what

```bash
aiplex usage summary
```

```text
REQUESTS  ERRORS  DENIED  IN  OUT  CACHE-W  MODALITY  UNITS  BATCH  COST  UNKNOWN  BOUNDED
──────────────────────────────────────────────────────────────────────────────────────────
1  0  0  1  9  unknown  —  —  —  USD 0.000006 (estimated)  —  —

1 records scanned.
```

(The separator line is shortened, and the column layout may change as the CLI output is reworked.)

The call is in the [usage ledger](../library/usage-ledger.md). The cost is labelled estimated because the mock model has a placeholder price. Values that are not known are shown as `unknown`, not zero. Try `aiplex usage records` and `aiplex usage summary --by model` too.

## 4. Use the same access point for tools and clients

The access point `local` also serves the built-in MCP server at `http://127.0.0.1:8080/ap/local/mcp`. `aiplex connect local` prints ready-to-paste configuration: environment variables, a curl command and an MCP client configuration. See [Connect a client](./connect-client.md) and the repository's `examples/` directory, which has a script for each of these steps (OpenAI SDK, MCP tools, usage report) that `make examples-test` runs.

## Use a real provider

```bash
export OPENAI_API_KEY=...        # your own key
aiplex dev --provider openai --api-key-env OPENAI_API_KEY
```

`--provider` takes `openai`, `anthropic` or `gemini` (the default is the mock). `--api-key-env` names the environment variable that holds the key; the key itself is never accepted as a flag, printed or written to disk. `--model` chooses the model. Without the variable set, `aiplex dev` stops with an error that names it. The project has not run this path against a live provider, so treat the first run as a test; provider charges apply.

## Other flags

`--port` and `--console-port` change the ports, `--no-console` skips the Console, `--no-tools` skips the built-in MCP server's connection, `--context` names the CLI context it creates, and `--api-bin` points at an `aiplex-api` binary. Run `aiplex dev --help` for the current list.

## What this does not establish

Everything is in memory and loopback-only, and prices are placeholders. Local success does not establish cloud routing, provider access, identity federation or quota behaviour. For the next steps, see [Local stack with Docker](./docker-stack.md) (emulators and Ory services), [Install the platform](./install-platform.md) and [Deploy a tool](./first-deploy.md).

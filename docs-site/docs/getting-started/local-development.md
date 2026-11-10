---
title: Run it locally in a minute
sidebar_label: Run it locally in a minute
description: Build AIPlex and start a local API with a mock model, a mock MCP server and a usage ledger using one command, aiplex dev.
---

# Run it locally in a minute

`aiplex dev` starts the API on your machine with a built-in mock provider and a built-in MCP server, creates an access point, signs you in as a local admin and prints what to try next. The mock provider has four models, one per wire: `mock-gpt` (OpenAI Chat Completions), `mock-responses` (OpenAI Responses), `mock-claude` (Anthropic Messages) and `mock-gemini` (Gemini generateContent). You need Go 1.25 or newer and `curl`. You do not need Docker, a cloud account or an API key.

## 1. Build and start

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex
make build
export PATH="$PWD/bin:$PATH"
aiplex dev
```

`make build` writes `bin/aiplex` and `bin/aiplex-api`. `aiplex dev` starts the API from the binary next to it, in memory on `127.0.0.1:8080`, and prints (output of a source build, copied as printed):

```text
  AIPlex local development

  API            http://127.0.0.1:8080  (development mode, in memory, loopback only)
  You            developer@localhost  (admin; token from a local issuer, valid 12h)
  Models         mock-gpt, mock-claude, mock-responses, mock-gemini  (built-in mock provider, no API key)
                 wires: OpenAI Chat, Anthropic Messages, OpenAI Responses, Gemini generateContent
  Tools          local-tools: echo, add, reset_counter  (built-in MCP server)
  Access point   local  ->  http://127.0.0.1:8080/ap/local
  Price          placeholder rates for each mock model, so estimated cost shows in usage
  Quota          off (optional: QuotaPlex reads an operator policy file named by AIPLEX_QUOTA_FILE)
  CLI context    "local" (current)
  Console        not in this binary (a source build without Node). Release binaries include it;
                 from source: make console && make build (Node 22), then restart aiplex dev
  API log        /root/.aiplex/dev-api.log

  WARNING: development mode serves unauthenticated requests as admin; never expose, tunnel or proxy 127.0.0.1:8080.

  Ready in 0.1s. Try next, in another terminal:

    # 1. Send a chat request through the access point
    eval "$(aiplex dev env)"
    curl -s "$AIPLEX_URL/ap/local/v1/chat/completions" \
      -H "Authorization: Bearer $AIPLEX_TOKEN" -H "Content-Type: application/json" \
      -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Hello"}]}'
    # (any OpenAI SDK works too: the eval line sets OPENAI_BASE_URL and OPENAI_API_KEY)

    # 2. See what it cost and who made it
    aiplex usage records

    # 3. Point an MCP client or IDE at it, and see where the Console is
    aiplex connect local
    aiplex dev status

  Press Ctrl+C to stop. State is in memory and is discarded on stop.
```

The Console is not part of a plain `make build` binary, and the summary says so. Release binaries include it. From source, run `make console && make build` (needs Node 22) and start `aiplex dev` again.

Keep this terminal open. Ctrl+C stops everything and discards the state, and the CLI context you had before is restored.

To run it in the background instead, use `aiplex dev --detach`. The summary then ends with the pid and the log file (`~/.aiplex/dev.log`). Check it with `aiplex dev status` and stop it with `aiplex dev stop`. A second `aiplex dev` is refused while one is running:

```text
Error: aiplex dev is already running (pid 13956) at http://127.0.0.1:18571
  Use it: eval "$(aiplex dev env)"
  Or stop it first: aiplex dev stop
```

:::warning
Development mode is unauthenticated and loopback-only. The API refuses to start in it on a non-loopback address. Do not expose, tunnel or proxy it.
:::

## 2. Send a request

In a second terminal, from the same directory:

```bash
export PATH="$PWD/bin:$PATH"
eval "$(aiplex dev env)"
```

`aiplex dev env` exports `AIPLEX_URL` and `AIPLEX_TOKEN` for the CLI and for curl, and the base URL and key variables that OpenAI, Anthropic and Gemini SDKs read. With the mock provider it exports all six (`OPENAI_BASE_URL`, `OPENAI_API_KEY`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY`, `GOOGLE_GEMINI_BASE_URL` and `GEMINI_API_KEY`), each pointing at the access point. The token is accepted only by the local API that issued it and expires when that API stops.

```bash
curl -s "$AIPLEX_URL/ap/local/v1/chat/completions" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" -H "Content-Type: application/json" \
  -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Hello"}]}'
```

```json
{"choices":[{"finish_reason":"stop","index":0,"message":{"content":"Hello from the AIPlex mock model. You said: Hello","role":"assistant"}}],"created":1794287630,"id":"chatcmpl-mock-1794287630385369003","model":"mock-gpt","object":"chat.completion","usage":{"completion_tokens":9,"prompt_tokens":1,"total_tokens":10}}
```

## 3. See who used what

```bash
aiplex usage summary
```

```text
REQUESTS  ERRORS  DENIED  IN  OUT  COST
1         0       0       1   9    USD 0.000006 (estimated)

1 records scanned.
```

```bash
aiplex usage records
```

```text
TIME      MODEL/TOOL  CALLER               ACCESS POINT  STATUS  IN  OUT  COST
just now  mock-gpt    developer@localhost  local         ok      1   9    USD 0.000006 (estimated)

Record IDs and every counter: -o wide. One record in full: aiplex usage get <id>.
```

The call is in the [usage ledger](../library/usage-ledger.md). The cost is labelled estimated because the mock model has a placeholder price. Values that are not known are shown as `unknown`, not zero. Add `-o wide` to `aiplex usage records` for record IDs and every counter, and try `aiplex usage summary --by model` too.

## 4. Create an access point and a price

The guided commands create what you will want next. `--model` exposes a model under a client-facing name, and `--tool` exposes a tool connection:

```bash
aiplex access-points create team --model mock-gpt=local-models --tool local-tools
```

```text
Created access point team (revision 1): http://127.0.0.1:8080/ap/team

Next:
  aiplex connect team                 # client configuration
  aiplex access-points get team
```

Set a price for a model, in dollars per million tokens. Prices only estimate cost:

```bash
aiplex usage prices set gpt-4o-mini --input-per-mtok 0.15 --output-per-mtok 0.60
```

```text
Created price gpt-4o-mini (revision 1): input USD 0.15/Mtok, output USD 0.60/Mtok
```

To start from public list prices instead, `aiplex usage prices catalog search gpt-4o` lists entries from a pinned third-party catalogue. Nothing is priced from it until you import an entry, and imported cost stays estimated ([price catalogue](../library/usage-ledger.md#price-catalogue)).

## 5. Use the same access point for tools and clients

The access point `local` also serves the built-in MCP server at `http://127.0.0.1:8080/ap/local/mcp`. `aiplex connect local` prints ready-to-paste configuration: environment variables, a curl command and an MCP client configuration. See [Connect a client](./connect-client.md) and the repository's `examples/` directory, which has a script for each of these steps (OpenAI SDK, MCP tools, usage report) that `make examples-test` runs.

## Use a real provider

```bash
export OPENAI_API_KEY=...        # your own key
aiplex dev --provider openai --api-key-env OPENAI_API_KEY
```

`--provider` takes `openai`, `anthropic` or `gemini` (the default is the mock). `--api-key-env` names the environment variable that holds the key; the key itself is never accepted as a flag, printed or written to disk. `--model` chooses the model. Without the variable set, `aiplex dev` stops with an error that names it. The project has not run this path against a live provider, so treat the first run as a test; provider charges apply.

## Other flags

`--port` and `--console-port` change the ports, `--no-console` skips the Console, `--no-tools` skips the built-in MCP server's connection, `--detach` runs in the background, `--allow-origin` lets the local API call another upstream origin, `--otel` exports traces and metrics to a collector you run ([Observability](../library/observability.md)), `--context` names the CLI context it creates, and `--api-bin` points at an `aiplex-api` binary. Run `aiplex dev --help` for the current list.

If a port is taken, `aiplex dev` says so and suggests another with `--port` or `--console-port`. The CLI's options, output formats and exit codes are in [the CLI reference](../library/cli.md).

## What this does not establish

Everything is in memory and loopback-only, and prices are placeholders. Local success does not establish cloud routing, provider access, identity federation or quota behaviour. For the next steps, see [Local stack with Docker](./docker-stack.md) (emulators and Ory services), [Install the platform](./install-platform.md) and [Deploy a tool](./first-deploy.md).

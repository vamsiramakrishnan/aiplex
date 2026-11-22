---
title: Run AIPlex locally
sidebar_label: Run locally
description: Invoke an agent, call a model, run an MCP tool and connect a coding harness on your machine.
---

# Run AIPlex locally

Try agents, models, tools and harnesses on your machine. The local stack runs a mock agent, model provider and MCP server. You need Git, Go 1.25 or newer, and Make.

## 1. Build and start

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex
make build
export PATH="$PWD/bin:$PATH"
aiplex dev --detach
```

The API listens at `http://127.0.0.1:8080` and creates the `local` access point. The CLI signs you in as `developer@localhost`. Run `aiplex dev status` to find the API address, logs and Console availability.

:::warning
Local development mode accepts unauthenticated requests as admin. Keep it on loopback and avoid exposing it through a tunnel or proxy.
:::

## 2. Send a request

Invoke the local agent. It replies `mock-agent: hello`.

```bash
aiplex a2a invoke local-agent --operation query
```

Call the mock model. Then run an MCP tool and print a harness configuration:

```bash
aiplex dev try
aiplex mcp connections call local-tools echo --args '{"text":"hello"}'
aiplex connect local --client codex
```

The tool echoes `hello`. The Codex configuration shows the model endpoint and MCP server. The CLI only prints these settings unless you request a file edit.

`aiplex dev try` sends “Hello” through `/ap/local/v1/chat/completions` and looks up **that call** by its AIPlex request ID. The output includes the model's reply and a ledger row showing the model, caller, access point, input and output tokens, and cost. The seeded model has a placeholder price, so its cost is an estimate. For scripts, `aiplex dev try -o json` returns `reply` and `record`.

## 3. See who used what

```bash
aiplex usage records
aiplex usage summary --by model
```

Use the `aiplex usage get` command printed by `dev try` to inspect the full record. The [usage ledger](../library/usage-ledger.md) preserves missing token counts and prices as `unknown`.

To make the same request from an SDK or curl, export the local credentials in the calling shell:

```bash
eval "$(aiplex dev env)"
curl --fail-with-body "$OPENAI_BASE_URL/chat/completions" \
  -H "Authorization: Bearer $OPENAI_API_KEY" -H "Content-Type: application/json" \
  -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Hello"}]}'
```

The mock stack seeds four native endpoints. OpenAI Chat Completions uses `mock-gpt`, OpenAI Responses uses `mock-responses`, Anthropic Messages uses `mock-claude`, and Gemini generateContent uses `mock-gemini`. The exports point each SDK family at the same local access point.

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

## Stop the stack

```bash
aiplex dev stop
```

Stopping the stack discards local records and restores the previous CLI context. Exports from `aiplex dev env` last for the shell session.

## Use a real provider

After stopping the mock stack, set a provider key in a fresh shell. For example, with `GEMINI_API_KEY` already set:

```bash
bin/aiplex dev --provider gemini --api-key-env GEMINI_API_KEY
```

`--provider` takes `openai`, `anthropic` or `gemini` (the default is the mock). `--api-key-env` names the environment variable that holds the key; the key itself is never accepted as a flag, printed or written to disk. `--model` chooses the model. Without the variable set, `aiplex dev` stops with an error that names it. Live validation covers the Gemini Developer API and z.ai surfaces. First-party OpenAI and Anthropic endpoints still need live qualification. Real requests use the provider account and can incur charges. See [project status](../library/project-status.md).

## Other flags

`--port` and `--console-port` change the ports, `--no-console` skips the Console, `--no-tools` skips the built-in MCP server's connection, `--detach` runs in the background, `--allow-origin` lets the local API call another upstream origin, `--quota` turns QuotaPlex on for the mock surfaces and tools so request budgets count, `--quota-tariff` adds fixed mock USD rates per model and tool call ([Budgets](../library/budgets.md)), `--otel` exports traces and metrics to a collector you run ([Observability](../library/observability.md)), `--context` names the CLI context it creates, and `--api-bin` points at an `aiplex-api` binary. Run `aiplex dev --help` for the current list.

If a port is taken, `aiplex dev` says so and suggests another with `--port` or `--console-port`. The CLI's options, output formats and exit codes are in [the CLI reference](../library/cli.md).

## What this does not establish

Everything is in memory and loopback-only, and prices are placeholders. Local success does not establish cloud routing, provider access, identity federation or quota behaviour. For the next steps, see [Local stack with Docker](./docker-stack.md) (emulators and Ory services), [Install the platform](./install-platform.md) and [Deploy a tool](./first-deploy.md).

The local stack also runs the commands for request hooks, budgets, routing, the MCP Registry, the price catalogue and usage feedback. The list, with a link to each guide, is in the [local development guide](../library/local-development.md#gateway-features-on-the-local-stack).

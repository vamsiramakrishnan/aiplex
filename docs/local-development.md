# Local development

`aiplex dev` runs AIPlex on one machine, with no Docker, no cloud account and no network access
beyond the loopback interface. It starts the API in development mode, a mock model provider, a
mock MCP server and a local token issuer. It creates a small set of resources through the normal
APIs and keeps them in memory. This page covers the command, its flags and output, the developer
identity, real provider keys, where local state lives, what is not available, the loopback rule,
and the end-to-end harness and demo.

The implementation is in `cmd/aiplex-cli/cmd_dev.go` and `internal/devstack/`.

## What `aiplex dev` starts

| Part | Where it listens | Notes |
| --- | --- | --- |
| `aiplex-api` in development mode | `127.0.0.1`, port from `--port` (default `8080`) | In-memory stores. Started with the environment described in [the dev API environment](#the-dev-api-environment). |
| Local token issuer | `127.0.0.1`, a free port | Serves `/.well-known/jwks.json` and `/.well-known/openid-configuration`. Issues the developer token. |
| Mock model provider | `127.0.0.1`, a free port | Speaks the OpenAI Chat Completions, OpenAI Responses, Anthropic Messages and Gemini generateContent wires, one model on each. Replies are deterministic. |
| Mock MCP server | `127.0.0.1`, a free port | Three tools: `echo`, `add` and `reset_counter`. |
| Console | `127.0.0.1`, `--console-port` (default `9090`) | Served only when this binary carries a built Console. Otherwise the summary says how to build it. |

The Console forwards its requests to the API and sets the developer token on each one. The mock model,
the mock MCP server and the issuer run inside the `aiplex dev` process. The API reaches them over loopback HTTP.

## What it seeds

`aiplex dev` creates these resources through the API. It creates nothing else.

| Resource | Kind | Contents |
| --- | --- | --- |
| `local-models` | Model surface | With the mock provider: the OpenAI Chat Completions wire, model `mock-gpt`. With a real provider: the one surface for that provider, with its model. |
| `local-responses` | Model surface | Mock provider only. OpenAI Responses wire, model `mock-responses`. |
| `local-messages` | Model surface | Mock provider only. Anthropic Messages wire, model `mock-claude`. |
| `local-gemini` | Model surface | Mock provider only. Gemini generateContent wire, model `mock-gemini`. |
| `mock-gpt`, `mock-responses`, `mock-claude`, `mock-gemini` | Prices | Mock provider only, one per model. Placeholder rates, so estimated cost shows in usage. The rates are illustrative, not a provider's prices. A real provider gets no price; set one with `aiplex usage prices set`. |
| `local-tools` | MCP tool connection | The built-in MCP server, with its three tools. Not created with `--no-tools`. |
| `local` | Access point | Admits callers of kinds `user`, `agent` and `client`, with no subject filter. Exposes the models of the surfaces above and, unless `--no-tools`, the tool connection. |

The access point is at `$AIPLEX_URL/ap/local`. See [access points](access-points.md).

## Flags

From the cobra definitions in `cmd/aiplex-cli/cmd_dev.go`:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--port` | `8080` | API port on `127.0.0.1`. |
| `--console-port` | `9090` | Console port on `127.0.0.1`. |
| `--provider` | `mock` | Model provider: `mock`, `openai`, `anthropic` or `gemini`. |
| `--api-key-env` | empty | Name of the environment variable that holds the provider key. Not the key itself. Required with a real provider. |
| `--model` | per provider | Model to expose. Defaults: `mock-gpt` (mock), `gpt-4o-mini` (openai), `claude-haiku-4-5` (anthropic), `gemini-2.5-flash` (gemini). |
| `--api-bin` | empty | Path to the `aiplex-api` binary. |
| `--context` | `local` | CLI context to create for the local API. |
| `--no-console` | off | Do not serve the Console. |
| `--open` | off | Open the Console in a browser. |
| `--no-tools` | off | Do not create the tool connection. |
| `-d`, `--detach` | off | Run in the background. Output goes to `~/.aiplex/dev.log`. Stop it with `aiplex dev stop`. |
| `--allow-origin` | none | Extra upstream origin the local API may call, for example `https://mcp.example.com`. Repeatable. The loopback mock upstreams and the public OpenAI, Anthropic and Gemini APIs are always allowed. |
| `--otel` | off | OTLP/HTTP endpoint of a collector you run, for example `http://127.0.0.1:4318`. Sets `OTEL_EXPORTER_OTLP_ENDPOINT` for the local API. Without it nothing is exported. See [observability](observability.md). |

`aiplex dev` has four subcommands. `env`, `status` and `stop` take no flags beyond the global ones:

| Command | What it does |
| --- | --- |
| `aiplex dev env` | Prints shell exports for the running local API, for `eval "$(aiplex dev env)"`. |
| `aiplex dev status` | Shows whether `aiplex dev` is running, its pid, the API URL, the access point, the models, the Console and the API log. Exits 1 when none is running. |
| `aiplex dev stop` | Stops the running `aiplex dev` and its local API. Exits 1 when none is running. |
| `aiplex dev token --user NAME [--role none\|deployer\|admin] [--groups a,b]` | Prints a token for another local caller, `NAME@localhost`, on stdout only, so it can be captured. It is valid for one hour and works only with the local API that issued it. `--user` is required. See [other local callers](#other-local-callers). |

Only one `aiplex dev` runs at a time. A second start is refused, and it exits with code 5 (conflict):

```text
Error: aiplex dev is already running (pid 13956) at http://127.0.0.1:18571
  Use it: eval "$(aiplex dev env)"
  Or stop it first: aiplex dev stop
```

When the default port is taken by another process, the start stops with
`port 18080 on 127.0.0.1 is already in use; stop what is using it or pass --port`.

### Finding `aiplex-api`

The command looks for the API binary in this order:

1. `--api-bin`.
2. The `AIPLEX_API_BIN` environment variable.
3. A file named `aiplex-api` next to the `aiplex` executable.
4. When run inside the AIPlex source tree, a fresh build of `./cmd/aiplex-api`. The binary is written to
   `~/.aiplex/bin/aiplex-api`.
5. `aiplex-api` on `PATH`.

If none is found, the command stops and says how to install or build it.

## Real provider keys

Use a real provider with `--provider` and `--api-key-env`:

```sh
export OPENAI_API_KEY=...          # set in your shell, not in a file
aiplex dev --provider openai --api-key-env OPENAI_API_KEY
```

The mechanism, from `cmd/aiplex-cli/cmd_dev.go` and `internal/devstack/stack.go`:

- `--api-key-env` takes the name of an environment variable. A value that contains `=`, or that is not
  a valid variable name, is refused.
- `--api-key-env` is refused with `--provider mock`. It requires `openai`, `anthropic` or `gemini`.
- The named variable must be set and non-empty. Otherwise the command stops.
- The key is passed to the API process in its environment, under the development secret name
  `AIPLEX_TOOL_SECRET_DEV_PROVIDER_KEY`. The surface refers to it as the secret `dev-provider-key`.
- The key is not printed, is not accepted as a flag value and is not written to disk.
- The upstream origin added to the outbound allowlist is the provider's HTTPS origin.

Auth wiring per provider, as the seeded surface sets it:

| Provider | Wire | Upstream auth header |
| --- | --- | --- |
| `openai` | OpenAI Chat Completions | `Authorization: Bearer` |
| `anthropic` | Anthropic Messages | `x-api-key` |
| `gemini` | Gemini GenAI | `x-goog-api-key` |

With a real provider, no price is seeded. The summary says so. Set contracted rates with
`aiplex usage prices set <model> --input-per-mtok <price> --output-per-mtok <price>`, or with a full
specification from a file, `aiplex usage prices set -f <price.json>`. To start from public list prices instead,
`aiplex usage prices catalog search <text>` lists entries from the bundled third-party catalogue, and
`aiplex usage prices catalog import <provider>/<model> --dry-run` shows what an import would write. Nothing is
imported until you run it without `--dry-run`, and imported cost stays `estimated`
([price catalogue](usage-ledger.md#price-catalogue)).

## The developer identity

`aiplex dev` issues one token for one local developer:

| Field | Value |
| --- | --- |
| Subject | `developer` |
| Email | `developer@localhost` |
| Name | `Local developer` |
| Group | `aiplex-local-admins`, bound to the `admin` role by a role-binding file the command writes |
| Scopes | `llm:*`, `mcp:*`, `a2a:*`, `skill:*`, `aiplex:*` |
| Audience | `aiplex-local` |
| Lifetime | 12 hours |
| Signed by | The local issuer. Its signing key is generated when the stack starts and lives only in memory. |

The token is stored encrypted in `~/.aiplex/credentials.json`, under the context name (default `local`).
The command makes that context current. When the command stops, it removes the token and restores the
previously current context, if there was one. The token is accepted only by the local API that issued it.

## Other local callers

`aiplex dev token` mints a token for a second caller from the same local issuer. The token is for
`NAME@localhost`, with subject `NAME`, and lasts one hour. Only your own developer token can ask for one,
and only from this machine. Use it as `AIPLEX_TOKEN`:

```sh
AIPLEX_TOKEN=$(aiplex dev token --user bob) aiplex whoami
AIPLEX_TOKEN=$(aiplex dev token --user dana --role deployer) aiplex access-points list
```

| `--role` | What the caller gets |
| --- | --- |
| `none` (default) | No role. It sees only the access points that admit it. Admission is not a grant: to call a model it also needs `llm:model:<model>` and `llm:surface:<surface>:*`, set by you with `aiplex agents user-scopes set <principal-id> --scopes ...` (see below). Without them the call is refused with `403` and a message that lists what is missing. |
| `deployer` | Configuration screens and commands, without admin-only actions. |
| `admin` | The same role as the developer. |

To let a no-role caller use the seeded model through an access point that admits it (the `aiplex dev` summary shows
the same steps):

```sh
aiplex access-points create team --model mock-gpt=local-models --allow-users bob@localhost
BOB=$(aiplex dev token --user bob)        # stderr prints bob's principal ID
aiplex agents user-scopes set PRINCIPAL_ID --scopes llm:model:mock-gpt,llm:surface:local-models:*
curl -s "$AIPLEX_URL/ap/team/v1/chat/completions" -H "Authorization: Bearer $BOB" \
  -H "Content-Type: application/json" -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Hi"}]}'
```

`user-scopes set` replaces the whole list. With only `llm:model:mock-gpt` the call is still refused (the surface
grant is missing); this is covered by `tests/e2e/model_grants_test.go`. Before, the refusal said only
`operation denied`; it now names the missing grants and the command.

`--groups a,b` adds identity-provider groups to the token, for access points that admit a group. The caller's
principal ID is printed by `aiplex whoami` when run with that token.

`aiplex connect <access-point>` works for any caller the access point admits. A caller without an operator role
gets a reduced response: only its own caller kind in `auth.caller_kinds` and the auth step for that kind, and no
notes that name internal resource IDs or other subjects. An access point that does not admit the caller answers
as if it did not exist. See [access points](access-points.md#routes).

## Credentials in development mode

Development mode treats a request with no credential as the local developer (admin). A request to `/api/…`
that carries a credential which does not verify (a mistyped, expired or foreign token) is refused with
`401 INVALID_CREDENTIAL`, and is not served as the developer:

```text
{"code":"INVALID_CREDENTIAL","message":"the credential you sent was not accepted (...). Send a valid token, or send none to use the anonymous local developer. With aiplex dev, run: eval \"$(aiplex dev env)\"", ...}
```

This rule covers `/api/…` and `/events/stream`. A request with no credential is still served as the developer.

## The dev API environment

The command starts the API with these variables. They are set by `internal/devstack/stack.go`:

| Variable | Value |
| --- | --- |
| `AIPLEX_DEV_MODE` | `true` |
| `HOST` | `127.0.0.1` |
| `PORT` | The `--port` value |
| `JWT_ISSUER`, `JWT_JWKS_URL`, `JWT_AUDIENCE` | The local issuer, and audience `aiplex-local` |
| `IDENTITY_WORKFORCE` | `true` |
| `WORKFORCE_POOL_ID` | `aiplex-local` |
| `AIPLEX_ROLE_BINDINGS_FILE` | The generated role-binding file |
| `AIPLEX_CONNECTIONS_FILE` | The generated connections file. Its `network` block allows the loopback mock upstreams (`allow_loopback`). |
| `AIPLEX_PUBLIC_URL` | The local API URL |
| `AIPLEX_TENANT_ID` | `development` |
| `LOG_LEVEL` | `info` |
| `CONSOLE_ORIGINS` | The Console's loopback origins, when the Console is served |

The command removes the variables that would change what the local API connects to, before it sets the
ones above. The removed list is in `filterEnv` (`internal/devstack/stack.go`). It includes
`AIPLEX_TENANT_ID`, `AIPLEX_QUOTA_FILE`, `AIPLEX_MODULES`, `AIPLEX_IDENTITY_PROVIDERS_FILE`,
`HYDRA_ADMIN_URL`, the Firestore and GCP project variables, `KUBERNETES_SERVICE_HOST`, the TLS file
variables and `CONSOLE_ORIGINS`. Other variables from your shell are inherited by the API.

## `aiplex dev env`

`aiplex dev env` prints shell exports for the running local API. It reads the state file, checks that
`/healthz` answers, and reads the token from the credentials file. Then it prints:

```sh
export AIPLEX_URL='http://127.0.0.1:8080'
export AIPLEX_TOKEN='...'
export OPENAI_BASE_URL='http://127.0.0.1:8080/ap/local/v1'
export OPENAI_API_KEY="$AIPLEX_TOKEN"
```

The provider-specific variables depend on the provider:

| Provider | Variables printed |
| --- | --- |
| `mock` (the default) | All of them: `OPENAI_BASE_URL` (the access point's `/v1`) and `OPENAI_API_KEY`; `ANTHROPIC_BASE_URL` (the access point) and `ANTHROPIC_API_KEY`; `GOOGLE_GEMINI_BASE_URL` (the access point) and `GEMINI_API_KEY`. Every key is set to `$AIPLEX_TOKEN`. |
| `openai` | `OPENAI_BASE_URL` and `OPENAI_API_KEY` |
| `anthropic` | `ANTHROPIC_BASE_URL` and `ANTHROPIC_API_KEY` |
| `gemini` | `GOOGLE_GEMINI_BASE_URL` and `GEMINI_API_KEY` |

Use it as `eval "$(aiplex dev env)"` in another terminal. It fails with a message when no local AIPlex is
running, when the API does not answer, or when no token is stored for the context.

## The summary the command prints

After the stack is ready, `aiplex dev` prints a summary. Its lines are:

| Line | Meaning |
| --- | --- |
| `API` | The URL, with `development mode, in memory, loopback only`. |
| `You` | The developer email, as admin, with the token lifetime. |
| `Models` | The models on the access point and the wire each one speaks. With the mock provider, the four mock models. For a real provider, its one model, and the environment variable the key came from. |
| `Tools` | The tool connection and its tools. Absent with `--no-tools`. |
| `Access point` | The access point name and its URL. |
| `Price` | The placeholder prices for the mock models. For a real provider, the line says none is seeded. |
| `Quota` | `off`. QuotaPlex needs an operator quota file, which the local API does not get. |
| `Telemetry` | Present only with `--otel`: the collector URL the local API exports traces and metrics to. Without it, nothing is exported. |
| `CLI context` | The context that is current, and the one that is restored on stop. |
| `Console` | The Console URL, or why it is not served. |
| `API log` | The log file path. |
| `WARNING` | Development mode serves unauthenticated requests as admin. Do not expose, tunnel or proxy the API. |

After the summary, `Ready in 0.1s. Try next` lists numbered steps, each with real commands:

1. Send a chat request through the access point, after `eval "$(aiplex dev env)"`. The request is the
   OpenAI Chat Completions example for `mock-gpt`.
2. `aiplex usage records`, to see what it cost and who made it.
3. `aiplex connect local` for client configuration, and `aiplex dev status` to see the Console address. To write Claude Code's configuration into your home directory after a diff, `aiplex connect local --client claude-code --write` (keeps a backup; `aiplex connect --undo --client claude-code` restores it). See [harness onboarding](harness-onboarding.md). The written settings point at the local access point, which exists only while `aiplex dev` runs and is gone after a restart, so undo the write when you stop.
4. With the mock provider, a second caller with no role: create `team`, which admits `bob@localhost`, mint bob's token
   with `aiplex dev token --user bob`, grant bob the model and its surface with `aiplex agents user-scopes set`, then call
   the model with bob's token. Admission alone is not enough for a model call; see
   [other local callers](#other-local-callers).

A background run (`--detach`) prints the same summary, then `Running in the background`, the pid and the log file.
The summary states the Console line in one of two ways. When the binary carries a built Console, it gives the
Console URL. When it does not (a source build without Node), it says so and gives the `make console && make build` command.

The guided commands used after the summary are `aiplex access-points create` (a model and a tool, exposed on one URL) and
`aiplex usage prices set <model> --input-per-mtok <price> --output-per-mtok <price>`. See [access points](access-points.md)
and [the usage ledger](usage-ledger.md#cli).

### Output of a background run

```text
  Running in the background. Stop it with: aiplex dev stop
  Background pid 14557; its output goes to /root/.aiplex/dev.log
```

### Errors in the local stack

```text
Error: access point "locl" not found
  Did you mean: local
  List them with: aiplex access-points list
  aiplex dev keeps state in memory: anything created before it restarted is gone.
```

Each CLI error names the cause, gives a hint and exits with a fixed code. The codes are in [the CLI reference](cli.md#exit-codes).
With `--debug`, the raw API error is printed too.

## Where state and logs live

Everything is under `~/.aiplex/` (`cliconfig.Dir`), except the generated configuration:

| Path | Contents | Lifetime |
| --- | --- | --- |
| `~/.aiplex/dev.json` | URL, context, access point, model, protocol, process ID, Console URL. No secrets. | Written at start, removed on stop. |
| `~/.aiplex/dev.log` | The output of a `--detach` run: the summary and the background pid. | Truncated at each start. Kept after stop. |
| `~/.aiplex/dev-api.log` | The API's output. | Truncated at each start. Kept after stop. |
| `~/.aiplex/credentials.json` | The developer token, encrypted, under the context name. | Removed on stop. |
| `~/.aiplex/config.json` | CLI contexts. | Context restored on stop. |
| `~/.aiplex/bin/aiplex-api` | The API binary built from source. | Kept until rebuilt. |
| A temporary `aiplex-dev-*` directory | Generated connections file and role-binding file. No secrets. | Removed on stop. |

The API keeps no state across runs. Stores are in memory and are discarded on stop. The in-memory usage ledger
keeps at most 100,000 records per tenant (`pkg/usageledger/memory.go`).

## What is not available locally

The summary and the environment filter define these limits:

| Area | Local behavior |
| --- | --- |
| Quota | Off. The quota file variable is removed from the API's environment. |
| Persistence | None. Stores are in memory. The stack does not use Firestore, AlloyDB or a cloud secret store. |
| Identity provider | Only the local issuer. Hydra and Kratos are not used. Agent registrations are kept in the local registry only, because `HYDRA_ADMIN_URL` is not set. |
| Federation | The stack does not set the agent or user federation variables. |
| Modules | The API uses its default module list. The `AIPLEX_MODULES` variable is removed from the inherited environment. |
| Kubernetes, mesh, gateway | None. There is no Kubernetes deployment and no Envoy gateway. |
| Models | The mock replies are deterministic and are not model output. Real output needs a real provider and key. |
| Prices | Placeholder rates for the four mock models only. |
| Console | Only if the binary was built with a Console. Otherwise build it with `make console && make build` (Node 22). |

## Loopback only

Development mode binds to loopback:

- `AIPLEX_DEV_MODE=true` with `HOST` unset sets `HOST` to `127.0.0.1` (`internal/config/config.go`).
- In development mode, `HOST` must be `127.0.0.1`, `::1` or `localhost`. Any other value stops the API at
  startup.
- Development mode also admits requests with no verified identity. The summary prints the warning shown
  in [the summary](#the-summary-the-command-prints).

Do not run a development API on a shared host, and do not put a proxy or tunnel in front of it.

## End-to-end harness

The end-to-end suite is in `tests/e2e/`. It is behind the `e2e` build tag. Each test starts the real
`aiplex-api` as a separate process, uses the real `aiplex` CLI, and talks to the API over HTTP. Every
upstream is a local mock on loopback. The suite needs no network, Docker or cloud account.

```sh
make e2e          # go test -tags e2e ./tests/e2e/ -count=1 -v
make e2e-race     # the same, with aiplex-api built with the race detector (E2E_RACE=1)
```

The harness builds `aiplex-api` and `aiplex` from this repository into a temporary directory when it
starts. A test that reports a data race in the API log fails.

| Variable | Effect |
| --- | --- |
| `E2E_RACE=1` | Builds the API with `-race`. Used by `make e2e-race`. |
| `E2E_KEEP_LOGS=1` | Keeps each test's API log. The log is written to the system temporary directory as `aiplex-e2e-<test name>.log`. A failed test always keeps its log. |

The suite has 14 top-level test functions, in files such as `authz_test.go`, `models_test.go`,
`usage_test.go`, `identity_test.go` and `restart_test.go`. `devcmd_test.go` checks how `aiplex dev` handles real provider key flags.

## Demo

`make demo` runs `tools/demo.sh`. The script:

1. Builds `aiplex` and `aiplex-api` into `$DEMO_BIN` (default `./bin` in the repository).
2. Starts `aiplex dev --port $DEMO_PORT --no-console` (default port `18080`) with an isolated `HOME`.
3. Evaluates `aiplex dev env`.
4. Sends a chat completion and a streaming chat completion through the access point.
5. Calls the MCP server through the access point: `initialize`, `tools/list` and `tools/call`.
6. Runs `aiplex usage records` and `aiplex usage summary --by model`.
7. Runs `aiplex connect local` and checks that its curl snippet is present.
8. Stops `aiplex dev` with Ctrl+C.

It exits non-zero at the first failed step. On failure it prints the `aiplex dev` output and the tail of
the API log. It needs Go and curl. Python is used only to read the connect output, and a sed fallback
covers its absence.

## Limits

- Local state is lost on stop. Nothing is written back to a cloud or to a database.
- A second `aiplex dev` is refused while one is running (exit 5). Stop the first with `aiplex dev stop`, or
  use `aiplex dev env` to reach the running one. A default port held by another process is reported with a
  message to stop that process or pass `--port`.
- The Console runs only when built into the binary. Without it, `--console-port` has no effect.
- `aiplex dev` keeps no state after a stop, so an access point or price created before a restart is gone.
  Run the guided commands again after `aiplex dev` starts.
- The mock model is for testing wiring, not for quality or behavior of a real model.
- The developer token expires after 12 hours, and the API that issued it stops with the command.

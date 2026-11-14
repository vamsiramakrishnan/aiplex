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
| Keyed MCP server | `127.0.0.1`, a free port | The same three tools, but it answers `401` to a request without its API key (header `X-Api-Key`). The key is random for each run. |
| Mock agent runtime | `127.0.0.1`, a free port | One agent on the Google Agent Runtime query wire (`POST /v1/projects/aiplex-local/locations/local/reasoningEngines/mock-agent:query`). Deterministic: the reply is a function of the request. It answers `401` to a request without its bearer token, which is random for each run. |
| Federation issuers | `127.0.0.1`, one free port | Serves only the public keys (JWKS) of three stand-in identity providers: `idp`, `workload` and `unconfigured`. See [identity federation](#identity-federation). |
| Branchyard stand-in | `127.0.0.1`, a free port | Serves only `GET /.well-known/jwks.json`: the Ed25519 public key of a stand-in Branchyard server (Branchyard is a separate product that runs coding agents on git branches). Its issuer is this listener's URL. See [a Branchyard turn token](#a-branchyard-turn-token). |
| Console | `127.0.0.1`, `--console-port` (default `9090`) | Served only when this binary carries a built Console. Otherwise the summary says how to build it. |

The Console forwards its requests to the API and sets the developer token on each one. The mock model,
the mock MCP servers and the issuers run inside the `aiplex dev` process. The API reaches them over loopback HTTP.

## What it seeds

`aiplex dev` creates these resources through the API. It creates nothing else.

| Resource | Kind | Contents |
| --- | --- | --- |
| `local-models` | Model surface | With the mock provider: the OpenAI Chat Completions wire, model `mock-gpt`. With a real provider: the one surface for that provider, with its model. |
| `local-responses` | Model surface | Mock provider only. OpenAI Responses wire, model `mock-responses`. |
| `local-messages` | Model surface | Mock provider only. Anthropic Messages wire, model `mock-claude`. |
| `local-models-b` | Model surface | Mock provider only. Like `local-models`, on the second mock provider (`Mock B`), on no access point: a second backend for trying [routing](reliability-and-routing.md). |
| `local-gemini` | Model surface | Mock provider only. Gemini generateContent wire, model `mock-gemini`. |
| `mock-gpt`, `mock-responses`, `mock-claude`, `mock-gemini` | Prices | Mock provider only, one per model. Placeholder rates, so estimated cost shows in usage. The rates are illustrative, not a provider's prices. A real provider gets no price; set one with `aiplex usage prices set`. |

`aiplex dev` also starts a second mock provider (printed as `Mock B`; its replies start
with `[b]`) with no surface of its own, for trying [reliability and
routing](reliability-and-routing.md#trying-it-locally): create surfaces on both mocks and list
them as backends of one model. Both mocks fail on demand for model names that contain `fault`
(for example `gpt-fault-s503-n2` fails twice with 503).
| `local-tools` | MCP tool connection | The built-in MCP server, with its three tools. Not created with `--no-tools`. |
| `local-tools-keyed` | MCP tool connection | The keyed MCP server, with API-key authentication whose key is the secret reference `dev-tool-key`. Not created with `--no-tools`. It is not on the `local` access point; add it with `--tool local-tools-keyed` when you create one. See [the keyed tool connection](#the-keyed-tool-connection). |
| `local-agent` | Agent connection | Provider `google-agent-runtime`, endpoint the mock agent runtime over plain `http` on loopback, identity profile `dev-agent-identity` (a `bearer-env` profile: it names the variable `AIPLEX_DEV_AGENT_TOKEN`, whose random value only the API process and the mock agent hold). Not on the `local` access point. See [agents](#agents-a2aplex). |
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
| `--model` | per provider | Model to expose. Defaults: `mock-gpt` (mock), `gpt-4o-mini` (openai), `claude-haiku-4-5` (anthropic), `gemini-3.1-flash-lite` (gemini; the Gemini Developer API answers 404 for `gemini-2.5-flash`). |
| `--api-bin` | empty | Path to the `aiplex-api` binary. |
| `--context` | `local` | CLI context to create for the local API. |
| `--no-console` | off | Do not serve the Console. |
| `--open` | off | Open the Console in a browser. |
| `--no-tools` | off | Do not create the tool connection. |
| `-d`, `--detach` | off | Run in the background. Output goes to `~/.aiplex/dev.log`. Stop it with `aiplex dev stop`. |
| `--allow-origin` | none | Extra upstream origin the local API may call, for example `https://mcp.example.com`. Repeatable. The loopback mock upstreams and the public OpenAI, Anthropic and Gemini APIs are always allowed. |
| `--otel` | off | OTLP/HTTP endpoint of a collector you run, for example `http://127.0.0.1:4318`. Sets `OTEL_EXPORTER_OTLP_ENDPOINT` for the local API. Without it nothing is exported. See [observability](observability.md). |
| `--quota` | off | Turns QuotaPlex on for the mock surfaces and tools, so budgets that count requests work. Mock provider only; the counters reset when the API restarts. See [budgets](budgets.md). |

`aiplex dev` has four subcommands. `env`, `status` and `stop` take no flags beyond the global ones:

| Command | What it does |
| --- | --- |
| `aiplex dev env` | Prints shell exports for the running local API, for `eval "$(aiplex dev env)"`. |
| `aiplex dev status` | Shows whether `aiplex dev` is running, its pid, the API URL, the access point, the models, the Console and the API log. Exits 1 when none is running. |
| `aiplex dev stop` | Stops the running `aiplex dev` and its local API. Exits 1 when none is running. |
| `aiplex dev token --user NAME [--role none\|deployer\|admin] [--groups a,b]` | Prints a token for another local caller, `NAME@localhost`, on stdout only, so it can be captured. It is valid for one hour and works only with the local API that issued it. `--user` is required. See [other local callers](#other-local-callers). |

Only one `aiplex dev` runs at a time. A second start is refused, and it exits with code 5 (conflict):

```text
Error: aiplex dev is already running (pid <pid>) at http://127.0.0.1:8080
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

The seeded Anthropic surfaces accept the `anthropic-beta` values that Claude Code 2.1.322 sends (for example `claude-code-20250219`, `interleaved-thinking-2025-05-14` and `context-management-2025-06-27`); any other beta value is refused, so a later Claude Code version may need more values added to the surface's `provider.anthropic_betas`. To use z.ai or another server with your own path prefix, add `--allow-origin` for its origin and create a surface with `aiplex llm surfaces create` (an `openai-compatible` surface takes `base_path`; the `anthropic` provider does not).

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

`--issuer` selects who signs the token; see [identity federation](#identity-federation). `--groups a,b` adds identity-provider groups to the token, for access points that admit a group. The caller's
principal ID is printed by `aiplex whoami` when run with that token.

`aiplex connect <access-point>` works for any caller the access point admits. A caller without an operator role
gets a reduced response: only its own caller kind in `auth.caller_kinds` and the auth step for that kind, and no
notes that name internal resource IDs or other subjects. An access point that does not admit the caller answers
as if it did not exist. See [access points](access-points.md#routes).

## Identity federation

The API runs with `AIPLEX_USER_FEDERATION=enabled` and `AIPLEX_AGENT_FEDERATION=enabled`, so a user identity
provider or an agent identity binding that you configure authenticates live requests, as in
[user identity federation](user-identity-federation.md) and [agent identity federation](agent-identity-federation.md).
The CLI and the Console no longer say that live authentication is not enabled.

The stack runs three stand-in identity providers so you can try this without an external one. They are not
trusted until you configure them, exactly as a real provider is not. Their issuer names are `https` names that
are only compared with the `iss` claim (identity presets accept `https` issuers only; the names end in
`.invalid` and are never looked up). Their keys are served on one `127.0.0.1` listener, which serves nothing else.
A fourth stand-in, `branchyard`, plays a Branchyard server (Branchyard is a separate product that runs coding
agents on git branches; its server signs a short-lived token for each turn) and has a listener of its own; see
[a Branchyard turn token](#a-branchyard-turn-token).

| `--issuer` | Stands for | Audience | Configure it with |
| --- | --- | --- | --- |
| `local` (default) | The issuer the API trusts statically: the developer and `--user` callers | `aiplex-local` | Nothing; it is always on. |
| `idp` | A workforce identity provider | `aiplex-dev-users` | `aiplex identity user-providers apply` |
| `workload` | A workload identity provider, for agents | `aiplex-dev-agents` | `aiplex agents identity bind` |
| `unconfigured` | An issuer nobody configured | `aiplex-dev-users` | Never. Its tokens are refused with `401`. |
| `branchyard` | A Branchyard server's model gateway (EdDSA turn tokens) | `aiplex-dev-agents` | `aiplex agents identity bind --preset branchyard`. See [a Branchyard turn token](#a-branchyard-turn-token). |

`aiplex dev token --issuer idp` prints the token on stdout and, on stderr, the issuer, JWKS address and audience
and the exact provider to apply. Applying it and using the token:

```sh
aiplex dev token --user alice --issuer idp --groups eng      # stderr shows the provider to apply
aiplex identity user-providers apply -f - <<'EOF'
id: local-idp
preset: oidc
inputs:
  issuer: https://idp.aiplex-dev.invalid
  jwks_url: http://127.0.0.1:PORT/idp/.well-known/jwks.json   # copy the real one from the token's stderr
  audience: aiplex-dev-users
  groups_from: claim
  group_claim: groups
EOF
AIPLEX_TOKEN=$(aiplex dev token --user alice --issuer idp) aiplex whoami   # alice@idp.localhost, no role
```

A workload token is bound to an agent the same way:

```sh
aiplex agents register tutor --name Tutor --scopes 'llm:model:mock-gpt,llm:surface:local-models:*'
aiplex dev token --user ci-1 --issuer workload 2>&1 >/dev/null | grep 'aiplex agents identity bind' | sh
AIPLEX_TOKEN=$(aiplex dev token --user ci-1 --issuer workload 2>/dev/null) aiplex whoami
```

The second line runs the bind command that `aiplex dev token --issuer workload` prints on stderr (it names the agent
`tutor`, the issuer, this stack's JWKS address, the audience and the subject), so you need not copy the JWKS address
by hand. Without the pipe, the printed line is
`aiplex agents identity bind tutor ci --preset oidc --input issuer=... --input jwks_url=... --input audience=... --input subject=ci-1`.
`tests/e2e/devfederation_steps_test.go` runs this block exactly as written.

### A Branchyard turn token

A turn is one step of a Branchyard agent on a branch; a turn token is the short-lived token a Branchyard server's
model gateway mints for one turn and presents to AIPlex. A Branchyard tenant is Branchyard's own grouping of its
users and repositories, not an AIPlex tenant. The `branchyard` stand-in plays a Branchyard server's model gateway, so the
[`branchyard` preset](agent-identity-federation.md#branchyard) can be tried without one. Unlike the other stand-ins it
has its own `127.0.0.1` listener, because the preset derives the key set from the issuer: its issuer is
`http://127.0.0.1:PORT` (a Branchyard server without TLS on loopback has an issuer of that form) and its key is at
`http://127.0.0.1:PORT/.well-known/jwks.json`. The preset accepts plain `http` only on a loopback address, and the
API reaches loopback only in development mode. The stand-in serves nothing else, and the API does not trust it
until an agent is bound to it.

`aiplex dev token --issuer branchyard --tenant NAME` takes a Branchyard tenant instead of `--user` and prints a turn
token shaped as Branchyard mints one: `alg` `EdDSA`, `sub` `branchyard:NAME`, `by_purpose` `aiplex`, `by_tenant`
(the Branchyard tenant), `by_branch`, `by_turn`, a fresh `jti`, and one hour of life, the most the preset accepts. On stderr it prints the commands to register an agent and
bind it. Binding the agent and using the token:

```sh
aiplex agents register branchyard-agent --name 'Branchyard acme' --scopes 'llm:model:mock-gpt,llm:surface:local-models:*'
aiplex dev token --issuer branchyard --tenant acme 2>&1 >/dev/null | grep 'aiplex agents identity bind' | sh
AIPLEX_TOKEN=$(aiplex dev token --issuer branchyard --tenant acme 2>/dev/null) aiplex whoami
```

The printed bind line is
`aiplex agents identity bind branchyard-agent branchyard --preset branchyard --input server=http://127.0.0.1:PORT --input tenant=acme --input audience=aiplex-dev-agents`.
To call a model through the access point `local`, grant the principal ID that stderr shows
(`aiplex agents user-scopes set <principal-id> --scopes 'llm:model:mock-gpt,llm:surface:local-models:*'`) and send the
token as `Authorization: Bearer`. `tests/e2e/identity_branchyard_test.go` does that, checks that the ledger record
names the agent, and checks that a token with the wrong `by_purpose`, `aud`, `sub`, an expired `exp`, a lifetime
over an hour or an `iss` with a trailing slash is refused with `401`. `TestDevBranchyardDocumentedSteps` in
`tests/e2e/identity_branchyard_steps_test.go` runs the block above as written.
`--user`, `--groups` and `--role` are refused with `--issuer branchyard`, and `--tenant` (a Branchyard tenant) with
any other issuer.

What a federated token is, and is not:

- It is a different caller from the developer: its subject, principal ID and email (`NAME@idp.localhost`) differ.
- It never has a role by itself. `--role` is refused with `--issuer` other than `local`, and the groups of an
  `idp` token do not match the role bindings the stack writes (they name the local issuer's provider and pool),
  so a token that claims the `aiplex-local-admins` group is still a roleless user. An admin route answers
  `403 ROLE_REQUIRED`. To give a federated user a role, add a role binding for its provider.
- It reaches a model only with a grant: `aiplex agents user-scopes set <principal-id> --scopes ...`. The principal
  ID is printed on stderr and by `aiplex whoami`.
- A token from an issuer that is not configured, for another audience, signed by another key, or expired is
  refused with `401`. This is covered by `tests/e2e/devfederation_test.go`.
- Your own developer token and the anonymous developer path are unchanged. Only the developer token can ask
  the local issuer for a federated token, and only from this machine.

What is not covered: the stand-in providers sign with an in-memory key that exists only while `aiplex dev` runs,
and there is no directory (group lookup) provider. Real identity providers, their metadata and their group
resolution have not been tried through this stack. The `branchyard` stand-in signs tokens of the shape the
integration contract gives; a running Branchyard server has not been tried through this stack. To turn federation off in a test, set
`AIPLEX_USER_FEDERATION=disabled` and `AIPLEX_AGENT_FEDERATION=disabled` in `devstack.Options.Env`: a variable there replaces
the stack's default entry (`mergeEnv`, no duplicates), and `TestDevStackFederationCanBeDisabled` checks that the presets
then report `live_authentication: false`. `aiplex dev` itself has no flag for it.

## Agents (A2APlex)

`local-agent` is an agent connection to the mock agent runtime. Try it, in another terminal after `eval "$(aiplex dev env)"`:

```sh
aiplex a2a connections list
aiplex a2a invoke local-agent --operation query --payload '{"input":{"message":"hello"}}'
aiplex usage records --plane a2aplex
aiplex a2a delegate --caller research-agent --callee local-agent --task summarise
aiplex a2a delegations list --agent research-agent
```

The invoke returns `{"output":{"agent":"mock-agent","reply":"mock-agent: you said hello"}}`. The call goes through the
normal path: the grant `a2a:agent:local-agent:query` is checked (the developer has `a2a:*`), the identity profile supplies the
bearer token, the outbound endpoint policy allows the loopback origin, and a usage record is written (plane `a2aplex`,
provider `google-agent-runtime`, operation `query`, status and HTTP status of the upstream). The mock reports no token counts or
price, so the record's usage is `null` and its cost basis is `unknown`: unknown is shown as unknown, not `0`.

What the mock agent is and is not: it is a fake that speaks one operation of one provider's wire, for trying the
connection, grant and record path. It is not a model, runs no tools and makes no calls of its own. A reply says nothing about
how a real agent runtime behaves. `stream-query` and the other providers' operations are not served (the mock answers `404`,
and the record shows that).

A delegation recorded with `aiplex a2a delegate` is reported history: the record carries who reported it (`reported_by`), and
it is not proof that the caller was authorized to call the callee, nor that a call happened. It grants nothing: a caller with no
`a2a:agent:local-agent:query` grant is still refused after a delegation names it. A pending delegation has no duration
(unknown), not `0`.

The origin rule. An agent connection's endpoint is a pinned `https` origin everywhere except development mode. In development mode
only, because the local API is started with the endpoint policy's `allow_loopback` (the same setting that lets the API call
the mock model and tool servers; the API refuses to start with it outside development mode), an endpoint may also be plain `http` on
a literal loopback host: `127.0.0.0/8`, `::1` or `localhost`. It is not accepted for `0.0.0.0`, a link-local or metadata address
(`169.254.169.254`), a name that only resolves to loopback (`127.0.0.1.nip.io`), a URL with userinfo, a path or a query. A redirect from the agent is never
followed. `pkg/cloudagents/origin_test.go`, `internal/api/agent_connection_origin_test.go` and `tests/e2e/devagent_test.go` cover both modes.

When an agent runtime answers `401` or `403`, it has refused the credential the API holds for the connection, not the caller's
token (the caller never supplies it). The API answers `502` with the code `AGENT_CREDENTIAL_REJECTED`, a message that names the
connection, and none of the runtime's body; the ledger records the call as an error with class `upstream_auth`, as for model
surfaces, and the CLI says your token was accepted. Try it by changing the mock's token (`MockAgent.RotateToken`, used by
`TestDevAgentRejectingTheGatewayCredential`).

Plain `http` to a loopback name is also checked at connection time: for an origin such as `http://localhost:PORT`, every
address the name resolves to must be loopback, so a resolver answering a public address for `localhost` is refused
(`TestPlainHTTPOriginNeedsAllLoopbackAnswers`). The `https` rules are unchanged.

To point a connection at an agent of your own on loopback, apply an `agent-connection` whose endpoint is its `http://127.0.0.1:PORT`
origin and whose `identity_ref` names a profile the stack defines; the stack defines only `dev-agent-identity`, whose
`allowed_origins` hold the mock agent's origin, so a different origin is refused (`aiplex dev` has no way to add an identity
profile). Real cloud agent runtimes (Google, AWS, Foundry, Copilot Studio) and Copilot token refresh are not available locally.

`aiplex a2a agents` lists deployed A2A agent instances, not agent connections, and stays empty here (see
[skills and deployed instances](#skills-and-deployed-instances)).

## Skills and deployed instances

SkillsPlex is not seeded. `aiplex skills servers` lists deployed skill-server instances: the API reads them from its
instance store (`ListSkillServers`, `internal/api/skills.go`), and an instance only exists after a deployment. Under `aiplex dev` the
deploy engine uses the no-op Kubernetes client (`cmd/aiplex-api/server.go`), which applies and discovers nothing: a
deployment would store an instance marked running with no skill server behind it. Seeding one would show a deployed skill
that does not exist, so none is seeded. `aiplex skills invocations` lists reported invocation records
(`aiplex skills record-invocation` writes one); a record with no skill server to have run it would be invented history, so
none is seeded either. Both commands work and answer with empty lists. On a loopback API the CLI prints one line on stderr saying that deployed instances need a cluster
(not with `-o json`). The same holds for `aiplex a2a agents`.

## The keyed tool connection

`local-tools-keyed` points at the keyed MCP server, which refuses any request without its API key. The connection
holds `auth: {type: api_key, in: header, name: X-Api-Key, secret_ref: dev-tool-key}`. The key itself is a random value
made when the stack starts. It is given to the API process only, as `AIPLEX_TOOL_SECRET_DEV_TOOL_KEY` (development mode
resolves a secret reference from that variable), and to the keyed server in memory. It is not in the connection, the
generated files, the CLI output or the logs.

```sh
aiplex mcp connections test local-tools-keyed                    # lists the tools: the gateway sent the key
aiplex mcp connections call local-tools-keyed add --args '{"a":2,"b":3}'
aiplex access-points create team --tool local-tools-keyed --allow-users bob@localhost
```

For a keyed connection of your own, a secret reference `my-key` is read from the variable
`AIPLEX_TOOL_SECRET_MY_KEY` (upper case, `-` as `_`) in the environment `aiplex dev` started with: the API inherits your
shell's variables apart from the ones listed above. Export it before starting:
`export AIPLEX_TOOL_SECRET_MY_KEY=...; aiplex dev --allow-origin https://host`, then
`aiplex tools set-auth ID --auth-type api_key --auth-in header --auth-name X-Api-Key --secret-ref my-key`.
A variable exported after `aiplex dev` started is not seen. `tests/e2e/devfederation_steps_test.go` covers this.

If the reference does not resolve, the gateway answers `503` with the plain text `operator tool credential unavailable`.
`aiplex mcp connections test` (and `call`) show the server's text, then say the credential could not be obtained (a secret
reference with no readable value, or an identity profile that could not produce a token) and where to look; the
variable hint is printed only when the API URL is loopback. Other `503` answers (for example
`TOOL_CONFIGURATION_UNAVAILABLE` or `QUOTA_UNAVAILABLE`) are shown with their own code and message and no credential hint.
The key is never in the message.

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
| `AIPLEX_USER_FEDERATION`, `AIPLEX_AGENT_FEDERATION` | `enabled`. See [identity federation](#identity-federation). |
| `AIPLEX_TAG_KEYS_ALLOWED` | `by_branch,by_turn,by_harness`: the three tag keys a Branchyard model gateway sends in `X-AIPlex-Tags`, so the integration can be tried locally. `aiplex dev` sets it itself, over a value in your shell (as it does for `LOG_LEVEL`), and has no flag to change it. A program that starts the stack with `devstack.Options.Env` replaces it with its own value. |
| `AIPLEX_DEV_AGENT_TOKEN` | The mock agent's random bearer token, read through the `dev-agent-identity` profile in the connections file. |
| `AIPLEX_TOOL_SECRET_DEV_TOOL_KEY` | The keyed tool connection's random key. `AIPLEX_TOOL_SECRET_DEV_MOCK_KEY` is a placeholder for the mock surfaces. |

The command removes the variables that would change what the local API connects to, before it sets the
ones above. The removed list is in `filterEnv` (`internal/devstack/stack.go`). It includes
`AIPLEX_TENANT_ID`, `AIPLEX_QUOTA_FILE`, `AIPLEX_MODULES`, `AIPLEX_IDENTITY_PROVIDERS_FILE`,
`AIPLEX_USER_FEDERATION`, `AIPLEX_AGENT_FEDERATION`, `AIPLEX_AGENT_FEDERATION_AUDIENCE`,
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
| `Keyed tools` | The tool connection whose upstream needs an API key held as a secret reference. Absent with `--no-tools`. |
| `Agent` | The agent connection to the mock agent runtime, with a command to invoke it. |
| `Identity` | `federation on`, with the command that mints tokens from the stand-in identity providers. |
| `Access point` | The access point name and its URL. |
| `Price` | The placeholder prices for the mock models. For a real provider, the line says none is seeded. |
| `Quota` | `off` unless `--quota` is given. With `--quota`, QuotaPlex runs on the mock surfaces and tools (request counts); the local API gets no operator quota file otherwise. |
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
  Background pid <pid>; its output goes to ~/.aiplex/dev.log
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
| Quota | Off unless `--quota` is given (see the flags table). The quota file variable is removed from the API's environment. |
| Persistence | None. Stores are in memory. The stack does not use Firestore, AlloyDB or a cloud secret store. |
| Identity provider | Only the local issuer. Hydra and Kratos are not used. Agent registrations are kept in the local registry only, because `HYDRA_ADMIN_URL` is not set. |
| Federation | Turned on, against the three stand-in providers described in [identity federation](#identity-federation). No external identity provider, directory (group lookup) or real provider metadata is used; real ones have not been tried through this stack. |
| Skills, deployed A2A agent instances | None. The no-op Kubernetes client deploys nothing, so `aiplex skills servers` and `aiplex a2a agents` are empty; see [skills and deployed instances](#skills-and-deployed-instances). |
| Cloud agents | Only the mock agent runtime (`local-agent`). Google, AWS, Foundry and Copilot Studio agents are not reachable and were not tried through this stack. |
| Tool credentials | `api_key` authentication works against the keyed MCP server with a development secret reference. OAuth connections, identity profiles (`gcp-adc`) and a cloud secret store are not available. |
| Modules | The API uses its default module list. The `AIPLEX_MODULES` variable is removed from the inherited environment. |
| Kubernetes, mesh, gateway | None. There is no Kubernetes deployment and no Envoy gateway. |
| Models | The mock replies are deterministic and are not model output. Real output needs a real provider and key. |
| Prices | Placeholder rates for the four mock models only. |
| Console | Only if the binary was built with a Console. Otherwise build it with `make console && make build` (Node 22). |

## Loopback only

Development mode binds to loopback. Everything the command starts listens on `127.0.0.1` only, the federation
issuers' listener, the keyed MCP server and the mock agent runtime included; `TestDevStackListensOnLoopbackOnly` checks the kernel's list of
listening sockets (`/proc/net/tcp`, `/proc/net/tcp6`). It covers the sockets of the test process and of its direct child
processes (the API), runs on Linux only and is skipped where `/proc/net/tcp` cannot be read; elsewhere the loopback
binding is by construction (`net.Listen("tcp", "127.0.0.1:0")`) and the API's own startup check. Turning federation on does not change who the developer is: anonymous requests are
still served as the developer in development mode, and a federated token never gets a role without a role binding.

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

## Gateway features on the local stack

Each command below exists in this version of the CLI. The guide named after each one says what it does and what it does not.

- `aiplex hooks policies list|get|set|delete|test`: bind request hooks to the tenant, an access point, a surface or a tool connection ([request hooks](request-hooks.md)).
- `aiplex budgets list|get|create|update|delete|status|preview`: team and project limits above per-caller quota. Use `--quota` with `aiplex dev` so request budgets count ([budgets](budgets.md)).
- `aiplex access-points routing`: live routing and breaker state for an access point with routed backends ([reliability and routing](reliability-and-routing.md)).
- `aiplex mcp registry search|show|import`: read the MCP Registry and import a streamable-HTTP remote as a tool connection ([MCP Registry import](mcp-registry-import.md)).
- `aiplex usage prices catalog search|show|import` and `aiplex usage prices backfill`: the price catalogue and pricing of records written before a price existed ([usage ledger](usage-ledger.md#price-catalogue)).
- `aiplex usage feedback`: append feedback to a usage record ([usage ledger](usage-ledger.md)).
- `aiplex connect local --client <client> --write`, including `--client antigravity`, writes the client's configuration after a diff. `--with-telemetry aiplex` also points the client's own OpenTelemetry export at this deployment's receiver ([harness onboarding](harness-onboarding.md)).
- `aiplex dev --otel <url>` exports traces and metrics to a collector you run ([observability](observability.md)).
- The second mock provider, `local-models-b`, and the fault-injection model names (any model name that contains `fault`) are described in the surfaces table above; they are for trying routing and retries.

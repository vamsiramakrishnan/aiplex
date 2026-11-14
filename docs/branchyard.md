# Branchyard

How to use an AIPlex [access point](access-points.md) as the model backend of Branchyard, a separate tool that runs several coding agents at once. This page describes the integration from AIPlex's side. The other side, Branchyard's `[models]` configuration and model gateway, is documented in the Branchyard repository.

Words from Branchyard that appear here. "Agent" in the rest of these docs is an AIPlex agent (a registered workload); Branchyard's are called coding agents.

- A **branch** is one coding agent's working copy and git branch; a **turn** is one round of work it does on that branch. `by` is Branchyard's command, and `branchyard.toml` is its configuration file in a repository's root.
- Each turn has a **model gateway**: a reverse proxy on loopback, inside Branchyard's own process, that the coding agent's **harness** (Claude Code, Codex and others) calls instead of the model provider. Branchyard's gateway checks the turn's token, picks a **backend**, adds the backend's credential, streams the answer back and records the call. A backend is a named entry in `branchyard.toml` under `[models.backends]`; a **route** (`[[models.routes]]`) says which backends serve which model. Branchyard's gateway is not the Envoy gateway of [naming](naming.md); on this page "the gateway" never appears without Branchyard's name or "model" in front of it.
- A Branchyard **tenant** is the owner of a branch on a Branchyard server (a Branchyard that serves several repositories and people); `<repo>` is a repository's name on that server.

## What goes where

An AIPlex access point is a Branchyard backend. A turn's model calls go harness, then Branchyard's model gateway, then the access point, then the provider. AIPlex verifies who is calling and applies admission and quota; the usage ledger record can be traced to the Branchyard branch and turn. Only Branchyard's model gateway talks to AIPlex: the harness never holds an AIPlex token and never sees an AIPlex URL.

| Piece | Where it lives | What it holds |
| --- | --- | --- |
| Access point, its models, quota, tag allow-list | AIPlex | Who may call, which models, what a call may spend |
| `[models.backends.*]`, `[[models.routes]]`, `[secrets]` | `branchyard.toml` (Branchyard) | The access point's URL, the name of the variable that holds the token, which model goes to which backend |
| The token | The environment of the process that runs the turn | Never a file: the configuration names the variable |
| Usage record with Branchyard's request id and tags | AIPlex usage ledger | One record per call, joined to Branchyard's own record by `client_request_id` |

## Connect a Branchyard repository to an access point

1. **Write the backends and routes.** From the repository's root:

   ```bash
   aiplex connect ide --client branchyard                    # print the tables
   aiplex connect ide --client branchyard --write --dry-run  # the diff against ./branchyard.toml
   aiplex connect ide --client branchyard --write            # diff, confirm, write
   ```

   `--write` edits `./branchyard.toml`, which must already exist: if it does not, the command says so and prints the tables instead of creating a file in a directory that may not be a Branchyard project. `--file PATH` names another file (Branchyard's user-level configuration, say) and creates it when missing. The backup, `--dry-run`, `--yes` and `--undo` behaviour is that of the other `--write` targets ([harness onboarding](harness-onboarding.md#what-it-does-to-your-files)). `--scope user`, `--model`, `--token-helper` and `--with-telemetry` do not apply to this client and are refused (usage error, exit 2) before AIPlex is contacted.

   What is written, for an access point `ide` that has an Anthropic Messages model and OpenAI models:

   ```toml
   [secrets]
   aiplex = "AIPLEX_TOKEN"

   [models.backends.aiplex-ide-anthropic]
   api = "anthropic"
   url = "https://aiplex.example.com/ap/ide"
   key = "aiplex"
   control_plane = "aiplex"

   [models.backends.aiplex-ide-openai]
   api = "openai"
   url = "https://aiplex.example.com/ap/ide"
   key = "aiplex"
   control_plane = "aiplex"

   [[models.routes]]
   model = "claude-sonnet"
   backends = ["aiplex-ide-anthropic"]

   [[models.routes]]
   model = "gpt-main"
   backends = ["aiplex-ide-openai"]
   ```

   One backend per wire the access point serves and Branchyard speaks (`anthropic` for an Anthropic Messages model; `openai` for an OpenAI Chat Completions or Responses model), both with the same `url`, which is the access point's base without `/v1`. One route per model the access point lists for that wire, each naming only the AIPlex backend of that wire. A wire Branchyard's model gateway does not speak (Google GenAI, Bedrock and the other paths) is reported as "not written: Branchyard's gateway speaks Anthropic's and OpenAI's APIs"; it is not an error unless nothing else remains.

   `control_plane` takes one value, `"aiplex"`; Branchyard refuses any other value when it reads the configuration. A backend without it is a plain backend, as before.

   Under that `url`, Branchyard's model gateway calls `POST /v1/messages` and `POST /v1/messages/count_tokens` (Anthropic Messages), `POST /v1/chat/completions` (OpenAI Chat Completions), `POST /v1/responses` (OpenAI Responses) and `GET /v1/models`. AIPlex does not translate between wires, which is why there is one backend per wire.

2. **Put a token in `AIPLEX_TOKEN`**, in the shell that runs `by`:

   ```bash
   export AIPLEX_TOKEN="$(aiplex auth print-token)"
   ```

   The token is your own: a signed-in person's (`aiplex auth print-token`), an agent's, or the developer token of `aiplex dev`. It expires: run the line again to refresh it. The command never writes or prints a token; the configuration holds only the name `AIPLEX_TOKEN`. Branchyard also takes `"@path"` instead of a variable name in `[secrets]`, which suits a long-running Branchyard server that reads a file you refresh. Every request Branchyard's model gateway sends to an AIPlex backend carries `Authorization: Bearer <token>` and no `x-api-key` header, on both wires.

3. **Put branches on the model gateway** with `by run --model-gateway`, or `[models] allow = ["*"]` in `branchyard.toml` (the command does not write that, because it decides which branches use it), and have the operator allow the tags below.

### What the merge does to your file

`branchyard.toml` is edited as text, not rewritten. Tables the command does not write keep their comments, order and values; new tables go at the end; a missing key is inserted into an existing `[secrets]` table; inserted lines use the file's line ending. After editing, the file is parsed and compared with the original plus the intended entries, and anything else that differs makes the command refuse and write nothing.

- **Repeatable.** A second run changes nothing and says "Nothing to change." The command adds or replaces entries; it never removes one, so a route for a model the access point no longer lists stays until you delete it.
- **Already right is left alone.** A backend table that already holds the values that would be written stays byte for byte, with its comments and extra keys. So does a route for the same model that names the backend that would be written, in `backends` or `fallbacks`, when every backend it names is defined in the file with `control_plane = "aiplex"`: your other AIPlex backends as failover, `weights`, `requests_per_minute` and `backend_model` are yours. If it names a backend that is plain or not defined in the file, it is the mixed-route refusal below.
- **Same name, different values.** A backend named `aiplex-<id>-<wire>` with other values, or a route for the same model that names other AIPlex backends of that wire but not the one that would be written, is replaced, and the diff and a note show it, with the same confirmation as any write.
- **Your `[secrets]` entry.** If `[secrets]` already has `aiplex = "..."`, the line is kept exactly as it is, comment included, and the backends use it. The value must be a reference as Branchyard reads it: a variable name, or `@` and a path. Anything else (empty, multi-line, spaces, hyphens, a semicolon) is refused with the file untouched, and the message does not repeat the value: if it is a token, remove it from the file, where it is stored in clear. The reference is printed (in a note before the diff, and in the remaining step: "put the token in that file" for an `@path`) only when it is an `@path` or the conventional upper-case form such as `AIPLEX_TOKEN`; any other name could be a token pasted by mistake, so the command says it is kept and used without showing it.
- **No mixing.** A route that names an AIPlex backend may name only AIPlex backends in `backends` and `fallbacks`; otherwise an outage or a refusal of AIPlex would be bypassed by a direct provider, and Branchyard refuses such a configuration. The same rule covers calls no route matches (a model no route names, or no model): such a call is offered to the plain backends of its API when there are any, and to the AIPlex backends of that API only when it has no plain backend, never to both kinds. An AIPlex backend that no route names, when the same API also has a plain backend, would never be used, and Branchyard refuses that configuration so that nobody believes calls are governed when they are not. This does not affect what the command writes: it writes a route for every model the access point lists, and a backend only when a route uses it; a model the access point does not list is not routed to AIPlex. The command refuses, naming the route (`models.routes[N]`, its line, model and backends), when a route you already have mixes the two, or names a plain backend for a model glob that matches (Branchyard's `*` and `?`) a model it would route to AIPlex: Branchyard takes the first route that matches and new routes are added at the end, so that route would send the model straight to its provider. A common case is a `model = "*"` catch-all to a provider. Two ways forward: move that route below the new ones by hand, or run without `--write` and paste the printed tables above it.
- **An earlier AIPlex route.** If a route of yours that names only AIPlex backends comes first and matches a model, a new route for it never takes effect. That cannot bypass AIPlex, so the command writes it and says so in a note; move the new route above the other.
- **Layouts it cannot edit.** `[models]` entries written as inline tables, or `routes = [...]` as an inline array, are refused, and so is a backend of the names it writes that exists only as dotted keys; the file is left as it was, and you add the printed tables by hand. Dotted keys that define other entries (`models.backends.mine.api = "openai"`) are merged around and kept.
- The diff shows the values of a `[secrets]` table as `[redacted]`, like any table with that name.

## What AIPlex records for a Branchyard call

The caller AIPlex sees is the owner of the token: a signed-in person, an agent, or the developer token of `aiplex dev` (see [naming](naming.md), "Caller"), or, with federated identity, the AIPlex agent bound to the Branchyard tenant. Branchyard's own tenant or branch is not a caller. Each call is one managed exchange and one usage ledger record (see [the usage ledger](usage-ledger.md)). For a call that comes through Branchyard's model gateway the record also carries:

- **`client_request_id`**, from the request header `X-Client-Request-Id`: the id of Branchyard's own usage record for the call (1 to 128 characters of `A-Z a-z 0-9 - _ . :`). It is the exact join between the two systems' records.
- **Tags**, from the request header `X-AIPlex-Tags: by_branch=<branch>,by_turn=<turn>,by_harness=<harness>`. A value holds only `A-Z a-z 0-9 _ . : / @ + ( ) -` and space, at most 64 characters; Branchyard leaves out a key whose value does not fit rather than cutting it. On a Branchyard server `<branch>` is `<repo>/<branch>`. The whole header is at most 512 bytes; if it would be longer, keys are dropped from the end until it fits.
- **The trace id**, when Branchyard's model gateway sends a W3C `traceparent` (AIPlex continues the trace).

Branchyard's model gateway removes these headers if the harness sent them and sets its own, so a harness cannot name its own branch or turn. It never sends a person's name or email address.

**The operator must allow the tags.** AIPlex keeps a tag only when its key is in `AIPLEX_TAG_KEYS_ALLOWED` (default: none), so set `AIPLEX_TAG_KEYS_ALLOWED=by_branch,by_turn,by_harness`. It stores at most `AIPLEX_TAG_MAX_VALUES` distinct values per key and tenant (default 50); past that a value is stored as `(other)`, which matters for `by_turn`, whose values never repeat. Tags are labels for grouping (`tag:by_branch`); the join is `client_request_id`. See [tags](usage-ledger.md#tags).

## Finding the record for a call

Branchyard records AIPlex's request id with each call and `by log` shows it. From either id, in AIPlex:

```bash
aiplex usage records --client-request-id <id>     # Branchyard's id for the call
aiplex usage records --request-id <id>            # the X-Aiplex-Request-Id Branchyard recorded
aiplex usage records --trace-id <id>
```

The API form is `GET /api/v1/usage/records?client_request_id=<id>` (also `request_id` and `trace_id`). Visibility is the usual rule: an admin sees the tenant's records; anyone else sees records where they are the principal or the actor.

## What a refusal looks like

Every response from an access point route carries `X-Aiplex-Request-Id`, AIPlex's own request id (the `request_id` of the ledger record). A refusal that AIPlex itself produced also carries `X-Aiplex-Error-Code`, an upper-case code such as `UNAUTHENTICATED`, `CALLER_NOT_ALLOWED`, `MODEL_NOT_FOUND`, `QUOTA_BUDGET_EXHAUSTED`, `QUOTA_UNAVAILABLE`, `NO_BACKEND_AVAILABLE` or `PROTOCOL_MISMATCH`. The header is never set on an error relayed from a provider, so a proxy can tell "AIPlex decided this" from "the provider answered this". The body keeps the provider's error shape.

Branchyard treats a response with `X-Aiplex-Error-Code` as AIPlex's decision: it returns it to the harness as it is, with its status, body and `Retry-After`, and does not try another backend, whatever the status. A response without the header (a provider's own error relayed by AIPlex, a 5xx, a connection failure) follows Branchyard's usual rules, which for a route that names an AIPlex backend can only move the call to another AIPlex backend. AIPlex being unreachable is never worked around by calling a provider directly.

## Federated identity for a Branchyard server

A Branchyard server can authenticate to AIPlex without any AIPlex secret in its configuration: the backend has `auth = "turn-token"` and `audience = "<value>"` instead of `key`, Branchyard's model gateway presents a token that Branchyard signs for the turn, and AIPlex verifies it against the server's published key set. In AIPlex this is the `branchyard` preset of agent identity federation, bound to an AIPlex agent; see [agent identity federation](agent-identity-federation.md). The calling identity is that agent. `aiplex connect --client branchyard` does not write this form: it writes the `key` form of the first step.

What the Branchyard-signed token carries:

| Part | Value |
| --- | --- |
| Algorithm and header | `alg: "EdDSA"`, `typ: "JWT"`, `kid`: the key's id |
| `iss` | The server's issuer: its public `https://` URL |
| `aud` | The backend's `audience`, a string |
| `sub` | `branchyard:<tenant>`, where `<tenant>` is the Branchyard tenant |
| `exp` | At most one hour after `iat`, and not past the turn's deadline |
| `by_purpose` | `"aiplex"` |
| `by_tenant`, `by_branch`, `by_turn` | As in the turn's other tokens |
| Key set | `GET {iss}/.well-known/jwks.json`: Ed25519 keys (`kty: "OKP"`, `crv: "Ed25519"`) |

A Branchyard run on one machine rather than on a server (its issuer is `branchyard:local:<id>` and its keys are a file) cannot be federated and uses a token.

## Not done

- Tools through AIPlex's MCP endpoint. Branchyard would have to write the AIPlex token into the harness's own configuration, which both products forbid; it needs a Branchyard-side proxy.
- Harness telemetry sent to AIPlex's OTLP receiver.
- A person's identity in AIPlex: the caller is the token's owner, or the Branchyard tenant's AIPlex agent.
- Budgets in AIPlex that match on a branch. Tags can be grouped and filtered, not budgeted.
- A ClientPlex profile for Branchyard: its harnesses never talk to AIPlex themselves.

## Status: what was tested

Nobody has run AIPlex and Branchyard together yet. Treat everything here as untested against a real Branchyard.

| Part | Tested | How |
| --- | --- | --- |
| `aiplex connect --client branchyard` (print, `--write`, merge, refusals, idempotence, undo, no token) | Yes, against this repository only | Unit tests in `internal/harness` and an end-to-end test against `aiplex dev` that parses the written TOML. The keys written (`secrets`, `models.backends.*.{api,url,key}`, `models.routes.{model,backends}`) were checked by hand against Branchyard's `schema/branchyard.config.json`. Branchyard itself was not run, and `by config validate` was not run on a written file |
| `control_plane = "aiplex"` | No | The key is new in Branchyard's side of version 1 of the integration contract and is not in that schema yet. A Branchyard that does not know it refuses the file |
| The request headers, tags and `client_request_id` | Not by this page | Described from the integration contract. AIPlex's tag and `client_request_id` storage predates it and has its own tests |
| `X-Aiplex-Request-Id`, `X-Aiplex-Error-Code`, the record lookups by `client_request_id`, `request_id` and `trace_id` | No | Described from the contract; implemented separately in AIPlex |
| Federated identity with the `branchyard` preset | No | Described from the contract; implemented separately in AIPlex |

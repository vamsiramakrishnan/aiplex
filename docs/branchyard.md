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

   Branchyard asks an AIPlex backend for those routes and no others. A harness's request for any other path that would go to an AIPlex backend (the access point's tools endpoint `/mcp`, another wire, embeddings, one model by id) is refused by Branchyard's gateway with its own `403` before anything is sent, so the backend's credential is used for these calls only. The `url` is required: a Branchyard backend without one is addressed at its provider's public API, where an AIPlex credential must never go, so Branchyard refuses an AIPlex backend that has none. `aiplex connect` always writes it.

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

Branchyard's model gateway sets these headers itself and forwards the harness's own request headers by allow-list (the content headers, `Accept`, the API's version and beta headers and a few more), so a harness cannot name its own branch or turn, and cannot present an identity assertion, a client address or a cookie to AIPlex. It never sends a person's name or email address.

**The operator must allow the tags.** AIPlex keeps a tag only when its key is in `AIPLEX_TAG_KEYS_ALLOWED` (default: none), so set `AIPLEX_TAG_KEYS_ALLOWED=by_branch,by_turn,by_harness`. It stores at most `AIPLEX_TAG_MAX_VALUES` distinct values per key and tenant (default 50); past that a value is stored as `(other)`, which matters for `by_turn`, whose values never repeat. Tags are labels for grouping (`tag:by_branch`); the join is `client_request_id`. See [tags](usage-ledger.md#tags).

## Finding the record for a call

Branchyard records AIPlex's request id with each call: `by log` shows it, and `by log --json` has it with the call's own id as `control_plane.request_id` and `control_plane.call_id` on the call's `model` event (the call id is on the event even when no answer came back). From either id, in AIPlex:

```bash
aiplex usage records --client-request-id <id>     # Branchyard's id for the call
aiplex usage records --request-id <id>            # the X-Aiplex-Request-Id Branchyard recorded
aiplex usage records --trace-id <id>
```

The API form is `GET /api/v1/usage/records?client_request_id=<id>` (also `request_id` and `trace_id`). Visibility is the usual rule: an admin sees the tenant's records; anyone else sees records where they are the principal or the actor.

## What a refusal looks like

Every response from an access point route carries `X-Aiplex-Request-Id`, AIPlex's own request id. It is the `request_id` of the ledger record when the call wrote one; a call refused before admission, such as one with no credential, has an id and no record ([which responses have a record](access-points.md#response-headers)). A refusal that AIPlex itself produced also carries `X-Aiplex-Error-Code`, an upper-case code such as `UNAUTHENTICATED`, `CALLER_NOT_ALLOWED`, `MODEL_NOT_FOUND`, `QUOTA_BUDGET_EXHAUSTED`, `QUOTA_UNAVAILABLE`, `NO_BACKEND_AVAILABLE` or `PROTOCOL_MISMATCH`. The header is never set on an error relayed from a provider, so a proxy can tell "AIPlex decided this" from "the provider answered this". The body keeps the provider's error shape.

Branchyard treats a response with `X-Aiplex-Error-Code` as AIPlex's decision: it returns it to the harness as it is, with its status, body and `Retry-After`, and does not try another backend, whatever the status. A response without the header (a provider's own error relayed by AIPlex, a 5xx, a connection failure) follows Branchyard's usual rules, which for a route that names an AIPlex backend can only move the call to another AIPlex backend. AIPlex being unreachable is never worked around by calling a provider directly.

AIPlex's own availability answers carry the code as well, for example when it could not reach the provider or no backend of a routed model was available. AIPlex has done its own routing and retries by then, so Branchyard treats those as final too. The rule is simple: the header is present whenever AIPlex wrote the body, and absent when the body is the provider's.

What the harness sees of AIPlex is deliberately little. Branchyard relays response headers from an AIPlex backend by allow-list (the content type, `Retry-After`, the provider's rate-limit headers and request id), so `X-Aiplex-Request-Id` and `X-Aiplex-Error-Code` stop at Branchyard, which records them; it neither follows nor relays a redirect; and it replaces a refusal body that contains the credential it sent. It also removes the variable that holds the AIPlex token from the harness's environment. In a local turn the harness still runs as the same person, so that is a precaution and not a boundary; a sandboxed turn is one. Branchyard's own guide has the exact lists.

## Federated identity for a Branchyard server

A Branchyard server can authenticate to AIPlex without any AIPlex secret in its configuration: the backend has `auth = "turn-token"` and `audience = "<value>"` instead of `key`, Branchyard's model gateway presents a token that Branchyard signs for the turn, and AIPlex verifies it against the server's published key set. In AIPlex this is the `branchyard` preset of agent identity federation, bound to an AIPlex agent; see [agent identity federation](agent-identity-federation.md). The calling identity is that agent. `aiplex connect --client branchyard` does not write this form: it writes the `key` form of the first step.

On the AIPlex side an operator registers an agent for the Branchyard tenant, binds it to the server, and grants it what its turns may call:

```bash
aiplex agents register acme-branchyard --name "Branchyard: acme"
aiplex agents identity bind acme-branchyard by --preset branchyard \
  --input server=https://branchyard.example.com \
  --input tenant=acme \
  --input audience=https://aiplex.example.com
aiplex agents identity plan acme-branchyard by    # prints the Branchyard backend configuration
```

The binding grants nothing by itself: the agent needs the model and surface scopes for the models its turns call, as any agent does. `aiplex agents identity plan` prints the principal the binding yields (step "Grant permissions"), so the grant can be made before the first call. [Agent identity federation](agent-identity-federation.md#branchyard) has the rules AIPlex applies and their limits: `server` is the issuer exactly as the tokens carry it (`https://host[:port]`, no path or trailing slash; plain `http` only on loopback in development), the Branchyard tenant is 1 to 128 of `A-Z a-z 0-9 . _ @ + -`, only `EdDSA` is accepted, the token must carry `by_purpose = "aiplex"` and a numeric `iat`, and `exp` may be at most 3600 seconds after `iat`. The gateway authorizer `aiplex-authz` verifies RS256 only, so this token works on the managed path (`/ap/{id}/...`) and not on the gateway path. To try the AIPlex side without a Branchyard server, `aiplex dev token --issuer branchyard --tenant acme` mints such a token from a loopback stand-in ([local development](local-development.md)).

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

**Run together on 14 November 2026.** Branchyard's qualification script (`tools/qualify_aiplex.py` in the Branchyard repository) ran the real `by` (Branchyard commit 8d6c52f) against a real `aiplex dev` (AIPlex commit f4e3991) on one machine over loopback: AIPlex's mock providers, a stand-in direct provider, and Branchyard's stand-in agent running a script as the harness. No model and no real coding agent were involved. It has two scenarios, a stored token and a Branchyard-signed token, of 24 checks each; all passed, twice in a row. The records are `docs/evidence/aiplex-control-plane-2026-11-14.json` and `docs/evidence/aiplex-signed-token-2026-11-14.json` in the Branchyard repository. What the stored-token scenario showed:

- `by config validate` accepts the file `aiplex connect local --client branchyard --write` produces, as written and with other backends added.
- Seven calls went through one turn's gateway on the Anthropic and OpenAI wires, one of them streamed. For each call routed to AIPlex there is exactly one ledger record, found by `--request-id` and by `--client-request-id`, whose `client_request_id` is Branchyard's call id and whose tags are the branch, the turn and the harness.
- A call AIPlex refused (`MODEL_NOT_FOUND`) was final: its Branchyard route named two AIPlex backends, and AIPlex holds one record for it.
- Headers the harness forged (`X-Client-Request-Id`, `X-AIPlex-Tags`) reached AIPlex as neither an id nor a tag.
- A call with a plain Branchyard route, and a call no route takes, went to the direct provider and left no record in AIPlex. With AIPlex stopped, an AIPlex-routed call failed with Branchyard's own 502 and the direct provider received nothing.
- No variable of the harness's environment held the token, the response headers it received were on Branchyard's allow-list, and the token was in no file of the run and in no captured output.

The signed-token scenario ran the same calls through a Branchyard server (`by serve`) whose backends have `auth = "turn-token"` and no key: no AIPlex token existed anywhere in the server's configuration, environment or data directory; before any grant the first call was refused `403 ACTION_DENIED`, which shows the token was verified and the caller was the bound agent; after the grant each call had one record with `caller_kind` `agent`; and a backend naming an audience the binding does not have was refused `401 UNAUTHENTICATED`.

The first runs found two defects, both fixed before those records: the ledger record of a refused call had no tags (AIPlex), and the harness's environment held the token's variable (Branchyard).

| Part | Tested in this repository | Run with the real Branchyard |
| --- | --- | --- |
| `aiplex connect --client branchyard` (print, `--write`, merge, refusals, idempotence, undo, no token) | Unit tests in `internal/harness` and `cmd/aiplex-cli`; an end-to-end test against `aiplex dev` that parses the written TOML | Yes: the written file is accepted by `by config validate` and used for the run |
| `X-Aiplex-Request-Id`, `X-Aiplex-Error-Code` | Unit tests over every refusal the access point routes produce and over relayed provider errors on three wires, streamed and not, including a provider that sets the headers itself; an end-to-end test | Yes, for a success and for `MODEL_NOT_FOUND`. Other refusals (quota, a caller not allowed, no backend) were not provoked in that run |
| Tags, `client_request_id`, and the lookups by `client_request_id` and `request_id` | Unit tests on the in-memory store and the Firestore wire fake; an end-to-end test | Yes, on the in-memory store of `aiplex dev`. Not run: the Firestore emulator for the new filters; the lookup by `trace_id` (a local Branchyard turn sends no `traceparent`) |
| Federated identity with the `branchyard` preset | Unit tests of the EdDSA verification and of each refusal; an end-to-end test with a stand-in issuer | Yes, on loopback: a real `by serve` (no TLS, its own key file) minted the token for each turn, AIPlex fetched the server's key set and verified it, the calls were recorded with `caller_kind` `agent` and the bound agent as `client_id`, and a backend naming another audience was refused `401 UNAUTHENTICATED` with no record. Not run: an `https` issuer, a Cloud KMS signing key, a tenant other than Branchyard's default |

Not validated: a real model or a real coding agent through this path; a Branchyard server with TLS, a worker or a sandbox; a deployed AIPlex (real quota, a person's token, several replicas); another operating system.

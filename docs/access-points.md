# Access points and the single pane

An access point is a tenant resource that gives a group of callers one stable URL,
`https://<aiplex>/ap/{id}/…`, in front of models, tools and skills. The URL is an
enforcement point. Before a request reaches a model or a tool, the access point checks
who the caller is, whether that kind of caller may use the access point, and whether
the caller is in its subjects. A QoS block ties the access point to QuotaPlex controls.
The single pane is the overview API and the `aiplex who` and `aiplex connect` commands,
which show every caller, what each can reach and why, and how to point a client at an
access point.

The design is in [access points and the single pane](design/2026-11-09-access-points-single-pane.md).
Quota controls are described in [managed QuotaPlex execution](quotaplex-integration.md).
Usage records carry `access_point_id` and `caller_kind`; see [usage ledger](usage-ledger.md).
Client profiles are described in [ClientPlex](clientplex.md).

## Create one with the CLI

`aiplex access-points create <id>` builds the access point from flags, so a reader does not need a
spec file for the common case. `--model name=surface[:native]` exposes a model of a saved model surface
under a client-facing name. `--tool <connection>` exposes every permitted tool of a tool connection, named
`<connection>__<tool>` to callers. Give at least one of them.

Who may call: with no `--allow-*` flag, every verified caller of the kinds in `--callers` (default
`user,agent,client`) is admitted. Otherwise a caller must match one of `--allow-users` (a subject, an email
the usage ledger knows, or a `principal-…` ID), `--allow-groups`, `--allow-agents` or `--allow-clients`.
On `aiplex dev`, a second caller from `aiplex dev token --user bob` has the email `bob@localhost`, so
`--allow-users bob@localhost` admits it. A caller that is not admitted gets `not found` from `aiplex connect team`.

```sh
aiplex access-points create team --model mock-gpt=local-models --tool local-tools
aiplex access-points create ide --model gpt-4o-mini=openai --model claude=claude:claude-sonnet-4-5 --allow-groups eng
aiplex access-points create ci --model gpt-4o-mini=openai --callers agent --allow-agents ci-bot
aiplex access-points create bob-only --model mock-gpt=local-models --allow-users bob --update
```

The output of the first command on `aiplex dev`, which has the `local-models` surface and the `local-tools`
connection seeded:

```text
Created access point team (revision 1): http://127.0.0.1:8080/ap/team

Next:
  aiplex connect team                 # client configuration
  aiplex access-points get team
```

The access point is created new. `--update` replaces an existing one; the CLI reads the current revision for you
and writes against it. `--name` sets the display name. QoS classes and expose lists are
not flags: use `aiplex access-points apply -f <file>` for them, and the [specification](#specification) below for the fields.

## Specification

```json
{
  "id": "ide-platform-team",
  "display_name": "IDE – platform team",
  "caller_kinds": ["user", "client"],
  "subjects": {
    "groups": [{"source": "example-source", "id": "platform-eng"}],
    "principals": [],
    "agents": [],
    "client_registrations": []
  },
  "models": [
    {"name": "gemini-2.5-pro", "surface_ref": "vertex-global", "native_model": "gemini-2.5-pro"}
  ],
  "tools": [{"connection_ref": "github"}, {"connection_ref": "search", "expose": ["search*"]}],
  "skills": [{"instance_ref": "runbooks"}],
  "qos": {
    "class": "interactive",
    "weight": 4,
    "limits": {"requests_per_minute": 120, "tokens_per_minute": 400000, "concurrency": 8},
    "budget_refs": ["platform-monthly"]
  }
}
```

| Field | Rules |
| --- | --- |
| `id` | `[A-Za-z0-9._-]`, 1 to 128 characters, starting with a letter or digit. It must equal the resource ID. |
| `caller_kinds` | One to three of `agent`, `user`, `client`. Unknown kinds are rejected. |
| `subjects` | Narrows who may call. An empty block admits every caller of an allowed kind. See [admission](#admission). |
| `models[]` | `name` is the client-facing name, `surface_ref` a saved model surface, `native_model` the model the surface lists. Names are unique. Instead of `surface_ref`/`native_model`, a model can list `backends` (surface and native model pairs, with optional `weight` and `priority`) and a `routing` block for retry, fallback, circuit breakers and cost-aware ordering; `cache` turns on the exact-match response cache. Every backend must serve the same client wires natively. See [reliability and routing](reliability-and-routing.md). |
| `tools[]` | `connection_ref` names a saved tool connection. `expose` is an optional list of tool names or prefixes ending in `*`. Omitted means every tool the connection permits. `rules` is an optional list of [policy rules](tool-policy-rules.md) for calls through this access point; they run in addition to the connection's own rules, so an access point can only add restrictions. Rules are compiled when the access point is saved. |
| `skills[]` | `instance_ref` names a deployed skill instance. Requires the `skillsplex` module. |
| `qos` | `class` (lower-case, up to 63 characters), `weight`, `limits` (`requests_per_minute`, `tokens_per_minute`, `concurrency`) and `budget_refs`. Rendered into QuotaPlex controls. See [QoS and quota](#qos-and-quota). |
| `labels` | Up to 8 trusted experiment tags (`key: value`), stored as `tags` on every usage record of an exchange through this access point. Keys are `[a-z][a-z0-9_.-]{0,31}`; values up to 64 characters of `[A-Za-z0-9_.:/@+() -]`, not `(other)`. A caller can add tags only with the `X-AIPlex-Tags` header for keys the operator allowed (`AIPLEX_TAG_KEYS_ALLOWED`), and cannot override a label. Filter and group usage by tag: see the [usage ledger](usage-ledger.md#tags). Invalid labels are refused on save as `labels`. |

Unknown fields are rejected. The API returns a validation error for any reference that
does not resolve in the tenant.

## Routes

Management routes are under `/api/v1`. Reads follow resource revision semantics. Writes
need the admin role.

Reads depend on the caller. Admins and deployers read the full resource. Any other verified
caller reads only the access points that admit it, and gets a view with `id`, `display_name`
and `endpoints` (the client-facing URLs). That view has no subject lists and no model or tool
lists. An access point that does not admit the caller answers `404`, the same as a missing
one. The route classes are in [authorization](authorization.md).

| Route | Purpose |
| --- | --- |
| `GET /api/v1/access-points` | List access points. |
| `GET /api/v1/access-points/{id}` | One access point, with its revision. |
| `PUT /api/v1/access-points/{id}` | Create or replace. Updates send the current revision in `If-Match`. |
| `DELETE /api/v1/access-points/{id}` | Delete. |
| `POST /api/v1/access-points/{id}/quota-plan` | Render the QoS block as QuotaPlex controls for review. Admin only. Does not activate anything. |
| `GET /api/v1/access-points/{id}/routing` | Live routing state of the routed models in the API process that answers: each backend's circuit breaker and last outcomes, and the response-cache occupancy. Admin or deployer. See [reliability and routing](reliability-and-routing.md). |
| `POST /api/v1/access-points/{id}/cache/purge` | Remove the access point's cached answers (or one model's, with `?model=`) in the API process that answers. Admin only. |
| `GET /api/v1/overview/subjects?kind=agent\|user\|client` | Callers of one kind, with identity source, last seen, access points and a usage roll-up. Admin or deployer. The usage roll-up, last-seen times and ledger-derived subjects are admin only. A deployer gets the rest with those fields reported as unavailable. |
| `GET /api/v1/overview/subjects/{kind}/{id}` | Effective access of one caller, with the reason for each grant. Also `GET /api/v1/overview/subjects/{kind}?id=…` for IDs that contain `/`. |
| `GET /api/v1/overview/access-points/{id}` | Callers, resources, QoS and live usage of one access point. Usage is admin only. |
| `GET /api/v1/overview/access-points` | List access points with their counts and quota coverage. Query: `limit` (1 to 200, default 50) and `cursor`. The response has `access_points` (each with `id`, `display_name`, `caller_kinds`, `qos_class`, `counts` of `models`, `tools` and `skills`, and `quota_coverage`), `next_cursor` when more pages exist, and `skipped_invalid` counting stored records that fail to decode. |
| `GET /api/v1/overview/access-points/{id}/connect?client=…` | Ready-to-paste configuration for one client profile. Defaults to `generic`. The response includes an `endpoints` array (see below). Any verified caller the access point admits may call it; operators get the full response and other callers a reduced one (see `aiplex connect` below). |

Client-facing routes are at the server root, not under `/api/v1`. They use the
caller's own bearer token. A request without a verified identity is refused.

| Path under `/ap/{id}` | Protocol | Where the model is |
| --- | --- | --- |
| `POST /v1/chat/completions` | OpenAI Chat Completions | Body. |
| `POST /v1/responses` | OpenAI Responses | Body. |
| `POST /v1/embeddings` | OpenAI Embeddings | Body. |
| `POST /v1/moderations` | OpenAI Moderations | Body. Without a model, the default moderation model routes the request. |
| `POST /v1/images/generations`, `POST /v1/images/edits` | OpenAI Images | Body. The `model` form field for `edits`. |
| `POST /v1/audio/transcriptions`, `POST /v1/audio/translations` | OpenAI Audio | Body. The `model` form field for multipart requests. |
| `POST /v1/audio/speech` | OpenAI Audio | Body. |
| `POST /v1/messages` | Anthropic Messages | Body. |
| `POST /v1/messages/count_tokens` | Anthropic token counting | Body. |
| `POST /v1beta/models/{model}:{method}` | Google GenAI | Path. |
| `POST /v1beta/cachedContents`, `POST /v1beta/interactions` | Google GenAI (Developer API creations) | Body (`models/{model}`). |
| `GET /v1beta/cachedContents`, and `GET`, `PATCH` or `DELETE` on `/v1beta/cachedContents/{id}` | Google GenAI | No model. See [model-less operations](#model-less-operations). |
| `GET` or `DELETE` on `/v1beta/interactions/{id}`, `POST /v1beta/interactions/{id}/cancel` | Google GenAI | No model. See [model-less operations](#model-less-operations). |
| `GET` or `DELETE` on `/v1/responses/{id}`, `POST /v1/responses/{id}/cancel` | OpenAI Responses | No model. See [model-less operations](#model-less-operations). |
| `POST /model/{model}/converse` and `POST /model/{model}/converse-stream` | Bedrock Converse | Path. Model IDs may contain `:`. |
| `POST /model/{model}/invoke` and `POST /model/{model}/invoke-with-response-stream` | Bedrock InvokeModel | Path. Model IDs may contain `:`. |
| `GET /v1/realtime?model={model}` (WebSocket upgrade) | OpenAI Realtime | Query. See [WebSocket sessions](#websocket-sessions). |
| `GET /ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent?model={model}` (WebSocket upgrade) | Gemini Live (Vertex Live) | Query. See [WebSocket sessions](#websocket-sessions). |
| `GET /v1/models` | OpenAI-shaped list | Anthropic-shaped when the request carries `anthropic-version`. Only models the caller may use. |
| `GET /v1beta/models` | Google GenAI list | Only models reachable through that protocol. |
| `POST /mcp` | MCP Streamable HTTP | Aggregates every exposed tool as `{connection}__{tool}`. See [aggregate tools/list](#aggregate-toolslist). |
| `POST /mcp/{connection}` | MCP Streamable HTTP | One connection, with original tool names. |
| `GET /v0.1/servers` | MCP registry listing | For clients that take a catalog URL. Served in the MCP Registry response shape (`servers[]` of `{server, _meta}`, `metadata.count`; server.json schema 2025-12-11) next to the AIPlex catalog members (`templates`, `total`, `page`, `page_size`). See [MCP Registry import](mcp-registry-import.md#related-the-access-points-own-registry-shaped-listing). |
| `GET /skills` | Skill manifest | Exposed skills. Requires `skillsplex`. |
| `GET /.well-known/oauth-protected-resource`, `…/mcp`, `…/mcp/{connection}` | RFC 9728 metadata | Public. Points at the operator's authorization server. |

Batch and file operations (`/v1/batches`, `/v1/files`, `/v1/messages/batches`,
`/v1beta/batches`) are not routed through access points. See
[model batches](model-batches.md#access-points).

Provider SDKs that send their key in `x-api-key`, `x-goog-api-key` or `api-key` are
accepted on the routes listed in [caller authentication](model-providers.md#caller-authentication).
The header value is the AIPlex token. A token in a URL query parameter is refused on these
routes with a `401` in the wire's own error shape.

The Azure OpenAI shapes below `/ap/{id}` are listed here. They map onto the OpenAI wires
and use the same admission, model resolution, quota and ledger path.

| Path under `/ap/{id}` | Protocol | Where the model is |
| --- | --- | --- |
| `POST /openai/v1/chat/completions` | Azure OpenAI Chat Completions | Body. |
| `POST /openai/v1/responses` | Azure OpenAI Responses | Body. |
| `GET` or `DELETE` on `/openai/v1/responses/{id}`, `POST /openai/v1/responses/{id}/cancel` | Azure OpenAI Responses | No model. See [model-less operations](#model-less-operations). |
| `POST /openai/v1/embeddings` | Azure OpenAI Embeddings | Body. |
| `POST /openai/deployments/{deployment}/chat/completions` | Azure OpenAI Chat Completions (deployment shape) | Path. See [Azure OpenAI paths](#azure-openai-paths). |
| `POST /openai/deployments/{deployment}/embeddings` | Azure OpenAI Embeddings (deployment shape) | Path. See [Azure OpenAI paths](#azure-openai-paths). |

The same metadata is also served at the root as `/.well-known/oauth-protected-resource/ap/{id}/mcp`
and `…/ap/{id}/mcp/{connection}`.

## How a caller is classified

The caller kind is derived from the verified identity. The request cannot set it.

| Kind | When |
| --- | --- |
| `agent` | The token carries a delegated actor (RFC 8693 `act`), or the principal is a workload identity rather than a workforce user. |
| `client` | The verified authorized party (`azp`) equals the ID of a ClientPlex registration in the tenant. |
| `user` | Otherwise. |

Precedence is `agent`, then `client`, then `user`. The client ID is resolved
separately, so an agent or user that presents a registered client is still attributed
to that client.

## Admission

Admission runs before any model or tool work.

1. `caller_kinds` must include the caller's kind.
2. Identity constraints (`principals`, `groups`, `agents`) are one constraint. Any one of
   them admits. Groups match on `{source, id}` against the verified group view. Missing
   or malformed groups never match.
3. `client_registrations` is a separate constraint. When it is set, the caller must
   present one of the listed registrations.
4. When both constraints in 2 and 3 are set, the caller must satisfy both.

An empty `subjects` block admits every caller of an allowed kind.

A client registration identified only by a public OAuth client ID is attribution, not a
security boundary. Any holder of a user token issued to that public client presents the
same authorized party.

## Model requests

The model name selects the route to a saved surface. The request is then handed to the
existing client facade and managed native handler, so credentials, quota admission and
the usage ledger are the ones already in place.

A model with `backends` (or a `routing` or `cache` block) goes through the router
described in [reliability and routing](reliability-and-routing.md): each attempt is one
pass through this same path for one backend, with that backend's grants, capacity
admission, quota lease and credential.

A model is reachable through a protocol only when its surface speaks that protocol
natively. There is no cross-protocol translation. A request that uses a protocol the
surface does not serve returns `400 PROTOCOL_MISMATCH` in that protocol's own error
shape, and the message lists the paths that do serve the model.

### Admission is not a grant

Admission lets a caller reach the access point. It does not let the caller use a model. A
caller with no operator role (an admin or deployer gets the grants with the role) also needs
its own grants, set by an operator:

| Grant | For |
| --- | --- |
| `llm:model:{model}` | The model, for example `llm:model:mock-gpt`. |
| `llm:surface:{surface}:*` | The model surface that serves it, for example `llm:surface:local-models:*`. A single operation can be granted instead of `*`. |

```sh
aiplex agents user-scopes get  <principal-id>
aiplex agents user-scopes set  <principal-id> --scopes llm:model:mock-gpt,llm:surface:local-models:*
```

`user-scopes set` replaces the caller's whole list, so include what `get` shows. Having only
`llm:model:{model}` is not enough: the call is still refused. A caller that is admitted but
missing grants gets `403` in the wire's own error shape (`permission_error` on OpenAI-style
wires), and the message names every missing grant and the command above with the caller's
principal ID. A caller that is not admitted does not get that message: it gets the same answer
as before, which does not say whether the access point exists. `GET /v1/models` lists only models
the caller holds `llm:model:{model}` for; it does not check the surface grant.

Other model errors:

| Status and code | Meaning |
| --- | --- |
| `403 permission_denied` | The caller is admitted but lacks a model, surface or agent grant. The message lists them. |
| `404 MODEL_NOT_FOUND` | The model is not in the access point, or the caller may not use it. |
| `404 UNSUPPORTED_OPERATION` | A GenAI operation that the surface does not implement. |
| `503 SURFACE_UNAVAILABLE` | The surface cannot be resolved right now. |
| `503 MODEL_ALIAS_UNSUPPORTED` | The client name differs from `native_model`, and the wire and surface cannot rewrite the name (see the aliasing table). |
| `404 MODULE_DISABLED` | The `llmplex` module is off. |
| `413 REQUEST_TOO_LARGE` | The body exceeds 8 MiB. |

Model aliasing is limited. The client name may differ from `native_model` only when the
access point can rewrite it. Each wire rewrites the name in one place:

| Wire | Alias allowed | How the name is rewritten |
| --- | --- | --- |
| GenAI (`:method` paths) | Yes | The path. |
| Bedrock Converse and InvokeModel | Yes | The path segment. |
| OpenAI Realtime | Yes | The `model` query parameter. |
| OpenAI Chat Completions, OpenAI Responses, OpenAI Embeddings, Anthropic Messages (JSON bodies) | Yes, when the surface protocol is one of: OpenAI Chat, OpenAI Responses, Anthropic Messages, OpenAI Embeddings, GenAI, Bedrock Converse, Bedrock InvokeModel, OpenAI Realtime | The body's `model` value only. Other body fields are not changed. |
| OpenAI Images and Audio (JSON bodies) | Only when the surface protocol is one of the list above. Images, audio and moderations surfaces are not on that list. | The body's `model` value. |
| Multipart bodies (image edits, audio transcriptions and translations, when sent as `multipart/form-data`) | No | Not rewritten. |
| Gemini Live (WebSocket) | No | Not rewritten. The request returns `503 MODEL_ALIAS_UNSUPPORTED`. |
| OpenAI Moderations | No | Not rewritten. |

The response keeps the provider's native model name in every case. The client sees its own name
only in the request.

Two checks run when the access point is saved. An alias (a `name` that differs from
`native_model`) is refused with `400` on `models/{n}/name` unless the surface protocol allows
rewriting, as listed above. A model entry is also refused when its surface cannot serve the
native model.

### Path translation

Wire paths are the vendor-default paths. Some surfaces expose the same operation at another
path, and the access point translates the wire path to the path the surface's client facade
accepts:

| Surface | Wire | Translated path |
| --- | --- | --- |
| `azure-openai` | Chat Completions | `/openai/v1/chat/completions` when the surface installs `azure.v1.chat-completions`. Otherwise `/openai/deployments/{native model}/chat/completions` when it installs `azure.deployments.chat-completions`. |
| `azure-openai` | Responses | `/openai/v1/responses` when the surface installs `azure.v1.responses`. |
| `openai-compatible` | Chat Completions | `{base_path}/chat/completions`, with `base_path` defaulting to `/v1`. |

Every other surface and wire pair keeps the wire path. A wire that the surface does not
install is refused.

### Save-time rejection

An access point is rejected when a model entry can never be served. For each model entry,
the save requires all of the following:

- The surface's protocol is served by at least one access-point wire. Otherwise the save
  fails with `model surface … protocol … is not served by any access-point wire protocol`.
- At least one wire that reaches the surface passes the facade path check for the native
  model. Otherwise the save fails with `model surface … cannot serve native model …`. For
  example, an Azure surface that installs neither Chat Completions operation cannot be used
  with the Chat Completions wire.
- The native model is listed on the surface.
- A `name` that differs from `native_model` is allowed only on a surface whose body can be
  rewritten (see the aliasing table above).

### Azure OpenAI paths

The Azure OpenAI routes map to the access point's OpenAI wires. The `api-version` query
parameter is accepted and dropped. The surface pins its own version, so a caller cannot choose it.

- On the deployment shapes, the `{deployment}` path segment is the access point's model name.
  A `model` value in the JSON body must equal the deployment. Otherwise the request is refused
  with `400 INVALID_REQUEST`. If the body has no `model`, the native model name is added to it.
- On the `/openai/v1/...` shapes, the model comes from the body, as on `/v1/...`.
- The `api-key` header is accepted as the AIPlex token. An `Authorization` header that differs
  from it is refused with `401`. The two are never merged.

### Model-less operations

Some operations name no model. Their resource is addressed by ID (Responses retrieve, cancel
and delete; cached contents and interactions get, update, cancel and delete), or the
project-wide cached-content list is requested. The access point serves them by this rule:

1. Among the access point's surfaces that speak the wire and have installed the operation,
   an ID-addressed operation goes to the one surface through which the caller created the
   resource. The binding is resource ownership, the same one `previous_response_id` uses. If
   no surface owns the resource, the response is `404` in the caller's own error shape, the
   same as a resource that does not exist. If several surfaces own it, the response is `409`.
2. An operation with neither a model nor an ID (the cached-content list) is served only when
   exactly one surface qualifies. Otherwise the response is `400`, and the message names the
   ambiguity. The gateway also requires the explicit shared-listing grant.

Admission, native errors, quota and ledger dimensions, and the operation scope check are the
same as for every other access-point model request.

Model surface protocols map to wire protocols as follows: `openai-chat` to Chat
Completions, `openai-responses` to Responses, `anthropic-messages` to Messages (and token
counting), `google-genai` to GenAI, `bedrock-converse` to Bedrock Converse, `bedrock-invoke` to
Bedrock InvokeModel, `openai-realtime` to OpenAI Realtime, `openai-embeddings` to Embeddings,
`openai-moderations` to Moderations, `openai-images` to Images and `openai-audio` to Audio.
Gemini Live is served from `google-genai` surfaces.

A wire is reachable only through a surface that speaks its protocol. The model name is read
from the place shown in the route table above. A request that names no model where one is
required is refused.

Model names may contain letters, digits, `.`, `_`, `@`, `/`, `:` and `-`. The colon is
allowed because Bedrock model IDs use it.

**Model lists and connect output.** The `protocols` field of each model in `GET /v1/models` and
`GET /v1beta/models` lists only the wires that the access point serves for that model. The
`aiplex connect` output uses the same list. A wire that the surface's protocol could reach, but
that the surface does not install for the native model, is left out. Discovery therefore never
advertises an endpoint that refuses every request.

**Load failures.** The quota-plan, overview and connect routes answer for a saved access point
that cannot be read:

| Status and code | Meaning |
| --- | --- |
| `404 NOT_FOUND` | The access point does not exist. |
| `409 ACCESS_POINT_INVALID` | The stored record loads but does not validate. Re-save it. |
| `503 ACCESS_POINT_UNAVAILABLE` | The store failed. The access point may exist. Retry. |

**Validation errors.** A rejected save returns `400 INVALID_SPEC`. `details.field` names the
offending field as a slash-separated path, for example `models/1/surface_ref`, `caller_kinds/0`,
`qos/scope` or `subjects/principals`. The field is absent when the error has no field. A referenced
resource that cannot be read returns `503 RESOURCE_STORE_UNAVAILABLE`, not a field error.

## Tool requests

Each `tools/call` goes through the managed tool path: connection policy, the caller's
`mcp:tools:` grant, policy rules (the connection's, then the access point's), quota, the
upstream credential and the usage ledger. See
[tool connection auth](tool-connection-auth.md) and
[policy rules on tool calls](tool-policy-rules.md).

The MCP endpoints accept `POST` only (other methods return `405`). The body must be
`application/json`. Browser origins are checked against the public URL's own origin
and against `CONSOLE_ORIGINS`. A value of `*` in `CONSOLE_ORIGINS` accepts any origin.

Protocol versions implemented:

| Version | Status |
| --- | --- |
| `2025-11-25` | Supported. The default for `initialize` when the client's version is not supported. |
| `2025-06-18` | Supported. |
| `2025-03-26` | Supported. Used when the `MCP-Protocol-Version` header is absent. |

- An `initialize` request with a version this endpoint does not support is answered
  with `2025-11-25`, the newest version supported here. The client decides whether to
  continue.
- A request whose `MCP-Protocol-Version` header names a version this endpoint does not
  support is rejected with HTTP 400. The JSON-RPC error lists the supported versions in
  its `data.supported` field. This is the fallback signal for clients that send a newer
  revision.

Tool endpoints return `404` when the `mcplex` module is off. A connection that the access
point does not expose returns a JSON-RPC error.

## WebSocket sessions

Two session endpoints are mounted under `/ap/{id}`. Both are GET upgrades:

- OpenAI Realtime: `GET /ap/{id}/v1/realtime?model={model}`.
- Gemini Live: `GET /ap/{id}/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent?model={model}`.

Rules that the code enforces:

- The query must carry exactly one `model` parameter, and the upgrade must have an empty
  body. Otherwise the request fails before the upgrade with `400 INVALID_REQUEST`.
- Refusals before the upgrade (unknown model, protocol mismatch, missing quota controls) are
  ordinary HTTP errors in the endpoint's own error shape.
- The access point rewrites the query to the surface's native model name before the native
  gateway dials. For Gemini Live the native gateway requires the `setup` message's model to
  name the same model as the query, after model normalization. When `name` differs from
  `native_model`, `setup.model` must therefore be the native name.
- Metering is the session model in [model providers](model-providers.md#websocket-sessions):
  one request-count admission and one concurrency permit per session. Token usage is
  advisory and is not limited by quota.

**Responses WebSocket mode is not routed through access points.** The `openai-responses`
wire has only `POST /v1/responses`, and no access-point route is registered for the
Responses WebSocket upgrade. The source comments give no reason. One likely reason, which is
an inference rather than a stated reason: access points select the model from the upgrade
request's `?model=`, while Responses WebSocket mode selects it per `response.create` event,
so an access point cannot pin the model at connect time. The native gateway still serves the
operation at `GET /api/v1/llm/gateway/{id}/v1/responses` for a surface, under the same
admission rules.

## Aggregate tools/list

`POST /mcp` answers `tools/list` with one page that aggregates every connection the access
point exposes. Each connection's own pages are followed within these bounds:

| Bound | Value |
| --- | --- |
| Upstream pages per connection | 20 |
| Tools across all connections | 2000. The check runs between pages, so the last page can pass the limit. Nothing already fetched is discarded. |
| Total time spent listing upstreams | 20 seconds |

A connection that fails, is denied or is unavailable at any page is omitted, and its error is
not relayed. A connection that a bound cuts short keeps the tools fetched so far. The result
then carries `_meta["io.aiplex/truncated"]`, a list of `{connection, reason}` entries. A
result with no truncation has no `_meta` member. The reasons are:

| Reason | Meaning |
| --- | --- |
| `page_limit` | The connection reached 20 pages. |
| `cursor_loop` | The upstream returned a cursor it had already returned. |
| `tool_limit` | The 2000-tool limit was reached. |
| `time_limit` | The 20-second limit was reached. |

A `tools/list` request on the aggregate that carries a `cursor` is refused. The aggregate
returns one page.

## QoS and quota

QuotaPlex stays the only limiter. An access point's QoS block is rendered into reviewable
QuotaPlex controls and is never enforced on its own.

```sh
aiplex access-points quota-plan ide-platform-team
```

The plan contains rate rules for `requests_per_minute` and `tokens_per_minute`, a
concurrency rule, the budget rules named in `budget_refs`, and a scheduler flow with the
QoS `weight`. Every control is scoped to this access point through `access_point_id`. Rate and
concurrency controls are also scoped to the caller's principal, unless `qos.scope` is
`access_point`. A plan includes `applied: false`. Apply it through the existing
QuotaPlex policy flow, not through this route. See [QuotaPlex administration](quotaplex-administration.md).

Notes from the rendering code:

- `qos.scope` chooses who shares the rate and concurrency limits. `principal` (the default) gives
  each caller principal its own allowance. `access_point` gives one pool shared by every caller of
  the access point. In either case the rules match this access point through `access_point_id`.
  Any other value is refused on save as `qos/scope`.
- `tokens_per_minute` admits on the bounded token exposure of a token meter. A model
  binding without that meter is denied.
- A QoS block with no limits and no matching quota control fails closed for requests.
- Re-rendering replaces this access point's earlier rate and concurrency rules.
- `budget_refs` select budgets that already exist in the active quota policy.

**Fail-closed rule.** A request through an access point with a `qos` block is refused
when no active quota control matches the access point and the target:

- Quota admission refuses both model and tool requests with `503
  QUOTA_ACCESS_POINT_UNCOVERED` when a QoS block exists and no budget, rate or
  concurrency rule covers the access point.
- When QuotaPlex is not configured at all, model requests return `503
  QUOTA_UNAVAILABLE`. Tool requests return a JSON-RPC error with `data.reason` set to
  `quota_unavailable`.

## Enable

Access point routes are mounted with the API. Each part needs its module:

| Part | Requires |
| --- | --- |
| `/ap/{id}` model routes | `llmplex` (`AIPLEX_MODULES`) |
| `/ap/{id}/mcp` routes | `mcplex` |
| `/ap/{id}/skills` | `skillsplex` |
| QoS | QuotaPlex enabled with `AIPLEX_MODULES` including `quotaplex` and an operator quota file (`AIPLEX_QUOTA_FILE`), and an applied quota policy that covers the access point |
| `aiplex connect` and overview routes | `AIPLEX_PUBLIC_URL` set to the trusted HTTPS origin. `connect` returns `409 PUBLIC_URL_REQUIRED` otherwise. |

## The single pane

The connect response's `endpoints` array lists the Bedrock and WebSocket endpoints of the
wires the access point serves. Its `models` entries carry `protocols`, which lists only the
served wires (see [model lists and connect output](#model-requests)). Each entry has `protocol`, `transport` (`http`, `http-stream`
or `websocket`), `url` (with `{model}` where the model goes), `label`, `query` (the query
parameters the client must send, for the WebSocket entries `model`), and `models` (the
access point's model names that reach that protocol). The `wss://` or `ws://` scheme follows
the public URL's scheme. Responses WebSocket mode is not listed, because it is not routed
through access points.

`aiplex who` lists callers of one kind, or one caller in detail, with usage for a window
(24 hours by default):

```sh
aiplex who --kind agent
aiplex who user principal-ab12… --since 7d
aiplex who --kind client -o json
```

`aiplex connect` prints configuration for a client profile:

```sh
aiplex connect ide-platform-team
aiplex connect ide-platform-team --client claude-code
aiplex connect ide-platform-team -o json
```

`aiplex connect <id> --client claude-code|codex|gemini-cli --write` merges that configuration
into the client's own config file instead of printing it, with a diff, a backup and
`aiplex connect --undo`; it never writes a token value. See [Harness onboarding](harness-onboarding.md).
It does not install the client.

Profiles are `generic` (the default), `antigravity`, `antigravity-acp`, `codex`,
`claude-code` and `claude-desktop`. A profile is offered only when the access point
serves a protocol that the client speaks. If it does not, the response is
`422 CLIENT_NOT_APPLICABLE` and names the protocols the access point offers. An unknown
profile returns `404 UNKNOWN_CLIENT`.

`claude-desktop` speaks the Claude apps gateway protocol, which signs in through a gateway
server rather than an access point. `--client claude-desktop` is therefore always refused with
`422 CLIENT_NOT_APPLICABLE`, and the message says to use `claude-code` for an access point.

Any verified caller that the access point admits can run `aiplex connect`. Operators (admins
and deployers) get the full response. Other callers get a reduced one: only the auth step and
`caller_kinds` for their own kind, and no notes that name internal resource IDs or the other
subjects the access point admits. A caller the access point does not admit gets `404`, the same
as for a missing access point.

The snippets read the bearer token from the `AIPLEX_TOKEN` environment variable. No
snippet contains a token or secret value. The caller still needs a token that the
access point admits.

`aiplex access-points` also has `list`, `get`, `create` (see [Create one with the CLI](#create-one-with-the-cli)),
`apply -f <file>` (JSON or YAML), and `delete <id>`. `apply` creates or replaces the access point from the full specification.

## Limits

- No cross-protocol translation. A model is reachable only through the wire protocols
  its surface speaks natively.
- Access points do not intercept traffic that bypasses them. Envoy routes, direct
  provider calls and other paths are unaffected.
- A client ID from a public OAuth client is attribution only. Do not use it as the sole
  control for a sensitive access point.
- Skills are exposed as manifests through `/skills`. This page does not describe skill
  invocation.
- The `connect` output is built from the configured public URL. It does not check that
  the client can reach that URL.
- Overview data is read from the usage ledger and resource store. Its usage roll-ups are
  admin-only. A deployer gets the same configuration view with usage withheld. Roll-ups are only
  as complete as the ledger's records.
- Google Agent Registry import, publish and Agent Gateway push-down are not part of this
  increment.
- Responses WebSocket mode is not reachable through an access point (see
  [WebSocket sessions](#websocket-sessions)).
- Bedrock and WebSocket usage through an access point is advisory, the same as on the
  direct routes. Quota bounds the request count and rates, not tokens, for those paths.
- Bedrock InvokeModel bodies that are not Messages-shaped are not observed for usage. See
  [model providers](model-providers.md) and the
  [usage-format coverage matrix](reference/usage-formats/coverage.md).
- Batch and file operations are not routed. Batch usage is settled through the native route
  or the client facade. See [model batches](model-batches.md).
- The aggregate `tools/list` is bounded. It is not a complete listing when the result carries
  a truncation marker. See [aggregate tools/list](#aggregate-toolslist).

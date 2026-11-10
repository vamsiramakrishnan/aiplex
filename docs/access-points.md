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
| `models[]` | `name` is the client-facing name, `surface_ref` a saved model surface, `native_model` the model the surface lists. Names are unique. |
| `tools[]` | `connection_ref` names a saved tool connection. `expose` is an optional list of tool names or prefixes ending in `*`. Omitted means every tool the connection permits. |
| `skills[]` | `instance_ref` names a deployed skill instance. Requires the `skillsplex` module. |
| `qos` | `class` (lower-case, up to 63 characters), `weight`, `limits` (`requests_per_minute`, `tokens_per_minute`, `concurrency`) and `budget_refs`. Rendered into QuotaPlex controls. See [QoS and quota](#qos-and-quota). |

Unknown fields are rejected. The API returns a validation error for any reference that
does not resolve in the tenant.

## Routes

Management routes are under `/api/v1`. Reads follow resource revision semantics. Writes
need the admin role.

| Route | Purpose |
| --- | --- |
| `GET /api/v1/access-points` | List access points. |
| `GET /api/v1/access-points/{id}` | One access point, with its revision. |
| `PUT /api/v1/access-points/{id}` | Create or replace. Updates send the current revision in `If-Match`. |
| `DELETE /api/v1/access-points/{id}` | Delete. |
| `POST /api/v1/access-points/{id}/quota-plan` | Render the QoS block as QuotaPlex controls for review. Admin only. Does not activate anything. |
| `GET /api/v1/overview/subjects?kind=agent\|user\|client` | Callers of one kind, with identity source, last seen, access points and a usage roll-up. Admin or deployer. |
| `GET /api/v1/overview/subjects/{kind}/{id}` | Effective access of one caller, with the reason for each grant. Also `GET /api/v1/overview/subjects/{kind}?id=…` for IDs that contain `/`. |
| `GET /api/v1/overview/access-points/{id}` | Callers, resources, QoS and live usage of one access point. |
| `GET /api/v1/overview/access-points/{id}/connect?client=…` | Ready-to-paste configuration for one client profile. Defaults to `generic`. |

Client-facing routes are at the server root, not under `/api/v1`. They use the
caller's own bearer token. A request without a verified identity is refused.

| Path under `/ap/{id}` | Protocol | Notes |
| --- | --- | --- |
| `POST /v1/chat/completions` | OpenAI Chat Completions | The model name is in the body. |
| `POST /v1/responses` | OpenAI Responses | The model name is in the body. |
| `POST /v1/messages` | Anthropic Messages | The model name is in the body. |
| `POST /v1beta/models/{model}:{method}` | Google GenAI | The model name is in the path. |
| `POST /model/{model}/converse` and `POST /model/{model}/converse-stream` | Bedrock Converse | The model name is in the path. Model IDs may contain `:`. |
| `POST /model/{model}/invoke` and `POST /model/{model}/invoke-with-response-stream` | Bedrock InvokeModel | The model name is in the path. Model IDs may contain `:`. |
| `GET /v1/realtime?model={model}` (WebSocket upgrade) | OpenAI Realtime | The model is selected by the query parameter. See [WebSocket sessions](#websocket-sessions). |
| `GET /ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent?model={model}` (WebSocket upgrade) | Gemini Live (Vertex Live) | The model is selected by the query parameter. See [WebSocket sessions](#websocket-sessions). |
| `GET /v1/models` | OpenAI-shaped list | Anthropic-shaped when the request carries `anthropic-version`. Only models the caller may use. |
| `GET /v1beta/models` | Google GenAI list | Only models reachable through that protocol. |
| `POST /mcp` | MCP Streamable HTTP | Aggregates every exposed tool as `{connection}__{tool}`. |
| `POST /mcp/{connection}` | MCP Streamable HTTP | One connection, with original tool names. |
| `GET /v0.1/servers` | MCP registry listing | For clients that take a catalog URL. |
| `GET /skills` | Skill manifest | Exposed skills. Requires `skillsplex`. |
| `GET /.well-known/oauth-protected-resource`, `…/mcp`, `…/mcp/{connection}` | RFC 9728 metadata | Public. Points at the operator's authorization server. |

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

A model is reachable through a protocol only when its surface speaks that protocol
natively. There is no cross-protocol translation. A request that uses a protocol the
surface does not serve returns `400 PROTOCOL_MISMATCH` in that protocol's own error
shape, and the message lists the paths that do serve the model.

Other model errors:

| Status and code | Meaning |
| --- | --- |
| `404 MODEL_NOT_FOUND` | The model is not in the access point, or the caller may not use it. |
| `404 UNSUPPORTED_OPERATION` | A GenAI operation that the surface does not implement. |
| `503 SURFACE_UNAVAILABLE` | The surface cannot be resolved right now. |
| `503 MODEL_ALIAS_UNSUPPORTED` | The client name differs from `native_model`, and the surface relays the body verbatim. |
| `404 MODULE_DISABLED` | The `llmplex` module is off. |
| `413 REQUEST_TOO_LARGE` | The body exceeds 8 MiB. |

Model aliasing is limited. The client name may differ from `native_model` only when the
access point can rewrite it without touching a body the surface relays verbatim:

| Wire | Alias allowed | How the name is rewritten |
| --- | --- | --- |
| GenAI | Yes | The path. |
| Bedrock Converse and InvokeModel | Yes | The path segment. |
| OpenAI Realtime | Yes | The `model` query parameter. |
| Gemini Live | Yes | The `model` query parameter, rewritten to the native name. |
| Anthropic Messages | Only when the surface is not a ready canonical operation | The body. |
| OpenAI Chat Completions and Responses | No | The body is relayed verbatim, so the client name must equal `native_model`. |

The configuration check rejects an alias that the surface cannot rewrite.

Model surface protocols map to wire protocols as follows: `openai-chat` to Chat
Completions, `openai-responses` to Responses, `anthropic-messages` to Messages,
`google-genai` to GenAI, `bedrock-converse` to Bedrock Converse, `bedrock-invoke` to
Bedrock InvokeModel and `openai-realtime` to OpenAI Realtime. Gemini Live is served from
`google-genai` surfaces.

Model names may contain letters, digits, `.`, `_`, `@`, `/`, `:` and `-`. The colon is
allowed because Bedrock model IDs use it.

## Tool requests

Each `tools/call` goes through the managed tool path: connection policy, the caller's
`mcp:tools:` grant, quota, the upstream credential and the usage ledger. See
[tool connection auth](tool-connection-auth.md).

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

## QoS and quota

QuotaPlex stays the only limiter. An access point's QoS block is rendered into reviewable
QuotaPlex controls and is never enforced on its own.

```sh
aiplex access-points quota-plan ide-platform-team
```

The plan contains rate rules for `requests_per_minute` and `tokens_per_minute`, a
concurrency rule, the budget rules named in `budget_refs`, and a scheduler flow with the
QoS `weight`. Every control is scoped to the caller's principal and to this access point
through `access_point_id`. A plan includes `applied: false`. Apply it through the existing
QuotaPlex policy flow, not through this route. See [QuotaPlex administration](quotaplex-administration.md).

Notes from the rendering code:

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

Profiles are `generic` (the default), `antigravity`, `antigravity-acp`, `codex`,
`claude-code` and `claude-desktop`. A profile is offered only when the access point
serves a protocol that the client speaks. If it does not, the response is
`422` and lists the protocols the access point offers. An unknown profile returns
`404 UNKNOWN_CLIENT`.

The snippets read the bearer token from the `AIPLEX_TOKEN` environment variable. No
snippet contains a token or secret value. The caller still needs a token that the
access point admits.

`aiplex access-points` also has `list`, `get`, `apply -f <file>` (JSON or YAML), and
`delete <id>`. `apply` creates or replaces the access point.

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
- Overview data is read from the usage ledger and resource store. Its roll-ups are only
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

---
title: Access Points API
---

An access point is one stable URL, `/ap/{id}/…`, in front of a tenant's models, tool connections and skill servers. It is the enforcement point for caller identity, admission, quality of service and quota. The control-plane resource under `/api/v1/access-points` stores the specification. Reads are open to any verified caller of the tenant. Writes and quota-plan rendering require the `admin` role. Clients call the `/ap/{id}/…` endpoints with their own verified token, and the caller kind and admission are derived from that token and the saved access point, never from the request.

## Control-plane routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/access-points` | List saved access points of the tenant | Verified identity and tenant |
| GET | `/api/v1/access-points/{id}` | Read one access point | Verified identity and tenant |
| PUT | `/api/v1/access-points/{id}` | Create or update an access point | `admin` role |
| DELETE | `/api/v1/access-points/{id}` | Delete an access point | `admin` role |
| POST | `/api/v1/access-points/{id}/quota-plan` | Render and validate the QoS quota fragment. Never activates a policy. | `admin` role |

All routes are under `/api/v1`, which requires a verified identity. Outside development mode, the `admin` or `deployer` role is required for non-GET methods, and the access-point writes narrow that to `admin`. A caller without the role receives `403 FORBIDDEN`.

## Access point specification

The resource `id` must equal the `id` field. The body is decoded strictly, so unknown fields are rejected. Maximum body size is 1 MiB, the same as other resource writes.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | Yes | Matches `[A-Za-z0-9._-]` with a first character that is alphanumeric, 1 to 128 characters. Used in the `/ap/{id}` path. |
| `display_name` | string | No | Printable text, at most 256 characters, with no leading or trailing space |
| `caller_kinds` | array of string | Yes | One to three unique values from `agent`, `user`, `client` |
| `subjects` | object | Yes | Who may call. An empty object admits every caller of an allowed kind. See Subjects. |
| `models` | array of object | No | Client-facing model names. See Models. |
| `tools` | array of object | No | Tool connections exposed. See Tools. |
| `skills` | array of object | No | Skill server instances exposed. See Skills. |
| `qos` | object | No | Quality of service and limits. See QoS. |
| `labels` | object of string | No | Up to 8 trusted experiment tags, stored as `tags` on every usage record of an exchange through this access point. Keys match `[a-z][a-z0-9_.-]{0,31}`, values are up to 64 characters of `[A-Za-z0-9_.:/@+() -]` and are not `(other)`. A caller can add tags only with the `X-AIPlex-Tags` header for keys the operator allowed, and cannot override a label. |

At least one of `models`, `tools` or `skills` must be present.

### Subjects

| Field | Type | Meaning |
|---|---|---|
| `groups` | array of `{source, id}` | Source-qualified group references, at most 256, unique |
| `principals` | array of string | Principal identifiers, at most 256, unique, each up to 1024 characters |
| `agents` | array of string | Agent identifiers, at most 256, unique, each up to 1024 characters |
| `client_registrations` | array of string | ClientPlex registration identifiers, at most 256, unique, each up to 256 characters. Each must resolve in the tenant. |

`principals`, `groups` and `agents` are one identity constraint: any one of them admits. `client_registrations` is an independent constraint. When both are set, a caller must satisfy both. A client registration matched only by a public OAuth client id is attribution, not a security boundary.

### Models

| Field | Type | Required | Meaning |
|---|---|---|---|
| `name` | string | Yes | Client-facing model name. Matches `[A-Za-z0-9._@-]` first, then `[A-Za-z0-9._@/:-]`, up to 256 characters, no `..`. Unique within the access point. |
| `surface_ref` | string | Yes | Identifier of a saved `model-surface` resource in the tenant |
| `native_model` | string | Yes | Model identifier the surface lists. Same character rules as `name`. |

A `name` that differs from `native_model` is allowed only when the surface's facade rewrites the model in the request. Otherwise the save is rejected.

### Tools

| Field | Type | Required | Meaning |
|---|---|---|---|
| `connection_ref` | string | Yes | Identifier of a managed `tool-connection` resource. Must match `[A-Za-z0-9][A-Za-z0-9_.-]{0,62}`, contain no `__` and not end in `_`. |
| `expose` | array of string | No | Tool names or prefixes ending in `*`. Omitted exposes every tool the connection permits. Validated as a tool policy. |

At most 64 tool entries, each connection listed once.

### Skills

| Field | Type | Required | Meaning |
|---|---|---|---|
| `instance_ref` | string | Yes | Identifier of a SkillsPlex instance labelled with this tenant |

At most 64 entries. Requires a configured skill registry.

### QoS

| Field | Type | Required | Meaning |
|---|---|---|---|
| `class` | string | Yes | Matches `[a-z][a-z0-9-]{0,62}` |
| `weight` | integer | No | 1 to 1000. Sets a scheduler flow weight when non-zero. |
| `limits` | object | No | At least one of the fields below must be positive |
| `budget_refs` | array of string | No | Budget identifiers that exist in the tenant's active quota policy. Requires a quota policy. |

`limits` fields:

| Field | Type | Bound |
|---|---|---|
| `requests_per_minute` | integer | 0 to 2^40 |
| `tokens_per_minute` | integer | 0 to 2^50 |
| `concurrency` | integer | 0 to 2^20 |

QoS is rendered into a quota policy fragment for review and is never enforced on its own. A request through an access point with a `qos` block requires quota enforcement and fails closed otherwise.

## GET /api/v1/access-points

Returns a JSON array of resource envelopes for the tenant. Each envelope has `tenant_id`, `kind` (`access-point`), `id`, `revision` and `spec`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/access-points" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/access-points/{id}`

Returns one resource envelope with an `ETag` header holding the revision in quotes.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Envelope returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No access point with this identifier |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/access-points/ap-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## PUT `/api/v1/access-points/{id}`

Creates or replaces an access point. The body is the specification described above. Revision rules are the same as for other resources: omit `If-Match` to create, and send the current revision in `If-Match` to replace. A mismatch returns `409 REVISION_CONFLICT`.

The specification is validated before it is stored. Validation resolves every reference in the tenant: the model surfaces, tool connections, client registrations, skill instances and budgets. An unresolvable reference is rejected.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Stored. Body is the envelope with the new revision and an `ETag` header. |
| 400 | `INVALID_REVISION` | `If-Match` is not a revision number |
| 400 | `INVALID_SPEC` | Body fails strict decoding or any validation rule, including unresolved references. The `message` says which rule failed. |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `FORBIDDEN` | Caller lacks the `admin` role. The message is `access point changes require the admin role`. |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS -X PUT "$AIPLEX_URL/api/v1/access-points/ap-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"ap-example","caller_kinds":["user"],"subjects":{},"models":[{"name":"model-example","surface_ref":"surface-example","native_model":"model-example"}]}'
```

## DELETE `/api/v1/access-points/{id}`

Deletes an access point. `If-Match` is required and must carry the current revision. Without it the request fails with `428`. On success the response is `204` with no body. Once deleted, `/ap/{id}` requests return `404 NOT_FOUND`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 204 | none | Deleted |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `FORBIDDEN` | Caller lacks the `admin` role |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No live access point with this identifier |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `REVISION_REQUIRED` | `If-Match` missing, zero, or not a revision number |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS -X DELETE "$AIPLEX_URL/api/v1/access-points/ap-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H 'If-Match: "2"'
```

## POST `/api/v1/access-points/{id}/quota-plan`

Renders the QoS block of a saved access point as a reviewable quota policy fragment and validates it against the tenant's active policy, or as a standalone policy when none is configured. It reads state only. It never activates a policy, and the response field `applied` is always `false`. Apply the fragment through the normal quota policy flow.

The request has no body.

### Response

| Field | Type | Meaning |
|---|---|---|
| `access_point_id` | string | Identifier of the access point |
| `tenant_id` | string | Tenant of the request |
| `applied` | boolean | Always `false` |
| `fragment` | object | `meters`, `rates`, `concurrency` and `budget_refs`. Each is omitted when empty. The `rates` and `concurrency` rules are scoped per principal and match the access point through `match.access_point_id`. |
| `scheduler` | object | Omitted unless `qos.weight` is set. Contains `class`, `flow_id` (`ap-{id}-{class}`), `weight` and `routes`. Each route has `resource_id`, `operation_id`, `access_point_id` and `flow_id`. |
| `policy` | object | The full rendered quota policy, as defined in `pkg/quotaplex` |
| `validation` | object | `valid` (boolean), `validator` (`engine` when checked against the active policy, otherwise `compile`), `reference` (omitted unless valid), `error` (omitted unless invalid) |
| `warnings` | array of string | Advisory messages, never an empty null |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Plan rendered. A failed validation is still `200` with `validation.valid` set to `false`. |
| 400 | `NO_QOS` | The access point has no `qos` block |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `FORBIDDEN` | Caller lacks the `admin` role |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No access point with this identifier |
| 409 | `ACCESS_POINT_INVALID` | The saved access point fails validation |
| 503 | `QUOTA_UNAVAILABLE` | The quota policy store could not be read |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/access-points/ap-example/quota-plan" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

Abridged response for an access point with `qos.limits.requests_per_minute`:

```json
{
  "access_point_id": "ap-example",
  "tenant_id": "tenant-example",
  "applied": false,
  "fragment": {
    "rates": [
      {
        "id": "ap-ap-example-rpm",
        "match": {"access_point_id": "ap-example"},
        "meter_id": "requests",
        "rate": 60,
        "burst": 60,
        "basis": "admitted"
      }
    ]
  },
  "warnings": []
}
```

## Client endpoints under `/ap/{id}`

These endpoints are called by clients with their own verified token. Every endpoint first runs the access point gate, described below, so an access point that is missing, not admitted to the caller, or invalid is refused before any model, tool or skill is reached.

Model, media and utility wires answer in the calling protocol's own error shape. The MCP endpoint answers JSON-RPC errors. The discovery endpoints answer the AIPlex error shape.

| Method | Path under `/ap/{id}` | Wire | Purpose |
|---|---|---|---|
| POST | `/v1/chat/completions` | `openai-chat-completions` | OpenAI Chat Completions |
| POST | `/v1/responses` | `openai-responses` | OpenAI Responses |
| POST | `/v1/messages` | `anthropic-messages` | Anthropic Messages |
| POST | `/v1/messages/count_tokens` | `anthropic-messages` | Anthropic token counting |
| POST | `/v1beta/models/{target}` | `google-genai` | Google GenAI. `{target}` is `{model}:{method}`. |
| POST | `/v1beta/cachedContents` | `google-genai` | Create cached content. The model is in the body. |
| POST | `/v1beta/interactions` | `google-genai` | Create an interaction. The model is in the body. |
| GET | `/v1beta/cachedContents` | `google-genai` | List cached content (model-less) |
| GET, PATCH, DELETE | `/v1beta/cachedContents/{rid}` | `google-genai` | Read, update or delete cached content (model-less) |
| GET, DELETE | `/v1beta/interactions/{rid}` | `google-genai` | Read or delete an interaction (model-less) |
| POST | `/v1beta/interactions/{rid}/cancel` | `google-genai` | Cancel an interaction (model-less) |
| POST | `/model/{target}/converse` | `bedrock-converse` | Bedrock Converse |
| POST | `/model/{target}/converse-stream` | `bedrock-converse` | Bedrock ConverseStream |
| POST | `/model/{target}/invoke` | `bedrock-invoke` | Bedrock InvokeModel |
| POST | `/model/{target}/invoke-with-response-stream` | `bedrock-invoke` | Bedrock InvokeModelWithResponseStream |
| GET (WebSocket) | `/v1/realtime?model={model}` | `openai-realtime` | OpenAI Realtime session |
| GET (WebSocket) | `/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent?model={model}` | `google-live` | Gemini Live session |
| POST | `/v1/images/generations` | `openai-images` | Image generation |
| POST | `/v1/images/edits` | `openai-images` | Image edits (multipart) |
| POST | `/v1/audio/transcriptions` | `openai-audio` | Audio transcription (multipart) |
| POST | `/v1/audio/translations` | `openai-audio` | Audio translation (multipart) |
| POST | `/v1/audio/speech` | `openai-audio` | Text to speech |
| POST | `/v1/embeddings` | `openai-embeddings` | Embeddings |
| POST | `/v1/moderations` | `openai-moderations` | Moderations |
| GET | `/v1/responses/{rid}` | `openai-responses` | Read a response (model-less) |
| DELETE | `/v1/responses/{rid}` | `openai-responses` | Delete a response (model-less) |
| POST | `/v1/responses/{rid}/cancel` | `openai-responses` | Cancel a response (model-less) |
| GET | `/v1/models` | none | List the models the caller may use, in the OpenAI shape. The Anthropic shape is returned when the request carries an `anthropic-version` header. |
| GET | `/v1beta/models` | none | List the models the caller may use, in the Google GenAI shape |
| POST | `/mcp` | MCP | Aggregate MCP endpoint across the access point's tool connections |
| POST | `/mcp/{connection}` | MCP | MCP endpoint for one tool connection |
| GET | `/v0.1/servers` | none | MCP registry view of the access point (one aggregate entry and one entry per connection) |
| GET | `/skills` | none | Manifests of the skill servers the caller may use |
| GET | `/.well-known/oauth-protected-resource` | none | RFC 9728 metadata. Public. |
| GET | `/.well-known/oauth-protected-resource/mcp` | none | RFC 9728 metadata for the aggregate MCP resource. Public. |
| GET | `/.well-known/oauth-protected-resource/mcp/{connection}` | none | RFC 9728 metadata for one connection. Public. |

The metadata routes are also served at the root as `/.well-known/oauth-protected-resource/ap/{apid}/mcp` and `/.well-known/oauth-protected-resource/ap/{apid}/mcp/{connection}`.

Wire-to-surface mapping is by protocol: a model is reachable through a wire only when its surface protocol is served by that wire. A request on the wrong wire is refused with `PROTOCOL_MISMATCH`, which names the wires that do serve the model.

### Access point gate

Every endpoint in this table runs the gate in this order. The first failure ends the request.

1. A verified identity must be present. Otherwise `401 UNAUTHENTICATED`.
2. The tenant must be bound. Otherwise `403 TENANT_REQUIRED`.
3. The access point must exist in the tenant. Otherwise `404 NOT_FOUND`. A store failure returns `503 ACCESS_POINT_UNAVAILABLE`.
4. The caller kind is derived from the verified identity and the tenant's client registrations, never from the request. If the kind cannot be resolved, `503 CALLER_UNAVAILABLE`.
   - `agent` when the token carries a delegated actor, or when the principal is a workload identity.
   - `client` when the authorized party equals a ClientPlex registration in the tenant.
   - `user` otherwise.
5. `caller_kinds` and `subjects` must admit the caller. Otherwise `403 CALLER_NOT_ALLOWED`.
6. Every reference of the access point is re-validated. A failure returns `503 ACCESS_POINT_UNAVAILABLE`.

An admitted request is delegated to the managed model, tool or skill handler with the access-point call attached, so the same quota, usage and authorization logic applies. `GET /v1/models` lists only the models whose `llm:model:{model_id}` grant the caller holds.

### Model endpoint errors (native shape)

The model wires (chat, responses, messages, GenAI, Bedrock, realtime, live, media, utility) return errors in the calling protocol's own shape. The status and the message are always present. The AIPlex code appears only in the OpenAI-shaped body, as `error.code` in lowercase. Any unauthenticated `401` also sets `WWW-Authenticate: Bearer`. Provider error bodies pass through unchanged.

| Status | Code | When |
|---|---|---|
| 400 | `INVALID_REQUEST` | Body cannot be read for the model, or a WebSocket session has an invalid `model` query |
| 400 | `INVALID_PATH` | Request path cannot be used |
| 400 | `PROTOCOL_MISMATCH` | The model is not served through this wire |
| 400 | `AMBIGUOUS_SURFACE` | A model-less operation on an access point with several surfaces that serve it |
| 401 | `UNAUTHENTICATED` | No verified identity |
| 403 | `TENANT_REQUIRED` | No tenant binding |
| 403 | `CALLER_NOT_ALLOWED` | The access point does not admit the caller |
| 404 | `NOT_FOUND` | Access point not found, or a model-less resource id is not owned through this access point |
| 404 | `MODEL_NOT_FOUND` | The requested model is not an entry of this access point |
| 404 | `MODULE_DISABLED` | The LLM module is disabled |
| 404 | `UNSUPPORTED_OPERATION` | The path is not an operation this wire supports |
| 409 | `AMBIGUOUS_RESOURCE` | A model-less resource id is owned through more than one surface |
| 413 | `REQUEST_TOO_LARGE` | Body exceeds 8 MiB (model wires) or 1 MiB (model-less operations) |
| 503 | `ACCESS_POINT_UNAVAILABLE`, `CALLER_UNAVAILABLE` | See the gate |
| 503 | `SURFACE_UNAVAILABLE` | The model surface cannot be read |
| 503 | `SURFACE_UNSUPPORTED` | The surface cannot serve this protocol |
| 503 | `QUOTA_UNAVAILABLE` | The access point has a `qos` block and quota enforcement is not configured |
| 503 | `MODEL_ALIAS_UNSUPPORTED` | The model name differs from the native name on a surface that relays bodies verbatim |

Responses that the gateway returns successfully pass through unchanged, including streams.

### Model listing response

`GET /v1/models` in the OpenAI shape:

```json
{
  "object": "list",
  "data": [
    {"id": "model-example", "object": "model", "created": 0, "owned_by": "aiplex", "protocols": ["openai-chat-completions"]}
  ]
}
```

`owned_by` is the surface publisher, or `aiplex` when none is set. `GET /v1beta/models` returns `{"models": [...]}` with `name` (`models/{name}`), `displayName` and `supportedGenerationMethods`, and lists only the models served through the Google GenAI wire.

### MCP endpoints

The MCP endpoints implement the stateless Streamable HTTP transport. They answer `initialize`, `ping`, `tools/list` and `tools/call` over `POST` with JSON responses. They never mint an `Mcp-Session-Id`, and any method other than POST returns `405` with `Allow: POST`. A request with a JSON-RPC batch returns `400`.

Requests must send `Content-Type: application/json` and carry a body of at most 1 MiB. `MCP-Protocol-Version` may be `2025-11-25`, `2025-06-18` or `2025-03-26`. When the header is absent, `2025-03-26` is assumed. When an `Origin` header is sent, it must be the public URL origin or one of the configured allowed origins. Otherwise the request is refused with HTTP `403`.

The aggregate endpoint names tools `{connection}__{tool}`. A tool call on the aggregate endpoint must use that name. Errors arrive as JSON-RPC errors:

| Code | Meaning |
|---|---|
| -32700 | Parse error (invalid JSON body) |
| -32600 | Invalid request. Also used for gate failures other than 403, such as a missing identity (401) or an unknown access point (404). Gate failures carry `error.data.reason`, the lowercased AIPlex code. |
| -32001 | Forbidden. Used for a gate denial (403) and for a tool-policy refusal. |
| -32601 | Method not found |
| -32602 | Invalid parameters, unknown tool, or a cursor sent to the aggregate `tools/list` |
| -32603 | Internal error, including access points with QoS and no quota enforcement |
| -32003 | Quota exceeded. Used when the managed tool answers `429`. |

Gate and tool errors that are JSON-RPC errors return HTTP `200` unless the failure is at the transport level, such as a gate failure, a wrong `Content-Type` (`415`), an oversize body (`413`), or an invalid JSON-RPC envelope (`400`).

`tools/list` on the aggregate endpoint accepts no cursor. Connections that fail, are denied or are unavailable are omitted without relaying their error. When a bound stops the listing, the result carries `_meta["io.aiplex/truncated"]`, a list of `{"connection", "reason"}` entries whose reason is `tool_limit`, `time_limit`, `cursor_loop` or `page_limit`.

A successful `tools/call` returns the managed tool's result unchanged. The response can carry `Retry-After` and `X-Aiplex-Attempt-ID` headers.

### Discovery responses

`GET /v0.1/servers`:

```json
{
  "templates": [],
  "total": 0,
  "page": 0,
  "page_size": 0
}
```

Each entry carries the template fields and a `remotes` list with `type: streamable-http` and the MCP URL. The response contains an aggregate entry only when the access point has tools and the MCP module is enabled.

`GET /skills` returns `access_point_id`, `servers` (the manifests the caller may invoke) and `unavailable` (instance references that are not running or whose template cannot be read). Errors use the AIPlex error shape.

## Behaviour notes

- **Admission is per request.** The gate runs on every request. A revoked identity, a changed group or a removed client registration takes effect on the next request.
- **Caller kind is never client-supplied.** The caller kind is derived from the verified token and the tenant's client registrations. A client cannot choose it.
- **Revisions.** `ETag` and `If-Match` follow the resource rules: the revision is the value in the `ETag`, quoted. Deletion requires `If-Match`.
- **Strict specs.** Unknown fields are rejected on write and on every request, because the saved specification is decoded strictly again at the gate.
- **Re-validation.** Every model, tool, skill and budget reference is resolved again at each request. An access point that references a deleted resource fails with `503 ACCESS_POINT_UNAVAILABLE` rather than silently dropping that entry.
- **QoS fails closed.** An access point with a `qos` block refuses requests when quota enforcement is not configured or no matching quota control exists.
- **Model names.** A client-facing `name` that differs from `native_model` is rewritten only on wires that rewrite the model in the path, the GenAI path or the Anthropic body. Elsewhere the name must be the native name.
- **Quota plan is read-only.** It validates the fragment and returns it. It never changes the active policy.
- **Public metadata.** The RFC 9728 metadata routes are public and do not reveal whether an access point exists. An unknown or malformed identifier returns plain `404`.
- **Body limits.** Model wires accept up to 8 MiB. Model-less operations and MCP accept up to 1 MiB.
- **Usage.** Requests through an access point are recorded in the usage ledger with `access_point_id` and `caller_kind`. See the usage API.

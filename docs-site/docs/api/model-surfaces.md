---
title: Model Surfaces API
---

A model surface is a saved connection from AIPlex to one model backend: a Google Cloud publisher surface, or a provider surface for OpenAI, Anthropic, Azure OpenAI, Bedrock, Gemini Developer or an OpenAI-compatible server. The control-plane resource under `/api/v1/llm/surfaces` stores the connection. The client gateway under `/api/v1/llm/gateway/{id}/…` lets a client call the surface with its own wire protocol, and the native gateway enforces the per-operation grants. Surfaces are tenant-scoped. Writes need the `admin` or `deployer` role, and capacity reads need the `admin` role.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/llm/surfaces` | List the tenant's model surfaces | `admin` or `deployer` outside development mode; the tenant comes from the verified identity |
| GET | `/api/v1/llm/surfaces/{id}` | Read one surface | `admin` or `deployer` outside development mode; the tenant comes from the verified identity |
| PUT | `/api/v1/llm/surfaces/{id}` | Create or replace a surface | `admin` or `deployer` |
| DELETE | `/api/v1/llm/surfaces/{id}` | Delete a surface. `If-Match` required. | `admin` or `deployer` |
| GET | `/api/v1/llm/surfaces/{id}/capacity` | Last observed provider limits and capacity policy state | `admin` |
| GET | `/api/v1/llm/operations` | Operation catalogue, or one provider's descriptor | Verified identity |
| ANY | `/api/v1/llm/gateway/{id}/*` | Client gateway: client wire paths mapped to the surface's native operations | Non-GET: `admin` or `deployer`. Per operation: scope grants checked by the native gateway. |

The `/api/v1/llm/native/{id}/*` route is the native gateway that the client gateway calls internally. It is mounted but not documented on this page. See the report for the reasons.

All routes are under `/api/v1` and require a verified identity. Errors use this shape:

```json
{
  "code": "NOT_FOUND",
  "message": "resource not found",
  "request_id": "req-example"
}
```

## Surface resource

The resource `id` is the surface `id`. The body is the connection below. Unknown fields are rejected. The body is at most 1 MiB.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `surface` | object | Yes | The surface specification. See Surface. The `surface.id` must equal the resource `id`. |
| `identity_ref` | string | Google surfaces | Operator identity profile. Its allowed origins must approve the surface origin. |
| `network_profile_ref` | string | No | Saved `network-profile` resource. Checked on every call. |
| `billing_project` | string | No | Billing project for the `X-Goog-User-Project` header. Must not contain whitespace, `/`, `?` or `#`. |
| `auth` | object | Provider surfaces only | Tenant-scoped secret reference used as the upstream credential. Refused on Google surfaces. |
| `capacity_policy` | object | No | Advisory enforcement of provider limit signals. Provider surfaces only. See Capacity policy. |

### Surface

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Surface identifier. Letters, digits, `.`, `_`, `@` and `-`, starting with a letter or digit. |
| `protocol` | string | Wire protocol of the surface, such as `google-genai`, `openai-chat`, `openai-responses`, `anthropic-messages` or `vertex-anthropic` |
| `origin` | string | Configured HTTP(S) origin. No path, query or fragment. |
| `api_version` | string | `v1` or `v1beta1` |
| `project` | string | Google Cloud project. Required. |
| `location` | string | Google Cloud location. Required. |
| `publisher` | string | Omitted when empty. Publisher segment. |
| `data_prefixes` | array of string | Omitted when empty. Cloud Storage prefixes the surface may use. |
| `models` | array of string | Model or agent identifiers the surface lists. Each must not contain `?`, `#`, `%`, `..`, a leading `/` or control characters. |
| `agents` | array of string | Omitted when empty. Agent identifiers the surface lists. |
| `endpoint` | string | Omitted when empty. Endpoint segment for endpoint-scoped protocols. |
| `operations` | array of operation | Required, not empty. The operations the surface permits. |
| `provider` | object | Omitted for Google surfaces. Provider specification. See Provider block. |
| `websocket` | object | Omitted when defaults apply. Session metering. See WebSocket block. |

### Operation

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Unique within the surface. Used in grants as `llm:surface:{surface}:{id}`. |
| `method` | string | `GET`, `POST`, `PATCH` or `DELETE` |
| `path` | string | Path template. Must start with `/`. Placeholders are `{version}`, `{project}`, `{location}`, `{publisher}`, `{model}`, `{endpoint}` and `{id}`. |
| `transport` | string | `http`, `sse` or `websocket` |
| `action` | string | One of `create`, `read`, `list`, `update`, `delete`, `cancel` or `invoke` |
| `resource_kind` | string | Required when `stateful` is true. Kind of the resource the operation creates or reads. |
| `stateful` | boolean | Omitted unless `true`. The operation creates or reads a resource owned by the caller. |
| `source_url` | string | Documentation source of the operation |

Surface validation also checks that the operation identifiers are unique and that `websocket.metering` is valid for the operation.

### Provider block

Set on provider surfaces only. Provider surfaces select their upstream credential through `auth` (a tenant secret reference) or through `identity_ref` (an operator identity profile, such as `azure-default` for Azure OpenAI, `aws-default` for Bedrock, or `bearer-env` for OpenAI and OpenAI-compatible servers). Google surfaces keep `identity_ref` only.

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Provider identifier. One of `openai`, `anthropic`, `azure-openai`, `bedrock`, `openai-compatible` or `gemini-developer`. |
| `base_path` | string | Omitted when empty. For `openai-compatible`, the prefix of operation paths (default `/v1`). For `anthropic`, a path prefix before every upstream path (for an Anthropic-compatible endpoint served under one, such as `/api/anthropic`); clients still call `/v1/messages`. An absolute path of 1 to 4 plain segments, with no query, fragment, dot segment or trailing slash. Refused for the other providers. |
| `api_version` | string | Omitted when empty. Pinned Azure `api-version` query value. |
| `region` | string | Omitted when empty. Bedrock signing region. Must match the origin. |
| `organization` | string | Omitted when empty. Sent as `OpenAI-Organization`. |
| `project` | string | Omitted when empty. Sent as `OpenAI-Project`. |
| `anthropic_version` | string | Omitted when empty. Pinned `anthropic-version` header. |
| `anthropic_betas` | array of string | Omitted when empty. Allowlist of caller `anthropic-beta` values. |
| `stream_usage` | string | Omitted when empty. Usage reporting for streamed Chat Completions. |
| `limit_header_passthrough` | boolean | Omitted unless `true`. Forwards the provider's rate-limit headers to the caller. Off by default because the headers describe the shared upstream credential. |
| `usage_semantics` | string | Omitted when empty. `openai` declares OpenAI usage semantics on an OpenAI-compatible server; `deepseek` declares DeepSeek's, which adds its cache-hit and cache-miss fields. Only with one of them may token usage settle a compatible chat operation. Any other value is refused. |

### WebSocket block

Controls how session operations are metered and bounded: OpenAI Realtime, OpenAI Responses over WebSocket, and Gemini Live.

| Field | Type | Meaning |
|---|---|---|
| `metering` | string | `session` (default, one request-count admission per session) or `token` (per-response admission for OpenAI WebSocket modes, and a renewable allowance for Gemini Live). `token` is refused on a surface that has the `openai.v1.live` operation. |
| `max_session_seconds` | integer | 60 to 86400. Omitted when unset, in which case the gateway's default applies. |
| `token_allowance` | integer | Gemini Live allowance window, 1024 to 4194304. Only with `token` metering. Default 32768. |

### Capacity policy

Advisory enforcement of the provider limit signals observed on the surface. It is off unless `cooldown_enabled` is `true` or `headroom_fraction` is greater than zero. The policy is valid only on provider surfaces. A policy that would have no effect is refused.

| Field | Type | Meaning |
|---|---|---|
| `cooldown_enabled` | boolean | Rejects new requests while the last observed response was a throttling or overload response with a retry or reset time |
| `max_cooldown_seconds` | integer | Bounds any cooldown. Default 60, at most 3600. |
| `headroom_fraction` | number | Greater than 0 and at most 1. When remaining requests or tokens fall below this fraction of the limit, requests whose QoS weight is below `min_weight` are shed. |
| `min_weight` | integer | Lowest QoS weight still admitted under low headroom |
| `default_weight` | integer | Weight of requests without access-point QoS. Default 1. |
| `signal_max_age_seconds` | integer | How old an observation may be and still be used. Default 60, at most 3600. |

## GET /api/v1/llm/surfaces

Returns a JSON array of resource envelopes: `tenant_id`, `kind` (`model-surface`), `id`, `revision` and `spec`. The `spec` is the connection.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/llm/surfaces" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/llm/surfaces/{id}`

Returns one envelope with an `ETag` header holding the revision in quotes.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Envelope returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No surface with this identifier |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/llm/surfaces/surface-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## PUT `/api/v1/llm/surfaces/{id}`

Creates or replaces a surface. Without `If-Match` it creates, which succeeds only when no live surface has this identifier. With `If-Match` it replaces the surface at that revision. The response is the envelope with the new revision and an `ETag` header.

The save validates the surface and the connection together. The check resolves every reference in the tenant, including `network_profile_ref`, and it checks the destination of the credential against the surface origin. A provider surface is checked against its provider's rules: the auth scheme, the header, the identity type, and the provider's required fields.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Replaced |
| 400 | `INVALID_REVISION` | `If-Match` is not a revision number |
| 400 | `INVALID_SPEC` | Body is not valid JSON for this resource, has unknown fields, is over 1 MiB, the `surface.id` does not match, the surface or connection fails validation, `auth` is used on a Google surface, `capacity_policy` is set on a Google surface, a provider credential does not match the provider's rules, or a network profile does not resolve. The `message` says which rule failed. |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision, or creation was sent while a live surface exists |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS -X PUT "$AIPLEX_URL/api/v1/llm/surfaces/surface-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"surface":{"id":"surface-example","protocol":"google-genai","origin":"https://aiplatform.googleapis.com","api_version":"v1","project":"project-example","location":"us-central1","publisher":"google","models":["model-example"],"operations":[{"id":"generate","method":"POST","path":"/{version}/projects/{project}/locations/{location}/publishers/{publisher}/models/{model}:generateContent","transport":"http","action":"invoke","source_url":"https://example.com/docs"}]},"identity_ref":"identity-example"}'
```

```json
{
  "tenant_id": "tenant-example",
  "kind": "model-surface",
  "id": "surface-example",
  "revision": 1,
  "spec": {
    "surface": {
      "id": "surface-example",
      "protocol": "google-genai",
      "origin": "https://aiplatform.googleapis.com",
      "api_version": "v1",
      "project": "project-example",
      "location": "us-central1",
      "publisher": "google",
      "models": ["model-example"],
      "operations": [
        {"id": "generate", "method": "POST", "path": "/{version}/projects/{project}/locations/{location}/publishers/{publisher}/models/{model}:generateContent", "transport": "http", "action": "invoke", "source_url": "https://example.com/docs"}
      ]
    },
    "identity_ref": "identity-example"
  }
}
```

The `spec` above is abridged to the fields shown in this page.

## DELETE `/api/v1/llm/surfaces/{id}`

Deletes a surface. `If-Match` is required and must carry the current revision. On success the response has no body.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 204 | none | Deleted |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No live surface with this identifier |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `REVISION_REQUIRED` | `If-Match` missing, zero, or not a revision number |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS -X DELETE "$AIPLEX_URL/api/v1/llm/surfaces/surface-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H 'If-Match: "1"'
```

## GET `/api/v1/llm/surfaces/{id}/capacity`

Returns the last provider limit observation this server replica holds for the surface, and the surface's capacity policy and cooldown state. The data is advisory and local to the replica. It is not a shared view.

### Response

| Field | Type | Meaning |
|---|---|---|
| `surface_id` | string | Surface identifier |
| `observed` | boolean | `true` when this replica has an observation for the surface |
| `scope` | string | `replica-local last observation; advisory` |
| `limits` | object | Omitted unless `observed` is `true`. The observation. |
| `capacity_policy` | object | `enabled` (boolean), and when enabled, `policy` (the effective policy with defaults) and `cooldown` (`phase`, `until` and `retry_after_ms`) |

The `limits` object has `provider`, `observed_at`, and optionally `http_status`, `classification`, `retry_after_ms`, and the windows `requests`, `tokens`, `input_tokens` and `output_tokens`. A window has `limit`, `remaining` and `reset_at`, and each is omitted when the provider did not report it. A value that is absent means unknown, not zero.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Report returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks the `admin` role (refused by route policy, before the handler) |
| 403 | `ADMIN_REQUIRED` | Caller lacks the `admin` role |
| 404 | `NOT_FOUND` | No surface with this identifier in the tenant |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/llm/surfaces/surface-example/capacity" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET /api/v1/llm/operations

Returns the operation catalogue.

- Without a query parameter, the response is the array of operations of the Google and Anthropic surfaces. Each entry has `id`, `method`, `path`, `transport`, `action`, `source_url`, and optionally `resource_kind` and `stateful`.
- With `provider=<id>`, the response is that provider's descriptor: `id`, `protocols`, `credential_schemes`, `api_key_header` (omitted when empty), `operations` (the same shape as above), and `default_stream_usage` (omitted when empty).

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Catalogue or descriptor returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 404 | `NOT_FOUND` | `provider` is not a known provider |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/llm/operations?provider=openai" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## `/api/v1/llm/gateway/{id}/*`

The client gateway lets a client call a surface with the wire protocol of its own SDK. The gateway rewrites the client path into the surface's native operation path and then calls the native gateway for the same surface. It applies no grants of its own. The native gateway checks the grants for each operation before any upstream call.

Client paths are mapped per protocol:

| Surface protocol | Client path accepted |
|---|---|
| `google-genai` | `POST /v1beta/models/{model}:{method}`, where `method` is `generateContent`, `streamGenerateContent`, `countTokens`, `embedContent` or `batchEmbedContents` |
| `openai-chat` | `POST /chat/completions` or `POST /v1/chat/completions`. The surface needs an `endpoint`. |
| `openai-responses` | `/responses` or `/v1/responses`, with a subpath. The surface needs an `endpoint`. |
| `vertex-anthropic` | A path that begins with the surface's own project and location |
| `anthropic-messages` | `POST /v1/messages` on an Anthropic publisher surface. The body's `model` is moved to the path. |
| Provider surfaces | Mapped by the provider adapter. Unsupported operations are refused. |

The Gemini Live WebSocket path is accepted as a `GET` on the surface's fixed live path.

Requests whose path is not a documented client path are refused before any upstream call. Paths containing `%`, `\`, control characters or `..` are refused.

A non-GET method passes the router check, which requires the `admin` or `deployer` role outside development mode. Operations are then authorized by these scopes, from the caller's token:

| Scope | Required for |
|---|---|
| `llm:surface:{surface}:{operation}` | Every operation |
| `llm:agent:{agent}` | Operations whose request names an agent |
| `llm:model:{model}` | Operations whose request names a model. The model ID is the surface's listed model. |
| `llm:surface:{surface}:shared-admin` | Operations the gateway classifies as shared administration |

A granted scope that ends in `:*` matches every required scope that begins with the part before the `*`.

### Errors

| Status | Body | When |
|---|---|---|
| 400 | `UNSUPPORTED_CLIENT_OPERATION` (AIPlex JSON error) | The client path or method has no adapter on this surface, or the path is invalid |
| 401 | `UNAUTHENTICATED` (AIPlex JSON error) | No verified identity outside development mode |
| 403 | `FORBIDDEN` (router) | Non-GET method by a caller without `admin` or `deployer` |
| 403 | `operation denied` (plain text) | The native gateway denied the operation for the caller's scopes |
| 404 | `NOT_FOUND` (AIPlex JSON error) | The surface does not exist in the tenant |
| 500 | `RESOURCE_ERROR` (AIPlex JSON error) | Resource store unavailable |

Responses from the upstream provider pass through unchanged. Other errors come from the native gateway, which this page does not document.

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/llm/gateway/surface-example/v1beta/models/model-example:generateContent" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}'
```

## Behaviour notes

- **Revisions.** Replace and delete require `If-Match`. Creating over a live surface returns `409 REVISION_CONFLICT`. The `ETag` after a write is the new revision.
- **Strict specs.** Unknown fields are rejected on write, and the stored spec is decoded strictly on every use.
- **Credentials.** The API stores references only. A provider surface's `auth` names a tenant secret, and an `identity_ref` names an operator profile. Neither a secret value nor a token appears in any response of this API.
- **Destinations.** Each credential is checked against the surface origin on every call. A destination that the profile does not allow fails with `503 CREDENTIAL_DESTINATION_DENIED`.
- **Native failures.** The native gateway returns `503` with an AIPlex code when a dependency is unavailable: `NETWORK_PROFILE_UNAVAILABLE`, `CREDENTIAL_DESTINATION_DENIED`, `NETWORK_UNAVAILABLE`, `LIVE_UNAVAILABLE`, `CREDENTIAL_UNAVAILABLE` or `SURFACE_UNAVAILABLE`.
- **Capacity is advisory.** `capacity_policy` acts on the last observation the replica holds. Another replica may hold a different observation. A missing value means unknown, not zero.
- **Usage.** Every native and client gateway request is recorded in the usage ledger on the LLM plane, with `model_id`, the surface ID as `resource_id`, and the token counters the provider reported. See the usage API.
- **Batches.** The batch operations are reached through the client gateway. The batch list is documented on the model batches page.

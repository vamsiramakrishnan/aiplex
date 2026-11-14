---
title: MCP Connections API
---

An MCP connection is a saved, managed connection from AIPlex to one upstream MCP server. The connection defines the endpoint, how the upstream credential is chosen, which tools may be called, and how an OAuth grant is registered and held. Clients call a connection through `invoke`, and the server checks the caller's `mcp:tools:{name}` grant for each call. Connections are tenant-scoped. Writes and client registration need the `admin` or `deployer` role. The invoke and authorization routes are called by the verified caller directly, and each one has its own grant checks, described below.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/mcp/auth-types` | Credential type catalogue, tool policy fields and secret reference rules | Verified identity |
| GET | `/api/v1/mcp/connections` | List the tenant's MCP connections | `admin` or `deployer` outside development mode; the tenant comes from the verified identity |
| GET | `/api/v1/mcp/connections/{id}` | Read one connection | `admin` or `deployer` outside development mode; the tenant comes from the verified identity |
| PUT | `/api/v1/mcp/connections/{id}` | Create or replace a connection | `admin` or `deployer` |
| DELETE | `/api/v1/mcp/connections/{id}` | Delete a connection. `If-Match` required. | `admin` or `deployer` |
| POST | `/api/v1/mcp/connections/{id}/registration` | Register the OAuth client with the upstream authorization server | `admin` or `deployer` |
| POST | `/api/v1/mcp/connections/{id}/invoke` | Send one MCP JSON-RPC request to the upstream. Also answers `tools/list`. | Per tool: `mcp:tools:{name}` grant, re-checked before dispatch |
| POST | `/api/v1/mcp/connections/{id}/rules/test` | Dry-run the connection's policy rules against a sample call. Nothing is stored. | `admin` or `deployer` |
| POST | `/api/v1/mcp/connections/{id}/authorization` | Start a delegated OAuth authorization for the caller | `mcp:authorize:{id}` grant, or `mcp:tools:{name}` for one allowed tool |
| POST | `/api/v1/mcp/connections/{id}/authorization/complete` | Complete the authorization with the provider's response | Same as `authorization` |
| GET | `/api/v1/mcp/connections/{id}/authorization` | Read the caller's authorization status | Same as `authorization` |
| DELETE | `/api/v1/mcp/connections/{id}/authorization` | Revoke the caller's authorization | Same as `authorization` |

The routes are under `/api/v1`. Every route requires a verified identity. Outside development mode, the writes and the registration require the `admin` or `deployer` role. The invoke and authorization routes are exempt from that router check, because the handler applies its own grant checks. Errors use this shape:

```json
{
  "code": "NOT_FOUND",
  "message": "resource not found",
  "request_id": "req-example"
}
```

## GET /api/v1/mcp/auth-types

Returns the catalogue used to build a connection's `auth` block.

| Field | Type | Meaning |
|---|---|---|
| `types` | array of type | The credential types, one per entry |
| `default_type` | string | `oauth_user` |
| `default_behaviour` | string | Explains that a connection without an `auth` block keeps its execution `credential_mode`: `delegated-oauth` uses `oauth_user` and `operator-bearer` uses the operator identity profile |
| `secret_references` | object | `pattern`, `namespace` (omitted when no tenant is bound) and `note`. Secret fields take a reference name, never a value. |
| `tools_policy` | array of field | The fields of a connection's `tools` block: `allow`, `deny`, `read_only` and `rules` (described in [Policy rules on tool calls](../library/tool-policy-rules.md)) |

A type has `type`, `title`, `description`, `execution_credential_mode` and `fields`. A field has `name`, `type`, `required`, `secret_reference` (`true` when the field holds a reference name), `enum` (omitted when empty) and `description`.

The catalogue has eight types: `none`, `api_key`, `bearer`, `basic`, `oauth_user`, `oauth_client_credentials`, `token_exchange` and `google_id_token`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Catalogue returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/mcp/auth-types" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## Connection resource

The resource `id` is the connection `id`. The body is the connection below. Unknown fields are rejected. The body is at most 1 MiB.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | Yes | Must equal the resource `id`. Used as the namespace of aggregate tool names. |
| `execution` | object | Yes for managed connections | Execution settings. See Execution. |
| `resource_url` | string | Yes for managed connections | The upstream MCP endpoint. Must equal `execution.endpoint`. |
| `issuer` | string | Delegated OAuth | The exact issuer of the upstream authorization server. Required when `credential_mode` is `delegated-oauth`. |
| `client` | object | No | OAuth client profile. See Client profile. |
| `client_registration_ref` | string | No | Identifier of a ClientPlex registration in the tenant. The reference must resolve. |
| `network_profile_ref` | string | No | Saved `network-profile` resource. Checked on every call. |
| `auth` | object | No | Upstream credential. See Auth. Without it, `credential_mode` decides the credential. |
| `tools` | object | No | Connection-level tool policy. See Tool policy. |
| `provenance` | object | No | Where an imported connection came from. See Provenance. Informational only. |

### Execution

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Must equal the connection `id` |
| `endpoint` | string | Must equal `resource_url` |
| `identity_ref` | string | Operator identity profile. Required for `operator-bearer`, and must be empty for `delegated-oauth` and `connection-auth`. |
| `credential_mode` | string | `operator-bearer` (default when empty), `delegated-oauth`, or `connection-auth` |
| `allowed_tools` | array of string | The tools the upstream exposes that this connection may call |
| `protocol_version` | string | MCP protocol version the connection speaks |
| `mode` | string | Transport mode of the connection |

### Client profile

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Profile identifier |
| `name` | string | Display name |
| `redirect_uris` | array of string | Exact redirect URIs the OAuth client registers |
| `application_type` | string | OAuth application type |
| `metadata_url` | string | Omitted when empty. Client metadata document URL. |
| `scopes` | array of string | Omitted when empty. Scopes requested by the authorization. |
| `refresh_tokens` | boolean | Omitted unless `true` |

### Auth

| Field | Type | Meaning |
|---|---|---|
| `type` | string | One of the eight catalogue types |
| `in` | string | `header` or `query` (`api_key` only). Query placement must be stated. |
| `name` | string | Header or query parameter name (`api_key`) |
| `prefix` | string | Optional prefix for the header value (`api_key`) |
| `secret_ref` | string | Tenant secret reference for `api_key`, `bearer` and `basic` |
| `username` | string | Username (`basic`) |
| `token_endpoint` | string | Token endpoint (`oauth_client_credentials`, `token_exchange`) |
| `client_id` | string | OAuth client identifier (`oauth_client_credentials`) |
| `client_secret_ref` | string | Secret reference for the client secret |
| `client_auth_method` | string | `client_secret_basic` or `client_secret_post` |
| `scopes` | array of string | Scopes requested |
| `resource` | string | RFC 8707 resource indicator |
| `audience` | string | Token audience (`token_exchange`, `google_id_token`) |
| `subject_token_type` | string | RFC 8693 subject token type (`token_exchange`) |
| `requested_token_type` | string | RFC 8693 requested token type (`token_exchange`) |
| `identity_ref` | string | Operator identity profile of type `gcp-adc` (`google_id_token`) |

Secret fields hold reference names in the tenant's secret namespace, matching the pattern in `secret_references`. The API never returns a secret value. Credential types are validated when the connection is saved.

### Tool policy

| Field | Type | Meaning |
|---|---|---|
| `allow` | array of string | Exact tool names, or prefixes ending in `*`. When present, only matching tools are callable. |
| `deny` | array of string | Exact tool names, or prefixes ending in `*`. Deny wins over allow. An empty list is refused. |
| `read_only` | boolean | Omitted unless `true`. Only tools whose gateway-observed `readOnlyHint` annotation is `true` are callable. Unknown annotations are denied. |
| `rules` | array of rule | Omitted when empty; an empty list is refused. Up to 64 [policy rules](../library/tool-policy-rules.md), evaluated in order after the lists, the caller's scope and `read_only`. |

A rule has:

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Required. `[a-z][a-z0-9_-]{0,62}`, unique in the list. |
| `when` | string | Required. A CEL boolean expression, at most 1024 bytes and 256 syntax nodes. It is compiled and type-checked when the connection is saved; an expression that does not parse, uses an unknown variable or field, or is not boolean is rejected with `400` naming the rule. |
| `effect` | string | Required. `deny` or `allow`. |
| `message` | string | Optional, at most 200 printable bytes. Shown to the caller when the rule refuses a call. |
| `description` | string | Optional, at most 500 printable bytes. |

The first matching `deny` refuses a call. When the list has an `allow` rule, a call that matches none is refused. A rule that cannot be evaluated refuses the call. A refusal is a JSON-RPC error `-32001` with `data.reason` `tool_policy_denied` or `tool_policy_error` and `data.rule`, HTTP `403` on `invoke`. The variables, helpers, limits and the full order are in [Policy rules on tool calls](../library/tool-policy-rules.md).

### Provenance

Set by `aiplex mcp registry import`. The fields are `registry` (an `https` URL, or `http` to a loopback host), `name` (a registry server name), `version`, `retrieved_at` (RFC 3339) and `sha256` (64 lower-case hex digits of the server object as the registry served it). All are required when the block is present. Nothing in authorization or dispatch reads it. See [Import from the MCP Registry](../library/mcp-registry-import.md).

## GET `/api/v1/mcp/connections`

Returns a JSON array of resource envelopes: `tenant_id`, `kind` (`tool-connection`), `id`, `revision` and `spec`. The `spec` is the connection, and it carries no secret values.

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
curl -sS "$AIPLEX_URL/api/v1/mcp/connections" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/mcp/connections/{id}`

Returns one envelope with an `ETag` header holding the revision in quotes.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Envelope returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No connection with this identifier |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/mcp/connections/connection-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## PUT `/api/v1/mcp/connections/{id}`

Creates or replaces a connection. Without `If-Match` it creates, which succeeds only when no live connection has this identifier. With `If-Match` it replaces the connection at that revision. The response is the envelope with the new revision and an `ETag` header.

Validation runs on save. It checks the id and endpoint, the credential type and its fields, the client profile and redirect URIs, the issuer, the tool policy, and that each referenced record resolves in the tenant.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Stored |
| 400 | `INVALID_REVISION` | `If-Match` is not a revision number |
| 400 | `INVALID_SPEC` | Body is not valid for this resource, has unknown fields, is over 1 MiB, the `id` does not match, the execution identity or endpoint does not match the connection, delegated OAuth lacks an issuer, a credential type does not fit the credential mode, `tools` is invalid, or a referenced record does not resolve. The `message` says which rule failed. |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision, or creation was sent while a live connection exists |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS -X PUT "$AIPLEX_URL/api/v1/mcp/connections/connection-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"connection-example","resource_url":"https://tools.example.com/mcp","execution":{"id":"connection-example","endpoint":"https://tools.example.com/mcp","identity_ref":"identity-example","credential_mode":"operator-bearer","allowed_tools":["search"]},"tools":{"allow":["search"],"read_only":true}}'
```

## DELETE `/api/v1/mcp/connections/{id}`

Deletes a connection. `If-Match` is required and must carry the current revision. On success the response has no body.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 204 | none | Deleted |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No live connection with this identifier |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `REVISION_REQUIRED` | `If-Match` missing, zero, or not a revision number |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS -X DELETE "$AIPLEX_URL/api/v1/mcp/connections/connection-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H 'If-Match: "1"'
```

## POST `/api/v1/mcp/connections/{id}/rules/test`

Evaluates the connection's [policy rules](../library/tool-policy-rules.md) against a sample call and returns the per-rule trace. Operators only. The sample is not stored, logged or written to the ledger. Only rules are evaluated: the name lists, the caller's scope, `read_only`, quota and request hooks are not part of the test. The body is at most 256 KiB and unknown fields are rejected.

| Field | Type | Meaning |
|---|---|---|
| `tool` | string | Required. The tool name of the sample call. |
| `args` | object | The sample `arguments`. Over 64 KiB the answer is `decision: "error"`, `reason: "arguments_too_large"`. |
| `subject` | object | The caller to simulate: `kind`, `principal`, `groups`, `groups_known`, `agent`, `client`. `groups_known` defaults to `true` when `groups` is set and `false` otherwise. |
| `access_point` | string | Also apply this access point's rules for the connection, and set `access_point.id`. |
| `annotations` | object | Simulated `read_only`, `destructive`, `idempotent`, `open_world`. Without it the gateway's stored observations are used (absent when unknown or stale). |
| `rules` | array of rule | Rules to evaluate instead of the saved ones, for this request only. Compiled with the same checks as on save. |
| `time` | string | RFC 3339 value of `request.time`. Default: now. |

The answer:

| Field | Type | Meaning |
|---|---|---|
| `connection`, `tool` | string | Echo of the request |
| `decision` | string | `allow`, `deny` or `error` |
| `error_class` | string | `tool_policy_denied` or `tool_policy_error` when refused |
| `reason` | string | `deny_rule_matched`, `no_allow_rule_matched`, `evaluation_error` or `arguments_too_large` |
| `rule` | string | The rule that decided; omitted for the default deny |
| `message` | string | What a caller would be told |
| `trace` | array | Per rule: `set` (`connection` or `access_point`), `id`, `effect`, `result` (`matched`, `not_matched`, `error` or `not_evaluated`) and, for `error`, the evaluator's `error` text |
| `list_hidden` | boolean | `true` when `tools/list` would hide this tool for this subject (decided without arguments) |
| `note` | string | Reminds what the test does not cover |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Evaluated |
| 400 | `INVALID_REQUEST` | The body is not valid, `tool` is not a tool name, `args` is not an object, or `time` is not RFC 3339 |
| 400 | `INVALID_RULES` | The `rules` in the request do not compile; the message names the rule |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No such connection, or no such access point |
| 409 | `INVALID_STORED_CONNECTION` or `INVALID_STORED_RULES` | A stored record no longer validates |
| 413 | `REQUEST_TOO_LARGE` | The body is over 256 KiB |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/mcp/connections/connection-example/rules/test" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" -H "Content-Type: application/json" \
  -d '{"tool":"read_file","args":{"path":"/etc/hosts"},"subject":{"kind":"user","groups":["eng"]}}'
```

## POST `/api/v1/mcp/connections/{id}/registration`

Registers the connection's OAuth client with the upstream authorization server. The request has no body. The connection must be valid, and its `client_registration_ref` if set must resolve. The result is stored as the connection's `oauth-registration` resource, and that envelope is returned.

The registration step reads the connection's current revision and fails with `409 CONNECTION_CHANGED` if the connection changed while the registration ran.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Registration stored. Body is the `oauth-registration` envelope. |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No connection with this identifier |
| 409 | `CONNECTION_UNAVAILABLE` | The saved connection's references do not resolve |
| 409 | `CONNECTION_CHANGED` | The connection changed during registration |
| 409 | `REVISION_CONFLICT` | The stored registration changed during the write |
| 502 | `REGISTRATION_FAILED` | The upstream registration failed. The message is the registration error. |
| 503 | `REGISTRATION_UNAVAILABLE` | Outbound endpoint policy or secure credential storage is not configured |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/mcp/connections/connection-example/registration" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

The same call from the CLI (the Go SDK method is `RegisterToolConnection`). The command prints the public registration only: mode, client ID, issuer and the credential reference, never a secret. `-o json` prints the envelope.

```bash
aiplex tool-auth register connection-example
```

## POST `/api/v1/mcp/connections/{id}/invoke`

Sends one MCP JSON-RPC request to the upstream. The request body is a JSON-RPC 2.0 request, with a `method` such as `tools/call`. A `tools/list` request is answered with the tools the caller may call. Each `tools/call` is checked against the `mcp:tools:{name}` grant of the verified caller, and that grant is checked again just before the upstream request is sent.

The request must carry `Content-Type: application/json`. It may carry `MCP-Protocol-Version`, and it must match the connection's protocol version when present. It must not carry `MCP-Session-Id` or `Last-Event-ID`, because the transport is stateless. The default body limit is 1 MiB.

For `delegated-oauth` connections, the upstream call uses the caller's own active authorization. It is refused with `409 TOOL_AUTHORIZATION_CHANGED` if that authorization has changed. The operator-bearer and connection-auth modes use the credential the connection defines, and the caller never sees it.

Quota applies when quota enforcement is configured. The call is then admitted, settled and recorded in the usage ledger on the MCP plane, with the tool name.

### Request example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/mcp/connections/connection-example/invoke" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":"rpc","method":"tools/call","params":{"name":"search","arguments":{"query":"hello"}}}'
```

The same call from the CLI (the Go SDK method is `CallConnectionTool`). Text content prints as text and `-o json` prints the raw result. A policy denial exits 3 and names the rule, a tool server error exits 1, and an argument that is not a JSON object exits 2 before anything is sent.

```bash
aiplex mcp connections call connection-example search --args '{"query":"hello"}'
```

### Response

On success, the upstream's JSON-RPC response is returned unchanged:

```json
{"jsonrpc":"2.0","id":"rpc","result":{"content":[{"type":"text","text":"native tool result"}]}}
```

### Status codes and errors

Denials and upstream results inside the JSON-RPC protocol are returned as JSON-RPC errors with the request `id`. The error code `-32001` marks a denial. The `data.reason` of a denial is `denied` unless the gateway supplies another reason. Transport-level refusals are plain text.

| Status | Code or text | When |
|---|---|---|
| 200 | JSON-RPC response | Upstream result, or a JSON-RPC denial. A denial has `error.code` `-32001`. |
| 400 | plain text | Session or replay header sent, protocol version mismatch, body invalid or over the bound, or the request cannot be parsed |
| 401 | `UNAUTHENTICATED` (router), or plain text `verified request context required` | No verified identity, or the request context cannot be verified. |
| 401 | `TOOL_IDENTITY_REQUIRED` | A delegated-oauth connection has no verified owner with an issuer |
| 403 | plain text `request context connection mismatch` | The connection in the verified context does not match the path |
| 403 | plain text `current tool grant required` | The caller's `mcp:tools:{name}` grant was revoked before dispatch |
| 404 | `NOT_FOUND` | No connection with this identifier |
| 405 | plain text | Method other than `POST` |
| 409 | plain text `tool authorization grant changed` | The caller's delegated authorization changed before dispatch |
| 415 | plain text | `Content-Type` is not `application/json` |
| 502 | plain text | Upstream unavailable, or the response did not establish terminal usage |
| 503 | `TOOL_CONFIGURATION_UNAVAILABLE` | A saved reference or prerequisite is unavailable |
| 503 | `TOOL_EXECUTION_UNAVAILABLE` | The managed tool gateway could not be created |
| 503 | plain text `quota service unavailable` | Quota is required and the service is unavailable |
| 503 | plain text | The operator tool credential is unavailable |

A call refused just before dispatch returns the refusal message as plain text, with the status shown above. The refusal's internal code is not written to the response body.

## POST `/api/v1/mcp/connections/{id}/authorization`

Starts the delegated OAuth authorization for the verified caller on a `delegated-oauth` connection. The body is `{"redirect_uri": "..."}`, and the value must be an exact registered redirect URI of the connection's client profile. The body is at most 16 KiB, and unknown fields are rejected.

The caller must hold `mcp:authorize:{id}`, or the `mcp:tools:{name}` grant of at least one tool that the connection allows.

The response is the challenge the caller uses to send the user to the provider:

| Field | Type | Meaning |
|---|---|---|
| `flow_id` | string | Identifier of the authorization flow. Send it back on `complete`. |
| `authorization_url` | string | URL the user opens to grant access |
| `expires_at` | RFC 3339 timestamp | When the flow expires |

The response has `Cache-Control: no-store` and `Referrer-Policy: no-referrer`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Challenge returned |
| 400 | `INVALID_REQUEST` | Body is invalid, has unknown fields, or the redirect URI is missing |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 401 | `TOOL_IDENTITY_REQUIRED` | The caller has no verified owner with an issuer |
| 403 | `TOOL_AUTHORIZATION_DENIED` | The caller lacks the authorization grant |
| 404 | `NOT_FOUND` | No connection with this identifier |
| 409 | `TOOL_AUTHORIZATION_UNAVAILABLE` | The connection is not a `delegated-oauth` connection, or the saved connection is invalid |
| 409 | `TOOL_CONFIGURATION_CHANGED` | The connection changed during authorization |
| 409 | runtime code | The authorization runtime refused the start. The code is a lowercase runtime code, such as `redirect_not_registered`. |
| 502 | `TOOL_REGISTRATION_FAILED` | The client registration could not be prepared |
| 503 | `TOOL_AUTHORIZATION_UNAVAILABLE` | Durable authorization or secret storage is not configured |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/mcp/connections/connection-example/authorization" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"redirect_uri":"https://app.example.com/callback"}'
```

```json
{
  "flow_id": "flow-example",
  "authorization_url": "https://auth.example.com/authorize?state=example",
  "expires_at": "2026-11-10T09:10:00Z"
}
```

## POST `/api/v1/mcp/connections/{id}/authorization/complete`

Completes the flow with the provider's response. The body has `flow_id`, `state`, `code`, and optionally `iss`, which is the issuer the provider reported. The body is at most 16 KiB, and unknown fields are rejected.

On success it returns the session information described under `GET .../authorization`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Authorization completed |
| 400 | `INVALID_REQUEST` | Body is invalid or has unknown fields |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 401 | `TOOL_IDENTITY_REQUIRED` | The caller has no verified owner with an issuer |
| 403 | `TOOL_AUTHORIZATION_DENIED` | The caller lacks the authorization grant |
| 404 | `NOT_FOUND` | No connection with this identifier |
| 409 | runtime code | The flow was not completed. The codes include `authorization_consumed_or_expired`, `invalid_callback`, `callback_binding_invalid` and `authorization_already_claimed`. The message says to inspect the status or start a new grant. |
| 409 | `TOOL_AUTHORIZATION_UNAVAILABLE` | The connection does not use delegated OAuth |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/mcp/connections/connection-example/authorization/complete" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"flow_id":"flow-example","state":"state-example","code":"code-example"}'
```

## GET `/api/v1/mcp/connections/{id}/authorization`

Returns the caller's current authorization for the connection.

| Field | Type | Meaning |
|---|---|---|
| `binding` | object | The session key: `owner` (`tenant_id`, `principal_id`, and `actor_id` when present), `connection_id`, `connection_revision`, `resource`, `issuer` and `profile_id` |
| `grant_id` | string | Identifier of the grant |
| `status` | string | Status of the grant |
| `scopes` | array of string | Scopes granted |
| `created_at` | RFC 3339 timestamp | When the grant was created |
| `expires_at` | RFC 3339 timestamp | When the grant expires |
| `access_expires_at` | RFC 3339 timestamp | When the current access token expires |

The response has `Cache-Control: no-store`. No token value appears in it.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Status returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 401 | `TOOL_IDENTITY_REQUIRED` | The caller has no verified owner with an issuer |
| 403 | `TOOL_AUTHORIZATION_DENIED` | The caller lacks the authorization grant |
| 404 | `TOOL_AUTHORIZATION_REQUIRED` | The caller has no active authorization for this connection |
| 404 | `NOT_FOUND` | No connection with this identifier |
| 409 | `TOOL_AUTHORIZATION_UNAVAILABLE` | The connection does not use delegated OAuth |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/mcp/connections/connection-example/authorization" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## DELETE `/api/v1/mcp/connections/{id}/authorization`

Revokes the caller's authorization. On success the response has no body. Revocation errors use the runtime codes described under `complete`, with status `409`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 204 | none | Revoked |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 401 | `TOOL_IDENTITY_REQUIRED` | The caller has no verified owner with an issuer |
| 403 | `TOOL_AUTHORIZATION_DENIED` | The caller lacks the authorization grant |
| 404 | `NOT_FOUND` | No connection with this identifier |
| 409 | runtime code | The revocation could not be completed, for example `revocation_conflict` |
| 409 | `TOOL_AUTHORIZATION_UNAVAILABLE` | The connection does not use delegated OAuth |

### Example

```bash
curl -sS -X DELETE "$AIPLEX_URL/api/v1/mcp/connections/connection-example/authorization" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## Behaviour notes

- **Credential values are never returned.** A connection stores references to tenant secrets. Values are resolved at dispatch and are not part of any response in this API.
- **Owner-scoped authorization.** A delegated authorization belongs to one verified owner: the tenant, the principal and, when present, the actor. A caller cannot read or revoke another owner's authorization.
- **Revision binding.** A delegated authorization is bound to the connection revision it was started under. A later revision is a different binding, so the caller authorizes again for it.
- **Grants are re-checked.** The `mcp:tools:{name}` grant is checked when the request is admitted and again just before dispatch. A grant that is revoked in between refuses the call.
- **Tool policy.** The connection's `tools` block and the access point's `expose` list both narrow the callable tools. `tools/list` returns only the callable tools.
- **Read-only.** `read_only` uses the upstream's `readOnlyHint` annotation as observed by the gateway. An unknown annotation is treated as not read-only.
- **Native routes.** The invoke route and the authorization routes are answered by the handler itself, so the router does not apply the `admin` or `deployer` check to them. Each one applies its own grant checks, as described above.
- **Usage.** Calls to `invoke` are recorded in the usage ledger on the MCP plane, with `tool_name` and the connection as `resource_id`.

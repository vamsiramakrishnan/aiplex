---
title: Clients API
---

Route access is enforced by a default-deny route policy: a route with no explicit rule is open to operators only. Check the route's response for the authoritative answer.

The clients API registers developer clients, such as coding CLIs and desktop apps, and produces reviewed configuration plans for them. A registration names a built-in client profile, a protocol, and either a direct Google Cloud connection or a gateway destination. A plan renders configuration fragments for the client. It never writes workstation files, fetches credentials, or runs helper commands, and it does not promise upstream model access. Gateway destinations are looked up by reference in the caller's tenant. A request can never supply a raw endpoint URL. Every route is tenant-scoped: the tenant comes from the verified binding, never from a query parameter.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/clients/profiles` | List the built-in client profiles | Verified identity |
| GET | `/api/v1/clients/registrations` | List the tenant's client registrations | `admin` or `deployer` outside development mode; the tenant comes from the verified identity |
| GET | `/api/v1/clients/registrations/{id}` | Read one registration | `admin` or `deployer` outside development mode; the tenant comes from the verified identity |
| PUT | `/api/v1/clients/registrations/{id}` | Create or replace a registration | `admin` or `deployer` outside development mode |
| DELETE | `/api/v1/clients/registrations/{id}` | Delete a registration | `admin` or `deployer` outside development mode |
| POST | `/api/v1/clients/registrations/{id}/plan` | Render the configuration plan for a saved registration | `admin` or `deployer` outside development mode |
| GET | `/api/v1/clients/destinations` | List the tenant's client destinations | `admin` or `deployer` outside development mode; the tenant comes from the verified identity |
| GET | `/api/v1/clients/destinations/{id}` | Read one client destination | `admin` or `deployer` outside development mode; the tenant comes from the verified identity |
| PUT | `/api/v1/clients/destinations/{id}` | Create or replace a client destination | `admin` or `deployer` outside development mode |
| DELETE | `/api/v1/clients/destinations/{id}` | Delete a client destination | `admin` or `deployer` outside development mode |

The routes are mounted under `/api/v1` and are served by the `clientplex` module. The module is enabled by default. All routes require a verified identity. The API is JSON in both directions, and errors use this shape:

```json
{
  "code": "NOT_FOUND",
  "message": "client registration not found",
  "request_id": "req-example"
}
```

`details`, `request_id` and `trace_id` are optional and omitted when empty.

Outside development mode, the `/api/v1` group requires the `admin` or `deployer` role for every non-GET method. A caller without either role receives `403 FORBIDDEN`. Development mode skips that check.

## GET `/api/v1/clients/profiles`

Returns the built-in client profiles as a JSON array, sorted by `id`. This route does not resolve a tenant, so it succeeds for any verified identity.

### Profile fields

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Profile identifier. Used as `profile_id` in a registration. |
| `name` | string | Display name |
| `surface` | string | Client surface, for example `cli` or `desktop` |
| `modes` | array of string | Modes the profile supports: `direct`, `gateway` or `claude-apps-gateway` |
| `protocols` | array of string | Native protocols the profile supports |
| `sources` | array of string | Documentation references for the profile |

The built-in profiles are `antigravity`, `antigravity-acp`, `codex`, `claude-code`, `claude-desktop` and `gemini-cli`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/clients/profiles" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
[
  {
    "id": "codex",
    "name": "Codex",
    "surface": "cli",
    "modes": ["direct", "gateway"],
    "protocols": ["openai-responses"],
    "sources": [
      "https://learn.chatgpt.com/docs/config-file/config-advanced",
      "https://learn.chatgpt.com/docs/enterprise/connect-to-a-gateway"
    ]
  }
]
```

## GET `/api/v1/clients/registrations`

Returns every client registration of the tenant as a JSON array of resource envelopes.

### Resource envelope

| Field | Type | Meaning |
|---|---|---|
| `tenant_id` | string | Owning tenant |
| `kind` | string | Always `client-registration` |
| `id` | string | Registration identifier |
| `revision` | integer | Revision. Used as the `If-Match` value. |
| `spec` | object | The registration, described below |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 403 | `TENANT_REQUIRED` | No trusted tenant binding |
| 500 | `CLIENT_STORE_ERROR` | Registration store unavailable |
| 503 | `TENANT_UNAVAILABLE` | The tenant resolver is not configured |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/clients/registrations" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/clients/registrations/{id}`

Returns one resource envelope. The response carries an `ETag` header whose value is the revision in quotes.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Envelope returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 403 | `TENANT_REQUIRED` | No trusted tenant binding |
| 404 | `NOT_FOUND` | No registration with this identifier in the tenant |
| 500 | `CLIENT_STORE_ERROR` | Registration store unavailable |
| 503 | `TENANT_UNAVAILABLE` | The tenant resolver is not configured |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/clients/registrations/dev" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## PUT `/api/v1/clients/registrations/{id}`

Creates or replaces a registration. Send the registration as the JSON body. Unknown fields are rejected, and the body must be one JSON object.

- To create a registration, send no `If-Match`. The write succeeds only when no registration with that identifier exists.
- To replace a registration, send `If-Match` with its current revision. Quotes around the value are accepted.

The `id` field in the body is optional. When it is present it must equal the path `{id}`.

Before storing, the handler checks the body in this order:

1. The body decodes into the registration fields below, with no unknown fields.
2. The body's `id` matches the path, when present.
3. A `network_profile_ref`, when set, names a network profile in the tenant.
4. The registration produces a plan. This validates the profile, mode, protocol, auth mode, the fields that are refused for that profile and mode (see [Fields that have no effect, and fields that are refused](#fields-that-have-no-effect-and-fields-that-are-refused) below) and, for gateway modes, the destination reference, which must resolve in the tenant.
5. The `If-Match` header parses as a positive revision, when present.

### Registration fields

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Registration identifier. Matches `^[a-z][a-z0-9-]{0,62}$`. Must equal the path `{id}`. |
| `profile_id` | string | Built-in profile identifier, for example `codex` |
| `mode` | string | Must be one of the profile's `modes` |
| `protocol` | string | Must be one of the profile's `protocols`: `google-genai`, `openai-chat`, `openai-responses`, `anthropic-messages`, `vertex-anthropic` or `claude-apps` |
| `principal_binding` | string | Required. Up to 1024 characters. |
| `network_profile_ref` | string | Optional network profile identifier, resolved in the tenant |
| `destination_ref` | string | Gateway destination identifier. Required outside `direct` mode and rejected in `direct` mode. |
| `project_id` | string | Google Cloud project for `direct` mode, and for the `vertex-anthropic` gateway protocol |
| `location` | string | Google Cloud location for `direct` mode |
| `model_id` | string | Optional model identifier, up to 256 characters |
| `allowed_models` | array of string | Optional. Up to 100 unique identifiers, each up to 256 characters. Checked against the destination's declared model ids when the registration is saved or planned. **Not enforced by the client configuration:** the destination's own model list decides what is served. Refused in `direct` mode. |
| `auth_mode` | string | `adc` for direct mode, or `workforce` for the Antigravity profiles in direct mode. `gateway-key`, `token-helper` or `oidc` for gateway modes. |
| `credential_env` | string | Environment variable name where the launcher holds the gateway credential. Matches `^[A-Z][A-Z0-9_]{0,127}$`. For Codex it is the key variable. For Claude Code and Gemini CLI, which read one fixed variable (`ANTHROPIC_AUTH_TOKEN`, `GEMINI_API_KEY`), a different name makes the plan bind `credential_ref` to this variable and add a `workstation shell` artifact that copies it into the client's own. |
| `credential_ref` | string | Opaque credential reference. The value is never returned. |
| `credential_file` | string | Absolute path, supported only for the Codex direct profile |
| `token_helper` | object | Token helper settings for `token-helper` auth. Fields: `command` (absolute path), `args` (up to 32 strings), `timeout_ms` (0 to 30000), `refresh_interval_ms`. Supported for Codex and Claude Code (Messages protocol; no timing fields, no `credential_ref`). Refused for Gemini CLI, which has no credential helper setting. |
| `gateway_server` | object | Gateway server settings. Valid only in `claude-apps-gateway` mode. Its fields are validated by the gateway server spec and are not listed here. |
| `managed` | boolean | Optional. Moves the settings fragment to the client's managed (MDM) target for Claude Code and Antigravity. Accepted without effect, and without a warning, for Claude Desktop and Antigravity ACP, whose plan is already the managed configuration. Refused (`400`, and `409` on a plan) for Codex, Gemini CLI and Antigravity in direct mode, which have no managed configuration that the plan renders. |

`platform` is not a registration field. It was validated and never read, and it was removed. A body that carries it is refused with `400 INVALID_REGISTRATION` and the message "platform is no longer a registration field and must be removed from the document"; every other unknown member keeps the decoder's generic message.

### Fields that have no effect, and fields that are refused

One rule applies to every profile, Antigravity included:

- A field the plan for the chosen profile, mode and auth mode uses is accepted.
- A field that promises control over the client, where the plan would not deliver it and nothing else enforces it, is **refused**: `400 INVALID_REGISTRATION` on a write, and `409 CLIENT_PLAN_UNAVAILABLE` for a stored registration. The message names every refused field and what to remove. These are `allowed_models` in direct mode (the client calls Google Cloud itself, so no gateway restricts its models) and `managed` for Codex, Gemini CLI and Antigravity in direct mode.
- Any other field with no effect in that profile and mode is **accepted**, and the plan carries one warning for it that names the field, for example `Registration field project_id has no effect for profile codex in gateway mode...`. Examples: `project_id` and `location` on a gateway registration that is not Vertex, `credential_env` and `credential_ref` in direct mode, and `model_id`, `credential_env`, `credential_ref`, `project_id` and `location` in the native `claude-apps-gateway` mode.
- `allowed_models` in a gateway mode is accepted. The plan warns that it is not enforced by the client configuration: it is only checked against the destination's declared model ids, and the destination's own model list decides what is served. An Aiplex access point answers `404 MODEL_NOT_FOUND` for a model outside its list, and a model surface refuses one outside its allowlist. Antigravity renders the list into the client's model list, so it is used there and carries no such warning.
- The `gemini-cli` profile and the Claude Code `token-helper` are new, so no stored registration can carry their unused fields. Those stay strict: `project_id`, `location` and `managed` for Gemini CLI, and `credential_env` and `credential_ref` for the Claude Code helper are refused.

#### Upgrade note

Registrations stored before this change are not rewritten, and every one of them still reads (`GET`, list). What changes is the plan:

- It gains a warning for each field that has no effect. Nothing needs to be done.
- A stored registration that carries `allowed_models` in direct mode, or `managed` on a profile with no managed configuration, no longer produces a plan: `POST .../plan` returns `409 CLIENT_PLAN_UNAVAILABLE` and a `PUT` that keeps the field returns `400`, each naming the field. Remove the field (or move the registration to a gateway mode and enforce the list at the destination) and `PUT` the document with the current `If-Match` revision.
- A document read back from the API that still holds `platform` must have that member deleted before it is written again.

### Response

On create the response is `201 Created`. On replace it is `200 OK`. The body is the resource envelope with the new revision, and the response carries an `ETag` header.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Registration replaced. Body is the envelope. |
| 201 | none | Registration created. Body is the envelope. |
| 400 | `INVALID_REGISTRATION` | Body does not decode, has unknown fields (for `platform`, a message that it is no longer a field and must be removed), `id` differs from the path, or the profile, mode, protocol, auth or destination rules fail (including a refused field, named in the message), or the destination does not resolve |
| 400 | `INVALID_REFERENCE` | `network_profile_ref` is not a network profile in the tenant |
| 400 | `INVALID_REVISION` | `If-Match` is not a positive revision |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | Caller lacks `admin` or `deployer` outside development mode |
| 403 | `TENANT_REQUIRED` | No trusted tenant binding |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision, or no `If-Match` was sent for an existing registration |
| 500 | `CLIENT_STORE_ERROR` | Registration store unavailable |
| 503 | `TENANT_UNAVAILABLE` | The tenant resolver is not configured |

### Example

```bash
curl -sS -X PUT "$AIPLEX_URL/api/v1/clients/registrations/dev" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"dev","profile_id":"codex","mode":"gateway","protocol":"openai-responses","destination_ref":"models","principal_binding":"principal:user","model_id":"model-example","auth_mode":"gateway-key","credential_env":"AIPLEX_TOKEN","credential_ref":"credentials/user"}'
```

```json
{
  "tenant_id": "tenant-example",
  "kind": "client-registration",
  "id": "dev",
  "revision": 1,
  "spec": {
    "id": "dev",
    "profile_id": "codex",
    "mode": "gateway",
    "protocol": "openai-responses",
    "destination_ref": "models",
    "principal_binding": "principal:user",
    "model_id": "model-example",
    "auth_mode": "gateway-key",
    "credential_env": "AIPLEX_TOKEN",
    "credential_ref": "credentials/user"
  }
}
```

## DELETE `/api/v1/clients/registrations/{id}`

Deletes a registration. `If-Match` is required and must carry the current revision. A missing, malformed or zero `If-Match` fails with `428`. On success the response has no body.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 204 | none | Registration deleted |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | Caller lacks `admin` or `deployer` outside development mode |
| 403 | `TENANT_REQUIRED` | No trusted tenant binding |
| 404 | `NOT_FOUND` | No registration with this identifier in the tenant |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `REVISION_REQUIRED` | `If-Match` is missing, malformed or zero |
| 500 | `CLIENT_STORE_ERROR` | Registration store unavailable |
| 503 | `TENANT_UNAVAILABLE` | The tenant resolver is not configured |

### Example

```bash
curl -sS -X DELETE "$AIPLEX_URL/api/v1/clients/registrations/dev" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H 'If-Match: "1"'
```

## POST `/api/v1/clients/registrations/{id}/plan`

Renders the configuration plan for a saved registration. The request has no body. The handler reads the stored registration, checks that its references still resolve in the tenant, and builds the plan. It does not change the registration or any other resource.

### Plan fields

| Field | Type | Meaning |
|---|---|---|
| `registration_id` | string | Identifier of the registration |
| `profile_id` | string | Profile used for the plan |
| `version` | string | Plan format version. Always `v1`. |
| `support_status` | string | Support status of the plan, `configured-unverified` for a rendered plan |
| `network_profile_ref` | string | Omitted when empty |
| `commands` | array of array of string | Omitted when empty. Commands the operator reviews. |
| `version_requirements` | array of string | Omitted when empty |
| `credential_bindings` | array of object | Omitted when empty. Each has `reference` and `environment`. The credential value is never included. |
| `artifacts` | array of object | Configuration fragments. Each has `target`, `format`, `merge_strategy` and `content`. A `workstation shell` artifact holds the line that copies the launcher's credential variable into the one the client reads. |
| `services` | array of object | Endpoints the client calls. Each has `host`, `purpose` and `required`. |
| `preserve_headers` | array of string | Omitted when empty |
| `warnings` | array of string | Advisory messages, including one per registration field that has no effect in this profile and mode |
| `sources` | array of string | Documentation references for the profile |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Plan returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | Caller lacks `admin` or `deployer` outside development mode |
| 403 | `TENANT_REQUIRED` | No trusted tenant binding |
| 404 | `NOT_FOUND` | No registration with this identifier in the tenant |
| 409 | `CLIENT_PLAN_UNAVAILABLE` | A saved reference no longer resolves, or the registration cannot produce a plan, including a stored registration that carries a now-refused field (named in the message) |
| 500 | `CLIENT_STORE_ERROR` | Stored registration cannot be read or decoded |
| 503 | `TENANT_UNAVAILABLE` | The tenant resolver is not configured |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/clients/registrations/dev/plan" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
{
  "registration_id": "dev",
  "profile_id": "codex",
  "version": "v1",
  "support_status": "configured-unverified",
  "artifacts": [],
  "services": [],
  "warnings": [],
  "sources": []
}
```

## Client destinations

`/api/v1/clients/destinations` stores the gateway destinations that registrations refer to by `destination_ref`. It uses the same resource routes as the other saved resources in this API. Reads return resource envelopes, and writes follow the same revision rules as the registrations. A destination's `endpoint` must pass the configured endpoint policy. A registration never supplies an endpoint.

### Destination fields

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Required. Must equal the path `{id}`. |
| `provider` | string | Provider name |
| `endpoint` | string | Gateway URL. Validated by the endpoint policy. |
| `protocols` | array of string | Required. At least one protocol the destination serves. |
| `project_id` | string | Optional Google Cloud project |
| `location` | string | Optional Google Cloud location |
| `availability` | string | Required. `configured`, `unverified` or `unavailable`. |
| `model_ids` | array of string | Optional models the destination lists |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Destination read, or replaced (body is the envelope or the list) |
| 204 | none | Destination deleted |
| 400 | `INVALID_SPEC` | Body does not decode, `id` differs from the path, no protocols, invalid availability, or endpoint rejected by policy |
| 400 | `INVALID_REVISION` | `If-Match` is not a revision number |
| 403 | `TENANT_REQUIRED` | No trusted tenant binding |
| 404 | `NOT_FOUND` | No destination with this identifier |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `REVISION_REQUIRED` | Delete without a positive `If-Match` |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

## Behaviour notes

- **Tenant from the binding.** The tenant comes from the verified identity. Registrations and destinations of another tenant are invisible. A reference to another tenant's resource fails validation.
- **References resolve at plan time.** A destination or network profile that is deleted after a registration is saved makes the plan return `409 CLIENT_PLAN_UNAVAILABLE`.
- **Raw endpoints are refused.** A registration body that carries an `endpoint` field is rejected with `400 INVALID_REGISTRATION`, because unknown fields are rejected.
- **Plans never change state.** Rendering a plan does not write a file, fetch a credential, or change the registration.
- **Credentials are references.** A registration carries `credential_ref` and `credential_env` only. Credential values are never part of a registration or plan.

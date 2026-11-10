---
title: IAM API
---

Access rules are being tightened in the same release; check the route's response for the authoritative answer.

The IAM API maps identity-provider groups to AIPlex roles and scopes, reports the caller's resolved access, and serves the native governance routes. A role binding connects a workforce group, optionally restricted to one issuer and pool, to a role and a set of scopes. Native governance is the controlled path for changing an external runtime. It reads a saved binding, plans a change, and applies that plan only with a matching approval.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/iam/roles` | List the AIPlex roles and their default scopes | Verified identity |
| GET | `/api/v1/iam/whoami` | Resolve the caller's identity, roles and scopes | Verified identity |
| POST | `/api/v1/iam/validate-principal` | Check the form of a workforce principal string | `admin` outside development mode |
| GET | `/api/v1/iam/role-bindings` | List role bindings, optionally by group | Verified identity |
| POST | `/api/v1/iam/role-bindings` | Create a role binding | `admin` outside development mode |
| GET | `/api/v1/iam/role-bindings/{id}` | Read one role binding | Verified identity |
| PUT | `/api/v1/iam/role-bindings/{id}` | Update a role binding | `admin` outside development mode |
| DELETE | `/api/v1/iam/role-bindings/{id}` | Delete a role binding | `admin` outside development mode |
| GET | `/api/v1/iam/native/providers` | List native governance providers of the tenant | Grant `aiplex:governance:providers:read` |
| GET | `/api/v1/iam/native/bindings` | List agent bindings of the tenant | Grant `aiplex:governance:read:bindings` |
| GET | `/api/v1/iam/native/bindings/{id}` | Read one agent binding | Grant `aiplex:governance:read:{id}` |
| PUT | `/api/v1/iam/native/bindings/{id}` | Create or replace an agent binding | `admin` outside development mode |
| DELETE | `/api/v1/iam/native/bindings/{id}` | Delete an agent binding | `admin` outside development mode |
| POST | `/api/v1/iam/native/bindings/{id}/read` | Run one read-only operation through a saved binding | Verified identity and the read grant checked by the governance controller |
| POST | `/api/v1/iam/native/bindings/{id}/plan` | Plan a change through a saved binding | `admin` outside development mode |
| GET | `/api/v1/iam/native/plans/{id}` | Read one governance plan | Grant `aiplex:governance:read:{binding_id}` |
| POST | `/api/v1/iam/native/plans/{id}/apply` | Apply a plan with an approval | `admin` outside development mode, the plan's own principal and actor, and grant `aiplex:governance:apply:{binding_id}` |
| GET | `/api/v1/iam/native/receipts/{id}` | Read one governance receipt | Grant `aiplex:governance:read:{binding_id}` |

The routes are mounted under `/api/v1/iam`. They require a verified identity. Outside development mode, the `/api/v1/iam/` prefix restricts every non-GET method to the `admin` role. A caller without that role receives `403 FORBIDDEN`. Development mode skips that check.

The `/read` route is the one exception. Its path is exempt from the role check, so any verified identity can call it. The governance controller then decides whether the caller may run the operation.

The JSON body of a request is one object. Unknown fields are rejected on the role-binding create and validate routes and on the native routes. Bodies are limited to 1 MiB.

Errors use this shape:

```json
{
  "code": "NOT_FOUND",
  "message": "role binding not found",
  "request_id": "req-example"
}
```

`details`, `request_id` and `trace_id` are optional and omitted when empty.

## Revisions for role bindings

Role bindings use the configuration revision, not the resource revision used by other APIs. Every read returns the revision as a quoted `ETag`. A write to an existing binding must send that value in `If-Match`, exactly.

- A write to an existing binding without `If-Match` returns `428 PRECONDITION_REQUIRED`.
- `If-Match` that is not one exact non-negative integer returns `400 INVALID_PRECONDITION`.
- A stale value returns `412 REVISION_CONFLICT`.
- A create may omit `If-Match`. If it sends one, the value must be `"0"`.

Writes to the configuration store also need a revisioned configuration backend. Without one, every write returns `503 CONFIGURATION_CAS_UNAVAILABLE`.

## GET `/api/v1/iam/roles`

Returns the four AIPlex roles and their default scopes as a JSON array.

### Role fields

| Field | Type | Meaning |
|---|---|---|
| `role` | string | `admin`, `deployer`, `viewer` or `agent` |
| `default_scopes` | array of string | Scopes granted by the role when a binding does not state its own |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/iam/roles" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
[
  {
    "role": "admin",
    "default_scopes": ["mcp:tools:*", "mcp:server:*", "a2a:task:*", "a2a:agent:*", "llm:model:*", "llm:capability:*"]
  }
]
```

## GET `/api/v1/iam/whoami`

Resolves the caller's verified identity, syncs the caller's scopes, and returns the result. The response is the access record for the current request.

### Response fields

| Field | Type | Meaning |
|---|---|---|
| `configuration_revision` | integer | Omitted when zero. Configuration revision used for the resolution. |
| `identity` | object | The resolved identity. Its fields are `actor_id`, `actor_subject`, `authorized_party`, `principal_id`, `subject`, `email`, `display_name`, `groups`, `domain`, `provider`, `pool_id` and `is_workforce`. Optional values are omitted when empty. |
| `roles` | array of string | Roles held by the caller |
| `scopes` | array of string | Effective scopes for this request |
| `group_scopes` | array of string | Omitted when empty. Scopes from matching role bindings. |
| `manual_scopes` | array of string | Omitted when empty. Scopes assigned to the principal directly. |
| `matched_bindings` | array of string | Omitted when empty. Identifiers of the matching role bindings. |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Access returned |
| 401 | `AUTH_ERROR` | The request carries no valid identity token |
| 500 | `RESOLVE_ERROR` | Access could not be resolved |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/iam/whoami" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## POST `/api/v1/iam/validate-principal`

Checks the form of a workforce principal string. It does not create or change anything.

### Request fields

| Field | Type | Required | Meaning |
|---|---|---|---|
| `principal` | string | Yes | Principal string to check |

### Response

The check always returns `200` when the body decodes. A principal that fails the check is reported in the body.

| Field | Type | Meaning |
|---|---|---|
| `valid` | boolean | `true` when the principal passes the check |
| `principal` | string | Present when `valid` is `true`. The principal as sent. |
| `error` | string | Present when `valid` is `false`. The reason. |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Check completed. See `valid`. |
| 400 | `BAD_REQUEST` | Body does not decode or has unknown fields |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` outside development mode |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/iam/validate-principal" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"principal":"principal-example"}'
```

## GET `/api/v1/iam/role-bindings`

Returns the role bindings as a JSON array. Without a filter, every binding is returned.

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `group` | string | No | Return only bindings for this group |

### Role binding fields

| Field | Type | Meaning |
|---|---|---|
| `revision` | integer | Configuration revision |
| `provider` | string | Omitted when empty. Issuer restriction. |
| `pool_id` | string | Omitted when empty. Pool restriction. |
| `id` | string | Binding identifier |
| `group` | string | Identity-provider group name |
| `role` | string | `admin`, `deployer`, `viewer` or `agent` |
| `scopes` | array of string | Scopes granted by the binding |
| `description` | string | Omitted when empty |
| `created_at` | RFC 3339 timestamp | Creation time |
| `created_by` | string | Principal that created the binding |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 500 | `STORE_ERROR` | Store read failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/iam/role-bindings?group=group-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## POST `/api/v1/iam/role-bindings`

Creates a role binding. The body has the role binding fields above, except `revision`, `created_at` and `created_by`, which the server sets.

Rules:

- `group` is required. It must not be blank, must be at most 512 characters, and must not contain NUL or line breaks.
- `role` must be `admin`, `deployer`, `viewer` or `agent`.
- `provider` is at most 2048 characters. `pool_id` is at most 512 characters. Neither may contain NUL or line breaks.
- `scopes` is normalized. When omitted, the role's default scopes are stored.
- `id` is optional. When empty, the server derives `rb-` followed by 32 hexadecimal characters from the provider, pool, group and role. A supplied `id` must be at most 256 characters and must not contain `/`, a backslash, NUL or line breaks.
- The creator is recorded as `created_by`. It is the caller's principal, or `anonymous` when there is no identity.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 201 | none | Binding created. Body is the binding. |
| 400 | `BAD_REQUEST` | Body does not decode, has unknown fields, or fails a rule above |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` outside development mode |
| 409 | `CONFLICT` | A binding with this `id` already exists |
| 412 | `REVISION_CONFLICT` | `If-Match` is present and is not `"0"` |
| 400 | `INVALID_PRECONDITION` | `If-Match` is malformed |
| 500 | `STORE_ERROR` | Binding lookup failed |
| 503 | `CONFIGURATION_CAS_UNAVAILABLE` | The configuration store does not support revisions |
| 503 | `CONFIGURATION_UNAVAILABLE` | The configuration generation or write could not be acknowledged |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/iam/role-bindings" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"group":"group-example","role":"viewer","description":"description-example"}'
```

```json
{
  "revision": 1,
  "id": "rb-0123456789abcdef0123456789abcdef",
  "group": "group-example",
  "role": "viewer",
  "scopes": [],
  "description": "description-example",
  "created_at": "2026-11-10T09:00:00Z",
  "created_by": "principal-example"
}
```

## GET `/api/v1/iam/role-bindings/{id}`

Returns one role binding. The response carries an `ETag` header with the revision.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Binding returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 404 | `NOT_FOUND` | No binding with this identifier |
| 500 | `STORE_ERROR` | Store read failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/iam/role-bindings/rb-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## PUT `/api/v1/iam/role-bindings/{id}`

Updates a role binding. Send `If-Match` with the current revision. The body may contain any of the role binding fields. A field that is not sent keeps its stored value. The stored `id`, `created_at` and `created_by` always stay as they are.

If `scopes` is not sent and `role` changes, the scopes are replaced with the defaults for the new role. The result is validated with the same rules as create.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Binding updated. Body is the binding. |
| 400 | `BAD_REQUEST` | Body does not decode, carries an unknown field, or fails validation |
| 400 | `INVALID_PRECONDITION` | `If-Match` is malformed or repeated |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` outside development mode |
| 404 | `NOT_FOUND` | No binding with this identifier |
| 412 | `REVISION_CONFLICT` | `If-Match` does not match the current revision, or a configuration dependency changed |
| 428 | `PRECONDITION_REQUIRED` | `If-Match` is missing |
| 500 | `STORE_ERROR` | Binding lookup failed |
| 503 | `CONFIGURATION_CAS_UNAVAILABLE` | The configuration store does not support revisions |
| 503 | `CONFIGURATION_UNAVAILABLE` | The write could not be acknowledged |

### Example

```bash
curl -sS -X PUT "$AIPLEX_URL/api/v1/iam/role-bindings/rb-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -H 'If-Match: "1"' \
  -d '{"description":"description-updated"}'
```

## DELETE `/api/v1/iam/role-bindings/{id}`

Deletes a role binding. `If-Match` is required and must carry the current revision. On success the response is `204` with no body.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 204 | none | Binding deleted |
| 400 | `INVALID_PRECONDITION` | `If-Match` is malformed |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` outside development mode |
| 404 | `NOT_FOUND` | No binding with this identifier |
| 412 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `PRECONDITION_REQUIRED` | `If-Match` is missing |
| 503 | `CONFIGURATION_UNAVAILABLE` | The lookup or delete could not be completed |

### Example

```bash
curl -sS -X DELETE "$AIPLEX_URL/api/v1/iam/role-bindings/rb-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H 'If-Match: "1"'
```

## Native governance

Native governance acts on external runtimes, such as an agent platform, through a saved agent binding. The flow has four steps:

1. Save an agent binding. Its `id` must equal the path `{id}`, and its tenant must be the caller's tenant. The governance controller validates it.
2. Read through the binding with `POST .../read`. Only non-mutating operations can be read.
3. Plan a change with `POST .../plan`. The plan is saved and has an identifier, a revision and a digest.
4. Apply the plan with `POST .../apply`. The approval must be the plan's digest, and the caller must be the plan's principal and actor and must hold the apply grant for the binding.

Native routes use the tenant of the verified binding. Without a tenant binding, the binding routes and the receipt route return `403 TENANT_REQUIRED`. The plan read and apply routes return `403 ACTION_DENIED`. The `read` and `plan` routes return `401 IDENTITY_REQUIRED`.

### GET `/api/v1/iam/native/providers`

Returns the native governance providers of the tenant as a JSON array. The list is empty when no provider is configured for the tenant.

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Provider identifier |
| `type` | string | Provider type |
| `operations` | array of object | Each has `name`, `mutating`, `credential` and `description` |
| `apply_enabled` | boolean | Whether apply is enabled for this provider |
| `status` | string | Provider status |

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 403 | `ACTION_DENIED` | Grant `aiplex:governance:providers:read` is missing |
| 403 | `TENANT_REQUIRED` | No trusted tenant binding |

### Native bindings

`/api/v1/iam/native/bindings` stores agent bindings as tenant resources. Listing and reading require a read grant. Writes follow the resource rules of the other APIs: the body must be one JSON object, an existing binding needs its current revision in `If-Match`, and a delete needs `If-Match`.

| Method | Path | Grant | Success |
|---|---|---|---|
| GET | `/api/v1/iam/native/bindings` | `aiplex:governance:read:bindings` | 200 with an array of resource envelopes |
| GET | `/api/v1/iam/native/bindings/{id}` | `aiplex:governance:read:{id}` | 200 with a resource envelope and an `ETag` |
| PUT | `/api/v1/iam/native/bindings/{id}` | Caller is `admin` outside development mode | 200 with a resource envelope |
| DELETE | `/api/v1/iam/native/bindings/{id}` | Caller is `admin` outside development mode | 204 |

The binding body is the agent binding object of the governance package. Its top-level fields are `id`, `tenant_id`, `driver_ref`, `agent`, `delegated_user`, `sponsor`, `registry`, `allowed` and `desired`. The governance controller validates the nested fields. They are not listed on this page.

Status codes for a binding write that fails validation:

| Status | Code | When |
|---|---|---|
| 400 | `INVALID_SPEC` | Body contains restricted fields, does not decode, the `id` or tenant does not match, the controller rejects the binding, or no governance provider is configured |

### POST `/api/v1/iam/native/bindings/{id}/read`

Runs one read-only operation through the saved binding. The request body has these fields:

| Field | Type | Meaning |
|---|---|---|
| `binding_revision` | integer | Revision of the saved binding to use |
| `operation` | object | The operation. Its fields are `name`, `resource`, `payload` (optional JSON) and `etag` (optional). |

Mutating operations are refused. The governance controller checks the read grant `aiplex:governance:read:{id}`.

The response is an observation with these fields: `version`, `id`, `revision`, `tenant_id`, `principal_id`, `actor_id`, `binding_id`, `binding_revision`, `binding_identity_digest`, `driver_ref`, `provider_revision`, `operation`, `resource`, `status_code`, `etag`, `result_digest`, `body` (omitted when empty) and `observed_at`.

| Status | Code | When |
|---|---|---|
| 200 | none | Observation returned |
| 400 | `INVALID_SPEC` | Body does not decode or contains restricted fields |
| 400 | `NATIVE_GOVERNANCE_REFUSED` | The governance controller refused the operation |
| 401 | `IDENTITY_REQUIRED` | No verified identity |
| 403 | `ACTION_DENIED` | The read grant is missing, or the operation is not allowed |
| 404 | `NOT_FOUND` | The binding or the resource is not found |
| 503 | `NATIVE_GOVERNANCE_UNAVAILABLE` | No governance providers are configured |

### POST `/api/v1/iam/native/bindings/{id}/plan`

Plans a change through the saved binding. The body has these fields:

| Field | Type | Required | Meaning |
|---|---|---|---|
| `binding_revision` | integer | Yes | Revision of the saved binding to use |
| `desired_id` | string | No | Desired identifier for the resource |
| `ttl_seconds` | integer | No | Plan lifetime. From 0 to 900. |

The response is the plan. Its fields are `version`, `id`, `revision`, `tenant_id`, `principal_id`, `actor_id`, `binding_id`, `binding_revision`, `binding_digest`, `desired_id`, `driver_ref`, `provider_revision`, `operation`, `resource`, `payload_hash`, `etag` (omitted when empty), `digest`, `created_at` and `expires_at`.

| Status | Code | When |
|---|---|---|
| 200 | none | Plan returned |
| 400 | `INVALID_SPEC` | Body does not decode, or `ttl_seconds` is outside 0 to 900 |
| 400 | `NATIVE_GOVERNANCE_REFUSED` | The controller refused the plan |
| 401 | `IDENTITY_REQUIRED` | No verified identity |
| 403 | `ACTION_DENIED` | A required grant or approval is missing |
| 404 | `NOT_FOUND` | Binding not found |
| 409 | `GOVERNANCE_CONFLICT` | A stale or already-attempted binding or plan, or a revision conflict |
| 503 | `NATIVE_GOVERNANCE_UNAVAILABLE` | No governance providers are configured |

### GET `/api/v1/iam/native/plans/{id}`

Returns the saved plan with the fields listed above. The caller needs the grant `aiplex:governance:read:{binding_id}` for the plan's binding. The response carries the plan's stored revision.

| Status | Code | When |
|---|---|---|
| 200 | none | Plan returned |
| 403 | `ACTION_DENIED` | No tenant binding, or the read grant is missing |
| 404 | `NOT_FOUND` | No plan with this identifier in the tenant |

### POST `/api/v1/iam/native/plans/{id}/apply`

Applies a saved plan. The body has these fields:

| Field | Type | Meaning |
|---|---|---|
| `plan_revision` | integer | Revision of the plan to apply |
| `approval_digest` | string | Must equal the plan's digest |

The caller must match the plan's principal and actor, and must hold `aiplex:governance:apply:{binding_id}`. The response is the receipt. Its fields are `version`, `plan_id`, `plan_revision`, `plan_digest`, `tenant_id`, `principal_id`, `actor_id` (omitted when empty), `binding_id`, `state`, `status_code` (omitted when zero), `result_digest` (omitted when empty), `etag` (omitted when empty), `started_at` and `finished_at`.

| Status | Code | When |
|---|---|---|
| 200 | none | Receipt returned |
| 400 | `INVALID_SPEC` | Body does not decode or contains restricted fields |
| 400 | `NATIVE_GOVERNANCE_REFUSED` | The controller refused the apply |
| 401 | `IDENTITY_REQUIRED` | No verified identity |
| 403 | `ACTION_DENIED` | The approval, the caller's identity, or the apply grant does not match |
| 404 | `NOT_FOUND` | No plan with this identifier |
| 409 | `APPLY_DISABLED` | Apply is disabled for this provider |
| 409 | `GOVERNANCE_CONFLICT` | The plan is stale or was already attempted |

### GET `/api/v1/iam/native/receipts/{id}`

Returns the saved receipt. The caller needs the grant `aiplex:governance:read:{binding_id}` for the receipt's binding.

| Status | Code | When |
|---|---|---|
| 200 | none | Receipt returned |
| 403 | `ACTION_DENIED` | The read grant is missing |
| 403 | `TENANT_REQUIRED` | No trusted tenant binding |
| 404 | `NOT_FOUND` | No receipt with this identifier in the tenant |

## Behaviour notes

- **Role bindings are configuration.** Their writes use configuration revisions and need a revisioned configuration store. They do not use the resource revision of other APIs.
- **Native plans are checked at apply.** Apply checks the approval digest, the caller's identity and the apply grant. A plan that was already attempted is refused with `GOVERNANCE_CONFLICT`.
- **The read route is open to verified identities.** Authority for each read comes from the governance controller, not from the role check.
- **Roles and default scopes are reported, not configured here.** The default scopes come from the built-in role table.

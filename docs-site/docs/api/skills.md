---
title: Skills API
---

Access rules are being tightened in the same release; check the route's response for the authoritative answer.

The skills API covers SkillsPlex, the plane that publishes skill servers and records skill invocations. It has three parts: a discovery list of running skill servers, a per-server skills manifest that agents read before they call a skill, and an invocation log. The invocation log is reported telemetry. A recorded invocation does not execute a skill and does not prove that live authorization succeeded.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/skills/servers` | List running skill server instances and the skills each one advertises | Verified identity |
| POST | `/api/v1/skills/invocations` | Record a reported skill invocation | `admin` or `deployer` outside development mode |
| GET | `/api/v1/skills/invocations` | List recorded skill invocations | Verified identity |
| GET | `/skills/{instanceId}/.well-known/skills.json` | Read the merged skills manifest of one skill server | Verified identity |

The `/api/v1` routes are served by the `skillsplex` module, which is enabled by default. The manifest route sits outside `/api/v1`, and it is mounted only when the `skillsplex` module is enabled. All routes require a verified identity. Requests and responses are JSON. Errors use this shape:

```json
{
  "code": "STORE_ERROR",
  "message": "store unavailable",
  "request_id": "req-example"
}
```

`details`, `request_id` and `trace_id` are optional and omitted when empty.

Outside development mode, the `/api/v1` group requires the `admin` or `deployer` role for every non-GET method. A caller without either role receives `403 FORBIDDEN`. Development mode skips that check. The manifest route is outside that group and does not check a role.

Skill calls are not routed through this API. Access points expose skill manifests under `/ap/{id}/skills`, which is covered by the access points reference.

## GET `/api/v1/skills/servers`

Returns one summary for each skill server instance whose status is `running`. Instances that are not running are omitted.

### Summary fields

| Field | Type | Meaning |
|---|---|---|
| `instance_id` | string | Instance identifier |
| `name` | string | Display name of the instance, or of its template when the instance has none |
| `url` | string | Manifest path, `/skills/{instance_id}` |
| `skill_bundle` | string | Omitted when the template has no bundle |
| `skills` | array of string | Callable skills, each prefixed with `skill:invoke:` |
| `status` | string | Instance status. Always `running` for listed entries. |

When the instance carries a scope list, a skill appears in `skills` only when the instance grants `skill:invoke:{name}`. When the instance has no scope list, the skills the template declares are used. A template that cannot be read fails the whole list.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned. An empty array when no instance is running. |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 500 | `STORE_ERROR` | Registry store failed |
| 503 | `TEMPLATE_UNAVAILABLE` | A running instance has a template that cannot be read |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/skills/servers" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
[
  {
    "instance_id": "skill-example",
    "name": "Skill example",
    "url": "/skills/skill-example",
    "skill_bundle": "bundle-example",
    "skills": ["skill:invoke:skill-name-example"],
    "status": "running"
  }
]
```

## GET `/skills/{instanceId}/.well-known/skills.json`

Returns the merged skill catalog of one skill server. When the instance carries a scope list, the callable skills come from its `skill:invoke:` grants, and template metadata enriches each matching skill. A skill that is granted but not declared by the template appears with its name only. When the instance has no scope list, the template's declared skills are returned.

### Manifest fields

| Field | Type | Meaning |
|---|---|---|
| `instance_id` | string | Instance identifier |
| `name` | string | Template name |
| `description` | string | Template description |
| `version` | string | Template version |
| `url` | string | `/skills/{instance_id}` |
| `skill_bundle` | string | Template bundle name |
| `skills` | array of object | Callable skills. Each has `name` and may have `description`, `version` and `triggers`. |
| `scopes` | array of string | Scopes granted to the instance |

The `scopes` field returns the instance's granted scopes as stored. It is included for discovery and is not a grant to the caller.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Manifest returned |
| 400 | `BAD_REQUEST` | The instance is not a SkillsPlex instance |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 404 | `NOT_FOUND` | No instance with this identifier |
| 409 | `INSTANCE_UNAVAILABLE` | The instance is not running |
| 500 | `STORE_ERROR` | Registry store or template read failed |
| 503 | `TEMPLATE_UNAVAILABLE` | The template is missing or belongs to another plane |

### Example

```bash
curl -sS "$AIPLEX_URL/skills/skill-example/.well-known/skills.json" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
{
  "instance_id": "skill-example",
  "name": "Skill example",
  "description": "description-example",
  "version": "1.0.0",
  "url": "/skills/skill-example",
  "skill_bundle": "bundle-example",
  "skills": [
    {"name": "skill-name-example", "description": "skill-description-example", "version": "1.0.0", "triggers": ["trigger-example"]}
  ],
  "scopes": ["skill:invoke:skill-name-example"]
}
```

## POST `/api/v1/skills/invocations`

Records one skill invocation that a collector reports. The handler does not run the skill. A record is an audit entry only. The request body is a single JSON object, read with a 1 MiB limit. Unknown fields are rejected.

### Request fields

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | No | Invocation identifier. Matches `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`. Generated as `inv-` followed by 16 hexadecimal characters when empty. |
| `agent_id` | string | Yes | Calling agent. Non-blank, up to 512 characters, no line breaks or NUL. |
| `instance_id` | string | No | Skill server instance. Up to 512 characters. |
| `skill_name` | string | Yes | Skill that was invoked. Non-blank, up to 512 characters. |
| `user_id` | string | No | User on whose behalf the call was made. Up to 512 characters. |
| `status` | string | No | `success` or `failed`. Defaults to `success`. |
| `started_at` | RFC 3339 timestamp | No | Start of the invocation. Defaults to the time of the request. Cannot be more than five minutes in the future. |
| `duration_ms` | integer | No | Duration in milliseconds. Cannot be negative. |
| `error` | string | No | Error text. Up to 8192 characters. |
| `trace_id` | string | No | 32 lowercase hexadecimal characters, not all zero |
| `span_id` | string | No | 16 lowercase hexadecimal characters, not all zero |
| `parent_span_id` | string | No | 16 lowercase hexadecimal characters, not all zero, and not equal to `span_id` |

When a `traceparent` header is present, it is parsed as a W3C trace context. Its trace and parent span identifiers are used. A `trace_id` or `parent_span_id` in the body must agree with the header. When no trace identifier is present, one is generated. The same applies to `span_id`.

The `reported_by` and `reported_at` fields are set by the server. They are not accepted from the request.

### Behaviour

- When `status` is `failed` and `error` contains `missing scope:`, the handler also records a policy denial with reason `reported_scope_missing`. The scope is the text after that prefix, up to the first space, comma or semicolon. The denial is best effort and does not change the response.
- The response is the stored record, with the server-set fields filled in.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 201 | none | Record stored. Body is the stored record. |
| 400 | `BAD_REQUEST` | Body does not decode, has unknown fields, or fails the label, error-length or duration checks |
| 400 | `INVALID_STATUS` | `status` is not `success` or `failed` |
| 400 | `INVALID_TIME` | `started_at` is more than five minutes in the future |
| 400 | `INVALID_ID` | `id` does not match the identifier pattern |
| 400 | `TRACE_CONTEXT` | The `traceparent` header is invalid, disagrees with the body, or a trace or span identifier is invalid |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` or `deployer` outside development mode |
| 409 | `ALREADY_EXISTS` | A record with this `id` already exists |
| 500 | `STORE_ERROR` | Registry store failed |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/skills/invocations" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"inv-example","agent_id":"agent-example","instance_id":"skill-example","skill_name":"skill-name-example","status":"success","duration_ms":120}'
```

```json
{
  "reported_by": "principal-example",
  "reported_at": "2026-11-10T09:00:00Z",
  "id": "inv-example",
  "agent_id": "agent-example",
  "instance_id": "skill-example",
  "skill_name": "skill-name-example",
  "status": "success",
  "started_at": "2026-11-10T09:00:00Z",
  "duration_ms": 120,
  "trace_id": "0123456789abcdef0123456789abcdef",
  "span_id": "0123456789abcdef"
}
```

## GET `/api/v1/skills/invocations`

Returns recorded skill invocations as a JSON array, filtered by the query parameters below.

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `agent_id` | string | No | Filter by calling agent |
| `skill` | string | No | Filter by skill name |
| `limit` | integer | No | From 1 to 1000. Default 100. |

### Invocation fields

| Field | Type | Meaning |
|---|---|---|
| `reported_by` | string | Omitted when empty. Verified collector that submitted the record. |
| `reported_at` | RFC 3339 timestamp | When the record was stored |
| `id` | string | Invocation identifier |
| `agent_id` | string | Calling agent, as reported |
| `instance_id` | string | Skill server instance, as reported |
| `skill_name` | string | Skill name |
| `user_id` | string | Omitted when empty |
| `status` | string | `success` or `failed` |
| `started_at` | RFC 3339 timestamp | Start of the invocation |
| `duration_ms` | integer | Omitted when zero |
| `error` | string | Omitted when empty |
| `trace_id` | string | Omitted when empty |
| `span_id` | string | Omitted when empty |
| `parent_span_id` | string | Omitted when empty |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 400 | `INVALID_LIMIT` | `limit` is not an integer from 1 to 1000 |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 500 | `STORE_ERROR` | Registry store failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/skills/invocations?agent_id=agent-example&skill=skill-name-example&limit=50" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## Behaviour notes

- **Invocations are reported history.** The `reported_by` field names the verified collector. The agent and user identifiers are reported values and are not proof of a live call or its authorization.
- **Listing is not tenant-scoped.** The skills handlers read the SkillsPlex registry directly and do not resolve a tenant. What they return depends on that store.
- **Skill execution is elsewhere.** This API never calls a skill server.

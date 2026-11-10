---
title: A2A Connections API
---

Access rules are being tightened in the same release; check the route's response for the authoritative answer.

The A2A connections API manages the agent runtimes that a tenant can call: Google agent runtimes, AWS Bedrock agents and AgentCore runtimes, Azure Foundry agents and Copilot Studio agents. A connection is a saved resource. Its invoke route forwards one native agent call through AIPlex, after checking identity, a per-operation grant, governance, network and credential policy. The discovery and delegation routes read the A2APlex agent registry and reported delegation history.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/a2a/connections` | List the tenant's agent connections | Verified identity and tenant |
| GET | `/api/v1/a2a/connections/{id}` | Read one agent connection | Verified identity and tenant |
| PUT | `/api/v1/a2a/connections/{id}` | Create or replace an agent connection | `admin` or `deployer` outside development mode |
| DELETE | `/api/v1/a2a/connections/{id}` | Delete an agent connection | `admin` or `deployer` outside development mode |
| POST | `/api/v1/a2a/connections/{id}/invoke` | Invoke a native agent through a connection | Verified identity, tenant and a per-operation grant `a2a:agent:{id}:{operation}` |
| GET | `/api/v1/a2a/agents` | List Agent Cards of running A2APlex agent instances | Verified identity |
| GET | `/api/v1/a2a/delegations` | List recent reported delegations | Verified identity |
| GET | `/api/v1/a2a/delegations/{id}` | Read one reported delegation | Verified identity |
| GET | `/api/v1/a2a/delegations/{id}/chain` | Read a delegation and its descendants | Verified identity |

The connection routes are mounted under `/api/v1/a2a` by the `a2aplex` module, which is enabled by default. The agent and delegation routes are part of the same module. All routes require a verified identity. Requests and responses are JSON, except the invoke route, which returns the upstream agent response. Errors use this shape:

```json
{
  "code": "NOT_FOUND",
  "message": "resource not found",
  "request_id": "req-example"
}
```

`details`, `request_id` and `trace_id` are optional and omitted when empty.

Outside development mode, the `/api/v1` group requires the `admin` or `deployer` role for every non-GET method, except the invoke route, which is exempt from that role check and is governed by its own grant. Development mode skips the role check.

## Agent connection resource

The connection routes store one resource of kind `agent-connection`. The body is the connection specification below. Unknown fields are rejected, and the body must be one JSON object.

### Connection fields

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | Yes | Identifier. Matches `^[A-Za-z0-9][A-Za-z0-9._-]*$`. Must equal the path `{id}`. |
| `tenant_id` | string | Yes | Must equal the tenant of the request |
| `provider` | string | Yes | `google-agent-runtime`, `aws-bedrock-agent`, `aws-agentcore-runtime`, `azure-foundry-agent` or `copilot-studio` |
| `endpoint` | string | Yes | Pinned HTTPS origin: a host, no user information, no query, no fragment, and no path other than `/` |
| `resource_id` | string | Yes | Provider resource. For `google-agent-runtime`, a canonical `projects/{project}/locations/{location}/reasoningEngines/{id}` name. For `aws-agentcore-runtime`, a runtime ARN whose region matches `region`. |
| `identity_ref` | string | Yes | Identifier of the credential identity used for the call |
| `governance_mode` | string | Yes | Must be `invocation`. Egress control needs a separately configured control point. |
| `network_profile_ref` | string | No | Network profile in the tenant. Resolved on save and on invoke. |
| `governance_binding_ref` | string | No | Native governance binding that identifies this runtime. Checked on save and on invoke. |
| `region` | string | Provider-dependent | Required for `aws-bedrock-agent` and `aws-agentcore-runtime` |
| `agent_id` | string | Provider-dependent | Required for `aws-bedrock-agent`. Optional for `azure-foundry-agent`. |
| `alias_id` | string | Provider-dependent | Required for `aws-bedrock-agent` |
| `project` | string | Provider-dependent | Required for `azure-foundry-agent` |
| `api_version` | string | Provider-dependent | Required for `azure-foundry-agent` |

The provider rules are checked on save. The identity reference and endpoint must also pass the runtime's destination check and the outbound endpoint policy.

### Invocation body

`POST .../invoke` takes this JSON object. The handler reads at most 1 MiB of the request body, and a longer body fails to decode.

| Field | Type | Meaning |
|---|---|---|
| `operation` | string | Operation name. It is part of the grant checked before the call. |
| `session_id` | string | Optional session identifier for a multi-turn session |
| `payload` | any JSON | Optional. Passed to the agent runtime. |
| `content_type` | string | Optional content type of the payload |
| `accept` | string | Optional accepted response type |
| `qualifier` | string | Optional runtime qualifier |
| `watermark` | string | Optional stream watermark |

## GET `/api/v1/a2a/connections`

Returns every agent connection of the tenant as a JSON array of resource envelopes.

### Resource envelope

| Field | Type | Meaning |
|---|---|---|
| `tenant_id` | string | Owning tenant |
| `kind` | string | Always `agent-connection` |
| `id` | string | Connection identifier |
| `revision` | integer | Revision. Used as the `If-Match` value. |
| `spec` | object | The connection specification |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/a2a/connections" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/a2a/connections/{id}`

Returns one resource envelope. The response carries an `ETag` header whose value is the revision in quotes.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Envelope returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No connection with this identifier in the tenant |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/a2a/connections/agent-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## PUT `/api/v1/a2a/connections/{id}`

Creates or replaces a connection. Send no `If-Match` to create. Send the current revision in `If-Match` to replace. Quotes around the value are accepted.

The body is checked against the connection rules above. Validation also resolves the governance binding and network profile in the tenant, and checks the endpoint and identity reference. A failed check is returned as `400 INVALID_SPEC` with the reason in `message`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Stored. Body is the envelope with the new revision and an `ETag` header. |
| 400 | `INVALID_REVISION` | `If-Match` is not a revision number |
| 400 | `INVALID_SPEC` | Body does not decode, has unknown fields, or fails a validation rule |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` or `deployer` outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | Store reports the resource as missing |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision, or no `If-Match` was sent for an existing connection |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS -X PUT "$AIPLEX_URL/api/v1/a2a/connections/agent-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"agent-example","tenant_id":"tenant-example","provider":"azure-foundry-agent","endpoint":"https://foundry.example.invalid","resource_id":"resource-example","identity_ref":"identity-example","governance_mode":"invocation","project":"project-example","api_version":"api-version-example"}'
```

## DELETE `/api/v1/a2a/connections/{id}`

Deletes a connection. `If-Match` is required and must carry the current revision. A missing, malformed or zero `If-Match` fails with `428`. On success the response has no body.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 204 | none | Connection deleted |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` or `deployer` outside development mode |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | Store reports the resource as missing |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `REVISION_REQUIRED` | `If-Match` is missing, malformed or zero |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### Example

```bash
curl -sS -X DELETE "$AIPLEX_URL/api/v1/a2a/connections/agent-example" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H 'If-Match: "1"'
```

## POST `/api/v1/a2a/connections/{id}/invoke`

Forwards one native agent call through the saved connection. The handler checks, in this order:

1. The tenant binding. Without it the response is `403 TENANT_REQUIRED`.
2. The saved connection. A missing connection is `404 NOT_FOUND`.
3. The verified identity. Without it the response is `401 IDENTITY_REQUIRED`.
4. The invocation body. An undecodable body is `400 INVALID_INVOCATION`.
5. The grant `a2a:agent:{id}:{operation}`, where `{operation}` is the `operation` field. Either the exact scope or a wildcard grant that ends in `:*` is accepted. Without it the response is `403 ACTION_DENIED`.
6. The governance binding, the network profile, the credential destination and the outbound policy. Each failure is a `503` with its own code.
7. The identity and the invoker. The identity is resolved, and the invoker is created from the saved connection. Before dispatch, the grant is checked again against the current authority snapshot when a resolver is configured.

On success the upstream response is returned with its own status code. The handler copies `Content-Type`, `Cache-Control`, `X-Request-Id` and `Retry-After` from the upstream response and streams the body. Quota and execution layers can reject a call with their own status and code, and they can set `Retry-After`. The call is recorded in the usage ledger on the A2A plane.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| upstream | none | Upstream status and body, streamed through |
| 400 | `INVALID_INVOCATION` | Body does not decode, or is larger than 1 MiB once decoded |
| 401 | `IDENTITY_REQUIRED` | No verified identity for the call |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 403 | `ACTION_DENIED` | The grant `a2a:agent:{id}:{operation}` is missing, or the current grant is missing at dispatch |
| 404 | `NOT_FOUND` | No connection with this identifier in the tenant |
| 500 | `RESOURCE_ERROR` | Resource store unavailable or the saved connection cannot be read |
| 502 | `INVOCATION_FAILED` | The call failed before a response was received |
| 503 | `GOVERNANCE_BINDING_UNAVAILABLE` | The governance binding is missing, or it does not identify this runtime |
| 503 | `NETWORK_PROFILE_UNAVAILABLE` | The network profile is missing in the tenant |
| 503 | `CREDENTIAL_DESTINATION_DENIED` | The identity is not permitted for the endpoint |
| 503 | `NETWORK_UNAVAILABLE` | The outbound policy is not configured |
| 503 | `IDENTITY_UNAVAILABLE` | The identity reference cannot be resolved |
| 503 | `AGENT_UNAVAILABLE` | The invoker cannot be created from the saved connection |
| other | from the layer | Quota or execution rejections, for example `503 AGENT_EXECUTION_UNAVAILABLE` for a managed execution lease that is unavailable. These codes come from the quota or execution layer and are not fixed by this route. |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/a2a/connections/agent-example/invoke" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"operation":"operation-example","payload":{"input":"text-example"}}'
```

The response is the upstream agent response. It has no AIPlex envelope.

## GET `/api/v1/a2a/agents`

Returns the Agent Cards of A2APlex agent instances that are running. Instances that are not running are omitted. An instance whose template cannot be read makes the whole list fail.

### Agent card list fields

| Field | Type | Meaning |
|---|---|---|
| `instance_id` | string | Instance identifier |
| `name` | string | Display name of the instance, or of its template when the instance has none |
| `url` | string | Agent URL, `/a2a/{instance_id}` |
| `task_types` | array of string | Task capabilities, each prefixed with `a2a:task:` |
| `status` | string | Instance status, `running` for every listed entry |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned. An empty array when no instance is running. |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 500 | `STORE_ERROR` | Registry store failed |
| 503 | `TEMPLATE_UNAVAILABLE` | A running instance has a template that cannot be read |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/a2a/agents" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
[
  {
    "instance_id": "agent-example",
    "name": "Agent example",
    "url": "/a2a/agent-example",
    "task_types": ["a2a:task:task-example"],
    "status": "running"
  }
]
```

## GET `/api/v1/a2a/delegations`

Returns recent reported delegations. The handler passes the filter and the limit to the registry and returns its result as given.

### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `agent_id` | string | No | Filter by agent identifier. Passed to the registry as given. |
| `limit` | integer | No | From 1 to 1000. Default 50. |

### Response

A JSON array of delegation objects, described below.

### Delegation fields

| Field | Type | Meaning |
|---|---|---|
| `reported_by` | string | Omitted when empty. Verified collector that submitted the audit event. |
| `reported_at` | RFC 3339 timestamp | When the event was reported |
| `id` | string | Delegation identifier |
| `caller_agent_id` | string | Calling agent, as reported |
| `callee_agent_id` | string | Called agent, as reported |
| `caller_instance_id` | string | Calling instance, as reported |
| `callee_instance_id` | string | Called instance, as reported |
| `task_type` | string | Task type |
| `status` | string | `pending`, `running`, `completed` or `failed` |
| `user_id` | string | User on whose behalf the call was made, as reported |
| `started_at` | RFC 3339 timestamp | Start of the delegation |
| `completed_at` | RFC 3339 timestamp | Omitted until the delegation completes |
| `duration_ms` | integer | Omitted when zero |
| `error` | string | Omitted when empty |
| `parent_id` | string | Omitted for a root delegation. Identifier of the parent delegation. |
| `trace_id` | string | Omitted when empty. 32 hexadecimal characters. |
| `span_id` | string | Omitted when empty. 16 hexadecimal characters. |
| `parent_span_id` | string | Omitted when empty. Span identifier of the caller. |

The identity fields describe reported history. They are not authenticated proof of a live invocation or of its authorization.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 400 | `INVALID_LIMIT` | `limit` is not an integer from 1 to 1000 |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 500 | `STORE_ERROR` | Registry store failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/a2a/delegations?agent_id=agent-example&limit=20" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/a2a/delegations/{id}`

Returns one delegation object as described above.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Delegation returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 404 | `NOT_FOUND` | No delegation with this identifier |
| 500 | `STORE_ERROR` | Registry store failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/a2a/delegations/delegation-example" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/a2a/delegations/{id}/chain`

Returns the delegation identified by `{id}` as `root`, and its descendants as `children`. Children are listed parent before child. Siblings are ordered by `started_at` and then by `id`.

When the root has a `trace_id` and the registry supports trace lookup, the descendants are read by trace. Otherwise the handler reads up to 10001 delegations of all agents and walks parent links from the root.

### Chain fields

| Field | Type | Meaning |
|---|---|---|
| `root` | object | The delegation identified by `{id}` |
| `children` | array of object | Omitted when there are no descendants. Flattened, parent before child. |
| `depth` | integer | Deepest level reached. The root is level 1. |
| `total_duration_ms` | integer | Sum of recorded `duration_ms` values, including nested work. This is not wall-clock elapsed time. |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Chain returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 404 | `NOT_FOUND` | No delegation with this identifier |
| 422 | `CHAIN_LIMIT` | The bounded scan found more than 10000 delegations |
| 422 | `INVALID_CHAIN` | The graph contains a cycle, or it is deeper than 100 levels |
| 500 | `STORE_ERROR` | Registry store failed, or the delegation graph cannot be read |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/a2a/delegations/delegation-example/chain" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
{
  "root": {
    "reported_at": "2026-11-10T09:00:00Z",
    "id": "delegation-example",
    "caller_agent_id": "caller-example",
    "callee_agent_id": "callee-example",
    "caller_instance_id": "",
    "callee_instance_id": "",
    "task_type": "task-example",
    "status": "completed",
    "user_id": "",
    "started_at": "2026-11-10T09:00:00Z",
    "duration_ms": 120
  },
  "depth": 1,
  "total_duration_ms": 120
}
```

## Behaviour notes

- **Connections are tenant-scoped.** Every connection route resolves the tenant from the verified binding. A connection of another tenant is reported as `404 NOT_FOUND`.
- **Agent cards and delegations do not resolve a tenant.** The agent and delegation handlers read the A2APlex registry directly. What they return depends on that store, not on a tenant check in the handler.
- **Reported history is not authorization.** Delegation records carry the identities that a collector reported. They do not prove a live call or its grant.
- **Credentials are never returned.** A connection stores an `identity_ref`. Credential values are resolved at invoke time and are not part of any response.
- **The invoke route is exempt from the role check.** Its authority is the per-operation grant, not the `admin` or `deployer` role.

---
title: Platform API
---

Route access is enforced by a default-deny route policy: a route with no explicit rule is open to operators only. Check the route's response for the authoritative answer.

The platform API covers the routes that are not specific to one plane. It has health probes, the module list, the catalog of deployable templates, deployed instances, registered agents and their permissions, the dashboard summary, the MCP registry view, the A2A Agent Card, and the authentication webhooks and user scopes that connect AIPlex to Ory Hydra.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/healthz` | Liveness | None |
| GET | `/readyz` | Readiness, including the store and module runtime | None |
| GET | `/api/v1/modules` | List module descriptors | Verified identity |
| GET | `/api/v1/catalog` | List catalog templates | Verified identity |
| GET | `/api/v1/catalog/{id}` | Read one catalog template | Verified identity |
| GET | `/api/v1/instances` | List instances | Verified identity |
| POST | `/api/v1/instances` | Deploy an instance from a template | `admin` or `deployer` outside development mode |
| GET | `/api/v1/instances/{id}` | Read one instance | Verified identity |
| DELETE | `/api/v1/instances/{id}` | Undeploy an instance | `admin` or `deployer` outside development mode |
| GET | `/api/v1/instances/{id}/history` | List deploy history of an instance | Verified identity |
| GET | `/api/v1/agents` | List registered agents | Verified identity |
| POST | `/api/v1/agents` | Register an agent | `admin` or `deployer` outside development mode |
| GET | `/api/v1/agents/{clientId}` | Read one agent | Verified identity |
| DELETE | `/api/v1/agents/{clientId}` | Delete an agent registration | `admin` or `deployer` outside development mode |
| GET | `/api/v1/agents/{clientId}/permissions` | Read an agent's permission ceiling by plane | Verified identity |
| GET | `/api/v1/dashboard/stats` | Read the platform summary | Verified identity |
| GET | `/api/v1/dashboard/denials` | List recent policy denials | `admin` role; deployment-wide, so an identity bound to a tenant is refused |
| POST | `/api/v1/dashboard/denials` | Record a policy denial | `admin` outside development mode |
| POST | `/internal/policy-denials` | Record a denial reported by the authz sidecar | Shared bearer secret (`AIPLEX_DENIAL_TOKEN`) |
| GET | `/v0.1/servers` | List catalog entries as an MCP registry view | Verified identity. Mounted when `mcplex` is enabled. |
| GET | `/a2a/{instanceId}/.well-known/agent.json` | Read the Agent Card of an A2A agent instance | Verified identity. Mounted when `a2aplex` is enabled. |
| GET | `/auth/login` | Return the login challenge details for Hydra | None |
| GET | `/auth/consent` | Read a consent request from Hydra | Verified identity |
| POST | `/auth/consent` | Accept or deny a consent request | Verified identity |
| POST | `/auth/token-hook` | Hydra token hook that adds the agent actor claim | Bearer token from `AUTH_HOOK_TOKEN` |
| GET | `/auth/users/{userId}/scopes` | Read a user's scope ceiling | Verified identity and `admin` role |
| PUT | `/auth/users/{userId}/scopes` | Replace a user's scope ceiling | Verified identity and `admin` role |

The `/api/v1` routes require a verified identity, and they are served by the platform's core handlers. Outside development mode, the `/api/v1` group requires the `admin` or `deployer` role for every non-GET method, except that `/api/v1/dashboard/` requires `admin` for POST. Development mode skips those checks.

The routes under `/auth` are outside `/api/v1`, so the role rule for `/api/v1` does not apply to them. Each route's own rule is in the table above.

Errors use this shape:

```json
{
  "code": "NOT_FOUND",
  "message": "instance not found",
  "request_id": "req-example"
}
```

`details`, `request_id` and `trace_id` are optional and omitted when empty.

## Health

### GET `/healthz`

Reports that the process is running. It does not check any dependency.

```json
{
  "status": "ok"
}
```

### GET `/readyz`

Checks the module runtime and the store before the service accepts traffic. Each failed check returns a `503` with its reason.

| Status | Body | When |
|---|---|---|
| 200 | `{"status": "ok"}` | Modules are ready and the store responds |
| 503 | `{"status": "unavailable", "error": "module unavailable"}` | A module is not ready |
| 503 | `{"status": "unavailable", "error": "store unreachable"}` | The store did not answer |

### Example

```bash
curl -sS "$AIPLEX_URL/readyz"
```

## GET `/api/v1/modules`

Returns the descriptor of every enabled module as a JSON array.

### Module descriptor fields

| Field | Type | Meaning |
|---|---|---|
| `name` | string | Module name, for example `mcplex` or `llmplex` |
| `version` | string | Module version |
| `contract_version` | string | Contract version the module implements |
| `capabilities` | array of string | Omitted when empty |
| `dependencies` | array of object | Omitted when empty. Each entry names a module dependency. |
| `api_prefixes` | array of string | Omitted when empty. API path prefixes the module serves. |
| `navigation` | array of object | Omitted when empty. Console navigation entries. |
| `commands` | array of object | Omitted when empty. Command-line commands the module provides. |
| `config_schema` | object | Omitted when the module declares none. A JSON schema for its configuration. |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/modules" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## Catalog

The catalog lists templates that can be deployed as instances. Each template has a plane. Only templates of enabled planes are returned.

### GET `/api/v1/catalog`

#### Query parameters

| Name | Type | Required | Meaning |
|---|---|---|---|
| `plane` | string | No | `mcplex`, `a2aplex`, `llmplex` or `skillsplex`. A disabled plane returns `404 MODULE_DISABLED`. |
| `page` | integer | No | Zero-based page. Must not be negative. Default 0. |
| `page_size` | integer | No | From 1 to 100. Any other value uses 20. |

#### Response fields

| Field | Type | Meaning |
|---|---|---|
| `templates` | array of template | Templates on this page, sorted by plane, then identifier, then source |
| `total` | integer | Number of matching templates |
| `page` | integer | Page returned |
| `page_size` | integer | Page size used |
| `sources_failed` | array of object | Omitted when every source answered. Each entry has `source` and `error`. |

#### Template fields

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Template identifier |
| `source` | string | Source the template came from |
| `plane` | string | Plane of the template |
| `name` | string | Display name |
| `description` | string | Description |
| `image` | string | Omitted when empty. MCP and A2A templates. |
| `repository` | string | Omitted when empty |
| `version` | string | Omitted when empty |
| `tools` | array of object | Omitted when empty. MCP tools. |
| `task_types` | array of string | Omitted when empty. A2A task types. |
| `agent_card` | object | Omitted when empty. Agent Card metadata. |
| `skills` | array of object | Omitted when empty. SkillsPlex skills. |
| `skill_bundle` | string | Omitted when empty |
| `model_id` | string | Omitted when empty. LLM model. |
| `provider` | string | Omitted when empty |
| `capabilities` | array of string | Omitted when empty |
| `fallback_model_id` | string | Omitted when empty |
| `category` | string | Category |
| `config_schema` | object | Omitted when empty |
| `resource_limits` | object | Omitted when empty |
| `verified` | boolean | Whether the template is verified |
| `tags` | array of string | Omitted when empty |
| `pricing` | object | Omitted when empty |
| `created_at` | RFC 3339 timestamp | Creation time |
| `updated_at` | RFC 3339 timestamp | Last update time |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Page returned |
| 400 | `BAD_REQUEST` | `page` is not a non-negative integer |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 404 | `MODULE_DISABLED` | The requested plane is disabled |

### GET `/api/v1/catalog/{id}`

Returns one template.

| Status | Code | When |
|---|---|---|
| 200 | none | Template returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 404 | `NOT_FOUND` | No template with this identifier |
| 404 | `MODULE_DISABLED` | The template's plane is disabled |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/catalog?plane=mcplex&page=0&page_size=20" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## Instances

An instance is a deployed template. The `plane` field is `mcplex`, `a2aplex`, `llmplex` or `skillsplex`.

### Instance fields

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Instance identifier |
| `plane` | string | Plane of the instance |
| `template_id` | string | Template the instance was deployed from |
| `owner` | string | Principal that deployed the instance, or `anonymous` |
| `namespace` | string | Runtime namespace |
| `spiffe_id` | string | Omitted when empty |
| `scopes` | array of string | Scopes granted to the instance |
| `config` | object | Omitted when empty. Configuration supplied at deploy time. |
| `labels` | object of string | Omitted when empty |
| `status` | string | `provisioning`, `running`, `degraded`, `stopped`, `failed` or `terminated` |
| `replicas` | integer | Replica count |
| `display_name` | string | Omitted when empty |
| `resource_version` | integer | Version of the stored instance |
| `deployed_at` | RFC 3339 timestamp | Deploy time |
| `updated_at` | RFC 3339 timestamp | Last update time |
| `deployed_by` | string | Principal that deployed the instance |
| `health` | object | Omitted when unknown |
| `runtime` | object | Execution runtime of the instance. `{"engine": "none"}` for the standard path. |
| `resources` | array of object | Omitted when empty. Resources the instance refers to. |

### GET `/api/v1/instances`

Returns instances as a JSON array. The optional `plane` query parameter filters the list.

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 500 | `STORE_ERROR` | Store read failed |

### POST `/api/v1/instances`

Deploys an instance. The body is one JSON object with these fields:

| Field | Type | Required | Meaning |
|---|---|---|---|
| `plane` | string | Yes | Plane of the template |
| `template_id` | string | Yes | Catalog template to deploy |
| `config` | object | No | Configuration for the instance |
| `display_name` | string | No | Display name |

The response is `201 Created` with the instance.

| Status | Code | When |
|---|---|---|
| 201 | none | Instance deployed. Body is the instance. |
| 400 | `BAD_REQUEST` | Body does not decode, or `plane` or `template_id` is missing |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` or `deployer` outside development mode |
| 409 | `RUNTIME_IMMUTABLE` | The change would alter the runtime of an existing instance |
| 500 | `DEPLOY_ERROR` | Any other deployment failure |

#### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/instances" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"plane":"mcplex","template_id":"template-example","display_name":"Display example"}'
```

### GET `/api/v1/instances/{id}`

Returns one instance.

| Status | Code | When |
|---|---|---|
| 200 | none | Instance returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 404 | `NOT_FOUND` | No instance with this identifier |
| 500 | `STORE_ERROR` | Store read failed |

### DELETE `/api/v1/instances/{id}`

Undeploys an instance. The response is `204` with no body.

| Status | Code | When |
|---|---|---|
| 204 | none | Instance undeployed |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` or `deployer` outside development mode |
| 404 | `NOT_FOUND` | No instance with this identifier |
| 500 | `UNDEPLOY_ERROR` | Undeploy failed |

### GET `/api/v1/instances/{id}/history`

Returns up to 50 deploy history entries of the instance as a JSON array.

#### History entry fields

| Field | Type | Meaning |
|---|---|---|
| `id` | string | History entry identifier |
| `instance_id` | string | Instance the entry belongs to |
| `action` | string | `deploy`, `undeploy`, `config_update`, `scale` or `restart` |
| `plane` | string | Plane of the instance |
| `template_id` | string | Omitted when empty |
| `owner` | string | Owner of the instance |
| `performed_by` | string | Principal that performed the action |
| `config` | object | Omitted when empty |
| `timestamp` | RFC 3339 timestamp | When the action happened |
| `duration_ms` | integer | Omitted when zero |
| `success` | boolean | Whether the action succeeded |
| `error` | string | Omitted when empty |

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 500 | `STORE_ERROR` | Store read failed |

## Agents

An agent is a registered OAuth client. Its `client_id` is its identifier. An agent's allowed scopes are its ceiling. Each scope starts with one of the namespaces `mcp:`, `a2a:`, `llm:`, `skill:` or `aiplex:`.

### Agent fields

| Field | Type | Meaning |
|---|---|---|
| `revision` | integer | Configuration revision. Sent as `ETag` on read. |
| `client_id` | string | Identifier of the agent. Required on registration. |
| `display_name` | string | Required on registration. Not blank. |
| `description` | string | Optional description |
| `auth_method` | string | `client_credentials`, `authorization_code` or `device_code` |
| `grant_types` | array of string | OAuth grant types. Defaults to the grant of the auth method. |
| `allowed_scopes` | array of string | Scope ceiling. Normalized on registration. |
| `client_secret` | string | Returned once on registration, and only when an authorization server issues the client. Never stored or listed. |
| `wif_principal` | string | Optional workforce principal. Validated on registration. |
| `spiffe_id` | string | Optional SPIFFE identity. Must be credential-free. |
| `redirect_uris` | array of string | Required for `authorization_code`. Each must pass the redirect URI rules. |
| `labels` | object of string | Optional labels |
| `resource_version` | integer | Version of the stored agent |
| `registered_at` | RFC 3339 timestamp | Set by the server |
| `registered_by` | string | Principal that registered the agent. Set by the server. |
| `status` | string | `active`, `provisioning` while an authorization server issues the client, or `deleting` |

### GET `/api/v1/agents`

Returns every registered agent as a JSON array.

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 500 | `STORE_ERROR` | Store read failed |

### POST `/api/v1/agents`

Registers an agent. Unknown fields are rejected. Send `client_id`, `display_name` and `auth_method`, plus any optional fields above.

The `client_secret` field must be absent. Client credentials are issued by the configured authorization server, not supplied by the caller.

When an authorization server is configured, the agent is first saved as `provisioning`, the client is created on the server, and the agent is then saved as `active`. A client that the server does not issue is not left active.

| Status | Code | When |
|---|---|---|
| 201 | none | Agent registered. Body is the agent, with `ETag`. |
| 400 | `BAD_REQUEST` | Body does not decode, has unknown fields, or lacks `client_id` or `display_name` |
| 400 | `INVALID_AUTH_METHOD` | `auth_method` is not one of the three methods |
| 400 | `INVALID_SCOPE` | A scope is malformed or outside the allowed namespaces |
| 400 | `INVALID_GRANT_TYPES` | Grant types do not match the auth method |
| 400 | `INVALID_SPIFFE_ID` | `spiffe_id` is not a credential-free SPIFFE identity |
| 400 | `INVALID_CLIENT_SECRET` | `client_secret` was supplied |
| 400 | `MISSING_REDIRECT_URIS` | `authorization_code` without a redirect URI |
| 400 | `INVALID_REDIRECT_URI` | A redirect URI does not pass the rules |
| 400 | `INVALID_WIF_PRINCIPAL` | `wif_principal` does not pass validation |
| 400 | `INVALID_PRECONDITION` | `If-Match` is malformed |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` or `deployer` outside development mode |
| 409 | `CONFLICT` | An agent with this `client_id` already exists |
| 412 | `REVISION_CONFLICT` | `If-Match` is present and is not `"0"` |
| 500 | `STORE_ERROR` | Agent registry lookup failed |
| 502 | `OAUTH_REGISTRATION_FAILED` | The authorization server did not issue a complete client |
| 503 | `CONFIGURATION_CAS_UNAVAILABLE` | The configuration store does not support revisions |
| 503 | `CONFIGURATION_UNAVAILABLE` | The configuration write could not be acknowledged |

#### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/agents" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"client_id":"agent-example","display_name":"Agent example","auth_method":"client_credentials","allowed_scopes":["mcp:tools:tool-example"]}'
```

### GET `/api/v1/agents/{clientId}`

Returns one agent with an `ETag` header holding the revision.

| Status | Code | When |
|---|---|---|
| 200 | none | Agent returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 404 | `NOT_FOUND` | No agent with this identifier |
| 500 | `STORE_ERROR` | Store read failed |

### DELETE `/api/v1/agents/{clientId}`

Deletes an agent registration. The agent is first marked `deleting`, the client is removed from the authorization server, and then the registration is deleted. `If-Match` must carry the current revision. A missing `If-Match` on an existing agent returns `428`. On success the response is `204` with no body.

| Status | Code | When |
|---|---|---|
| 204 | none | Agent deleted |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` or `deployer` outside development mode |
| 400 | `INVALID_PRECONDITION` | `If-Match` is malformed |
| 404 | `NOT_FOUND` | No agent with this identifier |
| 412 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `PRECONDITION_REQUIRED` | `If-Match` is missing |
| 500 | `STORE_ERROR` | Agent registry lookup failed |
| 502 | `OAUTH_REVOCATION_FAILED` | The authorization server did not remove the client. The agent remains `deleting`. |
| 503 | `CONFIGURATION_CAS_UNAVAILABLE` | The configuration store does not support revisions |
| 503 | `CONFIGURATION_UNAVAILABLE` | The configuration write could not be acknowledged |

### GET `/api/v1/agents/{clientId}/permissions`

Returns the agent's permission ceiling, grouped by plane. Each scope is listed under the plane its namespace belongs to, with a description. The description comes from the catalog template that declares the scope, or it is derived from the scope name when no template declares it.

#### Response fields

| Field | Type | Meaning |
|---|---|---|
| `agent_id` | string | Agent identifier |
| `ceiling` | object | Keys are planes: `mcplex`, `a2aplex`, `llmplex` and `skillsplex`. Each key lists its scopes. |

Each scope entry has `scope` and `description`.

| Status | Code | When |
|---|---|---|
| 200 | none | Permissions returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 404 | `NOT_FOUND` | No agent with this identifier |
| 500 | `STORE_ERROR` | Store read failed |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/agents/agent-example/permissions" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## Dashboard

### GET `/api/v1/dashboard/stats`

Returns one summary of the platform, derived from a single read of the instance inventory.

| Field | Type | Meaning |
|---|---|---|
| `total_instances` | integer | Instances of all planes |
| `running_instances` | integer | Instances with status `running` |
| `registered_agents` | integer | Registered agents |
| `active_planes` | integer | Planes that have at least one instance |
| `mcplex_instances` | integer | MCPlex instances |
| `a2aplex_instances` | integer | A2APlex instances |
| `llmplex_instances` | integer | LLMPlex instances |
| `skillsplex_instances` | integer | SkillsPlex instances |
| `daily_cost_usd` | number or null | USD cost of the last 24 hours, from the usage ledger. `null` when no record in the window carried a USD cost. Unknown is never reported as `0`. |
| `daily_tokens` | integer or null | Input and output tokens of the last 24 hours. `null` when no record reported tokens. |
| `daily_requests` | integer | Requests recorded in the last 24 hours. |
| `daily_unknown_cost_records` | integer | Records in the window that had no cost, so are not in `daily_cost_usd`. |
| `daily_unknown_usage_records` | integer | Records in the window that had no token usage, so are not in `daily_tokens`. |
| `usage_source` | string | `ledger` when the usage ledger is configured. `legacy-store` when it is not: the totals then come from the registry's usage records, and the `daily_unknown_*` counts are `0`. |
| `tool_calls_24h` | integer | Ledger records in the last 24 hours that name a tool (`tool_name` set). `tools/list` exchanges are not counted. |
| `a2a_delegations_24h` | integer | Recorded delegations |
| `policy_denials_24h` | integer | Recorded policy denials |

The usage fields are computed from the usage ledger for the caller's tenant, over the last 24 hours. Cost and
tokens are summed only from records that report them, so a total that includes unknown records is not complete:
read `daily_unknown_cost_records` and `daily_unknown_usage_records` next to it. `daily_requests` and
`tool_calls_24h` are always numbers, `0` when there are no matching records. In this handler `usage_source` is
only `ledger` or `legacy-store`.

| Status | Code | When |
|---|---|---|
| 200 | none | Summary returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 503 | `STATS_UNAVAILABLE` | A source needed for the summary could not be read |

### GET `/api/v1/dashboard/denials`

Returns the most recent 50 policy denials as a JSON array. The route policy classifies it as `admin` and deployment-wide: denials are deployment-wide operator data, so a verified identity without the `admin` role is refused (`403 ROLE_REQUIRED`), and so is an identity bound to a tenant (`403 DEPLOYMENT_SCOPE_REQUIRED`). A verified identity alone is not enough.

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Denial identifier |
| `timestamp` | RFC 3339 timestamp | When the denial happened |
| `plane` | string | Plane of the request |
| `agent_id` | string | Agent that was denied |
| `user_id` | string | User on whose behalf the call was made |
| `action` | string | Action that was denied |
| `scope` | string | Scope that was missing |
| `reason` | string | Reason, for example `scope_missing`, `budget_exceeded` or `rate_limited` |
| `request_id` | string | Request identifier |

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | The identity does not hold the `admin` role |
| 403 | `DEPLOYMENT_SCOPE_REQUIRED` | The identity is bound to a tenant |
| 500 | `STORE_ERROR` | Store read failed |

### POST `/api/v1/dashboard/denials`

Records a policy denial. The body has the denial fields above, plus an optional `trace_id` (32 lower-case hex digits). The server assigns `id` and `timestamp` and ignores any the caller sends; unknown fields, control characters and values over the length limits (agent and user 256, action 512, scope 512, request id 128) are refused. The body is limited to 4096 bytes. The response is `201 Created` with the stored denial. `POST /internal/policy-denials` takes the same body from the authz sidecar behind a shared bearer secret; see [policy denials](../library/policy-denials.md).

Outside development mode, this route requires the `admin` role, because the `/api/v1/dashboard/` prefix is restricted to `admin` for writes.

| Status | Code | When |
|---|---|---|
| 201 | none | Denial recorded |
| 400 | `BAD_REQUEST`, `INVALID_FIELD` | Body does not decode, has unknown fields, or a field is invalid |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | Caller lacks `admin` outside development mode |
| 413 | `PAYLOAD_TOO_LARGE` | Body over 4096 bytes |
| 429 | `RATE_LIMITED` | Too many reports |
| 500 | `STORE_ERROR` | Store write failed |

## Registry and Agent Cards

### GET `/v0.1/servers`

Returns the catalog page in the same shape as `GET /api/v1/catalog`, with templates from every enabled plane unless `plane` is given. The route is mounted when the `mcplex` module is enabled. Its query parameters and errors are those of the catalog list.

### GET `/a2a/{instanceId}/.well-known/agent.json`

Returns the Agent Card of an A2A agent instance. It is mounted when the `a2aplex` module is enabled.

#### Agent Card fields

| Field | Type | Meaning |
|---|---|---|
| `name` | string | Template name |
| `description` | string | Template description |
| `url` | string | `/a2a/{instanceId}` |
| `version` | string | Omitted when empty |
| `task_types` | array of object | Each has `type` and `description`, and may have `input_schema` and `output_schema` |
| `capabilities` | array of string | Omitted when empty |
| `auth_schemes` | array of object | Omitted when the host configures none. Each has `scheme` and may have `config`. Only the schemes the gateway is configured to use are listed. |
| `metadata` | object | Omitted when the template has no Agent Card metadata |

| Status | Code | When |
|---|---|---|
| 200 | none | Agent Card returned |
| 400 | `BAD_REQUEST` | The instance is not an A2A agent |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 404 | `NOT_FOUND` | No instance with this identifier |
| 409 | `INSTANCE_UNAVAILABLE` | The instance is not running |
| 500 | `STORE_ERROR` | Store read failed |
| 500 | `INVALID_AGENT_CARD` | The assembled card fails validation |
| 503 | `TEMPLATE_UNAVAILABLE` | The instance's template cannot be read |

## Authentication webhooks and user scopes

AIPlex uses Ory Hydra for OAuth. Hydra sends consent and token requests to these routes. The consent routes are also used by the Console.

### GET `/auth/login`

Hydra redirects a user here when login is needed. The route requires the `login_challenge` query parameter and returns the challenge and the login page path.

```json
{
  "challenge": "challenge-example",
  "login_url": "/ui/login?login_challenge=challenge-example"
}
```

| Status | Code | When |
|---|---|---|
| 200 | none | Challenge returned |
| 400 | `BAD_REQUEST` | `login_challenge` is missing |

### GET `/auth/consent`

Reads the consent request identified by the `consent_challenge` query parameter. The response contains what the Console needs to show the user. `grantable_scopes` is the set of requested scopes that the agent's ceiling and the user's ceiling both allow.

| Field | Type | Meaning |
|---|---|---|
| `challenge` | string | Consent challenge |
| `subject` | string | User the consent is for |
| `client_id` | string | Agent requesting access |
| `client_name` | string | Display name of the agent |
| `requested_scopes` | array of string | Scopes the agent asked for |
| `grantable_scopes` | array of string | Requested scopes that both ceilings allow |

| Status | Code | When |
|---|---|---|
| 200 | none | Consent request returned |
| 400 | `BAD_REQUEST` | `consent_challenge` is missing |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | The consent subject is not the verified caller |
| 403 | `OAUTH_AGENT_DENIED` | The agent is not active |
| 404 | `NOT_FOUND` | The agent is not registered |
| 502 | `HYDRA_ERROR` | Hydra did not return a complete consent request |
| 503 | `STORE_ERROR` | Agent policy unavailable |

### POST `/auth/consent`

Accepts or denies a consent request. The body has these fields:

| Field | Type | Required | Meaning |
|---|---|---|---|
| `challenge` | string | Yes | Consent challenge |
| `granted_scopes` | array of string | Yes | Scopes to grant. Must be present, and may be empty. |
| `deny` | boolean | No | `true` to deny the request. `granted_scopes` must still be present. |

The response is `{"redirect_to": "<url>"}`. Hydra then continues the flow at that URL.

| Status | Code | When |
|---|---|---|
| 200 | none | Decision recorded. Body contains `redirect_to`. |
| 400 | `BAD_REQUEST` | Body does not decode, `challenge` is missing, or `granted_scopes` is missing |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `FORBIDDEN` | On denial, the consent subject is not the verified caller |
| 502 | `HYDRA_ERROR` | Hydra could not complete the decision |

### POST `/auth/token-hook`

Called by Hydra before each token is issued. It is authenticated with the bearer token in `AUTH_HOOK_TOKEN`. It checks that the agent is active and that the requested scopes are within its ceiling, and it adds an `act` claim with the agent's actor subject.

The hook reads the client identity from `request.client_id`, `requester.client_id`, `client_id` or `client.client_id`. Conflicting values are refused. It reads the scopes from `granted_scopes`, and from `request` and `requester` (`granted_scopes` and `requested_scopes`). The session's `extra`, or else its `access_token`, becomes the token claims. Its `id_token` is passed through when present.

The response is `{"session": {"access_token": {...}, "id_token": {...}}}`. The `id_token` key is present only when the hook received one.

| Status | Code | When |
|---|---|---|
| 200 | none | Session returned |
| 400 | `BAD_REQUEST` | Body does not decode, client identity is missing or conflicting, or scopes are invalid |
| 403 | `OAUTH_AGENT_DENIED` | The agent is not registered or not active |
| 403 | `OAUTH_SCOPE_DENIED` | A scope exceeds the agent's ceiling |
| 503 | `STORE_ERROR` | Agent policy unavailable |

### GET `/auth/users/{userId}/scopes`

Returns a user's scope ceiling. `userId` must be a canonical principal identifier, as returned by `GET /api/v1/iam/whoami`. The response carries an `ETag` header with the revision.

| Field | Type | Meaning |
|---|---|---|
| `user_id` | string | Principal identifier |
| `revision` | integer | Configuration revision |
| `scopes` | array of string | The user's scopes. An empty array when none are set. |
| `by_plane` | object | The same scopes, grouped by plane |

| Status | Code | When |
|---|---|---|
| 200 | none | Scopes returned |
| 400 | `INVALID_PRINCIPAL` | `userId` is not a canonical principal identifier |
| 401 | `UNAUTHENTICATED` | No verified role context |
| 403 | `FORBIDDEN` | Caller lacks the `admin` role |
| 500 | `STORE_ERROR` | Store read failed |
| 503 | `CONFIGURATION_UNAVAILABLE` | The stored scope configuration cannot be read |

### PUT `/auth/users/{userId}/scopes`

Replaces a user's scope ceiling. The body is `{"scopes": [...]}`. `scopes` must be present. Send an empty array to clear the grants. `If-Match` must carry the current revision when the user already has a configuration. On success the response is `204` with no body.

| Status | Code | When |
|---|---|---|
| 204 | none | Scopes replaced |
| 400 | `INVALID_PRINCIPAL` | `userId` is not a canonical principal identifier |
| 400 | `BAD_REQUEST` | Body does not decode, has unknown fields, or `scopes` is missing |
| 400 | `INVALID_SCOPE` | A scope is malformed |
| 400 | `INVALID_PRECONDITION` | `If-Match` is malformed |
| 401 | `UNAUTHENTICATED` | No verified role context |
| 403 | `FORBIDDEN` | Caller lacks the `admin` role |
| 412 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `PRECONDITION_REQUIRED` | `If-Match` is missing for an existing configuration |
| 503 | `CONFIGURATION_CAS_UNAVAILABLE` | The configuration store does not support revisions |
| 503 | `CONFIGURATION_UNAVAILABLE` | The write could not be acknowledged |

## Internal routes

Routes under `/internal/` are called by other AIPlex components, not by clients. They use their own bearer token and rate limit, and they are not part of this reference.

| Method | Path | Note |
|---|---|---|
| POST | `/internal/tape/events` | Internal. Runtime event ingestion. |
| POST | `/internal/projections/rebuild/{run_id}` | Internal. Projection repair. |

## Behaviour notes

- **Health is unauthenticated.** `/healthz` and `/readyz` do not check identity, so they should be exposed only to the platform's probes.
- **Instances and agents are not tenant-scoped in these handlers.** They read the platform registry directly. The tenant-scoped APIs are described on their own pages.
- **Registration writes two systems.** An agent registration reaches the authorization server only after it is reserved locally. A failure leaves the agent in `provisioning` or `deleting`, and an operator must reconcile it.
- **Scope ceilings are per request.** The token hook and consent routes check the current agent and user ceilings, so a change takes effect on the next token.

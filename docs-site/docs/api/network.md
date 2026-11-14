---
title: Network API
---

Route access is enforced by a default-deny route policy: a route with no explicit rule is open to operators only. Check the route's response for the authoritative answer.

The network API describes how an agent's runtime reaches its network and how infrastructure is deployed for it. It has four parts. Network profiles describe a network target, such as an agent identity or a private endpoint, and render reviewed configuration. Deployment providers hold the approved bindings that can change infrastructure. Deployment intents connect desired state to one approved binding. Deployment plans move through approval, execution, receipt and observation. Every deployment action is tied to the tenant, to a specific plan digest, and to the principal and actor that prepared it.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/network/profiles` | List network profiles of the tenant | `admin` or `deployer` outside development mode; the tenant comes from the verified identity |
| GET | `/api/v1/network/profiles/{id}` | Read one network profile | `admin` or `deployer` outside development mode; the tenant comes from the verified identity |
| PUT | `/api/v1/network/profiles/{id}` | Create or replace a network profile | `admin` or `deployer` outside development mode |
| DELETE | `/api/v1/network/profiles/{id}` | Delete a network profile | `admin` or `deployer` outside development mode |
| POST | `/api/v1/network/profiles/{id}/plan` | Render the reviewed configuration for a network profile | `admin` or `deployer` outside development mode |
| GET | `/api/v1/network/deployment-providers/{provider}/bindings` | List the approved bindings of a deployment provider | `admin` or `deployer` outside development mode; the tenant comes from the verified identity |
| POST | `/api/v1/network/deployment-providers/{provider}/bindings/{binding}/plan` | Prepare a deployment plan from the saved intent | `admin` or `deployer` and grant `aiplex:deployment:plan:{resource_id}` |
| GET | `/api/v1/network/deployment-intents` | List deployment intents | `admin` or `deployer` outside development mode |
| GET | `/api/v1/network/deployment-intents/{id}` | Read one deployment intent | `admin` or `deployer` outside development mode |
| PUT | `/api/v1/network/deployment-intents/{id}` | Create or replace a deployment intent | `admin` or `deployer` outside development mode |
| DELETE | `/api/v1/network/deployment-intents/{id}` | Delete a deployment intent | `admin` or `deployer` outside development mode |
| POST | `/api/v1/network/deployment-intents/{id}/plan` | Prepare a deployment plan from one intent | `admin` or `deployer` and grant `aiplex:deployment:plan:{resource_id}` |
| GET | `/api/v1/network/plans` | List deployment plans | `admin` or `deployer` outside development mode |
| GET | `/api/v1/network/plans/{id}` | Read one deployment plan | `admin` or `deployer` outside development mode |
| POST | `/api/v1/network/plans/{id}/approve` | Approve a deployment plan | `admin` or `deployer` and grant `aiplex:deployment:approve:{resource_id}` |
| POST | `/api/v1/network/plans/{id}/execute` | Execute an approved deployment plan | `admin` or `deployer`, grant `aiplex:deployment:execute:{resource_id}`, and the original executor |
| GET | `/api/v1/network/plans/{id}/receipt` | Read the receipt of a deployment plan | `admin` or `deployer` outside development mode; only the original executor |
| POST | `/api/v1/network/plans/{id}/observe` | Observe the native state of an executed deployment | `admin` or `deployer` and grant `aiplex:deployment:observe:{resource_id}` |
| GET | `/api/v1/network/plans/{id}/observation` | Read a stored observation | `admin` or `deployer` outside development mode; only the original executor |

The routes are mounted under `/api/v1` by the `networking` module, which is enabled by default. All routes require a verified identity. Outside development mode, the `/api/v1` group requires the `admin` or `deployer` role for every non-GET method. Development mode skips the role checks on the GET routes that name a role below.

Errors use this shape:

```json
{
  "code": "NOT_FOUND",
  "message": "deployment receipt unavailable",
  "request_id": "req-example"
}
```

`details`, `request_id` and `trace_id` are optional and omitted when empty.

Resource envelopes returned by the saved-resource routes have this shape:

| Field | Type | Meaning |
|---|---|---|
| `tenant_id` | string | Owning tenant |
| `kind` | string | `network-profile`, `deployment-intent` or `deployment-plan` |
| `id` | string | Resource identifier |
| `revision` | integer | Revision. Used as the `If-Match` value. |
| `spec` | object | The stored resource |

## Network profiles

A network profile names a target and its reviewed settings. The `kind` field selects the rules that apply. The kinds are `gke-agent-identity`, `aws-agentcore-privatelink`, `azure-private-endpoint` and `kubernetes-egress`. Each kind requires the fields its validation checks. A profile that fails validation is refused on save.

### Profile fields

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Profile identifier. Must equal the path `{id}`. |
| `tenant_id` | string | Must equal the tenant of the request |
| `kind` | string | One of the four kinds above |
| `region` | string | Optional region |
| `namespace` | string | Optional Kubernetes namespace |
| `workload_name` | string | Optional workload name |
| `service_account` | string | Optional service account |
| `trust_domain` | string | Optional trust domain |
| `agent_card_path` | string | Optional path of the agent card |
| `vpc_id` | string | Optional VPC identifier |
| `subnet_ids` | array of string | Optional subnet identifiers |
| `security_group_ids` | array of string | Optional security group identifiers |
| `aws_principals` | array of string | Optional AWS principal ARNs |
| `aws_actions` | array of string | Optional AWS actions |
| `aws_resource_arns` | array of string | Optional AWS resource ARNs |
| `aws_auth_mode` | string | Optional AWS authentication mode |
| `resource_group` | string | Optional Azure resource group |
| `target_resource_id` | string | Optional target resource identifier |
| `private_dns_zone_ids` | array of string | Optional private DNS zone identifiers |
| `destination_cidrs` | array of string | Optional destination CIDR blocks |
| `dns_cidrs` | array of string | Optional DNS CIDR blocks |

### GET `/api/v1/network/profiles` and GET `/api/v1/network/profiles/{id}`

List returns a JSON array of resource envelopes. Read returns one envelope with an `ETag` header holding the revision.

| Status | Code | When |
|---|---|---|
| 200 | none | Array or envelope returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 403 | `TENANT_REQUIRED` | No trusted tenant binding |
| 404 | `NOT_FOUND` | Read of a profile that does not exist in the tenant |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### PUT `/api/v1/network/profiles/{id}`

Creates a profile when no `If-Match` is sent. Replaces it when `If-Match` carries the current revision. The body is one JSON object with the profile fields above. Unknown fields are rejected. The body's `id` must equal the path, and its `tenant_id` must equal the caller's tenant.

| Status | Code | When |
|---|---|---|
| 200 | none | Profile stored. Body is the envelope with an `ETag` header. |
| 400 | `INVALID_REVISION` | `If-Match` is not a revision number |
| 400 | `INVALID_SPEC` | Body does not decode, has unknown fields, the identity does not match, or the kind rules fail |
| 403 | `ROLE_REQUIRED` | Caller lacks `admin` or `deployer` outside development mode |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

### DELETE `/api/v1/network/profiles/{id}`

`If-Match` is required. On success the response is `204` with no body.

| Status | Code | When |
|---|---|---|
| 204 | none | Profile deleted |
| 403 | `ROLE_REQUIRED` | Caller lacks `admin` or `deployer` outside development mode |
| 404 | `NOT_FOUND` | No profile with this identifier |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `REVISION_REQUIRED` | `If-Match` is missing or not a positive revision |

### POST `/api/v1/network/profiles/{id}/plan`

Renders the reviewed configuration for a saved profile. It reads the profile and returns the rendered plan. It does not change infrastructure. The request has no body.

The response is a plan with these fields:

| Field | Type | Meaning |
|---|---|---|
| `version` | string | Plan format version |
| `profile_id` | string | Profile identifier |
| `tenant_id` | string | Tenant of the profile |
| `artifacts` | array of object | Rendered files. Each has `path`, `format` and `content`. |
| `apply_commands` | array of array of string | Commands a reviewer may run to apply the artifacts |
| `scope` | string | Scope the plan covers |

| Status | Code | When |
|---|---|---|
| 200 | none | Plan returned |
| 400 | `INVALID_PLAN` | The saved profile cannot be rendered |
| 403 | `ROLE_REQUIRED` | Caller lacks `admin` or `deployer` outside development mode |
| 404 | `NOT_FOUND` | No profile with this identifier |

## Deployment bindings

A binding is an approved action of a deployment provider for one tenant. The provider is configured by the operator. The API only lists and selects bindings. It never adds one.

### GET `/api/v1/network/deployment-providers/{provider}/bindings`

Returns the tenant's bindings of the provider as a JSON array, sorted by `id`.

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Binding identifier |
| `resource_id` | string | Resource the binding acts on. A deployment intent with the same identifier must use this binding. |
| `operation_id` | string | Omitted when empty |
| `effect` | string | Effect of the binding |
| `plan_only` | boolean | `true` when the binding only prepares artifacts and cannot be executed |

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `ROLE_REQUIRED` | The caller is signed in but lacks `admin` or `deployer` role |
| 403 | `TENANT_REQUIRED` | No trusted tenant binding |
| 404 | `PROVIDER_NOT_FOUND` | The provider is not configured |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/network/deployment-providers/provider-example/bindings" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
[
  {
    "id": "binding-example",
    "resource_id": "resource-example",
    "effect": "effect-example",
    "plan_only": false
  }
]
```

## Deployment intents

An intent is tenant-owned desired state. It names a provider, a binding, and reviewed non-secret inputs. Credentials are never part of an intent. An intent's `id` must equal the `resource_id` of its binding, so one intent can only drive one approved resource.

### Intent fields

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | Yes | Must equal the path `{id}` and the binding's `resource_id` |
| `provider_ref` | string | Yes | Configured deployment provider |
| `binding_id` | string | Yes | Binding of that provider |
| `network_profile_ref` | string | No | Network profile in the tenant. Its revision is recorded in the plan. |
| `inputs` | any JSON value | Yes | Reviewed non-secret desired state |

### Intent routes

`GET` on the list and the item route, and `POST .../plan`, follow the same rules as the other saved resources. `GET` requires the `admin` or `deployer` role outside development mode, and otherwise returns `403 RESOURCE_FORBIDDEN`. `PUT` validates the intent against its provider and binding, and returns `400 INVALID_SPEC` when the provider is not configured or the binding does not own the intent. `DELETE` requires `If-Match`.

| Status | Code | When |
|---|---|---|
| 200 | none | Intent or list returned, or intent stored |
| 204 | none | Intent deleted |
| 400 | `INVALID_SPEC` | Body does not decode, has unknown fields, the identity does not match, the provider is not configured, or the binding does not own the intent |
| 400 | `INVALID_REVISION` | `If-Match` is not a revision number |
| 403 | `RESOURCE_FORBIDDEN` | `GET` without the `admin` or `deployer` role outside development mode |
| 403 | `FORBIDDEN` | Non-GET without the `admin` or `deployer` role outside development mode |
| 404 | `NOT_FOUND` | No intent with this identifier |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `REVISION_REQUIRED` | Delete without `If-Match` |
| 500 | `RESOURCE_ERROR` | Resource store unavailable |

## Deployment plans

A plan is prepared from a saved intent and binding. The plan is keyed by its action digest, so preparing the same action again returns the same plan. Execution uses the saved plan, not the request body.

### Prepare a plan

`POST /api/v1/network/deployment-providers/{provider}/bindings/{binding}/plan` and `POST /api/v1/network/deployment-intents/{id}/plan` both prepare a plan. The second route uses the provider and binding stored on the intent.

Both routes need an `Idempotency-Key` header, and both reject a body other than an empty object or `{}`. The inputs come only from the saved intent.

The response is the saved plan as a resource envelope with kind `deployment-plan`. Its `spec` has these fields:

| Field | Type | Meaning |
|---|---|---|
| `plan` | object | The prepared plan. Its fields include `provider_id`, `binding_id`, `tenant_id`, `principal_id`, `actor_id` (omitted when empty), `resource_id`, `operation_id` (omitted when empty), `action_digest`, `idempotency_key`, `effect`, `plan_only`, `state`, `created_at`, `expires_at` and `preview`. |
| `intent_id` | string | Intent the plan was prepared from |
| `intent_revision` | integer | Revision of the intent at preparation |
| `network_profile_ref` | string | Network profile of the intent, if any |
| `network_profile_revision` | integer | Revision of that profile at preparation |

| Status | Code | When |
|---|---|---|
| 200 | none | Plan stored, or the existing plan for the same action returned |
| 400 | `IDEMPOTENCY_REQUIRED` | `Idempotency-Key` header missing |
| 400 | `SAVED_INTENT_REQUIRED` | Body is not empty or `{}` |
| 400 | `INTENT_BINDING_MISMATCH` | The saved intent does not match the provider and binding |
| 400 | `NETWORK_PROFILE_UNAVAILABLE` | The intent's network profile is missing in the tenant |
| 400 | `PLAN_REFUSED` | The provider refused the plan, including a missing `aiplex:deployment:plan:{resource_id}` grant |
| 401 | `IDENTITY_REQUIRED` | No verified identity |
| 403 | `FORBIDDEN` | Caller lacks `admin` or `deployer` outside development mode |
| 404 | `PROVIDER_NOT_FOUND` | The provider is not configured |
| 404 | `BINDING_NOT_FOUND` | The binding is not configured |
| 404 | `NOT_FOUND` | The intent or network profile is missing |
| 500 | `PLAN_ERROR` | The plan could not be serialized |

### GET `/api/v1/network/plans` and GET `/api/v1/network/plans/{id}`

These routes return the saved plans as resource envelopes. They require the `admin` or `deployer` role outside development mode, and otherwise return `403 RESOURCE_FORBIDDEN`.

### Approve a plan

`POST /api/v1/network/plans/{id}/approve` records an approval of the exact plan digest. Send `digest` (which must equal the plan's action digest) and `reason` (non-blank, at most 2048 characters). The caller must hold the grant `aiplex:deployment:approve:{resource_id}`.

The approval is stored as a resource of kind `deployment-approval`. Its identifier is derived from the tenant, the plan, and the approver's principal and actor. An approval that expires with the plan cannot be used.

| Status | Code | When |
|---|---|---|
| 201 | none | Approval stored. Body is the approval envelope. An existing approval for the same approver is returned in the same shape. |
| 400 | `APPROVAL_INVALID` | Body does not decode, `digest` does not match, or `reason` is blank or too long |
| 401 | `IDENTITY_REQUIRED` | No verified identity |
| 403 | `ACTION_DENIED` | The approval grant is missing |
| 409 | `PLAN_NOT_EXECUTABLE` | The plan is plan-only or has expired |
| 409 | `REVISION_CONFLICT` | The intent or network profile has changed since the plan was prepared |
| 404 | `NOT_FOUND` | The plan does not exist |

### Execute a plan

`POST /api/v1/network/plans/{id}/execute` runs an approved plan through its provider. Send `digest` (the plan's action digest) and `approval_id` (the approval's identifier). The caller must be the plan's original principal and actor, and must hold `aiplex:deployment:execute:{resource_id}`.

When a receipt already exists for the plan's idempotency key, that receipt is returned and nothing runs again.

| Status | Code | When |
|---|---|---|
| 200 | none | Execution completed. Body is the receipt. A prior receipt is returned in the same shape. |
| 202 | none | Execution was not confirmed, but a durable receipt exists. Body is the receipt. |
| 400 | `APPROVAL_REQUIRED` | `digest` does not match, or `approval_id` is missing |
| 401 | `IDENTITY_REQUIRED` | No verified identity |
| 403 | `ACTION_DENIED` | The caller is not the original executor, or the execute grant is missing |
| 404 | `NOT_FOUND` | The plan does not exist |
| 409 | `DEPLOYMENT_REFUSED` | The provider refused execution before a receipt was recorded |
| 409 | `REVISION_CONFLICT` | The intent or network profile changed after the plan was prepared |
| 503 | `PROVIDER_UNAVAILABLE` | The plan's provider is not configured |

### Receipt

`GET /api/v1/network/plans/{id}/receipt` returns the receipt of the plan. The fields are:

| Field | Type | Meaning |
|---|---|---|
| `provider_id` | string | Provider that ran the plan |
| `tenant_id` | string | Owning tenant |
| `principal_id` | string | Original executor |
| `resource_id` | string | Resource the plan acts on |
| `idempotency_key` | string | Idempotency key of the plan |
| `action_digest` | string | Digest of the approved action |
| `state` | string | `planned`, `pending`, `invocation_succeeded`, `refused` or `unknown` |
| `output_digest` | string | Omitted when empty |
| `error_code` | string | Omitted when empty |
| `readiness` | string | Readiness reported at completion |
| `finished_at` | RFC 3339 timestamp | Time the receipt was recorded |

Only the original executor can read a receipt. Anyone else receives `404 NOT_FOUND`.

| Status | Code | When |
|---|---|---|
| 200 | none | Receipt returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |
| 403 | `TENANT_REQUIRED` | No trusted tenant binding |
| 404 | `NOT_FOUND` | The caller is not the original executor, or no receipt exists |

### Observe a deployment

`POST /api/v1/network/plans/{id}/observe` reads the native state of an executed deployment through a configured readiness probe. The request needs an `Idempotency-Key` header of at most 200 characters, and the caller must hold `aiplex:deployment:observe:{resource_id}` and be the original executor.

The handler sends one `GET` to the probe's configured URL with the probe's identity. The URL and identity come from configuration, never from the request. The native response must be successful JSON of at most 1 MiB. The configured resource field of that response must equal the configured target, and the configured ready field is read as the native state.

The observation is stored under the plan and the idempotency key. Repeating the same key returns the stored observation.

Observation fields:

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Observation identifier |
| `tenant_id` | string | Owning tenant |
| `principal_id` | string | Original executor |
| `actor_id` | string | Omitted when empty |
| `plan_id` | string | Plan that was observed |
| `action_digest` | string | Digest of the approved action |
| `receipt_digest` | string | Digest of the receipt observed |
| `configuration_digest` | string | Digest of the probe configuration |
| `target_ref` | string | Target the native response named |
| `native_state` | string | State the native response reported |
| `ready` | boolean | `true` when `native_state` equals the configured ready value |
| `result_digest` | string | Digest of the native response |
| `observed_at` | RFC 3339 timestamp | Time of observation |
| `expires_at` | RFC 3339 timestamp | When the observation expires. Five minutes after observation when the probe sets no lifetime. |

| Status | Code | When |
|---|---|---|
| 200 | none | Observation stored, or the stored observation returned for the same key |
| 400 | `IDEMPOTENCY_REQUIRED` | `Idempotency-Key` missing or too long |
| 401 | `IDENTITY_REQUIRED` | No verified identity |
| 403 | `ACTION_DENIED` | The caller is not the original owner, or the observe grant is missing |
| 404 | `NOT_FOUND` | No receipt exists for the plan |
| 409 | `OBSERVATION_CONFLICT` | The key belongs to an observation of a different configuration |
| 501 | `READINESS_UNSUPPORTED` | The binding has no readiness probe configured |
| 502 | `OBSERVATION_UNKNOWN` | The native endpoint could not be reached |
| 502 | `OBSERVATION_INVALID` | The native response is not a successful bounded JSON object |
| 502 | `OBSERVATION_TARGET_MISMATCH` | The native response does not name the configured target and state |
| 503 | `OBSERVATION_ADMISSION_DENIED` | The observation could not be admitted |
| 503 | `READINESS_UNAVAILABLE` | The probe's identity or destination is unavailable |
| 503 | `OBSERVATION_DISPATCH_DENIED` | The observation could not be dispatched |
| 503 | `OBSERVATION_UNACKNOWLEDGED` | The observation was stored, but its accounting acknowledgement failed |

### Read an observation

`GET /api/v1/network/plans/{id}/observation` returns the observation stored under the plan and the `Idempotency-Key` header sent with the original observe request. Only the original executor can read it.

| Status | Code | When |
|---|---|---|
| 200 | none | Observation returned |
| 400 | `IDEMPOTENCY_REQUIRED` | The original key is missing or too long |
| 403 | `TENANT_REQUIRED` | No trusted tenant binding |
| 404 | `NOT_FOUND` | The caller is not the original executor, or no observation exists under this key |
| 503 | `OBSERVATION_INVALID` | The stored observation does not match the plan |

## Behaviour notes

- **Plans are keyed by digest.** A plan's identifier is the digest of the action. The same action prepared again returns the same plan.
- **Execution uses saved state.** Inputs, provenance and the network revision come from the saved plan. A change to the intent or the network profile after preparation makes approval or execution fail with a conflict.
- **Only the original executor continues.** Execution, observation and their reads check the principal and actor that prepared the plan. Approval records that executor, and execution then requires the same principal and actor.
- **Grants are checked twice.** Route handlers check the grant at the request, and the provider checks it again when it runs the action.
- **Observation is read-only.** It sends one `GET` to a configured probe and stores the result. It never changes the deployment.

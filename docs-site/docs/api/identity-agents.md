---
title: Agent Identity API
---

The agent identity API binds external workload identities (cloud service accounts, workload identity federation, Kubernetes service accounts, GitHub Actions, SPIFFE or any OIDC issuer) to a registered AIPlex agent. An operator picks a preset, supplies a few inputs, and the server derives an exact trust tuple: issuer, JWKS location, audience, subject and required claims. The API also renders a setup plan for the operator and runs a dry-run verification of a presented token. The verify endpoint never returns the token, and the token is not written to logs.

Bindings are tenant-scoped. A binding in one tenant is invisible in another. Listing and reading bindings and running dry-runs require the `admin` or `deployer` role. Writes require the `admin` role. The preset catalogue is readable by any verified caller of the tenant.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/agents/identity-presets` | Preset catalogue and the default audience | Any verified identity |
| GET | `/api/v1/agents/{clientId}/identities` | List the bindings of one agent | `admin` or `deployer` |
| GET | `/api/v1/agents/{clientId}/identities/{id}` | Read one binding | `admin` or `deployer` |
| PUT | `/api/v1/agents/{clientId}/identities/{id}` | Create or replace a binding | `admin` role |
| DELETE | `/api/v1/agents/{clientId}/identities/{id}` | Delete a binding. `If-Match` required. | `admin` role |
| POST | `/api/v1/agents/{clientId}/identities/{id}/plan` | Render the cloud-side setup plan for a binding | `admin` or `deployer` |
| POST | `/api/v1/agents/identities/verify` | Dry-run a presented token against bindings | `admin` or `deployer` |

Every route is under `/api/v1` and requires a verified identity. Outside development mode, a caller without the required role receives `403 FORBIDDEN`. A caller with no verified role context receives `401 UNAUTHENTICATED`. The tenant must be bound. Otherwise the response is `403 TENANT_REQUIRED`.

Errors use this shape:

```json
{
  "code": "INVALID_BINDING",
  "message": "binding id must be a lower-case DNS label of at most 64 characters",
  "request_id": "req-example"
}
```

## GET /api/v1/agents/identity-presets

Returns the ten presets, the default audience, and whether live authentication through bindings is enabled.

| Field | Type | Meaning |
|---|---|---|
| `presets` | array of preset | The catalogue |
| `default_audience` | string | Omitted when no audience is configured. The audience suggested to presets. |
| `live_authentication` | boolean | `true` when bindings authenticate live requests |

Each preset has:

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Preset identifier |
| `display_name` | string | Label |
| `cloud` | string | Cloud or platform |
| `description` | string | Description |
| `inputs` | array of field | Operator inputs: `name`, `label`, `description`, `required`, and optionally `example`, `enum` and `default` |
| `subject_shape` | string | Describes the subject the binding pins |
| `issuer_shape` | string | Describes the issuer the binding pins |
| `allows_required_claims` | boolean | Whether extra exact-match claims may be added |
| `algorithms` | array of string | Signature algorithms the verifier accepts for this preset: `["EdDSA"]` for `branchyard`, `["RS256", "ES256", "ES384"]` for every other preset |
| `notes` | array of string | Omitted when empty. Caveats to read before binding. |

The preset identifiers are `gcp-service-account`, `gcp-agent-identity`, `aws-sts-web-identity`, `aws-eks-irsa`, `kubernetes`, `azure-managed-identity`, `github-actions`, `spiffe-oidc`, `oidc` and `branchyard` (a Branchyard server's EdDSA turn tokens; see the agent identity federation guide).

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Catalogue returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/agents/identity-presets" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

```json
{
  "presets": [
    {
      "id": "oidc",
      "display_name": "Generic OIDC",
      "cloud": "any",
      "description": "example",
      "inputs": [
        {"name": "issuer", "label": "Issuer", "description": "example", "required": true}
      ],
      "subject_shape": "example",
      "issuer_shape": "example",
      "allows_required_claims": true,
      "algorithms": ["RS256", "ES256", "ES384"]
    }
  ],
  "default_audience": "https://aiplex.example.com",
  "live_authentication": false
}
```

## Binding resource

A binding is stored per tenant, per agent, per binding identifier. It has these fields:

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Binding identifier. A lower-case DNS label of at most 64 characters, matching `^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`. |
| `client_id` | string | Registered agent client identifier |
| `preset` | string | Preset identifier |
| `inputs` | object of string | Operator inputs, with empty values removed |
| `display_name` | string | Omitted when empty. At most 256 characters. |
| `disabled` | boolean | Disabled bindings never authenticate |
| `issuer` | string | Derived. Issuer the token must carry. |
| `jwks_url` | string | Derived. Where the signing keys are fetched. |
| `audience` | string | Derived. Exact audience. |
| `subject_claim` | string | Derived. Claim that holds the subject. `sub` or `oid`. |
| `subject` | string | Derived. Exact subject value. |
| `required_claims` | object of string | Omitted when empty. Exact-match claims the token must carry. Derived from the preset and the operator's extra claims. |
| `extra_claims` | object of string | Omitted when empty. Operator-added claims. |
| `revision` | integer | Revision. Equal to the `ETag` value. |

Responses to reads and writes add these fields to the binding:

| Field | Type | Meaning |
|---|---|---|
| `principal_id` | string | Canonical principal id that a matching token authenticates as |
| `actor_id` | string | Omitted when the agent is not registered. Canonical actor id of the agent. |
| `warnings` | array of string | Omitted unless a write succeeded with an acknowledged overlap |

Derived fields are recomputed by the server on every write. A request that supplies `issuer`, `subject` or another derived field is rejected.

## GET `/api/v1/agents/{clientId}/identities`

Returns a JSON array of binding views for the agent in the tenant.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 500 | `RESOURCE_ERROR` | Binding store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/agents/tutor-agent/identities" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/agents/{clientId}/identities/{id}`

Returns one binding view with an `ETag` header holding the revision in quotes.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Binding returned |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No such binding in this tenant |
| 500 | `RESOURCE_ERROR` | Binding store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/agents/tutor-agent/identities/worker" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## PUT `/api/v1/agents/{clientId}/identities/{id}`

Creates or replaces a binding. The body is the request part of a binding, at most 64 KiB. Unknown fields are rejected.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `preset` | string | Yes | One of the preset identifiers |
| `inputs` | object of string | Yes, as required by the preset | Operator inputs. Each name must be defined by the preset. Each value must be a single bounded value without surrounding whitespace, and must be one of the preset's `enum` values when it has one. |
| `required_claims` | object of string | No | Extra exact-match claims. Only allowed when the preset sets `allows_required_claims`. At most 16. Names may not be `issuer`, `subject`, `audience` or lifetime claims, and may not conflict with a derived value. |
| `display_name` | string | No | At most 256 characters |
| `disabled` | boolean | No | Store the binding disabled |
| `acknowledge_overlap` | boolean | No | Save the binding even though a user identity provider of the tenant trusts the same issuer and audience. Not stored: every write that keeps the overlap must send it again. |

Without `If-Match`, the request creates the binding. With `If-Match`, it replaces the binding at that revision. The agent must already be registered and active.

A successful create returns `201 Created`. A successful replace returns `200 OK`. Both return the binding view with an `ETag` header.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Replaced. Body is the binding view. |
| 201 | none | Created. Body is the binding view. |
| 400 | `INVALID_REVISION` | `If-Match` is not a revision number |
| 400 | `INVALID_BINDING` | Body is not valid JSON for the request, has unknown fields, is over 64 KiB, has an invalid binding id, display name or agent id, or fails preset derivation. Also returned when the JWKS URL is rejected by the outbound endpoint policy. |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `ROLE_REQUIRED` | Role is not `admin` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `AGENT_NOT_FOUND` | The agent is not registered. Register it first. |
| 404 | `NOT_FOUND` | Store reported the binding missing |
| 409 | `AGENT_NOT_ACTIVE` | The agent registration is not active |
| 409 | `SUBJECT_ALREADY_BOUND` | The external subject is already bound to another binding. The message names the owning agent and binding. |
| 409 | `IDENTITY_OVERLAPS_USER_PROVIDER` | A tenant user identity provider trusts the same issuer and audience. Send `acknowledge_overlap` to save anyway. `details.conflict` holds `kind`, `id`, `issuer` and `audience` of the conflicting entry, and `details.acknowledge_with` is `acknowledge_overlap`. |
| 409 | `REVISION_CONFLICT` | `If-Match` is missing on a replace of an existing binding, or does not match the current revision |
| 500 | `RESOURCE_ERROR` | Store or user identity provider store unavailable |
| 503 | `AGENT_REGISTRY_UNAVAILABLE` | Agent registry unavailable |

### Example

```bash
curl -sS -X PUT "$AIPLEX_URL/api/v1/agents/tutor-agent/identities/worker" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"preset":"oidc","display_name":"External worker","inputs":{"issuer":"https://issuer.example.com","jwks_url":"https://issuer.example.com/jwks","subject":"workload-7"},"required_claims":{"team":"research"}}'
```

```json
{
  "id": "worker",
  "client_id": "tutor-agent",
  "preset": "oidc",
  "inputs": {"issuer": "https://issuer.example.com", "jwks_url": "https://issuer.example.com/jwks", "subject": "workload-7"},
  "display_name": "External worker",
  "disabled": false,
  "issuer": "https://issuer.example.com",
  "jwks_url": "https://issuer.example.com/jwks",
  "audience": "https://aiplex.example.com",
  "subject_claim": "sub",
  "subject": "workload-7",
  "required_claims": {"team": "research"},
  "revision": 1,
  "principal_id": "example-principal-id",
  "actor_id": "example-actor-id"
}
```

## DELETE `/api/v1/agents/{clientId}/identities/{id}`

Deletes a binding. `If-Match` is required and must carry the current revision. On success the response has no body.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 204 | none | Deleted |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `ROLE_REQUIRED` | Role is not `admin` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No such binding |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `REVISION_REQUIRED` | `If-Match` missing, zero, or not a revision number |
| 500 | `RESOURCE_ERROR` | Store unavailable |

### Example

```bash
curl -sS -X DELETE "$AIPLEX_URL/api/v1/agents/tutor-agent/identities/worker" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H 'If-Match: "1"'
```

## POST `/api/v1/agents/{clientId}/identities/{id}/plan`

Renders the setup plan for one binding. The request has no body. The plan is text for an operator to review. Nothing is provisioned on the cloud side.

### Response

| Field | Type | Meaning |
|---|---|---|
| `binding` | object | `client_id`, `id` and `preset` |
| `cloud` | string | Cloud or platform |
| `issuer` | string | Issuer the token must carry |
| `audience` | string | Audience the workload requests |
| `subject` | string | Subject the binding pins |
| `steps` | array of step | Cloud-side steps |
| `token_snippet` | step | A step that shows how the token is obtained |
| `aiplex_steps` | array of step | Steps on the AIPlex side, such as registering the identity |
| `notes` | array of string | Omitted when empty |

A step has `title`, `description`, and optionally `language` (`bash`, `yaml`, `json` or empty for prose) and `content`.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Plan returned |
| 400 | `INVALID_PLAN` | The binding cannot be rendered |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No such binding |
| 500 | `RESOURCE_ERROR` | Store unavailable |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/agents/tutor-agent/identities/worker/plan" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## POST /api/v1/agents/identities/verify

Runs a dry-run of a presented token against the tenant's bindings. It reports each check, in order, and whether the token would authenticate.

### Request body

The body is at most 80 KiB. Unknown fields are rejected.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `token` | string | Yes | The token to evaluate. It is used only in memory. |
| `client_id` | string | With `binding_id` | Pins the evaluation to one binding |
| `binding_id` | string | With `client_id` | Pins the evaluation to one binding |

`client_id` and `binding_id` must be supplied together.

### Response

| Field | Type | Meaning |
|---|---|---|
| `valid` | boolean | `true` only when every check passed |
| `checks` | array of check | Every check, in order |
| `issuer` | string | Omitted when empty. Issuer of the token. |
| `subject` | string | Omitted when empty. Subject of the token. |
| `audience` | array of string | Omitted when empty |
| `binding` | object | Omitted when no binding matched. `client_id`, `id` and `preset`. |
| `actor_subject` | string | Omitted when empty. Agent actor subject the token authenticates as. |
| `principal_id` | string | Omitted unless the token matched a binding |
| `actor_id` | string | Omitted unless the token matched a binding |
| `live_authentication` | boolean | Whether bindings authenticate live requests |

A check has `name`, `status` (`pass`, `fail` or `skipped`) and `reason`. The checks are, in order: `token_shape`, `issuer`, `signature`, `audience`, `lifetime`, `subject`, `required_claims`, `proof_of_possession`, `binding`. A failed check skips every later check. An extra check, `federation_overlap`, is appended when the token is also claimed by a user identity provider of the tenant. A token claimed by both kinds is rejected.

The response never contains the token, its claims or its signature. Claims are kept in memory and are never serialised. The reasons do not echo the token. Malformed bodies return a message that does not contain the body.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Evaluation completed. A failed token is still `200` with `valid` set to `false`. |
| 400 | `INVALID_REQUEST` | Body is not valid for this request, `client_id` and `binding_id` are not supplied together, or the body is over 80 KiB |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | A pinned binding does not exist |
| 500 | `RESOURCE_ERROR` | Store unavailable |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/agents/identities/verify" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"token":"<token-to-check>","client_id":"tutor-agent","binding_id":"worker"}'
```

```json
{
  "valid": true,
  "checks": [
    {"name": "token_shape", "status": "pass", "reason": "example"},
    {"name": "issuer", "status": "pass", "reason": "example"},
    {"name": "signature", "status": "pass", "reason": "example"},
    {"name": "audience", "status": "pass", "reason": "example"},
    {"name": "lifetime", "status": "pass", "reason": "example"},
    {"name": "subject", "status": "pass", "reason": "example"},
    {"name": "required_claims", "status": "pass", "reason": "example"},
    {"name": "proof_of_possession", "status": "pass", "reason": "example"},
    {"name": "binding", "status": "pass", "reason": "example"}
  ],
  "issuer": "https://issuer.example.com",
  "subject": "workload-7",
  "binding": {"client_id": "tutor-agent", "id": "worker", "preset": "oidc"},
  "principal_id": "example-principal-id",
  "actor_id": "example-actor-id",
  "live_authentication": false
}
```

## Behaviour notes

- **The token is never returned.** The verify response has no token field, claim value or signature. The token is cleared from the request after evaluation, and the server's logs must not contain it.
- **Subjects are unique per tenant.** Each external subject (issuer, subject claim and subject) can be bound to one agent binding in a tenant. A second binding for the same subject returns `SUBJECT_ALREADY_BOUND`. Another tenant may bind the same subject independently.
- **Revisions.** Each replace and delete uses `If-Match`. Creating over an existing binding without `If-Match` returns `REVISION_CONFLICT`. The `ETag` after a write is the new revision.
- **Overlap with user providers.** A binding and a user identity provider that trust the same issuer and audience overlap. A write that overlaps is refused unless `acknowledge_overlap` is sent. Live authentication rejects a token that both kinds claim.
- **Live authentication** is off unless the server enables it. When it is off, bindings are used only for dry-runs and plans. The `live_authentication` field reports the mode.
- **Derived trust is server-side.** Presets derive the trust tuple deterministically from the inputs. Stored bindings always record the audience explicitly, even when a default was used.
- **Plans are advisory.** A plan describes steps for an operator. Nothing in the API changes cloud-side trust.
- **Tenant isolation.** A binding in another tenant returns `404 NOT_FOUND`, and a token verified in another tenant does not match.

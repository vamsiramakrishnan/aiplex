---
title: Workforce User Identity API
---

The workforce user identity API configures the identity providers that sign in the tenant's people: Google Workforce Identity Federation, Google Workspace, Microsoft Entra ID, Okta and generic OIDC. An operator chooses a preset, supplies a few inputs, and the server derives an exact trust tuple: issuer, JWKS location, audience, required claims and where groups come from. The API renders a setup plan for the identity provider, and it runs a dry-run verification of a presented token. When a provider has a `directory` block, the API can also resolve and invalidate cached directory group membership. The verify endpoint never returns the token.

Providers are tenant-scoped. A provider in one tenant is invisible in another. Writes require the `admin` role. Listing, reading, planning and dry-runs require `admin` or `deployer`. The preset catalogue is readable by any verified caller of the tenant.

## Routes

| Method | Path | Purpose | Authorization |
|---|---|---|---|
| GET | `/api/v1/identity/user-provider-presets` | Preset catalogue | Any verified identity |
| GET | `/api/v1/identity/user-providers` | List the tenant's providers | `admin` or `deployer` |
| GET | `/api/v1/identity/user-providers/{id}` | Read one provider | `admin` or `deployer` |
| PUT | `/api/v1/identity/user-providers/{id}` | Create or replace a provider | `admin` role |
| DELETE | `/api/v1/identity/user-providers/{id}` | Delete a provider. `If-Match` required. | `admin` role |
| POST | `/api/v1/identity/user-providers/{id}/plan` | Render the identity provider setup plan | `admin` or `deployer` |
| POST | `/api/v1/identity/user-providers/verify` | Dry-run a presented token | `admin` or `deployer` |
| POST | `/api/v1/identity/user-providers/{id}/membership/invalidate` | Drop the cached directory membership of one subject | `admin` role |

The provider identifier `verify` is reserved for the dry-run route. Every route is under `/api/v1` and requires a verified identity. Outside development mode, a caller without the required role receives `403 FORBIDDEN`. A caller with no verified role context receives `401 UNAUTHENTICATED`. The tenant must be bound. Otherwise the response is `403 TENANT_REQUIRED`.

Errors use this shape:

```json
{
  "code": "INVALID_PROVIDER",
  "message": "provider id must be a lower-case DNS label of at most 64 characters (and not \"verify\")",
  "request_id": "req-example"
}
```

## GET /api/v1/identity/user-provider-presets

Returns the five presets and whether live authentication through providers is enabled.

| Field | Type | Meaning |
|---|---|---|
| `presets` | array of preset | The catalogue |
| `live_authentication` | boolean | `true` when providers authenticate live requests |

The preset identifiers are `google-workforce-pool`, `google-workspace`, `entra-id`, `okta` and `oidc`.

Each preset has `id`, `display_name`, `vendor`, `description`, `inputs`, `issuer_shape`, `subject_shape`, `allows_required_claims`, `algorithms`, `group_sources`, and optionally `tenant_pinning`, `notes`, `operator_supplied`, `client_claim_default` and `directory_kinds`. Each input has `name`, `label`, `description`, `required`, and optionally `example`, `enum` and `default`.

`directory_kinds` names the directory kinds that the preset accepts. `entra-id` accepts `entra-graph`, `google-workspace` accepts `google-cloud-identity`, and `okta` accepts `okta`. The other presets accept no directory.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Catalogue returned |
| 401 | `UNAUTHENTICATED` | No verified identity outside development mode |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/identity/user-provider-presets" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## Provider request body

`PUT` takes this body, at most 64 KiB. Unknown fields are rejected.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `preset` | string | Yes | One of the preset identifiers |
| `inputs` | object of string | As the preset requires | Operator inputs. Names must be defined by the preset. The `issuer` must be an HTTPS URL, and the `audience` must be an exact value with no wildcard. Values must be single bounded values with no surrounding whitespace, and must be one of the preset's `enum` values when it has one. |
| `required_claims` | object of string | No | Extra exact-match claims, when the preset allows them. At most 16. Reserved claim names are refused. |
| `display_name` | string | No | At most 256 characters |
| `disabled` | boolean | No | Store the provider disabled |
| `directory` | object | No | Group resolution from the identity provider's directory. See Directory block. |
| `client_claim` | string | No | Overrides the preset's default client claim. `none` disables it. |
| `client_ids` | array of string | No | Accepts tokens issued only to these client applications |
| `acknowledge_overlap` | boolean | No | Save the provider even though an agent identity binding trusts the same issuer and audience. Not stored: every write that keeps the overlap must send it again. |

Derived fields are recomputed on the server from the preset and inputs. Stored `issuer`, `jwks_url`, `audience` and claim fields cannot be set directly.

The `groups_from` input chooses where groups come from. `claim` reads the groups from a token claim named by `group_claim`. `membership-authority` takes the groups only from the directory, so it requires a `directory` block.

### Directory block

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | `entra-graph`, `google-cloud-identity` or `okta`. Must be a kind that the preset accepts. |
| `mode` | string | `fallback` or `always`. `fallback` queries the directory only when the token carries no usable group view. `always` makes the directory the only group authority. When `groups_from` is `membership-authority`, the mode is always `always`, and `fallback` is refused. |
| `credential` | object | References only, never secret values. `client_id`, `client_secret_ref`, `identity_ref`, `api_token_ref`, `private_key_ref` and `key_id`, depending on the kind. |
| `org_url` | string | Okta organisation URL. Explicit. Not derived from the issuer. |
| `group_id` | string | Okta only. `id` (immutable group id) or `name` (the name carried by Okta's default groups claim). An Okta directory in `fallback` mode must set it. |

The directory kind must match the preset, and an `entra-graph` directory requires the pinned `tid`. A `google-cloud-identity` directory requires the pinned `hd`.

## GET /api/v1/identity/user-providers

Returns a JSON array of provider views for the tenant.

### Provider view

The provider fields are:

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Provider identifier |
| `preset` | string | Preset identifier |
| `inputs` | object of string | Operator inputs, with empty values removed |
| `display_name` | string | Omitted when empty |
| `disabled` | boolean | Disabled providers never authenticate |
| `issuer` | string | Derived. Exact `iss` the token must carry. |
| `jwks_url` | string | Derived. Where the signing keys are fetched. |
| `audience` | string | Derived. Exact `aud`. |
| `subject_claim` | string | Derived. Claim that holds the subject. |
| `required_claims` | object of string | Omitted when empty |
| `groups_from` | string | `claim` or `membership-authority` |
| `group_claim` | string | Omitted when empty |
| `client_claim` | string | Omitted when empty |
| `client_ids` | array of string | Omitted when empty |
| `directory` | object | Omitted when there is no directory block. The stored, normalised block. |
| `extra_claims` | object of string | Omitted when empty. Operator-added required claims. |
| `revision` | integer | Revision. Equal to the `ETag` value on reads. |
| `principal_pool` | string | Pool that scopes the principal ids of this tenant's users |
| `warnings` | array of string | Omitted unless a write succeeded with an acknowledged overlap |

Principal ids are the canonical id of the issuer, the principal pool and the subject. Role bindings scope to the pool.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Array returned. An empty tenant returns `[]`. |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 500 | `RESOURCE_ERROR` | Provider store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/identity/user-providers" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## GET `/api/v1/identity/user-providers/{id}`

Returns one provider view with an `ETag` header holding the revision in quotes.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Provider returned |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No such provider in this tenant |
| 500 | `RESOURCE_ERROR` | Provider store unavailable |

### Example

```bash
curl -sS "$AIPLEX_URL/api/v1/identity/user-providers/corp" -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## PUT `/api/v1/identity/user-providers/{id}`

Creates or replaces a provider. Without `If-Match` it creates, and the response is `201 Created`. With `If-Match` it replaces the provider at that revision, and the response is `200 OK`. The response is the provider view with an `ETag` header.

A new provider's `issuer` and `audience` pair may be used by only one provider in the tenant. Writes that overlap an agent identity binding are refused unless `acknowledge_overlap` is sent.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Replaced |
| 201 | none | Created |
| 400 | `INVALID_REVISION` | `If-Match` is not a revision number |
| 400 | `INVALID_PROVIDER` | Body is invalid JSON for this request, has unknown fields or is over 64 KiB. Also returned for an invalid id, an unknown preset, invalid inputs, an invalid directory block, or a JWKS URL refused by the outbound endpoint policy. |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `ROLE_REQUIRED` | Role is not `admin` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 409 | `SOURCE_ALREADY_CONFIGURED` | Another provider in the tenant already trusts this issuer and audience. The message names it. |
| 409 | `IDENTITY_OVERLAPS_AGENT_BINDING` | An agent identity binding trusts the same issuer and audience. Send `acknowledge_overlap` to save anyway. `details.conflict` names the binding. |
| 409 | `REVISION_CONFLICT` | `If-Match` is missing on a replace, or does not match the current revision |
| 500 | `RESOURCE_ERROR` | Store unavailable |

### Example

```bash
curl -sS -X PUT "$AIPLEX_URL/api/v1/identity/user-providers/corp" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"preset":"oidc","display_name":"Corporate SSO","inputs":{"issuer":"https://idp.example.com","jwks_url":"https://idp.example.com/jwks","audience":"aiplex","groups_from":"claim","group_claim":"groups"},"required_claims":{"org":"example"}}'
```

## DELETE `/api/v1/identity/user-providers/{id}`

Deletes a provider. `If-Match` is required and must carry the current revision. On success the response has no body.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 204 | none | Deleted |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `ROLE_REQUIRED` | Role is not `admin` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No such provider |
| 409 | `REVISION_CONFLICT` | `If-Match` does not match the current revision |
| 428 | `REVISION_REQUIRED` | `If-Match` missing, zero, or not a revision number |
| 500 | `RESOURCE_ERROR` | Store unavailable |

### Example

```bash
curl -sS -X DELETE "$AIPLEX_URL/api/v1/identity/user-providers/corp" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H 'If-Match: "2"'
```

## POST `/api/v1/identity/user-providers/{id}/plan`

Renders the setup plan for one provider. The request has no body. The plan describes steps for an operator to review. Nothing is provisioned.

### Response

| Field | Type | Meaning |
|---|---|---|
| `provider` | object | `id` and `preset` |
| `vendor` | string | Identity vendor |
| `issuer` | string | Issuer the token must carry |
| `audience` | string | Audience the client requests |
| `subject_claim` | string | Claim that holds the subject |
| `groups_from` | string | `claim` or `membership-authority` |
| `group_claim` | string | Omitted when empty |
| `directory` | object | Omitted when there is no directory block. `kind`, `mode` and `source`. |
| `client_claim` | string | Omitted when empty |
| `client_ids` | array of string | Omitted when empty |
| `principal_pool` | string | Pool the principal ids are scoped to |
| `steps` | array of step | Identity provider steps |
| `token_snippet` | step | How the token is obtained |
| `aiplex_steps` | array of step | AIPlex-side steps |
| `notes` | array of string | Omitted when empty |

A step has `title`, `description`, and optionally `language` and `content`. The plan refers to directory secrets by the deployment secret name derived from the tenant's secret reference, and the plan includes the `POST .../membership/invalidate` step when a directory is configured.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Plan returned |
| 400 | `INVALID_PLAN` | The provider cannot be rendered |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No such provider |
| 500 | `RESOURCE_ERROR` | Store unavailable |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/identity/user-providers/corp/plan" \
  -H "Authorization: Bearer $AIPLEX_TOKEN"
```

## POST /api/v1/identity/user-providers/verify

Runs a dry-run of a presented token against the tenant's providers. It reports each check in order and whether the token would authenticate as a workforce principal.

### Request body

At most 80 KiB. Unknown fields are rejected.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `token` | string | Yes | The token. It is used only in memory. |
| `provider_id` | string | No | Pins the evaluation to one provider. Without it, the provider is selected by the token's issuer. |

### Response

| Field | Type | Meaning |
|---|---|---|
| `valid` | boolean | `true` only when every check passed |
| `checks` | array of check | Every check, in order |
| `issuer` | string | Omitted when empty |
| `subject` | string | Omitted when empty |
| `audience` | array of string | Omitted when empty |
| `provider` | object | Omitted when no provider matched. `id` and `preset`. |
| `groups_available` | boolean | `true` only for a valid token whose group claim was well formed |
| `groups` | array of string | Omitted when groups are unavailable |
| `groups_note` | string | Omitted when empty. Says why groups are unavailable. |
| `groups_source` | string | `token`, `directory:<kind>` or `unavailable`. Omitted when empty. |
| `client_id` | string | Omitted when empty. The verified client application. |
| `principal_id` | string | Omitted unless the token matched a provider |
| `principal_pool` | string | Omitted unless the token matched a provider |
| `is_workforce` | boolean | `true` when the token matched a provider |
| `live_authentication` | boolean | Whether providers authenticate live requests |

A check has `name`, `status` (`pass`, `fail` or `skipped`) and `reason`. The checks are, in order: `token_shape`, `issuer`, `signature`, `audience`, `lifetime`, `subject`, `required_claims`, `proof_of_possession`, `client`, `groups`, `provider`. A failed check skips the later checks. A token that is also claimed by an agent identity binding of the tenant adds a `federation_overlap` failure, and the result is not valid, with no groups and no client.

The response never contains the token, its claims or its signature.

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Evaluation completed. A failed token is still `200` with `valid` set to `false`. |
| 400 | `INVALID_REQUEST` | Body is not valid for this request, or is over 80 KiB. The message does not echo the body. |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `ROLE_REQUIRED` | Role is neither `admin` nor `deployer` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | A pinned `provider_id` does not exist |
| 500 | `RESOURCE_ERROR` | Store unavailable |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/identity/user-providers/verify" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"token":"<token-to-check>","provider_id":"corp"}'
```

## POST `/api/v1/identity/user-providers/{id}/membership/invalidate`

Forces the next lookup of one subject's directory groups to observe the directory again, no earlier than now. The directory cache is held by each server process. Call every replica, or wait at most the cache TTL.

The body is `{"subject": "<verified subject claim value>"}`. The subject is the verified `oid` for Entra and the verified `sub` otherwise, and it must be non-empty, bounded, and free of surrounding whitespace. The body is at most 4 KiB.

### Response

| Field | Type | Meaning |
|---|---|---|
| `principal_id` | string | Canonical principal id that was invalidated |
| `not_before` | RFC 3339 timestamp | Directory observations from this instant on are used |
| `scope` | string | `this server process` |

### Status codes and errors

| Status | Code | When |
|---|---|---|
| 200 | none | Invalidated |
| 400 | `INVALID_REQUEST` | Body is invalid, or `subject` is missing or not valid text |
| 401 | `UNAUTHENTICATED` | No verified role context outside development mode |
| 403 | `ROLE_REQUIRED` | Role is not `admin` |
| 403 | `TENANT_REQUIRED` | No verified tenant binding |
| 404 | `NOT_FOUND` | No such provider |
| 409 | `DIRECTORY_NOT_CONFIGURED` | The provider has no `directory` block, so there is no cached membership |
| 500 | `RESOURCE_ERROR` | Store unavailable |
| 503 | `DIRECTORY_UNAVAILABLE` | Directory lookups are not configured on this server |
| 503 | `INVALIDATION_FAILED` | The cache could not be invalidated. The cached view expires within the cache TTL. |

### Example

```bash
curl -sS -X POST "$AIPLEX_URL/api/v1/identity/user-providers/corp/membership/invalidate" \
  -H "Authorization: Bearer $AIPLEX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"subject":"subject-example"}'
```

## Behaviour notes

- **Tokens are never returned.** The dry-run response has no token field, claim value or signature. The token is cleared after evaluation, and the server's logs must not contain it.
- **Derived trust.** Presets derive the trust tuple from the inputs. Stored providers always record the audience explicitly.
- **One provider per issuer and audience.** A tenant may have only one provider for each issuer and audience pair. Another tenant may have its own.
- **Overlap with agent bindings.** A provider and an agent identity binding that trust the same issuer and audience overlap. Live authentication rejects a token that both kinds claim.
- **Groups.** Groups come from the token claim, the directory, or both, depending on `groups_from` and the directory `mode`. `groups_available` is `false` when no group view can be verified, and the dry-run reports that as `unavailable` rather than as an empty list. An empty list is a verified answer, and absence is unknown.
- **Live authentication** is off unless the server enables it. The `live_authentication` field reports the mode.
- **Plans are advisory.** A plan describes steps for an operator. Nothing in the API changes identity-provider-side configuration.
- **Tenant isolation.** A provider in another tenant returns `404 NOT_FOUND`, and a token verified in another tenant does not match.
- **Directory credentials** are referenced by name. The API stores references and never secret values.

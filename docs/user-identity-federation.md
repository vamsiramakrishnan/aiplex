# User identity federation

User identity federation lets people sign in to AIPlex with a token from their own
identity provider: Google Workspace, Microsoft Entra ID, Okta, a Google Workforce
pool, or any OIDC issuer that signs RS256 or ES256 tokens. A user identity provider
pins the issuer, audience, subject claim and group source. A verified user gets a
canonical principal ID and, when groups are verified, a group view that role
bindings and quota eligibility can use.

The design is in [access points and the single pane](design/2026-11-09-access-points-single-pane.md),
section 2. Membership sources for quota are described in [membership authority](membership-authority.md).
Agent workloads use the parallel mechanism in [agent identity federation](agent-identity-federation.md).
A provider grants no scopes by itself. Access comes from the normal role bindings
and grants.

## Routes

All routes are under `/api/v1`.

| Route | Purpose | Role |
| --- | --- | --- |
| `GET /identity/user-provider-presets` | Preset catalog with input fields. | Any verified caller |
| `GET /identity/user-providers` | List providers. | Admin or deployer |
| `GET /identity/user-providers/{id}` | One provider, with its revision. | Admin or deployer |
| `PUT /identity/user-providers/{id}` | Create or replace. Updates send the current revision in `If-Match`. | Admin |
| `DELETE /identity/user-providers/{id}` | Delete. Send the current revision in `If-Match`. | Admin |
| `POST /identity/user-providers/{id}/plan` | Setup steps for the identity provider, rendered as text. | Admin or deployer |
| `POST /identity/user-providers/verify` | Dry run. Body: `token`, optional `provider_id`. The response never contains the token. | Admin or deployer |
| `POST /identity/user-providers/{id}/membership/invalidate` | Drop one user's cached directory groups on this server process. Body: `{"subject": "<verified subject>"}`. | Admin |

The `subject` is the provider's verified subject claim: `oid` for Entra ID and `sub`
otherwise. The invalidate route answers `409 DIRECTORY_NOT_CONFIGURED` when the
provider has no `directory` block.

## Provider request

| Field | Meaning |
| --- | --- |
| `preset` | One of the preset IDs below. |
| `inputs` | Preset inputs as strings. Unknown inputs are rejected. |
| `required_claims` | Extra exact-match claims. Reserved names (`iss`, `sub`, `aud`, `exp`, `nbf`, `iat`, `jti`, `act`, `azp`, `cnf`) are refused. |
| `directory` | Optional group resolution from the provider's own directory. See below. |
| `client_claim` | The verified claim that names the calling OAuth client. Overrides the preset default. `none` disables it. |
| `client_ids` | Exhaustive list of client IDs whose tokens are accepted. A token without a usable client claim is rejected when this list is set. |
| `display_name`, `disabled` | Label, and whether the provider is kept but does not authenticate. |
| `acknowledge_overlap` | Saves a provider that overlaps an agent identity binding. Not stored. |

The server recomputes the trust tuple from `preset` and `inputs` on every write.

## Presets

Every preset requires `audience`, which has no default. It is the exact `aud` the
tokens carry. Presets that read groups from a claim require `groups_from`, with no
default. The value is `claim` (read `group_claim` from the verified token) or
`membership-authority` (ignore token groups and use the configured membership
authority or the directory).

| Preset | Issuer | Subject | Inputs | Directory kind |
| --- | --- | --- | --- | --- |
| `google-workforce-pool` | Operator input (`issuer`) | `sub` | `issuer` (required), `jwks_url` (required), `pool_id` (optional, documentation only), `audience`, `groups_from`, `group_claim` (required with `claim`) | None |
| `google-workspace` | `https://accounts.google.com` | `sub` | `hosted_domain` (required, pinned as `hd`), `audience` (the OAuth client ID) | `google-cloud-identity` |
| `entra-id` | `https://login.microsoftonline.com/{tenant_id}/v2.0`, or `https://sts.windows.net/{tenant_id}/` for `token_version` `1.0` | `oid`, with `tid` pinned to the tenant | `tenant_id` (required), `audience`, `token_version` (`2.0` default), `groups_from`, `group_claim` (default `groups`) | `entra-graph` |
| `okta` | Operator input (`issuer`) | `sub` | `issuer` (required), `jwks_url` (required), `audience`, `groups_from`, `group_claim` (default `groups`) | `okta` |
| `oidc` | Operator input (`issuer`) | `sub`, or `oid` with `subject_claim` | `issuer`, `jwks_url`, `subject_claim` (`sub` default, or `oid`), `audience`, `groups_from`, `group_claim` | None |

Preset defaults for the client claim: `entra-id` reads `azp` for v2.0 tokens and
`appid` for v1.0 tokens. `okta` reads `cid`. `google-workspace` and the other
presets have no default.

Notes that are enforced:

- `google-workspace` accepts only `groups_from: membership-authority`. Google ID tokens
  carry no group claim.
- `google-workforce-pool` has no default issuer or JWKS URL. AIPlex does not publish
  either, so both are operator inputs. Copy the issuer from a decoded token or the
  issuer's discovery document.
- The workforce preset also has no directory kind.

## The `directory` block

A `directory` block resolves a user's groups from the identity provider's directory
when the token does not carry a usable group view, or always, depending on `mode`.

```yaml
directory:
  kind: entra-graph          # entra-graph | google-cloud-identity | okta
  mode: fallback             # fallback (default) | always
  credential:
    client_id: 11112222-bbbb-3333-cccc-4444dddd5555
    client_secret_ref: entra-graph-secret
```

| Field | Meaning |
| --- | --- |
| `kind` | Directory type. Each preset accepts only its own kind. |
| `mode` | `fallback` queries the directory only when the verified token has no usable group view. `always` makes the directory the only group authority, and token groups are ignored even when present. |
| `credential` | References only. Never secret values. |
| `org_url` | Okta only. The Okta org origin, for example `https://acme.okta.com`. AIPlex does not derive it from the issuer. Required for `okta`. |
| `group_id` | Okta only. `id` (immutable group ID, the default) or `name`. In `fallback` mode it must be set. Okta's default groups claim carries names, so `name` matches it. |

Credentials by kind:

| Kind | Credential fields |
| --- | --- |
| `entra-graph` | `client_id` (a lower-case GUID of an app registration with an application permission that reads group membership) and `client_secret_ref` |
| `google-cloud-identity` | `identity_ref`, the name of a `gcp-adc` identity profile configured for the tenant |
| `okta` | `api_token_ref`, or `client_id` with `private_key_ref` and optional `key_id` for an OAuth service app |

Mode rules:

- With `groups_from: membership-authority`, the directory is the only group
  authority, so the mode is `always`. An explicit `fallback` is rejected.
- With `groups_from: claim`, the mode defaults to `fallback`.

Secret references resolve in the tenant's tool secret namespace: Secret Manager, or
`AIPLEX_TOOL_SECRET_<REF>` environment variables in development mode. Directory calls
go through the outbound endpoint policy. The vendor origins (for example
`https://graph.microsoft.com`, `https://login.microsoftonline.com`,
`https://cloudidentity.googleapis.com`, or the Okta org) must be allowed in the
connections file named by `AIPLEX_CONNECTIONS_FILE`.

## Worked example

`okta.yaml`:

```yaml
id: okta
preset: okta
display_name: Acme Okta
inputs:
  issuer: https://acme.okta.com/oauth2/default
  jwks_url: https://acme.okta.com/oauth2/default/v1/keys
  audience: api://aiplex
  groups_from: claim
  group_claim: groups
directory:
  kind: okta
  org_url: https://acme.okta.com
  group_id: name
  credential:
    api_token_ref: okta-api-token
```

```sh
aiplex identity user-providers presets
aiplex identity user-providers apply -f okta.yaml
aiplex identity user-providers plan okta
aiplex identity user-providers verify --provider okta --token-file token.txt
aiplex identity user-providers list
```

`apply` takes a JSON or YAML file and creates or replaces the provider. `verify`
reads the token from a file or from stdin (`--token-file -`) and never accepts it as
a flag. Use `--provider` to pin the check to one provider. The global `--token`
flag is your AIPlex API token, not the user token under test.

Okta does not include a groups claim by default. Configure a groups claim with a
filter on the authorization server, and request the `groups` scope. Otherwise
groups are unavailable and group-based access does not match.

## Enable live authentication

Providers, plans and dry runs are always mounted. Live authentication with
providers is off by default:

```sh
export AIPLEX_USER_FEDERATION=enabled
export AIPLEX_TENANT_ID=example-tenant    # required unless AIPLEX_DEV_MODE is on
export AIPLEX_USER_DIRECTORY_CACHE_TTL=1m # optional; default 1m, at most 5m
```

Any value other than `enabled`, `disabled` or unset stops startup. With `enabled`,
providers act as a fallback identity source behind the statically configured
sources. A token claimed by both a user identity provider and an agent identity
binding is rejected. Restart the API after changing these variables.

## Group view and failure behavior

A group view is either available or unavailable. There is no third state.

- **Available.** A well-formed list from the pinned claim, or a complete, expanded
  directory view. An authoritative empty list is available and empty.
- **Unavailable.** The claim is missing, null, malformed, has more than 1024
  entries, or is flagged as overage (Entra `hasgroups` or `_claim_names`). It is
  also unavailable when the directory lookup fails, returns an incomplete view, or
  is not consulted (for example, when no enabled provider matches the token). The
  reason is recorded with the verification result.

Unavailable is never treated as an empty list. Group-based role bindings and
access-point group constraints do not match when the group view is unavailable.
A directory failure therefore denies group-based access. It does not fall back to
an empty membership.

When a quota membership authority is configured (see [membership authority](membership-authority.md)),
it is the only source for quota eligibility. Directory groups are not used for quota
in that case. Role-binding resolution always uses the provider's group view.

The directory cache is process-local. A resolved view is reused for the TTL, which
defaults to one minute and cannot exceed five minutes. The cache holds up to 1024
subjects per provider. `membership/invalidate` fences one subject on the process that
receives the request. Run it on every replica, or wait for the TTL to expire.

## Limits

- Google Workspace tokens carry no group membership. Use `membership-authority`
  groups from a configured authority, or a `google-cloud-identity` directory with
  `groups_from: membership-authority`.
- Dry-run `verify` checks the provider's signature, issuer, audience, subject, client
  claim and group view. It does not prove that the user has any access.
- Provider JWKS URLs must pass the outbound endpoint policy when saved.
- Live authentication applies only to the tenant named by `AIPLEX_TENANT_ID`.
- Proof-of-possession (`cnf`) tokens are rejected. User tokens must be bearer tokens.
- Setup plans are rendered text. Nothing is provisioned in the identity provider.
- Directory credentials are references. Rotate the referenced secrets in the secret
  store. AIPlex does not write them.
- `acknowledge_overlap`, `directory`, `client_claim` and `client_ids` are top-level
  fields of the provider file. `identity user-providers apply` passes them through
  unchanged, so they work from YAML or JSON.

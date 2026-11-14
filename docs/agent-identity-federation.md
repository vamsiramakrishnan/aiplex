# Agent identity federation

Agent identity federation lets a workload running outside AIPlex authenticate as a
registered agent, using a token its own cloud or cluster issued. A binding ties
one external subject (for example a GitHub Actions ref, an AWS IAM role ARN or an
Azure managed identity object ID) to one registered agent client. Presets derive
the exact issuer, JWKS URL, audience, subject and required claims from a few
operator inputs, so no wildcard trust is written by hand. The verifier explains
each check in a dry run, which helps when a token is rejected.

Registered agents are described in [native agent governance](native-agent-governance.md).
A binding grants nothing by itself: after it verifies a token, the identity goes through
the normal role-binding and grant path.

## Routes

All routes are under `/api/v1` and require a verified identity. The preset catalog is
readable by any verified caller. Other reads, and the plan and dry-run routes, need the
admin or deployer role. Writes need the admin role.

| Route | Purpose |
| --- | --- |
| `GET /api/v1/agents/identity-presets` | Preset catalog with each preset's input fields. |
| `GET /api/v1/agents/{clientId}/identities` | List the bindings of one agent. |
| `GET /api/v1/agents/{clientId}/identities/{id}` | One binding, with its revision. |
| `PUT /api/v1/agents/{clientId}/identities/{id}` | Create or replace a binding. Updates send the current revision in `If-Match`. The agent must be active (`409 AGENT_NOT_ACTIVE` otherwise). |
| `DELETE /api/v1/agents/{clientId}/identities/{id}` | Delete a binding. Send the current revision in `If-Match`. |
| `POST /api/v1/agents/{clientId}/identities/{id}/plan` | Setup steps for the other cloud. The plan is rendered text for review. |
| `POST /api/v1/agents/identities/verify` | Dry run: checks a presented token against bindings. Body: `token`, and optionally `client_id` and `binding_id` (supply both or neither). Admin or deployer only. Limited to 20 requests per minute per caller. A caller over the limit gets `429 RATE_LIMITED`. |

The verify route never echoes the token. A malformed body is answered with a generic
400 that does not repeat the body. The limit is kept in each API process, so a deployment with
several replicas allows 20 per minute per replica.

## Binding request

| Field | Meaning |
| --- | --- |
| `preset` | One of the preset IDs below. |
| `inputs` | Preset inputs, as strings. Unknown inputs are rejected. |
| `required_claims` | Extra exact-match claims. Only presets that accept them allow this. Reserved names (`iss`, `sub`, `aud`, `exp`, `nbf`, `iat`, `jti`, `act`, `azp`, `cnf`) are refused. |
| `display_name`, `disabled` | Label, and whether the binding is kept but does not authenticate. |
| `acknowledge_overlap` | Saves a binding that overlaps a user identity provider. Not stored. See [user identity federation](user-identity-federation.md). |

The server recomputes the trust tuple from `preset` and `inputs` on every write. The
stored binding always records the explicit issuer, JWKS URL, audience and subject.

## Presets

No preset accepts wildcards. The `branchyard` preset (for Branchyard, a separate product that
runs coding agents on git branches; see [Branchyard](#branchyard)) accepts only EdDSA signatures; every
other preset accepts only RS256, ES256 and ES384. A binding's verifier accepts exactly its
preset's list, so an EdDSA token is refused by an `oidc` binding even when the issuer
publishes an Ed25519 key, and an RS256 token is refused by a `branchyard` binding. The
catalog reports each preset's list in `algorithms`.

Each algorithm is bound to one key type in the issuer's JWKS: RS256 to an RSA key of at
least 2048 bits, ES256 to an EC key on P-256, ES384 to an EC key on P-384 and EdDSA to an
OKP key on Ed25519. A token is checked only against the key its `kid` names, under the one
algorithm that key's type allows, so a key set that mixes RSA, EC and OKP keys works. A token
whose `alg` names a different algorithm than its key's type or curve is rejected, as are
`none`, HS*, PS*, RS384, ES512 and any spelling of EdDSA other than exactly `EdDSA`
(`Ed25519`, `EDDSA`, `eddsa`). EC signatures must be the raw `R||S` form of exactly 64 bytes
(ES256) or 96 bytes (ES384), and EdDSA signatures exactly 64 bytes; DER-encoded signatures
are rejected. A JWK whose `alg` contradicts its `kty` or `crv`, whose point is not on its
named curve, or whose coordinates are not full length is not loaded, so a token naming its
`kid` fails as an unknown key. For OKP keys that also covers a curve other than `Ed25519`
(`Ed448`, `X25519`), an `x` that is not exactly 32 bytes of unpadded base64url, a `y`, and a
private `d`: an OKP key that carries `d` is dropped from the set. RSA and EC keys that carry
`d` are loaded as they always were (their private part is ignored). Two keys with the same
`kid`, of any types, make the whole key set unusable.

This is what the AIPlex API verifies. The gateway authorizer `aiplex-authz` (the Envoy
`ext_authz` check on the gateway path) verifies RS256 only, so an ES256, ES384 or EdDSA
federated token is accepted by the API and refused by that authorizer.
For all presets except `azure-managed-identity`, a missing `audience` takes the
server's default audience. That default is `AIPLEX_AGENT_FEDERATION_AUDIENCE`, or
`PUBLIC_URL` when it is unset.

| Preset | Issuer | Subject pinned | Inputs |
| --- | --- | --- | --- |
| `gcp-service-account` | `https://accounts.google.com` | Numeric service account unique ID (`sub`) | `service_account_unique_id` (required), `service_account_email` (optional; adds an `email` claim requirement), `audience` |
| `gcp-agent-identity` | Google STS agent pool for an organization or project | Agent SPIFFE ID | `owner_type` (`organizations` or `projects`), `owner_number`, `agent_spiffe_id`, `audience` |
| `aws-sts-web-identity` | Account-specific issuer, copied from AWS (required input) | IAM role ARN (`sub`) | `issuer` (required), `role_arn` (required), `region` (optional), `audience` |
| `aws-eks-irsa` | `https://oidc.eks.{region}.amazonaws.com/id/{oidc_id}` | `system:serviceaccount:{namespace}:{service_account}` | `region`, `oidc_id`, `cluster_name` (optional, used in rendered commands only), `namespace`, `service_account`, `audience` |
| `kubernetes` | `issuer` from the cluster's discovery document | `system:serviceaccount:{namespace}:{service_account}` | `issuer`, `jwks_url`, `namespace`, `service_account`, `audience` |
| `azure-managed-identity` | `https://login.microsoftonline.com/{tenant_id}/v2.0`, or `https://sts.windows.net/{tenant_id}/` when `token_version` is `1.0` | Object (principal) ID, matched on `oid`. `tid` is pinned to the tenant. | `tenant_id`, `object_id`, `managed_identity_client_id` (optional, used in rendered commands only), `audience` (no default), `token_version` (`2.0` default, or `1.0`) |
| `github-actions` | `https://token.actions.githubusercontent.com` | `repo:{owner}/{repo}:environment:{env}` or `repo:{owner}/{repo}:ref:{ref}` | `repository`, exactly one of `environment` or `ref`, `repository_id` and `repository_owner_id` (optional, pinned as claims), `subject_format` (`name` default, or `immutable`, which requires both IDs), `audience` |
| `spiffe-oidc` | Explicit `issuer` of an OIDC discovery provider | Explicit SPIFFE ID | `issuer`, `jwks_url`, `spiffe_id`, `audience` |
| `oidc` | Explicit `issuer` | Explicit `subject` | `issuer`, `jwks_url`, `subject`, `audience` |
| `branchyard` | A Branchyard server (a separate product that runs coding agents on git branches): `server`, exactly (keys at `server` + `/.well-known/jwks.json`) | `branchyard:{Branchyard tenant}` (`sub`). `by_purpose` is pinned to `aiplex`; the token may live at most an hour. | `server` (required), `tenant` (the Branchyard tenant, required), `audience`. See [Branchyard](#branchyard). |

Input names are the ones in the preset catalog. Run `aiplex agents identity presets`
to see the field list and each field's description and example.

## Branchyard

Branchyard is a separate product that runs coding agents on git branches. A Branchyard server
gives each turn (one step of an agent on a branch) a model gateway, and that gateway can call
an AIPlex access point as one of its model backends. With `auth = "turn-token"` on that
backend, the gateway mints a turn token (a short-lived token for that turn only) and AIPlex
verifies it against the server's published keys, so no AIPlex secret is stored in Branchyard.
A Branchyard tenant is Branchyard's own grouping of its users and repositories; it is not an
AIPlex tenant, and the binding lives in the AIPlex tenant that creates it.

The turn token is a compact JWS with `alg` `EdDSA` and the `kid` of an Ed25519 key the server
publishes as `kty` `OKP` at `{iss}/.well-known/jwks.json`. A key in that set may carry
`alg: "EdDSA"` or no `alg`; any other `alg` drops the key. The token's claims are `iss` (the
server's issuer), `aud` (the backend's `audience`), `sub` (`branchyard:{Branchyard tenant}`),
`iat`, `exp`, `jti`, `by_purpose` (`aiplex`) and `by_tenant` (the Branchyard tenant),
`by_branch` and `by_turn`.

A `branchyard` binding derives the trust tuple from `server` and `tenant` (the Branchyard
tenant):

| Derived | Value |
| --- | --- |
| Issuer | `server`, compared byte for byte with `iss` |
| JWKS URL | `server` + `/.well-known/jwks.json` |
| Subject | `branchyard:` + the Branchyard tenant, in `sub` |
| Required claim | `by_purpose` = `aiplex`, a string |
| Audience | The `audience` input, or the server's default audience |
| Lifetime | A numeric `iat`, and `exp` at most 3600 seconds after it |
| Algorithms | `EdDSA` only |

`aud` may be a string or an array that contains the binding audience; that is what the code
accepts for every preset. Branchyard sends a string.

The lifetime rule enforces what the integration contract says Branchyard mints: `exp` at most
one hour after `iat`. A token without `iat`, with an `iat` that is not a number, or with
`exp - iat` over 3600 seconds is refused at the `lifetime` check, whose reason gives the limit.
Both values come from the token, so no clock skew applies. The limit is read from the preset
when a token is verified; nothing is stored with the binding, and no other preset has it.

`server` must be exactly `https://host` or `https://host:port`, as the server's tokens carry it:
no path, no trailing slash (`https://by.example.com/` is a different issuer), no query, fragment,
user information or percent escape. Plain `http` is accepted only on a loopback address, for a
development server (a Branchyard server without TLS on loopback has an issuer such as
`http://127.0.0.1:8421`). The outbound endpoint policy then decides, and it reaches loopback only
when development mode allows it, both when the binding is saved and when keys are fetched. No
other preset accepts an `http` issuer. The Branchyard tenant must be 1 to 128 of
`A-Z a-z 0-9 . _ @ + -`. Branchyard itself allows any Branchyard tenant name without `/` or
control characters; AIPlex accepts the subset with no whitespace, colon, wildcard or non-ASCII
character, so two Branchyard tenants' subjects cannot look alike. A Branchyard tenant outside it
cannot be federated. The preset accepts no other inputs (no `jwks_url`, `issuer` or `subject`)
and no additional required claims.

```sh
aiplex agents register acme-branchyard --name "Branchyard: acme"
aiplex agents identity bind acme-branchyard by --preset branchyard \
  --input server=https://branchyard.example.com \
  --input tenant=acme \
  --input audience=https://aiplex.example.com
aiplex agents identity plan acme-branchyard by    # the Branchyard backend configuration
```

A turn token from that server for the Branchyard tenant `acme` then authenticates as the agent
`acme-branchyard`, and as nothing else: another Branchyard tenant's token has another subject,
and a token whose `by_purpose` is missing or not the string `aiplex`, whose `aud`, `sub`, `exp`
or `iss` differs, or whose lifetime is over an hour is refused (`401` on a live request; the dry
run names the failed check). `by_branch` and `by_turn` are not checked. The binding grants
nothing by itself; grant the principal it yields as for any binding.

A Branchyard that runs on one machine without a server, whose issuer is `branchyard:local:{id}`
and whose keys are a file, cannot be federated: give its backend an AIPlex token instead.

Limits: in this repository the preset is tested with a stand-in issuer that signs tokens of the shape above
(`aiplex dev token --issuer branchyard`, see [local development](local-development.md#a-branchyard-turn-token)).
On 14 November 2026 it was also run against a real Branchyard server: `by serve` on loopback over plain http
minted the token, a development AIPlex fetched the server's key set and verified it, and a token for another
audience was refused ([Branchyard](branchyard.md#status-what-was-tested)). Not validated: an `https` issuer, a
Cloud KMS signing key on the Branchyard side, a Branchyard tenant other than its default, a deployed AIPlex. The gateway authorizer
`aiplex-authz` verifies RS256 only, so a turn token works on the managed access point path
(`/ap/{id}/...`) and not on the gateway path.

## Worked example

```sh
# Inspect the inputs for GitHub Actions
aiplex agents identity presets

# Bind a workflow on the main branch of one repository to an agent
aiplex agents identity bind tutor-agent gh-ci --preset github-actions \
  --input repository=acme/tutor \
  --input ref=refs/heads/main \
  --input audience=https://aiplex.example.com

# Show the setup steps to run in GitHub, then dry-run a token from that workflow
aiplex agents identity plan tutor-agent gh-ci
aiplex agents identity verify --agent tutor-agent --binding gh-ci --token-file token.txt
```

`verify` prints every check with its reason and exits non-zero when the token is not
valid. Read the token from a file or from stdin (`--token-file -`). It is never
accepted as a flag. The global `--token` flag is your AIPlex API token, not the
workload token under test.

The CLI prints each warning from the server's response after a successful write. An
acknowledged overlap is one such warning.

To change a binding, pass `--replace`. The CLI reads the current revision and sends it
as `If-Match`, so a concurrent edit is rejected rather than overwritten. Remove a
binding with `aiplex agents identity delete tutor-agent gh-ci`.

## Enable live authentication

The API, bindings, plans and dry runs are always mounted. Live authentication with
bindings is off by default. Turn it on with:

```sh
export AIPLEX_AGENT_FEDERATION=enabled
export AIPLEX_TENANT_ID=example-tenant   # required unless AIPLEX_DEV_MODE is on
export AIPLEX_AGENT_FEDERATION_AUDIENCE=https://aiplex.example.com   # optional default audience
```

`AIPLEX_AGENT_FEDERATION_AUDIENCE` sets the default audience that presets suggest. It is
read in `cmd/aiplex-api/agent_federation.go`. When it is unset, the default is the
server's `PUBLIC_URL` with any trailing slash removed. The default fills the `audience`
input of a new binding when the request leaves it empty, and the catalog returns it as the
input's default. It is not a live check: the binding stores the audience it derived, and
verification compares tokens with that stored value. Changing the variable does not change
existing bindings. The `azure-managed-identity` preset never uses this default, because its
audience must be set explicitly.

Any value other than `enabled` or `disabled` (or unset) stops startup. With
`enabled`, bindings act as a fallback identity source behind the statically
configured sources. A token claimed by both an agent binding and a user identity
provider is rejected. Restart the API after changing these variables.

## Limits

- Nothing is provisioned in the other cloud. Plans are text for an operator to run.
- Trust rests on the issuer's JWKS. JWKS URLs must pass the outbound endpoint policy
  when a binding is saved. Issuers must be `https`, except that a `branchyard` binding's
  issuer may be plain `http` on a loopback address, where the JWKS is on that same origin. A
  JWKS URL may be plain `http` only when its host is `localhost` or a loopback address. Even then the outbound endpoint policy refuses
  it unless the `network.allow_loopback` option is set in the connections file
  (`AIPLEX_CONNECTIONS_FILE`). The API refuses that option unless development mode is on
  (`AIPLEX_DEV_MODE`), and stops at startup otherwise.
- A token that matches more than one binding is rejected.
- Live authentication applies only to the tenant named by `AIPLEX_TENANT_ID`.
- The verified identity is rebuilt from the binding: issuer, subject, audience,
  bound client ID and actor come from the binding and the check. Only the lifetime
  claims (`exp`, `iat`, `nbf`) carry over from the external token. Groups, `hd`,
  `email` and delegation claims are dropped, so an external token cannot gain AIPlex
  roles by claim name.
- A certificate-bound token (one carrying `cnf`) is accepted only by the
  `gcp-agent-identity` preset.
- AWS `GetWebIdentityToken` can sign with ES384 or RS256, and AIPlex verifies both. The
  rendered token command requests ES384. ES384 verification is covered by tests with locally
  generated P-384 keys; it has not been checked against a live AWS account.
- `pull_request` subjects are not offered for GitHub Actions because every pull
  request, including ones from forks, shares them.
- An overlap with a user identity provider is refused with a 409 unless the write sends
  `acknowledge_overlap`. The CLI `--acknowledge-overlap` flag sends it, and the acknowledgement
  is not stored, so it must be repeated on every write that keeps the overlap.

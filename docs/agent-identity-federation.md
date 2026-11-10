# Agent identity federation

Agent identity federation lets a workload running outside AIPlex authenticate as a
registered agent, using a token its own cloud or cluster issued. A binding ties
one external subject (for example a GitHub Actions ref, an AWS IAM role ARN or an
Azure managed identity object ID) to one registered agent client. Presets derive
the exact issuer, JWKS URL, audience, subject and required claims from a few
operator inputs, so no wildcard trust is written by hand. The verifier explains
each check in a dry run, which helps when a token is rejected.

The design is in [usage ledger, agent identity and tool auth](design/2026-11-09-usage-ledger-agent-identity-tool-auth.md),
section 2. Registered agents are described in [native agent governance](native-agent-governance.md).
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
| `POST /api/v1/agents/identities/verify` | Dry run: checks a presented token against bindings. Body: `token`, and optionally `client_id` and `binding_id` (supply both or neither). |

The verify route never echoes the token. A malformed body is answered with a generic
400 that does not repeat the body.

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

Every preset accepts only RS256 and ES256 signatures and never accepts wildcards.
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

Input names are the ones in the preset catalog. Run `aiplex agents identity presets`
to see the field list and each field's description and example.

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
  when a binding is saved.
- A token that matches more than one binding is rejected.
- Live authentication applies only to the tenant named by `AIPLEX_TENANT_ID`.
- The verified identity is rebuilt from the binding: issuer, subject, audience,
  bound client ID and actor come from the binding and the check. Only the lifetime
  claims (`exp`, `iat`, `nbf`) carry over from the external token. Groups, `hd`,
  `email` and delegation claims are dropped, so an external token cannot gain AIPlex
  roles by claim name.
- A certificate-bound token (one carrying `cnf`) is accepted only by the
  `gcp-agent-identity` preset.
- AWS `GetWebIdentityToken` can sign with ES384 or RS256. AIPlex verifies only RS256
  and ES256, so request RS256 from AWS.
- `pull_request` subjects are not offered for GitHub Actions because every pull
  request, including ones from forks, shares them.
- The overlap acknowledgement (`acknowledge_overlap`) is available only in the API
  body. The CLI `agents identity bind` command has no flag for it, so a CLI write that
  overlaps a user identity provider returns a 409 that the CLI cannot acknowledge.
- The CLI help example for `aws-sts-web-identity` uses `account_id` and `role_name`
  inputs. The preset takes `issuer` and `role_arn`, so use those.

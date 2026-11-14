# Policy denials

Where authorization denials are recorded, how the gateway's authz sidecar
reports them, and where an operator reads them.

**Not validated against a live cluster.** The route, the sidecar client and the
Helm wiring are covered by unit tests and a mock HTTP server in the sidecar's
tests. The sidecar has not been run behind a real gateway, and the chart's
optional `authz` Deployment has only been rendered with `helm template`.

## What a denial is

A policy denial is a record that a verified caller asked for something its
grants do not cover. Two components write them:

- the API, when its own admission refuses a skill or tool call, and
- the authz sidecar (`authz/`, the Rust ext_authz service), when it refuses a
  request at the gateway before the API sees it.

They are stored together and read together. `GET /api/v1/dashboard/denials`
returns the 50 most recent by default; `?limit=` (1 to 1000) asks for a different
number, and a malformed or out-of-range value is refused with `400 INVALID_LIMIT`.
`aiplex list denials` prints them with the trace id
of the refused request; `GET /api/v1/dashboard/stats` reports
`policy_denials_24h`, the count of the last 24 hours. Denials are deployment-wide
operator data: reading them needs the `admin` role, and an identity bound to a
tenant is refused.

The Console's overview shows the usage ledger's `denied` count when it is
non-zero and `policy_denials_24h` otherwise. The ledger counts requests the API
itself refused with a record; the sidecar's denials never reach the API as a
request, so the two are disjoint, but the Console does not add them.

## Why reports did not arrive before

The sidecar posted to `DENIAL_WEBHOOK_URL`, which was meant to be the dashboard
route `POST /api/v1/dashboard/denials`. That route requires the `admin` role
and a verified identity, and the sidecar has neither, so every report was
refused with 401. There was also no way to give the sidecar a credential, and
the route took any body of any size.

## The internal route

`POST /internal/policy-denials` is the sidecar's route. It has the class and
guard style of the other sidecar and relay routes (`/internal/tape/events`,
`/auth/token-hook`):

- **Authentication:** a shared bearer secret. The API reads it from
  `AIPLEX_DENIAL_TOKEN` (a Kubernetes Secret reference in the chart); until it
  is set the route answers 503, never open. The sidecar reads the same secret
  from a mounted file (`DENIAL_TOKEN_FILE`), re-reading it every 30 seconds so a
  rotated Secret is picked up. No value appears in Helm values, environment
  files, logs or errors. The route is not published by the gateway's
  `api-route`.
- **Classification:** `RouteInternal` in `cmd/aiplex-api/route_policy.go` and
  `"POST /internal/policy-denials": "internal/t"` in the policy test, which
  also checks that the route refuses a caller without the secret.
- **Size and rate:** the body is limited to 4096 bytes (413 beyond); a per
  source-IP token bucket (`AIPLEX_DENIAL_RPS`, default 20/s, burst 100) and a
  handler-wide limit of 50/s with a burst of 200 answer 429.
- **Schema:** closed. Unknown fields, trailing data and control characters are
  refused with 400. `plane` is one of `mcplex`, `a2aplex`, `llmplex`,
  `skillsplex` or empty; `reason` is `scope_missing`, `budget_exceeded` or
  `rate_limited`; `agent_id`/`user_id` up to 256 characters (one of them
  required); `action` required, up to 512; `scope` up to 512; `request_id` up to
  128; `trace_id` empty or 32 lower-case hex digits that are not all zero. The
  server assigns `id` and `timestamp` and ignores a client's (the older admin
  route accepted them; `POST /api/v1/dashboard/denials` now shares this
  schema, still for `admin` only).
- **Tenant:** never taken from the payload. The shared secret identifies the
  sidecar as a component of this deployment, not as an agent of any tenant, and
  the sidecar's verified tokens carry no tenant claim, so there is nothing
  trustworthy to assert. A body with a `tenant_id` field is refused. Records are
  stored without a tenant and read through the admin route. Tenant-partitioned
  denials would need a verified tenant claim in the tokens and a tenant-keyed
  store; neither exists today.
- **Storage:** bounded. The in-memory store keeps the latest 5000. Firestore
  documents carry `expires_at` (30 days) and Terraform sets a TTL on that field
  (`google_firestore_field.policy_denials_ttl`); documents written before this
  change have no `expires_at` and stay until deleted.

## Why not the usage ledger

A denial is not added to the usage ledger. A ledger record is a request with
a principal, a tenant, a provider and model, usage and a cost, and the ledger is
partitioned by tenant. The sidecar's refusal has no provider, model or usage
(they would have to be recorded as unknown on every field that matters) and no
verified tenant to partition by. The API's own refusals already write
`status=denied` ledger records; adding the sidecar's would give those records a
second life in another place without a tenant. The denial store is the right
shape for gateway-level refusals.

## What the sidecar reports

For each denial of a token that passed signature verification:
`plane` (derived from the missing scope's prefix), `agent_id` (the token's
`azp`), `user_id` (its `sub`), `action` (the request path), `scope` (the first
missing scope), `reason` `scope_missing`, `request_id` (the inbound
`x-request-id`) and `trace_id` (the trace id of the inbound W3C `traceparent`,
empty when it is absent or invalid). Denials of tokens that fail verification
are not reported, so unauthenticated traffic cannot fill the store. Fields are
stripped of control characters and truncated to the limits above before
sending.

Reporting is best effort and never changes the authorization answer: at most
32 posts are in flight (more are dropped with a warning), the timeout is 2
seconds, redirects are not followed (a redirect would resend the token), and
failures are logged without the token or URL. With `DENIAL_WEBHOOK_URL` set but
no readable token file the reporter does not start and nothing is sent.

## Configure it

```yaml
# values.yaml (Helm)
denialIngest:
  tokenSecret:
    name: aiplex-denial-token   # kubectl create secret generic aiplex-denial-token --from-literal=token=...
authz:
  enabled: true
  jwt: {issuer: ..., audience: ..., jwksUrl: https://...}
  reportDenials: true
```

The chart sets `AIPLEX_DENIAL_TOKEN` on the API from the Secret and mounts the
same Secret into the sidecar with `DENIAL_WEBHOOK_URL` pointing at the API
Service. The chart's default data path is still OPA; `authz.enabled` only
deploys the Service, and your gateway's ext_authz filter has to be pointed at
it.

### Build and push the sidecar image

The release pipeline does not build the sidecar image yet (see
[limits](#limits)), so a deployment that sets `authz.enabled: true` builds and
pushes it itself. The chart pulls
`<global.registry>/aiplex-authz:<authz.image.tag>`, and the tag defaults to the
chart's `appVersion` (`0.1.0` in this repository), unless `authz.image.repository`
or `authz.image.tag` say otherwise. The image builds from `authz/Dockerfile`,
with `authz/` as the build context:

```bash
REGISTRY=us-central1-docker.pkg.dev/my-gcp-project/aiplex   # your values.yaml global.registry
TAG=0.1.0                                                   # authz.image.tag, or the chart appVersion
docker build -t "$REGISTRY/aiplex-authz:$TAG" authz/
docker push "$REGISTRY/aiplex-authz:$TAG"
```

The Dockerfile compiles `aiplex-authz` with `cargo build --release --locked` on
Alpine and runs it as `nobody` on port 9191. Push from a machine that can reach
your registry (for Artifact Registry, after `gcloud auth configure-docker
us-central1-docker.pkg.dev`). Nothing signs the image or scans it: that is
yours to add if your cluster's admission policy requires it.

## Limits

- **The authz sidecar image is not built by the release pipeline yet.** The
  release workflow and `.goreleaser.yaml` publish the API and CLI images and
  artifacts; there is no `aiplex-authz` image in them, and CI only builds and
  tests the Rust crate. Until the pipeline builds it, follow the steps above.
- The sidecar and the chart's `authz` Deployment have not been run behind a
  real gateway (see the note at the top).

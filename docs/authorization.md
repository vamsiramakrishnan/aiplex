# Authorization

This page states how an API request is allowed or refused. It covers the route policy,
roles, grants, tenant scope, the filtered views, rate limits and response headers.
The route classes below come from `cmd/aiplex-api/route_policy.go`. The intended policy for
each mounted route is in `cmd/aiplex-api/authz_policy_test.go`.

## Layers

A request passes these checks in order:

1. **Identity.** A bearer token is verified when present. A request whose token does not verify
   carries no identity, so every route that needs one answers `401`. `WIFAuthMode` sets the
   identity and the resolved roles and scopes on the request (`internal/api/middleware.go`).
2. **Route policy.** The route is classified by its mounted pattern, not its raw path. The first
   matching rule wins (`internal/api/route_policy.go`). A route with no rule is classed
   `operator` and is deployment-wide. A request that matches no route is passed on, so the router
   answers `404` or `405`.
3. **Handler checks.** Handlers apply finer checks: an action grant, ownership of the record, or
   admission to an access point. The route class is the first gate, not the only one.

## Policy classes

The class names are the values the code prints (`RouteAccess.String()`). The intended-policy test
uses the short names in the last column.

| Class | Who passes | Test name |
| --- | --- | --- |
| `public` | Any caller. No identity is needed. | `public` |
| `internal` | Any caller. The route's own shared-secret guard decides. See [known limits](#known-limits). | `internal` |
| `authenticated` | Any verified identity, including a caller with no role. The handler scopes the data to the caller or checks a grant. | `auth` |
| `operator` | Roles `admin` or `deployer`. This is the default for any route without a rule. | `op` |
| `admin` | Role `admin` only. | `admin` |

Every rule also has a scope:

- **Tenant-scoped** routes return data partitioned by the caller's tenant. A caller bound to another
  tenant sees only that tenant's data.
- **Deployment-wide** routes act on state shared by the whole deployment. An identity bound to a
  different tenant is refused with `403 DEPLOYMENT_SCOPE_REQUIRED`. See [tenant rules](#tenant-rules).

Refusals use these codes. An anonymous caller gets `401 UNAUTHENTICATED` on every class except
`public` and `internal`. A verified caller without the required role gets `403 ROLE_REQUIRED`.

## Route groups

The table groups routes by API area. It is not a full list. For the complete table, see
[the machine-readable route table](#the-machine-readable-table).

| API area | Class | Scope | Routes |
| --- | --- | --- | --- |
| Health, login, metadata | `public` | tenant | `/healthz`, `/readyz`, `/auth/login`, the RFC 9728 metadata under `/.well-known/oauth-protected-resource`, `/mcp/client-metadata/` |
| Shared-secret callbacks | `internal` | tenant | `POST /auth/token-hook` (guard `AUTH_HOOK_TOKEN`), `POST /internal/tape/events` and `POST /internal/projections/rebuild/{run_id}` (guard `TAPE_INGEST_TOKEN`) |
| Hydra consent | `authenticated` | tenant | `GET` and `POST /auth/consent`. The handler binds the consent to the caller. |
| User scopes | `admin` | deployment | `GET` and `PUT /auth/users/{userId}/scopes` |
| Access point clients | `authenticated` | tenant | `/ap/{id}/…`. The handler applies access point admission, then the model or tool checks. |
| Access point resource | reads `authenticated`; writes `admin` | tenant | `GET /api/v1/access-points`, `GET /api/v1/access-points/{id}`, `PUT` and `DELETE` on `{id}`, `POST {id}/quota-plan` (admin). See [filtered access-point view](#filtered-access-point-view). |
| Self-service and static metadata | `authenticated` | tenant | `/api/v1/iam/whoami`, `/api/v1/iam/roles`, `/api/v1/modules`, `/api/v1/agents/identity-presets`, `/api/v1/identity/user-provider-presets`, `/api/v1/mcp/auth-types`, `/api/v1/llm/operations`, `/api/v1/clients/profiles` |
| Usage ledger | reads `authenticated`; prices read `operator`; price writes `admin` | tenant | `/api/v1/usage/records` and `/api/v1/usage/summary` (own records unless admin). See [usage ledger](usage-ledger.md). |
| Quota attempts | `authenticated` | tenant | `GET /api/v1/quota/attempts/{id}`. See [quota attempt reads](#quota-attempt-reads). |
| Invocation | `authenticated`, with a handler grant | tenant | `/api/v1/llm/native/{id}/*`, `/api/v1/llm/gateway/{id}/*`, `POST /api/v1/a2a/connections/{id}/invoke`, `POST /api/v1/mcp/connections/{id}/invoke`, the MCP authorization routes under `/api/v1/mcp/connections/{id}/authorization`, `POST /api/v1/iam/native/bindings/{id}/read` |
| Model batches and capacity | `admin` | tenant | `GET /api/v1/llm/batches`, `GET /api/v1/llm/surfaces/{id}/capacity`. See [model batches](model-batches.md). |
| Native governance writes | `admin` | tenant | Non-GET methods under `/api/v1/iam/native/`. Reads are `operator` with a grant. |
| Tenant configuration | `operator`, with handler grants | tenant | `/api/v1/llm/surfaces`, `/api/v1/a2a/connections`, `/api/v1/mcp/` (other than the `authenticated` routes above), `/api/v1/clients/`, `/api/v1/network/`, `/api/v1/quota/` (each operation needs an `aiplex:quota:*` grant), `/api/v1/overview/` (usage withheld for deployers), `/api/v1/iam/native/` (reads) |
| Deployment-wide administration | `admin` | deployment | `/api/v1/iam/role-bindings` and `/{id}`, `POST /api/v1/iam/validate-principal`, `/api/v1/dashboard/`, `/api/v1/llm/usage` (all methods), `PUT` and `DELETE` on `/api/v1/agents/{clientId}/identities/{id}` and `/api/v1/identity/user-providers/{id}`, `POST /api/v1/identity/user-providers/{id}/membership/invalidate` |
| Deployment-wide reads and actions | `operator` | deployment | Agent registry and identity verification, user identity providers (list and plan), catalog, instances, LLM routes and providers, A2A agents and delegations, skills, the runs API (with run grants), `GET /events/stream`, `GET /v0.1/servers` |
| Agent and skill discovery | `authenticated` | deployment | `GET /a2a/{instanceId}/.well-known/agent.json`, `GET /skills/{instanceId}/.well-known/skills.json` |

The intended class of every mounted route is listed in `intendedRoutePolicy` in
`cmd/aiplex-api/authz_policy_test.go`. A route with a handler grant has an entry ending in `+grant`.

### The machine-readable table

`TestRouteTable` in `cmd/aiplex-api/authz_harness_test.go` writes the mounted routes, with the class
and scope the policy enforces for each, when the environment variable `AIPLEX_ROUTE_TABLE` names a
file path:

```sh
AIPLEX_ROUTE_TABLE="$PWD/route-table.json" go test ./cmd/aiplex-api -run TestRouteTable -count=1
```

The file is a JSON array. Each entry has `method`, `pattern`, `handler`, `middlewares` and
`enforced_policy`. The value of `enforced_policy` is `<class>/<scope>`, for example
`authenticated/tenant` or `admin/deployment`. The test writes the file with mode `0600`. The Makefile
has no target for this.

Two tests check the policy:

- `TestEveryMountedRouteHasAnIntendedPolicy` fails when a mounted route has no entry in
  `intendedRoutePolicy`. A new route is refused to ordinary callers until it is classified.
- `TestRouteAuthorizationMatrix` sends every mounted route once for each caller: anonymous, no role,
  viewer, deployer, admin, a foreign-tenant admin and agent. It compares each answer with the
  intended policy.

## Roles

The roles are `admin`, `deployer`, `viewer` and `agent` (`internal/models/iam.go`). The route policy
checks only `admin` and `deployer`, so a `viewer` or `agent` caller has the same route-level access as a
caller with no role.

The built-in scope defaults apply when a role binding names no scopes:

| Role | Default scopes |
| --- | --- |
| `admin` | `mcp:tools:*`, `mcp:server:*`, `a2a:task:*`, `a2a:agent:*`, `llm:model:*`, `llm:capability:*` |
| `deployer` | `mcp:tools:*`, `mcp:server:*`, `a2a:task:*`, `a2a:agent:*`, `llm:model:*` |
| `viewer` | None. The code comment says viewers read and do not execute. |
| `agent` | None by default. Scopes are set per agent at registration. |

## What each caller can do

| Caller | Public and internal routes | Authenticated routes | Operator routes | Admin routes |
| --- | --- | --- | --- | --- |
| Anonymous | Public: yes. Internal: only with the route's secret. | No, `401 UNAUTHENTICATED` | No, `401` | No, `401` |
| No role, viewer or agent | Yes | Yes, scoped to the caller or to a grant | No, `403 ROLE_REQUIRED` | No, `403 ROLE_REQUIRED` |
| Deployer | Yes | Yes | Yes, in the caller's tenant, plus handler grants | No, `403 ROLE_REQUIRED` |
| Admin | Yes | Yes | Yes | Yes |
| Admin bound to another tenant | Yes | Yes, in its own tenant | Yes in its own tenant. Deployment-wide routes are refused with `403 DEPLOYMENT_SCOPE_REQUIRED`. | Same as the row above |

A caller with no role can invoke a model or tool only where a handler grant allows it. Being
authenticated is not enough.

Other codes a caller can see:

| Code | Source |
| --- | --- |
| `403 TENANT_REQUIRED` | No tenant can be resolved, or the identity holds more than one tenant grant on a tenant route. See [tenant rules](#tenant-rules). |
| `403 RUNS_SCOPE_REQUIRED` | A runs route without its `aiplex:runs:*` grant. |
| `403 FORBIDDEN`, `ADMIN_REQUIRED`, `RESOURCE_FORBIDDEN` | Handler-level checks, after the route class passed. |
| `404 NOT_FOUND` | A record that exists but is not visible to the caller. The response does not say which. |

## Tenant rules

Tenant-scoped routes resolve the caller's tenant in this order (`cmd/aiplex-api/connected.go`):

1. Exactly one `aiplex:tenant:<id>` scope in the verified access. That tenant is used.
2. More than one `aiplex:tenant:<id>` scope. The request is refused with `403 TENANT_REQUIRED`.
3. No tenant scope. The value of `AIPLEX_TENANT_ID` is used.
4. No tenant scope and no `AIPLEX_TENANT_ID`, in development mode. The tenant is `development`.
5. Otherwise the request is refused with `403 TENANT_REQUIRED`.

Deployment-wide routes are allowed when the caller has no tenant scope. They are also allowed when the
caller has exactly one tenant scope and it equals the deployment's home tenant. The home tenant is
`AIPLEX_TENANT_ID`, or `development` in development mode when that variable is empty. Any other caller
gets `403 DEPLOYMENT_SCOPE_REQUIRED`.

The runs API resolves the tenant differently (`internal/api/middleware.go`, `internal/api/runs_access.go`):

- The tenant comes from an `aiplex:tenant:<id>` scope. If there is none, it comes from the identity's
  domain, through `AIPLEX_DOMAIN_TENANT_MAP` (a comma-separated list of `domain=tenant`).
- `AIPLEX_REQUIRE_TENANT=1` makes `/api/v1/runs` refuse callers that have neither a tenant nor the
  cross-tenant grant, with `403 TENANT_REQUIRED`. Any other value leaves this check off.
- A run outside the caller's tenant is answered as `404 NOT_FOUND`, as if it did not exist.

## Grants

A grant is a scope in the verified access. A grant `X:*` matches any scope that starts with `X:`
(`scopeAllowed` in `internal/api/connected.go`).

| Grant | Used for |
| --- | --- |
| `aiplex:quota:read` | Quota reads, including other callers' attempts. See [quota attempt reads](#quota-attempt-reads). |
| `aiplex:quota:*` | Other quota operations. The list is in [QuotaPlex administration](quotaplex-administration.md). |
| `aiplex:runs:read` | Listing and reading runs, and the runs event stream. |
| `aiplex:runs:read:cross_tenant` | Seeing runs of every tenant. |
| `aiplex:runs:redrive`, `reconcile`, `cancel`, `signal`, `compensate`, `compact` | The matching run action. Each action is idempotent: a repeat with the same `Idempotency-Key` within 5 minutes returns the cached answer. |
| Handler action grants (for example on the invocation routes) | Checked by each handler before a provider or tool credential is used. |

Quota grants are checked again at the point of the operation, not only on the access that arrived with
the request.

### Quota attempt reads

`GET /api/v1/quota/attempts/{id}` returns one admission attempt when:

- the caller made the attempt, as the principal or as the delegated actor (`act`), or
- the caller holds `aiplex:quota:read`.

Otherwise the response is `404 NOT_FOUND`. A role alone does not grant the read. The route also answers
`403 TENANT_REQUIRED` without a verified tenant, `401 IDENTITY_REQUIRED` without a verified identity, and
`503 QUOTA_ATTEMPT_UNAVAILABLE` on a store failure.

### Filtered access-point view

Operators (admins and deployers) read the full access point resource. Any other verified caller gets:

- `GET /api/v1/access-points`: only the access points that admit the caller, each as a view with `id`,
  `display_name` and `endpoints`. The view has no subject lists and no model or tool lists.
- `GET /api/v1/access-points/{id}`: the same view for one access point. An access point that does not
  admit the caller answers `404`, the same as a missing one.

Admission is decided by the access point's `caller_kinds` and `subjects`. The code is in
`accessPointReads`, `accessPointCallerView` and `AccessPoint.admits` (`internal/api/access_point_view.go`,
`internal/api/access_point_caller.go`).

## Rate limits

| Limit | Value | Key | Response |
| --- | --- | --- | --- |
| General | 200 requests per minute. 1200 in development mode. | The verified principal. Anonymous requests use the peer address, without the port. Forwarded headers are not trusted here. | `429 RATE_LIMITED` with `Retry-After: 60`. The window is one minute per key. |
| Token verification dry runs | 20 requests per minute (`VerifyDryRunsPerMinute`) | The same key as the general limit. Applied to `POST /api/v1/agents/identities/verify` and `POST /api/v1/identity/user-providers/verify`. | `429 RATE_LIMITED` with `Retry-After: 60`. |
| Tape ingest and projection rebuild (`/internal/…`) | `TAPE_INGEST_RPS` events per second, default 1000. Burst `TAPE_INGEST_BURST`, default 1000. | The source IP. The first entry of `X-Forwarded-For` is used when present. | `429 RATE_LIMITED` with `Retry-After: 1`. |

Request bodies are capped at 1 MiB. Invocation routes are capped at 8 MiB. Those are the access point
routes, the MCP connection invoke and authorization routes, the A2A invoke route, the LLM native and
gateway routes, and `POST /api/v1/iam/native/bindings/{id}/read`.

## Browser access and response headers

**CORS.** Allowed origins come only from `CONSOLE_ORIGINS`. See
[server environment variables](helm-identity-config.md#server-environment-variables).

- A listed origin is echoed in `Access-Control-Allow-Origin`. A `*` entry is answered with a literal
  `*`. The request's origin is never reflected.
- Credentials are never allowed cross-origin.
- With no `CONSOLE_ORIGINS` and development mode off, no cross-origin read is allowed.
- Allowed methods: `GET, POST, PUT, PATCH, DELETE, OPTIONS`.
- Allowed request headers: `Authorization, Content-Type, X-Request-Id, Idempotency-Key,
  X-Aiplex-Funding-Account, X-Aiplex-Operation-Id, MCP-Protocol-Version`.
- Exposed response headers: `X-Request-Id, X-Aiplex-Attempt-ID, Retry-After, ETag`.
- Preflight `Access-Control-Max-Age` is 86400 seconds.
- `OPTIONS` requests get `204 No Content` before authorization runs.

**Request IDs.** A client's `X-Request-Id` is kept only when it matches `^[A-Za-z0-9._:-]{1,128}$`.
Otherwise the server generates `req-<n>`. The ID is echoed in the `X-Request-Id` response header and in
error bodies as `request_id`.

**Caching.** Every response on a route that is not `public` or `internal` carries `Cache-Control: no-store`,
including refusals. The access point's RFC 9728 metadata answers `Cache-Control: public, max-age=300`.
Some access point and MCP authorization responses set `no-store` themselves.

## Known limits

- **Rate limits are per process.** The counters are in memory. The code comment on `RateLimit` says a
  multi-replica deployment should use a shared limiter. With several replicas, each replica counts
  separately.
- **Internal callbacks share the public listener.** `POST /auth/token-hook` is guarded by the bearer token
  in `AUTH_HOOK_TOKEN`. `POST /internal/tape/events` and `POST /internal/projections/rebuild/{run_id}` are
  guarded by `TAPE_INGEST_TOKEN`. When the variable is empty, the route answers `503 AUTH_DISABLED` and does
  not run. These guards sit outside the route policy.
- **The ingest rate limit trusts `X-Forwarded-For`.** The first entry is used as the client IP. The code
  comment says the gateway sets the header in production. A caller that can reach the API without the
  gateway can send any value.
- **Development mode.** `AIPLEX_DEV_MODE=true` admits requests with no verified identity to routes that
  need one. It raises the general limit to 1200 per minute and allows every origin when `CONSOLE_ORIGINS`
  is empty. The server refuses a non-loopback `HOST` in this mode. Do not expose a development API.
- **The route class is not the whole check.** A route classed `authenticated` depends on its handler for
  scoping and grants. The route table shows the class; it does not prove each handler is correct.
- **Two tenant grants are not resolved.** On tenant routes an identity with two `aiplex:tenant:<id>` scopes
  is refused with `403 TENANT_REQUIRED`. The runs API does not refuse; it takes one of the two. Do not
  grant two tenant scopes to one identity.

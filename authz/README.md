# aiplex-authz

Rust authorization service and reusable scope-decision/JWT-verification library.
The service implements authorization only. It does not reserve quota or account for spending.

## Authentication

Production startup requires `JWT_ISSUER`, `JWT_AUDIENCE`, and `JWT_JWKS_URL`.
The configured JWKS URL must use HTTPS; redirects are refused. Keys are fetched
before the listener starts. Only RS256 signing keys are accepted. Each token must
have a matching signature, issuer, audience, nonempty subject, and unexpired `exp`;
`nbf` is enforced when present. Token-provided key URLs are never followed.

Keys are cached for five minutes. Refresh is serialized and attempted at most once
per 30 seconds, including unknown-key requests. A newly rotated key may therefore
be unavailable for up to 30 seconds after a preceding refresh. Expired cached keys
are not accepted if refresh fails. `/readyz` refreshes expired keys and returns 503
when valid signing keys cannot be obtained. `/healthz` checks process liveness.

| Variable | Meaning |
|---|---|
| `JWT_ISSUER` | Exact trusted JWT issuer; required in production |
| `JWT_AUDIENCE` | Exact API audience; required in production |
| `JWT_JWKS_URL` | Pinned HTTPS JWKS endpoint; required in production |
| `AUTHZ_BIND` | Listener IP, defaults to `0.0.0.0` |
| `PORT` | Listener port, defaults to `9191` |
| `AUTHZ_DEV_MODE` | Set exactly `true` for unsigned local development tokens; requires loopback `AUTHZ_BIND` |
| `DENIAL_WEBHOOK_URL` | Optional best-effort denial event sink |

Development mode is never selected implicitly by missing configuration. Example:

```sh
AUTHZ_DEV_MODE=true AUTHZ_BIND=127.0.0.1 cargo run
```

Bind and network-restrict this service so only the trusted gateway can access it.
Client metadata becomes trusted only after successful token verification. Native
Google OAuth access tokens and workforce group resolution need a separate trusted
identity adapter; this service expects JWTs with Aiplex's scope claims.

## Interfaces

`POST /check` is an explicit JSON adapter API, with this request body:

```json
{
  "headers": {"authorization": "Bearer <JWT>"},
  "path": "/mcp/server/mcp",
  "method": "POST",
  "body": {"method": "tools/call", "params": {"name": "search"}}
}
```

A 200 allows the operation; a 403 denies it. The JSON response includes `allowed`
and, on denial, `denied_reason`. This JSON envelope is **not** Envoy's native HTTP
ext_authz wire format and is not its gRPC Check service.

The native HTTP adapter accepts `/authorize` followed by the original path.
Configure the HTTP ext_authz service to prepend `/authorize` to the original path
and preserve the original method, Authorization header, and complete request body.
For example, an original `POST /llm/chat` is authorized using
`POST /authorize/llm/chat`, the original headers, and the original JSON body.
The service derives the resource from that URI, never an original-path header.
Bodies above 64 KiB and malformed JSON fail closed. Configure gateway body
buffering to reject partial bodies. Authz availability errors must fail closed.

Existing Kubernetes manifests target OPA's gRPC service. Switching those manifests
to this HTTP adapter requires validating the gateway controller's configuration;
this binary does not expose a gRPC service.

## Scope behavior

- MCP tool calls require `mcp:tools:<name>`.
- Agent task delegation requires `a2a:task:<type>`.
- Model calls require `llm:model:<id>`.
- Skill calls require `skill:invoke:<name>`.
- A terminal namespace wildcard, such as `llm:model:*`, grants that namespace.
- Discovery methods require authentication and a matching protocol path. A discovery
  field injected into a model request does not bypass its model scope.
- Unrecognized paths and operations deny by default.

Denial reporting is best-effort and bounded to 32 in-flight requests with a
2-second timeout. Saturation can omit events; use gateway logs for durable audit.

## Build and validation

```sh
cargo fmt --check
cargo clippy --locked --all-targets -- -D warnings
cargo test --locked
cargo build --release --locked
```

`tests/fixtures/test-only-private.pem` is an intentionally public test key. It is
used exclusively for signed-token regression tests and must never be deployed.
Runtime latency and memory targets require measurement in the intended gateway
configuration; this repository does not establish production benchmark results.

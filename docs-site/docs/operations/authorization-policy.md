---
title: Authorization policy
description: "How the gateway checks a request's scopes: the aiplex-authz service and the Rego policy."
---

# Authorization policy

The gateway calls an external authorization service for each request. The check is stateless: the JWT is the policy. The effective permissions (agent ceiling, user ceiling and session consent) were already intersected when the token was issued, so the service only parses the token and checks that the requested action is among its scopes.

Two implementations exist in the repository:

| Implementation | Location | Use |
| --- | --- | --- |
| `aiplex-authz` | `authz/` (Rust) | The `ext_authz` gRPC service |
| Rego policy | `policies/aiplex_authz.rego`, with tests in `policies/aiplex_authz_test.rego` | The same rules for OPA |

## What is checked

The Rego policy denies by default and allows:

- MCP `tools/call` when `mcp:tools:{name}` is in the token's scopes.
- Requests under `/a2a/` when `a2a:task:{task_type}` is in the scopes.
- Requests under `/llm/` when `llm:model:{model}` is in the scopes, with the model taken from the `x-model-id` header.
- Skill invocation (`skills/invoke`, or HTTP calls under `/skills/`) when `skill:invoke:{name}` is in the scopes.
- Discovery methods such as `initialize`, `tools/list`, `skills/list` and `ping`.
- `GET /api/v1/runs*` with `aiplex:runs:read`, and run operator actions with `aiplex:runs:{action}`.

Read the policy itself on [GitHub](https://github.com/vamsiramakrishnan/aiplex/blob/main/policies/aiplex_authz.rego).

## aiplex-authz

Production startup requires `JWT_ISSUER`, `JWT_AUDIENCE` and `JWT_JWKS_URL`. The JWKS URL must be HTTPS and redirects are refused. Only RS256 signing keys are accepted, and each token must match signature, issuer, audience, a nonempty subject and an unexpired `exp`. Keys are cached for five minutes. `/healthz` checks liveness and `/readyz` returns 503 when valid signing keys cannot be obtained.

| Variable | Meaning |
| --- | --- |
| `JWT_ISSUER` | Exact trusted JWT issuer; required in production |
| `JWT_AUDIENCE` | Exact API audience; required in production |
| `JWT_JWKS_URL` | Pinned HTTPS JWKS endpoint; required in production |
| `AUTHZ_BIND` | Listener IP, default `0.0.0.0` |
| `PORT` | Listener port, default `9191` |
| `AUTHZ_DEV_MODE` | Exactly `true` for unsigned local tokens; requires a loopback `AUTHZ_BIND` |
| `DENIAL_WEBHOOK_URL` | Optional best-effort denial event sink |

Development mode is never selected implicitly. Run it locally with:

```bash
AUTHZ_DEV_MODE=true AUTHZ_BIND=127.0.0.1 cargo run
```

Bind and network-restrict the service so only the trusted gateway can reach it. The service implements authorization only; it does not reserve quota or account for spending. The full text is `authz/README.md` in the repository.

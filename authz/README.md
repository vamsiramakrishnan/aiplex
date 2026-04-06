# aiplex-authz

High-performance ext_authz service for AIPlex, written in Rust with Axum.

## Overview

Replaces OPA on the data path for ~24x lower latency (target: 0.05ms p50).

- **HTTP ext_authz**: Envoy calls `POST /check` with request metadata
- **JWT validation**: Parses and validates OAuth 2.1 tokens from Ory Hydra
- **Scope-based authz**: Enforces `mcp:tools:*`, `a2a:task:*`, `llm:model:*` scopes
- **Zero dependencies**: No OPAL, no external policy store — policy is in the JWT

## Protocol

Envoy HTTP ext_authz filter sends:

```json
POST /check
{
  "headers": {"authorization": "Bearer eyJ..."},
  "path": "/mcp/knowledge-base-xyz/mcp",
  "method": "POST",
  "body": {"method": "tools/call", "params": {"name": "search_curriculum"}}
}
```

Response:

```json
200 OK
{"allowed": true}

# or

403 Forbidden
{"allowed": false, "denied_reason": "missing scope: mcp:tools:search_curriculum"}
```

## Authorization Rules (Matches OPA Rego)

1. **Discovery methods**: always allowed (`tools/list`, `initialize`, `ping`, etc.)
2. **MCPlex** (`tools/call`): requires `mcp:tools:{tool_name}` scope
3. **A2APlex** (`/a2a/*`): requires `a2a:task:{task_type}` scope
4. **LLMPlex** (`/llm/*`): requires `llm:model:{model_id}` scope
5. **Health endpoints**: `/healthz`, `/readyz` always allowed

## Environment Variables

| Variable     | Default | Description                                    |
|--------------|---------|------------------------------------------------|
| `PORT`       | `9191`  | HTTP listen port                               |
| `JWT_ISSUER` | (empty) | JWT issuer for validation (skip if empty/dev)  |

## Build

```bash
cargo build --release
cargo test
```

## Docker

```bash
docker build -t aiplex-authz:latest .
docker run -p 9191:9191 aiplex-authz:latest
```

## Deployment (Envoy)

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: SecurityPolicy
metadata:
  name: aiplex-authz
spec:
  extAuth:
    http:
      backendRef: { name: aiplex-authz, port: 9191 }
      path: /check
      headersToBackend: ["authorization"]
```

## Performance

- **Target latency**: 0.05ms p50 (vs 1.2ms OPA)
- **Memory**: ~5MB RSS (vs 120MB OPA + OPAL)
- **Startup**: <10ms (vs 2s OPA)
- **Zero external deps**: No Redis, no Firestore, no OPAL

## Testing

```bash
# Unit tests
cargo test

# Integration test (manual)
curl -X POST http://localhost:9191/check \
  -H 'Content-Type: application/json' \
  -d '{
    "headers": {"authorization": "Bearer <JWT>"},
    "path": "/mcp/server/mcp",
    "body": {"method": "tools/list"}
  }'
```

## License

Apache 2.0

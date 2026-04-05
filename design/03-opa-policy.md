# 03 — OPA Policy Engine

## Overview

OPA (Open Policy Agent) is the runtime authorization enforcer. It receives every request from Envoy via ext_authz, parses the JWT, and checks whether the requested action is covered by the token's scopes. It is stateless — the JWT is the only input.

---

## Design Principles

1. **JWT is the policy.** No external data fetches. No OPAL. No bundle updates for per-user rules.
2. **Fail closed.** `default allow := false`. If OPA can't parse the JWT or the body, the request is denied.
3. **No side effects.** OPA never modifies requests, writes logs, or calls external services. It returns allow/deny.
4. **Plane-agnostic pattern.** Every plane follows the same logic: extract the action identifier, format it as a scope string, check if it's in the token's scope claim.

---

## The Policy

```rego
package aiplex.authz
import rego.v1

default allow := false

# ── JWT Verification ──
token := io.jwt.decode_verify(
    input.attributes.request.http.headers.authorization,
    {
        "iss": "https://aiplex.example.com/auth/realms/aiplex",
        "aud": "aiplex"
    }
)
claims := token[2]
scopes := split(claims.scope, " ")
body := json.unmarshal(input.attributes.request.http.body)
path := input.attributes.request.http.path

# ── MCPlex: tool calls ──
allow if {
    body.method == "tools/call"
    sprintf("mcp:tools:%s", [body.params.name]) in scopes
}

# ── A2APlex: agent-to-agent task delegation ──
allow if {
    startswith(path, "/a2a/")
    sprintf("a2a:task:%s", [body.task_type]) in scopes
}

# ── LLMPlex: model inference ──
allow if {
    startswith(path, "/llm/")
    model := input.attributes.request.http.headers["x-model-id"]
    sprintf("llm:model:%s", [model]) in scopes
}

# ── Discovery (all planes) ──
allow if {
    body.method in {"initialize", "tools/list", "resources/list",
                    "tasks/list", "agents/list", "models/list", "ping"}
}
```

---

## Line-by-Line Analysis

### JWT Verification

```rego
token := io.jwt.decode_verify(
    input.attributes.request.http.headers.authorization,
    {"iss": "https://aiplex.example.com/auth/realms/aiplex"}
)
```

- `io.jwt.decode_verify` performs full JWT validation: signature check, expiry, issuer
- JWKS is fetched from Keycloak's well-known endpoint and cached by OPA
- If verification fails, `token` is undefined → `claims` is undefined → all `allow` rules fail → `default allow := false` kicks in
- The `authorization` header must contain the bare token (Envoy strips "Bearer " prefix via header mutation before ext_authz)

> Decision: Envoy strips the "Bearer " prefix before sending to OPA. This simplifies the Rego — no string manipulation needed.

### Scope Extraction

```rego
claims := token[2]
scopes := split(claims.scope, " ")
```

- `token[2]` is the payload (index 0 = header, 1 = signature valid bool, 2 = payload)
- Scopes are space-delimited per OAuth 2.0 spec (RFC 6749 §3.3)
- If the token has no `scope` claim, `claims.scope` is undefined → `split` fails → `scopes` is undefined → all scope checks fail → denied

### Body Parsing

```rego
body := json.unmarshal(input.attributes.request.http.body)
```

- Envoy sends the request body to OPA via `withRequestBody` in the SecurityPolicy
- Max body size: 64KB (`maxRequestBytes: 65536`)
- If body is not valid JSON, `json.unmarshal` fails → `body` is undefined → method/params checks fail → denied
- For GET requests (like SSE), body is empty → `json.unmarshal("")` fails → only discovery rules could match

### MCPlex Rule

```rego
allow if {
    body.method == "tools/call"
    sprintf("mcp:tools:%s", [body.params.name]) in scopes
}
```

- Matches JSON-RPC method `tools/call`
- Constructs the scope string from the tool name in the request
- Checks membership in the token's scopes
- If `body.params.name` is missing, `sprintf` still produces a string but it won't match any valid scope → denied

### A2APlex Rule

```rego
allow if {
    startswith(path, "/a2a/")
    sprintf("a2a:task:%s", [body.task_type]) in scopes
}
```

- Path-based routing: any request to `/a2a/*`
- Task type comes from the request body (A2A protocol defines `task_type` field)
- Same scope check pattern as MCPlex

### LLMPlex Rule

```rego
allow if {
    startswith(path, "/llm/")
    model := input.attributes.request.http.headers["x-model-id"]
    sprintf("llm:model:%s", [model]) in scopes
}
```

- Path-based routing: any request to `/llm/*`
- Model ID comes from a custom header `x-model-id` (set by the agent)
- Why a header, not a body field? LLM request bodies vary by provider. The header is a stable contract.

### Discovery Rule

```rego
allow if {
    body.method in {"initialize", "tools/list", "resources/list",
                    "tasks/list", "agents/list", "models/list", "ping"}
}
```

- Discovery is always allowed for any valid JWT (the JWT must still pass verification)
- This lets agents introspect available capabilities before requesting specific scopes
- `initialize` is required for MCP session setup
- `ping` is for health checks

---

## OPA Deployment

### As a DaemonSet (not sidecar)

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: opa-ext-authz
  namespace: aiplex-system
spec:
  template:
    spec:
      containers:
        - name: opa
          image: openpolicyagent/opa:0.68.0
          args:
            - "run"
            - "--server"
            - "--addr=:9191"
            - "--set=decision_logs.console=true"
            - "--set=services.keycloak.url=https://aiplex.example.com"
            - "--set=bundles.keycloak.service=keycloak"
            - "--set=bundles.keycloak.resource=auth/realms/aiplex/protocol/openid-connect/certs"
            - "/policies"
          ports:
            - containerPort: 9191
              name: grpc
          volumeMounts:
            - name: policy
              mountPath: /policies
      volumes:
        - name: policy
          configMap:
            name: aiplex-authz-policy
```

> Decision: DaemonSet, not sidecar. One OPA per node serves all Envoy instances on that node. Reduces pod count. Pod disruption budget ensures at least one OPA is always running.

### Policy as ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: aiplex-authz-policy
  namespace: aiplex-system
data:
  aiplex_authz.rego: |
    package aiplex.authz
    import rego.v1
    # ... (the full policy above)
```

Policy updates = ConfigMap update + OPA restart. No bundle server needed for 20 lines of Rego.

### JWKS Caching

OPA fetches Keycloak's JWKS endpoint on startup and caches it:

```
https://aiplex.example.com/auth/realms/aiplex/protocol/openid-connect/certs
```

- Cache TTL: 5 minutes (Keycloak rotates keys on a longer schedule)
- If JWKS fetch fails, OPA uses cached keys
- Key rotation: Keycloak publishes both old and new keys during rotation window

---

## ext_authz Integration

### Envoy Configuration

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: SecurityPolicy
metadata:
  name: aiplex-authz
  namespace: aiplex-system
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: aiplex-gateway
  extAuth:
    grpc:
      backendRef:
        name: opa-ext-authz
        port: 9191
    withRequestBody:
      maxRequestBytes: 65536
      allowPartialMessage: false
      packAsBytes: false
```

### Request/Response Flow

```
Envoy → OPA (gRPC ext_authz v3)

Request:
{
  "attributes": {
    "request": {
      "http": {
        "method": "POST",
        "path": "/mcp/knowledge-base-xyz/mcp",
        "headers": {
          "authorization": "<JWT>",
          "content-type": "application/json",
          "x-model-id": "gemini-2.5-flash"  // only for LLMPlex
        },
        "body": "{\"jsonrpc\":\"2.0\",\"method\":\"tools/call\",\"params\":{\"name\":\"search_curriculum\"}}"
      }
    }
  }
}

Response (allow):
{
  "status": {"code": 0},  // OK
  "ok_response": {
    "headers": [
      {"header": {"key": "x-jwt-sub", "value": "student@school.edu"}},
      {"header": {"key": "x-jwt-azp", "value": "tutor-agent"}}
    ]
  }
}

Response (deny):
{
  "status": {"code": 7},  // PERMISSION_DENIED
  "denied_response": {
    "status": {"code": 403},
    "body": "{\"error\":{\"code\":\"SCOPE_DENIED\",\"message\":\"Missing scope: mcp:tools:search_curriculum\"}}"
  }
}
```

> Decision: On allow, OPA injects `x-jwt-sub` and `x-jwt-azp` headers. Envoy uses `x-jwt-sub` for rate limiting (per-user). Backend services can use both for audit logging without re-parsing the JWT.

---

## Testing the Policy

### Unit Tests (OPA native)

```rego
package aiplex.authz_test
import rego.v1

# Test: MCPlex tool call with valid scope → allow
test_mcplex_tool_call_allowed if {
    allow with input as {
        "attributes": {
            "request": {
                "http": {
                    "headers": {"authorization": valid_jwt(["mcp:tools:search"])},
                    "body": "{\"method\":\"tools/call\",\"params\":{\"name\":\"search\"}}",
                    "path": "/mcp/server-1/mcp"
                }
            }
        }
    }
}

# Test: MCPlex tool call without scope → deny
test_mcplex_tool_call_denied if {
    not allow with input as {
        "attributes": {
            "request": {
                "http": {
                    "headers": {"authorization": valid_jwt(["a2a:task:research"])},
                    "body": "{\"method\":\"tools/call\",\"params\":{\"name\":\"search\"}}",
                    "path": "/mcp/server-1/mcp"
                }
            }
        }
    }
}

# Test: Discovery always allowed
test_discovery_allowed if {
    allow with input as {
        "attributes": {
            "request": {
                "http": {
                    "headers": {"authorization": valid_jwt([])},
                    "body": "{\"method\":\"tools/list\"}",
                    "path": "/mcp/server-1/mcp"
                }
            }
        }
    }
}

# Test: Expired JWT → deny
test_expired_jwt_denied if {
    not allow with input as {
        "attributes": {
            "request": {
                "http": {
                    "headers": {"authorization": expired_jwt()},
                    "body": "{\"method\":\"tools/call\",\"params\":{\"name\":\"search\"}}",
                    "path": "/mcp/server-1/mcp"
                }
            }
        }
    }
}

# Test: Missing body → deny (except for discovery)
test_missing_body_denied if {
    not allow with input as {
        "attributes": {
            "request": {
                "http": {
                    "headers": {"authorization": valid_jwt(["mcp:tools:search"])},
                    "body": "",
                    "path": "/mcp/server-1/mcp"
                }
            }
        }
    }
}
```

Run with: `opa test policies/ -v`

### Integration Tests

```python
# test_opa_integration.py
async def test_opa_allows_valid_tool_call(opa_container, keycloak_jwt):
    """Spin up OPA in a container, send real ext_authz requests."""
    response = await ext_authz_check(
        opa_url=opa_container.url,
        jwt=keycloak_jwt(scopes=["mcp:tools:search"]),
        body={"method": "tools/call", "params": {"name": "search"}},
        path="/mcp/server-1/mcp"
    )
    assert response.status == "OK"

async def test_opa_denies_cross_plane_scope(opa_container, keycloak_jwt):
    """A2A scope should not authorize MCPlex tool calls."""
    response = await ext_authz_check(
        opa_url=opa_container.url,
        jwt=keycloak_jwt(scopes=["a2a:task:research"]),
        body={"method": "tools/call", "params": {"name": "search"}},
        path="/mcp/server-1/mcp"
    )
    assert response.status == "PERMISSION_DENIED"
```

---

## Edge Cases & Security Considerations

### Scope injection via tool name

If a tool is named `search mcp:tools:admin_delete`, the scope would be `mcp:tools:search mcp:tools:admin_delete`. The `sprintf` + `in` check prevents this because `in` does exact membership check on the split scopes array — the space-containing string won't match.

> Decision: Tool names, task types, and model IDs must match `[a-zA-Z0-9_-]+` (validated at deploy time). This is enforced by AIPlex API, not OPA. OPA is a safety net, not the primary validator.

### Missing x-model-id header

If an agent calls `/llm/` without the `x-model-id` header, the LLMPlex rule fails because `input.attributes.request.http.headers["x-model-id"]` is undefined. Request is denied.

### Body larger than 64KB

Envoy rejects the request before it reaches OPA (`allowPartialMessage: false`). The agent gets a 413.

### JSON-RPC batch requests

MCP supports batch JSON-RPC (array of requests). The current policy parses `body.method` which would fail for arrays. 

> Open: Should we support JSON-RPC batch? If yes, OPA needs to iterate over the array and check each method. For v1, we can reject batch requests (return 400 from Envoy if body starts with `[`).

### Multiple tools in one request

MCP `tools/call` is always a single tool. But a future protocol version might support multi-tool calls. The current policy handles one tool per request. Future: iterate over `body.params.tools` array.

---

## Performance

| Metric | Target | Typical |
|--------|--------|---------|
| p50 latency | < 1ms | 0.3ms |
| p99 latency | < 5ms | 2ms |
| Throughput | > 10,000 req/s per OPA instance | ~15,000 req/s |
| Memory | < 50MB per instance | ~30MB |
| CPU | < 0.1 core steady state | ~0.05 core |

JWT verification dominates latency. The scope check itself is O(n) where n = number of scopes in the token (typically < 20).

### JWKS Cache Warming

On cold start, the first request blocks on JWKS fetch (~100ms). Mitigation: OPA readiness probe checks JWKS availability before accepting traffic.

```yaml
readinessProbe:
  httpGet:
    path: /health?bundles
    port: 8181
  initialDelaySeconds: 5
```

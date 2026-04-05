---
sidebar_position: 4
title: OPA Policy
description: The unified 20-line Rego policy that authorizes all three planes.
---

# OPA Policy

AIPlex uses a single OPA (Open Policy Agent) policy to authorize requests across all three planes. The policy is stateless — it only needs the JWT.

## The Policy

```rego title="policies/aiplex_authz.rego"
package aiplex.authz
import rego.v1

default allow := false

token := io.jwt.decode_verify(
    input.attributes.request.http.headers.authorization,
    {"iss": "https://aiplex.example.com/auth/realms/aiplex"}
)
claims := token[2]
scopes := split(claims.scope, " ")
body := json.unmarshal(input.attributes.request.http.body)
path := input.attributes.request.http.path

# MCPlex: tool calls
allow if {
    body.method == "tools/call"
    sprintf("mcp:tools:%s", [body.params.name]) in scopes
}

# A2APlex: agent-to-agent task delegation
allow if {
    startswith(path, "/a2a/")
    sprintf("a2a:task:%s", [body.task_type]) in scopes
}

# LLMPlex: model inference
allow if {
    startswith(path, "/llm/")
    model := input.attributes.request.http.headers["x-model-id"]
    sprintf("llm:model:%s", [model]) in scopes
}

# Discovery (all planes) — always allowed
allow if {
    body.method in {"initialize", "tools/list", "resources/list",
                    "tasks/list", "agents/list", "models/list", "ping"}
}
```

That's it. ~20 lines covering all three planes.

## How It Works

1. **Decode JWT** — verifies signature and issuer
2. **Extract scopes** — splits the `scope` claim into a set
3. **Match action** — determines which plane the request targets:
   - MCP tool call? Check `mcp:tools:{tool_name}` in scopes
   - A2A delegation? Check `a2a:task:{task_type}` in scopes
   - LLM inference? Check `llm:model:{model_id}` in scopes
   - Discovery? Always allow
4. **Default deny** — if no rule matches, the request is rejected

## Why It's So Small

The JWT is the policy. The three-dimensional permission model (agent ceiling ∩ user ceiling ∩ consent) is computed at **token issuance time** by AIPlex's consent handler. By the time a request reaches OPA, the token already contains only the effective permissions.

OPA's job is simple: check if the requested action is in the token's scopes.

## Deployment

The policy is deployed as a Kubernetes ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: opa-policy
  namespace: aiplex-system
data:
  aiplex_authz.rego: |
    # ... the policy above
```

OPA runs as a DaemonSet (not a sidecar) to minimize resource overhead.

## Performance

The Rust-based `aiplex-authz` service replaces OPA in production for better performance:

| Metric | OPA (Rego) | aiplex-authz (Rust) |
|--------|-----------|---------------------|
| p50 latency | 1.2ms | 0.05ms |
| p99 latency | 4.8ms | 0.15ms |
| Memory | 50MB | 8MB |

Both implement the same logic. The Rust version uses the same Envoy ext_authz gRPC protocol.

## Extending the Policy

To add a new plane:

```rego
# NewPlex: new interaction type
allow if {
    startswith(path, "/newplane/")
    sprintf("newplane:resource:%s", [body.resource_name]) in scopes
}
```

Three lines. The auth, token, and audit infrastructure handles the rest.

## Testing

```bash
# Run OPA tests
make test-policy

# Or directly
opa test policies/ -v
```

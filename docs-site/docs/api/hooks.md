---
title: Hooks API
---

The hooks API manages request-hook policies: tenant resources of kind `hook-policy` that bind an ordered list of hooks (`redact`, `size_limit`, `http`) to the tenant, an access point, a model surface or a tool connection. What hooks inspect, how they fail closed and how refusals look to callers is described in [Request hooks](/docs/library/request-hooks).

Every route requires an operator (`admin` or `deployer`) and is tenant-scoped: the tenant comes from the verified binding. Ordinary callers cannot read policies, because a policy names detection rules, patterns and sidecar endpoints.

## Routes

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/v1/hooks/policies` | List the tenant's hook policies with revisions |
| GET | `/api/v1/hooks/policies/{id}` | Read one policy; the `ETag` header is its revision |
| PUT | `/api/v1/hooks/policies/{id}` | Create (`If-Match: 0`) or replace (`If-Match: <revision>`) a policy |
| DELETE | `/api/v1/hooks/policies/{id}` | Delete a policy (`If-Match: <revision>`) |
| POST | `/api/v1/hooks/policies/{id}/test` | Dry-run a saved policy on sample texts |

## PUT /api/v1/hooks/policies/{id}

The body is the policy specification. `id` must equal the path id.

```json
{
  "id": "pii-guard",
  "scope": {"kind": "tenant"},
  "streaming": "deny",
  "hooks": [
    {
      "id": "redact",
      "type": "redact",
      "phases": ["request"],
      "redact": {"rules": [
        {"id": "email", "builtin": "email", "action": "mask"},
        {"id": "private_key", "builtin": "private_key", "action": "deny"}
      ]}
    }
  ]
}
```

| Field | Required | Meaning |
|---|---|---|
| `id` | Yes | 1 to 63 lowercase letters, digits, `.`, `_` or `-` |
| `description` | No | Up to 512 characters |
| `scope.kind` | Yes | `tenant`, `access_point`, `model_surface` or `tool_connection` |
| `scope.id` | For non-tenant scopes | The access point, surface or connection id |
| `planes` | No | `llmplex`, `mcplex` (default both) |
| `streaming` | No | `deny` (default), `buffer` or `passthrough_uninspected` |
| `non_text` | No | `allow` (default) or `deny` |
| `hooks[]` | Yes | 1 to 16 hooks |
| `hooks[].id`, `.type`, `.phases` | Yes | `type` is `redact`, `size_limit` or `http`; `phases` lists `request` and/or `response` |
| `hooks[].timeout_ms` | No | Default 2000, at most 30000 |
| `hooks[].on_uninspectable` | No | `deny` (default) or `allow` |
| `hooks[].message` | No | Safe public message shown on denial (printable, at most 256 characters) |
| `hooks[].redact.rules[]` | For `redact` | `id`, `builtin` or `pattern` (RE2), `action` (`mask` or `deny`), optional `placeholder` |
| `hooks[].size_limit` | For `size_limit` | `max_chars`, `max_parts` |
| `hooks[].http` | For `http` | `endpoint`, `format` (`aiplex`, `presidio`, `llm-guard`), `auth_secret_ref`, `fail_open`, `presidio`, `llm_guard` |

Errors: `400 INVALID_SPEC` for an unknown field, an unknown hook type, a pattern that does not compile, or a sidecar endpoint the outbound endpoint policy does not approve; `409` for a stale revision. The response is the stored resource record:

```json
{"tenant_id": "development", "kind": "hook-policy", "id": "pii-guard", "revision": 1, "spec": {"id": "pii-guard", "...": "..."}}
```

## POST /api/v1/hooks/policies/{id}/test

Runs the saved policy's hooks, whatever its scope and planes, over sample texts. The sample is never stored or logged and no ledger record is written. `http` hooks call their sidecar.

Request:

```json
{"texts": ["mail jane@example.com", "clean"], "phase": "request", "plane": "llmplex"}
```

`texts` holds 1 to 256 samples (4 MiB in total). `phase` defaults to `request`, `plane` to `llmplex`.

Response:

```json
{
  "policy": "pii-guard",
  "phase": "request",
  "decision": "transform",
  "texts": ["mail [EMAIL]", "clean"],
  "results": [
    {"id": "pii-guard/redact", "type": "redact", "phase": "request", "decision": "transform",
     "rules": [{"id": "email", "count": 1}], "duration_ms": 0}
  ],
  "uninspected_parts": 0
}
```

On a refusal `decision` is `deny`, `code` and `message` are the refusal a caller would see, and `texts` is absent.

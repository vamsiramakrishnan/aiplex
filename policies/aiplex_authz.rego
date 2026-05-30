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

# ── SkillsPlex: skill invocation (skills/invoke JSON-RPC) ──
allow if {
    body.method == "skills/invoke"
    sprintf("skill:invoke:%s", [body.params.name]) in scopes
}

# ── SkillsPlex: HTTP-style skill calls under /skills/ ──
allow if {
    startswith(path, "/skills/")
    sprintf("skill:invoke:%s", [body.skill_name]) in scopes
}

# ── Discovery (all planes) ──
allow if {
    body.method in {"initialize", "tools/list", "resources/list",
                    "tasks/list", "agents/list", "models/list",
                    "skills/list", "ping"}
}

# ── Runs (AIPlex ↔ Tape, PR 11 item 5) ──
# GET /api/v1/runs* and per-run subroutes require aiplex:runs:read.
# Operator-action POSTs each require their own action-scoped grant.

method := input.attributes.request.http.method

allow if {
    method == "GET"
    startswith(path, "/api/v1/runs")
    "aiplex:runs:read" in scopes
}

# Operator actions — POST /api/v1/runs/{id}/{action}.
# We parse the last path segment as the action name and require the
# matching aiplex:runs:{action} scope.
allow if {
    method == "POST"
    startswith(path, "/api/v1/runs/")
    segments := split(trim_prefix(path, "/api/v1/runs/"), "/")
    count(segments) == 2
    action := segments[1]
    action in {"redrive", "reconcile", "cancel", "signal", "compensate"}
    sprintf("aiplex:runs:%s", [action]) in scopes
}

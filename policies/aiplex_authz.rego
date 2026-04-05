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

# ── Discovery (all planes) ──
allow if {
    body.method in {"initialize", "tools/list", "resources/list",
                    "tasks/list", "agents/list", "models/list", "ping"}
}

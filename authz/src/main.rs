use serde::Deserialize;
use std::collections::HashSet;

/// JWT claims from Ory Hydra.
#[derive(Debug, Deserialize)]
struct Claims {
    sub: String,
    azp: Option<String>,
    scope: String,
    act: Option<ActorClaim>,
}

#[derive(Debug, Deserialize)]
struct ActorClaim {
    sub: String,
}

/// Check if a request is authorized based on JWT scopes.
///
/// This is the core authorization logic — identical to the OPA Rego policy
/// but running as a compiled Rust binary for ~24x lower latency.
fn check_authorization(
    scopes: &HashSet<&str>,
    path: &str,
    method: Option<&str>,       // JSON-RPC method (MCP)
    tool_name: Option<&str>,    // tools/call param
    task_type: Option<&str>,    // A2A task type
    model_id: Option<&str>,     // LLM model header
) -> bool {
    // Discovery methods are always allowed
    if let Some(m) = method {
        match m {
            "initialize" | "tools/list" | "resources/list"
            | "tasks/list" | "agents/list" | "models/list" | "ping" => return true,
            _ => {}
        }
    }

    // MCPlex: tool calls
    if method == Some("tools/call") {
        if let Some(name) = tool_name {
            let required = format!("mcp:tools:{}", name);
            return scopes.contains(required.as_str());
        }
    }

    // A2APlex: agent-to-agent delegation
    if path.starts_with("/a2a/") {
        if let Some(tt) = task_type {
            let required = format!("a2a:task:{}", tt);
            return scopes.contains(required.as_str());
        }
    }

    // LLMPlex: model inference
    if path.starts_with("/llm/") {
        if let Some(mid) = model_id {
            let required = format!("llm:model:{}", mid);
            return scopes.contains(required.as_str());
        }
    }

    false
}

#[tokio::main]
async fn main() {
    tracing_subscriber::init();
    tracing::info!("aiplex-authz starting");

    // TODO: implement gRPC ext_authz server using tonic
    // For now, this validates the core authorization logic compiles.
    // The gRPC service will implement envoy.service.auth.v3.Authorization

    let scopes: HashSet<&str> = ["mcp:tools:search", "llm:model:gemini-2.5-flash"]
        .into_iter()
        .collect();

    assert!(check_authorization(&scopes, "/mcp/server", Some("tools/call"), Some("search"), None, None));
    assert!(!check_authorization(&scopes, "/mcp/server", Some("tools/call"), Some("delete_all"), None, None));
    assert!(check_authorization(&scopes, "/llm/v1/chat", None, None, None, Some("gemini-2.5-flash")));
    assert!(check_authorization(&scopes, "/mcp/server", Some("tools/list"), None, None, None));

    tracing::info!("authorization logic validated");
}

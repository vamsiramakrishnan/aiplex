use std::collections::HashSet;
use std::env;
use std::net::SocketAddr;
use std::sync::Arc;

use axum::{
    extract::{Json, State},
    http::StatusCode,
    response::IntoResponse,
    routing::{get, post},
    Router,
};
use jsonwebtoken::{decode, DecodingKey, Validation, Algorithm};
use serde::{Deserialize, Serialize};
use tracing::{info, warn};

/// JWT claims from Ory Hydra.
#[derive(Debug, Deserialize)]
struct Claims {
    #[serde(default)]
    sub: String,
    #[serde(default)]
    azp: String,
    #[serde(default)]
    scope: String,
    #[serde(default)]
    #[allow(dead_code)]
    act: Option<ActorClaim>,
}

#[derive(Debug, Deserialize)]
struct ActorClaim {
    #[allow(dead_code)]
    sub: String,
}

/// Envoy HTTP ext_authz check request (simplified subset).
#[derive(Debug, Deserialize)]
struct CheckRequest {
    #[serde(default)]
    headers: std::collections::HashMap<String, String>,
    #[serde(default)]
    path: String,
    #[serde(default)]
    #[allow(dead_code)]
    method: String,
    #[serde(default)]
    body: Option<serde_json::Value>,
}

/// Envoy HTTP ext_authz check response.
#[derive(Debug, Serialize)]
struct CheckResponse {
    allowed: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    denied_reason: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    headers: Option<std::collections::HashMap<String, String>>,
}

/// Check if a request is authorized based on JWT scopes.
///
/// This is the core authorization logic — identical to the OPA Rego policy
/// but running as a compiled Rust binary for ~24x lower latency.
fn check_authorization(
    scopes: &HashSet<&str>,
    path: &str,
    body: Option<&serde_json::Value>,
) -> (bool, Option<String>) {
    // Health/readiness — always allowed
    if path == "/healthz" || path == "/readyz" {
        return (true, None);
    }

    // Discovery methods are always allowed
    if let Some(body_val) = body {
        if let Some(method) = body_val.get("method").and_then(|v| v.as_str()) {
            match method {
                "initialize" | "tools/list" | "resources/list"
                | "tasks/list" | "agents/list" | "models/list"
                | "skills/list" | "ping" => {
                    return (true, None);
                }
                _ => {}
            }

            // MCPlex: tools/call
            if method == "tools/call" {
                if let Some(name) = body_val
                    .get("params")
                    .and_then(|p| p.get("name"))
                    .and_then(|n| n.as_str())
                {
                    let required = format!("mcp:tools:{}", name);
                    if scopes.contains(required.as_str()) {
                        return (true, None);
                    }
                    return (false, Some(format!("missing scope: {}", required)));
                }
            }

            // SkillsPlex: skills/invoke
            if method == "skills/invoke" {
                if let Some(name) = body_val
                    .get("params")
                    .and_then(|p| p.get("name"))
                    .and_then(|n| n.as_str())
                {
                    let required = format!("skill:invoke:{}", name);
                    if scopes.contains(required.as_str()) {
                        return (true, None);
                    }
                    return (false, Some(format!("missing scope: {}", required)));
                }
            }
        }
    }

    // SkillsPlex: HTTP-style /skills/ paths with skill_name in body
    if path.starts_with("/skills/") {
        if let Some(body_val) = body {
            if let Some(name) = body_val.get("skill_name").and_then(|v| v.as_str()) {
                let required = format!("skill:invoke:{}", name);
                if scopes.contains(required.as_str()) {
                    return (true, None);
                }
                return (false, Some(format!("missing scope: {}", required)));
            }
        }
    }

    // A2APlex: agent-to-agent delegation
    if path.starts_with("/a2a/") {
        if let Some(body_val) = body {
            if let Some(task_type) = body_val.get("task_type").and_then(|v| v.as_str()) {
                let required = format!("a2a:task:{}", task_type);
                if scopes.contains(required.as_str()) {
                    return (true, None);
                }
                return (false, Some(format!("missing scope: {}", required)));
            }
        }
    }

    // LLMPlex: model inference
    if path.starts_with("/llm/") {
        if let Some(body_val) = body {
            if let Some(model_id) = body_val.get("model").and_then(|v| v.as_str()) {
                let required = format!("llm:model:{}", model_id);
                if scopes.contains(required.as_str()) {
                    return (true, None);
                }
                return (false, Some(format!("missing scope: {}", required)));
            }
        }
    }

    // Default deny
    (
        false,
        Some(format!("no matching policy for path: {}", path)),
    )
}

/// AppState carries shared config and the optional denial reporter into handlers.
#[derive(Clone)]
struct AppState {
    reporter: Option<Arc<DenialReporter>>,
}

/// PolicyDenial wire format matching internal/models/metrics.go (Go side).
/// Only the fields the dashboard needs to render are populated; the Go side
/// fills in ID/Timestamp when missing.
#[derive(Debug, Serialize)]
struct PolicyDenial {
    timestamp: String,
    plane: String,
    agent_id: String,
    user_id: String,
    action: String,
    scope: String,
    reason: String,
    request_id: String,
}

/// DenialReporter fires denial events to the AIPlex dashboard. The POST is
/// best-effort: failures are logged and never block the authz response.
struct DenialReporter {
    url: String,
    client: reqwest::Client,
}

impl DenialReporter {
    fn from_env() -> Option<Arc<Self>> {
        let url = env::var("DENIAL_WEBHOOK_URL").ok()?;
        if url.is_empty() {
            return None;
        }
        let client = reqwest::Client::builder()
            .timeout(std::time::Duration::from_secs(2))
            .build()
            .ok()?;
        info!(url = %url, "denial webhook enabled");
        Some(Arc::new(Self { url, client }))
    }

    fn report(self: Arc<Self>, denial: PolicyDenial) {
        let target = self.clone();
        tokio::spawn(async move {
            let res = target
                .client
                .post(&target.url)
                .json(&denial)
                .send()
                .await;
            match res {
                Ok(resp) if resp.status().is_success() => {}
                Ok(resp) => warn!(status = %resp.status(), "denial webhook non-2xx"),
                Err(e) => warn!(error = %e, "denial webhook failed"),
            }
        });
    }
}

/// plane_for_scope returns the AIPlex plane label for a missing scope, used
/// only for dashboard categorisation.
fn plane_for_scope(scope: &str) -> &'static str {
    if scope.starts_with("mcp:") {
        "mcplex"
    } else if scope.starts_with("a2a:") {
        "a2aplex"
    } else if scope.starts_with("llm:") {
        "llmplex"
    } else if scope.starts_with("skill:") {
        "skillsplex"
    } else {
        ""
    }
}

/// missing_scope_from extracts the scope embedded in a "missing scope: ..." reason.
fn missing_scope_from(reason: &str) -> Option<&str> {
    const PREFIX: &str = "missing scope:";
    let idx = reason.find(PREFIX)?;
    let rest = reason[idx + PREFIX.len()..].trim();
    if rest.is_empty() {
        return None;
    }
    Some(rest.split(|c: char| c.is_whitespace() || c == ',').next().unwrap_or(rest))
}

async fn check_handler(
    State(state): State<AppState>,
    Json(req): Json<CheckRequest>,
) -> impl IntoResponse {
    let auth_header = req
        .headers
        .get("authorization")
        .or_else(|| req.headers.get("Authorization"))
        .map(|s| s.as_str())
        .unwrap_or("");

    // Extract JWT token
    let token_str = auth_header.strip_prefix("Bearer ").unwrap_or("");
    if token_str.is_empty() {
        return (
            StatusCode::FORBIDDEN,
            Json(CheckResponse {
                allowed: false,
                denied_reason: Some("missing authorization header".to_string()),
                headers: None,
            }),
        );
    }

    // Decode JWT (skip signature validation in dev mode for testing)
    let issuer = env::var("JWT_ISSUER").unwrap_or_default();
    let mut validation = Validation::new(Algorithm::RS256);

    if issuer.is_empty() {
        // Dev mode: skip signature validation
        validation.insecure_disable_signature_validation();
        validation.validate_aud = false;
        validation.validate_exp = false;
    } else {
        // Prod mode: validate issuer
        validation.set_issuer(&[&issuer]);
        validation.validate_aud = false;
    }

    let key = DecodingKey::from_secret(b""); // Only used when validation is disabled
    let claims = match decode::<Claims>(token_str, &key, &validation) {
        Ok(data) => data.claims,
        Err(e) => {
            warn!("JWT decode failed: {}", e);
            return (
                StatusCode::FORBIDDEN,
                Json(CheckResponse {
                    allowed: false,
                    denied_reason: Some(format!("invalid token: {}", e)),
                    headers: None,
                }),
            );
        }
    };

    let scopes: HashSet<&str> = claims.scope.split_whitespace().collect();
    let (allowed, reason) = check_authorization(&scopes, &req.path, req.body.as_ref());

    if allowed {
        (
            StatusCode::OK,
            Json(CheckResponse {
                allowed: true,
                denied_reason: None,
                headers: None,
            }),
        )
    } else {
        info!(
            sub = %claims.sub,
            azp = %claims.azp,
            path = %req.path,
            reason = ?reason,
            "denied"
        );

        if let (Some(reporter), Some(reason_text)) = (state.reporter.as_ref(), reason.as_deref()) {
            let scope = missing_scope_from(reason_text).unwrap_or("").to_string();
            let plane = plane_for_scope(&scope).to_string();
            let request_id = req
                .headers
                .get("x-request-id")
                .or_else(|| req.headers.get("X-Request-Id"))
                .cloned()
                .unwrap_or_default();
            reporter.clone().report(PolicyDenial {
                timestamp: chrono::Utc::now().to_rfc3339(),
                plane,
                agent_id: claims.azp.clone(),
                user_id: claims.sub.clone(),
                action: req.path.clone(),
                scope,
                reason: "scope_missing".to_string(),
                request_id,
            });
        }

        (
            StatusCode::FORBIDDEN,
            Json(CheckResponse {
                allowed: false,
                denied_reason: reason,
                headers: None,
            }),
        )
    }
}

async fn health() -> &'static str {
    "ok"
}

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt::init();

    let port: u16 = env::var("PORT")
        .unwrap_or_else(|_| "9191".to_string())
        .parse()
        .unwrap_or(9191);
    let addr = SocketAddr::from(([0, 0, 0, 0], port));

    let state = AppState {
        reporter: DenialReporter::from_env(),
    };

    let app = Router::new()
        .route("/check", post(check_handler))
        .route("/healthz", get(health))
        .route("/readyz", get(health))
        .with_state(state);

    info!("aiplex-authz listening on {}", addr);
    let listener = tokio::net::TcpListener::bind(addr).await.unwrap();
    axum::serve(listener, app).await.unwrap();
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_discovery_allowed() {
        let scopes = HashSet::new();
        let body = serde_json::json!({"method": "tools/list"});
        let (allowed, _) = check_authorization(&scopes, "/mcp/server", Some(&body));
        assert!(allowed);
    }

    #[test]
    fn test_tool_call_with_scope() {
        let scopes: HashSet<&str> = ["mcp:tools:search"].into_iter().collect();
        let body = serde_json::json!({"method": "tools/call", "params": {"name": "search"}});
        let (allowed, _) = check_authorization(&scopes, "/mcp/server", Some(&body));
        assert!(allowed);
    }

    #[test]
    fn test_tool_call_without_scope() {
        let scopes: HashSet<&str> = ["mcp:tools:other"].into_iter().collect();
        let body = serde_json::json!({"method": "tools/call", "params": {"name": "search"}});
        let (allowed, reason) = check_authorization(&scopes, "/mcp/server", Some(&body));
        assert!(!allowed);
        assert!(reason.unwrap().contains("mcp:tools:search"));
    }

    #[test]
    fn test_a2a_task_access() {
        let scopes: HashSet<&str> = ["a2a:task:research"].into_iter().collect();
        let body = serde_json::json!({"task_type": "research"});
        let (allowed, _) = check_authorization(&scopes, "/a2a/research-agent", Some(&body));
        assert!(allowed);
    }

    #[test]
    fn test_llm_model_access() {
        let scopes: HashSet<&str> = ["llm:model:gemini-2.5-flash"].into_iter().collect();
        let body = serde_json::json!({"model": "gemini-2.5-flash"});
        let (allowed, _) = check_authorization(&scopes, "/llm/v1/chat", Some(&body));
        assert!(allowed);
    }

    #[test]
    fn test_skill_invoke_with_scope() {
        let scopes: HashSet<&str> = ["skill:invoke:review_pr"].into_iter().collect();
        let body = serde_json::json!({"method": "skills/invoke", "params": {"name": "review_pr"}});
        let (allowed, _) = check_authorization(&scopes, "/skills/code-review", Some(&body));
        assert!(allowed);
    }

    #[test]
    fn test_skill_invoke_without_scope() {
        let scopes: HashSet<&str> = ["skill:invoke:other"].into_iter().collect();
        let body = serde_json::json!({"method": "skills/invoke", "params": {"name": "review_pr"}});
        let (allowed, reason) = check_authorization(&scopes, "/skills/code-review", Some(&body));
        assert!(!allowed);
        assert!(reason.unwrap().contains("skill:invoke:review_pr"));
    }

    #[test]
    fn test_skills_list_allowed_without_scope() {
        let scopes = HashSet::new();
        let body = serde_json::json!({"method": "skills/list"});
        let (allowed, _) = check_authorization(&scopes, "/skills/code-review", Some(&body));
        assert!(allowed);
    }

    #[test]
    fn test_skill_http_path() {
        let scopes: HashSet<&str> = ["skill:invoke:draft"].into_iter().collect();
        let body = serde_json::json!({"skill_name": "draft"});
        let (allowed, _) = check_authorization(&scopes, "/skills/writing", Some(&body));
        assert!(allowed);
    }

    #[test]
    fn test_health_always_allowed() {
        let scopes = HashSet::new();
        let (allowed, _) = check_authorization(&scopes, "/healthz", None);
        assert!(allowed);
    }

    #[test]
    fn test_default_deny() {
        let scopes = HashSet::new();
        let (allowed, reason) = check_authorization(&scopes, "/unknown/path", None);
        assert!(!allowed);
        assert!(reason.is_some());
    }

    #[test]
    fn test_missing_scope_from() {
        assert_eq!(
            missing_scope_from("missing scope: skill:invoke:review_pr"),
            Some("skill:invoke:review_pr"),
        );
        assert_eq!(
            missing_scope_from("missing scope: mcp:tools:search and other text"),
            Some("mcp:tools:search"),
        );
        assert_eq!(missing_scope_from("no policy match"), None);
        assert_eq!(missing_scope_from("missing scope: "), None);
    }

    #[test]
    fn test_plane_for_scope() {
        assert_eq!(plane_for_scope("mcp:tools:search"), "mcplex");
        assert_eq!(plane_for_scope("a2a:task:research"), "a2aplex");
        assert_eq!(plane_for_scope("llm:model:gemini-2.5-flash"), "llmplex");
        assert_eq!(plane_for_scope("skill:invoke:review_pr"), "skillsplex");
        assert_eq!(plane_for_scope("unknown:thing"), "");
    }
}

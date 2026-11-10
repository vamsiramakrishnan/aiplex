pub mod denial;
pub mod jwt;

use std::collections::HashSet;

/// Check if a request is authorized based on JWT scopes.
///
/// Evaluated locally without a policy-service network call. Discovery is
/// constrained to its protocol path so a body field cannot bypass model access.
pub fn check_authorization(
    scopes: &HashSet<&str>,
    path: &str,
    body: Option<&serde_json::Value>,
) -> (bool, Option<String>) {
    // Health/readiness — always allowed
    if path == "/healthz" || path == "/readyz" {
        return (true, None);
    }

    // Discovery requires authentication and a matching protocol path.
    if let Some(body_val) = body {
        if let Some(method) = body_val.get("method").and_then(|v| v.as_str()) {
            let discovery = if path.starts_with("/mcp/") {
                matches!(
                    method,
                    "initialize" | "tools/list" | "resources/list" | "ping"
                )
            } else if path.starts_with("/skills/") {
                matches!(method, "skills/list" | "ping")
            } else if path.starts_with("/a2a/") {
                matches!(method, "tasks/list" | "agents/list" | "ping")
            } else {
                false
            };
            if discovery {
                return (true, None);
            }

            // MCPlex: tools/call
            if path.starts_with("/mcp/") && method == "tools/call" {
                if let Some(name) = body_val
                    .get("params")
                    .and_then(|p| p.get("name"))
                    .and_then(|n| n.as_str())
                {
                    let required = format!("mcp:tools:{}", name);
                    if has_scope(scopes, &required) {
                        return (true, None);
                    }
                    return (false, Some(format!("missing scope: {}", required)));
                }
            }

            // SkillsPlex: skills/invoke
            if path.starts_with("/skills/") && method == "skills/invoke" {
                if let Some(name) = body_val
                    .get("params")
                    .and_then(|p| p.get("name"))
                    .and_then(|n| n.as_str())
                {
                    let required = format!("skill:invoke:{}", name);
                    if has_scope(scopes, &required) {
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
                if has_scope(scopes, &required) {
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
                if has_scope(scopes, &required) {
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
                if has_scope(scopes, &required) {
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

fn has_scope(scopes: &HashSet<&str>, required: &str) -> bool {
    if scopes.contains(required) {
        return true;
    }
    required
        .rsplit_once(':')
        .is_some_and(|(namespace, _)| scopes.contains(format!("{namespace}:*").as_str()))
}

/// plane_for_scope returns the AIPlex plane label for a missing scope, used
/// only for dashboard categorisation.
pub fn plane_for_scope(scope: &str) -> &'static str {
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
pub fn missing_scope_from(reason: &str) -> Option<&str> {
    const PREFIX: &str = "missing scope:";
    let idx = reason.find(PREFIX)?;
    let rest = reason[idx + PREFIX.len()..].trim();
    if rest.is_empty() {
        return None;
    }
    Some(
        rest.split(|c: char| c.is_whitespace() || c == ',')
            .next()
            .unwrap_or(rest),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn discovery_body_cannot_bypass_model_authorization() {
        let body = serde_json::json!({"method": "tools/list", "model": "expensive"});
        assert!(!check_authorization(&HashSet::new(), "/llm/chat", Some(&body)).0);
    }

    #[test]
    fn tool_scope_cannot_authorize_another_plane() {
        let scopes = ["mcp:tools:search"].into_iter().collect();
        let body = serde_json::json!({"method":"tools/call","params":{"name":"search"}});
        assert!(!check_authorization(&scopes, "/llm/chat", Some(&body)).0);
    }

    #[test]
    fn namespace_wildcard_is_bounded() {
        let scopes = ["llm:model:*"].into_iter().collect();
        let body = serde_json::json!({"model":"gemini"});
        assert!(check_authorization(&scopes, "/llm/chat", Some(&body)).0);
        let body = serde_json::json!({"method":"tools/call","params":{"name":"search"}});
        assert!(!check_authorization(&scopes, "/mcp/server", Some(&body)).0);
    }

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

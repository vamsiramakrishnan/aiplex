use std::collections::HashSet;
use std::env;
use std::net::SocketAddr;
use std::sync::Arc;

use aiplex_authz::{
    check_authorization,
    denial::{
        clean, header, trace_id_from_traceparent, DenialReporter, PolicyDenial, MAX_ACTION, MAX_ID,
        MAX_REQUEST_ID, MAX_SCOPE,
    },
    jwt::{Claims, Config as JwtConfig, Verifier},
    missing_scope_from, plane_for_scope,
};
use axum::{
    body::to_bytes,
    extract::{Json, Request, State},
    http::StatusCode,
    response::IntoResponse,
    routing::{get, post},
    Router,
};
use jsonwebtoken::{decode, Algorithm, DecodingKey, Validation};
use serde::{Deserialize, Serialize};
use tracing::{info, warn};

/// Explicit JSON adapter API. Native Envoy HTTP ext_authz uses /authorize*.
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

/// JSON adapter response; HTTP status determines allow/deny.
#[derive(Debug, Serialize)]
struct CheckResponse {
    allowed: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    denied_reason: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    headers: Option<std::collections::HashMap<String, String>>,
}

/// AppState carries shared config and the optional denial reporter into handlers.
#[derive(Clone)]
struct AppState {
    reporter: Option<Arc<DenialReporter>>,
    verifier: Option<Arc<Verifier>>,
}

async fn check_handler(
    State(state): State<AppState>,
    Json(req): Json<CheckRequest>,
) -> impl IntoResponse {
    evaluate(&state, req).await
}

// Envoy HTTP ext_authz sends the original headers and body, with its configured
// /authorize prefix prepended to the original path. Never trust client-supplied
// original-path headers to choose the resource being authorized.
async fn authorize_handler(State(state): State<AppState>, request: Request) -> impl IntoResponse {
    if request
        .headers()
        .get("x-envoy-auth-partial-body")
        .is_some_and(|v| v == "true")
    {
        return (
            StatusCode::FORBIDDEN,
            Json(CheckResponse {
                allowed: false,
                denied_reason: Some("partial authorization body rejected".into()),
                headers: None,
            }),
        );
    }
    let path = request
        .uri()
        .path()
        .strip_prefix("/authorize")
        .unwrap_or("")
        .to_string();
    if !path.starts_with('/') {
        return (
            StatusCode::FORBIDDEN,
            Json(CheckResponse {
                allowed: false,
                denied_reason: Some("invalid authorization path".into()),
                headers: None,
            }),
        );
    }
    let method = request.method().to_string();
    let headers = request
        .headers()
        .iter()
        .filter_map(|(k, v)| v.to_str().ok().map(|v| (k.to_string(), v.to_string())))
        .collect();
    let bytes = match to_bytes(request.into_body(), 65_536).await {
        Ok(bytes) => bytes,
        Err(_) => {
            return (
                StatusCode::FORBIDDEN,
                Json(CheckResponse {
                    allowed: false,
                    denied_reason: Some("authorization body exceeds limit".into()),
                    headers: None,
                }),
            )
        }
    };
    let body = if bytes.is_empty() {
        None
    } else {
        match serde_json::from_slice(&bytes) {
            Ok(body) => Some(body),
            Err(_) => {
                return (
                    StatusCode::FORBIDDEN,
                    Json(CheckResponse {
                        allowed: false,
                        denied_reason: Some("invalid request body".into()),
                        headers: None,
                    }),
                )
            }
        }
    };
    evaluate(
        &state,
        CheckRequest {
            headers,
            path,
            method,
            body,
        },
    )
    .await
}

async fn evaluate(state: &AppState, req: CheckRequest) -> (StatusCode, Json<CheckResponse>) {
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

    let verified = if let Some(verifier) = &state.verifier {
        verifier.verify(token_str).await
    } else {
        // Explicit development mode is startup-restricted to a loopback listener.
        let mut validation = Validation::new(Algorithm::RS256);
        validation.insecure_disable_signature_validation();
        validation.validate_aud = false;
        validation.required_spec_claims.clear();
        decode::<Claims>(token_str, &DecodingKey::from_secret(b""), &validation)
            .map(|data| data.claims)
            .map_err(|_| "invalid development JWT".to_string())
    };
    let claims = match verified {
        Ok(claims) => claims,
        Err(error) => {
            warn!(reason = %error, "JWT validation failed");
            return (
                StatusCode::FORBIDDEN,
                Json(CheckResponse {
                    allowed: false,
                    denied_reason: Some("invalid authentication token".to_string()),
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
            let request_id = header(&req.headers, "x-request-id").unwrap_or("");
            // The trace id of the caller's W3C traceparent, so a denial can be
            // found from the trace that was refused. An invalid header gives none.
            let trace_id = header(&req.headers, "traceparent")
                .and_then(trace_id_from_traceparent)
                .unwrap_or_default();
            reporter.clone().report(PolicyDenial {
                plane,
                agent_id: clean(&claims.azp, MAX_ID),
                user_id: clean(&claims.sub, MAX_ID),
                action: clean(&req.path, MAX_ACTION),
                scope: clean(&scope, MAX_SCOPE),
                reason: "scope_missing".to_string(),
                request_id: clean(request_id, MAX_REQUEST_ID),
                trace_id,
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

async fn readiness(State(state): State<AppState>) -> StatusCode {
    match &state.verifier {
        Some(verifier) if verifier.ready().await.is_err() => StatusCode::SERVICE_UNAVAILABLE,
        _ => StatusCode::OK,
    }
}

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt::init();

    let port: u16 = env::var("PORT")
        .unwrap_or_else(|_| "9191".to_string())
        .parse()
        .unwrap_or(9191);
    let bind = env::var("AUTHZ_BIND").unwrap_or_else(|_| "0.0.0.0".into());
    let addr = SocketAddr::new(
        bind.parse().expect("AUTHZ_BIND must be an IP address"),
        port,
    );
    let development = env::var("AUTHZ_DEV_MODE").as_deref() == Ok("true");
    if development && !addr.ip().is_loopback() {
        panic!("AUTHZ_DEV_MODE requires a loopback AUTHZ_BIND");
    }
    let verifier = if development {
        warn!("explicit loopback-only development authentication enabled");
        None
    } else {
        let required = |name: &str| env::var(name).unwrap_or_default();
        Some(Arc::new(
            Verifier::new(JwtConfig {
                issuer: required("JWT_ISSUER"),
                audience: required("JWT_AUDIENCE"),
                jwks_url: required("JWT_JWKS_URL"),
            })
            .await
            .expect("JWT verifier initialization failed"),
        ))
    };

    let state = AppState {
        reporter: DenialReporter::from_env(),
        verifier,
    };

    let app = Router::new()
        .route("/check", post(check_handler))
        .route("/healthz", get(health))
        .route("/readyz", get(readiness))
        .fallback(authorize_handler)
        .with_state(state);

    info!("aiplex-authz listening on {}", addr);
    let listener = tokio::net::TcpListener::bind(addr).await.unwrap();
    axum::serve(listener, app).await.unwrap();
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::body::Body;
    use jsonwebtoken::{encode, EncodingKey, Header};
    use serde_json::json;

    fn development_state() -> AppState {
        AppState {
            reporter: None,
            verifier: None,
        }
    }

    fn bearer() -> String {
        let claims = json!({"sub":"test-only", "exp":chrono::Utc::now().timestamp()+300});
        let token = encode(
            &Header::new(Algorithm::RS256),
            &claims,
            &EncodingKey::from_rsa_pem(include_bytes!("../tests/fixtures/test-only-private.pem"))
                .unwrap(),
        )
        .unwrap();
        format!("Bearer {token}")
    }

    #[tokio::test]
    async fn native_http_adapter_uses_original_uri_and_rejects_spoofed_path_header() {
        let request = Request::builder()
            .method("POST")
            .uri("/authorize/mcp/server")
            .header("authorization", bearer())
            .body(Body::from(r#"{"method":"tools/list"}"#))
            .unwrap();
        assert_eq!(
            authorize_handler(State(development_state()), request)
                .await
                .into_response()
                .status(),
            StatusCode::OK
        );
        let request = Request::builder()
            .method("POST")
            .uri("/authorize/llm/chat")
            .header("authorization", bearer())
            .header("x-envoy-original-path", "/mcp/server")
            .body(Body::from(r#"{"method":"tools/list","model":"expensive"}"#))
            .unwrap();
        assert_eq!(
            authorize_handler(State(development_state()), request)
                .await
                .into_response()
                .status(),
            StatusCode::FORBIDDEN
        );
    }

    #[tokio::test]
    async fn native_http_adapter_fails_closed_on_missing_token_or_partial_body() {
        for (path, body, token) in [
            (
                "/authorize/mcp/server",
                r#"{"method":"tools/list"}"#.to_string(),
                false,
            ),
            ("/authorize/mcp/server", "{".to_string(), true),
            ("/authorize/mcp/server", " ".repeat(65_537), true),
            (
                "/unrecognized/mcp/server",
                r#"{"method":"tools/list"}"#.to_string(),
                true,
            ),
        ] {
            let mut builder = Request::builder().method("POST").uri(path);
            if token {
                builder = builder.header("authorization", bearer());
            }
            let request = builder.body(Body::from(body)).unwrap();
            assert_eq!(
                authorize_handler(State(development_state()), request)
                    .await
                    .into_response()
                    .status(),
                StatusCode::FORBIDDEN
            );
        }
    }
    /// A denial reaches the API with the verified principal, the request id and
    /// the trace id of the inbound traceparent; an allowed request reports nothing.
    #[tokio::test]
    async fn denial_is_reported_with_principal_and_trace_id() {
        use std::sync::Mutex;
        type Post = (Option<String>, serde_json::Value);
        let seen: Arc<Mutex<Vec<Post>>> = Arc::default();
        let sink = seen.clone();
        let app = Router::new().route(
            "/internal/policy-denials",
            post(
                move |headers: axum::http::HeaderMap, body: axum::body::Bytes| {
                    let sink = sink.clone();
                    async move {
                        sink.lock().unwrap().push((
                            headers
                                .get("authorization")
                                .and_then(|v| v.to_str().ok())
                                .map(str::to_string),
                            serde_json::from_slice(&body).unwrap(),
                        ));
                        StatusCode::CREATED
                    }
                },
            ),
        );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });

        let token =
            std::env::temp_dir().join(format!("aiplex-authz-main-test-{}", std::process::id()));
        std::fs::write(&token, "shared-secret\n").unwrap();
        let state = AppState {
            reporter: Some(
                DenialReporter::new(
                    format!("http://{addr}/internal/policy-denials"),
                    token.clone(),
                )
                .unwrap(),
            ),
            verifier: None,
        };
        let _ = std::fs::remove_file(&token);

        // Allowed: nothing is reported.
        let allowed = Request::builder()
            .method("POST")
            .uri("/authorize/mcp/server")
            .header("authorization", bearer())
            .body(Body::from(r#"{"method":"tools/list"}"#))
            .unwrap();
        assert_eq!(
            authorize_handler(State(state.clone()), allowed)
                .await
                .into_response()
                .status(),
            StatusCode::OK
        );

        let denied = Request::builder()
            .method("POST")
            .uri("/authorize/mcp/server")
            .header("authorization", bearer())
            .header("x-request-id", "req-42")
            .header(
                "traceparent",
                "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
            )
            .body(Body::from(
                r#"{"method":"tools/call","params":{"name":"search"}}"#,
            ))
            .unwrap();
        assert_eq!(
            authorize_handler(State(state), denied)
                .await
                .into_response()
                .status(),
            StatusCode::FORBIDDEN
        );

        for _ in 0..50 {
            if !seen.lock().unwrap().is_empty() {
                break;
            }
            tokio::time::sleep(std::time::Duration::from_millis(20)).await;
        }
        let seen = seen.lock().unwrap();
        assert_eq!(seen.len(), 1, "exactly the denial is reported");
        assert_eq!(seen[0].0.as_deref(), Some("Bearer shared-secret"));
        let body = &seen[0].1;
        assert_eq!(body["trace_id"], "4bf92f3577b34da6a3ce929d0e0e4736");
        assert_eq!(body["request_id"], "req-42");
        assert_eq!(body["user_id"], "test-only");
        assert_eq!(body["scope"], "mcp:tools:search");
        assert_eq!(body["plane"], "mcplex");
        assert_eq!(body["reason"], "scope_missing");
        assert_eq!(body["action"], "/mcp/server");
    }

    #[tokio::test]
    async fn native_http_adapter_rejects_gateway_partial_body_marker() {
        let request = Request::builder()
            .method("POST")
            .uri("/authorize/mcp/server")
            .header("authorization", bearer())
            .header("x-envoy-auth-partial-body", "true")
            .body(Body::from(r#"{"method":"tools/list"}"#))
            .unwrap();
        assert_eq!(
            authorize_handler(State(development_state()), request)
                .await
                .into_response()
                .status(),
            StatusCode::FORBIDDEN
        );
    }
}

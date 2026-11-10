use std::sync::{Arc, Mutex};

use aiplex_authz::denial::{DenialReporter, PolicyDenial};
use axum::{
    body::Bytes,
    http::{HeaderMap, StatusCode},
    routing::post,
    Router,
};

type Post = (Option<String>, String);

#[derive(Clone, Default)]
struct Seen(Arc<Mutex<Vec<Post>>>);

/// Starts a mock API on 127.0.0.1 that records the Authorization header and
/// body of every post, and answers with `status`.
async fn mock_api(status: u16, seen: Seen) -> String {
    let app = Router::new().route(
        "/internal/policy-denials",
        post(move |headers: HeaderMap, body: Bytes| {
            let seen = seen.clone();
            async move {
                seen.0.lock().unwrap().push((
                    headers
                        .get("authorization")
                        .and_then(|v| v.to_str().ok())
                        .map(str::to_string),
                    String::from_utf8_lossy(&body).to_string(),
                ));
                StatusCode::from_u16(status).unwrap()
            }
        }),
    );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    format!("http://{addr}/internal/policy-denials")
}

fn token_file(contents: &str) -> (tempdir::Guard, std::path::PathBuf) {
    let guard = tempdir::Guard::new();
    let path = guard.path.join("token");
    std::fs::write(&path, contents).unwrap();
    (guard, path)
}

/// A tiny temp-dir guard so the tests need no extra dependency.
mod tempdir {
    pub struct Guard {
        pub path: std::path::PathBuf,
    }
    impl Guard {
        pub fn new() -> Self {
            let path = std::env::temp_dir().join(format!(
                "aiplex-authz-test-{}-{}",
                std::process::id(),
                std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap()
                    .as_nanos()
            ));
            std::fs::create_dir_all(&path).unwrap();
            Guard { path }
        }
    }
    impl Drop for Guard {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.path);
        }
    }
}

fn denial() -> PolicyDenial {
    PolicyDenial {
        plane: "mcplex".into(),
        agent_id: "agent-1".into(),
        user_id: "user-1".into(),
        action: "/mcp/server".into(),
        scope: "mcp:tools:search".into(),
        reason: "scope_missing".into(),
        request_id: "req-1".into(),
        trace_id: "4bf92f3577b34da6a3ce929d0e0e4736".into(),
    }
}

#[tokio::test]
async fn posts_the_denial_with_the_token_read_from_the_file() {
    let seen = Seen::default();
    let url = mock_api(201, seen.clone()).await;
    let (_guard, path) = token_file("s3cret-value\n");
    let reporter = DenialReporter::new(url, path).unwrap();
    reporter.send(&denial()).await.unwrap();

    let seen = seen.0.lock().unwrap();
    assert_eq!(seen.len(), 1);
    assert_eq!(seen[0].0.as_deref(), Some("Bearer s3cret-value"));
    let body: serde_json::Value = serde_json::from_str(&seen[0].1).unwrap();
    assert_eq!(body["trace_id"], "4bf92f3577b34da6a3ce929d0e0e4736");
    assert_eq!(body["agent_id"], "agent-1");
    assert_eq!(body["scope"], "mcp:tools:search");
    // The wire schema is closed: no tenant, no timestamp, no id.
    let mut keys: Vec<_> = body.as_object().unwrap().keys().cloned().collect();
    keys.sort();
    assert_eq!(
        keys,
        [
            "action",
            "agent_id",
            "plane",
            "reason",
            "request_id",
            "scope",
            "trace_id",
            "user_id"
        ]
    );
}

#[tokio::test]
async fn an_error_status_is_reported_without_the_token_or_url() {
    let seen = Seen::default();
    let url = mock_api(401, seen).await;
    let (_guard, path) = token_file("s3cret-value");
    let reporter = DenialReporter::new(url.clone(), path).unwrap();
    let err = reporter.send(&denial()).await.unwrap_err();
    assert!(err.contains("401"), "{err}");
    assert!(
        !err.contains("s3cret-value") && !err.contains(&url),
        "{err}"
    );
}

#[tokio::test]
async fn an_unreachable_server_error_carries_neither_token_nor_url() {
    let (_guard, path) = token_file("s3cret-value");
    // Port 1 on loopback refuses connections.
    let url = "http://127.0.0.1:1/internal/policy-denials".to_string();
    let reporter = DenialReporter::new(url.clone(), path).unwrap();
    let err = reporter.send(&denial()).await.unwrap_err();
    assert!(
        !err.contains("s3cret-value") && !err.contains(&url),
        "{err}"
    );
}

#[tokio::test]
async fn redirects_are_not_followed_so_the_token_stays_put() {
    let other = Seen::default();
    let other_url = mock_api(200, other.clone()).await;
    let redirector = Router::new().route(
        "/internal/policy-denials",
        post(move || {
            let location = other_url.clone();
            async move { (StatusCode::TEMPORARY_REDIRECT, [("location", location)]) }
        }),
    );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move { axum::serve(listener, redirector).await.unwrap() });

    let (_guard, path) = token_file("s3cret-value");
    let reporter =
        DenialReporter::new(format!("http://{addr}/internal/policy-denials"), path).unwrap();
    assert!(reporter.send(&denial()).await.is_err());
    assert!(
        other.0.lock().unwrap().is_empty(),
        "the redirect target received a request"
    );
}

#[tokio::test]
async fn construction_fails_closed_without_a_usable_token_or_url() {
    let url = "http://127.0.0.1:9/x".to_string();
    let dir = tempdir::Guard::new();
    assert!(DenialReporter::new(url.clone(), dir.path.join("absent")).is_err());
    for bad in ["", "   \n", "two words", "tab\there"] {
        let (_g, path) = token_file(bad);
        assert!(DenialReporter::new(url.clone(), path).is_err(), "{bad:?}");
    }
    let (_g, path) = token_file("ok");
    for bad_url in ["", "not a url", "ftp://host/x", "http://user:pw@host/x"] {
        assert!(
            DenialReporter::new(bad_url.to_string(), path.clone()).is_err(),
            "{bad_url:?}"
        );
    }
}

#[tokio::test]
async fn report_is_fire_and_forget_and_survives_a_dead_server() {
    let (_guard, path) = token_file("s3cret-value");
    let reporter = DenialReporter::new("http://127.0.0.1:1/x".into(), path).unwrap();
    reporter.clone().report(denial());
    // No panic and no blocking; give the spawned task a moment to fail quietly.
    tokio::time::sleep(std::time::Duration::from_millis(100)).await;
}

#[test]
fn from_env_is_off_without_a_token_file() {
    // This is the only test in this binary that touches these variables.
    std::env::set_var("DENIAL_WEBHOOK_URL", "http://127.0.0.1:9/x");
    std::env::remove_var("DENIAL_TOKEN_FILE");
    assert!(
        DenialReporter::from_env().is_none(),
        "reporting must not start without a token"
    );
    std::env::remove_var("DENIAL_WEBHOOK_URL");
    assert!(DenialReporter::from_env().is_none());
}

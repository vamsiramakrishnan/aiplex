//! Best-effort reporting of authorization denials to the AIPlex API.
//!
//! The sidecar posts one JSON object per denial to the API's internal route
//! (`POST /internal/policy-denials`). The route is guarded by a shared bearer
//! token that both sides read from a Kubernetes Secret: the sidecar reads it
//! from a mounted file (`DENIAL_TOKEN_FILE`), never from the environment or
//! configuration, and the value is never logged. Without a readable token the
//! reporter is not created, so nothing is ever sent unauthenticated.
//!
//! Only denials of tokens that passed signature verification are reported, so
//! unauthenticated traffic cannot fill the API's denial store through the
//! sidecar. The payload carries no tenant: the API stores denials as
//! deployment-wide operator data and does not accept a tenant from the sidecar.
//! Reporting never blocks or changes the authorization decision.

use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use serde::Serialize;
use tracing::{info, warn};

/// Field limits. The API enforces the same limits and refuses a larger value,
/// so the sidecar truncates before sending.
pub const MAX_ID: usize = 256;
pub const MAX_ACTION: usize = 512;
pub const MAX_SCOPE: usize = 512;
pub const MAX_REQUEST_ID: usize = 128;

/// How long a token read from the mounted file is reused. Kubernetes updates a
/// mounted Secret in place when it rotates, so the file is re-read.
const TOKEN_TTL: Duration = Duration::from_secs(30);
const MAX_IN_FLIGHT: usize = 32;

/// Wire format of `POST /internal/policy-denials` (internal/api/policy_denials.go).
/// The server assigns the id and timestamp.
#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct PolicyDenial {
    pub plane: String,
    pub agent_id: String,
    pub user_id: String,
    pub action: String,
    pub scope: String,
    pub reason: String,
    pub request_id: String,
    pub trace_id: String,
}

/// Removes control characters and truncates to `max` characters.
pub fn clean(value: &str, max: usize) -> String {
    value
        .chars()
        .filter(|c| !c.is_control())
        .take(max)
        .collect()
}

/// Case-insensitive header lookup in the request's header map.
pub fn header<'a>(headers: &'a HashMap<String, String>, name: &str) -> Option<&'a str> {
    headers
        .iter()
        .find(|(k, _)| k.eq_ignore_ascii_case(name))
        .map(|(_, v)| v.as_str())
}

/// Returns the trace id of a W3C `traceparent` header
/// (`version-traceid-parentid-flags`), or None when it is not a valid one.
/// Version `ff` and an all-zero trace or parent id are invalid.
pub fn trace_id_from_traceparent(value: &str) -> Option<String> {
    let parts: Vec<&str> = value.trim().split('-').collect();
    if parts.len() < 4 {
        return None;
    }
    let (version, trace, parent, flags) = (parts[0], parts[1], parts[2], parts[3]);
    let hex = |s: &str, n: usize| {
        s.len() == n
            && s.bytes()
                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
    };
    if !hex(version, 2) || version == "ff" || !hex(trace, 32) || !hex(parent, 16) || !hex(flags, 2)
    {
        return None;
    }
    // Version 00 has exactly four fields.
    if version == "00" && parts.len() != 4 {
        return None;
    }
    if trace.bytes().all(|b| b == b'0') || parent.bytes().all(|b| b == b'0') {
        return None;
    }
    Some(trace.to_string())
}

struct CachedToken {
    value: String,
    read_at: Instant,
}

/// DenialReporter posts denials to the API. Posts are best-effort: failures are
/// logged without the token and never affect the authorization response.
pub struct DenialReporter {
    url: String,
    token_file: PathBuf,
    token: Mutex<Option<CachedToken>>,
    client: reqwest::Client,
    in_flight: Arc<tokio::sync::Semaphore>,
}

impl DenialReporter {
    /// Builds a reporter from `DENIAL_WEBHOOK_URL` and `DENIAL_TOKEN_FILE`.
    /// Returns None (reporting off) when the URL is unset, and also, with an
    /// error log, when the URL is set but the token cannot be read: the
    /// reporter fails closed rather than posting without credentials.
    pub fn from_env() -> Option<Arc<Self>> {
        let url = std::env::var("DENIAL_WEBHOOK_URL").ok()?;
        if url.is_empty() {
            return None;
        }
        let token_file = match std::env::var("DENIAL_TOKEN_FILE") {
            Ok(path) if !path.is_empty() => PathBuf::from(path),
            _ => {
                tracing::error!(
                    "DENIAL_WEBHOOK_URL is set but DENIAL_TOKEN_FILE is not; denial reporting disabled"
                );
                return None;
            }
        };
        match Self::new(url, token_file) {
            Ok(reporter) => Some(reporter),
            Err(error) => {
                tracing::error!(%error, "denial reporting disabled");
                None
            }
        }
    }

    pub fn new(url: String, token_file: PathBuf) -> Result<Arc<Self>, String> {
        let parsed = reqwest::Url::parse(&url)
            .map_err(|_| "DENIAL_WEBHOOK_URL is not a valid URL".to_string())?;
        if !matches!(parsed.scheme(), "http" | "https") || parsed.host_str().is_none() {
            return Err("DENIAL_WEBHOOK_URL must be an http or https URL".into());
        }
        if !parsed.username().is_empty() || parsed.password().is_some() {
            return Err("DENIAL_WEBHOOK_URL must not carry credentials".into());
        }
        let reporter = Self {
            url,
            token_file,
            token: Mutex::new(None),
            client: reqwest::Client::builder()
                .timeout(Duration::from_secs(2))
                // A redirect would resend the bearer token to another place.
                .redirect(reqwest::redirect::Policy::none())
                .build()
                .map_err(|e| format!("http client: {e}"))?,
            in_flight: Arc::new(tokio::sync::Semaphore::new(MAX_IN_FLIGHT)),
        };
        // Fail at startup, not at the first denial, when the secret is absent.
        reporter.token()?;
        info!(
            host = parsed.host_str().unwrap_or(""),
            "denial reporting enabled"
        );
        Ok(Arc::new(reporter))
    }

    fn token(&self) -> Result<String, String> {
        let mut cached = self
            .token
            .lock()
            .map_err(|_| "token cache poisoned".to_string())?;
        if let Some(t) = cached.as_ref() {
            if t.read_at.elapsed() < TOKEN_TTL {
                return Ok(t.value.clone());
            }
        }
        let raw = std::fs::read_to_string(&self.token_file)
            .map_err(|_| "denial token file unreadable".to_string())?;
        let value = raw.trim().to_string();
        if value.is_empty() || value.chars().any(|c| c.is_whitespace() || c.is_control()) {
            return Err("denial token file is empty or malformed".into());
        }
        *cached = Some(CachedToken {
            value: value.clone(),
            read_at: Instant::now(),
        });
        Ok(value)
    }

    /// Sends one denial and returns the outcome. Used by `report` and tests.
    pub async fn send(&self, denial: &PolicyDenial) -> Result<(), String> {
        let token = self.token()?;
        let resp = self
            .client
            .post(&self.url)
            .bearer_auth(token)
            .json(denial)
            .send()
            .await
            .map_err(|e| format!("request failed: {}", e.without_url()))?;
        if resp.status().is_success() {
            Ok(())
        } else {
            Err(format!("non-2xx status {}", resp.status()))
        }
    }

    /// Fire and forget. At most 32 posts are in flight; further denials are
    /// dropped with a warning rather than queued.
    pub fn report(self: Arc<Self>, denial: PolicyDenial) {
        let Ok(permit) = self.in_flight.clone().try_acquire_owned() else {
            warn!("denial reporting saturated; event omitted");
            return;
        };
        tokio::spawn(async move {
            let _permit = permit;
            if let Err(error) = self.send(&denial).await {
                warn!(%error, "denial report failed");
            }
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn traceparent_yields_the_trace_id_only_when_valid() {
        let good = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";
        assert_eq!(
            trace_id_from_traceparent(good).as_deref(),
            Some("4bf92f3577b34da6a3ce929d0e0e4736")
        );
        for bad in [
            "",
            "garbage",
            "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",
            "00-00000000000000000000000000000000-00f067aa0ba902b7-01",
            "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
            "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
            "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01",
            "00-4bf92f3577b34da6a3ce929d0e0e47-00f067aa0ba902b7-01",
            "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-extra",
        ] {
            assert_eq!(trace_id_from_traceparent(bad), None, "{bad}");
        }
        // A future version may append fields.
        assert!(trace_id_from_traceparent(
            "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-x"
        )
        .is_some());
    }

    #[test]
    fn clean_strips_control_characters_and_truncates_on_characters() {
        assert_eq!(clean("a\nb\u{0}c\tz", 10), "abcz");
        assert_eq!(clean("ééééé", 3), "ééé");
    }

    #[test]
    fn header_lookup_ignores_case() {
        let mut h = HashMap::new();
        h.insert("Traceparent".to_string(), "v".to_string());
        assert_eq!(header(&h, "traceparent"), Some("v"));
        assert_eq!(header(&h, "x-request-id"), None);
    }
}

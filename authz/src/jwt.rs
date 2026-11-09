//! Pinned-issuer JWT verification. Key discovery is configuration, never token input.
use std::{
    collections::HashMap,
    time::{Duration, Instant},
};

use jsonwebtoken::{decode, decode_header, Algorithm, DecodingKey, Validation};
use serde::{Deserialize, Serialize};
use tokio::sync::{Mutex, RwLock};

#[derive(Debug, Deserialize, Serialize)]
pub struct Claims {
    pub sub: String,
    pub exp: u64,
    #[serde(default)]
    pub azp: String,
    #[serde(default)]
    pub scope: String,
    #[serde(default)]
    pub act: Option<ActorClaim>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct ActorClaim {
    pub sub: String,
}

#[derive(Clone)]
pub struct Config {
    pub issuer: String,
    pub audience: String,
    pub jwks_url: String,
}

#[derive(Deserialize)]
struct KeySet {
    keys: Vec<Key>,
}

#[derive(Deserialize)]
struct Key {
    kid: String,
    kty: String,
    n: Option<String>,
    e: Option<String>,
    alg: Option<String>,
    #[serde(rename = "use")]
    usage: Option<String>,
}

struct Cache {
    keys: HashMap<String, DecodingKey>,
    loaded_at: Option<Instant>,
    last_attempt: Option<Instant>,
}

pub struct Verifier {
    config: Config,
    client: reqwest::Client,
    cache: RwLock<Cache>,
    refresh_gate: Mutex<()>,
}

impl Verifier {
    /// Readiness refreshes expired keys, coalescing with request-path refreshes.
    pub async fn ready(&self) -> Result<(), String> {
        self.ensure_keys(None).await
    }

    async fn ensure_keys(&self, kid: Option<&str>) -> Result<(), String> {
        {
            let cache = self.cache.read().await;
            if cache
                .loaded_at
                .is_some_and(|t| t.elapsed() < Duration::from_secs(300))
                && kid.is_none_or(|kid| cache.keys.contains_key(kid))
            {
                return Ok(());
            }
        }
        // Refreshes coalesce, but network I/O never holds the key-cache lock.
        // A slow/unknown-key request cannot stall valid cached-key verification.
        let _gate = self.refresh_gate.lock().await;
        {
            let cache = self.cache.read().await;
            if cache
                .loaded_at
                .is_some_and(|t| t.elapsed() < Duration::from_secs(300))
                && kid.is_none_or(|kid| cache.keys.contains_key(kid))
            {
                return Ok(());
            }
            if cache
                .last_attempt
                .is_some_and(|t| t.elapsed() < Duration::from_secs(30))
            {
                return Err("signing key unavailable".into());
            }
        }
        self.refresh().await
    }

    pub async fn new(config: Config) -> Result<Self, String> {
        if config.issuer.is_empty() || config.audience.is_empty() {
            return Err("JWT_ISSUER and JWT_AUDIENCE are required".into());
        }
        let url = reqwest::Url::parse(&config.jwks_url).map_err(|_| "invalid JWT_JWKS_URL")?;
        if url.scheme() != "https" && !(cfg!(test) && url.host_str() == Some("127.0.0.1")) {
            return Err("JWT_JWKS_URL must use HTTPS".into());
        }
        if !url.username().is_empty() || url.password().is_some() || url.fragment().is_some() {
            return Err("JWT_JWKS_URL cannot contain credentials or a fragment".into());
        }
        let client = reqwest::Client::builder()
            .timeout(Duration::from_secs(5))
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|e| e.to_string())?;
        let verifier = Self {
            config,
            client,
            cache: RwLock::new(Cache {
                keys: HashMap::new(),
                loaded_at: None,
                last_attempt: None,
            }),
            refresh_gate: Mutex::new(()),
        };
        verifier.refresh().await?;
        Ok(verifier)
    }

    async fn refresh(&self) -> Result<(), String> {
        self.cache.write().await.last_attempt = Some(Instant::now());
        let mut response = self
            .client
            .get(&self.config.jwks_url)
            .send()
            .await
            .map_err(|_| "JWKS request failed")?
            .error_for_status()
            .map_err(|_| "JWKS returned an unsuccessful status")?;
        let mut body = Vec::new();
        while let Some(chunk) = response.chunk().await.map_err(|_| "JWKS read failed")? {
            if body.len() + chunk.len() > 1_048_576 {
                return Err("JWKS exceeds size limit".into());
            }
            body.extend_from_slice(&chunk);
        }
        let set: KeySet = serde_json::from_slice(&body).map_err(|_| "invalid JWKS")?;
        if set.keys.len() > 100 {
            return Err("JWKS exceeds key limit".into());
        }
        let mut keys = HashMap::new();
        for key in set.keys {
            if key.kty != "RSA"
                || key.alg.as_deref().is_some_and(|a| a != "RS256")
                || key.usage.as_deref().is_some_and(|u| u != "sig")
            {
                continue;
            }
            if key.kid.is_empty() {
                return Err("JWKS key has no kid".into());
            }
            let value = DecodingKey::from_rsa_components(
                key.n.as_deref().ok_or("JWKS RSA key has no modulus")?,
                key.e.as_deref().ok_or("JWKS RSA key has no exponent")?,
            )
            .map_err(|_| "invalid JWKS RSA key")?;
            if keys.insert(key.kid, value).is_some() {
                return Err("duplicate JWKS kid".into());
            }
        }
        if keys.is_empty() {
            return Err("JWKS has no usable signing keys".into());
        }
        let mut cache = self.cache.write().await;
        cache.keys = keys;
        cache.loaded_at = Some(Instant::now());
        Ok(())
    }

    pub async fn verify(&self, token: &str) -> Result<Claims, String> {
        if token.len() > 16_384 {
            return Err("token exceeds size limit".into());
        }
        let header = decode_header(token).map_err(|_| "invalid JWT header")?;
        if header.alg != Algorithm::RS256 {
            return Err("unsupported JWT algorithm".into());
        }
        let kid = header.kid.ok_or("JWT kid is required")?;
        self.ensure_keys(Some(&kid)).await?;
        let key = self
            .cache
            .read()
            .await
            .keys
            .get(&kid)
            .cloned()
            .ok_or("unknown signing key")?;
        let mut validation = Validation::new(Algorithm::RS256);
        validation.set_issuer(&[&self.config.issuer]);
        validation.set_audience(&[&self.config.audience]);
        validation.set_required_spec_claims(&["sub", "exp", "iss", "aud"]);
        validation.validate_nbf = true;
        validation.leeway = 0;
        let claims = decode::<Claims>(token, &key, &validation)
            .map_err(|_| "invalid JWT signature or claims")?
            .claims;
        if claims.sub.is_empty() {
            return Err("JWT subject is empty".into());
        }
        Ok(claims)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{routing::get, Router};
    use jsonwebtoken::{encode, EncodingKey, Header};
    use serde_json::{json, Value};
    use std::sync::{
        atomic::{AtomicUsize, Ordering},
        Arc,
    };

    const PRIVATE: &[u8] = include_bytes!("../tests/fixtures/test-only-private.pem");
    const JWKS: &str = include_str!("../tests/fixtures/jwks.json");

    fn claims() -> Value {
        json!({"sub":"immutable-user", "iss":"https://issuer.example", "aud":"aiplex",
            "exp":chrono::Utc::now().timestamp()+300, "scope":"llm:model:gemini"})
    }

    fn token(claims: &Value, kid: &str) -> String {
        let mut header = Header::new(Algorithm::RS256);
        header.kid = Some(kid.into());
        encode(
            &header,
            claims,
            &EncodingKey::from_rsa_pem(PRIVATE).unwrap(),
        )
        .unwrap()
    }

    async fn verifier() -> (Verifier, Arc<AtomicUsize>, tokio::task::JoinHandle<()>) {
        let hits = Arc::new(AtomicUsize::new(0));
        let count = hits.clone();
        let app = Router::new().route(
            "/jwks",
            get(move || {
                let count = count.clone();
                async move {
                    count.fetch_add(1, Ordering::SeqCst);
                    JWKS
                }
            }),
        );
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let task = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
        let verifier = Verifier::new(Config {
            issuer: "https://issuer.example".into(),
            audience: "aiplex".into(),
            jwks_url: format!("http://{address}/jwks"),
        })
        .await
        .unwrap();
        (verifier, hits, task)
    }

    #[tokio::test]
    async fn validates_signature_issuer_audience_expiry_and_subject() {
        let (verifier, hits, task) = verifier().await;
        assert_eq!(
            verifier
                .verify(&token(&claims(), "test-1"))
                .await
                .unwrap()
                .sub,
            "immutable-user"
        );
        for (field, value) in [
            ("iss", json!("https://other")),
            ("aud", json!("other")),
            ("exp", json!(1)),
            ("sub", json!("")),
        ] {
            let mut input = claims();
            input[field] = value;
            assert!(
                verifier.verify(&token(&input, "test-1")).await.is_err(),
                "accepted invalid {field}"
            );
        }
        let mut missing = claims();
        missing.as_object_mut().unwrap().remove("sub");
        assert!(verifier.verify(&token(&missing, "test-1")).await.is_err());
        let mut tampered = token(&claims(), "test-1");
        let split = tampered.rfind('.').unwrap() + 1;
        tampered.replace_range(
            split..split + 1,
            if &tampered[split..split + 1] == "A" {
                "B"
            } else {
                "A"
            },
        );
        assert!(verifier.verify(&tampered).await.is_err());
        assert_eq!(hits.load(Ordering::SeqCst), 1);
        task.abort();
    }

    #[tokio::test]
    async fn rejects_algorithm_confusion_and_limits_unknown_key_refresh() {
        let (verifier, hits, task) = verifier().await;
        let forged = encode(
            &Header::new(Algorithm::HS256),
            &claims(),
            &EncodingKey::from_secret(b"public-key"),
        )
        .unwrap();
        assert!(verifier.verify(&forged).await.is_err());
        for _ in 0..20 {
            assert!(verifier.verify(&token(&claims(), "unknown")).await.is_err());
        }
        assert_eq!(hits.load(Ordering::SeqCst), 1);
        verifier.cache.write().await.last_attempt = Some(Instant::now() - Duration::from_secs(31));
        assert!(verifier.verify(&token(&claims(), "unknown")).await.is_err());
        assert_eq!(hits.load(Ordering::SeqCst), 2);
        assert!(verifier.verify(&token(&claims(), "unknown")).await.is_err());
        assert_eq!(hits.load(Ordering::SeqCst), 2);
        task.abort();
    }

    #[tokio::test]
    async fn refuses_missing_configuration_and_insecure_remote_jwks() {
        assert!(Verifier::new(Config {
            issuer: "".into(),
            audience: "aiplex".into(),
            jwks_url: "https://keys.example".into()
        })
        .await
        .is_err());
        assert!(Verifier::new(Config {
            issuer: "issuer".into(),
            audience: "aiplex".into(),
            jwks_url: "http://keys.example".into()
        })
        .await
        .is_err());
    }

    #[tokio::test]
    async fn expired_cache_does_not_accept_stale_keys_when_refresh_fails() {
        let (verifier, _, task) = verifier().await;
        task.abort();
        let mut cache = verifier.cache.write().await;
        cache.loaded_at = Some(Instant::now() - Duration::from_secs(301));
        cache.last_attempt = Some(Instant::now() - Duration::from_secs(31));
        drop(cache);
        assert!(verifier.verify(&token(&claims(), "test-1")).await.is_err());
        assert!(verifier.ready().await.is_err());
    }

    #[tokio::test]
    async fn cached_keys_do_not_wait_for_an_in_flight_network_refresh() {
        let (verifier, _, task) = verifier().await;
        let _refresh = verifier.refresh_gate.lock().await;
        assert!(tokio::time::timeout(
            Duration::from_millis(50),
            verifier.verify(&token(&claims(), "test-1"))
        )
        .await
        .unwrap()
        .is_ok());
        task.abort();
    }
}

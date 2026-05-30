package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

type contextKey string

const (
	keyRequestID   contextKey = "request_id"
	keyUserID      contextKey = "user_id"
	keyWIFIdentity contextKey = "wif_identity"
	keyWIFAccess   contextKey = "wif_access"
)

// RequestID injects a request ID from the X-Request-Id header (or generates one).
func RequestID(next http.Handler) http.Handler {
	var counter atomic.Uint64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			rid = "req-" + formatUint(counter.Add(1))
		}
		ctx := context.WithValue(r.Context(), keyRequestID, rid)
		w.Header().Set("X-Request-Id", rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Logger adds structured request logging.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid, _ := r.Context().Value(keyRequestID).(string)
		logger := log.With().Str("request_id", rid).Str("method", r.Method).Str("path", r.URL.Path).Logger()
		ctx := logger.WithContext(r.Context())
		logger.Info().Msg("request started")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Recover catches panics and returns 500.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				zerolog.Ctx(r.Context()).Error().Interface("panic", err).Msg("recovered from panic")
				http.Error(w, `{"code":"INTERNAL","message":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func formatUint(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// GetRequestID extracts the request ID from context.
func GetRequestID(ctx context.Context) string {
	rid, _ := ctx.Value(keyRequestID).(string)
	return rid
}

// CORS adds Cross-Origin Resource Sharing headers for the Console SPA.
func CORS(allowedOrigins ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowed := false
			for _, o := range allowedOrigins {
				if o == "*" || o == origin {
					allowed = true
					break
				}
			}
			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MaxBody limits request body size. Returns 413 if exceeded.
func MaxBody(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// extractOwner extracts the user identity from the Authorization header JWT.
// Falls back to "anonymous" when no valid JWT is present (e.g., dev mode).
func extractOwner(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return "anonymous"
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "anonymous"
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "anonymous"
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Sub == "" {
		return "anonymous"
	}
	return claims.Sub
}

// WIFAuth extracts the caller's WIF identity from the request token,
// resolves group→role mappings, and syncs Dimension B scopes.
// The resolved identity and access are stored in the request context.
// This middleware is non-blocking: if no WIF token is present, the request
// proceeds without WIF identity (for unauthenticated/dev endpoints).
func WIFAuth(wif *auth.WIFValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, err := wif.ExtractIdentity(r)
			if err != nil {
				// No valid WIF token — proceed without identity
				next.ServeHTTP(w, r)
				return
			}

			ctx := context.WithValue(r.Context(), keyWIFIdentity, identity)

			// Resolve roles and sync Dimension B scopes
			access, err := wif.SyncUserScopes(r.Context(), identity)
			if err != nil {
				zerolog.Ctx(r.Context()).Warn().Err(err).Msg("failed to resolve WIF access")
			} else {
				ctx = context.WithValue(ctx, keyWIFAccess, access)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetWIFIdentity extracts the WIF identity from context (set by WIFAuth middleware).
func GetWIFIdentity(ctx context.Context) *models.WIFIdentity {
	identity, _ := ctx.Value(keyWIFIdentity).(*models.WIFIdentity)
	return identity
}

// GetWIFAccess extracts the resolved WIF access from context (set by WIFAuth middleware).
func GetWIFAccess(ctx context.Context) *models.ResolvedAccess {
	access, _ := ctx.Value(keyWIFAccess).(*models.ResolvedAccess)
	return access
}

// RequireRole returns middleware that rejects requests from callers
// who don't have at least one of the specified AIPlex roles.
func RequireRole(roles ...models.IAMRole) func(http.Handler) http.Handler {
	roleSet := make(map[models.IAMRole]bool, len(roles))
	for _, r := range roles {
		roleSet[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			access := GetWIFAccess(r.Context())
			if access == nil {
				// No WIF identity resolved — allow through for dev/IAP-only setups
				next.ServeHTTP(w, r)
				return
			}
			for _, role := range access.Roles {
				if roleSet[role] {
					next.ServeHTTP(w, r)
					return
				}
			}
			Error(w, r, http.StatusForbidden, "FORBIDDEN",
				"insufficient role; required one of: "+formatRoles(roles))
		})
	}
}

func formatRoles(roles []models.IAMRole) string {
	parts := make([]string, len(roles))
	for i, r := range roles {
		parts[i] = string(r)
	}
	return strings.Join(parts, ", ")
}

// RateLimit implements a simple in-process token-bucket rate limiter.
// For production multi-replica deployment, this should be backed by Redis
// or the Envoy Gateway rate limit service (already configured in gateway.yaml).
func RateLimit(requestsPerMinute int) func(http.Handler) http.Handler {
	type bucket struct {
		tokens    int
		lastReset time.Time
	}
	var mu sync.Mutex
	buckets := make(map[string]*bucket)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := extractOwner(r)
			if key == "anonymous" {
				key = r.RemoteAddr
			}

			mu.Lock()
			b, ok := buckets[key]
			now := time.Now()
			if !ok || now.Sub(b.lastReset) > time.Minute {
				b = &bucket{tokens: requestsPerMinute, lastReset: now}
				buckets[key] = b
			}
			if b.tokens <= 0 {
				mu.Unlock()
				w.Header().Set("Retry-After", "60")
				Error(w, r, http.StatusTooManyRequests, "RATE_LIMITED",
					"rate limit exceeded; retry after 60 seconds")
				return
			}
			b.tokens--
			mu.Unlock()

			next.ServeHTTP(w, r)
		})
	}
}

// Compress adds gzip compression to responses.
func Compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		// Set header indicating gzip is available but let the reverse proxy handle it.
		// In production, Envoy Gateway handles compression at the edge.
		w.Header().Set("Vary", "Accept-Encoding")
		next.ServeHTTP(w, r)
	})
}

// ValidatePlane checks that the plane query param is valid if provided.
func ValidatePlane(next http.Handler) http.Handler {
	validPlanes := map[string]bool{
		"":           true, // empty = all planes
		"mcplex":     true,
		"a2aplex":    true,
		"llmplex":    true,
		"skillsplex": true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plane := strings.ToLower(r.URL.Query().Get("plane"))
		if !validPlanes[plane] {
			Error(w, r, http.StatusBadRequest, "INVALID_PLANE",
				"plane must be one of: mcplex, a2aplex, llmplex")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── AIPlex ↔ Tape integration (PR 11) ────────────────────────────────────

// BearerToken is a strict bearer-token middleware for internal endpoints
// (currently /internal/tape/events and /internal/projections/*). Compares
// the Authorization header against `envVar`; missing or wrong token →
// 401. Empty `envVar` value at startup means the route is disabled
// rather than open (defence in depth — a misconfigured deploy fails
// closed, not open).
//
// Distinct from WIFAuth: that handles user JWTs and is non-blocking
// (proceeds without identity for dev paths). This one is strict and
// terminates the request on mismatch.
func BearerToken(envVar string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			expected := os.Getenv(envVar)
			if expected == "" {
				log.Ctx(r.Context()).Error().Str("env", envVar).
					Msg("bearer token env var is empty — route disabled")
				Error(w, r, http.StatusServiceUnavailable, "AUTH_DISABLED",
					"this endpoint is not configured (set "+envVar+")")
				return
			}
			auth := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(auth, prefix) {
				Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED",
					"Bearer token required")
				return
			}
			if subtle.ConstantTimeCompare([]byte(auth[len(prefix):]), []byte(expected)) != 1 {
				Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED",
					"invalid token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// IPRateLimit is a token-bucket rate limiter keyed by source IP. Used
// on the high-volume ingestion endpoint where one busy Tape outbox
// could otherwise flood AIPlex's projection storage. Per-IP because
// internal endpoints are mesh-only — the source IP IS the caller
// identity. Production-grade: lazy creation, periodic GC of stale
// buckets, configurable via env (default 1000 ev/sec/IP).
func IPRateLimit(eventsPerSecond float64, burst int) func(http.Handler) http.Handler {
	if eventsPerSecond <= 0 {
		eventsPerSecond = 1000
	}
	if burst <= 0 {
		burst = int(eventsPerSecond)
	}
	type entry struct {
		limiter  *rate.Limiter
		lastUsed time.Time
	}
	var mu sync.Mutex
	buckets := make(map[string]*entry)

	// GC stale buckets every 5 minutes so a long-running server doesn't
	// accumulate one entry per source IP it's ever seen.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			mu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for k, e := range buckets {
				if e.lastUsed.Before(cutoff) {
					delete(buckets, k)
				}
			}
			mu.Unlock()
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			mu.Lock()
			e, ok := buckets[ip]
			if !ok {
				e = &entry{limiter: rate.NewLimiter(rate.Limit(eventsPerSecond), burst)}
				buckets[ip] = e
			}
			e.lastUsed = time.Now()
			limiter := e.limiter
			mu.Unlock()

			if !limiter.Allow() {
				w.Header().Set("Retry-After", "1")
				Error(w, r, http.StatusTooManyRequests, "RATE_LIMITED",
					"events/sec per source IP exceeded; retry shortly")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP returns the request's source IP, preferring X-Forwarded-For
// (set by the Envoy gateway in production) then RemoteAddr.
func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		// First entry is the original client; subsequent are proxies.
		if comma := strings.IndexByte(v, ','); comma >= 0 {
			return strings.TrimSpace(v[:comma])
		}
		return strings.TrimSpace(v)
	}
	// RemoteAddr is host:port — strip the port.
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host
}

// Idempotency caches operator-action responses keyed by Idempotency-Key
// header (RFC 8235) or, when absent, by sha256(run_id, action, body).
// Second call with the same key returns the cached response without
// invoking the underlying handler — so a double-clicked redrive
// doesn't drive Tape twice. 5-minute TTL.
//
// Implementation notes:
//   - sync.Map for the table; entries expire via time.AfterFunc so
//     there's no scanning goroutine.
//   - Caches the full response body + status code; replays bit-exact.
//   - Body is read once via io.ReadAll, then replayed downstream via
//     bytes.NewReader so handlers see the original request shape.
func Idempotency(actionFromPath func(*http.Request) string) func(http.Handler) http.Handler {
	const ttl = 5 * time.Minute
	type cached struct {
		status int
		hdrs   http.Header
		body   []byte
	}
	var cache sync.Map // string → *cached

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Read body once so we can hash + replay.
			body, err := io.ReadAll(r.Body)
			if err != nil {
				Error(w, r, http.StatusBadRequest, "READ_BODY_FAILED", err.Error())
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				action := actionFromPath(r)
				runID := chi.URLParam(r, "run_id")
				h := sha256.Sum256(body)
				key = fmt.Sprintf("%s|%s|%s", runID, action, hex.EncodeToString(h[:]))
			}

			if v, ok := cache.Load(key); ok {
				c := v.(*cached)
				for k, vv := range c.hdrs {
					w.Header()[k] = vv
				}
				w.Header().Set("X-Idempotent-Replay", "true")
				w.WriteHeader(c.status)
				_, _ = w.Write(c.body)
				return
			}

			// Capture the downstream response.
			rec := &responseRecorder{ResponseWriter: w, status: 200, body: &bytes.Buffer{}}
			next.ServeHTTP(rec, r)

			// Cache only success-ish responses (2xx). Errors should
			// fail loudly on each click so the operator can retry.
			if rec.status >= 200 && rec.status < 300 {
				c := &cached{
					status: rec.status,
					hdrs:   rec.Header().Clone(),
					body:   rec.body.Bytes(),
				}
				cache.Store(key, c)
				time.AfterFunc(ttl, func() { cache.Delete(key) })
			}
		})
	}
}

// responseRecorder tees the downstream handler's response into a
// buffer + the real writer simultaneously. Avoids the "buffer then
// flush" pattern that breaks SSE; here we just need bytes for the
// cache and the body has already been written when AfterFunc fires.
type responseRecorder struct {
	http.ResponseWriter
	status int
	body   *bytes.Buffer
	wrote  bool
}

func (r *responseRecorder) WriteHeader(code int) {
	if r.wrote {
		return
	}
	r.status = code
	r.ResponseWriter.WriteHeader(code)
	r.wrote = true
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// TenantFromContext returns the caller's tenant id and a flag
// indicating whether they have the cross-tenant scope. Sources:
//
//   1. ResolvedAccess.Scopes — looks for `aiplex:tenant:<id>` and
//      `aiplex:runs:read:cross_tenant`.
//   2. WIFIdentity.Domain — the workforce identity's `hd` claim,
//      mapped via env AIPLEX_DOMAIN_TENANT_MAP (k=v,k=v).
//
// Returns ("", false) when no tenant info is available; callers
// decide whether to deny or fall through (the AIPLEX_REQUIRE_TENANT
// env var, when "1", flips that to "deny by default").
func TenantFromContext(ctx context.Context) (tenantID string, crossTenant bool) {
	access := GetWIFAccess(ctx)
	if access == nil {
		return "", false
	}
	for _, scope := range access.Scopes {
		switch {
		case scope == "aiplex:runs:read:cross_tenant":
			crossTenant = true
		case strings.HasPrefix(scope, "aiplex:tenant:"):
			tenantID = scope[len("aiplex:tenant:"):]
		}
	}
	if tenantID == "" && access.Identity.Domain != "" {
		// Optional convention: map workforce domain → tenant via env.
		for _, pair := range strings.Split(os.Getenv("AIPLEX_DOMAIN_TENANT_MAP"), ",") {
			if i := strings.IndexByte(pair, '='); i > 0 {
				d, t := strings.TrimSpace(pair[:i]), strings.TrimSpace(pair[i+1:])
				if d == access.Identity.Domain && t != "" {
					tenantID = t
					break
				}
			}
		}
	}
	return tenantID, crossTenant
}

// RequireTenant returns 403 unless the caller has either a tenant
// claim OR the cross-tenant scope. Wired on /api/v1/runs* in main.go.
func RequireTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("AIPLEX_REQUIRE_TENANT") != "1" {
			next.ServeHTTP(w, r)
			return
		}
		tenant, cross := TenantFromContext(r.Context())
		if tenant == "" && !cross {
			Error(w, r, http.StatusForbidden, "TENANT_REQUIRED",
				"caller has no tenant claim and no cross-tenant scope")
			return
		}
		next.ServeHTTP(w, r)
	})
}

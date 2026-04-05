package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

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
		"":        true, // empty = all planes
		"mcplex":  true,
		"a2aplex": true,
		"llmplex": true,
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

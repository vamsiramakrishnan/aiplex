package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type contextKey string

const (
	keyRequestID contextKey = "request_id"
	keyUserID    contextKey = "user_id"
)

// RequestID injects a request ID from the X-Request-Id header (or generates one).
func RequestID(next http.Handler) http.Handler {
	var counter uint64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			counter++
			rid = "req-" + formatUint(counter)
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

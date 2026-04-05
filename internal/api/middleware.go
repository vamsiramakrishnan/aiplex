package api

import (
	"context"
	"net/http"

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

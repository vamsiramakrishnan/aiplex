package api

import (
	"context"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// AuditEvent records a mutation for compliance and debugging.
type AuditEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	RequestID  string    `json:"request_id"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	UserID     string    `json:"user_id"`
	StatusCode int       `json:"status_code"`
	DurationMs int64     `json:"duration_ms"`
	Resource   string    `json:"resource,omitempty"`
	Action     string    `json:"action,omitempty"`
}

// responseCapture wraps http.ResponseWriter to capture the status code.
type responseCapture struct {
	http.ResponseWriter
	statusCode int
}

func (r *responseCapture) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

// AuditLog middleware logs all mutations (POST, PUT, PATCH, DELETE) to
// structured JSON logs. Read operations (GET, HEAD, OPTIONS) are not audited
// to avoid log volume explosion.
func AuditLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only audit mutations
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rc := &responseCapture{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rc, r)

		event := AuditEvent{
			Timestamp:  start,
			RequestID:  GetRequestID(r.Context()),
			Method:     r.Method,
			Path:       r.URL.Path,
			UserID:     extractOwner(r),
			StatusCode: rc.statusCode,
			DurationMs: time.Since(start).Milliseconds(),
		}

		// Classify the resource and action from path
		event.Resource, event.Action = classifyMutation(r.Method, r.URL.Path)

		log.Info().
			Str("audit", "mutation").
			Str("request_id", event.RequestID).
			Str("method", event.Method).
			Str("path", event.Path).
			Str("user_id", event.UserID).
			Int("status", event.StatusCode).
			Int64("duration_ms", event.DurationMs).
			Str("resource", event.Resource).
			Str("action", event.Action).
			Msg("audit event")
	})
}

func classifyMutation(method, path string) (resource, action string) {
	switch {
	case contains(path, "/instances"):
		resource = "instance"
	case contains(path, "/agents"):
		resource = "agent"
	case contains(path, "/llm/routes"):
		resource = "llm_route"
	case contains(path, "/llm/providers"):
		resource = "llm_provider"
	case contains(path, "/a2a/delegations"):
		resource = "delegation"
	case contains(path, "/iam/role-bindings"):
		resource = "role_binding"
	case contains(path, "/auth/consent"):
		resource = "consent"
	case contains(path, "/auth/users"):
		resource = "user_scopes"
	default:
		resource = "unknown"
	}

	switch method {
	case http.MethodPost:
		action = "create"
	case http.MethodPut:
		action = "update"
	case http.MethodPatch:
		action = "patch"
	case http.MethodDelete:
		action = "delete"
	}
	return
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// AuditContext returns a logger enriched with audit context.
func AuditContext(ctx context.Context) zerolog.Logger {
	return log.With().Str("request_id", GetRequestID(ctx)).Logger()
}

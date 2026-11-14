package api

import (
	"encoding/json"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/errorcode"
	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
)

// JSON writes a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Error writes a structured API error response.
func Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if accessPointRequest(r) {
		errorcode.Set(w.Header(), code)
	}
	JSON(w, status, models.APIError{
		Code:      code,
		Message:   message,
		RequestID: GetRequestID(r.Context()),
		TraceID:   telemetry.TraceID(r.Context()),
	})
}

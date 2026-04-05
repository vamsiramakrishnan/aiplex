package api

import (
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// HealthHandler provides liveness and readiness checks.
type HealthHandler struct {
	store registry.Store
}

// NewHealthHandler creates a health check handler that verifies dependencies.
func NewHealthHandler(store registry.Store) *HealthHandler {
	return &HealthHandler{store: store}
}

// Liveness returns 200 OK — the process is running.
func (h *HealthHandler) Liveness(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Readiness checks that the store is reachable before accepting traffic.
func (h *HealthHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	// Probe the store with a lightweight read
	if _, err := h.store.ListInstances(r.Context(), ""); err != nil {
		JSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "unavailable",
			"error":  "store unreachable",
		})
		return
	}
	JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Health is a backward-compatible static health check for simple liveness probes.
func Health(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

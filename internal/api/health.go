package api

import "net/http"

// Health returns 200 OK with a simple status for liveness/readiness probes.
func Health(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

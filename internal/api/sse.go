package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// SSEHandler provides Server-Sent Events for live dashboard updates.
type SSEHandler struct {
	store registry.Store
}

// NewSSEHandler creates an SSE handler.
func NewSSEHandler(store registry.Store) *SSEHandler {
	return &SSEHandler{store: store}
}

// Stream sends periodic dashboard updates via Server-Sent Events.
// Clients connect to GET /events/stream and receive JSON payloads every 5 seconds.
// This replaces polling for the Console dashboard.
func (h *SSEHandler) Stream(w http.ResponseWriter, r *http.Request) {
	// Accept token from query param (EventSource can't set headers)
	if token := r.URL.Query().Get("token"); token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		Error(w, r, http.StatusInternalServerError, "SSE_UNSUPPORTED", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Send initial state immediately
	h.sendStats(w, flusher, r)

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			h.sendStats(w, flusher, r)
		}
	}
}

func (h *SSEHandler) sendStats(w http.ResponseWriter, flusher http.Flusher, r *http.Request) {
	ctx := r.Context()

	// Gather stats (same as dashboard handler)
	instances, _ := h.store.ListInstances(ctx, "")
	agents, _ := h.store.ListAgents(ctx)
	delegations, _ := h.store.CountDelegations(ctx)
	denials, _ := h.store.CountPolicyDenials(ctx)

	var running, mcplex, a2aplex, llmplex int
	for _, inst := range instances {
		if inst.Status == "running" {
			running++
		}
		switch inst.Plane {
		case "mcplex":
			mcplex++
		case "a2aplex":
			a2aplex++
		case "llmplex":
			llmplex++
		}
	}

	data := fmt.Sprintf(`{"total_instances":%d,"running":%d,"mcplex":%d,"a2aplex":%d,"llmplex":%d,"agents":%d,"delegations":%d,"denials":%d,"timestamp":"%s"}`,
		len(instances), running, mcplex, a2aplex, llmplex,
		len(agents), delegations, denials,
		time.Now().Format(time.RFC3339))

	fmt.Fprintf(w, "event: stats\ndata: %s\n\n", data)
	flusher.Flush()
}

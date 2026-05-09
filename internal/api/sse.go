package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
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
func (h *SSEHandler) Stream(w http.ResponseWriter, r *http.Request) {
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

	instances, _ := h.store.ListInstances(ctx, "")
	agents, _ := h.store.ListAgents(ctx)
	delegations, _ := h.store.CountDelegations(ctx)
	denials, _ := h.store.CountPolicyDenials(ctx)

	running := 0
	byKind := make(map[capability.Kind]int)
	for _, inst := range instances {
		if inst.Status == "running" {
			running++
		}
		byKind[inst.Kind]++
	}

	payload := map[string]any{
		"total_instances":   len(instances),
		"running":           running,
		"instances_by_kind": byKind,
		"agents":            len(agents),
		"delegations":       delegations,
		"denials":           denials,
		"timestamp":         time.Now().Format(time.RFC3339),
	}
	data, _ := json.Marshal(payload)

	fmt.Fprintf(w, "event: stats\ndata: %s\n\n", string(data))
	flusher.Flush()
}

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// jsonMarshal kept as a tiny wrapper so the SSE helper above doesn't
// depend on json directly in its hot path (and to mirror the package's
// existing writeJSON convention).
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// SSEHandler provides Server-Sent Events for live dashboard updates.
type SSEHandler struct {
	store registry.Store
}

// NewSSEHandler creates an SSE handler.
func NewSSEHandler(store registry.Store) *SSEHandler {
	return &SSEHandler{store: store}
}

// Stream sends periodic dashboard updates via Server-Sent Events.
// Clients connect to GET /events/stream and receive JSON payloads every
// 5 seconds. This replaces polling for the Console dashboard.
//
// PR 11 item 11: when `?run_id=<id>` is supplied, the stream switches
// to per-run mode — it tails execution_events from from_seq=N and emits
// one SSE event per new row (kind="run_event") plus periodic
// "run_summary" snapshots. The Console's RunDetail panel uses this
// instead of useQuery(refetchInterval) so a busy agent's timeline
// stays current without polling.
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

	if runID := r.URL.Query().Get("run_id"); runID != "" {
		h.streamRun(w, flusher, r, runID)
		return
	}

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

// streamRun tails a single run's events. Polls the store every 500ms;
// emits an SSE event for each new row above the high-water seq, plus a
// summary snapshot every 5 seconds for clients that want both views.
// Server-side polling is cheap (the store has a per-run index) and
// portable across SQLite / Firestore / future Postgres backends.
func (h *SSEHandler) streamRun(w http.ResponseWriter, flusher http.Flusher, r *http.Request, runID string) {
	ctx := r.Context()
	fromSeq := int64(0)
	// initial backlog — send everything we know so the client doesn't
	// have to poll once on connect.
	if events, err := h.store.ListExecutionEvents(ctx, runID, 0, 1000); err == nil {
		for _, ev := range events {
			fmt.Fprintf(w, "event: run_event\ndata: %s\n\n", mustJSON(ev))
			if ev.Seq >= fromSeq {
				fromSeq = ev.Seq + 1
			}
		}
		if run, err := h.store.GetExecutionRun(ctx, runID); err == nil {
			fmt.Fprintf(w, "event: run_summary\ndata: %s\n\n", mustJSON(run))
		}
		flusher.Flush()
	}

	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	summaryTick := time.NewTicker(5 * time.Second)
	defer summaryTick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			events, err := h.store.ListExecutionEvents(ctx, runID, fromSeq, 200)
			if err != nil {
				continue
			}
			for _, ev := range events {
				fmt.Fprintf(w, "event: run_event\ndata: %s\n\n", mustJSON(ev))
				if ev.Seq >= fromSeq {
					fromSeq = ev.Seq + 1
				}
			}
			if len(events) > 0 {
				flusher.Flush()
			}
		case <-summaryTick.C:
			if run, err := h.store.GetExecutionRun(ctx, runID); err == nil {
				fmt.Fprintf(w, "event: run_summary\ndata: %s\n\n", mustJSON(run))
				flusher.Flush()
			}
		}
	}
}

func mustJSON(v any) string {
	b, err := jsonMarshal(v)
	if err != nil {
		return `{"error":"marshal"}`
	}
	return string(b)
}

func (h *SSEHandler) sendStats(w http.ResponseWriter, flusher http.Flusher, r *http.Request) {
	ctx := r.Context()

	// Gather stats (same as dashboard handler)
	instances, _ := h.store.ListInstances(ctx, "")
	agents, _ := h.store.ListAgents(ctx)
	delegations, _ := h.store.CountDelegations(ctx)
	denials, _ := h.store.CountPolicyDenials(ctx)

	var running, mcplex, a2aplex, llmplex, skillsplex int
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
		case "skillsplex":
			skillsplex++
		}
	}

	data := fmt.Sprintf(`{"total_instances":%d,"running":%d,"mcplex":%d,"a2aplex":%d,"llmplex":%d,"skillsplex":%d,"agents":%d,"delegations":%d,"denials":%d,"timestamp":"%s"}`,
		len(instances), running, mcplex, a2aplex, llmplex, skillsplex,
		len(agents), delegations, denials,
		time.Now().Format(time.RFC3339))

	fmt.Fprintf(w, "event: stats\ndata: %s\n\n", data)
	flusher.Flush()
}

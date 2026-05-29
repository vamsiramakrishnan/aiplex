package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// RunsHandler serves the AIPlex ↔ Tape audit ingestion endpoint and
// (PR 7) the read API on top of the projected events.
type RunsHandler struct {
	store registry.Store
}

// NewRunsHandler constructs the handler.
func NewRunsHandler(store registry.Store) *RunsHandler {
	return &RunsHandler{store: store}
}

// IngestRequest is the payload Tape's outbox relay POSTs to
// /internal/tape/events. A single POST may carry one event or a batch
// (matches the Tape outbox sink's natural batching unit).
type IngestRequest struct {
	Events []models.ExecutionEvent `json:"events"`
}

// IngestResponse summarises the outcome of an ingest request — how
// many events landed fresh, how many were duplicates (idempotent
// no-ops), how many got quarantined (unknown agent).
type IngestResponse struct {
	Ingested    int `json:"ingested"`
	Duplicates  int `json:"duplicates"`
	Quarantined int `json:"quarantined"`
}

// Ingest handles POST /internal/tape/events. Behaviour:
//
//   * Idempotent on (RunID, Seq) — duplicate events return a typed
//     no-op counted in the response.
//   * Unknown AIPlexInstanceID → quarantined in a separate collection
//     for operator triage; the rest of the batch still lands.
//   * Empty events array is a valid 200 (lets the outbox relay's
//     polling loop hold a connection open without errors).
//
// PR 6 ships the wire contract + idempotency + projection. PR 7 adds
// the read API on top of the projected runs.
func (h *RunsHandler) Ingest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	resp := IngestResponse{}
	for i := range req.Events {
		ev := &req.Events[i]
		if ev.RunID == "" {
			http.Error(w, "events[].run_id is required", http.StatusBadRequest)
			return
		}
		// Unknown agent: quarantine and move on. Operators triage from
		// the quarantine collection — typically by registering the
		// missing instance, then replaying.
		if !h.agentKnown(ctx, ev.AIPlexInstanceID) {
			_ = h.store.QuarantineExecutionEvent(ctx, &models.QuarantinedExecutionEvent{
				ReceivedAt: time.Now(),
				Reason:     "unknown_aiplex_instance_id",
				Event:      *ev,
			})
			resp.Quarantined++
			continue
		}

		wrote, err := h.store.AppendExecutionEvent(ctx, ev)
		if err != nil {
			http.Error(w, "ingest failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if !wrote {
			resp.Duplicates++
			continue
		}
		resp.Ingested++

		// Project — fold the event into the run summary. Best-effort:
		// a projection failure doesn't roll back the event row (the
		// event is the source of truth; the projection can be rebuilt).
		if err := h.projectInto(ctx, ev); err != nil {
			// Log via the request-scoped logger if one exists; never
			// fail the ingest on a projection error.
			_ = err
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// agentKnown checks whether the AIPlexInstanceID names an instance the
// API has on file. Empty IDs (legacy or non-AIPlex callers) are
// admitted — quarantine is only for cases where a non-empty ID doesn't
// resolve.
func (h *RunsHandler) agentKnown(ctx context.Context, instanceID string) bool {
	if instanceID == "" {
		return true
	}
	_, err := h.store.GetInstance(ctx, instanceID)
	if errors.Is(err, registry.ErrNotFound) {
		return false
	}
	// Any other error (transient store failure): admit and let the
	// append-side error path surface the real issue, rather than
	// silently quarantining live traffic during a Firestore blip.
	return true
}

// projectInto folds one event into the (idempotent) ExecutionRun
// projection. Counters are increment-by-kind; status is the most
// recent terminal-or-status-bearing kind we've seen.
func (h *RunsHandler) projectInto(ctx context.Context, ev *models.ExecutionEvent) error {
	run, err := h.store.GetExecutionRun(ctx, ev.RunID)
	if errors.Is(err, registry.ErrNotFound) {
		run = &models.ExecutionRun{
			RunID:            ev.RunID,
			TenantID:         ev.TenantID,
			AgentID:          ev.AgentID,
			Plane:            ev.Plane,
			Actor:            ev.Actor,
			Subject:          ev.Subject,
			AIPlexInstanceID: ev.AIPlexInstanceID,
			Status:           models.ExecutionRunRunnable,
			StartedAt:        ev.Timestamp,
		}
	} else if err != nil {
		return err
	}
	applyEventToRun(run, ev)
	return h.store.UpsertExecutionRun(ctx, run)
}

// applyEventToRun is the projection logic, factored out for testing.
// Pure function over the (run, event) pair; no side effects.
func applyEventToRun(run *models.ExecutionRun, ev *models.ExecutionEvent) {
	switch ev.Kind {
	case models.ExecutionEventRunStarted:
		run.Status = models.ExecutionRunRunning
		if run.StartedAt.IsZero() || ev.Timestamp.Before(run.StartedAt) {
			run.StartedAt = ev.Timestamp
		}
	case models.ExecutionEventRunCompleted:
		run.Status = models.ExecutionRunTerminal
		t := ev.Timestamp
		run.EndedAt = &t
	case models.ExecutionEventRunFailed:
		run.Status = models.ExecutionRunFailed
		t := ev.Timestamp
		run.EndedAt = &t
	case models.ExecutionEventDecisionRecorded:
		run.DecisionsCount++
	case models.ExecutionEventEffectBegin,
		models.ExecutionEventEffectConfirmed,
		models.ExecutionEventEffectFailed,
		models.ExecutionEventEffectDuplicate:
		run.EffectsCount++
	case models.ExecutionEventEffectUnknown:
		run.EffectsCount++
		run.UnknownEffects++
	case models.ExecutionEventObligationCreated:
		run.Obligations++
		run.Status = models.ExecutionRunCompensating
	case models.ExecutionEventGateWaiting:
		run.Status = models.ExecutionRunWaiting
	case models.ExecutionEventPolicyViolation:
		run.PolicyViolations++
	case models.ExecutionEventBudgetCharged:
		// Extract usd_charged from the payload if present. Parsing
		// kept deliberately permissive — payload schema is Tape's
		// contract, but a malformed row shouldn't kill the projection.
		if amount := budgetUSDFromPayload(ev.PayloadJSON); amount > 0 {
			run.BudgetUSDCharged += amount
		}
	}
}

func budgetUSDFromPayload(payload string) float64 {
	if payload == "" {
		return 0
	}
	var p struct {
		USDCharged float64 `json:"usd_charged"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return 0
	}
	return p.USDCharged
}

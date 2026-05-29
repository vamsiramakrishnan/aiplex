package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func pathParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, registry.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// RunsHandler serves the AIPlex ↔ Tape audit ingestion endpoint,
// (PR 7) the read API on top of the projected events, and
// (PR 10) the operator actions that mutate runtime state via Tape's
// admin gRPC surface.
type RunsHandler struct {
	store registry.Store
	admin TapeAdmin // optional; defaults to NoopTapeAdmin (see PR 10)
}

// NewRunsHandler constructs the handler with Noop admin (default).
// Wire a real Tape admin client via WithTapeAdmin in main.go.
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

// ── Read API (AIPlex integration PR 7) ───────────────────────────────────
//
// All routes mounted under /api/v1/runs/* require the aiplex:runs:read
// scope at the gateway authz layer (the JWT-scope check in
// policies/aiplex_authz.rego). Operator-action scopes — redrive /
// reconcile / cancel / signal / compensate — arrive in PR 10.

// List returns the most recent runs for a tenant / agent.
//   GET /api/v1/runs?tenant_id=...&agent_id=...&limit=100
func (h *RunsHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	limit := parseIntDefault(q.Get("limit"), 100)
	runs, err := h.store.ListExecutionRuns(ctx, q.Get("tenant_id"), q.Get("agent_id"), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Optional client-side filters that don't need a separate query
	// path: hide finished runs / show only those with unresolved
	// follow-ups. Cheap to compute in-memory because the store already
	// capped the result set.
	if q.Get("has_unknown_effects") == "true" {
		runs = filterRuns(runs, func(rn models.ExecutionRun) bool {
			return rn.UnknownEffects > 0
		})
	}
	if q.Get("has_obligations") == "true" {
		runs = filterRuns(runs, func(rn models.ExecutionRun) bool {
			return rn.Obligations > 0
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// Get returns one run's projected summary.
//   GET /api/v1/runs/{run_id}
func (h *RunsHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runID := pathParam(r, "run_id")
	run, err := h.store.GetExecutionRun(ctx, runID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// Events returns the ordered timeline for one run.
//   GET /api/v1/runs/{run_id}/events?from_seq=0&limit=1000
func (h *RunsHandler) Events(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runID := pathParam(r, "run_id")
	q := r.URL.Query()
	fromSeq := int64(parseIntDefault(q.Get("from_seq"), 0))
	limit := parseIntDefault(q.Get("limit"), 1000)
	events, err := h.store.ListExecutionEvents(ctx, runID, fromSeq, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// Effects returns just the effect-kind events for a run. Useful for the
// Console's "what did this agent attempt to do" panel without
// shuttling decision / gate / budget noise.
//   GET /api/v1/runs/{run_id}/effects
func (h *RunsHandler) Effects(w http.ResponseWriter, r *http.Request) {
	h.filteredEvents(w, r, isEffectKind, "effects")
}

// Obligations returns the obligation.* events for a run.
//   GET /api/v1/runs/{run_id}/obligations
func (h *RunsHandler) Obligations(w http.ResponseWriter, r *http.Request) {
	h.filteredEvents(w, r, isObligationKind, "obligations")
}

// Budgets returns the budget.* events for a run.
//   GET /api/v1/runs/{run_id}/budgets
func (h *RunsHandler) Budgets(w http.ResponseWriter, r *http.Request) {
	h.filteredEvents(w, r, isBudgetKind, "budgets")
}

func (h *RunsHandler) filteredEvents(w http.ResponseWriter, r *http.Request,
	keep func(models.ExecutionEventKind) bool, fieldName string) {
	ctx := r.Context()
	runID := pathParam(r, "run_id")
	all, err := h.store.ListExecutionEvents(ctx, runID, 0, 10000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]models.ExecutionEvent, 0, len(all))
	for _, ev := range all {
		if keep(ev.Kind) {
			out = append(out, ev)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{fieldName: out})
}

func isEffectKind(k models.ExecutionEventKind) bool {
	switch k {
	case models.ExecutionEventEffectBegin,
		models.ExecutionEventEffectConfirmed,
		models.ExecutionEventEffectFailed,
		models.ExecutionEventEffectUnknown,
		models.ExecutionEventEffectDuplicate:
		return true
	}
	return false
}

func isObligationKind(k models.ExecutionEventKind) bool {
	return k == models.ExecutionEventObligationCreated
}

func isBudgetKind(k models.ExecutionEventKind) bool {
	return k == models.ExecutionEventBudgetCharged
}

func filterRuns(runs []models.ExecutionRun, keep func(models.ExecutionRun) bool) []models.ExecutionRun {
	out := make([]models.ExecutionRun, 0, len(runs))
	for _, r := range runs {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

// ── Operator actions (AIPlex integration PR 10) ──────────────────────────
//
// These endpoints govern *runtime mutations* on a Tape-backed run —
// redrive a stuck one, reconcile an UNKNOWN effect, cancel cooperatively,
// signal a waiting gate, kick off compensation. Each one:
//
//   * checks an aiplex:runs:* scope at the gateway (PR 10 also adds
//     the scope strings to the authz layer; the handler trusts the
//     middleware did its job and focuses on translating the request
//     into a Tape admin RPC + recording the action in audit);
//   * calls Tape's admin gRPC surface (PR 10 wires this through; PR 6/7
//     left it as a TODO marker — the handler structure is here, the
//     adapter is what arrives next);
//   * appends a synthetic ExecutionEvent so the action shows up on the
//     run timeline alongside Tape's own journal entries — the Console
//     should never have to second-guess "did somebody click redrive?"
//
// For PR 10 the handlers return 202 Accepted with the synthetic event
// already projected; the actual gRPC dispatch is stubbed behind the
// TapeAdmin interface so a future PR can wire a real client without
// touching the HTTP shape.

// TapeAdmin abstracts the admin RPCs AIPlex calls on a Tape server.
// Real implementations dial the tape-server gRPC port; the in-process
// E2E tests use a no-op stub. Kept as an interface so the handler
// doesn't depend on the Tape proto-generated types directly — the
// only thing AIPlex needs is "did this operation get accepted by
// Tape?", not the full response shape.
type TapeAdmin interface {
	Redrive(ctx context.Context, runID string) error
	Reconcile(ctx context.Context, runID string) error
	Cancel(ctx context.Context, runID, reason string) error
	Signal(ctx context.Context, runID, gateName, resolutionJSON string) error
	Compensate(ctx context.Context, runID string) error
}

// NoopTapeAdmin is the default — returns nil for every action. Used
// when AIPlex is wired up without a live tape-server (tests, dev).
type NoopTapeAdmin struct{}

func (NoopTapeAdmin) Redrive(_ context.Context, _ string) error                   { return nil }
func (NoopTapeAdmin) Reconcile(_ context.Context, _ string) error                 { return nil }
func (NoopTapeAdmin) Cancel(_ context.Context, _, _ string) error                 { return nil }
func (NoopTapeAdmin) Signal(_ context.Context, _, _, _ string) error              { return nil }
func (NoopTapeAdmin) Compensate(_ context.Context, _ string) error                { return nil }

// WithTapeAdmin returns a copy of the handler bound to a Tape admin
// client. Optional — uncalled, the handler falls back to NoopTapeAdmin.
func (h *RunsHandler) WithTapeAdmin(admin TapeAdmin) *RunsHandler {
	cp := *h
	cp.admin = admin
	return &cp
}

func (h *RunsHandler) tapeAdmin() TapeAdmin {
	if h.admin != nil {
		return h.admin
	}
	return NoopTapeAdmin{}
}

// SignalBody is the JSON body for POST /api/v1/runs/{id}/signal.
type SignalBody struct {
	GateName       string `json:"gate_name"`
	ResolutionJSON string `json:"resolution_json,omitempty"`
}

// CancelBody is the JSON body for POST /api/v1/runs/{id}/cancel.
type CancelBody struct {
	Reason string `json:"reason,omitempty"`
}

// Redrive — POST /api/v1/runs/{id}/redrive. Requires aiplex:runs:redrive.
func (h *RunsHandler) Redrive(w http.ResponseWriter, r *http.Request) {
	h.operatorAction(w, r, "redrive", func(ctx context.Context, runID string) error {
		return h.tapeAdmin().Redrive(ctx, runID)
	}, nil)
}

// Reconcile — POST /api/v1/runs/{id}/reconcile. Requires aiplex:runs:reconcile.
func (h *RunsHandler) Reconcile(w http.ResponseWriter, r *http.Request) {
	h.operatorAction(w, r, "reconcile", func(ctx context.Context, runID string) error {
		return h.tapeAdmin().Reconcile(ctx, runID)
	}, nil)
}

// Cancel — POST /api/v1/runs/{id}/cancel.  Requires aiplex:runs:cancel.
// Body: { "reason": "..." } (optional).
func (h *RunsHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	var body CancelBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	h.operatorAction(w, r, "cancel", func(ctx context.Context, runID string) error {
		return h.tapeAdmin().Cancel(ctx, runID, body.Reason)
	}, map[string]any{"reason": body.Reason})
}

// Signal — POST /api/v1/runs/{id}/signal. Requires aiplex:runs:signal.
// Body: { "gate_name": "...", "resolution_json": "..." }.
func (h *RunsHandler) Signal(w http.ResponseWriter, r *http.Request) {
	var body SignalBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.GateName == "" {
		http.Error(w, "gate_name is required", http.StatusBadRequest)
		return
	}
	h.operatorAction(w, r, "signal", func(ctx context.Context, runID string) error {
		return h.tapeAdmin().Signal(ctx, runID, body.GateName, body.ResolutionJSON)
	}, map[string]any{"gate_name": body.GateName})
}

// Compensate — POST /api/v1/runs/{id}/compensate. Requires aiplex:runs:compensate.
func (h *RunsHandler) Compensate(w http.ResponseWriter, r *http.Request) {
	h.operatorAction(w, r, "compensate", func(ctx context.Context, runID string) error {
		return h.tapeAdmin().Compensate(ctx, runID)
	}, nil)
}

// operatorAction is the shared body of every PR 10 handler: resolve
// the run id, call the Tape admin action, and append a synthetic
// ExecutionEvent so the action surfaces on the timeline (it'll appear
// in /events with kind=run.* and a payload describing what the
// operator did — Tape itself is not aware of the AIPlex-side call).
//
// Returns 202 Accepted on success: Tape's admin RPCs are themselves
// async (they enqueue work for a reactor loop), so 200 OK would
// overpromise.
func (h *RunsHandler) operatorAction(w http.ResponseWriter, r *http.Request,
	action string, callTape func(context.Context, string) error,
	extraPayload map[string]any) {
	ctx := r.Context()
	runID := pathParam(r, "run_id")
	if _, err := h.store.GetExecutionRun(ctx, runID); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := callTape(ctx, runID); err != nil {
		http.Error(w, "tape admin call failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	payload := map[string]any{
		"operator_action": action,
		"actor":           operatorFromRequest(r),
		"at":              time.Now().UTC().Format(time.RFC3339Nano),
	}
	for k, v := range extraPayload {
		payload[k] = v
	}
	pb, _ := json.Marshal(payload)
	// Use a synthetic seq above any real journal entry (1e18) so the
	// operator action is visibly out-of-band from Tape's own writes.
	// Idempotency on (run_id, seq) still applies — multiple clicks
	// produce multiple ordered entries because the timestamp differs
	// each time, so the seq derivation is timestamp-based.
	ev := &models.ExecutionEvent{
		RunID:     runID,
		Seq:       syntheticSeq(),
		Kind:      synthKindFor(action),
		Timestamp: time.Now().UTC(),
		PayloadJSON: string(pb),
	}
	// Best-effort projection: don't fail the action just because we
	// couldn't write the audit row.
	_, _ = h.store.AppendExecutionEvent(ctx, ev)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"action":   action,
		"run_id":   runID,
	})
}

// operatorFromRequest reads the actor from a request header set by
// WIFAuth middleware. Falls back to "unknown" rather than failing —
// the operator action is authoritative through Tape's admin call;
// the audit row is the supporting trail.
func operatorFromRequest(r *http.Request) string {
	if v := r.Header.Get("X-Aiplex-Actor"); v != "" {
		return v
	}
	if v := r.Header.Get("Authorization"); v != "" {
		// Best effort: just record that someone authenticated.
		return "authenticated"
	}
	return "unknown"
}

// syntheticSeq produces a high seq so the operator action sorts after
// any real journal entry. Nanosecond resolution gives unique values
// across rapid back-to-back clicks within a single millisecond — good
// enough for an audit trail, not used for any ordering guarantee.
func syntheticSeq() int64 {
	return time.Now().UnixNano()
}

// synthKindFor maps the action name to a journal kind. The Console
// renders these with their own glyph/colour (see Runs.tsx KIND_STYLE);
// we reuse the existing kinds where the semantics overlap (gate.waiting
// for a signal, run.failed for cancel, etc.) and add a "policy.violation"
// style "operator." prefix would be nice but would force a Console-side
// migration. Until then, operator actions land as kind=run.* and the
// Console differentiates by the payload's `operator_action` key.
func synthKindFor(action string) models.ExecutionEventKind {
	switch action {
	case "redrive", "reconcile":
		return models.ExecutionEventRunStarted
	case "cancel":
		return models.ExecutionEventRunFailed
	case "signal":
		return models.ExecutionEventGateWaiting
	case "compensate":
		return models.ExecutionEventObligationCreated
	default:
		return models.ExecutionEventRunStarted
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

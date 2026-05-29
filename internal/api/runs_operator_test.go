package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// fakeTapeAdmin records every method call so tests can assert that
// the right admin RPC fired. Each method can also be set to error
// (so the handler's BadGateway path is exercised).
type fakeTapeAdmin struct {
	redriveCalls    []string
	reconcileCalls  []string
	cancelCalls     []struct{ ID, Reason string }
	signalCalls     []struct{ ID, Gate, Json string }
	compensateCalls []string
	compactCalls    []string

	errOn map[string]error // method name → error
}

func (f *fakeTapeAdmin) Redrive(_ context.Context, id string) error {
	f.redriveCalls = append(f.redriveCalls, id)
	return f.errOn["Redrive"]
}
func (f *fakeTapeAdmin) Reconcile(_ context.Context, id string) error {
	f.reconcileCalls = append(f.reconcileCalls, id)
	return f.errOn["Reconcile"]
}
func (f *fakeTapeAdmin) Cancel(_ context.Context, id, reason string) error {
	f.cancelCalls = append(f.cancelCalls, struct{ ID, Reason string }{id, reason})
	return f.errOn["Cancel"]
}
func (f *fakeTapeAdmin) Signal(_ context.Context, id, gate, j string) error {
	f.signalCalls = append(f.signalCalls, struct{ ID, Gate, Json string }{id, gate, j})
	return f.errOn["Signal"]
}
func (f *fakeTapeAdmin) Compensate(_ context.Context, id string) error {
	f.compensateCalls = append(f.compensateCalls, id)
	return f.errOn["Compensate"]
}
func (f *fakeTapeAdmin) CompactRun(_ context.Context, id string) (api.TapeCompactResult, error) {
	f.compactCalls = append(f.compactCalls, id)
	return api.TapeCompactResult{}, f.errOn["CompactRun"]
}

func opSetup(t *testing.T, admin api.TapeAdmin) (chi.Router, *registry.MemoryStore) {
	t.Helper()
	store := registry.NewMemoryStore()
	ctx := context.Background()
	_ = store.UpsertExecutionRun(ctx, &models.ExecutionRun{
		RunID:     "run-x",
		TenantID:  "acme",
		AgentID:   "treasury",
		Status:    models.ExecutionRunRunning,
		StartedAt: time.Now(),
	})
	h := api.NewRunsHandler(store).WithTapeAdmin(admin)
	r := chi.NewRouter()
	r.Route("/api/v1/runs", func(r chi.Router) {
		r.Post("/{run_id}/redrive", h.Redrive)
		r.Post("/{run_id}/reconcile", h.Reconcile)
		r.Post("/{run_id}/cancel", h.Cancel)
		r.Post("/{run_id}/signal", h.Signal)
		r.Post("/{run_id}/compensate", h.Compensate)
		r.Post("/{run_id}/compact", h.Compact)
	})
	return r, store
}

func opPost(t *testing.T, r chi.Router, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body == nil {
		rd = bytes.NewReader([]byte("{}"))
	} else {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(http.MethodPost, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Aiplex-Actor", "test@example.com")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestOperator_Redrive_CallsTapeAndAudits(t *testing.T) {
	admin := &fakeTapeAdmin{}
	r, store := opSetup(t, admin)

	rec := opPost(t, r, "/api/v1/runs/run-x/redrive", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
	if len(admin.redriveCalls) != 1 || admin.redriveCalls[0] != "run-x" {
		t.Errorf("Tape admin not called: %+v", admin.redriveCalls)
	}
	// PR 11 item 8: operator action lands in operator_audit, NOT in
	// execution_events. The Tape journal stays clean of synthetic rows.
	events, _ := store.ListExecutionEvents(context.Background(), "run-x", 0, 100)
	if len(events) != 0 {
		t.Errorf("operator action leaked into execution_events: %+v", events)
	}
	audit, _ := store.ListOperatorAudit(context.Background(), "run-x", 100)
	if len(audit) != 1 || audit[0].Action != "redrive" {
		t.Errorf("expected 1 operator_audit row with action=redrive, got %+v", audit)
	}
	if audit[0].Status != "accepted" {
		t.Errorf("expected status=accepted, got %s", audit[0].Status)
	}
}

func TestOperator_Reconcile_CallsTapeAdmin(t *testing.T) {
	admin := &fakeTapeAdmin{}
	r, _ := opSetup(t, admin)
	rec := opPost(t, r, "/api/v1/runs/run-x/reconcile", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(admin.reconcileCalls) != 1 {
		t.Errorf("Reconcile not called: %+v", admin.reconcileCalls)
	}
}

func TestOperator_Cancel_PassesReasonThrough(t *testing.T) {
	admin := &fakeTapeAdmin{}
	r, _ := opSetup(t, admin)
	rec := opPost(t, r, "/api/v1/runs/run-x/cancel", api.CancelBody{Reason: "user requested"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
	if len(admin.cancelCalls) != 1 || admin.cancelCalls[0].Reason != "user requested" {
		t.Errorf("Cancel call did not propagate reason: %+v", admin.cancelCalls)
	}
}

func TestOperator_Signal_RequiresGateName(t *testing.T) {
	admin := &fakeTapeAdmin{}
	r, _ := opSetup(t, admin)
	// Missing gate_name → 400, Tape admin not called.
	rec := opPost(t, r, "/api/v1/runs/run-x/signal", api.SignalBody{})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing gate_name, got %d", rec.Code)
	}
	if len(admin.signalCalls) != 0 {
		t.Errorf("Tape admin called despite validation failure")
	}
	// With gate_name → 202 + propagated to Tape.
	rec = opPost(t, r, "/api/v1/runs/run-x/signal",
		api.SignalBody{GateName: "manager_approval", ResolutionJSON: `{"approved":true}`})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(admin.signalCalls) != 1 ||
		admin.signalCalls[0].Gate != "manager_approval" ||
		admin.signalCalls[0].Json != `{"approved":true}` {
		t.Errorf("Signal call did not propagate body: %+v", admin.signalCalls)
	}
}

func TestOperator_Compensate_CallsTapeAdmin(t *testing.T) {
	admin := &fakeTapeAdmin{}
	r, _ := opSetup(t, admin)
	rec := opPost(t, r, "/api/v1/runs/run-x/compensate", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
	if len(admin.compensateCalls) != 1 {
		t.Errorf("Compensate not called: %+v", admin.compensateCalls)
	}
}

func TestOperator_Compact_CallsTapeAndStampsProjection(t *testing.T) {
	admin := &fakeTapeAdmin{}
	r, store := opSetup(t, admin)
	rec := opPost(t, r, "/api/v1/runs/run-x/compact", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(admin.compactCalls) != 1 || admin.compactCalls[0] != "run-x" {
		t.Errorf("CompactRun not called: %+v", admin.compactCalls)
	}
	got, err := store.GetExecutionRun(context.Background(), "run-x")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !got.Compacted {
		t.Errorf("expected projection to be stamped Compacted=true")
	}
	if got.CompactedAt == nil {
		t.Errorf("expected projection to carry CompactedAt timestamp")
	}
	// PR 13: the manual compact lands in operator_audit, not events.
	events, _ := store.ListExecutionEvents(context.Background(), "run-x", 0, 100)
	if len(events) != 0 {
		t.Errorf("compact leaked into execution_events: %+v", events)
	}
	audit, _ := store.ListOperatorAudit(context.Background(), "run-x", 100)
	if len(audit) != 1 || audit[0].Action != "compact" {
		t.Errorf("expected 1 operator_audit row with action=compact, got %+v", audit)
	}
}

func TestOperator_TapeFailure_Returns502(t *testing.T) {
	admin := &fakeTapeAdmin{errOn: map[string]error{
		"Redrive": errors.New("tape: unavailable"),
	}}
	r, _ := opSetup(t, admin)
	rec := opPost(t, r, "/api/v1/runs/run-x/redrive", nil)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

func TestOperator_UnknownRun_Returns404(t *testing.T) {
	admin := &fakeTapeAdmin{}
	r, _ := opSetup(t, admin)
	rec := opPost(t, r, "/api/v1/runs/ghost/redrive", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
	if len(admin.redriveCalls) != 0 {
		t.Error("Tape admin called for nonexistent run")
	}
}

func TestOperator_AuditCarriesActor(t *testing.T) {
	admin := &fakeTapeAdmin{}
	r, store := opSetup(t, admin)
	opPost(t, r, "/api/v1/runs/run-x/redrive", nil)
	// PR 11: action lands in operator_audit, not execution_events.
	audit, _ := store.ListOperatorAudit(context.Background(), "run-x", 100)
	if len(audit) != 1 {
		t.Fatalf("expected 1 operator audit row, got %d", len(audit))
	}
	if audit[0].Action != "redrive" {
		t.Errorf("expected action=redrive, got %s", audit[0].Action)
	}
	if audit[0].Actor != "test@example.com" {
		t.Errorf("expected actor=test@example.com, got %s", audit[0].Actor)
	}
}

// Tiny helpers — keep payload parsing tolerant. We don't reach for a
// struct definition because the audit payload is intentionally
// free-form (per-action keys).
func containsOperatorAction(payloadJSON, action string) bool {
	m := map[string]any{}
	if err := json.Unmarshal([]byte(payloadJSON), &m); err != nil {
		return false
	}
	got, _ := m["operator_action"].(string)
	return got == action
}

func containsField(payloadJSON, key, want string) bool {
	m := map[string]any{}
	if err := json.Unmarshal([]byte(payloadJSON), &m); err != nil {
		return false
	}
	got, _ := m[key].(string)
	return got == want
}

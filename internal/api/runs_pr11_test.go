package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// PR 11 item 9 — projection rebuild.

func TestRebuildProjection_RecomputesFromEvents(t *testing.T) {
	store := registry.NewMemoryStore()
	ctx := context.Background()
	_ = store.PutInstance(ctx, &models.Instance{ID: "inst-x", Plane: models.PlaneA2APlex})

	now := time.Now()
	events := []models.ExecutionEvent{
		{RunID: "r-1", Seq: 1, AIPlexInstanceID: "inst-x", TenantID: "acme", AgentID: "t",
			Kind: models.ExecutionEventRunStarted, Timestamp: now},
		{RunID: "r-1", Seq: 2, AIPlexInstanceID: "inst-x",
			Kind: models.ExecutionEventDecisionRecorded, Timestamp: now.Add(time.Second)},
		{RunID: "r-1", Seq: 3, AIPlexInstanceID: "inst-x",
			Kind: models.ExecutionEventEffectBegin, Timestamp: now.Add(2 * time.Second)},
		{RunID: "r-1", Seq: 4, AIPlexInstanceID: "inst-x",
			Kind: models.ExecutionEventEffectConfirmed, Timestamp: now.Add(3 * time.Second)},
	}
	for i := range events {
		if _, err := store.AppendExecutionEvent(ctx, &events[i]); err != nil {
			t.Fatal(err)
		}
	}
	// Now corrupt the projection — set wildly wrong counters.
	_ = store.UpsertExecutionRun(ctx, &models.ExecutionRun{
		RunID:           "r-1",
		DecisionsCount:  999,
		EffectsCount:    999,
		Status:          models.ExecutionRunFailed,
	})

	h := api.NewRunsHandler(store)
	r := chi.NewRouter()
	r.Post("/internal/projections/rebuild/{run_id}", h.RebuildProjection)

	req := httptest.NewRequest(http.MethodPost, "/internal/projections/rebuild/r-1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Projection should now match the events.
	run, err := store.GetExecutionRun(ctx, "r-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.DecisionsCount != 1 || run.EffectsCount != 2 {
		t.Errorf("expected (1 decision, 2 effects) after rebuild, got (%d, %d)",
			run.DecisionsCount, run.EffectsCount)
	}
	if run.Status != models.ExecutionRunRunning {
		// (run.started but no completion yet → running)
		t.Errorf("expected status=running, got %q", run.Status)
	}
}

func TestRebuildProjection_NoEvents_404(t *testing.T) {
	store := registry.NewMemoryStore()
	h := api.NewRunsHandler(store)
	r := chi.NewRouter()
	r.Post("/internal/projections/rebuild/{run_id}", h.RebuildProjection)

	req := httptest.NewRequest(http.MethodPost, "/internal/projections/rebuild/ghost", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for run with no events, got %d", rec.Code)
	}
}

// PR 11 item 13 — runs health.

func TestRunsHealth_ReturnsState(t *testing.T) {
	store := registry.NewMemoryStore()
	ctx := context.Background()
	// Seed an instance with tape runtime.
	_ = store.PutInstance(ctx, &models.Instance{
		ID:      "inst-tape",
		Plane:   models.PlaneA2APlex,
		Runtime: models.TapeRuntime(),
	})
	// Seed an event so LastIngestAt advances.
	_, _ = store.AppendExecutionEvent(ctx, &models.ExecutionEvent{
		RunID: "r-1", Seq: 1, AIPlexInstanceID: "inst-tape",
		Kind: models.ExecutionEventRunStarted, Timestamp: time.Now(),
	})
	_ = store.UpsertExecutionRun(ctx, &models.ExecutionRun{RunID: "r-1"})

	h := api.NewRunsHandler(store)
	r := chi.NewRouter()
	r.Get("/api/v1/runs/_health", h.Health)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/runs/_health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["has_runs"] != true {
		t.Errorf("expected has_runs=true, got %v", body["has_runs"])
	}
	if n, ok := body["tape_instances_count"].(float64); !ok || n != 1 {
		t.Errorf("expected tape_instances_count=1, got %v", body["tape_instances_count"])
	}
}

// PR 11 item 8 — operator audit listing endpoint.

func TestListOperatorAudit_ReturnsAppendedRows(t *testing.T) {
	store := registry.NewMemoryStore()
	ctx := context.Background()
	_ = store.AppendOperatorAudit(ctx, &models.OperatorAudit{
		RunID: "r-1", Action: "redrive", Actor: "alice", At: time.Now(), Status: "accepted",
	})
	_ = store.AppendOperatorAudit(ctx, &models.OperatorAudit{
		RunID: "r-1", Action: "cancel", Actor: "bob", At: time.Now(), Reason: "oops", Status: "accepted",
	})

	h := api.NewRunsHandler(store)
	r := chi.NewRouter()
	r.Get("/api/v1/runs/{run_id}/operator-audit", h.ListOperatorAudit)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/runs/r-1/operator-audit", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Audit []models.OperatorAudit `json:"audit"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Audit) != 2 {
		t.Errorf("expected 2 rows, got %d", len(resp.Audit))
	}
}

// PR 11 item 6 — multi-tenant filter. Without a tenant context the
// caller-supplied query value is used as-is (legacy behaviour).
// With a tenant context, cross-tenant requests return empty rather
// than 403 (silent denial — see TestTenantFromContext_FromScope
// for the empty-context flow).

func TestList_NoTenantContext_HonoursQuery(t *testing.T) {
	store := registry.NewMemoryStore()
	ctx := context.Background()
	_ = store.UpsertExecutionRun(ctx, &models.ExecutionRun{RunID: "r-a", TenantID: "acme"})
	_ = store.UpsertExecutionRun(ctx, &models.ExecutionRun{RunID: "r-b", TenantID: "globex"})

	h := api.NewRunsHandler(store)
	r := chi.NewRouter()
	r.Get("/api/v1/runs", h.List)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/runs?tenant_id=acme", nil))
	var resp struct {
		Runs []models.ExecutionRun `json:"runs"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Runs) != 1 || resp.Runs[0].RunID != "r-a" {
		t.Errorf("expected just r-a (acme), got %+v", resp.Runs)
	}
}

// ── Cancel body shape compat ──────────────────────────────────────────────

func TestOperatorEndpoint_BodyShapeStable(t *testing.T) {
	// Smoke test that the cancel endpoint accepts the documented body
	// and the reason makes it to the audit row.
	store := registry.NewMemoryStore()
	_ = store.UpsertExecutionRun(context.Background(), &models.ExecutionRun{RunID: "r-x"})
	h := api.NewRunsHandler(store)
	r := chi.NewRouter()
	r.Post("/api/v1/runs/{run_id}/cancel", h.Cancel)

	body := []byte(`{"reason":"end of demo"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runs/r-x/cancel", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	audit, _ := store.ListOperatorAudit(context.Background(), "r-x", 10)
	if len(audit) != 1 || audit[0].Reason != "end of demo" {
		t.Errorf("expected cancel reason captured, got %+v", audit)
	}
}

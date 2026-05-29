package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// runsReadSetup builds a router with the read API mounted under
// /api/v1/runs, seeded with three runs for two tenants/agents so the
// filter behaviour is testable.
func runsReadSetup(t *testing.T) (chi.Router, *registry.MemoryStore) {
	t.Helper()
	store := registry.NewMemoryStore()
	ctx := context.Background()
	now := time.Now()

	// Seed instances (so ingestion below isn't quarantined).
	for _, id := range []string{"treasury-abc", "research-def"} {
		_ = store.PutInstance(ctx, &models.Instance{ID: id, Plane: models.PlaneA2APlex})
	}

	// Three runs:
	//   run-1: acme / treasury-agent, terminal, 1 unknown effect
	//   run-2: acme / treasury-agent, running, 1 obligation
	//   run-3: globex / research-agent, terminal, nothing notable
	seed := []models.ExecutionRun{
		{
			RunID: "run-1", TenantID: "acme", AgentID: "treasury-agent",
			Plane: "a2aplex", Actor: "spiffe://acme/treasury",
			AIPlexInstanceID: "treasury-abc",
			Status:           models.ExecutionRunTerminal,
			StartedAt:        now.Add(-1 * time.Hour),
			DecisionsCount:   2, EffectsCount: 3, UnknownEffects: 1,
		},
		{
			RunID: "run-2", TenantID: "acme", AgentID: "treasury-agent",
			Plane: "a2aplex", Actor: "spiffe://acme/treasury",
			AIPlexInstanceID: "treasury-abc",
			Status:           models.ExecutionRunCompensating,
			StartedAt:        now.Add(-30 * time.Minute),
			DecisionsCount:   1, EffectsCount: 2, Obligations: 1,
		},
		{
			RunID: "run-3", TenantID: "globex", AgentID: "research-agent",
			Plane: "a2aplex", Actor: "spiffe://globex/research",
			AIPlexInstanceID: "research-def",
			Status:           models.ExecutionRunTerminal,
			StartedAt:        now.Add(-10 * time.Minute),
			DecisionsCount:   3,
		},
	}
	for _, r := range seed {
		r := r
		if err := store.UpsertExecutionRun(ctx, &r); err != nil {
			t.Fatal(err)
		}
	}

	// Also seed some events on run-1 so /events / /effects / /obligations / /budgets are non-empty.
	events := []models.ExecutionEvent{
		{RunID: "run-1", Seq: 1, AIPlexInstanceID: "treasury-abc",
			Kind: models.ExecutionEventRunStarted, Timestamp: now.Add(-1 * time.Hour)},
		{RunID: "run-1", Seq: 2, AIPlexInstanceID: "treasury-abc",
			Kind: models.ExecutionEventDecisionRecorded, Timestamp: now.Add(-59 * time.Minute)},
		{RunID: "run-1", Seq: 3, AIPlexInstanceID: "treasury-abc",
			Kind: models.ExecutionEventEffectBegin, Timestamp: now.Add(-58 * time.Minute)},
		{RunID: "run-1", Seq: 4, AIPlexInstanceID: "treasury-abc",
			Kind: models.ExecutionEventEffectUnknown, Timestamp: now.Add(-57 * time.Minute)},
		{RunID: "run-1", Seq: 5, AIPlexInstanceID: "treasury-abc",
			Kind: models.ExecutionEventObligationCreated, Timestamp: now.Add(-56 * time.Minute)},
		{RunID: "run-1", Seq: 6, AIPlexInstanceID: "treasury-abc",
			Kind:        models.ExecutionEventBudgetCharged,
			PayloadJSON: `{"usd_charged":0.42}`,
			Timestamp:   now.Add(-55 * time.Minute)},
	}
	for i := range events {
		if _, err := store.AppendExecutionEvent(ctx, &events[i]); err != nil {
			t.Fatal(err)
		}
	}

	runs := api.NewRunsHandler(store)
	r := chi.NewRouter()
	r.Route("/api/v1/runs", func(r chi.Router) {
		r.Get("/", runs.List)
		r.Get("/{run_id}", runs.Get)
		r.Get("/{run_id}/events", runs.Events)
		r.Get("/{run_id}/effects", runs.Effects)
		r.Get("/{run_id}/obligations", runs.Obligations)
		r.Get("/{run_id}/budgets", runs.Budgets)
	})
	return r, store
}

func getJSON(t *testing.T, r chi.Router, url string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	r.ServeHTTP(rec, req)
	if out != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v\nbody: %s", url, err, rec.Body.String())
		}
	}
	return rec.Code
}

func TestRunsList_All(t *testing.T) {
	r, _ := runsReadSetup(t)
	var resp struct {
		Runs []models.ExecutionRun `json:"runs"`
	}
	if code := getJSON(t, r, "/api/v1/runs", &resp); code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(resp.Runs) != 3 {
		t.Errorf("expected 3 runs, got %d", len(resp.Runs))
	}
}

func TestRunsList_TenantFilter(t *testing.T) {
	r, _ := runsReadSetup(t)
	var resp struct {
		Runs []models.ExecutionRun `json:"runs"`
	}
	getJSON(t, r, "/api/v1/runs?tenant_id=acme", &resp)
	if len(resp.Runs) != 2 {
		t.Errorf("expected 2 acme runs, got %d", len(resp.Runs))
	}
	for _, run := range resp.Runs {
		if run.TenantID != "acme" {
			t.Errorf("got non-acme run: %s", run.RunID)
		}
	}
}

func TestRunsList_HasUnknownEffectsFilter(t *testing.T) {
	r, _ := runsReadSetup(t)
	var resp struct {
		Runs []models.ExecutionRun `json:"runs"`
	}
	getJSON(t, r, "/api/v1/runs?has_unknown_effects=true", &resp)
	if len(resp.Runs) != 1 || resp.Runs[0].RunID != "run-1" {
		t.Errorf("expected only run-1 (has unknown effects), got %+v",
			runIDs(resp.Runs))
	}
}

func TestRunsList_HasObligationsFilter(t *testing.T) {
	r, _ := runsReadSetup(t)
	var resp struct {
		Runs []models.ExecutionRun `json:"runs"`
	}
	getJSON(t, r, "/api/v1/runs?has_obligations=true", &resp)
	if len(resp.Runs) != 1 || resp.Runs[0].RunID != "run-2" {
		t.Errorf("expected only run-2 (has obligations), got %+v",
			runIDs(resp.Runs))
	}
}

func TestRunsGet_Found(t *testing.T) {
	r, _ := runsReadSetup(t)
	var run models.ExecutionRun
	if code := getJSON(t, r, "/api/v1/runs/run-1", &run); code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if run.RunID != "run-1" || run.UnknownEffects != 1 {
		t.Errorf("unexpected run: %+v", run)
	}
}

func TestRunsGet_NotFound_404(t *testing.T) {
	r, _ := runsReadSetup(t)
	code := getJSON(t, r, "/api/v1/runs/ghost", nil)
	if code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", code)
	}
}

func TestRunsEvents_Ordered(t *testing.T) {
	r, _ := runsReadSetup(t)
	var resp struct {
		Events []models.ExecutionEvent `json:"events"`
	}
	getJSON(t, r, "/api/v1/runs/run-1/events", &resp)
	if len(resp.Events) != 6 {
		t.Fatalf("expected 6 events on run-1, got %d", len(resp.Events))
	}
	for i, ev := range resp.Events {
		if ev.Seq != int64(i+1) {
			t.Errorf("event %d: expected seq=%d, got %d", i, i+1, ev.Seq)
		}
	}
}

func TestRunsEvents_FromSeq(t *testing.T) {
	r, _ := runsReadSetup(t)
	var resp struct {
		Events []models.ExecutionEvent `json:"events"`
	}
	getJSON(t, r, "/api/v1/runs/run-1/events?from_seq=4", &resp)
	if len(resp.Events) != 3 {
		t.Fatalf("expected events 4..6, got %d", len(resp.Events))
	}
	if resp.Events[0].Seq != 4 {
		t.Errorf("expected first seq=4, got %d", resp.Events[0].Seq)
	}
}

func TestRunsEffects_OnlyEffectKinds(t *testing.T) {
	r, _ := runsReadSetup(t)
	var resp struct {
		Effects []models.ExecutionEvent `json:"effects"`
	}
	getJSON(t, r, "/api/v1/runs/run-1/effects", &resp)
	// seq 3 (effect.begin) + seq 4 (effect.unknown) = 2 effect events
	if len(resp.Effects) != 2 {
		t.Errorf("expected 2 effect events, got %d", len(resp.Effects))
	}
	for _, ev := range resp.Effects {
		if !isEffectKindForTest(ev.Kind) {
			t.Errorf("non-effect kind leaked: %s", ev.Kind)
		}
	}
}

func TestRunsObligations(t *testing.T) {
	r, _ := runsReadSetup(t)
	var resp struct {
		Obligations []models.ExecutionEvent `json:"obligations"`
	}
	getJSON(t, r, "/api/v1/runs/run-1/obligations", &resp)
	if len(resp.Obligations) != 1 {
		t.Errorf("expected 1 obligation event, got %d", len(resp.Obligations))
	}
}

func TestRunsBudgets(t *testing.T) {
	r, _ := runsReadSetup(t)
	var resp struct {
		Budgets []models.ExecutionEvent `json:"budgets"`
	}
	getJSON(t, r, "/api/v1/runs/run-1/budgets", &resp)
	if len(resp.Budgets) != 1 {
		t.Fatalf("expected 1 budget event, got %d", len(resp.Budgets))
	}
	if resp.Budgets[0].PayloadJSON == "" {
		t.Error("expected budget payload_json present")
	}
}

func isEffectKindForTest(k models.ExecutionEventKind) bool {
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

func runIDs(runs []models.ExecutionRun) string {
	ids := ""
	for i, r := range runs {
		if i > 0 {
			ids += ","
		}
		ids += r.RunID
	}
	return fmt.Sprintf("[%s]", ids)
}

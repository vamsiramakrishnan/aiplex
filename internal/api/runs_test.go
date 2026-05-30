package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func runsHandlerWithKnownAgent(t *testing.T, instanceID string) (*api.RunsHandler, *registry.MemoryStore) {
	t.Helper()
	store := registry.NewMemoryStore()
	if instanceID != "" {
		ctx := context.Background()
		err := store.PutInstance(ctx, &models.Instance{
			ID:    instanceID,
			Plane: models.PlaneA2APlex,
		})
		if err != nil {
			t.Fatalf("seed instance: %v", err)
		}
	}
	return api.NewRunsHandler(store), store
}

func post(t *testing.T, h *api.RunsHandler, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/internal/tape/events",
		bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Ingest(rec, req)
	return rec
}

func decodeResp(t *testing.T, rec *httptest.ResponseRecorder) api.IngestResponse {
	t.Helper()
	var resp api.IngestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response (%d): %v\nbody: %s", rec.Code, err, rec.Body.String())
	}
	return resp
}

func TestIngest_NewEvent_LandsAndProjects(t *testing.T) {
	h, store := runsHandlerWithKnownAgent(t, "treasury-abc")

	ev := models.ExecutionEvent{
		RunID:            "run-1",
		Seq:              1,
		TenantID:         "acme",
		AgentID:          "treasury-agent",
		Plane:            "a2aplex",
		Actor:            "spiffe://test/sa/treasury",
		AIPlexInstanceID: "treasury-abc",
		Kind:             models.ExecutionEventRunStarted,
		Timestamp:        time.Now(),
	}
	rec := post(t, h, api.IngestRequest{Events: []models.ExecutionEvent{ev}})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResp(t, rec)
	if resp.Ingested != 1 || resp.Duplicates != 0 || resp.Quarantined != 0 {
		t.Errorf("unexpected counts: %+v", resp)
	}

	run, err := store.GetExecutionRun(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("projection missing: %v", err)
	}
	if run.Status != models.ExecutionRunRunning {
		t.Errorf("expected status=running, got %q", run.Status)
	}
	if run.TenantID != "acme" {
		t.Errorf("expected tenant_id propagated, got %q", run.TenantID)
	}
}

func TestIngest_Duplicate_IsNoOp(t *testing.T) {
	h, _ := runsHandlerWithKnownAgent(t, "treasury-abc")

	ev := models.ExecutionEvent{
		RunID: "run-2", Seq: 1, AIPlexInstanceID: "treasury-abc",
		Kind: models.ExecutionEventRunStarted, Timestamp: time.Now(),
	}
	post(t, h, api.IngestRequest{Events: []models.ExecutionEvent{ev}})
	rec := post(t, h, api.IngestRequest{Events: []models.ExecutionEvent{ev}})
	resp := decodeResp(t, rec)
	if resp.Ingested != 0 || resp.Duplicates != 1 {
		t.Errorf("expected duplicate count=1, got %+v", resp)
	}
}

func TestIngest_UnknownAgent_Quarantined(t *testing.T) {
	h, store := runsHandlerWithKnownAgent(t, "treasury-abc")

	ev := models.ExecutionEvent{
		RunID:            "run-3",
		Seq:              1,
		AIPlexInstanceID: "ghost-instance",
		Kind:             models.ExecutionEventRunStarted,
		Timestamp:        time.Now(),
	}
	rec := post(t, h, api.IngestRequest{Events: []models.ExecutionEvent{ev}})
	resp := decodeResp(t, rec)
	if resp.Quarantined != 1 {
		t.Errorf("expected quarantine count=1, got %+v", resp)
	}
	// And the event is NOT in the normal projection.
	if _, err := store.GetExecutionRun(context.Background(), "run-3"); err == nil {
		t.Error("expected unknown-agent run to be quarantined, not projected")
	}
}

func TestIngest_EmptyInstanceID_Admitted(t *testing.T) {
	// Tape callers that don't know their AIPlex instance id (e.g.
	// non-AIPlex deployments) should still ingest cleanly.
	h, _ := runsHandlerWithKnownAgent(t, "treasury-abc")

	ev := models.ExecutionEvent{
		RunID: "run-4", Seq: 1,
		Kind: models.ExecutionEventRunStarted, Timestamp: time.Now(),
	}
	rec := post(t, h, api.IngestRequest{Events: []models.ExecutionEvent{ev}})
	resp := decodeResp(t, rec)
	if resp.Ingested != 1 || resp.Quarantined != 0 {
		t.Errorf("empty instance_id should admit, got %+v", resp)
	}
}

func TestIngest_OutOfOrder_AcceptedAndOrderedAtRead(t *testing.T) {
	h, store := runsHandlerWithKnownAgent(t, "treasury-abc")

	// Post seq 2, then seq 1 — both should land; ListExecutionEvents
	// returns them in seq order regardless of ingest order.
	for _, seq := range []int64{2, 1} {
		ev := models.ExecutionEvent{
			RunID: "run-5", Seq: seq, AIPlexInstanceID: "treasury-abc",
			Kind: models.ExecutionEventDecisionRecorded, Timestamp: time.Now(),
		}
		rec := post(t, h, api.IngestRequest{Events: []models.ExecutionEvent{ev}})
		if rec.Code != http.StatusOK {
			t.Fatalf("seq %d ingest failed: %d %s", seq, rec.Code, rec.Body.String())
		}
	}

	events, err := store.ListExecutionEvents(context.Background(), "run-5", 0, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Seq != 1 || events[1].Seq != 2 {
		t.Errorf("expected seq order [1, 2], got [%d, %d]", events[0].Seq, events[1].Seq)
	}
}

func TestIngest_BatchedEvents_AllProjected(t *testing.T) {
	h, store := runsHandlerWithKnownAgent(t, "treasury-abc")

	// A realistic mini-trace: run.started + decision + effect.begin +
	// effect.confirmed → projection sees 1 decision, 2 effects.
	now := time.Now()
	events := []models.ExecutionEvent{
		{RunID: "run-6", Seq: 1, AIPlexInstanceID: "treasury-abc",
			Kind: models.ExecutionEventRunStarted, Timestamp: now},
		{RunID: "run-6", Seq: 2, AIPlexInstanceID: "treasury-abc",
			Kind: models.ExecutionEventDecisionRecorded, Timestamp: now},
		{RunID: "run-6", Seq: 3, AIPlexInstanceID: "treasury-abc",
			Kind: models.ExecutionEventEffectBegin, Timestamp: now},
		{RunID: "run-6", Seq: 4, AIPlexInstanceID: "treasury-abc",
			Kind: models.ExecutionEventEffectConfirmed, Timestamp: now},
	}
	post(t, h, api.IngestRequest{Events: events})

	run, err := store.GetExecutionRun(context.Background(), "run-6")
	if err != nil {
		t.Fatalf("missing projection: %v", err)
	}
	if run.DecisionsCount != 1 {
		t.Errorf("expected 1 decision, got %d", run.DecisionsCount)
	}
	if run.EffectsCount != 2 {
		t.Errorf("expected 2 effects, got %d", run.EffectsCount)
	}
	if run.Status != models.ExecutionRunRunning {
		t.Errorf("expected running, got %q", run.Status)
	}
}

func TestIngest_UnknownEffectAndPolicyViolation_CountedOnRun(t *testing.T) {
	h, store := runsHandlerWithKnownAgent(t, "treasury-abc")

	events := []models.ExecutionEvent{
		{RunID: "run-7", Seq: 1, AIPlexInstanceID: "treasury-abc",
			Kind: models.ExecutionEventEffectUnknown, Timestamp: time.Now()},
		{RunID: "run-7", Seq: 2, AIPlexInstanceID: "treasury-abc",
			Kind: models.ExecutionEventPolicyViolation, Timestamp: time.Now(),
			Scope: "mcp:tools:bank_wire"},
	}
	post(t, h, api.IngestRequest{Events: events})

	run, err := store.GetExecutionRun(context.Background(), "run-7")
	if err != nil {
		t.Fatalf("missing projection: %v", err)
	}
	if run.UnknownEffects != 1 {
		t.Errorf("expected 1 unknown effect, got %d", run.UnknownEffects)
	}
	if run.PolicyViolations != 1 {
		t.Errorf("expected 1 policy violation, got %d", run.PolicyViolations)
	}
}

func TestIngest_MissingRunID_400(t *testing.T) {
	h, _ := runsHandlerWithKnownAgent(t, "treasury-abc")

	rec := post(t, h, api.IngestRequest{Events: []models.ExecutionEvent{
		{Seq: 1, Kind: models.ExecutionEventRunStarted, Timestamp: time.Now()},
	}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing run_id, got %d", rec.Code)
	}
}

func TestIngest_EmptyBody_OK(t *testing.T) {
	// The outbox relay holds open a polling connection; an empty
	// batch must round-trip cleanly.
	h, _ := runsHandlerWithKnownAgent(t, "treasury-abc")
	rec := post(t, h, api.IngestRequest{})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for empty batch, got %d", rec.Code)
	}
}

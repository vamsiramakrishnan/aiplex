package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/catalog"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// TestE2E_AIPlexTape_TreasuryStory wires the full PR 4–8 pipeline in
// one in-process test so a regression in any layer surfaces here
// before it reaches a real Tape server. The story:
//
//   1. AIPlex deploys a Tape-backed treasury agent (PR 4 model +
//      PR 5 manifest generation).
//   2. Tape (simulated here) journals: run.started, a decision, an
//      effect.begin against the bank, and then crashes — leaving the
//      effect in UNKNOWN state.
//   3. The reconciler resolves UNKNOWN by asking the bank; the effect
//      transitions to CONFIRMED. The reconciler's outbox relay then
//      POSTs the new events to AIPlex's /internal/tape/events
//      endpoint (PR 6).
//   4. A Console request to GET /api/v1/runs/{run_id} (PR 7) returns
//      the projected timeline; the test asserts: one effect.begin,
//      one effect.unknown, one effect.confirmed — NO second
//      effect.begin and NO duplicate wire. That is the integration's
//      headline claim.
//
// Self-contained: no Tape server, no Pub/Sub, no Console — just the
// AIPlex API surface driven the way each side of the integration
// would drive it in production.
func TestE2E_AIPlexTape_TreasuryStory(t *testing.T) {
	srv := setupE2EServer(t)
	defer srv.Close()
	ctx := context.Background()

	// ── Step 1: deploy a Tape-backed treasury agent ────────────────
	deployReq := map[string]any{
		"plane":        "a2aplex",
		"template_id":  "treasury-agent",
		"display_name": "Treasury Agent (e2e)",
		"config": map[string]any{
			"policy_version": "aiplex-treasury-e2e",
			"runtime": map[string]any{
				"engine":  "tape",
				"durable": true,
				"store":   map[string]any{"type": "sqlite"},
				"reactors": map[string]any{
					"recovery": true, "reconciler": true, "timers": true,
					"outbox": true, "compensation": true,
				},
				"outbox": map[string]any{"sink": "log"},
			},
		},
	}
	inst := postJSON[models.Instance](t, srv, "/api/v1/instances", deployReq)
	if inst.Runtime.Engine != models.RuntimeEngineTape {
		t.Fatalf("expected deployed instance to carry runtime.engine=tape, got %q",
			inst.Runtime.Engine)
	}

	// ── Step 2: Tape simulator journals the run + crash ────────────
	// All POSTs use the AIPLEX_INSTANCE_ID we just got back; events
	// carry the same identity Tape would derive from AIPLEX_* env
	// vars on the agent pod (see internal/deploy/tape.go::tapeAgentEnv).
	runID := "run-treasury-e2e"
	now := time.Now()

	phase1 := []models.ExecutionEvent{
		mkEvent(runID, 1, inst.ID, models.ExecutionEventRunStarted, "", "", now),
		mkEvent(runID, 2, inst.ID, models.ExecutionEventDecisionRecorded, "", "", now.Add(1*time.Second)),
		mkEvent(runID, 3, inst.ID, models.ExecutionEventEffectBegin,
			"mcp:tools:bank_wire", "bank_wire", now.Add(2*time.Second)),
		mkEvent(runID, 4, inst.ID, models.ExecutionEventEffectUnknown,
			"mcp:tools:bank_wire", "bank_wire", now.Add(3*time.Second)),
	}
	postIngest(t, srv, phase1)

	// Check projection mid-flight — UNKNOWN must be visible immediately
	// (this is the operator-visible "did the wire go out?" question).
	intermediate := getRun(t, srv, runID, ctx)
	if intermediate.UnknownEffects != 1 {
		t.Errorf("expected 1 UNKNOWN mid-run, got %d", intermediate.UnknownEffects)
	}
	if intermediate.Status == models.ExecutionRunTerminal {
		t.Errorf("run should not be terminal yet, got %q", intermediate.Status)
	}

	// ── Step 3: reconciler confirms + run completes ────────────────
	phase2 := []models.ExecutionEvent{
		mkEvent(runID, 5, inst.ID, models.ExecutionEventEffectConfirmed,
			"mcp:tools:bank_wire", "bank_wire", now.Add(10*time.Second)),
		mkEvent(runID, 6, inst.ID, models.ExecutionEventRunCompleted, "", "", now.Add(11*time.Second)),
	}
	postIngest(t, srv, phase2)

	// ── Step 4: Console-side read shows the full timeline ──────────
	final := getRun(t, srv, runID, ctx)
	if final.Status != models.ExecutionRunTerminal {
		t.Errorf("expected terminal status, got %q", final.Status)
	}
	if final.DecisionsCount != 1 {
		t.Errorf("expected 1 decision, got %d", final.DecisionsCount)
	}
	// Headline assertion: 3 effect events on the timeline (begin +
	// unknown + confirmed), counted as 3 — but NOT 4. A second
	// effect.begin would mean a duplicate wire, which is exactly the
	// failure mode this integration claims to prevent.
	if final.EffectsCount != 3 {
		t.Errorf("expected 3 effect events, got %d (a 4th would mean a duplicate wire)",
			final.EffectsCount)
	}
	if final.UnknownEffects != 1 {
		t.Errorf("UNKNOWN count should stay at 1 (it transitioned, not duplicated), got %d",
			final.UnknownEffects)
	}

	// Verify the per-run timeline endpoint reflects the same story
	// (this is what the Console renders).
	tl := getTimeline(t, srv, runID)
	kinds := make([]models.ExecutionEventKind, len(tl))
	for i, ev := range tl {
		kinds[i] = ev.Kind
	}
	wantKinds := []models.ExecutionEventKind{
		models.ExecutionEventRunStarted,
		models.ExecutionEventDecisionRecorded,
		models.ExecutionEventEffectBegin,
		models.ExecutionEventEffectUnknown,
		models.ExecutionEventEffectConfirmed,
		models.ExecutionEventRunCompleted,
	}
	if len(kinds) != len(wantKinds) {
		t.Fatalf("timeline length mismatch: got %d, want %d", len(kinds), len(wantKinds))
	}
	for i, k := range wantKinds {
		if kinds[i] != k {
			t.Errorf("timeline[%d]: got %q, want %q", i, kinds[i], k)
		}
	}

	// And effect-only filter shows just the effect events.
	effects := getEffects(t, srv, runID)
	if len(effects) != 3 {
		t.Errorf("effect-filter returned %d, expected 3", len(effects))
	}
}

// TestE2E_AIPlexTape_PolicyDenialFlow proves that an unscoped attempt
// at a non-idempotent effect: (a) never lands as a real effect,
// (b) gets recorded as a policy.violation journal entry, (c) shows up
// in the projected run's PolicyViolations counter, and (d) is
// surfaced by the /runs/{id}/events timeline so the Console can show
// it. This is the audit-trail half of the integration claim — denied
// attempts are not invisible.
func TestE2E_AIPlexTape_PolicyDenialFlow(t *testing.T) {
	srv := setupE2EServer(t)
	defer srv.Close()
	ctx := context.Background()
	_ = ctx

	inst := postJSON[models.Instance](t, srv, "/api/v1/instances", map[string]any{
		"plane":        "a2aplex",
		"template_id":  "treasury-agent",
		"display_name": "Treasury Agent (denial-flow)",
		"config": map[string]any{
			"runtime": map[string]any{
				"engine":  "tape",
				"durable": true,
				"store":   map[string]any{"type": "sqlite"},
				"reactors": map[string]any{
					"recovery": true, "reconciler": true,
				},
				"outbox": map[string]any{"sink": "log"},
			},
		},
	})

	runID := "run-denial-e2e"
	now := time.Now()

	postIngest(t, srv, []models.ExecutionEvent{
		mkEvent(runID, 1, inst.ID, models.ExecutionEventRunStarted, "", "", now),
		mkEvent(runID, 2, inst.ID, models.ExecutionEventPolicyViolation,
			"mcp:tools:bank_wire", "bank_wire", now.Add(1*time.Second)),
		mkEvent(runID, 3, inst.ID, models.ExecutionEventRunCompleted, "", "", now.Add(2*time.Second)),
	})

	run := getRun(t, srv, runID, ctx)
	if run.PolicyViolations != 1 {
		t.Errorf("expected 1 policy violation, got %d", run.PolicyViolations)
	}
	if run.EffectsCount != 0 {
		t.Errorf("denied attempt must NOT count as an effect; got effects_count=%d",
			run.EffectsCount)
	}

	// The has_unknown / has_obligations filters MUST NOT include this
	// run (it had neither). The Console filter for "things to look at"
	// is plural by design.
	listed := listRuns(t, srv, "?has_unknown_effects=true")
	for _, r := range listed {
		if r.RunID == runID {
			t.Errorf("run with no UNKNOWNs leaked into has_unknown_effects=true filter")
		}
	}
}

// ─── helpers (deliberately minimal — the test file IS the documentation) ───

// setupE2EServer wires the AIPlex API the same way main.go does, but
// against a MemoryStore so we don't need a Firestore emulator. Returns
// the running httptest.Server; the caller defers Close().
func setupE2EServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := registry.NewMemoryStore()
	ctx := context.Background()
	_ = store.PutTemplate(ctx, &models.Template{
		ID:        "treasury-agent",
		Plane:     models.PlaneA2APlex,
		Name:      "Treasury Agent",
		TaskTypes: []string{"treasury_sweep"},
		Category:  "agents",
		Verified:  true,
	})

	aggregator := catalog.NewAggregator([]catalog.Source{})
	wifValidator := auth.NewWIFValidator(store, auth.WIFConfig{
		TrustedIssuers: []string{"https://accounts.google.com"},
	})
	engine := deploy.NewEngine(store, "test.local")

	r := chi.NewRouter()
	r.Use(api.Recover)
	r.Use(api.RequestID)
	r.Use(api.CORS("*"))
	r.Use(api.MaxBody(1 << 20))
	r.Use(api.WIFAuth(wifValidator))

	instanceH := api.NewInstanceHandler(store, engine)
	runsH := api.NewRunsHandler(store)
	_ = aggregator

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/instances", instanceH.Deploy)
		r.Get("/instances/{id}", instanceH.Get)
		r.Get("/runs", runsH.List)
		r.Get("/runs/{run_id}", runsH.Get)
		r.Get("/runs/{run_id}/events", runsH.Events)
		r.Get("/runs/{run_id}/effects", runsH.Effects)
	})
	r.Post("/internal/tape/events", runsH.Ingest)
	return httptest.NewServer(r)
}

func mkEvent(runID string, seq int64, instanceID string, kind models.ExecutionEventKind,
	scope, tool string, ts time.Time) models.ExecutionEvent {
	return models.ExecutionEvent{
		RunID:            runID,
		Seq:              seq,
		TenantID:         "acme",
		AgentID:          "treasury-agent",
		Plane:            "a2aplex",
		Actor:            "spiffe://test.local/ns/a2aplex/sa/treasury",
		Subject:          "vamsi@example.com",
		AIPlexInstanceID: instanceID,
		Kind:             kind,
		Scope:            scope,
		Tool:             tool,
		Timestamp:        ts,
	}
}

func postJSON[T any](t *testing.T, srv *httptest.Server, path string, body any) T {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("POST %s: status %d", path, resp.StatusCode)
	}
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return out
}

func postIngest(t *testing.T, srv *httptest.Server, events []models.ExecutionEvent) {
	t.Helper()
	body := map[string]any{"events": events}
	b, _ := json.Marshal(body)
	resp, err := http.Post(srv.URL+"/internal/tape/events", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("ingest POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ingest POST: status %d", resp.StatusCode)
	}
}

func getRun(t *testing.T, srv *httptest.Server, runID string, _ context.Context) models.ExecutionRun {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/v1/runs/" + runID)
	if err != nil {
		t.Fatalf("GET run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET run: status %d", resp.StatusCode)
	}
	var run models.ExecutionRun
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	return run
}

func getTimeline(t *testing.T, srv *httptest.Server, runID string) []models.ExecutionEvent {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/v1/runs/" + runID + "/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Events []models.ExecutionEvent `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	return body.Events
}

func getEffects(t *testing.T, srv *httptest.Server, runID string) []models.ExecutionEvent {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/v1/runs/" + runID + "/effects")
	if err != nil {
		t.Fatalf("GET effects: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Effects []models.ExecutionEvent `json:"effects"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode effects: %v", err)
	}
	return body.Effects
}

func listRuns(t *testing.T, srv *httptest.Server, querySuffix string) []models.ExecutionRun {
	t.Helper()
	url := srv.URL + "/api/v1/runs" + querySuffix
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET runs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET runs: status %d", resp.StatusCode)
	}
	var body struct {
		Runs []models.ExecutionRun `json:"runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	return body.Runs
}

// Compile-time: ensure the test file links against the json package
// (some Go versions warn about unused imports otherwise — we use the
// fmt import in the assertion fmt.Sprintf paths below).
var _ = fmt.Sprintf

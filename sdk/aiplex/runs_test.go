package aiplex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func newTestClient(srv *httptest.Server) *aiplex.Client {
	c := aiplex.NewClient(srv.URL)
	c.HTTPClient = srv.Client()
	return c
}

func TestListRuns_FiltersInQuery(t *testing.T) {
	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"runs":[{"run_id":"r-1","tenant_id":"acme","status":"running"}]}`))
	}))
	defer srv.Close()

	runs, err := newTestClient(srv).ListRuns(context.Background(), aiplex.ListRunsOpts{
		TenantID: "acme", AgentID: "treasury", HasUnknownEffects: true, Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != "r-1" {
		t.Errorf("expected r-1, got %+v", runs)
	}
	for _, want := range []string{"tenant_id=acme", "agent_id=treasury", "has_unknown_effects=true", "limit=50"} {
		if !strings.Contains(seenQuery, want) {
			t.Errorf("expected query to contain %q, got %q", want, seenQuery)
		}
	}
}

func TestRedriveRun_POSTsToCorrectPath(t *testing.T) {
	var seenPath, seenMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenMethod = r.Method
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	if err := newTestClient(srv).RedriveRun(context.Background(), "run-7"); err != nil {
		t.Fatal(err)
	}
	if seenMethod != "POST" || !strings.HasSuffix(seenPath, "/runs/run-7/redrive") {
		t.Errorf("expected POST /.../runs/run-7/redrive, got %s %s", seenMethod, seenPath)
	}
}

func TestCancelRun_SendsReason(t *testing.T) {
	var seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		seenBody = string(b)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	if err := newTestClient(srv).CancelRun(context.Background(), "r-x", "out of demo"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seenBody, `"reason":"out of demo"`) {
		t.Errorf("expected reason in body, got %s", seenBody)
	}
}

func TestSignalRun_BodyShape(t *testing.T) {
	var seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		seenBody = string(b)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	err := newTestClient(srv).SignalRun(context.Background(), "r-z", "approval", `{"ok":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seenBody, `"gate_name":"approval"`) ||
		!strings.Contains(seenBody, `"resolution_json":"{\"ok\":true}"`) {
		t.Errorf("body mismatch: %s", seenBody)
	}
}

func TestListRunOperatorAudit_Decodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"audit":[{"id":"a-1","run_id":"r-1","action":"redrive","actor":"alice","status":"accepted"}]}`))
	}))
	defer srv.Close()

	rows, err := newTestClient(srv).ListRunOperatorAudit(context.Background(), "r-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Action != "redrive" {
		t.Errorf("expected 1 redrive row, got %+v", rows)
	}
}

func TestListRunEvents_PagingQuery(t *testing.T) {
	var seenPath, seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath, seenQuery = r.URL.Path, r.URL.RawQuery
		_, _ = w.Write([]byte(`{"events":[{"run_id":"r 1","seq":7,"kind":"effect.unknown"}]}`))
	}))
	defer srv.Close()
	c := newTestClient(srv)

	if _, err := c.ListRunEvents(context.Background(), "r 1"); err != nil || seenQuery != "" {
		t.Fatalf("no opts must send no query: %v %q", err, seenQuery)
	}
	ev, err := c.ListRunEvents(context.Background(), "r 1", aiplex.ListRunEventsOpts{FromSeq: 7, Limit: 2})
	if err != nil || len(ev) != 1 || ev[0].Seq != 7 {
		t.Fatalf("%v %+v", err, ev)
	}
	if seenPath != "/api/v1/runs/r 1/events" || seenQuery != "from_seq=7&limit=2" {
		t.Errorf("path %q query %q", seenPath, seenQuery)
	}
	if _, err := c.ListRunEvents(context.Background(), "r", aiplex.ListRunEventsOpts{}, aiplex.ListRunEventsOpts{}); err == nil {
		t.Error("two opts must be refused")
	}
}

func TestListRunEffectsObligationsBudgets_PathsAndDecode(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/effects"):
			_, _ = w.Write([]byte(`{"effects":[{"run_id":"r-1","seq":1,"kind":"effect.unknown","tool":"pay","scope":"s","timestamp":"2026-01-02T03:04:05Z","payload_json":"{}"}]}`))
		case strings.HasSuffix(r.URL.Path, "/obligations"):
			_, _ = w.Write([]byte(`{"obligations":[{"run_id":"r-1","seq":2,"kind":"obligation.created"}]}`))
		default:
			_, _ = w.Write([]byte(`{"budgets":[{"run_id":"r-1","seq":3,"kind":"budget.charged","payload_json":"{\"usd_charged\":1.5}"}]}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(srv)
	ctx := context.Background()

	eff, err := c.ListRunEffects(ctx, "r-1")
	if err != nil || len(eff) != 1 || eff[0].Kind != "effect.unknown" || eff[0].Tool != "pay" || eff[0].Timestamp.Year() != 2026 {
		t.Fatalf("effects: %v %+v", err, eff)
	}
	ob, err := c.ListRunObligations(ctx, "r-1")
	if err != nil || len(ob) != 1 || ob[0].Kind != "obligation.created" {
		t.Fatalf("obligations: %v %+v", err, ob)
	}
	bu, err := c.ListRunBudgets(ctx, "r-1")
	if err != nil || len(bu) != 1 || !strings.Contains(bu[0].PayloadJSON, "usd_charged") {
		t.Fatalf("budgets: %v %+v", err, bu)
	}
	want := []string{"/api/v1/runs/r-1/effects", "/api/v1/runs/r-1/obligations", "/api/v1/runs/r-1/budgets"}
	for i, p := range want {
		if paths[i] != p {
			t.Errorf("path %d = %q want %q", i, paths[i], p)
		}
	}
}

func TestListRunEffects_ErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"run not found"}`))
	}))
	defer srv.Close()
	if _, err := newTestClient(srv).ListRunEffects(context.Background(), "nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetRun_DecodesCompactionFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/r-2") {
			_, _ = w.Write([]byte(`{"run_id":"r-2","status":"running"}`))
			return
		}
		_, _ = w.Write([]byte(`{"run_id":"r-1","status":"terminal","compacted":true,"compacted_at":"2026-05-01T10:00:00Z","retained_until":"2026-08-01T00:00:00Z"}`))
	}))
	defer srv.Close()
	c := newTestClient(srv)
	run, err := c.GetRun(context.Background(), "r-1")
	if err != nil || !run.Compacted || run.CompactedAt == nil || run.CompactedAt.Month() != 5 || run.RetainedUntil == nil || run.RetainedUntil.Month() != 8 {
		t.Fatalf("%v %+v", err, run)
	}
	run, err = c.GetRun(context.Background(), "r-2")
	if err != nil || run.Compacted || run.CompactedAt != nil || run.RetainedUntil != nil {
		t.Fatalf("absent fields must stay nil: %v %+v", err, run)
	}
}

func TestGetRunsHealth_Decodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/runs/_health" {
			t.Errorf("path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"last_ingest_at":"2026-05-01T10:00:00Z","has_runs":true,"tape_instances_count":2,"now":"2026-05-01T10:05:00Z"}`))
	}))
	defer srv.Close()
	h, err := newTestClient(srv).GetRunsHealth(context.Background())
	if err != nil || !h.HasRuns || h.TapeInstancesCount != 2 || h.LastIngestAt.Minute() != 0 || h.Now.Minute() != 5 {
		t.Fatalf("%v %+v", err, h)
	}
}

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

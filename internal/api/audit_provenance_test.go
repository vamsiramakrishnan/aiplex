package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func auditRouter(store *registry.MemoryStore) chi.Router {
	r := chi.NewRouter()
	a := NewA2AHandler(store)
	s := NewSkillsHandler(store)
	r.Post("/a2a/delegations", a.RecordDelegation)
	r.Patch("/a2a/delegations/{id}", a.UpdateDelegation)
	r.Get("/a2a/delegations/{id}/chain", a.GetDelegationChain)
	r.Post("/skills/invocations", s.RecordInvocation)
	return r
}
func auditRequest(router chi.Router, method, path, body string) *httptest.ResponseRecorder {
	r := trustedConnectedRequest(path, body, nil)
	r.Method = method
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func TestHistoricalAuditKeepsReportedSubjectsAndVerifiesCollector(t *testing.T) {
	store := registry.NewMemoryStore()
	r := auditRouter(store)
	start := time.Now().UTC().Add(-2 * time.Hour)
	end := start.Add(time.Minute)
	body := map[string]any{"id": "historical", "caller_agent_id": "retired-caller", "callee_agent_id": "retired-worker", "caller_instance_id": "removed-instance", "task_type": "research", "user_id": "historical-user", "status": "completed", "started_at": start, "completed_at": end, "reported_by": "spoofed-collector", "reported_at": "2000-01-01T00:00:00Z"}
	raw, _ := json.Marshal(body)
	w := auditRequest(r, http.MethodPost, "/a2a/delegations", string(raw))
	if w.Code != 201 {
		t.Fatalf("historical event rejected %d %s", w.Code, w.Body.String())
	}
	var d models.Delegation
	_ = json.Unmarshal(w.Body.Bytes(), &d)
	if d.ReportedBy != "principal-a" || d.ReportedAt.Before(time.Now().Add(-time.Minute)) || d.UserID != "historical-user" || d.CallerAgentID != "retired-caller" || d.DurationMs != 60000 {
		t.Fatalf("collector and reported subject conflated: %+v", d)
	}
	for _, patch := range []string{`{}`, `{"error":"partial without status"}`, `{"status":"nonsense"}`, `{"status":"running"}`, `{"status":"failed"}`, `{"status":"completed"}`} {
		w = auditRequest(r, http.MethodPatch, "/a2a/delegations/historical", patch)
		if w.Code != 400 && w.Code != 409 {
			t.Fatalf("invalid/final transition accepted: %s %d", patch, w.Code)
		}
	}
	stored, _ := store.GetDelegation(context.Background(), "historical")
	if !stored.CompletedAt.Equal(end) || stored.Status != "completed" {
		t.Fatal("repeated completion changed final audit state")
	}
	w = auditRequest(r, http.MethodPost, "/a2a/delegations", string(raw))
	if w.Code != 409 {
		t.Fatal("duplicate historical event overwrote original")
	}
	w = auditRequest(r, http.MethodPost, "/skills/invocations", `{"id":"history-skill","agent_id":"retired-agent","instance_id":"removed-server","skill_name":"old_skill","user_id":"historical-user","reported_by":"spoofed"}`)
	if w.Code != 201 {
		t.Fatalf("historical skill rejected %d %s", w.Code, w.Body.String())
	}
	var inv models.SkillInvocation
	_ = json.Unmarshal(w.Body.Bytes(), &inv)
	if inv.ReportedBy != "principal-a" || inv.UserID != "historical-user" || inv.Status != "success" || inv.ReportedAt.IsZero() {
		t.Fatalf("skill audit provenance invalid %+v", inv)
	}
	w = auditRequest(r, http.MethodPost, "/skills/invocations", `{"id":"history-skill","agent_id":"another","skill_name":"old_skill"}`)
	if w.Code != 409 {
		t.Fatal("skill history ID could be overwritten")
	}
}

func TestNestedDelegationContinuityAndStrictTraceparent(t *testing.T) {
	store := registry.NewMemoryStore()
	r := auditRouter(store)
	w := auditRequest(r, "POST", "/a2a/delegations", `{"id":"parent","caller_agent_id":"caller","callee_agent_id":"worker","callee_instance_id":"worker-instance","user_id":"subject","task_type":"research"}`)
	if w.Code != 201 {
		t.Fatalf("parent %d %s", w.Code, w.Body.String())
	}
	parent, _ := store.GetDelegation(context.Background(), "parent")
	w = auditRequest(r, "POST", "/a2a/delegations", `{"id":"child","parent_id":"parent","caller_agent_id":"worker","callee_agent_id":"helper","task_type":"summarize"}`)
	if w.Code != 201 {
		t.Fatalf("child %d %s", w.Code, w.Body.String())
	}
	child, _ := store.GetDelegation(context.Background(), "child")
	if child.TraceID != parent.TraceID || child.ParentSpanID != parent.SpanID || child.UserID != "subject" || child.CallerInstanceID != "worker-instance" {
		t.Fatalf("nested context lost: %+v", child)
	}
	for _, body := range []string{`{"id":"bad","parent_id":"missing","caller_agent_id":"worker","callee_agent_id":"helper","task_type":"x"}`, `{"id":"bad","parent_id":"parent","caller_agent_id":"other-worker","callee_agent_id":"helper","task_type":"x"}`, `{"id":"bad","parent_id":"parent","caller_agent_id":"worker","callee_agent_id":"helper","task_type":"x","user_id":"other-user"}`, `{"id":"self","parent_id":"self","caller_agent_id":"worker","callee_agent_id":"helper","task_type":"x"}`} {
		w = auditRequest(r, "POST", "/a2a/delegations", body)
		if w.Code != 400 {
			t.Fatalf("broken parent chain accepted %d %s", w.Code, body)
		}
	}
	for _, header := range []string{"00-" + strings.Repeat("0", 32) + "-" + strings.Repeat("1", 16) + "-01", "00-" + strings.Repeat("g", 32) + "-" + strings.Repeat("1", 16) + "-01", "ff-" + strings.Repeat("1", 32) + "-" + strings.Repeat("2", 16) + "-01"} {
		request := trustedConnectedRequest("/skills/invocations", `{"agent_id":"agent","skill_name":"review"}`, nil)
		request.Header.Set("traceparent", header)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, request)
		if w.Code != 400 {
			t.Fatalf("invalid W3C traceparent accepted %q", header)
		}
	}
}

func TestDelegationChainRejectsStoredCycle(t *testing.T) {
	s := registry.NewMemoryStore()
	r := auditRouter(s)
	for _, d := range []models.Delegation{{ID: "root", ParentID: "child"}, {ID: "child", ParentID: "root"}} {
		_ = s.AppendDelegation(context.Background(), &d)
	}
	w := auditRequest(r, "GET", "/a2a/delegations/root/chain", "")
	if w.Code != 422 {
		t.Fatalf("cyclic audit graph returned success %d %s", w.Code, w.Body.String())
	}
}

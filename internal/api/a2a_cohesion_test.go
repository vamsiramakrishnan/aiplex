package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

func TestDelegationPatchIsVisibleInMountedList(t *testing.T) {
	r, _ := setupA2ARouter()
	for _, request := range []struct{ method, path, body string }{{"POST", "/api/v1/a2a/delegations", `{"id":"coherent","caller_agent_id":"reporter","callee_agent_id":"worker","task_type":"research"}`}, {"PATCH", "/api/v1/a2a/delegations/coherent", `{"status":"completed"}`}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(request.method, request.path, strings.NewReader(request.body)))
		if w.Code >= 300 {
			t.Fatalf("%s: %d %s", request.method, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/a2a/delegations", nil))
	var list []models.Delegation
	if e := json.Unmarshal(w.Body.Bytes(), &list); e != nil {
		t.Fatal(e)
	}
	if len(list) != 1 || list[0].Status != "completed" || list[0].CompletedAt == nil {
		t.Fatalf("mounted list exposes stale delegation after completed PATCH: %+v", list)
	}
}

func TestAgentCardAuthenticationComesFromFrozenHostConfiguration(t *testing.T) {
	_, s := setupA2ARouter()
	schemes := []models.AuthSchemeInfo{{Scheme: "oauth2", Config: map[string]any{"issuer": "https://configured.example"}}}
	option := api.WithAgentCardAuthSchemes(schemes)
	schemes[0].Config["issuer"] = "https://mutated.example"
	handler := api.NewA2AHandler(s, option)
	r := chi.NewRouter()
	r.Get("/a2a/{instanceId}/.well-known/agent.json", handler.GetAgentCard)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/a2a/research-abc123/.well-known/agent.json", nil))
	var card models.AgentCard
	_ = json.Unmarshal(w.Body.Bytes(), &card)
	if w.Code != 200 || len(card.AuthSchemes) != 1 || card.AuthSchemes[0].Config["issuer"] != "https://configured.example" || strings.Contains(w.Body.String(), "aiplex.example.com") {
		t.Fatalf("fabricated or mutable gateway authentication: %d %s", w.Code, w.Body.String())
	}
}

func TestSkillManifestAndDiscoveryAgreeWithObservedInstanceSkills(t *testing.T) {
	r, s := setupSkillsRouter()
	ctx := context.Background()
	_ = s.PutTemplate(ctx, &models.Template{ID: "skills", Plane: models.PlaneSkillsPlex, Name: "Skills", Skills: []models.SkillInfo{{Name: "known", Description: "retained metadata"}, {Name: "removed"}}})
	inst := &models.Instance{ID: "skills-instance", Plane: models.PlaneSkillsPlex, TemplateID: "skills", Status: models.StatusRunning, Scopes: []string{"skill:invoke:known", "skill:invoke:discovered", "skill:bundle:bundle", "skill:invoke:known"}}
	_ = s.PutInstance(ctx, inst)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/skills/skills-instance/.well-known/skills.json", nil))
	var manifest struct {
		Skills []models.SkillInfo `json:"skills"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &manifest)
	if w.Code != 200 || len(manifest.Skills) != 2 || manifest.Skills[0].Description != "retained metadata" || manifest.Skills[1].Name != "discovered" {
		t.Fatalf("manifest ignores deployed skill discovery: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/skills/servers", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "bundle") || strings.Contains(w.Body.String(), "removed") || !strings.Contains(w.Body.String(), "skill:invoke:discovered") {
		t.Fatalf("skill discovery and manifest disagree: %s", w.Body.String())
	}
	inst.Status = models.StatusStopped
	_ = s.PutInstance(ctx, inst)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/skills/skills-instance/.well-known/skills.json", nil))
	if w.Code != 409 {
		t.Fatalf("stopped skill server advertises running discovery: %d", w.Code)
	}
}

func TestDelegationChainContainsAllDescendantsAndActualDepth(t *testing.T) {
	r, s := setupA2ARouter()
	ctx := context.Background()
	now := time.Now().Add(-time.Hour)
	for i, d := range []models.Delegation{{ID: "root", DurationMs: 10}, {ID: "child-a", ParentID: "root", DurationMs: 20}, {ID: "child-b", ParentID: "root", DurationMs: 30}, {ID: "child-c", ParentID: "root", DurationMs: 40}, {ID: "grandchild", ParentID: "child-a", DurationMs: 50}, {ID: "unrelated", DurationMs: 900}} {
		d.StartedAt = now.Add(time.Duration(i) * time.Second)
		if e := s.AppendDelegation(ctx, &d); e != nil {
			t.Fatal(e)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/a2a/delegations/root/chain", nil))
	if w.Code != 200 {
		t.Fatalf("chain %d %s", w.Code, w.Body.String())
	}
	var chain models.DelegationChain
	if e := json.Unmarshal(w.Body.Bytes(), &chain); e != nil {
		t.Fatal(e)
	}
	if len(chain.Children) != 4 || chain.Depth != 3 || chain.TotalDurationMs != 150 {
		t.Fatalf("chain is incomplete or counts sibling count as depth: %+v", chain)
	}
}

func TestDiscoveryUsesDeployedCapabilities(t *testing.T) {
	r, s := setupA2ARouter()
	ctx := context.Background()
	inst, e := s.GetInstance(ctx, "research-abc123")
	if e != nil {
		t.Fatal(e)
	}
	inst.Scopes = []string{"a2a:task:discovered", "a2a:agent:research-abc123", "a2a:task:discovered"}
	if e = s.PutInstance(ctx, inst); e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/a2a/research-abc123/.well-known/agent.json", nil))
	var card models.AgentCard
	if e = json.Unmarshal(w.Body.Bytes(), &card); e != nil {
		t.Fatal(e)
	}
	if w.Code != 200 || len(card.TaskTypes) != 1 || card.TaskTypes[0].Type != "discovered" {
		t.Fatalf("card advertises template capabilities that discovery replaced: %d %+v", w.Code, card.TaskTypes)
	}
}

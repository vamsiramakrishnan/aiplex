package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func setupSkillsRouter() (chi.Router, *registry.MemoryStore) {
	store := registry.NewMemoryStore()
	h := api.NewSkillsHandler(store)
	r := chi.NewRouter()
	r.Use(api.RequestID)
	r.Route("/api/v1/skills", func(r chi.Router) {
		r.Get("/servers", h.ListSkillServers)
		r.Post("/invocations", h.RecordInvocation)
		r.Get("/invocations", h.ListInvocations)
	})
	r.Get("/skills/{instanceId}/.well-known/skills.json", h.GetSkillsManifest)
	return r, store
}

func TestSkillsHandler_RecordInvocation_RecordsDenialOnCapMissing(t *testing.T) {
	r, store := setupSkillsRouter()

	body := `{
		"agent_id": "tutor-agent",
		"instance_id": "skill-server-001",
		"skill_name": "review_pr",
		"user_id": "alice@example.com",
		"status": "failed",
		"error": "missing cap: cap://skill/review_pr@v1"
	}`
	req := httptest.NewRequest("POST", "/api/v1/skills/invocations", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("status = %d, want 201", w.Code)
	}

	denials, err := store.ListPolicyDenials(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListPolicyDenials: %v", err)
	}
	if len(denials) != 1 {
		t.Fatalf("expected 1 denial recorded, got %d", len(denials))
	}
	d := denials[0]
	if d.Kind != capability.KindSkill {
		t.Errorf("Kind = %q, want skill", d.Kind)
	}
	if d.CapURI != "cap://skill/review_pr@v1" {
		t.Errorf("CapURI = %q", d.CapURI)
	}
	if d.AgentID != "tutor-agent" {
		t.Errorf("AgentID = %q, want tutor-agent", d.AgentID)
	}
	if d.Reason != "cap_missing" {
		t.Errorf("Reason = %q, want cap_missing", d.Reason)
	}
	if d.Action != "invoke" {
		t.Errorf("Action = %q, want invoke", d.Action)
	}
}

func TestSkillsHandler_RecordInvocation_NoDenialOnSuccess(t *testing.T) {
	r, store := setupSkillsRouter()

	body := `{
		"agent_id": "tutor-agent",
		"instance_id": "skill-server-001",
		"skill_name": "review_pr"
	}`
	req := httptest.NewRequest("POST", "/api/v1/skills/invocations", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("status = %d, want 201", w.Code)
	}

	denials, _ := store.ListPolicyDenials(context.Background(), 10)
	if len(denials) != 0 {
		t.Errorf("expected 0 denials on successful invocation, got %d", len(denials))
	}
}

func TestSkillsHandler_RecordInvocation_NoDenialOnGenericFailure(t *testing.T) {
	r, store := setupSkillsRouter()

	body := `{
		"agent_id": "tutor-agent",
		"instance_id": "skill-server-001",
		"skill_name": "review_pr",
		"status": "failed",
		"error": "skill server timed out"
	}`
	req := httptest.NewRequest("POST", "/api/v1/skills/invocations", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("status = %d, want 201", w.Code)
	}

	denials, _ := store.ListPolicyDenials(context.Background(), 10)
	if len(denials) != 0 {
		t.Errorf("expected 0 denials for non-cap failures, got %d", len(denials))
	}
}

func TestSkillsHandler_GetSkillsManifest(t *testing.T) {
	r, store := setupSkillsRouter()
	ctx := context.Background()

	store.PutTemplate(ctx, &models.Template{
		ID:          "code-review",
		Kind:        capability.KindSkill,
		Name:        "Code Review",
		Description: "Pull request review",
		Version:     "v1",
		SkillBundle: "code-review",
		Capabilities: []capability.Capability{
			{URI: "cap://skill/code-review/review_pr@v1", Kind: capability.KindSkill, Name: "code-review/review_pr", Version: "v1", Description: "Review a PR diff"},
			{URI: "cap://skill/code-review/suggest_tests@v1", Kind: capability.KindSkill, Name: "code-review/suggest_tests", Version: "v1", Description: "Suggest tests"},
		},
	})
	store.PutInstance(ctx, &models.Instance{
		ID:         "code-review-abc",
		Kind:       capability.KindSkill,
		TemplateID: "code-review",
		Status:     models.StatusRunning,
		Capabilities: capability.CapSet{
			{URI: "cap://skill/code-review/review_pr@v1", Actions: []string{"invoke"}},
			{URI: "cap://skill/code-review/suggest_tests@v1", Actions: []string{"invoke"}},
		},
	})

	req := httptest.NewRequest("GET", "/skills/code-review-abc/.well-known/skills.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var manifest map[string]any
	if err := json.NewDecoder(w.Body).Decode(&manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest["instance_id"] != "code-review-abc" {
		t.Errorf("instance_id = %v", manifest["instance_id"])
	}
	if manifest["skill_bundle"] != "code-review" {
		t.Errorf("skill_bundle = %v", manifest["skill_bundle"])
	}
	skills := manifest["skills"].([]any)
	if len(skills) != 2 {
		t.Errorf("got %d skills, want 2", len(skills))
	}
}

package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
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

func TestSkillsHandler_RecordInvocation_RecordsDenialOnScopeMissing(t *testing.T) {
	r, store := setupSkillsRouter()

	body := `{
		"agent_id": "tutor-agent",
		"instance_id": "skill-server-001",
		"skill_name": "review_pr",
		"user_id": "alice@example.com",
		"status": "failed",
		"error": "missing scope: skill:invoke:review_pr"
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
	if d.Plane != string(models.PlaneSkillsPlex) {
		t.Errorf("Plane = %q, want skillsplex", d.Plane)
	}
	if d.Scope != "skill:invoke:review_pr" {
		t.Errorf("Scope = %q, want skill:invoke:review_pr", d.Scope)
	}
	if d.AgentID != "tutor-agent" {
		t.Errorf("AgentID = %q, want tutor-agent", d.AgentID)
	}
	if d.Reason != "scope_missing" {
		t.Errorf("Reason = %q, want scope_missing", d.Reason)
	}
	if d.Action != "skills/invoke:review_pr" {
		t.Errorf("Action = %q, want skills/invoke:review_pr", d.Action)
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
		t.Errorf("expected 0 denials for non-scope failures, got %d", len(denials))
	}
}

func TestSkillsHandler_GetSkillsManifest(t *testing.T) {
	r, store := setupSkillsRouter()
	ctx := context.Background()

	store.PutTemplate(ctx, &models.Template{
		ID:          "code-review",
		Plane:       models.PlaneSkillsPlex,
		Name:        "Code Review",
		Description: "Pull request review",
		Version:     "1.0.0",
		SkillBundle: "code-review",
		Skills: []models.SkillInfo{
			{Name: "review_pr", Description: "Review a PR diff"},
			{Name: "suggest_tests", Description: "Suggest tests"},
		},
	})
	store.PutInstance(ctx, &models.Instance{
		ID:         "code-review-abc",
		Plane:      models.PlaneSkillsPlex,
		TemplateID: "code-review",
		Status:     models.StatusRunning,
		Scopes:     []string{"skill:invoke:review_pr", "skill:invoke:suggest_tests"},
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

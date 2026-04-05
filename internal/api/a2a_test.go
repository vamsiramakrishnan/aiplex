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

func setupA2ARouter() (chi.Router, *registry.MemoryStore) {
	store := registry.NewMemoryStore()
	ctx := context.Background()

	// Seed A2A template and instance
	store.PutTemplate(ctx, &models.Template{
		ID:        "research-agent",
		Plane:     models.PlaneA2APlex,
		Name:      "Research Agent",
		Version:   "1.0.0",
		TaskTypes: []string{"research", "summarize"},
	})
	store.PutInstance(ctx, &models.Instance{
		ID:         "research-abc123",
		Plane:      models.PlaneA2APlex,
		TemplateID: "research-agent",
		Status:     models.StatusRunning,
		Scopes:     []string{"a2a:task:research", "a2a:task:summarize"},
	})

	h := api.NewA2AHandler(store)
	r := chi.NewRouter()
	r.Get("/a2a/{instanceId}/.well-known/agent.json", h.GetAgentCard)
	r.Get("/api/v1/a2a/agents", h.ListAgentCards)
	r.Post("/api/v1/a2a/delegations", h.RecordDelegation)
	r.Get("/api/v1/a2a/delegations", h.ListDelegations)
	r.Get("/api/v1/a2a/delegations/{id}", h.GetDelegation)
	r.Patch("/api/v1/a2a/delegations/{id}", h.UpdateDelegation)
	r.Get("/api/v1/a2a/delegations/{id}/chain", h.GetDelegationChain)
	return r, store
}

func TestA2A_AgentCard(t *testing.T) {
	r, _ := setupA2ARouter()

	req := httptest.NewRequest("GET", "/a2a/research-abc123/.well-known/agent.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("agent card: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var card models.AgentCard
	json.NewDecoder(w.Body).Decode(&card)
	if card.Name != "Research Agent" {
		t.Errorf("expected Research Agent, got %s", card.Name)
	}
	if len(card.TaskTypes) != 2 {
		t.Errorf("expected 2 task types, got %d", len(card.TaskTypes))
	}
	if len(card.AuthSchemes) != 2 {
		t.Errorf("expected 2 auth schemes (bearer, spiffe), got %d", len(card.AuthSchemes))
	}
}

func TestA2A_AgentCard_NotFound(t *testing.T) {
	r, _ := setupA2ARouter()

	req := httptest.NewRequest("GET", "/a2a/nonexistent/.well-known/agent.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestA2A_ListAgentCards(t *testing.T) {
	r, _ := setupA2ARouter()

	req := httptest.NewRequest("GET", "/api/v1/a2a/agents", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("list agents: expected 200, got %d", w.Code)
	}

	var cards []map[string]any
	json.NewDecoder(w.Body).Decode(&cards)
	if len(cards) != 1 {
		t.Errorf("expected 1 agent card, got %d", len(cards))
	}
}

func TestA2A_DelegationLifecycle(t *testing.T) {
	r, _ := setupA2ARouter()

	// Record delegation
	body := `{
		"id": "del-001",
		"caller_agent_id": "tutor-agent",
		"callee_agent_id": "research-agent",
		"caller_instance_id": "tutor-xyz",
		"callee_instance_id": "research-abc123",
		"task_type": "research",
		"user_id": "student@school.edu"
	}`
	req := httptest.NewRequest("POST", "/api/v1/a2a/delegations", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Fatalf("record delegation: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var d models.Delegation
	json.NewDecoder(w.Body).Decode(&d)
	if d.Status != "pending" {
		t.Errorf("expected pending, got %s", d.Status)
	}

	// Get delegation
	req = httptest.NewRequest("GET", "/api/v1/a2a/delegations/del-001", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("get delegation: expected 200, got %d", w.Code)
	}

	// Update to completed
	body = `{"status": "completed"}`
	req = httptest.NewRequest("PATCH", "/api/v1/a2a/delegations/del-001", strings.NewReader(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("update delegation: expected 200, got %d", w.Code)
	}

	json.NewDecoder(w.Body).Decode(&d)
	if d.Status != "completed" {
		t.Errorf("expected completed, got %s", d.Status)
	}
	if d.CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}

	// List delegations
	req = httptest.NewRequest("GET", "/api/v1/a2a/delegations", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var delegations []models.Delegation
	json.NewDecoder(w.Body).Decode(&delegations)
	if len(delegations) != 1 {
		t.Errorf("expected 1 delegation, got %d", len(delegations))
	}

	// Get chain
	req = httptest.NewRequest("GET", "/api/v1/a2a/delegations/del-001/chain", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var chain models.DelegationChain
	json.NewDecoder(w.Body).Decode(&chain)
	if chain.RootDelegation.ID != "del-001" {
		t.Errorf("expected root del-001, got %s", chain.RootDelegation.ID)
	}
}

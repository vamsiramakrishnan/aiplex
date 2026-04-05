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
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func setupDashboardRouter() (chi.Router, *registry.MemoryStore) {
	store := registry.NewMemoryStore()
	ctx := context.Background()

	// Seed data
	store.PutInstance(ctx, &models.Instance{
		ID: "inst-1", Plane: models.PlaneMCPlex, Status: models.StatusRunning,
	})
	store.PutInstance(ctx, &models.Instance{
		ID: "inst-2", Plane: models.PlaneA2APlex, Status: models.StatusRunning,
	})
	store.PutInstance(ctx, &models.Instance{
		ID: "inst-3", Plane: models.PlaneLLMPlex, Status: models.StatusStopped,
	})
	store.PutAgent(ctx, &models.Agent{
		ClientID: "agent-1", DisplayName: "Agent 1", Status: "active",
	})
	store.AppendUsage(ctx, &models.UsageRecord{
		ModelID: "gemini", InputTokens: 1000, OutputTokens: 500,
		TotalTokens: 1500, CostUSD: 0.50, Timestamp: time.Now(),
	})
	store.AppendPolicyDenial(ctx, &models.PolicyDenial{
		ID: "d1", Plane: "mcplex", AgentID: "agent-1", Action: "tools/call:secret_tool",
		Scope: "mcp:tools:secret_tool", Reason: "scope_missing", Timestamp: time.Now(),
	})

	h := api.NewDashboardHandler(store)
	r := chi.NewRouter()
	r.Get("/api/v1/dashboard/stats", h.GetStats)
	r.Get("/api/v1/dashboard/denials", h.ListPolicyDenials)
	r.Post("/api/v1/dashboard/denials", h.RecordPolicyDenial)
	return r, store
}

func TestDashboard_Stats(t *testing.T) {
	r, _ := setupDashboardRouter()

	req := httptest.NewRequest("GET", "/api/v1/dashboard/stats", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("stats: expected 200, got %d", w.Code)
	}

	var stats models.DashboardStats
	json.NewDecoder(w.Body).Decode(&stats)

	if stats.TotalInstances != 3 {
		t.Errorf("expected 3 total, got %d", stats.TotalInstances)
	}
	if stats.RunningInstances != 2 {
		t.Errorf("expected 2 running, got %d", stats.RunningInstances)
	}
	if stats.RegisteredAgents != 1 {
		t.Errorf("expected 1 agent, got %d", stats.RegisteredAgents)
	}
	if stats.ActivePlanes != 3 {
		t.Errorf("expected 3 planes, got %d", stats.ActivePlanes)
	}
	if stats.MCPlexInstances != 1 {
		t.Errorf("expected 1 MCPlex, got %d", stats.MCPlexInstances)
	}
	if stats.DailyCostUSD != 0.50 {
		t.Errorf("expected $0.50 daily cost, got %f", stats.DailyCostUSD)
	}
	if stats.PolicyDenials != 1 {
		t.Errorf("expected 1 denial, got %d", stats.PolicyDenials)
	}
}

func TestDashboard_PolicyDenials(t *testing.T) {
	r, _ := setupDashboardRouter()

	// List existing
	req := httptest.NewRequest("GET", "/api/v1/dashboard/denials", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var denials []models.PolicyDenial
	json.NewDecoder(w.Body).Decode(&denials)
	if len(denials) != 1 {
		t.Errorf("expected 1 denial, got %d", len(denials))
	}

	// Record new denial
	body := `{
		"id": "d2",
		"plane": "llmplex",
		"agent_id": "agent-1",
		"action": "llm:model:gpt-4.1",
		"scope": "llm:model:gpt-4.1",
		"reason": "scope_missing"
	}`
	req = httptest.NewRequest("POST", "/api/v1/dashboard/denials", strings.NewReader(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("record denial: expected 201, got %d", w.Code)
	}

	// Verify count increased
	req = httptest.NewRequest("GET", "/api/v1/dashboard/denials", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	json.NewDecoder(w.Body).Decode(&denials)
	if len(denials) != 2 {
		t.Errorf("expected 2 denials, got %d", len(denials))
	}
}

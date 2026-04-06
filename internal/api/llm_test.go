package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func setupLLMRouter() (chi.Router, *registry.MemoryStore) {
	store := registry.NewMemoryStore()
	store.PutTemplate(context.Background(), &models.Template{
		ID:      "gemini-2.5-flash",
		Plane:   models.PlaneLLMPlex,
		ModelID: "gemini-2.5-flash",
		Pricing: &models.Pricing{Input: 0.15, Output: 0.60},
	})

	k8s := deploy.NewNoOpK8sClient()
	h := api.NewLLMHandler(store, k8s, "aiplex-gateway", nil)
	r := chi.NewRouter()
	r.Get("/api/v1/llm/routes", h.ListRouteConfigs)
	r.Get("/api/v1/llm/routes/{modelId}", h.GetRouteConfig)
	r.Put("/api/v1/llm/routes/{modelId}", h.PutRouteConfig)
	r.Delete("/api/v1/llm/routes/{modelId}", h.DeleteRouteConfig)
	r.Get("/api/v1/llm/providers", h.ListProviders)
	r.Put("/api/v1/llm/providers/{provider}", h.PutProvider)
	r.Post("/api/v1/llm/usage", h.RecordUsage)
	r.Get("/api/v1/llm/usage/summary", h.GetUsageSummary)
	return r, store
}

func TestLLM_RouteConfig_CRUD(t *testing.T) {
	r, _ := setupLLMRouter()

	// Create route config
	body := `{
		"backends": [
			{"provider": "google", "model_id": "gemini-2.5-flash", "weight": 80, "enabled": true},
			{"provider": "anthropic", "model_id": "claude-sonnet-4", "weight": 20, "enabled": true}
		],
		"fallbacks": ["gpt-4.1"],
		"cache_ttl_seconds": 300,
		"budget": {"max_daily_cost_usd": 100, "alert_threshold_pct": 80}
	}`
	req := httptest.NewRequest("PUT", "/api/v1/llm/routes/gemini-2.5-flash", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("put route: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var rc models.LLMRouteConfig
	json.NewDecoder(w.Body).Decode(&rc)
	if len(rc.Backends) != 2 {
		t.Errorf("expected 2 backends, got %d", len(rc.Backends))
	}
	if rc.Budget == nil || rc.Budget.MaxDailyCostUSD != 100 {
		t.Error("budget not set correctly")
	}

	// Get
	req = httptest.NewRequest("GET", "/api/v1/llm/routes/gemini-2.5-flash", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("get route: expected 200, got %d", w.Code)
	}

	// List
	req = httptest.NewRequest("GET", "/api/v1/llm/routes", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var routes []models.LLMRouteConfig
	json.NewDecoder(w.Body).Decode(&routes)
	if len(routes) != 1 {
		t.Errorf("expected 1 route, got %d", len(routes))
	}

	// Delete
	req = httptest.NewRequest("DELETE", "/api/v1/llm/routes/gemini-2.5-flash", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Errorf("delete route: expected 204, got %d", w.Code)
	}
}

func TestLLM_RouteConfig_BadWeights(t *testing.T) {
	r, _ := setupLLMRouter()

	body := `{
		"backends": [
			{"provider": "google", "model_id": "gemini", "weight": 50, "enabled": true},
			{"provider": "anthropic", "model_id": "claude", "weight": 30, "enabled": true}
		]
	}`
	req := httptest.NewRequest("PUT", "/api/v1/llm/routes/test-model", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for bad weights, got %d", w.Code)
	}
}

func TestLLM_Provider(t *testing.T) {
	r, _ := setupLLMRouter()

	body := `{"display_name": "Google AI", "enabled": true, "secret_ref": "gemini-api-key"}`
	req := httptest.NewRequest("PUT", "/api/v1/llm/providers/google", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("put provider: expected 200, got %d", w.Code)
	}

	req = httptest.NewRequest("GET", "/api/v1/llm/providers", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var providers []models.ProviderConfig
	json.NewDecoder(w.Body).Decode(&providers)
	if len(providers) != 1 || providers[0].Provider != "google" {
		t.Errorf("expected 1 google provider, got %+v", providers)
	}
}

func TestLLM_UsageTracking(t *testing.T) {
	r, _ := setupLLMRouter()

	// Record usage
	body := `{
		"id": "u1",
		"model_id": "gemini-2.5-flash",
		"provider": "google",
		"agent_id": "tutor-agent",
		"user_id": "student@school.edu",
		"input_tokens": 1000,
		"output_tokens": 500,
		"latency_ms": 250
	}`
	req := httptest.NewRequest("POST", "/api/v1/llm/usage", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Fatalf("record usage: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var record models.UsageRecord
	json.NewDecoder(w.Body).Decode(&record)
	if record.TotalTokens != 1500 {
		t.Errorf("expected 1500 total tokens, got %d", record.TotalTokens)
	}
	if record.CostUSD == 0 {
		t.Error("expected auto-calculated cost")
	}

	// Get summary
	req = httptest.NewRequest("GET", "/api/v1/llm/usage/summary?model_id=gemini-2.5-flash&period=day", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var summary models.UsageSummary
	json.NewDecoder(w.Body).Decode(&summary)
	if summary.RequestCount != 1 {
		t.Errorf("expected 1 request, got %d", summary.RequestCount)
	}
	if summary.TotalTokens != 1500 {
		t.Errorf("expected 1500 tokens, got %d", summary.TotalTokens)
	}
}

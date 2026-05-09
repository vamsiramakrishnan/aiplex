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
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func setupLLMRouter() (chi.Router, *registry.MemoryStore) {
	store := registry.NewMemoryStore()
	store.PutTemplate(context.Background(), &models.Template{
		ID:      "gemini-2.5-flash",
		Kind:    capability.KindModel,
		ModelID: "gemini-2.5-flash",
		Pricing: &capability.Pricing{Input: 0.15, Output: 0.60},
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

func TestLLM_MonthlyBudgetEnforcement(t *testing.T) {
	r, store := setupLLMRouter()

	// Set up a route with $1 monthly budget
	routeBody := `{
		"backends": [{"provider": "google", "model_id": "gemini-2.5-flash", "weight": 100, "enabled": true}],
		"budget": {"max_monthly_cost_usd": 1.0}
	}`
	req := httptest.NewRequest("PUT", "/api/v1/llm/routes/gemini-2.5-flash", strings.NewReader(routeBody))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("put route: expected 200, got %d", w.Code)
	}

	// Record usage that approaches the monthly budget
	usageBody := `{
		"model_id": "gemini-2.5-flash",
		"provider": "google",
		"agent_id": "test-agent",
		"user_id": "user@test.com",
		"input_tokens": 500000,
		"output_tokens": 500000,
		"cost_usd": 0.75
	}`
	req = httptest.NewRequest("POST", "/api/v1/llm/usage", strings.NewReader(usageBody))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("first usage: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Verify monthly summary
	monthlySummary, _ := store.GetUsageSummary(context.Background(), "gemini-2.5-flash", "", "month")
	if monthlySummary.TotalCostUSD != 0.75 {
		t.Errorf("expected $0.75 monthly cost, got $%.2f", monthlySummary.TotalCostUSD)
	}

	// Try to record usage that exceeds monthly budget
	usageBody2 := `{
		"model_id": "gemini-2.5-flash",
		"provider": "google",
		"agent_id": "test-agent",
		"user_id": "user@test.com",
		"input_tokens": 300000,
		"output_tokens": 300000,
		"cost_usd": 0.30
	}`
	req = httptest.NewRequest("POST", "/api/v1/llm/usage", strings.NewReader(usageBody2))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 429 {
		t.Errorf("expected 429 for monthly budget exceeded, got %d: %s", w.Code, w.Body.String())
	}

	warning := w.Header().Get("X-Budget-Warning")
	if warning != "monthly_cost_exceeded" {
		t.Errorf("expected X-Budget-Warning header, got %q", warning)
	}
}

func TestLLM_RateLimiting(t *testing.T) {
	r, _ := setupLLMRouter()

	// Set up a route with 2 requests/minute limit
	routeBody := `{
		"backends": [{"provider": "google", "model_id": "gemini-2.5-flash", "weight": 100, "enabled": true}],
		"rate_limit": {"requests_per_minute": 2, "tokens_per_minute": 10000}
	}`
	req := httptest.NewRequest("PUT", "/api/v1/llm/routes/gemini-2.5-flash", strings.NewReader(routeBody))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("put route: expected 200, got %d", w.Code)
	}

	// Make 2 requests (should succeed)
	for i := 0; i < 2; i++ {
		usageBody := `{
			"model_id": "gemini-2.5-flash",
			"provider": "google",
			"agent_id": "test-agent",
			"user_id": "user@test.com",
			"input_tokens": 100,
			"output_tokens": 100
		}`
		req = httptest.NewRequest("POST", "/api/v1/llm/usage", strings.NewReader(usageBody))
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 201 {
			t.Fatalf("request %d: expected 201, got %d: %s", i+1, w.Code, w.Body.String())
		}
	}

	// Make 3rd request (should get 429)
	usageBody := `{
		"model_id": "gemini-2.5-flash",
		"provider": "google",
		"agent_id": "test-agent",
		"user_id": "user@test.com",
		"input_tokens": 100,
		"output_tokens": 100
	}`
	req = httptest.NewRequest("POST", "/api/v1/llm/usage", strings.NewReader(usageBody))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 429 {
		t.Errorf("expected 429 for rate limit exceeded, got %d: %s", w.Code, w.Body.String())
	}

	retryAfter := w.Header().Get("Retry-After")
	if retryAfter != "60" {
		t.Errorf("expected Retry-After: 60, got %q", retryAfter)
	}
}

func TestLLM_TokenRateLimiting(t *testing.T) {
	r, _ := setupLLMRouter()

	// Set up a route with 500 tokens/minute limit
	routeBody := `{
		"backends": [{"provider": "google", "model_id": "gemini-2.5-flash", "weight": 100, "enabled": true}],
		"rate_limit": {"requests_per_minute": 100, "tokens_per_minute": 500}
	}`
	req := httptest.NewRequest("PUT", "/api/v1/llm/routes/gemini-2.5-flash", strings.NewReader(routeBody))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("put route: expected 200, got %d", w.Code)
	}

	// Use 300 tokens (should succeed)
	usageBody := `{
		"model_id": "gemini-2.5-flash",
		"provider": "google",
		"agent_id": "test-agent",
		"user_id": "user@test.com",
		"input_tokens": 200,
		"output_tokens": 100
	}`
	req = httptest.NewRequest("POST", "/api/v1/llm/usage", strings.NewReader(usageBody))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("first request: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Try to use 300 more tokens (should exceed 500 token limit)
	usageBody2 := `{
		"model_id": "gemini-2.5-flash",
		"provider": "google",
		"agent_id": "test-agent",
		"user_id": "user@test.com",
		"input_tokens": 200,
		"output_tokens": 100
	}`
	req = httptest.NewRequest("POST", "/api/v1/llm/usage", strings.NewReader(usageBody2))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 429 {
		t.Errorf("expected 429 for token rate limit exceeded, got %d: %s", w.Code, w.Body.String())
	}

	retryAfter := w.Header().Get("Retry-After")
	if retryAfter != "60" {
		t.Errorf("expected Retry-After: 60, got %q", retryAfter)
	}
}

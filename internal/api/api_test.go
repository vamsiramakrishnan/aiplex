package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/catalog"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func setupRouter() (chi.Router, *registry.MemoryStore) {
	store := registry.NewMemoryStore()

	// Seed a template
	store.PutTemplate(context.Background(), &models.Template{
		ID:    "kb-search",
		Plane: models.PlaneMCPlex,
		Name:  "Knowledge Base Search",
		Tools: []models.ToolInfo{{Name: "search", Description: "Search docs"}},
	})

	sources := []catalog.Source{
		catalog.NewLocalSource(store, models.PlaneMCPlex),
		catalog.NewLocalSource(store, models.PlaneA2APlex),
		catalog.NewLocalSource(store, models.PlaneLLMPlex),
		catalog.NewBuiltInProviders(),
	}
	agg := catalog.NewAggregator(sources)
	engine := deploy.NewEngine(store, "test.local")

	catalogH := api.NewCatalogHandler(agg, store)
	instanceH := api.NewInstanceHandler(store, engine)
	agentH := api.NewAgentHandler(store)

	r := chi.NewRouter()
	r.Get("/api/v1/catalog", catalogH.List)
	r.Get("/api/v1/catalog/{id}", catalogH.Get)
	r.Get("/api/v1/instances", instanceH.List)
	r.Post("/api/v1/instances", instanceH.Deploy)
	r.Get("/api/v1/instances/{id}", instanceH.Get)
	r.Delete("/api/v1/instances/{id}", instanceH.Undeploy)
	r.Get("/api/v1/agents", agentH.List)
	r.Post("/api/v1/agents", agentH.Register)
	r.Get("/api/v1/agents/{clientId}", agentH.Get)
	r.Delete("/api/v1/agents/{clientId}", agentH.Delete)
	r.Get("/healthz", api.Health)

	return r, store
}

func TestHealth(t *testing.T) {
	r, _ := setupRouter()
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestCatalogList(t *testing.T) {
	r, _ := setupRouter()
	req := httptest.NewRequest("GET", "/api/v1/catalog?plane=mcplex", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var page models.CatalogPage
	json.NewDecoder(w.Body).Decode(&page)
	if page.Total != 1 {
		t.Errorf("expected 1 MCPlex template, got %d", page.Total)
	}
}

func TestCatalogListLLM(t *testing.T) {
	r, _ := setupRouter()
	req := httptest.NewRequest("GET", "/api/v1/catalog?plane=llmplex", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var page models.CatalogPage
	json.NewDecoder(w.Body).Decode(&page)
	// Built-in providers + local source (no LLM templates in local)
	if page.Total < 6 {
		t.Errorf("expected at least 6 LLM providers, got %d", page.Total)
	}
}

func TestDeployAndGet(t *testing.T) {
	r, _ := setupRouter()

	// Deploy
	body := `{"plane":"mcplex","template_id":"kb-search","display_name":"My KB"}`
	req := httptest.NewRequest("POST", "/api/v1/instances", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Fatalf("deploy: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var inst models.Instance
	json.NewDecoder(w.Body).Decode(&inst)
	if inst.Plane != models.PlaneMCPlex {
		t.Errorf("expected plane mcplex, got %s", inst.Plane)
	}
	if inst.Status != models.StatusRunning {
		t.Errorf("expected status running, got %s", inst.Status)
	}
	if len(inst.Scopes) != 1 || inst.Scopes[0] != "mcp:tools:search" {
		t.Errorf("expected scope mcp:tools:search, got %v", inst.Scopes)
	}

	// Get the instance
	req = httptest.NewRequest("GET", "/api/v1/instances/"+inst.ID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("get instance: expected 200, got %d", w.Code)
	}

	// List instances
	req = httptest.NewRequest("GET", "/api/v1/instances?plane=mcplex", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var instances []models.Instance
	json.NewDecoder(w.Body).Decode(&instances)
	if len(instances) != 1 {
		t.Errorf("expected 1 instance, got %d", len(instances))
	}
}

func TestDeployBadRequest(t *testing.T) {
	r, _ := setupRouter()

	// Missing plane
	body := `{"template_id":"kb-search"}`
	req := httptest.NewRequest("POST", "/api/v1/instances", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUndeployNotFound(t *testing.T) {
	r, _ := setupRouter()

	req := httptest.NewRequest("DELETE", "/api/v1/instances/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestAgentRegisterAndGet(t *testing.T) {
	r, _ := setupRouter()

	// Register
	body := `{"client_id":"tutor-agent","display_name":"Tutor","auth_method":"client_credentials","allowed_scopes":["mcp:tools:search"]}`
	req := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Fatalf("register: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Get
	req = httptest.NewRequest("GET", "/api/v1/agents/tutor-agent", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("get agent: expected 200, got %d", w.Code)
	}

	var agent models.Agent
	json.NewDecoder(w.Body).Decode(&agent)
	if agent.Status != "active" {
		t.Errorf("expected active status, got %s", agent.Status)
	}

	// Duplicate registration
	req = httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Errorf("expected 409 for duplicate, got %d", w.Code)
	}

	// Delete
	req = httptest.NewRequest("DELETE", "/api/v1/agents/tutor-agent", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Errorf("delete: expected 204, got %d", w.Code)
	}
}

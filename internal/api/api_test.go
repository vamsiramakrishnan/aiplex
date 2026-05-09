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
	"github.com/vamsiramakrishnan/aiplex/internal/catalog"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func setupRouter() (chi.Router, *registry.MemoryStore) {
	store := registry.NewMemoryStore()

	store.PutTemplate(context.Background(), &models.Template{
		ID:   "kb-search",
		Kind: capability.KindTool,
		Name: "Knowledge Base Search",
		Capabilities: []capability.Capability{
			{URI: "cap://tool/search@v1", Kind: capability.KindTool, Name: "search", Version: "v1", Description: "Search docs"},
		},
	})

	sources := []catalog.Source{
		catalog.NewLocalSource(store, capability.KindTool),
		catalog.NewLocalSource(store, capability.KindTask),
		catalog.NewLocalSource(store, capability.KindModel),
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
	req := httptest.NewRequest("GET", "/api/v1/catalog?kind=tool", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var page models.CatalogPage
	json.NewDecoder(w.Body).Decode(&page)
	if page.Total != 1 {
		t.Errorf("expected 1 tool template, got %d", page.Total)
	}
}

func TestCatalogListModel(t *testing.T) {
	r, _ := setupRouter()
	req := httptest.NewRequest("GET", "/api/v1/catalog?kind=model", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var page models.CatalogPage
	json.NewDecoder(w.Body).Decode(&page)
	if page.Total < 6 {
		t.Errorf("expected at least 6 model templates, got %d", page.Total)
	}
}

func TestDeployAndGet(t *testing.T) {
	r, _ := setupRouter()

	body := `{"kind":"tool","template_id":"kb-search","display_name":"My KB"}`
	req := httptest.NewRequest("POST", "/api/v1/instances", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Fatalf("deploy: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var inst models.Instance
	json.NewDecoder(w.Body).Decode(&inst)
	if inst.Kind != capability.KindTool {
		t.Errorf("expected kind tool, got %s", inst.Kind)
	}
	if inst.Status != models.StatusRunning {
		t.Errorf("expected status running, got %s", inst.Status)
	}
	if len(inst.Capabilities) != 1 || inst.Capabilities[0].URI != "cap://tool/search@v1" {
		t.Errorf("expected cap cap://tool/search@v1, got %v", inst.Capabilities)
	}

	req = httptest.NewRequest("GET", "/api/v1/instances/"+inst.ID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("get instance: expected 200, got %d", w.Code)
	}

	req = httptest.NewRequest("GET", "/api/v1/instances?kind=tool", nil)
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

	body := `{"kind":"tool"}` // missing template_id
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

	body := `{"client_id":"tutor-agent","display_name":"Tutor","auth_method":"client_credentials","allowed_caps":[{"uri":"cap://tool/search@v1","actions":["call"]}]}`
	req := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Fatalf("register: expected 201, got %d: %s", w.Code, w.Body.String())
	}

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

	req = httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Errorf("expected 409 for duplicate, got %d", w.Code)
	}

	req = httptest.NewRequest("DELETE", "/api/v1/agents/tutor-agent", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Errorf("delete: expected 204, got %d", w.Code)
	}
}

func TestAgentRegister_InvalidAuthMethod(t *testing.T) {
	r, _ := setupRouter()
	body := `{"client_id":"bad","display_name":"Bad","auth_method":"invalid","allowed_caps":[{"uri":"cap://tool/x@v1","actions":["call"]}]}`
	req := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400: %s", w.Code, w.Body.String())
	}

	var errResp map[string]any
	json.NewDecoder(w.Body).Decode(&errResp)
	if errResp["code"] != "INVALID_AUTH_METHOD" {
		t.Errorf("expected error code INVALID_AUTH_METHOD, got %v", errResp["code"])
	}
}

func TestAgentRegister_InvalidCap(t *testing.T) {
	r, _ := setupRouter()
	body := `{"client_id":"bad2","display_name":"Bad","auth_method":"client_credentials","allowed_caps":[{"uri":"invalid","actions":["call"]}]}`
	req := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400: %s", w.Code, w.Body.String())
	}

	var errResp map[string]any
	json.NewDecoder(w.Body).Decode(&errResp)
	if errResp["code"] != "INVALID_CAPABILITY" {
		t.Errorf("expected error code INVALID_CAPABILITY, got %v", errResp["code"])
	}
}

func TestAgentRegister_MissingRedirectURIs(t *testing.T) {
	r, _ := setupRouter()
	body := `{"client_id":"auth-agent","display_name":"Auth","auth_method":"authorization_code","allowed_caps":[{"uri":"cap://tool/x@v1","actions":["call"]}]}`
	req := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400: %s", w.Code, w.Body.String())
	}

	var errResp map[string]any
	json.NewDecoder(w.Body).Decode(&errResp)
	if errResp["code"] != "MISSING_REDIRECT_URIS" {
		t.Errorf("expected error code MISSING_REDIRECT_URIS, got %v", errResp["code"])
	}
}

func TestAgentRegister_InvalidRedirectURI(t *testing.T) {
	r, _ := setupRouter()
	body := `{"client_id":"auth-agent","display_name":"Auth","auth_method":"authorization_code","allowed_caps":[{"uri":"cap://tool/x@v1","actions":["call"]}],"redirect_uris":["http://example.com/callback"]}`
	req := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400: %s", w.Code, w.Body.String())
	}

	var errResp map[string]any
	json.NewDecoder(w.Body).Decode(&errResp)
	if errResp["code"] != "INVALID_REDIRECT_URI" {
		t.Errorf("expected error code INVALID_REDIRECT_URI, got %v", errResp["code"])
	}
}

func TestAgentRegister_ValidRedirectURI(t *testing.T) {
	r, _ := setupRouter()
	body := `{"client_id":"auth-agent","display_name":"Auth","auth_method":"authorization_code","allowed_caps":[{"uri":"cap://tool/x@v1","actions":["call"]}],"redirect_uris":["https://example.com/callback","http://localhost:3000/callback"]}`
	req := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}

	var agent models.Agent
	json.NewDecoder(w.Body).Decode(&agent)
	if agent.ClientID != "auth-agent" {
		t.Errorf("expected client_id auth-agent, got %s", agent.ClientID)
	}
	if len(agent.RedirectURIs) != 2 {
		t.Errorf("expected 2 redirect URIs, got %d", len(agent.RedirectURIs))
	}
}

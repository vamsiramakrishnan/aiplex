package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/catalog"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// setupFullRouter builds the complete AIPlex API router — same wiring as main.go.
func setupFullRouter() *httptest.Server {
	store := registry.NewMemoryStore()

	// Seed templates for all three planes
	ctx := context.Background()
	store.PutTemplate(ctx, &models.Template{
		ID:    "kb-search",
		Plane: models.PlaneMCPlex,
		Name:  "Knowledge Base Search",
		Tools: []models.ToolInfo{
			{Name: "search_curriculum", Description: "Search the curriculum"},
			{Name: "get_document", Description: "Get a document"},
		},
		Category: "tools",
		Verified: true,
	})
	store.PutTemplate(ctx, &models.Template{
		ID:        "research-agent",
		Plane:     models.PlaneA2APlex,
		Name:      "Research Agent",
		TaskTypes: []string{"research", "summarize"},
		Category:  "agents",
		Verified:  true,
	})
	store.PutTemplate(ctx, &models.Template{
		ID:           "gemini-2.5-flash",
		Plane:        models.PlaneLLMPlex,
		Name:         "Gemini 2.5 Flash",
		ModelID:      "gemini-2.5-flash",
		Provider:     "google",
		Capabilities: []string{"text", "vision"},
		Category:     "llm",
		Verified:     true,
	})

	// Set up user scopes (Dimension B)
	store.SetUserScopes(ctx, "admin@school.edu", []string{
		"mcp:tools:search_curriculum", "mcp:tools:get_document",
		"a2a:task:research", "a2a:task:summarize",
		"llm:model:gemini-2.5-flash",
	})

	sources := []catalog.Source{
		catalog.NewLocalSource(store, models.PlaneMCPlex),
		catalog.NewLocalSource(store, models.PlaneA2APlex),
		catalog.NewLocalSource(store, models.PlaneLLMPlex),
		catalog.NewBuiltInProviders(),
	}
	agg := catalog.NewAggregator(sources)
	engine := deploy.NewEngine(store, "test.local")
	hydraClient := auth.NewHydraClient("http://localhost:0")

	catalogH := api.NewCatalogHandler(agg, store)
	instanceH := api.NewInstanceHandler(store, engine)
	agentH := api.NewAgentHandler(store)
	authH := api.NewAuthHandler(hydraClient, store)

	r := chi.NewRouter()
	r.Use(api.RequestID)

	r.Get("/healthz", api.Health)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/catalog", catalogH.List)
		r.Get("/catalog/{id}", catalogH.Get)
		r.Get("/instances", instanceH.List)
		r.Post("/instances", instanceH.Deploy)
		r.Get("/instances/{id}", instanceH.Get)
		r.Delete("/instances/{id}", instanceH.Undeploy)
		r.Get("/instances/{id}/history", instanceH.History)
		r.Get("/agents", agentH.List)
		r.Post("/agents", agentH.Register)
		r.Get("/agents/{clientId}", agentH.Get)
		r.Delete("/agents/{clientId}", agentH.Delete)
		r.Get("/agents/{clientId}/permissions", agentH.GetPermissions)
	})
	r.Route("/auth", func(r chi.Router) {
		r.Post("/token-hook", authH.TokenHook)
		r.Get("/users/{userId}/scopes", authH.GetUserScopes)
		r.Put("/users/{userId}/scopes", authH.SetUserScopes)
	})

	return httptest.NewServer(r)
}

func TestE2E_FullDeployLifecycle(t *testing.T) {
	srv := setupFullRouter()
	defer srv.Close()
	client := srv.Client()

	// 1. Health check
	resp, _ := client.Get(srv.URL + "/healthz")
	if resp.StatusCode != 200 {
		t.Fatalf("healthz: %d", resp.StatusCode)
	}

	// 2. Browse MCPlex catalog
	resp, _ = client.Get(srv.URL + "/api/v1/catalog?plane=mcplex")
	var catalogPage models.CatalogPage
	json.NewDecoder(resp.Body).Decode(&catalogPage)
	resp.Body.Close()
	if catalogPage.Total < 1 {
		t.Fatalf("expected at least 1 MCPlex template, got %d", catalogPage.Total)
	}

	// 3. Browse LLMPlex catalog (should include built-in providers)
	resp, _ = client.Get(srv.URL + "/api/v1/catalog?plane=llmplex")
	json.NewDecoder(resp.Body).Decode(&catalogPage)
	resp.Body.Close()
	if catalogPage.Total < 6 {
		t.Fatalf("expected at least 6 LLM providers, got %d", catalogPage.Total)
	}

	// 4. Deploy an MCP server
	body := `{"plane":"mcplex","template_id":"kb-search","display_name":"Knowledge Base"}`
	resp, _ = client.Post(srv.URL+"/api/v1/instances", "application/json", strings.NewReader(body))
	if resp.StatusCode != 201 {
		t.Fatalf("deploy MCPlex: %d", resp.StatusCode)
	}
	var mcpInst models.Instance
	json.NewDecoder(resp.Body).Decode(&mcpInst)
	resp.Body.Close()

	if mcpInst.Status != models.StatusRunning {
		t.Errorf("MCPlex instance status: %s", mcpInst.Status)
	}
	if len(mcpInst.Scopes) != 2 {
		t.Errorf("expected 2 MCP scopes, got %d: %v", len(mcpInst.Scopes), mcpInst.Scopes)
	}
	if mcpInst.SpiffeID == "" {
		t.Error("MCPlex instance missing SPIFFE ID")
	}

	// 5. Deploy an A2A agent
	body = `{"plane":"a2aplex","template_id":"research-agent","display_name":"Research"}`
	resp, _ = client.Post(srv.URL+"/api/v1/instances", "application/json", strings.NewReader(body))
	if resp.StatusCode != 201 {
		t.Fatalf("deploy A2APlex: %d", resp.StatusCode)
	}
	var a2aInst models.Instance
	json.NewDecoder(resp.Body).Decode(&a2aInst)
	resp.Body.Close()

	if len(a2aInst.Scopes) != 2 {
		t.Errorf("expected 2 A2A scopes, got %d", len(a2aInst.Scopes))
	}

	// 6. Deploy an LLM provider
	body = `{"plane":"llmplex","template_id":"gemini-2.5-flash","display_name":"Gemini Flash"}`
	resp, _ = client.Post(srv.URL+"/api/v1/instances", "application/json", strings.NewReader(body))
	if resp.StatusCode != 201 {
		t.Fatalf("deploy LLMPlex: %d", resp.StatusCode)
	}
	var llmInst models.Instance
	json.NewDecoder(resp.Body).Decode(&llmInst)
	resp.Body.Close()

	if llmInst.SpiffeID != "" {
		t.Error("LLMPlex should not have SPIFFE ID")
	}

	// 7. List all instances
	resp, _ = client.Get(srv.URL + "/api/v1/instances")
	var allInstances []models.Instance
	json.NewDecoder(resp.Body).Decode(&allInstances)
	resp.Body.Close()
	if len(allInstances) != 3 {
		t.Errorf("expected 3 instances, got %d", len(allInstances))
	}

	// 8. List by plane
	resp, _ = client.Get(srv.URL + "/api/v1/instances?plane=mcplex")
	var mcpInstances []models.Instance
	json.NewDecoder(resp.Body).Decode(&mcpInstances)
	resp.Body.Close()
	if len(mcpInstances) != 1 {
		t.Errorf("expected 1 MCPlex instance, got %d", len(mcpInstances))
	}

	// 9. Check deploy history
	resp, _ = client.Get(srv.URL + "/api/v1/instances/" + mcpInst.ID + "/history")
	var history []models.DeployHistory
	json.NewDecoder(resp.Body).Decode(&history)
	resp.Body.Close()
	if len(history) != 1 || history[0].Action != "deploy" {
		t.Errorf("expected 1 deploy history, got %+v", history)
	}

	// 10. Undeploy the MCP server
	req, _ := http.NewRequest("DELETE", srv.URL+"/api/v1/instances/"+mcpInst.ID, nil)
	resp, _ = client.Do(req)
	if resp.StatusCode != 204 {
		t.Fatalf("undeploy: %d", resp.StatusCode)
	}

	// 11. Verify terminated status
	resp, _ = client.Get(srv.URL + "/api/v1/instances/" + mcpInst.ID)
	var terminated models.Instance
	json.NewDecoder(resp.Body).Decode(&terminated)
	resp.Body.Close()
	if terminated.Status != models.StatusTerminated {
		t.Errorf("expected terminated, got %s", terminated.Status)
	}

	// 12. Verify undeploy history
	resp, _ = client.Get(srv.URL + "/api/v1/instances/" + mcpInst.ID + "/history")
	json.NewDecoder(resp.Body).Decode(&history)
	resp.Body.Close()
	if len(history) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(history))
	}
}

func TestE2E_AgentRegistrationWithPermissions(t *testing.T) {
	srv := setupFullRouter()
	defer srv.Close()
	client := srv.Client()

	// 1. Register an agent
	body := `{
		"client_id": "tutor-agent",
		"display_name": "Tutor Agent",
		"description": "Aristocratic tutoring agent",
		"auth_method": "client_credentials",
		"grant_types": ["client_credentials"],
		"allowed_scopes": ["mcp:tools:search_curriculum", "mcp:tools:get_document", "a2a:task:research", "llm:model:gemini-2.5-flash"],
		"spiffe_id": "spiffe://test.local/ns/a2aplex/sa/tutor-agent"
	}`
	resp, _ := client.Post(srv.URL+"/api/v1/agents", "application/json", strings.NewReader(body))
	if resp.StatusCode != 201 {
		t.Fatalf("register: %d", resp.StatusCode)
	}

	// 2. Get agent
	resp, _ = client.Get(srv.URL + "/api/v1/agents/tutor-agent")
	var agent models.Agent
	json.NewDecoder(resp.Body).Decode(&agent)
	resp.Body.Close()
	if agent.Status != "active" {
		t.Errorf("agent status: %s", agent.Status)
	}

	// 3. Get cross-plane permissions
	resp, _ = client.Get(srv.URL + "/api/v1/agents/tutor-agent/permissions")
	var perms models.AgentPermissions
	json.NewDecoder(resp.Body).Decode(&perms)
	resp.Body.Close()

	if len(perms.Ceiling[models.PlaneMCPlex]) != 2 {
		t.Errorf("expected 2 MCPlex scopes, got %d", len(perms.Ceiling[models.PlaneMCPlex]))
	}
	if len(perms.Ceiling[models.PlaneA2APlex]) != 1 {
		t.Errorf("expected 1 A2APlex scope, got %d", len(perms.Ceiling[models.PlaneA2APlex]))
	}
	if len(perms.Ceiling[models.PlaneLLMPlex]) != 1 {
		t.Errorf("expected 1 LLMPlex scope, got %d", len(perms.Ceiling[models.PlaneLLMPlex]))
	}

	// 4. Token hook should inject act claim
	hookBody := `{
		"subject": "student@school.edu",
		"client": {"client_id": "tutor-agent"},
		"granted_scopes": ["mcp:tools:search_curriculum"],
		"session": {"access_token": {}}
	}`
	resp, _ = client.Post(srv.URL+"/auth/token-hook", "application/json", strings.NewReader(hookBody))
	var hookResult map[string]any
	json.NewDecoder(resp.Body).Decode(&hookResult)
	resp.Body.Close()

	session := hookResult["session"].(map[string]any)
	accessToken := session["access_token"].(map[string]any)
	act := accessToken["act"].(map[string]any)
	if act["sub"] != "spiffe://test.local/ns/a2aplex/sa/tutor-agent" {
		t.Errorf("act claim: %v", act)
	}

	// 5. User scopes (Dimension B)
	resp, _ = client.Get(srv.URL + "/auth/users/admin@school.edu/scopes")
	var scopeResult map[string]any
	json.NewDecoder(resp.Body).Decode(&scopeResult)
	resp.Body.Close()

	scopes := scopeResult["scopes"].([]any)
	if len(scopes) != 5 {
		t.Errorf("expected 5 user scopes, got %d", len(scopes))
	}

	// 6. List all agents
	resp, _ = client.Get(srv.URL + "/api/v1/agents")
	var agents []models.Agent
	json.NewDecoder(resp.Body).Decode(&agents)
	resp.Body.Close()
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}

	// 7. Delete agent
	req, _ := http.NewRequest("DELETE", srv.URL+"/api/v1/agents/tutor-agent", nil)
	resp, _ = client.Do(req)
	if resp.StatusCode != 204 {
		t.Errorf("delete agent: %d", resp.StatusCode)
	}
}

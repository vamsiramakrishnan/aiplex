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
	store.PutTemplate(ctx, &models.Template{
		ID:          "code-review",
		Plane:       models.PlaneSkillsPlex,
		Name:        "Code Review",
		Description: "Review pull requests",
		SkillBundle: "code-review",
		Skills: []models.SkillInfo{
			{Name: "review_pr", Description: "Review a PR diff"},
			{Name: "suggest_tests", Description: "Suggest unit tests"},
		},
		Category: "skill",
		Verified: true,
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
		catalog.NewLocalSource(store, models.PlaneSkillsPlex),
		catalog.NewBuiltInProviders(),
	}
	agg := catalog.NewAggregator(sources)
	engine := deploy.NewEngine(store, "test.local")
	hydraClient := auth.NewHydraClient("http://localhost:0")

	catalogH := api.NewCatalogHandler(agg, store)
	instanceH := api.NewInstanceHandler(store, engine)
	agentH := api.NewAgentHandler(store)
	authH := api.NewAuthHandler(hydraClient, store)
	skillsH := api.NewSkillsHandler(store)
	dashH := api.NewDashboardHandler(store)

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
		r.Route("/skills", func(r chi.Router) {
			r.Get("/servers", skillsH.ListSkillServers)
			r.Post("/invocations", skillsH.RecordInvocation)
			r.Get("/invocations", skillsH.ListInvocations)
		})
		r.Route("/dashboard", func(r chi.Router) {
			r.Get("/stats", dashH.GetStats)
			r.Get("/denials", dashH.ListPolicyDenials)
		})
	})
	r.Get("/skills/{instanceId}/.well-known/skills.json", skillsH.GetSkillsManifest)
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

// TestE2E_SkillsPlexDeployAndInvocation exercises the full SkillsPlex flow:
// browse catalog → deploy skill server → fetch manifest → record successful
// invocation → record denied invocation → verify denial appears in dashboard.
func TestE2E_SkillsPlexDeployAndInvocation(t *testing.T) {
	srv := setupFullRouter()
	defer srv.Close()
	client := srv.Client()

	// 1. Browse SkillsPlex catalog — built-in skill template should appear.
	resp, _ := client.Get(srv.URL + "/api/v1/catalog?plane=skillsplex")
	var catalogPage models.CatalogPage
	json.NewDecoder(resp.Body).Decode(&catalogPage)
	resp.Body.Close()
	if catalogPage.Total < 1 {
		t.Fatalf("expected at least 1 SkillsPlex template, got %d", catalogPage.Total)
	}
	foundCodeReview := false
	for _, tmpl := range catalogPage.Templates {
		if tmpl.ID == "code-review" {
			foundCodeReview = true
			break
		}
	}
	if !foundCodeReview {
		t.Fatalf("expected 'code-review' in skillsplex catalog, got %+v", catalogPage.Templates)
	}

	// 2. Deploy the skill server.
	body := `{"plane":"skillsplex","template_id":"code-review","display_name":"Code Review"}`
	resp, _ = client.Post(srv.URL+"/api/v1/instances", "application/json", strings.NewReader(body))
	if resp.StatusCode != 201 {
		t.Fatalf("deploy SkillsPlex: %d", resp.StatusCode)
	}
	var inst models.Instance
	json.NewDecoder(resp.Body).Decode(&inst)
	resp.Body.Close()

	if inst.Plane != models.PlaneSkillsPlex {
		t.Errorf("Plane = %q, want skillsplex", inst.Plane)
	}
	if inst.Status != models.StatusRunning {
		t.Errorf("Status = %q, want running", inst.Status)
	}
	// Two skills + bundle = 3 scopes from the template.
	if len(inst.Scopes) != 3 {
		t.Errorf("expected 3 scopes from template, got %d: %v", len(inst.Scopes), inst.Scopes)
	}
	if inst.SpiffeID == "" {
		t.Error("SkillsPlex instance missing SPIFFE ID")
	}

	// 3. Fetch the well-known skills manifest.
	resp, _ = client.Get(srv.URL + "/skills/" + inst.ID + "/.well-known/skills.json")
	if resp.StatusCode != 200 {
		t.Fatalf("manifest: %d", resp.StatusCode)
	}
	var manifest map[string]any
	json.NewDecoder(resp.Body).Decode(&manifest)
	resp.Body.Close()
	if manifest["instance_id"] != inst.ID {
		t.Errorf("manifest instance_id = %v", manifest["instance_id"])
	}
	if manifest["skill_bundle"] != "code-review" {
		t.Errorf("manifest skill_bundle = %v", manifest["skill_bundle"])
	}
	skills := manifest["skills"].([]any)
	if len(skills) != 2 {
		t.Errorf("manifest expected 2 skills, got %d", len(skills))
	}

	// 4. Record a successful invocation.
	invBody := `{
		"agent_id": "tutor-agent",
		"instance_id": "` + inst.ID + `",
		"skill_name": "review_pr",
		"user_id": "alice@example.com",
		"duration_ms": 142
	}`
	resp, _ = client.Post(srv.URL+"/api/v1/skills/invocations", "application/json", strings.NewReader(invBody))
	if resp.StatusCode != 201 {
		t.Fatalf("record invocation: %d", resp.StatusCode)
	}
	var ok models.SkillInvocation
	json.NewDecoder(resp.Body).Decode(&ok)
	resp.Body.Close()
	if ok.Status != "success" {
		t.Errorf("default Status = %q, want success", ok.Status)
	}
	if ok.TraceID == "" || ok.SpanID == "" {
		t.Errorf("expected trace fields populated, got TraceID=%q SpanID=%q", ok.TraceID, ok.SpanID)
	}

	// 5. Record a denied invocation (scope missing).
	deniedBody := `{
		"agent_id": "tutor-agent",
		"instance_id": "` + inst.ID + `",
		"skill_name": "suggest_tests",
		"user_id": "alice@example.com",
		"status": "failed",
		"error": "missing scope: skill:invoke:suggest_tests"
	}`
	resp, _ = client.Post(srv.URL+"/api/v1/skills/invocations", "application/json", strings.NewReader(deniedBody))
	if resp.StatusCode != 201 {
		t.Fatalf("record denied invocation: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 6. List invocations — both should be present, newest first.
	resp, _ = client.Get(srv.URL + "/api/v1/skills/invocations")
	var invs []models.SkillInvocation
	json.NewDecoder(resp.Body).Decode(&invs)
	resp.Body.Close()
	if len(invs) != 2 {
		t.Fatalf("expected 2 invocations, got %d", len(invs))
	}

	// 7. Filter invocations by skill.
	resp, _ = client.Get(srv.URL + "/api/v1/skills/invocations?skill=review_pr")
	json.NewDecoder(resp.Body).Decode(&invs)
	resp.Body.Close()
	if len(invs) != 1 || invs[0].SkillName != "review_pr" {
		t.Errorf("filter by skill: got %+v", invs)
	}

	// 8. Verify the denied invocation produced a PolicyDenial.
	resp, _ = client.Get(srv.URL + "/api/v1/dashboard/denials")
	var denials []models.PolicyDenial
	json.NewDecoder(resp.Body).Decode(&denials)
	resp.Body.Close()
	if len(denials) != 1 {
		t.Fatalf("expected 1 PolicyDenial, got %d", len(denials))
	}
	if denials[0].Plane != string(models.PlaneSkillsPlex) {
		t.Errorf("denial Plane = %q, want skillsplex", denials[0].Plane)
	}
	if denials[0].Scope != "skill:invoke:suggest_tests" {
		t.Errorf("denial Scope = %q, want skill:invoke:suggest_tests", denials[0].Scope)
	}
	if denials[0].Reason != "scope_missing" {
		t.Errorf("denial Reason = %q, want scope_missing", denials[0].Reason)
	}

	// 9. Skill-server listing surfaces the running instance with its scopes.
	resp, _ = client.Get(srv.URL + "/api/v1/skills/servers")
	var servers []map[string]any
	json.NewDecoder(resp.Body).Decode(&servers)
	resp.Body.Close()
	if len(servers) != 1 {
		t.Fatalf("expected 1 skill server, got %d", len(servers))
	}
	if servers[0]["instance_id"] != inst.ID {
		t.Errorf("server instance_id = %v, want %s", servers[0]["instance_id"], inst.ID)
	}

	// 10. Dashboard stats reflect the running SkillsPlex instance and the denial.
	resp, _ = client.Get(srv.URL + "/api/v1/dashboard/stats")
	var stats models.DashboardStats
	json.NewDecoder(resp.Body).Decode(&stats)
	resp.Body.Close()
	if stats.SkillsPlexInstances != 1 {
		t.Errorf("SkillsPlexInstances = %d, want 1", stats.SkillsPlexInstances)
	}
	if stats.PolicyDenials != 1 {
		t.Errorf("PolicyDenials = %d, want 1", stats.PolicyDenials)
	}
}

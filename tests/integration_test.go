package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/catalog"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/memplex"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/internal/workflow"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// mustFetchTemplates fetches templates from a catalog source, panicking on
// error so test setup stays terse.
func mustFetchTemplates(s catalog.Source, ctx context.Context) []models.Template {
	templates, err := s.Fetch(ctx)
	if err != nil {
		panic(err)
	}
	return templates
}

// setupFullRouter builds the complete AIPlex API router — same wiring as main.go.
func setupFullRouter() *httptest.Server {
	store := registry.NewMemoryStore()

	ctx := context.Background()
	store.PutTemplate(ctx, &models.Template{
		ID:   "kb-search",
		Kind: capability.KindTool,
		Name: "Knowledge Base Search",
		Capabilities: []capability.Capability{
			{URI: "cap://tool/search_curriculum@v1", Kind: capability.KindTool, Name: "search_curriculum", Version: "v1", Description: "Search the curriculum"},
			{URI: "cap://tool/get_document@v1", Kind: capability.KindTool, Name: "get_document", Version: "v1", Description: "Get a document"},
		},
		Category: "tools",
		Verified: true,
	})
	store.PutTemplate(ctx, &models.Template{
		ID:   "research-agent",
		Kind: capability.KindTask,
		Name: "Research Agent",
		Capabilities: []capability.Capability{
			{URI: "cap://task/research@v1", Kind: capability.KindTask, Name: "research", Version: "v1"},
			{URI: "cap://task/summarize@v1", Kind: capability.KindTask, Name: "summarize", Version: "v1"},
		},
		Category: "agents",
		Verified: true,
	})
	store.PutTemplate(ctx, &models.Template{
		ID:       "gemini-2.5-flash",
		Kind:     capability.KindModel,
		Name:     "Gemini 2.5 Flash",
		ModelID:  "gemini-2.5-flash",
		Provider: "google",
		Capabilities: []capability.Capability{
			{URI: "cap://model/gemini-2.5-flash@v1", Kind: capability.KindModel, Name: "gemini-2.5-flash", Version: "v1"},
		},
		Category: "llm",
		Verified: true,
	})
	store.PutTemplate(ctx, &models.Template{
		ID:          "code-review",
		Kind:        capability.KindSkill,
		Name:        "Code Review",
		Description: "Review pull requests",
		SkillBundle: "code-review",
		Capabilities: []capability.Capability{
			{URI: "cap://skill/code-review/review_pr@v1", Kind: capability.KindSkill, Name: "code-review/review_pr", Version: "v1", Description: "Review a PR diff"},
			{URI: "cap://skill/code-review/suggest_tests@v1", Kind: capability.KindSkill, Name: "code-review/suggest_tests", Version: "v1", Description: "Suggest unit tests"},
		},
		Category: "skill",
		Verified: true,
	})

	// Seed built-in memory templates into the store (same flow as cmd/aiplex-api/main.go).
	for _, t := range mustFetchTemplates(catalog.NewBuiltInMemory(), ctx) {
		store.PutTemplate(ctx, &t)
	}

	store.SetUserCaps(ctx, "admin@school.edu", capability.CapSet{
		{URI: "cap://tool/search_curriculum@v1", Actions: []string{"call"}},
		{URI: "cap://tool/get_document@v1", Actions: []string{"call"}},
		{URI: "cap://task/research@v1", Actions: []string{"invoke"}},
		{URI: "cap://task/summarize@v1", Actions: []string{"invoke"}},
		{URI: "cap://model/gemini-2.5-flash@v1", Actions: []string{"complete"}},
	})

	sources := []catalog.Source{
		catalog.NewLocalSource(store, capability.KindTool),
		catalog.NewLocalSource(store, capability.KindTask),
		catalog.NewLocalSource(store, capability.KindModel),
		catalog.NewLocalSource(store, capability.KindSkill),
		catalog.NewLocalSource(store, capability.KindMemory),
		catalog.NewBuiltInProviders(),
		catalog.NewBuiltInMemory(),
	}
	agg := catalog.NewAggregator(sources)
	engine := deploy.NewEngine(store, "test.local")
	memBroker := memplex.NewBroker(memplex.NewLocalBackend())
	engine.RegisterKindHook(capability.KindMemory, memBroker)
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
		r.Get("/users/{userId}/caps", authH.GetUserCaps)
		r.Put("/users/{userId}/caps", authH.SetUserCaps)
	})

	// Memory plane: broker handles every cap://memory/* call.
	r.Mount("/cap/memory/", http.StripPrefix("", memBroker))

	return httptest.NewServer(r)
}

func TestE2E_FullDeployLifecycle(t *testing.T) {
	srv := setupFullRouter()
	defer srv.Close()
	client := srv.Client()

	resp, _ := client.Get(srv.URL + "/healthz")
	if resp.StatusCode != 200 {
		t.Fatalf("healthz: %d", resp.StatusCode)
	}

	resp, _ = client.Get(srv.URL + "/api/v1/catalog?kind=tool")
	var catalogPage models.CatalogPage
	json.NewDecoder(resp.Body).Decode(&catalogPage)
	resp.Body.Close()
	if catalogPage.Total < 1 {
		t.Fatalf("expected at least 1 tool template, got %d", catalogPage.Total)
	}

	resp, _ = client.Get(srv.URL + "/api/v1/catalog?kind=model")
	json.NewDecoder(resp.Body).Decode(&catalogPage)
	resp.Body.Close()
	if catalogPage.Total < 6 {
		t.Fatalf("expected at least 6 model templates, got %d", catalogPage.Total)
	}

	body := `{"kind":"tool","template_id":"kb-search","display_name":"Knowledge Base"}`
	resp, _ = client.Post(srv.URL+"/api/v1/instances", "application/json", strings.NewReader(body))
	if resp.StatusCode != 201 {
		t.Fatalf("deploy tool: %d", resp.StatusCode)
	}
	var toolInst models.Instance
	json.NewDecoder(resp.Body).Decode(&toolInst)
	resp.Body.Close()

	if toolInst.Status != models.StatusRunning {
		t.Errorf("tool instance status: %s", toolInst.Status)
	}
	if len(toolInst.Capabilities) != 2 {
		t.Errorf("expected 2 caps, got %d: %v", len(toolInst.Capabilities), toolInst.Capabilities)
	}
	if toolInst.SpiffeID == "" {
		t.Error("tool instance missing SPIFFE ID")
	}

	body = `{"kind":"task","template_id":"research-agent","display_name":"Research"}`
	resp, _ = client.Post(srv.URL+"/api/v1/instances", "application/json", strings.NewReader(body))
	if resp.StatusCode != 201 {
		t.Fatalf("deploy task: %d", resp.StatusCode)
	}
	var taskInst models.Instance
	json.NewDecoder(resp.Body).Decode(&taskInst)
	resp.Body.Close()
	if len(taskInst.Capabilities) != 2 {
		t.Errorf("expected 2 task caps, got %d", len(taskInst.Capabilities))
	}

	body = `{"kind":"model","template_id":"gemini-2.5-flash","display_name":"Gemini Flash"}`
	resp, _ = client.Post(srv.URL+"/api/v1/instances", "application/json", strings.NewReader(body))
	if resp.StatusCode != 201 {
		t.Fatalf("deploy model: %d", resp.StatusCode)
	}
	var modelInst models.Instance
	json.NewDecoder(resp.Body).Decode(&modelInst)
	resp.Body.Close()
	if modelInst.SpiffeID != "" {
		t.Error("model kind should not have SPIFFE ID")
	}

	resp, _ = client.Get(srv.URL + "/api/v1/instances")
	var allInstances []models.Instance
	json.NewDecoder(resp.Body).Decode(&allInstances)
	resp.Body.Close()
	if len(allInstances) != 3 {
		t.Errorf("expected 3 instances, got %d", len(allInstances))
	}

	resp, _ = client.Get(srv.URL + "/api/v1/instances?kind=tool")
	var toolInstances []models.Instance
	json.NewDecoder(resp.Body).Decode(&toolInstances)
	resp.Body.Close()
	if len(toolInstances) != 1 {
		t.Errorf("expected 1 tool instance, got %d", len(toolInstances))
	}

	resp, _ = client.Get(srv.URL + "/api/v1/instances/" + toolInst.ID + "/history")
	var history []models.DeployHistory
	json.NewDecoder(resp.Body).Decode(&history)
	resp.Body.Close()
	if len(history) != 1 || history[0].Action != "deploy" {
		t.Errorf("expected 1 deploy history, got %+v", history)
	}

	req, _ := http.NewRequest("DELETE", srv.URL+"/api/v1/instances/"+toolInst.ID, nil)
	resp, _ = client.Do(req)
	if resp.StatusCode != 204 {
		t.Fatalf("undeploy: %d", resp.StatusCode)
	}

	resp, _ = client.Get(srv.URL + "/api/v1/instances/" + toolInst.ID)
	var terminated models.Instance
	json.NewDecoder(resp.Body).Decode(&terminated)
	resp.Body.Close()
	if terminated.Status != models.StatusTerminated {
		t.Errorf("expected terminated, got %s", terminated.Status)
	}

	resp, _ = client.Get(srv.URL + "/api/v1/instances/" + toolInst.ID + "/history")
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

	body := `{
		"client_id": "tutor-agent",
		"display_name": "Tutor Agent",
		"description": "Aristocratic tutoring agent",
		"auth_method": "client_credentials",
		"grant_types": ["client_credentials"],
		"allowed_caps": [
			{"uri": "cap://tool/search_curriculum@v1", "actions": ["call"]},
			{"uri": "cap://tool/get_document@v1", "actions": ["call"]},
			{"uri": "cap://task/research@v1", "actions": ["invoke"]},
			{"uri": "cap://model/gemini-2.5-flash@v1", "actions": ["complete"]}
		],
		"spiffe_id": "spiffe://test.local/ns/a2aplex/sa/tutor-agent"
	}`
	resp, _ := client.Post(srv.URL+"/api/v1/agents", "application/json", strings.NewReader(body))
	if resp.StatusCode != 201 {
		t.Fatalf("register: %d", resp.StatusCode)
	}

	resp, _ = client.Get(srv.URL + "/api/v1/agents/tutor-agent")
	var agent models.Agent
	json.NewDecoder(resp.Body).Decode(&agent)
	resp.Body.Close()
	if agent.Status != "active" {
		t.Errorf("agent status: %s", agent.Status)
	}

	resp, _ = client.Get(srv.URL + "/api/v1/agents/tutor-agent/permissions")
	var perms models.AgentPermissions
	json.NewDecoder(resp.Body).Decode(&perms)
	resp.Body.Close()

	if len(perms.Ceiling[capability.KindTool]) != 2 {
		t.Errorf("expected 2 tool caps, got %d", len(perms.Ceiling[capability.KindTool]))
	}
	if len(perms.Ceiling[capability.KindTask]) != 1 {
		t.Errorf("expected 1 task cap, got %d", len(perms.Ceiling[capability.KindTask]))
	}
	if len(perms.Ceiling[capability.KindModel]) != 1 {
		t.Errorf("expected 1 model cap, got %d", len(perms.Ceiling[capability.KindModel]))
	}

	hookBody := `{
		"subject": "student@school.edu",
		"client": {"client_id": "tutor-agent"},
		"granted_scopes": ["cap://tool/search_curriculum@v1"],
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

	resp, _ = client.Get(srv.URL + "/auth/users/admin@school.edu/caps")
	var capsResult map[string]any
	json.NewDecoder(resp.Body).Decode(&capsResult)
	resp.Body.Close()

	caps := capsResult["caps"].([]any)
	if len(caps) != 5 {
		t.Errorf("expected 5 user caps, got %d", len(caps))
	}

	resp, _ = client.Get(srv.URL + "/api/v1/agents")
	var agents []models.Agent
	json.NewDecoder(resp.Body).Decode(&agents)
	resp.Body.Close()
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}

	req, _ := http.NewRequest("DELETE", srv.URL+"/api/v1/agents/tutor-agent", nil)
	resp, _ = client.Do(req)
	if resp.StatusCode != 204 {
		t.Errorf("delete agent: %d", resp.StatusCode)
	}
}

// TestE2E_SkillsKindLifecycle exercises the full skill kind flow:
// browse catalog → deploy skill server → fetch manifest → record successful
// invocation → record denied invocation → verify denial appears in dashboard.
func TestE2E_SkillsKindLifecycle(t *testing.T) {
	srv := setupFullRouter()
	defer srv.Close()
	client := srv.Client()

	resp, _ := client.Get(srv.URL + "/api/v1/catalog?kind=skill")
	var catalogPage models.CatalogPage
	json.NewDecoder(resp.Body).Decode(&catalogPage)
	resp.Body.Close()
	if catalogPage.Total < 1 {
		t.Fatalf("expected at least 1 skill template, got %d", catalogPage.Total)
	}
	foundCodeReview := false
	for _, tmpl := range catalogPage.Templates {
		if tmpl.ID == "code-review" {
			foundCodeReview = true
			break
		}
	}
	if !foundCodeReview {
		t.Fatalf("expected 'code-review' in skill catalog, got %+v", catalogPage.Templates)
	}

	body := `{"kind":"skill","template_id":"code-review","display_name":"Code Review"}`
	resp, _ = client.Post(srv.URL+"/api/v1/instances", "application/json", strings.NewReader(body))
	if resp.StatusCode != 201 {
		t.Fatalf("deploy skill: %d", resp.StatusCode)
	}
	var inst models.Instance
	json.NewDecoder(resp.Body).Decode(&inst)
	resp.Body.Close()

	if inst.Kind != capability.KindSkill {
		t.Errorf("Kind = %q, want skill", inst.Kind)
	}
	if inst.Status != models.StatusRunning {
		t.Errorf("Status = %q, want running", inst.Status)
	}
	if len(inst.Capabilities) != 2 {
		t.Errorf("expected 2 caps from template, got %d: %v", len(inst.Capabilities), inst.Capabilities)
	}
	if inst.SpiffeID == "" {
		t.Error("skill instance missing SPIFFE ID")
	}

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

	deniedBody := `{
		"agent_id": "tutor-agent",
		"instance_id": "` + inst.ID + `",
		"skill_name": "suggest_tests",
		"user_id": "alice@example.com",
		"status": "failed",
		"error": "missing cap: cap://skill/code-review/suggest_tests@v1"
	}`
	resp, _ = client.Post(srv.URL+"/api/v1/skills/invocations", "application/json", strings.NewReader(deniedBody))
	if resp.StatusCode != 201 {
		t.Fatalf("record denied invocation: %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp, _ = client.Get(srv.URL + "/api/v1/skills/invocations")
	var invs []models.SkillInvocation
	json.NewDecoder(resp.Body).Decode(&invs)
	resp.Body.Close()
	if len(invs) != 2 {
		t.Fatalf("expected 2 invocations, got %d", len(invs))
	}

	resp, _ = client.Get(srv.URL + "/api/v1/skills/invocations?skill=review_pr")
	json.NewDecoder(resp.Body).Decode(&invs)
	resp.Body.Close()
	if len(invs) != 1 || invs[0].SkillName != "review_pr" {
		t.Errorf("filter by skill: got %+v", invs)
	}

	resp, _ = client.Get(srv.URL + "/api/v1/dashboard/denials")
	var denials []models.PolicyDenial
	json.NewDecoder(resp.Body).Decode(&denials)
	resp.Body.Close()
	if len(denials) != 1 {
		t.Fatalf("expected 1 PolicyDenial, got %d", len(denials))
	}
	if denials[0].Kind != capability.KindSkill {
		t.Errorf("denial Kind = %q, want skill", denials[0].Kind)
	}
	if denials[0].CapURI != "cap://skill/code-review/suggest_tests@v1" {
		t.Errorf("denial CapURI = %q", denials[0].CapURI)
	}
	if denials[0].Reason != "cap_missing" {
		t.Errorf("denial Reason = %q, want cap_missing", denials[0].Reason)
	}

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

	resp, _ = client.Get(srv.URL + "/api/v1/dashboard/stats")
	var stats models.DashboardStats
	json.NewDecoder(resp.Body).Decode(&stats)
	resp.Body.Close()
	if stats.InstancesByKind[capability.KindSkill] != 1 {
		t.Errorf("InstancesByKind[skill] = %d, want 1", stats.InstancesByKind[capability.KindSkill])
	}
	if stats.PolicyDenials != 1 {
		t.Errorf("PolicyDenials = %d, want 1", stats.PolicyDenials)
	}
}

// TestE2E_MemoryKindLifecycle exercises a full memory namespace:
// catalog → deploy → write/read/search/list/delete via the broker.
func TestE2E_MemoryKindLifecycle(t *testing.T) {
	srv := setupFullRouter()
	defer srv.Close()
	client := srv.Client()

	// 1. Catalog includes the built-in memory templates.
	resp, _ := client.Get(srv.URL + "/api/v1/catalog?kind=memory")
	var catalogPage models.CatalogPage
	json.NewDecoder(resp.Body).Decode(&catalogPage)
	resp.Body.Close()
	if catalogPage.Total < 1 {
		t.Fatalf("expected at least 1 memory template, got %d", catalogPage.Total)
	}

	// 2. Deploy a memory namespace from the built-in scratch template.
	body := `{"kind":"memory","template_id":"scratch","display_name":"Scratch"}`
	resp, _ = client.Post(srv.URL+"/api/v1/instances", "application/json", strings.NewReader(body))
	if resp.StatusCode != 201 {
		buf := make([]byte, 1024)
		n, _ := resp.Body.Read(buf)
		t.Fatalf("deploy memory: %d %s", resp.StatusCode, string(buf[:n]))
	}
	var inst models.Instance
	json.NewDecoder(resp.Body).Decode(&inst)
	resp.Body.Close()
	if inst.Kind != capability.KindMemory {
		t.Errorf("Kind = %q, want memory", inst.Kind)
	}
	if len(inst.Capabilities) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(inst.Capabilities))
	}

	uri := inst.Capabilities[0].URI

	// 3. Write a value.
	pathBase := "/cap/memory/" + strings.TrimPrefix(uri, "cap://memory/")
	writeBody := `{"data":{"x":42,"note":"hello"}}`
	req, _ := http.NewRequest("PUT", srv.URL+pathBase+"/k1", strings.NewReader(writeBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = client.Do(req)
	if resp.StatusCode != 204 {
		t.Fatalf("write: status=%d", resp.StatusCode)
	}

	// 4. Read it back.
	resp, _ = client.Get(srv.URL + pathBase + "/k1")
	if resp.StatusCode != 200 {
		t.Fatalf("read: status=%d", resp.StatusCode)
	}
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	data := got["data"].(map[string]any)
	if data["note"] != "hello" {
		t.Errorf("data.note = %v, want hello", data["note"])
	}

	// 5. Write a second key, then list with prefix.
	req, _ = http.NewRequest("PUT", srv.URL+pathBase+"/k2",
		strings.NewReader(`{"data":{"x":100}}`))
	req.Header.Set("Content-Type", "application/json")
	client.Do(req)

	resp, _ = client.Get(srv.URL + pathBase + "/?prefix=k")
	var listing map[string]any
	json.NewDecoder(resp.Body).Decode(&listing)
	resp.Body.Close()
	keys := listing["keys"].([]any)
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}

	// 6. Search with a vector.
	searchBody := `{"embedding":[1,0,0],"top_k":5}`
	resp, _ = client.Post(srv.URL+pathBase+"/_search", "application/json",
		strings.NewReader(searchBody))
	if resp.StatusCode != 200 {
		t.Fatalf("search: status=%d", resp.StatusCode)
	}

	// 7. Delete and verify 404.
	req, _ = http.NewRequest("DELETE", srv.URL+pathBase+"/k1", nil)
	resp, _ = client.Do(req)
	if resp.StatusCode != 204 {
		t.Errorf("delete: status=%d", resp.StatusCode)
	}
	resp, _ = client.Get(srv.URL + pathBase + "/k1")
	if resp.StatusCode != 404 {
		t.Errorf("post-delete read: status=%d, want 404", resp.StatusCode)
	}
}

// TestE2E_WorkflowAsCapability proves the agent-as-cap insight end-to-end:
// deploy a workflow capability, invoke it, and verify it chains three
// downstream cap calls (a stub tool, a stub model, and a real memory write)
// while threading the original token through every step. The receipt for
// the workflow run lists each step and its outcome — one connected audit
// trail rather than a forest of orphan invocations.
func TestE2E_WorkflowAsCapability(t *testing.T) {
	store := registry.NewMemoryStore()
	ctx := context.Background()

	// Seed two stub backends as fake "downstream caps" the workflow will
	// call: a tool that returns canned content and a model that echoes it.
	stubMux := chi.NewRouter()
	stubMux.Post("/cap/tool/get_quiz@v1/_invoke", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"content": "What is 2+2?",
			"id":      "q-1",
		})
	})
	stubMux.Post("/cap/model/echo@v1/_invoke", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input map[string]any `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(map[string]any{
			"text": "GRADED: " + fmt.Sprintf("%v", body.Input["prompt"]),
		})
	})

	// Real memory broker — the workflow's third step writes to it.
	memBroker := memplex.NewBroker(memplex.NewLocalBackend())
	memURI := capability.New(capability.KindMemory, "students/{student}/grades", "v1")
	memBroker.Register(memplex.Namespace{URI: memURI, Backend: "local"}, nil)
	stubMux.Mount("/cap/memory/", http.StripPrefix("", memBroker))

	// Workflow executor pointing back at the same test server.
	wfSpec := workflow.Spec{
		Inputs: workflow.InputSchema{Required: []string{"quiz_id", "student"}},
		Steps: []workflow.Step{
			{
				ID:     "fetch",
				Cap:    "cap://tool/get_quiz@v1",
				Action: "call",
				Input:  map[string]any{"id": "{{ inputs.quiz_id }}"},
			},
			{
				ID:     "grade",
				Cap:    "cap://model/echo@v1",
				Action: "complete",
				Input: map[string]any{
					"prompt": "Grade: {{ steps.fetch.output.content }}",
				},
			},
			{
				ID:     "store",
				Cap:    "cap://memory/students/{{ inputs.student }}/grades@v1",
				Action: "write",
				Input: map[string]any{
					"key": "quiz-{{ inputs.quiz_id }}",
					"data": map[string]any{
						"quiz":  "{{ inputs.quiz_id }}",
						"grade": "{{ steps.grade.output.text }}",
					},
				},
			},
		},
		Outputs: workflow.OutputSchema{
			"grade": "{{ steps.grade.output.text }}",
		},
	}

	// Deploy the workflow as a capability. The deploy engine forwards
	// tmpl.Config["spec"] into inst.Config and the workflow KindHook reads
	// it back out — so the spec lives inside the capability, not next to it.
	specJSON, _ := json.Marshal(wfSpec)
	store.PutTemplate(ctx, &models.Template{
		ID:   "grade-quiz",
		Kind: capability.KindWorkflow,
		Name: "Grade Quiz",
		Capabilities: []capability.Capability{{
			URI: "cap://workflow/grade-quiz@v1", Kind: capability.KindWorkflow,
			Name: "grade-quiz", Version: "v1",
		}},
		Config: map[string]any{"spec": json.RawMessage(specJSON)},
	})

	engine := deploy.NewEngine(store, "test.local")
	srv := httptest.NewServer(stubMux)
	defer srv.Close()

	wfExec := workflow.NewExecutor(workflow.NewHTTPInvoker(srv.URL), 50)
	engine.RegisterKindHook(capability.KindWorkflow, wfExec)
	wfServer := workflow.NewServer(wfExec)
	stubMux.Mount("/cap/workflow/", http.StripPrefix("", wfServer))

	inst, err := engine.Deploy(ctx, capability.KindWorkflow, "grade-quiz",
		nil, "alice@school.edu", "Grade Quiz Workflow")
	if err != nil {
		t.Fatalf("deploy workflow: %v", err)
	}
	if inst.Kind != capability.KindWorkflow {
		t.Fatalf("kind = %s, want workflow", inst.Kind)
	}
	if !wfExec.HasSpec("cap://workflow/grade-quiz@v1") {
		t.Fatalf("workflow spec was not registered with the executor")
	}

	// Invoke the workflow via the SDK.
	c := aiplex.NewClient(srv.URL)
	c.SetToken("test-token-xyz")
	run, err := c.Workflow("cap://workflow/grade-quiz@v1").Run(ctx, map[string]any{
		"quiz_id": "q-1",
		"student": "alice",
	})
	if err != nil {
		t.Fatalf("workflow run: %v", err)
	}

	if run.Status != "succeeded" {
		t.Fatalf("Status = %q (error: %s)", run.Status, run.Error)
	}
	if len(run.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(run.Steps))
	}
	for i, s := range run.Steps {
		if s.Status != "succeeded" {
			t.Errorf("step %d (%s) Status = %q, error = %s", i, s.StepID, s.Status, s.Error)
		}
	}

	// The third step's templated URI should have substituted {student}=alice.
	if got := run.Steps[2].Cap; got != "cap://memory/students/alice/grades@v1" {
		t.Errorf("step 3 Cap = %q, want template substituted", got)
	}

	// The workflow's declared output should contain the model's response.
	grade, _ := run.Outputs["grade"].(string)
	if !strings.Contains(grade, "GRADED:") {
		t.Errorf("Outputs[grade] = %q, want contains GRADED:", grade)
	}

	// Verify the memory write actually happened by reading it back.
	resp, _ := http.Get(srv.URL + "/cap/memory/students/alice/grades@v1/")
	if resp.StatusCode != 200 {
		t.Errorf("list memory: status=%d", resp.StatusCode)
	}
}

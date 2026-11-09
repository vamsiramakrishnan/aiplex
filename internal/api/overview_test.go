package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

type ovFixture struct {
	*apFixture
	ledger *usageledger.MemoryStore
	reg    *registry.MemoryStore
	router http.Handler
	now    time.Time
	roles  []models.IAMRole
}

func i64(v int64) *int64 { return &v }

func ovRecord(id, tenant string, at time.Time) usageledger.Record {
	return usageledger.Record{ID: id, TenantID: tenant, StartedAt: at, FinishedAt: at.Add(time.Second), Plane: usageledger.PlaneLLM, Status: usageledger.StatusOK, Source: usageledger.SourceManaged, Requests: 1, Cost: usageledger.UnknownCost()}
}

func newOverviewFixture(t *testing.T) *ovFixture {
	t.Helper()
	base := newAPFixture(t)
	f := &ovFixture{apFixture: base, ledger: usageledger.NewMemoryStore(), reg: registry.NewMemoryStore(), now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC), roles: []models.IAMRole{models.RoleAdmin}}
	ctx := context.Background()

	// A second access point for agents, plus an open model-only access point.
	base.put(AccessPointKind, "agents", AccessPoint{ID: "agents", CallerKinds: []string{CallerAgent}, Subjects: AccessPointSubjects{Agents: []string{"spiffe://x/tutor"}}, Tools: []AccessPointTool{{ConnectionRef: "search"}}, QoS: &AccessPointQoS{Class: "gold", Limits: &AccessPointLimit{RequestsPerMinute: 60}}})
	base.put(AccessPointKind, "chat-only", AccessPoint{ID: "chat-only", CallerKinds: []string{CallerUser}, Models: []AccessPointModel{{Name: "chat-model", SurfaceRef: "chat", NativeModel: "chat-model"}}})

	alice := apUser("alice").PrincipalID
	bob := apUser("bob").PrincipalID
	if err := f.reg.PutAgent(ctx, &models.Agent{ClientID: "tutor", DisplayName: "Tutor agent", AuthMethod: "client_credentials", SpiffeID: "spiffe://x/tutor", Status: "active", AllowedScopes: []string{"llm:model:gemini-2.5-pro", "mcp:tools:search"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.reg.SetUserScopes(ctx, alice, []string{"llm:model:gemini-2.5-pro"}); err != nil {
		t.Fatal(err)
	}
	if err := f.reg.PutRoleBinding(ctx, &models.RoleBinding{ID: "rb-eng", Group: "platform-eng", Role: models.RoleDeployer, Scopes: []string{"mcp:tools:search"}}); err != nil {
		t.Fatal(err)
	}

	at := f.now.Add(-2 * time.Hour)
	// alice: one fully known record, one with no usage and no price.
	r1 := ovRecord("r1", "tenant", at)
	r1.AccessPointID, r1.CallerKind, r1.PrincipalID, r1.ModelID = "ide", CallerUser, alice, "gemini-2.5-pro"
	r1.Usage = &usageledger.Usage{InputTokens: i64(10), OutputTokens: i64(5), TotalTokens: i64(15)}
	r1.Cost = usageledger.Cost{Basis: usageledger.BasisSettled, Currency: "USD", Amount: "1234", Scale: 4}
	r2 := ovRecord("r2", "tenant", at.Add(time.Minute))
	r2.AccessPointID, r2.CallerKind, r2.PrincipalID, r2.ModelID = "ide", CallerUser, alice, "gemini-2.5-pro"
	// tutor agent through the agents access point.
	r3 := ovRecord("r3", "tenant", at.Add(2*time.Minute))
	r3.Plane, r3.AccessPointID, r3.CallerKind, r3.PrincipalID, r3.ActorID, r3.ToolName = usageledger.PlaneMCP, "agents", CallerAgent, "wl-principal", "spiffe://x/tutor", "search"
	// bob through the registered client "ide".
	r4 := ovRecord("r4", "tenant", at.Add(3*time.Minute))
	r4.AccessPointID, r4.CallerKind, r4.PrincipalID, r4.ClientID, r4.ModelID = "ide", CallerClient, bob, "ide", "claude-sonnet-4-5"
	// another tenant, and an old record outside the window.
	other := ovRecord("r5", "other", at)
	other.AccessPointID, other.CallerKind, other.PrincipalID = "ide", CallerUser, "other-user"
	old := ovRecord("r6", "tenant", f.now.Add(-72*time.Hour))
	old.AccessPointID, old.CallerKind, old.PrincipalID = "chat-only", CallerUser, "dormant-user"
	for _, rec := range []usageledger.Record{r1, r2, r3, r4, other, old} {
		if err := f.ledger.Append(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	base.h.PublicURL = "https://gateway.example"
	h := NewOverviewHandler(base.h, f.ledger, f.reg, nil)
	h.Now = func() time.Time { return f.now }
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), keyRequestID, "request-1")
			if f.roles != nil {
				id := apUser("operator")
				ctx = context.WithValue(ctx, keyWIFIdentity, id)
				ctx = context.WithValue(ctx, keyWIFAccess, &models.ResolvedAccess{Identity: *id, Roles: f.roles})
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	router.Route("/api/v1", func(r chi.Router) { h.Mount(r) })
	f.router = router
	return f
}

func (f *ovFixture) get(path string, headers ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}

func (f *ovFixture) json(path string) map[string]any {
	f.t.Helper()
	w := f.get(path)
	if w.Code != http.StatusOK {
		f.t.Fatalf("GET %s = %d %s", path, w.Code, w.Body.String())
	}
	return decodeJSON(f.t, w)
}

func TestOverviewRoleMatrix(t *testing.T) {
	f := newOverviewFixture(t)
	paths := []string{"/api/v1/overview/subjects?kind=user", "/api/v1/overview/access-points/ide", "/api/v1/overview/access-points/ide/connect", "/api/v1/overview/subjects/client/ide"}
	cases := []struct {
		name  string
		roles []models.IAMRole
		want  int
	}{
		{"admin", []models.IAMRole{models.RoleAdmin}, 200},
		{"deployer", []models.IAMRole{models.RoleDeployer}, 200},
		{"viewer", []models.IAMRole{models.RoleViewer}, 403},
		{"no roles", []models.IAMRole{}, 403},
		{"anonymous", nil, 401},
	}
	for _, c := range cases {
		f.roles = c.roles
		for _, p := range paths {
			if w := f.get(p); w.Code != c.want {
				t.Errorf("%s GET %s = %d, want %d: %s", c.name, p, w.Code, c.want, w.Body.String())
			}
		}
	}
}

func TestOverviewTenantIsolation(t *testing.T) {
	f := newOverviewFixture(t)
	f.apFixture.tenant = "other"
	for _, p := range []string{"/api/v1/overview/access-points/ide", "/api/v1/overview/access-points/ide/connect", "/api/v1/overview/subjects/client/ide"} {
		if w := f.get(p); w.Code != http.StatusNotFound {
			t.Errorf("GET %s from another tenant = %d, want 404: %s", p, w.Code, w.Body.String())
		}
	}
	out := f.json("/api/v1/overview/subjects?kind=user")
	rows := out["subjects"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != "other-user" {
		t.Fatalf("other tenant sees %v, want only its own user", rows)
	}
	if w := f.get("/api/v1/overview/subjects/user/" + apUser("alice").PrincipalID); w.Code != http.StatusNotFound {
		t.Fatalf("another tenant's user detail = %d", w.Code)
	}
}

func TestOverviewSubjectsUnknownIsNeverZero(t *testing.T) {
	f := newOverviewFixture(t)
	out := f.json("/api/v1/overview/subjects?kind=user")
	var alice map[string]any
	ids := []string{}
	for _, row := range out["subjects"].([]any) {
		m := row.(map[string]any)
		ids = append(ids, m["id"].(string))
		if m["id"] == apUser("alice").PrincipalID {
			alice = m
		}
	}
	if alice == nil {
		t.Fatalf("alice missing from %v", ids)
	}
	usage := alice["usage"].(map[string]any)
	tokens := usage["tokens"].(map[string]any)
	unknown := usage["tokens_unknown"].(map[string]any)
	if tokens["input_tokens"] != float64(10) || tokens["total_tokens"] != float64(15) {
		t.Fatalf("known tokens = %v", tokens)
	}
	for _, counter := range []string{"reasoning_tokens", "cache_read_tokens", "cache_write_tokens"} {
		if _, present := tokens[counter]; present {
			t.Errorf("counter %s never reported but present in tokens: %v", counter, tokens)
		}
		if unknown[counter] != float64(2) {
			t.Errorf("tokens_unknown[%s] = %v, want 2", counter, unknown[counter])
		}
	}
	if unknown["input_tokens"] != float64(1) {
		t.Errorf("tokens_unknown[input_tokens] = %v, want 1 (one record without usage)", unknown["input_tokens"])
	}
	if usage["unknown_cost_records"] != float64(1) {
		t.Errorf("unknown_cost_records = %v, want 1", usage["unknown_cost_records"])
	}
	cost := usage["cost"].([]any)
	if len(cost) != 1 || cost[0].(map[string]any)["amount"] != "1234" || cost[0].(map[string]any)["basis"] != "settled" {
		t.Errorf("cost = %v", cost)
	}
	if usage["requests"] != float64(2) {
		t.Errorf("requests = %v", usage["requests"])
	}
	if alice["last_seen"] == nil {
		t.Error("last_seen missing")
	}
	// A user seen only before the window has no usage in it, but keeps last_seen.
	out = f.json("/api/v1/overview/subjects?kind=user&since=7d")
	foundDormant := false
	for _, row := range out["subjects"].([]any) {
		if row.(map[string]any)["id"] == "dormant-user" {
			foundDormant = true
		}
	}
	if !foundDormant {
		t.Error("dormant user should appear in a 7d window")
	}
}

func TestOverviewAgentsAndClients(t *testing.T) {
	f := newOverviewFixture(t)
	out := f.json("/api/v1/overview/subjects?kind=agent")
	rows := out["subjects"].([]any)
	if len(rows) != 1 {
		t.Fatalf("agents = %v", rows)
	}
	agent := rows[0].(map[string]any)
	if agent["id"] != "tutor" || agent["display_name"] != "Tutor agent" {
		t.Fatalf("agent id was not resolved to the registered client: %v", agent)
	}
	if agent["identity"].(map[string]any)["source"] != "spiffe" {
		t.Fatalf("identity = %v", agent["identity"])
	}
	if aps := agent["access_points"].([]any); len(aps) != 1 || aps[0] != "agents" {
		t.Fatalf("access points = %v", aps)
	}
	out = f.json("/api/v1/overview/subjects?kind=client")
	client := out["subjects"].([]any)[0].(map[string]any)
	if client["id"] != "ide" || client["identity"].(map[string]any)["source"] != "client-registration" {
		t.Fatalf("client = %v", client)
	}
	if client["usage"].(map[string]any)["requests"] != float64(1) {
		t.Fatalf("client usage = %v", client["usage"])
	}
}

func TestOverviewSubjectsPagination(t *testing.T) {
	f := newOverviewFixture(t)
	out := f.json("/api/v1/overview/subjects?kind=user&since=7d&limit=1")
	first := out["subjects"].([]any)[0].(map[string]any)["id"].(string)
	cursor, _ := out["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("expected a next cursor: %v", out)
	}
	out2 := f.json("/api/v1/overview/subjects?kind=user&since=7d&limit=5&cursor=" + cursor)
	for _, row := range out2["subjects"].([]any) {
		if row.(map[string]any)["id"].(string) <= first {
			t.Fatalf("cursor page repeats %v", row)
		}
	}
	for _, bad := range []string{"kind=robot", "kind=user&limit=0", "kind=user&limit=9999", "kind=user&bogus=1", "kind=user&cursor=!!", "kind=user&since=tomorrow", "kind=user&since=9999d", ""} {
		if w := f.get("/api/v1/overview/subjects?" + bad); w.Code != http.StatusBadRequest {
			t.Errorf("query %q = %d, want 400", bad, w.Code)
		}
	}
}

func TestOverviewUserDetailReasons(t *testing.T) {
	f := newOverviewFixture(t)
	alice := apUser("alice").PrincipalID
	out := f.json("/api/v1/overview/subjects/user/" + alice)
	access := out["access"].(map[string]any)
	var gemini, claude map[string]any
	for _, item := range access["models"].([]any) {
		m := item.(map[string]any)
		switch m["resource"] {
		case "gemini-2.5-pro":
			if m["access_point_id"] == "ide" {
				gemini = m
			}
		case "claude-sonnet-4-5":
			claude = m
		}
	}
	if gemini == nil || gemini["state"] != "granted" {
		t.Fatalf("gemini access = %v", gemini)
	}
	reasons := gemini["reasons"].([]any)
	types := []string{}
	for _, r := range reasons {
		types = append(types, r.(map[string]any)["type"].(string))
	}
	if strings.Join(types, ",") != "access-point,manual-scope" {
		t.Fatalf("reason types = %v", types)
	}
	if claude == nil || claude["state"] != "not_granted" {
		t.Fatalf("claude should be not_granted without any scope: %v", claude)
	}
	// Tools are only possible through the role binding, which depends on groups.
	var search map[string]any
	for _, item := range access["tools"].([]any) {
		if m := item.(map[string]any); m["name"] == "search__search" {
			search = m
		}
	}
	if search == nil || search["state"] != "unknown" {
		t.Fatalf("tool access should be unknown (group-dependent): %v", search)
	}
	conditional := false
	for _, r := range search["reasons"].([]any) {
		if m := r.(map[string]any); m["type"] == "role-binding" && m["conditional"] == true {
			conditional = true
		}
	}
	if !conditional {
		t.Fatalf("role binding reason missing: %v", search["reasons"])
	}
	bal := out["quota"].(map[string]any)["balance"].(map[string]any)
	if bal["available"] != false || bal["reason"] == "" {
		t.Fatalf("balance must be omitted with a reason: %v", bal)
	}
	if out["usage_by_model"].([]any)[0].(map[string]any)["key"].(map[string]any)["model_id"] != "gemini-2.5-pro" {
		t.Fatalf("usage_by_model = %v", out["usage_by_model"])
	}
}

func TestOverviewAgentDetail(t *testing.T) {
	f := newOverviewFixture(t)
	out := f.json("/api/v1/overview/subjects/agent/spiffe:%2F%2Fx%2Ftutor")
	if out["id"] != "tutor" {
		t.Fatalf("alias not resolved: %v", out["id"])
	}
	tools := out["access"].(map[string]any)["tools"].([]any)
	if len(tools) == 0 || tools[0].(map[string]any)["state"] != "granted" || tools[0].(map[string]any)["reasons"].([]any)[1].(map[string]any)["type"] != "agent-ceiling" {
		t.Fatalf("tools = %v", tools)
	}
	byTool := out["usage_by_tool"].([]any)
	if len(byTool) != 1 || byTool[0].(map[string]any)["key"].(map[string]any)["tool_name"] != "search" {
		t.Fatalf("usage_by_tool = %v", byTool)
	}
	quota := out["quota"].(map[string]any)["access_points"].([]any)
	if len(quota) != 1 || quota[0].(map[string]any)["qos_class"] != "gold" {
		t.Fatalf("quota = %v", quota)
	}
}

func TestOverviewAccessPoint(t *testing.T) {
	f := newOverviewFixture(t)
	out := f.json("/api/v1/overview/access-points/ide")
	res := out["resources"].(map[string]any)
	if len(res["models"].([]any)) != 4 || len(res["tools"].([]any)) != 3 {
		t.Fatalf("resources = %v", res)
	}
	callers := out["callers"].(map[string]any)
	if len(callers["users"].([]any)) != 1 || len(callers["clients"].([]any)) != 1 {
		t.Fatalf("callers = %v", callers)
	}
	if open := callers["open_to_all_of_kind"].([]any); len(open) != 2 {
		t.Fatalf("open = %v", open)
	}
	usage := out["usage"].(map[string]any)
	kinds := usage["by_caller_kind"].([]any)
	if len(kinds) != 2 {
		t.Fatalf("by_caller_kind = %v", kinds)
	}
	if usage["total"].(map[string]any)["requests"] != float64(3) {
		t.Fatalf("total = %v", usage["total"])
	}
	if out["quota"].(map[string]any)["coverage"].(map[string]any)["state"] != "not_required" {
		t.Fatalf("quota = %v", out["quota"])
	}
	qos := f.json("/api/v1/overview/access-points/agents")
	cov := qos["quota"].(map[string]any)["coverage"].(map[string]any)
	if cov["state"] != "unknown" || qos["qos"].(map[string]any)["class"] != "gold" {
		t.Fatalf("a QoS access point with no visible quota policy must report unknown, got %v", cov)
	}
	if w := f.get("/api/v1/overview/access-points/missing"); w.Code != http.StatusNotFound {
		t.Fatalf("missing = %d", w.Code)
	}
}

func TestOverviewConnectProtocolAvailability(t *testing.T) {
	f := newOverviewFixture(t)
	full := f.json("/api/v1/overview/access-points/ide/connect")
	urls := full["base_urls"].(map[string]any)
	if urls["openai"] != "https://gateway.example/ap/ide/v1" || urls["anthropic"] != "https://gateway.example/ap/ide" || urls["genai"] != "https://gateway.example/ap/ide" {
		t.Fatalf("base_urls = %v", urls)
	}
	if full["mcp_url"] != "https://gateway.example/ap/ide/mcp" || full["mcp_catalog_url"] != "https://gateway.example/ap/ide/v0.1/servers" {
		t.Fatalf("mcp urls = %v %v", full["mcp_url"], full["mcp_catalog_url"])
	}
	if _, ok := full["skills_url"]; ok {
		t.Fatal("skills_url offered without skills")
	}
	chatOnly := f.json("/api/v1/overview/access-points/chat-only/connect")
	urls = chatOnly["base_urls"].(map[string]any)
	if len(urls) != 1 || urls["openai"] == nil {
		t.Fatalf("chat-only base_urls = %v", urls)
	}
	if _, ok := chatOnly["mcp_url"]; ok {
		t.Fatal("mcp_url offered for an access point with no tools")
	}
	toolsOnly := f.json("/api/v1/overview/access-points/agents/connect")
	if len(toolsOnly["base_urls"].(map[string]any)) != 0 || toolsOnly["mcp_url"] == nil {
		t.Fatalf("tools-only = %v", toolsOnly)
	}
	// Known profiles: claude-code speaks Messages (available), codex needs
	// Responses (not served by any model here).
	cc := f.json("/api/v1/overview/access-points/ide/connect?client=claude-code")
	found := false
	for _, s := range cc["snippets"].([]any) {
		if c := s.(map[string]any)["content"].(string); strings.Contains(c, "ANTHROPIC_BASE_URL") && strings.Contains(c, "https://gateway.example/ap/ide") {
			found = true
		}
	}
	if !found {
		t.Fatalf("claude-code snippets = %v", cc["snippets"])
	}
	if w := f.get("/api/v1/overview/access-points/ide/connect?client=codex"); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("codex without a Responses model = %d %s", w.Code, w.Body.String())
	}
	if w := f.get("/api/v1/overview/access-points/ide/connect?client=nonsense"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown client = %d", w.Code)
	}
	if w := f.get("/api/v1/overview/access-points/chat-only/connect?client=antigravity"); w.Code != http.StatusOK {
		t.Fatalf("antigravity with chat model = %d %s", w.Code, w.Body.String())
	}
}

func TestOverviewConnectUsesPublicURLAndLeaksNothing(t *testing.T) {
	f := newOverviewFixture(t)
	for _, client := range []string{"generic", "claude-code", "antigravity"} {
		w := f.get("/api/v1/overview/access-points/ide/connect?client="+client, "Host", "evil.example", "X-Forwarded-Host", "evil.example", "Authorization", "Bearer eyJsecret.token.value")
		if w.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", client, w.Code, w.Body.String())
		}
		body := w.Body.String()
		for _, forbidden := range []string{"evil.example", "operator-upstream-token", "eyJsecret", "Bearer eyJ"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s output contains %q", client, forbidden)
			}
		}
		if !strings.Contains(body, "$AIPLEX_TOKEN") && !strings.Contains(body, "AIPLEX_TOKEN") {
			t.Errorf("%s output has no token placeholder", client)
		}
	}
	f.h.PublicURL = ""
	if w := f.get("/api/v1/overview/access-points/ide/connect"); w.Code != http.StatusConflict {
		t.Fatalf("without a public URL = %d", w.Code)
	}
}

func TestOverviewGenericSnippetsPerProtocol(t *testing.T) {
	f := newOverviewFixture(t)
	out := f.json("/api/v1/overview/access-points/ide/connect")
	titles := []string{}
	for _, s := range out["snippets"].([]any) {
		titles = append(titles, s.(map[string]any)["title"].(string))
	}
	joined := strings.Join(titles, "|")
	for _, want := range []string{"Environment variables", "curl: OpenAI chat completions", "curl: Anthropic messages", "curl: Google GenAI generateContent", "MCP client configuration"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing snippet %q in %v", want, titles)
		}
	}
	if strings.Contains(joined, "OpenAI responses") {
		t.Errorf("responses snippet offered with no Responses model: %v", titles)
	}
	_ = modelapi.Surface{}
	_ = clientplex.Kind
}

func TestOverviewSubjectIDQueryForm(t *testing.T) {
	f := newOverviewFixture(t)
	out := f.json("/api/v1/overview/subjects/agent?id=spiffe://x/tutor")
	if out["id"] != "tutor" {
		t.Fatalf("id query form = %v", out["id"])
	}
	if w := f.get("/api/v1/overview/subjects/agent?id=spiffe://x/nobody"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown agent = %d", w.Code)
	}
}

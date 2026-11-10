package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

type budgetAPIFixture struct {
	*apFixture
	api     http.Handler
	records func() []usageledger.Record
}

// budgetAPINew wires a quota host that meters access-point tool calls per
// request and mounts /api/v1/budgets next to the access point endpoints.
func budgetAPINew(t *testing.T, withQuota bool) *budgetAPIFixture {
	t.Helper()
	f := newAPFixture(t)
	b := &budgetAPIFixture{apFixture: f}
	b.records = attachLedger(t, f.h)
	if withQuota {
		f.h.Quota = budgetTestHost(t)
		f.h.AttachBudgets()
	}
	access := f.access
	f.h.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) { return access, nil }
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), keyRequestID, "request-1")
			if f.identity != nil {
				ctx = context.WithValue(ctx, keyWIFIdentity, f.identity)
				ctx = context.WithValue(ctx, keyWIFAccess, f.access)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	router.Route("/api/v1", func(r chi.Router) { f.h.MountBudgets(r) })
	b.api = router
	return b
}

// budgetTestHost meters access-point tool calls per request and has money and
// token meters so cost and token budgets can be saved.
func budgetTestHost(t *testing.T) *QuotaHost {
	t.Helper()
	policy := quotaplex.Policy{Version: quotaplex.Version, TenantID: "tenant", ID: "budget-api", Revision: 1, EffectiveAt: apQuotaNow.Add(-time.Hour),
		Meters:   map[string]quotaplex.Meter{"requests": {ID: "requests", Unit: "requests"}, "calls": {ID: "calls", Unit: "calls"}, "usd": {ID: "usd", Unit: "money", Currency: "USD", Scale: 6}, "tokens": {ID: "tokens", Unit: "tokens"}},
		Accounts: []quotaplex.Account{{ID: "team", TenantWide: true}}, Concurrency: []quotaplex.ConcurrencyRule{{ID: "tenant", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 50}}}
	config := QuotaHostConfig{Tenants: []QuotaTenantConfig{{Policy: policy, Activate: true}}, Bindings: []QuotaBinding{{ID: "github-search", TenantID: "tenant", Plane: "mcplex", ResourceID: "github", OperationID: "tools/call:search", Profile: "request-count", FundingAccountID: "team", Provider: "mcp", Meters: map[string]string{"requests": "requests", "tool_calls": "calls"}}}}
	quota, err := NewQuotaHost(context.Background(), config, quotaplex.NewMemoryStore(), func() time.Time { return apQuotaNow }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return quota
}

func (b *budgetAPIFixture) call(method, path, body string) *httptest.ResponseRecorder {
	b.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	b.api.ServeHTTP(w, r)
	return w
}

func (b *budgetAPIFixture) role(roles ...models.IAMRole) { b.access.Roles = roles }

func (b *budgetAPIFixture) verified(subject string, groups ...string) {
	identity := apUser(subject, groups...)
	identity.VerifiedEvidence = &models.IdentityEvidence{Issuer: apIssuer, PoolID: "people", Subject: subject, IssuedAt: apQuotaNow.Add(-time.Minute), ExpiresAt: apQuotaNow.Add(time.Hour), LifetimeValid: true, GroupsPresent: true, GroupsValid: true, Groups: groups}
	b.as(identity, apAllScopes...)
	b.role() // callers are plain members unless a test grants a role
	access := b.access
	b.h.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) { return access, nil }
}

func apGroup(id string) string {
	return `{"source":"` + auth.MembershipSource(apIssuer, "people") + `","id":"` + id + `"}`
}

func budgetJSON(id, parent, name, unit, amount string, extra string) string {
	cur := ""
	if unit == "cost" {
		cur = `"currency":"USD",`
	}
	p := ""
	if parent != "" {
		p = `"parent":"` + parent + `",`
	}
	return `{"id":"` + id + `",` + p + `"display_name":"` + name + `","limit":{"unit":"` + unit + `",` + cur + `"amount":"` + amount + `"},"window":{"kind":"calendar","unit":"month","time_zone":"UTC"}` + extra + `}`
}

func TestBudgetCRUDValidationAndRoutes(t *testing.T) {
	b := budgetAPINew(t, false)
	// Without QuotaPlex a budget would look active and enforce nothing.
	if w := b.call("PUT", "/api/v1/budgets/org", budgetJSON("org", "", "Org", "requests", "100", "")); w.Code != 409 || !strings.Contains(w.Body.String(), "QUOTAPLEX_REQUIRED") {
		t.Fatalf("saving without an enforcer: %d %s", w.Code, w.Body.String())
	}
	b = budgetAPINew(t, true)
	w := b.call("PUT", "/api/v1/budgets/org", budgetJSON("org", "", "Org", "cost", "1000.00", ""))
	if w.Code != 200 || w.Header().Get("ETag") != `"1"` {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	for name, c := range map[string]struct{ id, body, field string }{
		"unknown field":    {"x1", `{"id":"x1","limit":{"unit":"requests","amount":"1"},"window":{"kind":"calendar","unit":"day","time_zone":"UTC"},"oops":1}`, ""},
		"cross currency":   {"x2", strings.Replace(budgetJSON("x2", "org", "X", "cost", "1.00", ""), `"USD"`, `"EUR"`, 1), "limit/currency"},
		"missing parent":   {"x3", budgetJSON("x3", "ghost", "X", "requests", "5", ""), "parent"},
		"rolling":          {"x4", strings.Replace(budgetJSON("x4", "", "X", "requests", "5", ""), `"calendar","unit":"month","time_zone":"UTC"`, `"rolling","hours":24`, 1), "window/kind"},
		"fractional":       {"x5", budgetJSON("x5", "", "X", "tokens", "1.5", ""), "limit/amount"},
		"finer than meter": {"x6", budgetJSON("x6", "", "X", "cost", "1.0000001", ""), "limit/amount"},
		"no meter":         {"x7", strings.Replace(budgetJSON("x7", "", "X", "cost", "1.00", ""), `"USD"`, `"JPY"`, 1), "limit/currency"},
		"unknown client":   {"x8", budgetJSON("x8", "", "X", "requests", "5", `,"members":{"client_registrations":["nope"]}`), "members/client_registrations/0"},
		"unknown ap":       {"x9", budgetJSON("x9", "", "X", "requests", "5", `,"members":{"access_points":["nope"]}`), "members/access_points/0"},
	} {
		w := b.call("PUT", "/api/v1/budgets/"+c.id, c.body)
		if w.Code != 400 {
			t.Errorf("%s accepted: %d %s", name, w.Code, w.Body.String())
			continue
		}
		if c.field != "" && !strings.Contains(w.Body.String(), `"field":"`+c.field+`"`) {
			t.Errorf("%s: error does not name %s: %s", name, c.field, w.Body.String())
		}
	}
	// A child with a matching currency, then update with a revision.
	if w = b.call("PUT", "/api/v1/budgets/team", budgetJSON("team", "org", "Team", "cost", "10.00", `,"members":{"groups":[`+apGroup("platform-eng")+`]}`)); w.Code != 200 {
		t.Fatalf("child: %d %s", w.Code, w.Body.String())
	}
	if w = b.call("DELETE", "/api/v1/budgets/org", ""); w.Code != 428 {
		t.Fatalf("delete needs a revision: %d", w.Code)
	}
	r := httptest.NewRequest("DELETE", "/api/v1/budgets/org", nil)
	r.Header.Set("If-Match", `"1"`)
	rec := httptest.NewRecorder()
	b.api.ServeHTTP(rec, r)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "BUDGET_HAS_CHILDREN") {
		t.Fatalf("parents with children are not deleted: %d %s", rec.Code, rec.Body.String())
	}
	// Reads: operators only; deployers read, cannot write or preview.
	if w = b.call("GET", "/api/v1/budgets", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"team"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	b.role(models.RoleDeployer)
	if w = b.call("GET", "/api/v1/budgets/team", ""); w.Code != 200 {
		t.Fatalf("deployer read: %d", w.Code)
	}
	if w = b.call("PUT", "/api/v1/budgets/team", budgetJSON("team", "org", "Team", "cost", "10.00", "")); w.Code != 403 {
		t.Fatalf("deployer write: %d", w.Code)
	}
	if w = b.call("POST", "/api/v1/budgets/preview", `{}`); w.Code != 403 {
		t.Fatalf("deployer preview: %d", w.Code)
	}
	b.role()
	if w = b.call("GET", "/api/v1/budgets", ""); w.Code != 403 {
		t.Fatalf("plain callers do not list budgets: %d", w.Code)
	}
}

func TestBudgetStatusScopesWhatMembersAndOperatorsSee(t *testing.T) {
	b := budgetAPINew(t, true)
	for _, c := range [][2]string{
		{"org", budgetJSON("org", "", "Organisation", "requests", "20", "")},
		{"team", budgetJSON("team", "org", "Platform", "requests", "5", `,"members":{"groups":[`+apGroup("platform-eng")+`]},"thresholds":[20,100]`)},
		{"solo", budgetJSON("solo", "org", "Solo", "requests", "5", `,"members":{"principals":["someone-else"]}`)},
	} {
		if w := b.call("PUT", "/api/v1/budgets/"+c[0], c[1]); w.Code != 200 {
			t.Fatalf("%s: %d %s", c[0], w.Code, w.Body.String())
		}
	}
	// A member makes two real calls through the access point.
	b.verified("alice", "platform-eng")
	b.role()
	for i := 0; i < 2; i++ {
		if w := mcpPost(b.apFixture, "/ap/ide/mcp", apSearchCall); !strings.Contains(w.Body.String(), "github:search") {
			t.Fatalf("call %d: %s", i, w.Body.String())
		}
	}
	// Member view: own budget, spent and remaining only.
	w := b.call("GET", "/api/v1/budgets/team/status", "")
	var member BudgetStatus
	if err := json.Unmarshal(w.Body.Bytes(), &member); err != nil || w.Code != 200 || member.Spent != "2" || member.Remaining != "3" || member.Limit != "5" || member.UnknownCostRequests != nil || len(member.Children) != 0 || len(member.TopSpenders) != 0 || len(member.ThresholdEvents) != 0 {
		t.Fatalf("member status: %d %s", w.Code, w.Body.String())
	}
	// The parent is reached only through the child: not the member's budget.
	for _, id := range []string{"org", "solo", "missing"} {
		if w = b.call("GET", "/api/v1/budgets/"+id+"/status", ""); w.Code != 404 {
			t.Fatalf("%s is not the caller's budget: %d %s", id, w.Code, w.Body.String())
		}
	}
	// A caller whose group view is unknown is not a member.
	identity := apUser("alice", "platform-eng")
	identity.VerifiedEvidence = &models.IdentityEvidence{Issuer: apIssuer, PoolID: "people", Subject: "alice", LifetimeValid: true, GroupsPresent: false}
	b.as(identity)
	b.role()
	if w = b.call("GET", "/api/v1/budgets/team/status", ""); w.Code != 404 {
		t.Fatalf("unknown groups must not grant membership: %d", w.Code)
	}
	// Another user without the group.
	b.verified("bob", "sales")
	if w = b.call("GET", "/api/v1/budgets/team/status", ""); w.Code != 404 {
		t.Fatalf("non-member: %d", w.Code)
	}
	// Operators: everything, including children and threshold events.
	b.verified("root", "admins")
	b.role(models.RoleAdmin)
	w = b.call("GET", "/api/v1/budgets/org/status", "")
	var op BudgetStatus
	if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil || w.Code != 200 || op.Spent != "2" || len(op.Children) != 2 || op.UnknownCostRequests == nil {
		t.Fatalf("operator status: %d %s", w.Code, w.Body.String())
	}
	w = b.call("GET", "/api/v1/budgets/team/status", "")
	if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil || len(op.ThresholdEvents) != 1 || op.ThresholdEvents[0].Percent != 20 || op.ThresholdEvents[0].Consumed != "1" {
		t.Fatalf("a crossed threshold is recorded once: %s", w.Body.String())
	}
	if len(op.TopSpenders) != 1 || op.TopSpenders[0].Requests != 2 || !strings.Contains(op.TopSpenders[0].PrincipalID, "alice") && op.TopSpenders[0].Display == "" {
		t.Fatalf("top spenders come from the ledger: %s", w.Body.String())
	}
	// The ledger recorded the leaf and the ancestor.
	records := b.records()
	if len(records) != 2 || strings.Join(records[0].BudgetIDs, ",") != "org,team" {
		t.Fatalf("ledger budget ids: %+v", records)
	}
	// Deployers read the status but not usage-derived spenders.
	b.role(models.RoleDeployer)
	w = b.call("GET", "/api/v1/budgets/team/status", "")
	var dep BudgetStatus
	if err := json.Unmarshal(w.Body.Bytes(), &dep); err != nil || len(dep.TopSpenders) != 0 || dep.UnknownCostRequests == nil {
		t.Fatalf("deployer status: %s", w.Body.String())
	}
}

func TestBudgetSummaryByBudgetAgreesWithTheCounters(t *testing.T) {
	b := budgetAPINew(t, true)
	b.call("PUT", "/api/v1/budgets/org", budgetJSON("org", "", "Org", "requests", "50", ""))
	b.call("PUT", "/api/v1/budgets/team", budgetJSON("team", "org", "Team", "requests", "50", `,"members":{"groups":[`+apGroup("platform-eng")+`]}`))
	b.verified("alice", "platform-eng")
	b.role()
	for i := 0; i < 3; i++ {
		mcpPost(b.apFixture, "/ap/ide/mcp", apSearchCall)
	}
	store := b.h.Ledger.Store()
	b.h.Ledger.Flush()
	summary, err := usageledger.Summarize(context.Background(), store, "tenant", usageledger.SummaryQuery{GroupBy: []string{"budget"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, g := range summary.Groups {
		got[g.Key["budget"]] = g.Requests
	}
	if got["org"] != 3 || got["team"] != 3 {
		t.Fatalf("each budget counts the records that drew from it: %v", got)
	}
	b.verified("root", "admins")
	b.role(models.RoleAdmin)
	var team BudgetStatus
	json.Unmarshal(b.call("GET", "/api/v1/budgets/team/status", "").Body.Bytes(), &team)
	if team.Spent != "3" {
		t.Fatalf("counter and ledger disagree: %+v", team)
	}
}

func TestBudgetProjectLabelsComeOnlyFromAccessPointConfiguration(t *testing.T) {
	b := budgetAPINew(t, true)
	if w := b.call("PUT", "/api/v1/budgets/apollo", budgetJSON("apollo", "", "Apollo", "requests", "10", `,"members":{"projects":["apollo"]}`)); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	b.verified("alice", "platform-eng")
	b.role()
	// The access point has no project yet; a header cannot supply one.
	mcpPost(b.apFixture, "/ap/ide/mcp", apSearchCall, "X-Aiplex-Project", "apollo", "X-Project", "apollo")
	b.role(models.RoleAdmin)
	var st BudgetStatus
	json.Unmarshal(b.call("GET", "/api/v1/budgets/apollo/status", "").Body.Bytes(), &st)
	if st.Spent != "0" {
		t.Fatalf("a caller-supplied project header drew from a budget: %+v", st)
	}
	spec := apSpec()
	spec.Project = "apollo"
	setSpec(b.apFixture, spec)
	b.verified("alice", "platform-eng")
	b.role()
	mcpPost(b.apFixture, "/ap/ide/mcp", apSearchCall)
	b.role(models.RoleAdmin)
	json.Unmarshal(b.call("GET", "/api/v1/budgets/apollo/status", "").Body.Bytes(), &st)
	if st.Spent != "1" {
		t.Fatalf("the access point's configured project must select the budget: %+v", st)
	}
	spec.Project = "bad label"
	raw, _ := json.Marshal(spec)
	if err := b.h.validate(context.Background(), "tenant", AccessPointKind, "ide", raw); err == nil {
		t.Fatal("an invalid project label was accepted")
	}
}

func TestBudgetPreviewExplainsDrawsAndValidatesADraft(t *testing.T) {
	b := budgetAPINew(t, true)
	b.call("PUT", "/api/v1/budgets/org", budgetJSON("org", "", "Org", "requests", "50", ""))
	b.call("PUT", "/api/v1/budgets/team", budgetJSON("team", "org", "Team", "requests", "20", `,"members":{"groups":[`+apGroup("platform-eng")+`]}`))
	b.call("PUT", "/api/v1/budgets/spend", budgetJSON("spend", "", "Spend", "tokens", "1000", `,"members":{"groups":[`+apGroup("platform-eng")+`]}`))
	preview := func(body string) BudgetPreview {
		w := b.call("POST", "/api/v1/budgets/preview", body)
		var out BudgetPreview
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
			t.Fatalf("preview: %d %s", w.Code, w.Body.String())
		}
		return out
	}
	out := preview(`{"subject":{"principal":"x","groups":[` + apGroup("platform-eng") + `]},"plane":"llmplex"}`)
	ids := func(p BudgetPreview) string {
		var s []string
		for _, d := range p.Draws {
			s = append(s, d.ID)
		}
		return strings.Join(s, ",")
	}
	if !out.Hypothetical || ids(out) != "team,org,spend" || !out.Draws[0].Direct || out.Draws[1].Direct || out.Draws[1].ViaChild != "team" || out.Draws[0].Reasons[0] != "group:platform-eng" {
		t.Fatalf("%+v", out)
	}
	// Token budgets count model traffic only.
	out = preview(`{"subject":{"groups":[` + apGroup("platform-eng") + `]},"plane":"mcplex"}`)
	if ids(out) != "team,org" || len(out.Skipped) != 1 || out.Skipped[0].ID != "spend" {
		t.Fatalf("%+v", out)
	}
	if out = preview(`{"subject":{"principal":"stranger"}}`); len(out.Draws) != 0 {
		t.Fatalf("%+v", out)
	}
	// A draft is validated like a save and shown in place, never stored.
	out = preview(`{"subject":{"principal":"stranger"},"draft":` + budgetJSON("late", "org", "Late", "requests", "5", `,"members":{"principals":["stranger"]}`) + `}`)
	if out.Draft == nil || !out.Draft.Valid || ids(out) != "late,org" || !out.Draws[0].Draft {
		t.Fatalf("%+v", out)
	}
	if w := b.call("GET", "/api/v1/budgets/late", ""); w.Code != 404 {
		t.Fatalf("a preview stored its draft: %d", w.Code)
	}
	out = preview(`{"subject":{"principal":"stranger"},"draft":` + strings.Replace(budgetJSON("bad", "org", "Bad", "cost", "5", ""), `"USD"`, `"EUR"`, 1) + `}`)
	if out.Draft == nil || out.Draft.Valid || out.Draft.Field != "limit/currency" {
		t.Fatalf("%+v", out.Draft)
	}
	if w := b.call("POST", "/api/v1/budgets/preview", `{"surprise":1}`); w.Code != 400 {
		t.Fatalf("unknown preview fields: %d", w.Code)
	}
}

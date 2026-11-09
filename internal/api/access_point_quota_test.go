package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

var apQuotaNow = time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)

// withAPQuota protects github tools/call:search with a request-count binding
// and the given extra controls, and gives the caller verified membership.
func withAPQuota(t *testing.T, f *apFixture, concurrency []quotaplex.ConcurrencyRule) *QuotaHost {
	t.Helper()
	policy := quotaplex.Policy{Version: quotaplex.Version, TenantID: "tenant", ID: "tool-policy", Revision: 1, EffectiveAt: apQuotaNow.Add(-time.Hour), Meters: map[string]quotaplex.Meter{"requests": {ID: "requests", Unit: "requests"}, "calls": {ID: "calls", Unit: "calls"}}, Accounts: []quotaplex.Account{{ID: "team", TenantWide: true}}, Budgets: []quotaplex.BudgetRule{{ID: "team-calls", Scope: quotaplex.Scope{Kind: "account", ID: "team"}, MeterID: "calls", Limit: 100, Period: quotaplex.Period{Kind: "fixed", Start: apQuotaNow.Add(-time.Hour), End: apQuotaNow.Add(time.Hour)}}}, Concurrency: concurrency}
	config := QuotaHostConfig{Tenants: []QuotaTenantConfig{{Policy: policy, Activate: true}}, Bindings: []QuotaBinding{{ID: "github-search", TenantID: "tenant", Plane: "mcplex", ResourceID: "github", OperationID: "tools/call:search", Profile: "request-count", FundingAccountID: "team", Provider: "mcp", Meters: map[string]string{"requests": "requests", "tool_calls": "calls"}}}}
	quota, err := NewQuotaHost(context.Background(), config, quotaplex.NewMemoryStore(), func() time.Time { return apQuotaNow }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.h.Quota = quota
	identity := apUser("alice")
	identity.VerifiedEvidence = &models.IdentityEvidence{Issuer: apIssuer, PoolID: "people", Subject: "alice", IssuedAt: apQuotaNow.Add(-time.Minute), ExpiresAt: apQuotaNow.Add(time.Hour), LifetimeValid: true, GroupsPresent: true, GroupsValid: true, Groups: []string{}}
	f.as(identity, apAllScopes...)
	access := f.access
	f.h.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) { return access, nil }
	return quota
}

func setQoS(f *apFixture, qos *AccessPointQoS) {
	spec := apSpec()
	spec.QoS = qos
	record, _ := f.store.Get(context.Background(), "tenant", AccessPointKind, "ide")
	raw, _ := json.Marshal(spec)
	if _, err := f.store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: AccessPointKind, ID: "ide", Spec: raw}, record.Revision); err != nil {
		f.t.Fatal(err)
	}
}

const apSearchCall = `{"jsonrpc":"2.0","id":"q","method":"tools/call","params":{"name":"github__search"}}`

func TestAccessPointQoSFailsClosedWithoutMatchingQuotaControl(t *testing.T) {
	f := newAPFixture(t)
	setQoS(f, &AccessPointQoS{Class: "interactive", Limits: &AccessPointLimit{Concurrency: 2}})
	// No quota host at all.
	w := mcpPost(f, "/ap/ide/mcp", apSearchCall)
	if !strings.Contains(w.Body.String(), "quota_unavailable") || len(f.upstreamCalls()) != 0 {
		t.Fatalf("QoS without quota did not fail closed: %s", w.Body.String())
	}
	if w = f.do("POST", "/ap/ide/v1/chat/completions", `{"model":"chat-model"}`); w.Code != 503 || len(f.upstreamCalls()) != 0 {
		t.Fatalf("model QoS without quota: %d %s", w.Code, w.Body.String())
	}
	// A quota host whose policy has no control for this access point.
	withAPQuota(t, f, []quotaplex.ConcurrencyRule{{ID: "tenant", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 5}})
	w = mcpPost(f, "/ap/ide/mcp", apSearchCall)
	if !strings.Contains(w.Body.String(), "no active quota control matches") || len(f.upstreamCalls()) != 0 {
		t.Fatalf("uncovered QoS did not fail closed: %s", w.Body.String())
	}
	// An operation without a quota binding also fails closed (existing rule).
	w = mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":"q","method":"tools/call","params":{"name":"github__delete"}}`)
	if !strings.Contains(w.Body.String(), "no protected quota binding") || len(f.upstreamCalls()) != 0 {
		t.Fatalf("unbound operation dispatched: %s", w.Body.String())
	}
}

func TestAccessPointQuotaDimensionsAdmitAndReachTheLedger(t *testing.T) {
	f := newAPFixture(t)
	setQoS(f, &AccessPointQoS{Class: "interactive", Limits: &AccessPointLimit{Concurrency: 2}})
	quota := withAPQuota(t, f, []quotaplex.ConcurrencyRule{{ID: "ap-ide-users", Scope: quotaplex.Scope{Kind: "principal"}, Match: quotaplex.Match{AccessPointID: "ide", CallerKind: CallerUser}, Limit: 2}})
	records := attachLedger(t, f.h)
	w := mcpPost(f, "/ap/ide/mcp", apSearchCall)
	attempt := w.Header().Get("X-Aiplex-Attempt-ID")
	if !strings.Contains(w.Body.String(), "github:search") || attempt == "" {
		t.Fatalf("covered call failed: %s", w.Body.String())
	}
	info, err := quota.Engines()["tenant"].GetAttempt(context.Background(), "tenant", attempt)
	if err != nil || info.Demand.Target.AccessPointID != "ide" || info.Demand.Target.CallerKind != CallerUser || info.Decision.State != "settled" || len(info.Plan.Concurrency) != 1 {
		t.Fatalf("quota target lacks access-point dimensions: %+v %v", info, err)
	}
	// The same operation called directly carries no access-point dimension
	// and so does not match the access-point control.
	direct := httptestToolRequest(f, `{"jsonrpc":"2.0","id":"d","method":"tools/call","params":{"name":"search"}}`)
	rec := httptest.NewRecorder()
	f.h.NativeTool(rec, direct)
	directInfo, err := quota.Engines()["tenant"].GetAttempt(context.Background(), "tenant", rec.Header().Get("X-Aiplex-Attempt-ID"))
	if rec.Code != 200 || err != nil || directInfo.Demand.Target.AccessPointID != "" || len(directInfo.Plan.Concurrency) != 0 || len(directInfo.Plan.Budgets) != 1 {
		t.Fatalf("direct call matched the access-point control: %d %s %+v %v", rec.Code, rec.Body.String(), directInfo.Plan, err)
	}
	var viaAP, directRecord *usageledger.Record
	for _, r := range records() {
		r := r
		if r.AccessPointID != "" {
			viaAP = &r
		} else {
			directRecord = &r
		}
	}
	if viaAP == nil || viaAP.AccessPointID != "ide" || viaAP.CallerKind != CallerUser || viaAP.PrincipalID == "" || viaAP.ToolName != "search" {
		t.Fatalf("ledger record lacks access-point fields: %+v", viaAP)
	}
	if directRecord == nil || directRecord.CallerKind != "" {
		t.Fatalf("direct record carries access-point fields: %+v", directRecord)
	}
}

func TestAccessPointWithoutQoSBehavesLikeManagedPath(t *testing.T) {
	f := newAPFixture(t)
	withAPQuota(t, f, []quotaplex.ConcurrencyRule{{ID: "tenant", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 5}})
	w := mcpPost(f, "/ap/ide/mcp", apSearchCall)
	if !strings.Contains(w.Body.String(), "github:search") {
		t.Fatalf("access point without QoS diverged from the managed path: %s", w.Body.String())
	}
}

func TestAccessPointQuotaPlanRendersValidatedFragmentWithoutApplying(t *testing.T) {
	f := newAPFixture(t)
	if w := f.do("POST", "/api/v1/access-points/ide/quota-plan", ""); w.Code != 400 {
		t.Fatalf("plan without qos: %d", w.Code)
	}
	setQoS(f, &AccessPointQoS{Class: "interactive", Weight: 4, Limits: &AccessPointLimit{RequestsPerMinute: 120, TokensPerMinute: 400000, Concurrency: 8}})
	f.access.Roles = []models.IAMRole{models.RoleDeployer}
	if w := f.do("POST", "/api/v1/access-points/ide/quota-plan", ""); w.Code != 403 {
		t.Fatalf("non-admin plan: %d", w.Code)
	}
	f.access.Roles = []models.IAMRole{models.RoleAdmin}
	w := f.do("POST", "/api/v1/access-points/ide/quota-plan", "")
	var plan AccessPointQuotaPlan
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil || w.Code != 200 {
		t.Fatalf("plan: %d %s", w.Code, w.Body.String())
	}
	if plan.Applied || !plan.Validation.Valid || plan.Validation.Validator != "compile" || len(plan.Fragment.Rates) != 2 || len(plan.Fragment.Concurrency) != 1 || plan.Scheduler == nil || plan.Scheduler.Weight != 4 || plan.Scheduler.FlowID != "ap-ide-interactive" {
		t.Fatalf("plan content: %s", w.Body.String())
	}
	for _, rule := range plan.Fragment.Rates {
		if rule.Match.AccessPointID != "ide" || rule.Interval != time.Minute || rule.Basis != "admitted" {
			t.Fatalf("rate rule: %+v", rule)
		}
	}
	foundRoute := false
	for _, route := range plan.Scheduler.Routes {
		if route.ResourceID == "github" && route.OperationID == "tools/call:search" && route.AccessPointID == "ide" {
			foundRoute = true
		}
		if route.OperationID == "tools/call:other" {
			t.Fatal("scheduler route for an unexposed tool")
		}
	}
	if !foundRoute {
		t.Fatalf("scheduler routes: %+v", plan.Scheduler.Routes)
	}
	compiled, err := quotaplex.Compile(plan.Policy)
	if err != nil {
		t.Fatal("rendered policy rejected by the validator", err)
	}
	if _, err = compiled.Resolve(quotaplex.Demand{}, apQuotaNow); err == nil {
		t.Fatal("empty demand resolved")
	}

	// Against an active tenant policy the engine validator is used and the
	// active policy is not changed.
	quota := withAPQuota(t, f, []quotaplex.ConcurrencyRule{{ID: "tenant", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 5}})
	f.access.Roles = []models.IAMRole{models.RoleAdmin}
	before, _ := quota.Engines()["tenant"].InspectPolicy(context.Background(), "tenant")
	w = f.do("POST", "/api/v1/access-points/ide/quota-plan", "")
	plan = AccessPointQuotaPlan{}
	_ = json.Unmarshal(w.Body.Bytes(), &plan)
	after, _ := quota.Engines()["tenant"].InspectPolicy(context.Background(), "tenant")
	if w.Code != 200 || !plan.Validation.Valid || plan.Validation.Validator != "engine" || plan.Policy.Revision != 2 || before.Active != after.Active || len(plan.Policy.Budgets) != 1 {
		t.Fatalf("engine plan: %d %s", w.Code, w.Body.String())
	}
}

func TestAccessPointSkillsManifestIsTenantBoundAndScoped(t *testing.T) {
	f := newAPFixture(t)
	skills := registry.NewMemoryStore()
	f.h.AccessPoint.Skills = skills
	ctx := context.Background()
	_ = skills.PutTemplate(ctx, &models.Template{ID: "runbook-template", Plane: models.PlaneSkillsPlex, Name: "Runbooks", Skills: []models.SkillInfo{{Name: "deploy"}, {Name: "rollback"}}})
	_ = skills.PutInstance(ctx, &models.Instance{ID: "runbooks", Plane: models.PlaneSkillsPlex, TemplateID: "runbook-template", Status: models.StatusRunning, Labels: map[string]string{SkillTenantLabel: "tenant"}})
	_ = skills.PutInstance(ctx, &models.Instance{ID: "foreign", Plane: models.PlaneSkillsPlex, TemplateID: "runbook-template", Status: models.StatusRunning, Labels: map[string]string{SkillTenantLabel: "other"}})
	spec := apSpec()
	spec.Skills = []AccessPointSkill{{InstanceRef: "foreign"}}
	raw, _ := json.Marshal(spec)
	if err := f.h.validate(ctx, "tenant", AccessPointKind, "ide", raw); err == nil {
		t.Fatal("another tenant's skill server accepted")
	}
	spec.Skills = []AccessPointSkill{{InstanceRef: "runbooks"}}
	setSpec(f, spec)
	f.as(apUser("alice"), "skill:invoke:deploy")
	w := f.do("GET", "/ap/ide/skills", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"deploy"`) || strings.Contains(w.Body.String(), "rollback") || !strings.Contains(w.Body.String(), `"instance_id":"runbooks"`) {
		t.Fatalf("skills manifest: %d %s", w.Code, w.Body.String())
	}
}

func setSpec(f *apFixture, spec AccessPoint) {
	record, _ := f.store.Get(context.Background(), "tenant", AccessPointKind, spec.ID)
	raw, _ := json.Marshal(spec)
	if _, err := f.store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: AccessPointKind, ID: spec.ID, Spec: raw}, record.Revision); err != nil {
		f.t.Fatal(err)
	}
}

func httptestToolRequest(f *apFixture, body string) *http.Request {
	r := trustedConnectedRequest("/api/v1/mcp/connections/github/invoke", body, nil)
	r.SetPathValue("id", "github")
	ctx := context.WithValue(r.Context(), keyWIFIdentity, f.identity)
	ctx = context.WithValue(ctx, keyWIFAccess, f.access)
	r = r.WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	return r
}

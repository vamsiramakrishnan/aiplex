package main

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// The runs routes decide which tenant's runs a caller sees from the verified
// identity only. These tests drive the production router (newServer) and walk
// every mounted /api/v1/runs route, so a runs route added later without the
// tenant filter fails here.

const (
	runsTenantA = authzHomeTenant // the deployment's own tenant
	runsTenantB = "tenant-b"
	runsTenantC = "tenant-c" // a tenant with no runs
	runA        = "run-of-tenant-a"
	runB        = "run-of-tenant-b"
)

// Callers, as test tokens. Each names a group the test binds below; "@" adds
// the workforce domain (hd) claim.
const (
	rtNone   = "group.rt-none"            // runs grants, no tenant
	rtA      = "group.rt-a"               // tenant A by scope
	rtATwice = "group.rt-a-twice"         // tenant A, granted twice
	rtAB     = "group.rt-ab"              // tenants A and B by scope
	rtCross  = "group.rt-cross"           // the cross-tenant scope, no tenant
	rtDomA   = "group.rt-dom-a@a.example" // tenant A through the domain map
	rtDomB   = "group.rt-dom-b@b.example" // tenant B through the domain map
	rtDomC   = "group.rt-dom-c@c.example" // tenant C (no runs) through the domain map
	rtDomAmb = "group.rt-amb@amb.example" // a domain mapped to two tenants
)

const runsDomainMap = "a.example=" + runsTenantA + ",a.example=" + runsTenantA +
	",b.example=" + runsTenantB + ",c.example=" + runsTenantC +
	",amb.example=" + runsTenantA + ",amb.example=" + runsTenantB

// requireTenantSettings are the operator settings the matrix runs under.
// "unset" removes the variable for the test: the default must hold with no
// configuration at all.
var requireTenantSettings = []string{"unset", "", "1", "true", "yes", "0"}

func setRequireTenant(t *testing.T, setting string) {
	t.Helper()
	t.Setenv(api.RequireTenantEnv, "") // restores the original value afterwards
	if setting == "unset" {
		if err := os.Unsetenv(api.RequireTenantEnv); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Setenv(api.RequireTenantEnv, setting)
}

func newRunsTenantServer(t *testing.T, mutate ...func(*config.Config)) *authzServer {
	t.Helper()
	t.Setenv("AIPLEX_DOMAIN_TENANT_MAP", runsDomainMap)
	s := newAuthzServer(t, mutate...)
	ctx := context.Background()
	runs := []string{"aiplex:runs:*"}
	for _, b := range []*models.RoleBinding{
		{ID: "rt-none", Group: "rt-none", Role: models.RoleDeployer, Scopes: runs},
		{ID: "rt-a", Group: "rt-a", Role: models.RoleDeployer, Scopes: append([]string{"aiplex:tenant:" + runsTenantA}, runs...)},
		{ID: "rt-a-twice-1", Group: "rt-a-twice", Role: models.RoleDeployer, Scopes: append([]string{"aiplex:tenant:" + runsTenantA, "aiplex:tenant:" + runsTenantA}, runs...)},
		{ID: "rt-a-twice-2", Group: "rt-a-twice", Role: models.RoleDeployer, Scopes: []string{"aiplex:tenant:" + runsTenantA}},
		{ID: "rt-ab", Group: "rt-ab", Role: models.RoleDeployer, Scopes: append([]string{"aiplex:tenant:" + runsTenantA, "aiplex:tenant:" + runsTenantB}, runs...)},
		{ID: "rt-cross", Group: "rt-cross", Role: models.RoleDeployer, Scopes: append([]string{"aiplex:runs:read:cross_tenant"}, runs...)},
		{ID: "rt-dom-a", Group: "rt-dom-a", Role: models.RoleDeployer, Scopes: runs},
		{ID: "rt-dom-b", Group: "rt-dom-b", Role: models.RoleDeployer, Scopes: runs},
		{ID: "rt-dom-c", Group: "rt-dom-c", Role: models.RoleDeployer, Scopes: runs},
		{ID: "rt-amb", Group: "rt-amb", Role: models.RoleDeployer, Scopes: runs},
	} {
		if err := s.store.PutRoleBinding(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	for _, run := range []models.ExecutionRun{
		{RunID: runA, TenantID: runsTenantA, AgentID: "agent", Status: models.ExecutionRunRunnable},
		{RunID: runB, TenantID: runsTenantB, AgentID: "agent", Status: models.ExecutionRunRunnable},
	} {
		run := run
		if err := s.store.UpsertExecutionRun(ctx, &run); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// runsRoute is one mounted runs route, made concrete.
type runsRoute struct {
	method, pattern string
	perRun          bool // carries a run id
}

func (r runsRoute) path(runID string) string {
	if r.pattern == "/events/stream" {
		return "/events/stream?run_id=" + runID
	}
	return strings.ReplaceAll(r.pattern, "{run_id}", runID)
}

// mountedRunsRoutes walks the router for every /api/v1/runs route and adds
// the event stream's per-run mode, which serves a run's journal. A runs route
// this test cannot classify fails it.
func mountedRunsRoutes(t *testing.T, s *authzServer) []runsRoute {
	t.Helper()
	var out []runsRoute
	for _, r := range walkRoutes(t, s.handler) {
		if r.Pattern != "/api/v1/runs" && !strings.HasPrefix(r.Pattern, "/api/v1/runs/") {
			continue
		}
		if r.Method == http.MethodHead || r.Method == http.MethodOptions || r.Method == http.MethodConnect || r.Method == http.MethodTrace {
			continue
		}
		switch {
		case strings.Contains(r.Pattern, "{run_id}"):
			out = append(out, runsRoute{r.Method, r.Pattern, true})
		case r.Pattern == "/api/v1/runs" && r.Method == http.MethodGet, r.Pattern == "/api/v1/runs/_health" && r.Method == http.MethodGet:
			out = append(out, runsRoute{r.Method, r.Pattern, false})
		default:
			t.Fatalf("unclassified runs route %s %s: give it the tenant filter (runsTenant) and add it to this test", r.Method, r.Pattern)
		}
	}
	out = append(out, runsRoute{http.MethodGet, "/events/stream", true})
	sort.Slice(out, func(i, j int) bool { return out[i].method+out[i].pattern < out[j].method+out[j].pattern })
	// Guard against a vacuous walk: every route named in the runs guide.
	want := []string{"GET /api/v1/runs", "GET /api/v1/runs/_health", "GET /api/v1/runs/{run_id}", "GET /api/v1/runs/{run_id}/events",
		"GET /api/v1/runs/{run_id}/effects", "GET /api/v1/runs/{run_id}/obligations", "GET /api/v1/runs/{run_id}/budgets",
		"GET /api/v1/runs/{run_id}/operator-audit", "POST /api/v1/runs/{run_id}/redrive", "POST /api/v1/runs/{run_id}/reconcile",
		"POST /api/v1/runs/{run_id}/cancel", "POST /api/v1/runs/{run_id}/signal", "POST /api/v1/runs/{run_id}/compensate",
		"POST /api/v1/runs/{run_id}/compact"}
	have := map[string]bool{}
	for _, r := range out {
		have[r.method+" "+r.pattern] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Fatalf("runs route %s is not mounted", w)
		}
	}
	return out
}

// hostileRunsRequest adds every request input a caller might use to name a
// tenant: headers, the tenant_id query parameter and body fields.
func hostileRunsRequest(method, target, token, tenant string) *http.Request {
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	body := ""
	if method == http.MethodPost {
		body = `{"reason":"test","tenant_id":"` + tenant + `","tenant":"` + tenant + `"}`
	}
	req := authzRequest(method, target+sep+"tenant_id="+tenant+"&tenant="+tenant, token, body)
	for _, h := range []string{"X-Aiplex-Tenant", "X-Aiplex-Tenant-Id", "X-Tenant-Id", "X-Tenant", "X-Goog-Authenticated-User-Domain"} {
		req.Header.Set(h, tenant)
	}
	return req
}

func (s *authzServer) runsCall(t *testing.T, method, target, token string) (int, string, string) {
	t.Helper()
	body := ""
	if method == http.MethodPost {
		body = `{"reason":"test"}`
	}
	rec := s.do(t, method, target, token, body)
	return rec.Code, authzErrorCode(rec.Body.Bytes()), rec.Body.String()
}

// TestRunsTenantRefusals: a tenantless caller is refused under default
// configuration (with no variable set) and every value but "0"; an identity
// naming two tenants is refused under every setting, "0" included.
func TestRunsTenantRefusals(t *testing.T) {
	for _, setting := range requireTenantSettings {
		t.Run("setting="+setting, func(t *testing.T) {
			setRequireTenant(t, setting)
			s := newRunsTenantServer(t)
			for _, route := range mountedRunsRoutes(t, s) {
				target := route.path(runA)
				if route.pattern == "/events/stream" {
					// A served stream does not end; a missing run answers 404
					// once the tenant decision has admitted the caller.
					target = route.path("missing-run")
				}
				if !route.perRun {
					target = route.pattern
				}
				// Two tenant scopes. The route policy refuses identities bound
				// to another tenant on deployment-wide routes first; the runs
				// layer refuses the same identity with TENANT_AMBIGUOUS
				// wherever it is reached. Either way it is never resolved.
				status, code, body := s.runsCall(t, route.method, target, rtAB)
				if status != http.StatusForbidden || (code != "TENANT_AMBIGUOUS" && code != "DEPLOYMENT_SCOPE_REQUIRED") {
					t.Errorf("%s %s as two-tenant identity: %d %s", route.method, target, status, body)
				}
				// A domain mapped to two tenants reaches the runs layer.
				if status, code, body := s.runsCall(t, route.method, target, rtDomAmb); status != http.StatusForbidden || code != "TENANT_AMBIGUOUS" {
					t.Errorf("%s %s as ambiguously mapped domain: %d %s", route.method, target, status, body)
				}
				// No tenant, no cross-tenant scope.
				status, code, body = s.runsCall(t, route.method, target, rtNone)
				if setting == "0" {
					if status == http.StatusForbidden || status == http.StatusUnauthorized {
						t.Errorf("%s %s as tenantless caller with the opt-out: %d %s", route.method, target, status, body)
					}
				} else if status != http.StatusForbidden || code != "TENANT_REQUIRED" {
					t.Errorf("%s %s as tenantless caller (setting %q): %d %s", route.method, target, setting, status, body)
				}
			}
		})
	}
}

// TestRunsTenantIsolationOnEveryRoute: a caller bound to one tenant never
// receives another tenant's run from any runs route, whatever headers, query
// parameters or body fields name the other tenant, and the operator actions
// do not act on it.
func TestRunsTenantIsolationOnEveryRoute(t *testing.T) {
	for _, setting := range []string{"unset", "0"} {
		t.Run("setting="+setting, func(t *testing.T) {
			setRequireTenant(t, setting)
			s := newRunsTenantServer(t)
			routes := mountedRunsRoutes(t, s)
			for _, caller := range []struct {
				token, own, ownRun, other, otherRun string
			}{
				{rtA, runsTenantA, runA, runsTenantB, runB},
				{rtATwice, runsTenantA, runA, runsTenantB, runB},
				{rtDomA, runsTenantA, runA, runsTenantB, runB},
				{rtDomB, runsTenantB, runB, runsTenantA, runA},
			} {
				for _, route := range routes {
					if !route.perRun {
						continue
					}
					rec := s.do2(hostileRunsRequest(route.method, route.path(caller.otherRun), caller.token, caller.other))
					if rec.Code != http.StatusNotFound || authzErrorCode(rec.Body.Bytes()) != "NOT_FOUND" || strings.Contains(rec.Body.String(), "tenant_id") {
						t.Errorf("%s %s as %s: %d %s", route.method, route.path(caller.otherRun), caller.token, rec.Code, rec.Body)
					}
					// Positive control: the caller's own run is served (the
					// stream is skipped; it does not end).
					if route.method == http.MethodGet && route.pattern != "/events/stream" {
						rec = s.do2(hostileRunsRequest(route.method, route.path(caller.ownRun), caller.token, caller.other))
						if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), caller.otherRun) {
							t.Errorf("%s %s (own run) as %s: %d %s", route.method, route.path(caller.ownRun), caller.token, rec.Code, rec.Body)
						}
					}
				}
				list := s.do2(hostileRunsRequest(http.MethodGet, "/api/v1/runs", caller.token, caller.other))
				if list.Code != http.StatusOK || strings.Contains(list.Body.String(), caller.otherRun) || strings.Contains(list.Body.String(), caller.ownRun) {
					t.Errorf("list naming tenant %s as %s: %d %s", caller.other, caller.token, list.Code, list.Body)
				}
				list = s.do2(authzRequest(http.MethodGet, "/api/v1/runs", caller.token, ""))
				if list.Code != http.StatusOK || strings.Contains(list.Body.String(), caller.otherRun) || !strings.Contains(list.Body.String(), caller.ownRun) {
					t.Errorf("list as %s: %d %s", caller.token, list.Code, list.Body)
				}
			}
			ctx := context.Background()
			audit, err := s.store.ListOperatorAudit(ctx, runB, 100)
			if err != nil || len(audit) != 0 {
				t.Fatalf("an operator action reached another tenant's run: %v %+v", err, audit)
			}
			// has_runs is scoped: tenant C has none although others do.
			if status, _, body := s.runsCall(t, "GET", "/api/v1/runs/_health", rtDomC); status != 200 || !strings.Contains(body, `"has_runs":false`) {
				t.Errorf("health for a tenant with no runs: %d %s", status, body)
			}
			// Deployment-wide pipeline values go only to the all-tenants view.
			for _, token := range []string{rtA, rtATwice, rtDomA, rtDomB, rtDomC} {
				status, _, body := s.runsCall(t, "GET", "/api/v1/runs/_health", token)
				if status != 200 || strings.Contains(body, "last_ingest_at") || strings.Contains(body, "tape_instances_count") {
					t.Errorf("health for confined caller %s: %d %s", token, status, body)
				}
			}
			if status, _, body := s.runsCall(t, "GET", "/api/v1/runs/_health", rtA); status != 200 || !strings.Contains(body, `"has_runs":true`) {
				t.Errorf("health for tenant A: %d %s", status, body)
			}
			if status, _, body := s.runsCall(t, "GET", "/api/v1/runs/_health", rtCross); status != 200 || !strings.Contains(body, "last_ingest_at") || !strings.Contains(body, `"tape_instances_count":`) {
				t.Errorf("health for the cross-tenant scope: %d %s", status, body)
			}
			status, _, body := s.runsCall(t, "GET", "/api/v1/runs/_health", rtNone)
			if setting == "0" && (status != 200 || !strings.Contains(body, `"tape_instances_count":`)) {
				t.Errorf("health for a tenantless caller with the opt-out: %d %s", status, body)
			}
			// The cross-tenant scope sees both tenants.
			if status, _, body := s.runsCall(t, "GET", "/api/v1/runs", rtCross); status != 200 || !strings.Contains(body, runA) || !strings.Contains(body, runB) {
				t.Errorf("cross-tenant list: %d %s", status, body)
			}
			if status, _, body := s.runsCall(t, "GET", "/api/v1/runs/"+runB, rtCross); status != 200 {
				t.Errorf("cross-tenant get: %d %s", status, body)
			}
			// With the opt-out, a tenantless caller sees every run, as before.
			status, _, body = s.runsCall(t, "GET", "/api/v1/runs", rtNone)
			if setting == "0" && (status != 200 || !strings.Contains(body, runA) || !strings.Contains(body, runB)) {
				t.Errorf("tenantless list with the opt-out: %d %s", status, body)
			}
		})
	}
}

// TestRunsTenantDevelopmentMode: the loopback-only development server serves
// the anonymous local developer every run, with no configuration, as before.
// Ambiguous identities are refused there too.
func TestRunsTenantDevelopmentMode(t *testing.T) {
	setRequireTenant(t, "unset")
	s := newRunsTenantServer(t, func(c *config.Config) {
		c.DevelopmentMode = true
		c.Host = "127.0.0.1"
		c.TenantID = ""
	})
	if status, _, body := s.runsCall(t, "GET", "/api/v1/runs", ""); status != 200 || !strings.Contains(body, runA) || !strings.Contains(body, runB) {
		t.Fatalf("development list: %d %s", status, body)
	}
	for _, route := range mountedRunsRoutes(t, s) {
		if route.method != http.MethodGet || !route.perRun || route.pattern == "/events/stream" {
			continue
		}
		if status, _, body := s.runsCall(t, route.method, route.path(runB), ""); status != 200 {
			t.Errorf("development %s: %d %s", route.path(runB), status, body)
		}
	}
	if status, _, body := s.runsCall(t, "GET", "/api/v1/runs", rtNone); status != 200 || !strings.Contains(body, runB) {
		t.Errorf("development list as a verified tenantless developer: %d %s", status, body)
	}
	if status, code, body := s.runsCall(t, "GET", "/api/v1/runs", rtDomAmb); status != 403 || code != "TENANT_AMBIGUOUS" {
		t.Errorf("development list as an ambiguous identity: %d %s", status, body)
	}
}

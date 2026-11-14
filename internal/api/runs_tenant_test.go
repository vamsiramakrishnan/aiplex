package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// The tenant decision for the runs routes, caller by caller, under each
// operator setting and in development mode. The production router is covered
// route by route in cmd/aiplex-api/runs_tenant_test.go.

const tenantDomainMap = "a.example=acme, a.example=acme,amb.example=acme,amb.example=globex"

func tenantAccess(domain string, scopes ...string) *models.ResolvedAccess {
	return &models.ResolvedAccess{Identity: models.WIFIdentity{Subject: "s", Domain: domain}, Scopes: append([]string{"aiplex:runs:read"}, scopes...)}
}

type tenantCase struct {
	name   string
	access *models.ResolvedAccess
	// want is "acme", "all", or an error code.
	want string
}

var tenantCases = []tenantCase{
	{"one tenant", tenantAccess("", "aiplex:tenant:acme"), "acme"},
	{"same tenant twice", tenantAccess("", "aiplex:tenant:acme", "aiplex:tenant:acme"), "acme"},
	{"two tenants", tenantAccess("", "aiplex:tenant:acme", "aiplex:tenant:globex"), "TENANT_AMBIGUOUS"},
	{"two tenants, other order", tenantAccess("", "aiplex:tenant:globex", "aiplex:tenant:acme"), "TENANT_AMBIGUOUS"},
	{"two tenants with cross-tenant scope", tenantAccess("", "aiplex:tenant:acme", "aiplex:tenant:globex", "aiplex:runs:read:cross_tenant"), "TENANT_AMBIGUOUS"},
	{"cross-tenant scope", tenantAccess("", "aiplex:runs:read:cross_tenant"), "all"},
	{"one tenant with cross-tenant scope", tenantAccess("", "aiplex:tenant:acme", "aiplex:runs:read:cross_tenant"), "all"},
	{"domain-mapped tenant", tenantAccess("a.example"), "acme"},
	{"ambiguously mapped domain", tenantAccess("amb.example"), "TENANT_AMBIGUOUS"},
	{"tenant scope wins over domain map", tenantAccess("amb.example", "aiplex:tenant:acme"), "acme"},
	{"unmapped domain", tenantAccess("other.example"), "TENANT_REQUIRED"},
	{"no tenant", tenantAccess(""), "TENANT_REQUIRED"},
	{"empty tenant scope", tenantAccess("", "aiplex:tenant:"), "TENANT_REQUIRED"},
	{"no verified identity", nil, "UNAUTHENTICATED"},
}

// expected adjusts a case's default outcome for the opt-out and development
// mode. Neither relaxes ambiguity, and the opt-out never admits an
// unauthenticated request.
func (c tenantCase) expected(optOut, dev bool) string {
	switch c.want {
	case "TENANT_REQUIRED":
		if optOut || dev {
			return "all"
		}
	case "UNAUTHENTICATED":
		if dev {
			return "all"
		}
	}
	return c.want
}

func tenantRouter(t *testing.T, access *models.ResolvedAccess, dev bool) http.Handler {
	t.Helper()
	store := registry.NewMemoryStore()
	ctx := context.Background()
	for _, run := range []models.ExecutionRun{{RunID: "r-acme", TenantID: "acme"}, {RunID: "r-globex", TenantID: "globex"}} {
		run := run
		if err := store.UpsertExecutionRun(ctx, &run); err != nil {
			t.Fatal(err)
		}
	}
	h := api.NewRunsHandler(store)
	r := chi.NewRouter()
	r.Use(api.RunsTestCaller(access, dev))
	r.Route("/api/v1/runs", func(r chi.Router) {
		r.Use(api.RequireTenant)
		r.Get("/", h.List)
		r.Get("/_health", h.Health)
		r.With(h.RunVisible).Get("/{run_id}", h.Get)
	})
	// The handlers repeat the decision without the RequireTenant gate.
	r.Get("/ungated/runs", h.List)
	r.Get("/ungated/health", h.Health)
	r.With(h.RunVisible).Get("/ungated/runs/{run_id}", h.Get)
	return r
}

func serve(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	// Request inputs that might try to name a tenant.
	req.Header.Set("X-Aiplex-Tenant", "globex")
	req.Header.Set("X-Tenant-Id", "globex")
	h.ServeHTTP(rec, req)
	return rec
}

func listedRuns(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var body struct {
		Runs []models.ExecutionRun `json:"runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	var ids []string
	for _, r := range body.Runs {
		ids = append(ids, r.RunID)
	}
	sort.Strings(ids)
	return ids
}

// healthKeys returns the fields present in a 200 _health answer.
func healthKeys(t *testing.T, rec *httptest.ResponseRecorder) map[string]bool {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("health: %d %s", rec.Code, rec.Body)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	keys := map[string]bool{}
	for k := range body {
		keys[k] = true
	}
	return keys
}

// A value the store cannot read is omitted, not reported as zero or false.
func TestRunsHealthOmitsUnreadableValues(t *testing.T) {
	h := api.NewRunsHandler(failingRunsStore{registry.NewMemoryStore()})
	r := chi.NewRouter()
	r.Use(api.RunsTestCaller(tenantAccess("", "aiplex:runs:read:cross_tenant"), false))
	r.Get("/health", h.Health)
	if keys := healthKeys(t, serve(r, "/health")); keys["has_runs"] || keys["last_ingest_at"] || keys["tape_instances_count"] || !keys["now"] {
		t.Fatalf("health with a failing store has keys %v", keys)
	}
}

type failingRunsStore struct{ *registry.MemoryStore }

var errRunsStore = errors.New("store unavailable")

func (failingRunsStore) LastIngestAt(context.Context) (time.Time, error) {
	return time.Time{}, errRunsStore
}
func (failingRunsStore) ListExecutionRuns(context.Context, string, string, int) ([]models.ExecutionRun, error) {
	return nil, errRunsStore
}
func (failingRunsStore) CountInstancesWithRuntime(context.Context, models.RuntimeEngine) (int, error) {
	return 0, errRunsStore
}

func TestRunsTenantDecisionMatrix(t *testing.T) {
	t.Setenv("AIPLEX_DOMAIN_TENANT_MAP", tenantDomainMap)
	for _, setting := range []string{"unset", "", "1", "true", "0"} {
		for _, dev := range []bool{false, true} {
			for _, c := range tenantCases {
				t.Run(setting+"/dev="+map[bool]string{false: "off", true: "on"}[dev]+"/"+c.name, func(t *testing.T) {
					t.Setenv(api.RequireTenantEnv, "")
					if setting == "unset" {
						if err := os.Unsetenv(api.RequireTenantEnv); err != nil {
							t.Fatal(err)
						}
					} else {
						t.Setenv(api.RequireTenantEnv, setting)
					}
					want := c.expected(setting == "0", dev)
					h := tenantRouter(t, c.access, dev)
					for _, prefix := range []string{"/api/v1/runs", "/ungated"} {
						list, health, other, own := prefix+"/", prefix+"/_health", prefix+"/r-globex", prefix+"/r-acme"
						if prefix == "/ungated" {
							list, health, other, own = "/ungated/runs", "/ungated/health", "/ungated/runs/r-globex", "/ungated/runs/r-acme"
						}
						switch want {
						case "acme":
							if got := listedRuns(t, serve(h, list)); len(got) != 1 || got[0] != "r-acme" {
								t.Errorf("%s: list = %v", prefix, got)
							}
							if got := listedRuns(t, serve(h, list+"?tenant_id=globex")); len(got) != 0 {
								t.Errorf("%s: list naming globex = %v", prefix, got)
							}
							if rec := serve(h, other); rec.Code != http.StatusNotFound {
								t.Errorf("%s: other tenant's run: %d %s", prefix, rec.Code, rec.Body)
							}
							if rec := serve(h, own); rec.Code != http.StatusOK {
								t.Errorf("%s: own run: %d %s", prefix, rec.Code, rec.Body)
							}
							if keys := healthKeys(t, serve(h, health)); !keys["has_runs"] || keys["last_ingest_at"] || keys["tape_instances_count"] {
								t.Errorf("%s: health for a confined caller has keys %v", prefix, keys)
							}
						case "all":
							if got := listedRuns(t, serve(h, list)); len(got) != 2 {
								t.Errorf("%s: list = %v", prefix, got)
							}
							if rec := serve(h, other); rec.Code != http.StatusOK {
								t.Errorf("%s: run: %d %s", prefix, rec.Code, rec.Body)
							}
							if keys := healthKeys(t, serve(h, health)); !keys["has_runs"] || !keys["last_ingest_at"] || !keys["tape_instances_count"] {
								t.Errorf("%s: health for the all-tenants view has keys %v", prefix, keys)
							}
						default:
							status := http.StatusForbidden
							if want == "UNAUTHENTICATED" {
								status = http.StatusUnauthorized
							}
							for _, target := range []string{list, list + "?tenant_id=acme", health, own, other} {
								rec := serve(h, target)
								if rec.Code != status || !strings.Contains(rec.Body.String(), `"`+want+`"`) || strings.Contains(rec.Body.String(), "r-") {
									t.Errorf("%s: want %d %s, got %d %s", target, status, want, rec.Code, rec.Body)
								}
							}
						}
					}
				})
			}
		}
	}
}

func TestTenantFromContextNeverPicksOneOfTwo(t *testing.T) {
	t.Setenv("AIPLEX_DOMAIN_TENANT_MAP", tenantDomainMap)
	for _, c := range tenantCases {
		if c.access == nil {
			continue
		}
		ctx := httptest.NewRequest(http.MethodGet, "/", nil)
		var tenant string
		var err error
		api.RunsTestCaller(c.access, false)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			tenant, _, err = api.TenantFromContext(r.Context())
		})).ServeHTTP(httptest.NewRecorder(), ctx)
		if c.want == "TENANT_AMBIGUOUS" {
			if !errors.Is(err, api.ErrAmbiguousTenant) || tenant != "" {
				t.Errorf("%s: got tenant %q err %v", c.name, tenant, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// The authorization harness builds the server exactly as main does and
// drives it with a fixed set of verified identities. Identity tokens are
// opaque test strings; the verifier below maps each to fixed claims.

const (
	authzIssuer     = "https://identity.authz.test"
	authzHomeTenant = "tenant"
	authzForeign    = "other-tenant"
)

// authzCaller is one column of the authorization matrix.
type authzCaller string

const (
	callerAnonymous authzCaller = "anonymous"
	callerNoRole    authzCaller = "no-role"
	callerViewer    authzCaller = "viewer"
	callerDeployer  authzCaller = "deployer"
	callerAdmin     authzCaller = "admin"
	callerForeign   authzCaller = "foreign-admin"
	callerAgent     authzCaller = "agent"
)

var authzCallers = []authzCaller{callerAnonymous, callerNoRole, callerViewer, callerDeployer, callerAdmin, callerForeign, callerAgent}

// authzVerifier accepts any token naming a known caller. A token of the form
// "<caller>:<marker>" carries the marker in a claim so log-leak tests can
// prove claims and tokens never reach logs.
type authzVerifier struct{}

func (authzVerifier) Verify(_ context.Context, token string) (map[string]any, error) {
	name := token
	if i := strings.IndexByte(token, ':'); i >= 0 {
		name = token[:i]
	}
	now := time.Now()
	claims := map[string]any{"iss": authzIssuer, "sub": "subject-" + name, "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix()}
	switch authzCaller(name) {
	case callerNoRole:
		claims["groups"] = []any{"everyone"}
	case callerViewer:
		claims["groups"] = []any{"viewers"}
	case callerDeployer:
		claims["groups"] = []any{"deployers"}
	case callerAdmin:
		claims["groups"] = []any{"admins"}
	case callerForeign:
		claims["groups"] = []any{"foreign-admins"}
	case callerAgent:
		claims["sub"] = "subject-agent-user"
		claims["act"] = map[string]any{"sub": "agent-workload"}
		claims["groups"] = []any{"agents"}
	case "quota-reader":
		claims["groups"] = []any{"quota-readers"}
	case "runs-operator":
		claims["groups"] = []any{"runs-operators"}
	default:
		return nil, errUnknownTestToken
	}
	return claims, nil
}

type testTokenError string

func (e testTokenError) Error() string { return string(e) }

const errUnknownTestToken = testTokenError("unknown test token")

// authzStore adds the durable quota capability production quotaplex requires.
type authzStore struct {
	*registry.MemoryStore
	quota     quotaplex.Store
	resources resources.Store
}

func (s authzStore) QuotaStore() quotaplex.Store      { return s.quota }
func (s authzStore) ModuleResources() resources.Store { return s.resources }

type authzServer struct {
	handler   http.Handler
	store     authzStore
	cfg       *config.Config
	resources resources.Store
}

func authzRoleBindings(t *testing.T, store *registry.MemoryStore) {
	t.Helper()
	for _, b := range []*models.RoleBinding{
		{ID: "viewers", Group: "viewers", Role: models.RoleViewer},
		{ID: "deployers", Group: "deployers", Role: models.RoleDeployer},
		{ID: "admins", Group: "admins", Role: models.RoleAdmin},
		{ID: "agents", Group: "agents", Role: models.RoleAgent},
		{ID: "foreign-admins", Group: "foreign-admins", Role: models.RoleAdmin, Scopes: append([]string{"aiplex:tenant:" + authzForeign}, models.DefaultRoleScopes[models.RoleAdmin]...)},
		{ID: "quota-readers", Group: "quota-readers", Role: models.RoleViewer, Scopes: []string{"aiplex:quota:read"}},
		{ID: "runs-operators", Group: "runs-operators", Role: models.RoleDeployer, Scopes: []string{"aiplex:tenant:" + authzHomeTenant, "aiplex:runs:read", "aiplex:runs:cancel"}},
	} {
		if err := store.PutRoleBinding(context.Background(), b); err != nil {
			t.Fatal(err)
		}
	}
}

// newAuthzServer builds the production router (development mode off) with
// every module, including quotaplex, enabled.
func newAuthzServer(t *testing.T, mutate ...func(*config.Config)) *authzServer {
	t.Helper()
	return newAuthzServerWithResources(t, resources.NewMemoryStore(), mutate...)
}

func newAuthzServerWithResources(t *testing.T, resourceStore resources.Store, mutate ...func(*config.Config)) *authzServer {
	t.Helper()
	registryURL := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(registryURL.Close)
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
	}))
	t.Cleanup(hydra.Close)
	memory := registry.NewMemoryStore()
	authzRoleBindings(t, memory)
	store := authzStore{MemoryStore: memory, quota: quotaplex.NewMemoryStore(), resources: resourceStore}
	cfg := &config.Config{
		Host:           "0.0.0.0",
		HydraAdminURL:  hydra.URL,
		HydraPublicURL: hydra.URL,
		EnabledModules: []string{"mcplex", "a2aplex", "llmplex", "skillsplex", "clientplex", "networking", "quotaplex"},
		TenantID:       authzHomeTenant,
		PublicURL:      "https://aiplex.authz.test",
		JWTIssuer:      authzIssuer,
		AllowedOrigins: []string{"https://console.authz.test"},
		TrustDomain:    "authz.test",
		GatewayName:    "gateway",
		QuotaFile:      quotaStartupFile(t, quotaStartupSpec(time.Now())),
	}
	for _, m := range mutate {
		m(cfg)
	}
	validator := auth.NewWIFValidator(memory, auth.WIFConfig{Verifier: authzVerifier{}, TrustedIssuers: []string{authzIssuer}, WorkforcePoolID: "people", IsWorkforce: true})
	srv, err := newServer(context.Background(), cfg, serverOptions{store: store, identity: validator, officialRegistryURL: registryURL.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.close)
	return &authzServer{handler: srv.handler, store: store, cfg: cfg, resources: store.resources}
}

// mountedRoute is one row of the machine-readable route table.
type mountedRoute struct {
	Method      string   `json:"method"`
	Pattern     string   `json:"pattern"`
	Handler     string   `json:"handler"`
	Middlewares []string `json:"middlewares"`
	Policy      string   `json:"enforced_policy,omitempty"`
}

func (r mountedRoute) key() string { return r.Method + " " + r.Pattern }

func funcName(v any) string {
	value := reflect.ValueOf(v)
	if value.Kind() != reflect.Func {
		return reflect.TypeOf(v).String()
	}
	name := runtime.FuncForPC(value.Pointer()).Name()
	return strings.TrimPrefix(name, "github.com/vamsiramakrishnan/aiplex/")
}

// walkRoutes enumerates every route the router serves, with the handler and
// the middleware chain chi applies to it. Handle/HandleFunc registrations
// appear once per method chi expands them to.
func walkRoutes(t *testing.T, handler http.Handler) []mountedRoute {
	t.Helper()
	router, ok := handler.(chi.Routes)
	if !ok {
		t.Fatal("server handler is not a chi router")
	}
	seen := map[string]bool{}
	var out []mountedRoute
	err := chi.Walk(router, func(method, route string, h http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		route = strings.ReplaceAll(route, "/*/", "/")
		if len(route) > 1 {
			route = strings.TrimSuffix(route, "/")
		}
		r := mountedRoute{Method: method, Pattern: route}
		if f, ok := h.(http.HandlerFunc); ok {
			r.Handler = funcName(f)
		} else {
			r.Handler = funcName(h)
		}
		for _, m := range middlewares {
			r.Middlewares = append(r.Middlewares, funcName(m))
		}
		if !seen[r.key()] {
			seen[r.key()] = true
			out = append(out, r)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

func authzRequest(method, target, token, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.RemoteAddr = "192.0.2.10:4000"
	return r
}

// TestRouteTable checks that the matched-pattern lookup the route policy
// uses agrees with chi.Walk for every route, and writes the machine-readable
// route table to $AIPLEX_ROUTE_TABLE when set.
func TestRouteTable(t *testing.T) {
	s := newAuthzServer(t)
	routes := walkRoutes(t, s.handler)
	policy := routePolicy(s.cfg)
	for i, r := range routes {
		if r.Method == http.MethodConnect {
			continue
		}
		req := httptest.NewRequest(r.Method, concretePath(r.Pattern), nil)
		pattern, ok := api.MatchedRoutePattern(s.handler.(chi.Routes), req)
		if !ok || pattern != r.Pattern {
			t.Errorf("%s: policy lookup resolves pattern %q (matched %v)", r.key(), pattern, ok)
		}
		rule := policy.Lookup(r.Method, r.Pattern)
		scope := "deployment"
		if rule.TenantScoped {
			scope = "tenant"
		}
		routes[i].Policy = rule.Access.String() + "/" + scope
	}
	if path := os.Getenv("AIPLEX_ROUTE_TABLE"); path != "" {
		raw, err := json.MarshalIndent(routes, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

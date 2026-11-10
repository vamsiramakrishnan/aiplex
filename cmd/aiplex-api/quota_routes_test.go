package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
)

type quotaRouteVerifier struct{}

func (quotaRouteVerifier) Verify(_ context.Context, token string) (map[string]any, error) {
	group, subject := "viewers", token
	if token == "admin" {
		group = "admins"
	}
	if token == "quota-reader" {
		group = "quota-readers"
	}
	claims := map[string]any{"iss": "https://identity.example", "sub": subject, "groups": []any{group}, "iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix()}
	if strings.HasPrefix(token, "owner-agent-") {
		claims["sub"] = "owner"
		claims["act"] = map[string]any{"sub": strings.TrimPrefix(token, "owner-agent-")}
	}
	if strings.HasPrefix(token, "stranger-agent-") {
		claims["sub"] = "stranger"
		claims["act"] = map[string]any{"sub": strings.TrimPrefix(token, "stranger-agent-")}
	}
	return claims, nil
}
func quotaRouteValidator(t *testing.T, granted bool) *auth.WIFValidator {
	t.Helper()
	store := registry.NewMemoryStore()
	scopes := []string{}
	if granted {
		scopes = []string{"mcp:tools:search"}
	}
	for _, b := range []*models.RoleBinding{{ID: "viewers", Group: "viewers", Role: models.RoleViewer, Scopes: scopes}, {ID: "admins", Group: "admins", Role: models.RoleAdmin}, {ID: "quota-readers", Group: "quota-readers", Role: models.RoleViewer, Scopes: []string{"aiplex:quota:read"}}} {
		if err := store.PutRoleBinding(context.Background(), b); err != nil {
			t.Fatal(err)
		}
	}
	return auth.NewWIFValidator(store, auth.WIFConfig{Verifier: quotaRouteVerifier{}, TrustedIssuers: []string{"https://identity.example"}, WorkforcePoolID: "people", IsWorkforce: true})
}
func mountedQuotaRouter(t *testing.T, h *api.ConnectedHandler, validator *auth.WIFValidator, mount func(chi.Router)) http.Handler {
	t.Helper()
	h.ResolveAccess = validator.SyncUserScopes
	router := chi.NewRouter()
	router.Use(api.RequestID, api.WIFAuth(validator))
	router.Route("/api/v1", func(r chi.Router) {
		r.Use(api.RequireIdentity(false))
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if nativeInvocation(r) {
					next.ServeHTTP(w, r)
					return
				}
				api.ControlPlaneAccess(false)(next).ServeHTTP(w, r)
			})
		})
		mount(r)
	})
	return router
}
func TestMountedManagedToolPreservesViewerAndAdministrativeBoundaries(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer server-tool-token" {
			t.Error("server identity was not used")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"rpc","result":{"content":[]}}`)
	}))
	defer upstream.Close()
	t.Setenv("QUOTA_ROUTE_TOOL_TOKEN", "server-tool-token")
	for _, test := range []struct {
		name, method, path string
		enabled, granted   bool
		want               int
	}{
		{"granted viewer invokes", "POST", "/api/v1/mcp/connections/tool/invoke", true, true, 200},
		{"missing scope denies", "POST", "/api/v1/mcp/connections/tool/invoke", true, false, 403},
		{"disabled module denies", "POST", "/api/v1/mcp/connections/tool/invoke", false, true, 404},
		{"invoke scope does not register", "POST", "/api/v1/mcp/connections/tool/registration", true, true, 403},
		{"invoke scope does not mutate", "PUT", "/api/v1/mcp/connections/tool", true, true, 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{upstream.URL}, AllowLoopback: true}, Identities: []connections.Identity{{ID: "server", TenantID: "tenant", Type: "bearer-env", TokenEnv: "QUOTA_ROUTE_TOOL_TOKEN", AllowedOrigins: []string{upstream.URL}}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			store := resources.NewMemoryStore()
			connection := api.ToolConnection{ID: "tool", ResourceURL: upstream.URL + "/mcp", Execution: &toolexec.Connection{ID: "tool", Endpoint: upstream.URL + "/mcp", IdentityRef: "server", AllowedTools: []string{"search"}, ProtocolVersion: "2025-03-26", Mode: toolexec.StatelessJSON}}
			spec, _ := json.Marshal(connection)
			if _, err = store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "tool-connection", ID: "tool", Spec: spec}, 0); err != nil {
				t.Fatal(err)
			}
			h := api.NewConnectedHandler(store, runtime, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
			available := builtinModules(api.NewLLMHandler(registry.NewMemoryStore(), nil, "gateway", nil), api.NewA2AHandler(registry.NewMemoryStore()), api.NewSkillsHandler(registry.NewMemoryStore()))
			extendConnectedModules(available, h, api.NewClientHandler(store, h.Tenant, h))
			router := mountedQuotaRouter(t, h, quotaRouteValidator(t, test.granted), func(r chi.Router) {
				if test.enabled {
					available["mcplex"].mount(r)
				}
			})
			before := calls.Load()
			r := httptest.NewRequest(test.method, test.path, strings.NewReader(`{"jsonrpc":"2.0","id":"rpc","method":"tools/call","params":{"name":"search","arguments":{}}}`))
			r.Header.Set("Authorization", "Bearer owner")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != test.want {
				t.Fatalf("got %d want %d: %s", w.Code, test.want, w.Body.String())
			}
			if test.want != 200 && calls.Load() != before {
				t.Fatal("denied route reached provider")
			}
		})
	}
}
func TestQuotaStatusBindsTenantPrincipalAndDelegatedActor(t *testing.T) {
	now := time.Now().UTC()
	spec := quotaStartupSpec(now)
	host, err := api.NewQuotaHost(context.Background(), spec.Host, quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	principal := auth.CanonicalPrincipalID("https://identity.example", "people", "owner")
	actor := auth.CanonicalPrincipalID("https://identity.example", "people", "a")
	membership := quotaplex.Membership{Ref: contracts.SnapshotRef{ID: "verified-membership", Version: "1"}, TenantID: "tenant", SubjectID: principal, Groups: []quotaplex.GroupRef{}, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
	for _, delegated := range []bool{false, true} {
		id := "own"
		contextActor := ""
		var actorSubject *quotaplex.Subject
		if delegated {
			id = "delegated"
			contextActor = actor
			m := membership
			m.SubjectID = actor
			actorSubject = &quotaplex.Subject{ID: actor, Kind: "agent", Membership: m}
		}
		demand := quotaplex.Demand{Version: quotaplex.Version, Context: contracts.RequestContext{Version: contracts.Version, TenantID: "tenant", RequestID: "request", PrincipalID: principal, ActorID: contextActor, ResourceID: "tool", MembershipSnapshotRef: &membership.Ref}, Principal: quotaplex.Subject{ID: principal, Kind: "human", Membership: membership}, Actor: actorSubject, LogicalOperationID: id, AttemptID: id, PayloadDigest: strings.Repeat("a", 64), Target: quotaplex.Target{ConfigurationDigest: strings.Repeat("b", 64), Plane: "mcplex", Provider: "mcp", ResourceID: "tool", Operation: "tools/call:search"}, Bounds: quotaplex.Vector{"requests": 1}}
		decision, err := host.Engines()["tenant"].Admit(context.Background(), demand)
		if err != nil || !decision.Allowed {
			t.Fatal(decision, err)
		}
	}
	h := api.NewConnectedHandler(nil, nil, func(r *http.Request) (string, error) {
		if api.GetWIFIdentity(r.Context()).Subject == "outsider" {
			return "other", nil
		}
		return "tenant", nil
	}, nil, true)
	m := &quotaRuntime{host: host}
	router := mountedQuotaRouter(t, h, quotaRouteValidator(t, false), func(r chi.Router) { m.mount(r, h) })
	for _, test := range []struct {
		token, id string
		want      int
	}{{"owner", "own", 200}, {"stranger", "own", 404}, {"owner-agent-a", "own", 200}, {"owner", "delegated", 200}, {"owner-agent-a", "delegated", 200}, {"owner-agent-b", "delegated", 200}, {"stranger-agent-a", "delegated", 200}, {"stranger-agent-a", "own", 404}, {"admin", "delegated", 404}, {"quota-reader", "delegated", 200}, {"quota-reader", "missing", 404}, {"outsider", "own", 404}, {"owner", "missing", 404}} {
		r := httptest.NewRequest("GET", "/api/v1/quota/attempts/"+test.id, nil)
		r.Header.Set("Authorization", "Bearer "+test.token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("%s/%s got %d want %d: %s", test.token, test.id, w.Code, test.want, w.Body.String())
		}
	}
}

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/internal/secrets"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
)

type connectedVerifier struct{}

func (connectedVerifier) Verify(_ context.Context, token string) (map[string]any, error) {
	if token != "verified-caller" {
		return nil, errors.New("unverified")
	}
	return map[string]any{"iss": "https://identity.example", "sub": "caller", "groups": []any{"callers"}}, nil
}

func connectedRouter(t *testing.T, upstream string, enabled, grant bool) http.Handler {
	t.Helper()
	identityStore := registry.NewMemoryStore()
	scopes := []string{}
	if grant {
		scopes = []string{"llm:surface:surface:models.streamGenerateContent", "llm:model:model-a"}
	}
	if e := identityStore.PutRoleBinding(context.Background(), &models.RoleBinding{ID: "caller", Group: "callers", Role: models.RoleViewer, Scopes: scopes}); e != nil {
		t.Fatal(e)
	}
	validator := auth.NewWIFValidator(identityStore, auth.WIFConfig{Verifier: connectedVerifier{}, TrustedIssuers: []string{"https://identity.example"}, WorkforcePoolID: "people", IsWorkforce: true})
	runtime, e := connections.New(connections.Config{
		Network:    endpointpolicy.Config{AllowedOrigins: []string{upstream}, AllowLoopback: true},
		Identities: []connections.Identity{{ID: "server", TenantID: "tenant", Type: "bearer-env", TokenEnv: "CONNECTED_TEST_TOKEN", AllowedOrigins: []string{upstream}}},
	}, nil)
	if e != nil {
		t.Fatal(e)
	}
	store := resources.NewMemoryStore()
	operations, e := modelapi.SelectOperations("models.streamGenerateContent")
	if e != nil {
		t.Fatal(e)
	}
	spec, _ := json.Marshal(api.ModelConnection{IdentityRef: "server", Surface: modelapi.Surface{
		ID: "surface", Protocol: "google-genai", Origin: upstream, APIVersion: "v1", Project: "project", Location: "global", Publisher: "google", Models: []string{"model-a"}, Operations: operations,
	}})
	if _, e = store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "model-surface", ID: "surface", Spec: spec}, 0); e != nil {
		t.Fatal(e)
	}
	connected := api.NewConnectedHandler(store, runtime, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
	available := builtinModules(api.NewLLMHandler(identityStore, deploy.NewNoOpK8sClient(), "gateway", nil), api.NewA2AHandler(identityStore), api.NewSkillsHandler(identityStore))
	extendConnectedModules(available, connected, api.NewClientHandler(store, func(*http.Request) (string, error) { return "tenant", nil }, connected))
	router := chi.NewRouter()
	router.Use(api.RequestID, api.WIFAuth(validator), api.AuditLog)
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
		if enabled {
			available["llmplex"].mount(r)
		}
	})
	return router
}

func TestConnectedMountedRoutesPreserveActionAndModuleBoundaries(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer server-credential" {
			t.Error("caller credential reached upstream")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[]}\n\n")
	}))
	defer upstream.Close()
	t.Setenv("CONNECTED_TEST_TOKEN", "server-credential")
	path := "/api/v1/llm/native/surface/v1/projects/project/locations/global/publishers/google/models/model-a:streamGenerateContent"
	for _, tc := range []struct {
		name, method, path string
		enabled, grant     bool
		want               int
	}{
		{"viewer with action can invoke", "POST", path, true, true, 200},
		{"viewer without action cannot invoke", "POST", path, true, false, 403},
		{"action does not grant CRUD", "PUT", "/api/v1/llm/surfaces/surface", true, true, 403},
		{"nested CRUD route is mounted", "GET", "/api/v1/llm/surfaces/surface", true, false, 200},
		{"disabled native route", "POST", path, false, true, 404},
		{"disabled nested CRUD route", "GET", "/api/v1/llm/surfaces/surface", false, true, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := upstreamCalls.Load()
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"contents":[]}`))
			r.Header.Set("Authorization", "Bearer verified-caller")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			connectedRouter(t, upstream.URL, tc.enabled, tc.grant).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("mounted route got %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want != 200 && upstreamCalls.Load() != before {
				t.Fatal("denied/disabled request acquired upstream connection")
			}
		})
	}
}

func TestConnectedSSEFlushesBeforeUpstreamFinishes(t *testing.T) {
	finish := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(finish) }) }
	defer release()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-finish:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	t.Setenv("CONNECTED_TEST_TOKEN", "server-credential")
	server := httptest.NewServer(connectedRouter(t, upstream.URL, true, true))
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	r, _ := http.NewRequest("POST", server.URL+"/api/v1/llm/native/surface/v1/projects/project/locations/global/publishers/google/models/model-a:streamGenerateContent", strings.NewReader(`{"contents":[]}`))
	r.Header.Set("Authorization", "Bearer verified-caller")
	r.Header.Set("Content-Type", "application/json")
	response, e := client.Do(r)
	if e != nil {
		release()
		t.Fatal(e)
	}
	defer response.Body.Close()
	line, e := bufio.NewReader(response.Body).ReadString('\n')
	release()
	if e != nil || !strings.HasPrefix(line, "data:") {
		t.Fatalf("first event unavailable before terminal response: %q %v", line, e)
	}
}

func TestConnectedNilSecretBackendPreventsDynamicRegistration(t *testing.T) {
	var origin string
	var registrations atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": origin + "/mcp", "authorization_servers": []string{origin}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": origin, "authorization_endpoint": origin + "/authorize", "token_endpoint": origin + "/token", "registration_endpoint": origin + "/register",
				"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
			})
		case "/register":
			registrations.Add(1)
			_, _ = io.WriteString(w, `{"client_id":"unpersistable","client_secret":"must-not-be-created"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	origin = server.URL
	var manager *secrets.Manager
	service, e := toolauth.NewService(toolauth.Config{
		Network:     endpointpolicy.Config{AllowedOrigins: []string{origin}, AllowLoopback: true, TLSConfig: server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()},
		Credentials: connections.SecretCredentials{Manager: manager}, // Interface holds a typed-nil manager, as production startup can.
	})
	if e != nil {
		t.Fatal(e)
	}
	_, e = service.Prepare(context.Background(), toolauth.Connection{TenantID: "tenant", ID: "tool", ResourceURL: origin + "/mcp"}, toolauth.ClientProfile{ID: "native", Name: "Native", ApplicationType: "native", RedirectURIs: []string{"http://127.0.0.1:12345/callback"}})
	if e == nil || registrations.Load() != 0 {
		t.Fatalf("missing secret backend registered an unpersistable client: %v calls=%d", e, registrations.Load())
	}
}

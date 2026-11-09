package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func TestIAMPartialUpdatePreservesSourceAndScopeRestrictions(t *testing.T) {
	store := registry.NewMemoryStore()
	original := models.RoleBinding{ID: "binding", Group: "research", Role: models.RoleDeployer, Provider: "issuer", PoolID: "pool", Scopes: []string{"llm:model:reviewed"}}
	if err := store.PutRoleBinding(context.Background(), &original); err != nil {
		t.Fatal(err)
	}
	h := NewIAMHandler(store, nil)
	r := chi.NewRouter()
	r.Put("/bindings/{id}", h.UpdateRoleBinding)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest("PUT", "/bindings/binding", strings.NewReader(`{"description":"updated"}`)))
	got, _ := store.GetRoleBinding(context.Background(), "binding")
	if response.Code != 200 || got.Provider != "issuer" || got.PoolID != "pool" || !reflect.DeepEqual(got.Scopes, original.Scopes) {
		t.Fatalf("partial edit changed trust boundary: %d %+v", response.Code, got)
	}
	response = httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest("PUT", "/bindings/binding", strings.NewReader(`{"role":"superuser"}`)))
	if response.Code != 400 {
		t.Fatalf("invalid IAM role persisted: %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest("PUT", "/bindings/binding", strings.NewReader(`{"role":"viewer"}`)))
	got, _ = store.GetRoleBinding(context.Background(), "binding")
	if response.Code != 200 || len(got.Scopes) != 0 {
		t.Fatalf("role downgrade retained old execution grants: %d %+v", response.Code, got)
	}
}

func TestIAMGeneratedIDsDoNotOverwriteSameGroupAcrossProviders(t *testing.T) {
	store := registry.NewMemoryStore()
	h := NewIAMHandler(store, nil)
	for _, issuer := range []string{"issuer-a", "issuer-b"} {
		response := httptest.NewRecorder()
		h.CreateRoleBinding(response, httptest.NewRequest("POST", "/bindings", strings.NewReader(fmt.Sprintf(`{"group":"research","role":"viewer","provider":%q,"pool_id":"pool"}`, issuer))))
		if response.Code != 201 {
			t.Fatalf("create: %d %s", response.Code, response.Body.String())
		}
	}
	bindings, _ := store.ListRoleBindings(context.Background())
	if len(bindings) != 2 {
		t.Fatalf("source-scoped group bindings overwritten: %+v", bindings)
	}
}

func TestManualScopesUseCanonicalPrincipalAndIncludeSkills(t *testing.T) {
	store := registry.NewMemoryStore()
	h := NewAuthHandler(nil, store)
	r := chi.NewRouter()
	r.Put("/users/{userId}/scopes", h.SetUserScopes)
	r.Get("/users/{userId}/scopes", h.GetUserScopes)
	for _, id := range []string{"mutable@example.com", "principal-not-a-canonical-id"} {
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequest("PUT", "/users/"+url.PathEscape(id)+"/scopes", strings.NewReader(`{"scopes":["llm:model:a"]}`)))
		if response.Code != 400 {
			t.Fatalf("unresolvable alias accepted for permission write: %q %d", id, response.Code)
		}
	}
	id := auth.CanonicalPrincipalID("issuer", "pool", "immutable")
	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest("PUT", "/users/"+id+"/scopes", strings.NewReader(`{"scopes":["skill:invoke:review","llm:model:a","llm:model:a"]}`)))
	if response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest("GET", "/users/"+id+"/scopes", nil))
	var body struct {
		Scopes  []string            `json:"scopes"`
		ByPlane map[string][]string `json:"by_plane"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != 200 || len(body.Scopes) != 2 || len(body.ByPlane["skillsplex"]) != 1 {
		t.Fatalf("manual scopes were not normalized across Plexes: %s", response.Body.String())
	}
	response = httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest("PUT", "/users/"+id+"/scopes", strings.NewReader(`{"scopes":["llm:model:a mcp:tools:unreviewed"]}`)))
	if response.Code != 400 {
		t.Fatalf("multi-scope injection accepted: %d", response.Code)
	}
}

func TestTokenHookNativeHydraPayloadRechecksAgentCeiling(t *testing.T) {
	store := registry.NewMemoryStore()
	if err := store.PutAgent(context.Background(), &models.Agent{ClientID: "agent", Status: "active", AllowedScopes: []string{"llm:model:a"}}); err != nil {
		t.Fatal(err)
	}
	h := NewAuthHandler(nil, store)
	for _, shape := range []string{"request", "requester"} {
		raw := fmt.Sprintf(`{%q:{"client_id":"agent","granted_scopes":["llm:model:a"]},"session":{"extra":{"keep":"access"},"id_token":{"id_token_claims":{"ext":{"keep":"id"}}}}}`, shape)
		response := httptest.NewRecorder()
		h.TokenHook(response, httptest.NewRequest("POST", "/auth/token-hook", strings.NewReader(raw)))
		var body struct {
			Session struct {
				AccessToken map[string]any `json:"access_token"`
				IDToken     map[string]any `json:"id_token"`
			} `json:"session"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		actor, _ := body.Session.AccessToken["act"].(map[string]any)
		if response.Code != 200 || actor["sub"] != "agent" || body.Session.AccessToken["keep"] != "access" || body.Session.IDToken["keep"] != "id" {
			t.Fatalf("real Hydra hook did not preserve claims or canonical actor: %d %s", response.Code, response.Body.String())
		}
	}
	for _, raw := range []string{
		`{"request":{"client_id":"agent","granted_scopes":["llm:model:revoked"]}}`,
		`{"request":{"client_id":"missing","granted_scopes":[]}}`,
		`{"request":{"client_id":"agent"},"client":{"client_id":"different"}}`,
	} {
		response := httptest.NewRecorder()
		h.TokenHook(response, httptest.NewRequest("POST", "/auth/token-hook", strings.NewReader(raw)))
		if response.Code < 400 {
			t.Fatalf("hook issued token for missing agent, conflicting identity or stale grant: %s", raw)
		}
	}
}

func TestConsentDenyCannotDecideAnotherPrincipalsChallenge(t *testing.T) {
	var rejects int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`{"subject":"someone-else","client":{"client_id":"agent"}}`))
			return
		}
		rejects++
		_, _ = w.Write([]byte(`{"redirect_to":"https://callback.example"}`))
	}))
	defer upstream.Close()
	h := NewAuthHandler(auth.NewHydraClient(upstream.URL), registry.NewMemoryStore())
	request := trustedConnectedRequest("/auth/consent", `{"challenge":"c","deny":true}`, nil)
	response := httptest.NewRecorder()
	h.ConsentAccept(response, request)
	if response.Code != 403 || rejects != 0 {
		t.Fatalf("cross-principal consent denial reached Hydra: %d rejects=%d", response.Code, rejects)
	}
}

type failedAgentReadStore struct{ *registry.MemoryStore }

func (s failedAgentReadStore) GetAgent(context.Context, string) (*models.Agent, error) {
	return nil, errors.New("backend unavailable")
}
func TestAgentRegistrationStoreFailurePrecedesOAuthSideEffects(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"client_id":"agent","client_secret":"secret"}`))
	}))
	defer upstream.Close()
	h := NewAgentHandler(failedAgentReadStore{registry.NewMemoryStore()}, auth.NewHydraClient(upstream.URL))
	response := httptest.NewRecorder()
	h.Register(response, httptest.NewRequest("POST", "/agents", strings.NewReader(`{"client_id":"agent","display_name":"Agent","auth_method":"client_credentials","allowed_scopes":[]}`)))
	if response.Code != 500 || calls != 0 {
		t.Fatalf("unavailable registry treated as missing agent: %d calls=%d", response.Code, calls)
	}
}

func TestAgentRegistrationDerivesAndPersistsRequestedOAuthFlow(t *testing.T) {
	for _, test := range []struct{ method, grant string }{{"authorization_code", "authorization_code"}, {"device_code", "urn:ietf:params:oauth:grant-type:device_code"}, {"client_credentials", "client_credentials"}} {
		t.Run(test.method, func(t *testing.T) {
			var observed auth.OAuthClient
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&observed)
				_, _ = w.Write([]byte(`{"client_id":"agent","client_secret":"one-time-secret"}`))
			}))
			defer upstream.Close()
			store := registry.NewMemoryStore()
			h := NewAgentHandler(store, auth.NewHydraClient(upstream.URL))
			response := httptest.NewRecorder()
			body := fmt.Sprintf(`{"client_id":"agent","display_name":"Agent","auth_method":%q,"allowed_scopes":["llm:model:a"],"redirect_uris":["https://callback.example"]}`, test.method)
			h.Register(response, httptest.NewRequest("POST", "/agents", strings.NewReader(body)))
			saved, _ := store.GetAgent(context.Background(), "agent")
			if response.Code != 201 || observed.GrantTypes[0] != test.grant || saved.GrantTypes[0] != test.grant {
				t.Fatalf("chosen flow not propagated: %d observed=%+v saved=%+v", response.Code, observed, saved)
			}
		})
	}
}

type failedAgentWriteStore struct {
	*registry.MemoryStore
	cancel context.CancelFunc
}

func (s failedAgentWriteStore) PutAgent(context.Context, *models.Agent) error {
	if s.cancel != nil {
		s.cancel()
	}
	return errors.New("persistent store failed")
}
func TestAgentRegistrationRollsBackOAuthAfterCanceledPersistenceFailure(t *testing.T) {
	var methods []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == "POST" {
			_, _ = w.Write([]byte(`{"client_id":"agent","client_secret":"never-return-after-failure"}`))
			return
		}
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := NewAgentHandler(failedAgentWriteStore{registry.NewMemoryStore(), cancel}, auth.NewHydraClient(upstream.URL))
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/agents", strings.NewReader(`{"client_id":"agent","display_name":"Agent","auth_method":"client_credentials","allowed_scopes":[]}`)).WithContext(ctx)
	h.Register(response, request)
	if response.Code != 500 || !reflect.DeepEqual(methods, []string{"POST", "DELETE"}) || strings.Contains(response.Body.String(), "never-return-after-failure") {
		t.Fatalf("failed registration left an OAuth credential or leaked it: %d methods=%v body=%s", response.Code, methods, response.Body.String())
	}
}

func TestAgentRegistrationRejectsConflictingFlowAndInjectedCredentials(t *testing.T) {
	h := NewAgentHandler(registry.NewMemoryStore())
	for _, extra := range []string{`"grant_types":["client_credentials"]`, `"client_secret":"caller-supplied"`, `"allowed_scopes":["llm:model:a mcp:tools:unreviewed"]`, `"spiffe_id":"spiffe://domain/../another"`} {
		raw := `{"client_id":"agent","display_name":"Agent","auth_method":"authorization_code","redirect_uris":["https://client.example/callback"],` + extra + `}`
		response := httptest.NewRecorder()
		h.Register(response, httptest.NewRequest("POST", "/agents", strings.NewReader(raw)))
		if response.Code != 400 {
			t.Fatalf("conflicting registration accepted: %s -> %d %s", extra, response.Code, response.Body.String())
		}
	}
}

func TestAgentPermissionDescriptionsIncludeFirstCatalogPage(t *testing.T) {
	store := registry.NewMemoryStore()
	ctx := context.Background()
	_ = store.PutTemplate(ctx, &models.Template{ID: "first", Plane: models.PlaneMCPlex, Tools: []models.ToolInfo{{Name: "review", Description: "Review from deployed catalog"}}})
	_ = store.PutAgent(ctx, &models.Agent{ClientID: "agent", AllowedScopes: []string{"mcp:tools:review"}, Status: "active"})
	h := NewAgentHandler(store)
	router := chi.NewRouter()
	router.Get("/agents/{clientId}/permissions", h.GetPermissions)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/agents/agent/permissions", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Review from deployed catalog") {
		t.Fatalf("first catalog page skipped: %d %s", response.Code, response.Body.String())
	}
}

type failingConsentManualReadStore struct{ *registry.MemoryStore }

func (s failingConsentManualReadStore) GetUserScopes(context.Context, string) ([]string, error) {
	return nil, errors.New("manual source unavailable")
}
func TestConsentPreviewUsesVerifiedAccessAndRejectsSuspendedAgent(t *testing.T) {
	principal := auth.CanonicalPrincipalID("issuer", "pool", "immutable")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fmt.Sprintf(`{"subject":%q,"client":{"client_id":"agent"},"requested_scope":["llm:model:a"]}`, principal)))
	}))
	defer upstream.Close()
	store := registry.NewMemoryStore()
	agent := &models.Agent{ClientID: "agent", Status: "active", AllowedScopes: []string{"llm:model:a"}}
	_ = store.PutAgent(context.Background(), agent)
	h := NewAuthHandler(auth.NewHydraClient(upstream.URL), failingConsentManualReadStore{store})
	identity := models.WIFIdentity{Subject: "immutable", Provider: "issuer", PoolID: "pool", PrincipalID: principal}
	request := httptest.NewRequest("GET", "/auth/consent?consent_challenge=c", nil)
	request = request.WithContext(context.WithValue(request.Context(), keyWIFAccess, &models.ResolvedAccess{Identity: identity, Scopes: []string{"llm:model:a"}}))
	response := httptest.NewRecorder()
	h.ConsentGet(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"grantable_scopes":["llm:model:a"]`) {
		t.Fatalf("preview ignored request's verified grants: %d %s", response.Code, response.Body.String())
	}
	agent.Status = "suspended"
	_ = store.PutAgent(context.Background(), agent)
	response = httptest.NewRecorder()
	h.ConsentGet(response, request)
	if response.Code != 403 {
		t.Fatalf("preview offered scopes from suspended agent: %d %s", response.Code, response.Body.String())
	}
}

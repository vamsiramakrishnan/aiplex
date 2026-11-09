package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func rawConfigurationRequest(router http.Handler, method, path, body, revision string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if revision != "" {
		request.Header.Set("If-Match", revision)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
func TestLegacyIAMPreconditionsPreventLostUpdateAndABA(t *testing.T) {
	store := registry.NewMemoryStore()
	if err := store.PutRoleBinding(context.Background(), &models.RoleBinding{ID: "binding", Group: "engineering", Role: models.RoleViewer}); err != nil {
		t.Fatal(err)
	}
	h := api.NewIAMHandler(store, nil)
	router := chi.NewRouter()
	router.Get("/bindings/{id}", h.GetRoleBinding)
	router.Put("/bindings/{id}", h.UpdateRoleBinding)
	router.Delete("/bindings/{id}", h.DeleteRoleBinding)
	read := rawConfigurationRequest(router, "GET", "/bindings/binding", "", "")
	if read.Header().Get("ETag") != "\"1\"" {
		t.Fatal("missing strong revision", read.Header())
	}
	if response := rawConfigurationRequest(router, "PUT", "/bindings/binding", `{"description":"blind"}`, ""); response.Code != 428 {
		t.Fatal("blind replacement allowed", response.Code, response.Body.String())
	}
	updated := rawConfigurationRequest(router, "PUT", "/bindings/binding", `{"description":"first"}`, "\"1\"")
	if updated.Code != 200 || updated.Header().Get("ETag") != "\"2\"" {
		t.Fatal(updated.Code, updated.Body.String())
	}
	for _, method := range []string{"PUT", "DELETE"} {
		if response := rawConfigurationRequest(router, method, "/bindings/binding", `{"description":"stale"}`, "1"); response.Code != 412 {
			t.Fatal("stale writer allowed", method, response.Code)
		}
	}
	if response := rawConfigurationRequest(router, "DELETE", "/bindings/binding", "", "2"); response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
	binding := &models.RoleBinding{ID: "binding", Group: "engineering", Role: models.RoleViewer}
	if err := store.PutRoleBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	if binding.Revision <= 2 {
		t.Fatal("deleted revision reused", binding)
	}
	if response := rawConfigurationRequest(router, "PUT", "/bindings/binding", `{"description":"old incarnation"}`, "2"); response.Code != 412 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, invalid := range []string{"*", "W/\"4\"", "\"\"4\"\"", "4,5", "-1", "04"} {
		if response := rawConfigurationRequest(router, "PUT", "/bindings/binding", `{"description":"invalid"}`, invalid); response.Code != 400 {
			t.Fatal("ambiguous precondition accepted", invalid, response.Code)
		}
	}
}
func TestConcurrentAgentRegistrationClaimsBeforeOAuthWrite(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		<-release
		json.NewEncoder(w).Encode(map[string]string{"client_id": "agent", "client_secret": "one-time"})
	}))
	defer upstream.Close()
	store := registry.NewMemoryStore()
	h := api.NewAgentHandler(store, auth.NewHydraClient(upstream.URL))
	body := `{"client_id":"agent","display_name":"Agent","auth_method":"client_credentials","allowed_scopes":[]}`
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.Register(w, httptest.NewRequest("POST", "/agents", strings.NewReader(body)))
		first <- w
	}()
	<-entered
	second := httptest.NewRecorder()
	h.Register(second, httptest.NewRequest("POST", "/agents", strings.NewReader(body)))
	if second.Code != 409 || calls.Load() != 1 {
		t.Error("second replica touched winning credential", second.Code, calls.Load())
	}
	close(release)
	if response := <-first; response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	agent, err := store.GetAgent(context.Background(), "agent")
	if err != nil || agent.Status != "active" || agent.ClientSecret != "" || agent.Revision != 2 {
		t.Fatal(agent, err)
	}
}

type failAgentActivationStore struct {
	*registry.MemoryStore
	writes int
}

func (s *failAgentActivationStore) PutConfiguration(ctx context.Context, record registry.ConfigurationRecord, expected, generation int64) (registry.ConfigurationRecord, error) {
	s.writes++
	if s.writes == 2 {
		return registry.ConfigurationRecord{}, errors.New("ack unavailable")
	}
	return s.MemoryStore.PutConfiguration(ctx, record, expected, generation)
}
func TestUnacknowledgedAgentActivationRetainsClaimWithoutRollbackDelete(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" {
			t.Error("uncertain activation attempted destructive rollback")
		}
		json.NewEncoder(w).Encode(map[string]string{"client_id": "agent", "client_secret": "never-expose"})
	}))
	defer upstream.Close()
	store := &failAgentActivationStore{MemoryStore: registry.NewMemoryStore()}
	h := api.NewAgentHandler(store, auth.NewHydraClient(upstream.URL))
	response := httptest.NewRecorder()
	h.Register(response, httptest.NewRequest("POST", "/agents", strings.NewReader(`{"client_id":"agent","display_name":"Agent","auth_method":"client_credentials","allowed_scopes":[]}`)))
	if response.Code != 503 || calls.Load() != 1 || strings.Contains(response.Body.String(), "never-expose") {
		t.Fatal(response.Code, calls.Load(), response.Body.String())
	}
	agent, err := store.GetAgent(context.Background(), "agent")
	if err != nil || agent.Status != "provisioning" || agent.Revision != 1 {
		t.Fatal(agent, err)
	}
}
func TestManualScopeReplacementRequiresCurrentRevision(t *testing.T) {
	store := registry.NewMemoryStore()
	principal := "principal-" + strings.Repeat("a", 64)
	h := api.NewAuthHandler(nil, store)
	router := chi.NewRouter()
	router.Get("/users/{userId}/scopes", h.GetUserScopes)
	router.Put("/users/{userId}/scopes", h.SetUserScopes)
	path := "/users/" + principal + "/scopes"
	created := rawConfigurationRequest(router, "PUT", path, `{"scopes":["llm:model:a"]}`, "0")
	if created.Code != 204 {
		t.Fatal(created.Code, created.Body.String())
	}
	if err := store.ModifyUserScopes(context.Background(), principal, func(current []string) ([]string, error) { return append(current, "mcp:tools:reviewed"), nil }); err != nil {
		t.Fatal(err)
	}
	stale := rawConfigurationRequest(router, "PUT", path, `{"scopes":[]}`, "1")
	if stale.Code != 412 {
		t.Fatal(stale.Code, stale.Body.String())
	}
	current := rawConfigurationRequest(router, "GET", path, "", "")
	if current.Header().Get("ETag") != strconv.Quote("2") || !strings.Contains(current.Body.String(), "mcp:tools:reviewed") {
		t.Fatal(current.Code, current.Body.String(), current.Header())
	}
}

func TestRouteRevisionConflictNeverSubmitsAdditionalNativeWrites(t *testing.T) {
	store := registry.NewMemoryStore()
	k8s := &llmK8sRecorder{}
	h := api.NewLLMHandler(store, k8s, "gateway", nil)
	router := chi.NewRouter()
	router.Put("/routes/{modelId}", h.PutRouteConfig)
	router.Get("/routes/{modelId}", h.GetRouteConfig)
	initial := rawConfigurationRequest(router, "PUT", "/routes/model", llmCohesionBody("google"), "0")
	if initial.Code != 200 || initial.Header().Get("ETag") != "\"2\"" {
		t.Fatal(initial.Code, initial.Body.String())
	}
	baseline := len(k8s.applied)
	for _, revision := range []string{"", "0", "1"} {
		result := rawConfigurationRequest(router, "PUT", "/routes/model", llmCohesionBody("anthropic"), revision)
		want := 412
		if revision == "" {
			want = 428
		}
		if result.Code != want || len(k8s.applied) != baseline {
			t.Fatal("unreviewed route reached native apply", result.Code, len(k8s.applied), result.Body.String())
		}
	}
	updated := rawConfigurationRequest(router, "PUT", "/routes/model", llmCohesionBody("anthropic"), initial.Header().Get("ETag"))
	if updated.Code != 200 || updated.Header().Get("ETag") != "\"4\"" {
		t.Fatal(updated.Code, updated.Body.String())
	}
}

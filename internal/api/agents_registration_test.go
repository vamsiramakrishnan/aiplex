package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func TestAgentRegistrationRejectsLocalhostPrefixSpoof(t *testing.T) {
	store := registry.NewMemoryStore()
	handler := api.NewAgentHandler(store)
	request := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(`{"client_id":"client","display_name":"Client","auth_method":"authorization_code","allowed_scopes":["mcp:tools:x"],"redirect_uris":["http://localhost.attacker.example/callback"]}`))
	response := httptest.NewRecorder()
	handler.Register(response, request)
	if response.Code != 400 {
		t.Fatal(response.Code, response.Body.String())
	}
}
func TestConfiguredHydraFailureDoesNotRegisterActiveAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	store := registry.NewMemoryStore()
	handler := api.NewAgentHandler(store, auth.NewHydraClient(server.URL))
	request := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(`{"client_id":"client","display_name":"Client","auth_method":"client_credentials","allowed_scopes":["mcp:tools:x"]}`))
	response := httptest.NewRecorder()
	handler.Register(response, request)
	if response.Code != 502 {
		t.Fatal(response.Code, response.Body.String())
	}
	if pending, e := store.GetAgent(context.Background(), "client"); e != nil || pending.Status != "provisioning" || pending.ClientSecret != "" {
		t.Fatal("issuance failure did not retain a non-active reconciliation claim", pending, e)
	}
}
func TestAgentSecretReturnedOnceAndRevocationFailureRetainsRecord(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"client_id": "client", "client_secret": "one-time-secret"})
	}))
	defer server.Close()
	store := registry.NewMemoryStore()
	handler := api.NewAgentHandler(store, auth.NewHydraClient(server.URL))
	request := httptest.NewRequest("POST", "/api/v1/agents", strings.NewReader(`{"client_id":"client","display_name":"Client","auth_method":"client_credentials","allowed_scopes":["mcp:tools:x"]}`))
	response := httptest.NewRecorder()
	handler.Register(response, request)
	if response.Code != 201 || !strings.Contains(response.Body.String(), "one-time-secret") {
		t.Fatal(response.Code, response.Body.String())
	}
	stored, e := store.GetAgent(context.Background(), "client")
	if e != nil || stored.ClientSecret != "" {
		t.Fatal("secret persisted", e)
	}
	request = httptest.NewRequest("DELETE", "/api/v1/agents/client", nil)
	request.SetPathValue("clientId", "client")
	request.Header.Set("If-Match", strconv.FormatInt(stored.Revision, 10))
	response = httptest.NewRecorder()
	handler.Delete(response, request)
	if response.Code != 502 {
		t.Fatal(response.Code, response.Body.String())
	}
	if _, e := store.GetAgent(context.Background(), "client"); e != nil {
		t.Fatal("record deleted despite revocation failure")
	}
}

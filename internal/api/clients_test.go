package api

import (
	"context"
	"encoding/json"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func clientTestHandler(tenant string, store resources.Store) *ClientHandler {
	return NewClientHandler(store, func(*http.Request) (string, error) { return tenant, nil }, clientplex.ResolverFunc(func(_ context.Context, t, id string) (clientplex.Destination, error) {
		if t != tenant {
			panic("wrong tenant")
		}
		return clientplex.Destination{ID: id, Endpoint: "https://gateway.internal/v1", Protocols: []string{clientplex.Responses}}, nil
	}))
}

const clientTestBody = `{"id":"dev","profile_id":"codex","mode":"gateway","protocol":"openai-responses","destination_ref":"models","principal_binding":"principal:user","model_id":"model-declaration","auth_mode":"gateway-key","credential_env":"AIPLEX_TOKEN","credential_ref":"credentials/user"}`

func clientTestRequest(method, body, revision string) *http.Request {
	r := httptest.NewRequest(method, "/api/v1/clients/registrations/dev", strings.NewReader(body))
	r.SetPathValue("id", "dev")
	if revision != "" {
		r.Header.Set("If-Match", revision)
	}
	return r
}
func TestClientRegistrationCASAndTenantIsolation(t *testing.T) {
	store := resources.NewMemoryStore()
	h := clientTestHandler("a", store)
	w := httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", clientTestBody, ""))
	if w.Code != 201 || w.Header().Get("ETag") != `"1"` {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", clientTestBody, ""))
	if w.Code != 409 {
		t.Fatal("stale overwrite", w.Code)
	}
	w = httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", clientTestBody, `"1"`))
	if w.Code != 200 || w.Header().Get("ETag") != `"2"` {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	clientTestHandler("b", store).Get(w, clientTestRequest("GET", "", ""))
	if w.Code != 404 {
		t.Fatal("cross-tenant read", w.Code)
	}
	w = httptest.NewRecorder()
	h.Delete(w, clientTestRequest("DELETE", "", ""))
	if w.Code != 428 {
		t.Fatal("unversioned deletion", w.Code)
	}
	w = httptest.NewRecorder()
	h.Delete(w, clientTestRequest("DELETE", "", `"1"`))
	if w.Code != 409 {
		t.Fatal("stale deletion", w.Code)
	}
}
func TestClientPlanResolvesSavedDestinationAndRejectsRawEndpoint(t *testing.T) {
	store := resources.NewMemoryStore()
	h := clientTestHandler("a", store)
	w := httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", strings.Replace(clientTestBody, `"profile_id":"codex"`, `"endpoint":"https://attacker.invalid","profile_id":"codex"`, 1), ""))
	if w.Code != 400 {
		t.Fatal("raw endpoint accepted", w.Code)
	}
	w = httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", clientTestBody, ""))
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	h.Plan(w, clientTestRequest("POST", "", ""))
	var p clientplex.Plan
	if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil || w.Code != 200 || len(p.Artifacts) == 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestClientNetworkReferenceCannotCrossTenant(t *testing.T) {
	store := resources.NewMemoryStore()
	_, e := store.Put(context.Background(), resources.Record{TenantID: "b", Kind: "network-profile", ID: "private-network", Spec: json.RawMessage(`{}`)}, 0)
	if e != nil {
		t.Fatal(e)
	}
	h := clientTestHandler("a", store)
	body := strings.Replace(clientTestBody, `"id":"dev"`, `"network_profile_ref":"private-network","id":"dev"`, 1)
	w := httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", body, ""))
	if w.Code != 400 {
		t.Fatal("cross-tenant network ref accepted", w.Code)
	}
	_, e = store.Put(context.Background(), resources.Record{TenantID: "a", Kind: "network-profile", ID: "private-network", Spec: json.RawMessage(`{}`)}, 0)
	if e != nil {
		t.Fatal(e)
	}
	w = httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", body, ""))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	_ = store.Delete(context.Background(), "a", "network-profile", "private-network", 1)
	w = httptest.NewRecorder()
	h.Plan(w, clientTestRequest("POST", "", ""))
	if w.Code != 409 {
		t.Fatal("deleted network ref accepted", w.Code)
	}
}

package aiplex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestLegacySDKCarriesSelectedConfigurationRevisions(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path+" "+r.Header.Get("If-Match"))
		if r.Method == http.MethodDelete || r.URL.Path == "/auth/users/principal-"+strings.Repeat("a", 64)+"/scopes" && r.Method == http.MethodPut {
			w.WriteHeader(204)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"revision": 7, "client_id": "agent", "scopes": []string{}, "model_id": "model"})
	}))
	defer server.Close()
	c := aiplex.NewClient(server.URL)
	ctx := context.Background()
	if _, err := c.PutLLMRoute(ctx, "model", &aiplex.LLMRouteConfig{Revision: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutProvider(ctx, "google", &aiplex.ProviderConfig{Revision: 7}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteAgentAtRevision(ctx, "agent", 7); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteLLMRouteAtRevision(ctx, "model", 7); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetUserScopesAtRevision(ctx, "principal-"+strings.Repeat("a", 64), nil, 7); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if !strings.HasPrefix(call, "GET ") && !strings.HasSuffix(call, " 7") {
			t.Fatal("selected revision omitted", calls)
		}
	}
	before := len(calls)
	if err := c.DeleteAgentAtRevision(ctx, "agent", 0); err == nil {
		t.Fatal("unconditional delete accepted")
	}
	if len(calls) != before {
		t.Fatal("invalid revision dispatched")
	}
}

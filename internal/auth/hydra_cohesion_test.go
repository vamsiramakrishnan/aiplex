package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func TestHydraScopeReplacementUsesNativeJSONPatchAndNoSecretErrors(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "PATCH" || r.URL.Path != "/admin/clients/agent" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("wrong native patch protocol: %s %s %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		var patch []struct {
			Op, Path string
			Value    string
		}
		if json.NewDecoder(r.Body).Decode(&patch) != nil || len(patch) != 1 || patch[0].Op != "replace" || patch[0].Path != "/scope" || patch[0].Value != "llm:model:a mcp:tools:search" {
			t.Errorf("not one exact scope replacement: %+v", patch)
		}
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"client_secret":"never-include-this"}`))
	}))
	defer upstream.Close()
	hydra := NewHydraClient(upstream.URL + "/")
	err := hydra.UpdateClientScopes(context.Background(), "agent", []string{"mcp:tools:search", "llm:model:a", "llm:model:a"})
	if err == nil || strings.Contains(err.Error(), "never-include-this") || calls != 1 {
		t.Fatalf("native failure mishandled: %v calls=%d", err, calls)
	}
	for _, id := range []string{"../other", "agent?unsafe=true", "agent/../other"} {
		if hydra.UpdateClientScopes(context.Background(), id, []string{"llm:model:a"}) == nil {
			t.Fatal("path-controlling client identity accepted")
		}
	}
	if hydra.UpdateClientScopes(context.Background(), "agent", []string{"llm:model:a mcp:tools:unreviewed"}) == nil || calls != 1 {
		t.Fatal("scope entry injected additional native permission")
	}
	if hydra.CreateScope(context.Background(), "llm:model:unreviewed", "discovered") == nil {
		t.Fatal("deprecated no-op reported successful authorization update")
	}
}

func TestHydraAdminMutationDoesNotFollowRedirect(t *testing.T) {
	var escaped atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { escaped.Add(1); w.WriteHeader(204) }))
	defer other.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/admin/clients/agent", 307)
	}))
	defer upstream.Close()
	if err := NewHydraClient(upstream.URL).UpdateClientScopes(context.Background(), "agent", []string{"llm:model:a"}); err == nil || escaped.Load() != 0 {
		t.Fatalf("admin mutation followed redirect: %v calls=%d", err, escaped.Load())
	}
}

func TestExplicitEmptyRoleScopesDoNotFallBackToExecutionDefaults(t *testing.T) {
	store := registry.NewMemoryStore()
	ctx := context.Background()
	_ = store.PutRoleBinding(ctx, &models.RoleBinding{ID: "deny-execution", Group: "group", Role: models.RoleDeployer, Scopes: []string{}})
	access, err := NewWIFValidator(store, WIFConfig{}).ResolveAccess(ctx, &models.WIFIdentity{Subject: "immutable", Provider: "issuer", Groups: []string{"group"}})
	if err != nil || len(access.Scopes) != 0 || !reflect.DeepEqual(access.Roles, []models.IAMRole{models.RoleDeployer}) {
		t.Fatalf("explicit empty scope ceiling expanded role defaults: %+v %v", access, err)
	}
}

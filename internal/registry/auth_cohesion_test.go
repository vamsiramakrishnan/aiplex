package registry

import (
	"context"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"testing"
)

func TestAuthenticationRecordsDoNotShareMutableScopeState(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	agent := &models.Agent{ClientID: "agent", AllowedScopes: []string{"llm:model:reviewed"}, GrantTypes: []string{"client_credentials"}, RedirectURIs: []string{"https://client.example"}, Labels: map[string]string{"owner": "reviewed"}}
	if err := store.PutAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	agent.AllowedScopes[0] = "llm:model:injected"
	agent.Labels["owner"] = "injected"
	got, _ := store.GetAgent(ctx, "agent")
	if got.AllowedScopes[0] != "llm:model:reviewed" || got.Labels["owner"] != "reviewed" {
		t.Fatal("agent write retained caller state")
	}
	got.AllowedScopes[0] = "llm:model:injected"
	got.GrantTypes[0] = "password"
	got.RedirectURIs[0] = "https://unreviewed.example"
	got.Labels["owner"] = "injected"
	listed, _ := store.ListAgents(ctx)
	listed[0].AllowedScopes[0] = "llm:model:injected"
	again, _ := store.GetAgent(ctx, "agent")
	if again.AllowedScopes[0] != "llm:model:reviewed" || again.GrantTypes[0] != "client_credentials" || again.RedirectURIs[0] != "https://client.example" || again.Labels["owner"] != "reviewed" {
		t.Fatal("agent read/list mutated persisted authentication state")
	}
	binding := &models.RoleBinding{ID: "binding", Group: "group", Role: models.RoleDeployer, Scopes: []string{"llm:model:reviewed"}}
	if err := store.PutRoleBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	binding.Scopes[0] = "llm:model:injected"
	read, _ := store.GetRoleBinding(ctx, "binding")
	read.Scopes[0] = "llm:model:changed"
	bindings, _ := store.ListRoleBindings(ctx)
	bindings[0].Scopes[0] = "llm:model:changed"
	grouped, _ := store.ListRoleBindingsByGroup(ctx, "group")
	grouped[0].Scopes[0] = "llm:model:changed"
	againBinding, _ := store.GetRoleBinding(ctx, "binding")
	if againBinding.Scopes[0] != "llm:model:reviewed" {
		t.Fatal("role scope mutation escaped a transaction")
	}
	scopes := []string{"llm:model:reviewed"}
	if err := store.SetUserScopes(ctx, "principal", scopes); err != nil {
		t.Fatal(err)
	}
	scopes[0] = "llm:model:injected"
	readScopes, _ := store.GetUserScopes(ctx, "principal")
	readScopes[0] = "llm:model:changed"
	againScopes, _ := store.GetUserScopes(ctx, "principal")
	if againScopes[0] != "llm:model:reviewed" {
		t.Fatal("manual scope store retained shared slices")
	}
}

func TestRoleBindingPreservesExplicitEmptyScopeCeiling(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.PutRoleBinding(ctx, &models.RoleBinding{ID: "empty", Group: "group", Scopes: []string{}}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetRoleBinding(ctx, "empty")
	if got.Scopes == nil {
		t.Fatal("explicit deny-all scope list became default role scopes")
	}
	if err := store.PutRoleBinding(ctx, &models.RoleBinding{ID: "default", Group: "group"}); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetRoleBinding(ctx, "default")
	if got.Scopes != nil {
		t.Fatal("default role scope semantics changed during cloning")
	}
}

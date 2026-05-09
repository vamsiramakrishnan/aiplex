package deploy_test

import (
	"context"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func TestDeploy_Tool(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	store.PutTemplate(ctx, &models.Template{
		ID:   "kb-search",
		Kind: capability.KindTool,
		Name: "KB Search",
		Capabilities: []capability.Capability{
			{URI: "cap://tool/search@v1", Kind: capability.KindTool, Name: "search", Version: "v1", Description: "Search docs"},
			{URI: "cap://tool/get_document@v1", Kind: capability.KindTool, Name: "get_document", Version: "v1", Description: "Get a document"},
		},
	})

	engine := deploy.NewEngine(store, "test.local")
	inst, err := engine.Deploy(ctx, capability.KindTool, "kb-search", nil, "admin@test.com", "My KB")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	if inst.Kind != capability.KindTool {
		t.Errorf("kind: got %s", inst.Kind)
	}
	if inst.Status != models.StatusRunning {
		t.Errorf("status: got %s", inst.Status)
	}
	if len(inst.Capabilities) != 2 {
		t.Errorf("caps: expected 2, got %d", len(inst.Capabilities))
	}
	if inst.SpiffeID == "" {
		t.Error("expected SPIFFE ID")
	}
	if inst.DisplayName != "My KB" {
		t.Errorf("display_name: got %s", inst.DisplayName)
	}

	history, _ := store.ListHistory(ctx, inst.ID, 10)
	if len(history) != 1 || history[0].Action != "deploy" {
		t.Errorf("history: expected 1 deploy entry, got %+v", history)
	}
}

func TestDeploy_Model(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	store.PutTemplate(ctx, &models.Template{
		ID:      "gemini-2.5-flash",
		Kind:    capability.KindModel,
		ModelID: "gemini-2.5-flash",
		Capabilities: []capability.Capability{
			{URI: "cap://model/gemini-2.5-flash@v1", Kind: capability.KindModel, Name: "gemini-2.5-flash", Version: "v1"},
		},
	})

	engine := deploy.NewEngine(store, "test.local")
	inst, err := engine.Deploy(ctx, capability.KindModel, "gemini-2.5-flash", nil, "admin@test.com", "")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	if inst.SpiffeID != "" {
		t.Error("model kind should not have SPIFFE ID")
	}
	if len(inst.Capabilities) != 1 {
		t.Errorf("caps: expected 1, got %d", len(inst.Capabilities))
	}
}

func TestDeploy_Task(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	store.PutTemplate(ctx, &models.Template{
		ID:   "research-agent",
		Kind: capability.KindTask,
		Capabilities: []capability.Capability{
			{URI: "cap://task/research@v1", Kind: capability.KindTask, Name: "research", Version: "v1"},
			{URI: "cap://task/summarize@v1", Kind: capability.KindTask, Name: "summarize", Version: "v1"},
		},
	})

	engine := deploy.NewEngine(store, "test.local")
	inst, err := engine.Deploy(ctx, capability.KindTask, "research-agent", nil, "admin@test.com", "Research")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	if len(inst.Capabilities) != 2 {
		t.Errorf("caps: expected 2, got %d", len(inst.Capabilities))
	}
	if inst.SpiffeID == "" {
		t.Error("task kind should have SPIFFE ID")
	}
}

func TestUndeploy(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	store.PutTemplate(ctx, &models.Template{
		ID:   "kb-search",
		Kind: capability.KindTool,
		Capabilities: []capability.Capability{
			{URI: "cap://tool/search@v1", Kind: capability.KindTool, Name: "search", Version: "v1"},
		},
	})

	engine := deploy.NewEngine(store, "test.local")
	inst, _ := engine.Deploy(ctx, capability.KindTool, "kb-search", nil, "admin@test.com", "")

	err := engine.Undeploy(ctx, inst.ID, "admin@test.com")
	if err != nil {
		t.Fatalf("Undeploy: %v", err)
	}

	got, _ := store.GetInstance(ctx, inst.ID)
	if got.Status != models.StatusTerminated {
		t.Errorf("expected terminated, got %s", got.Status)
	}

	history, _ := store.ListHistory(ctx, inst.ID, 10)
	if len(history) != 2 {
		t.Errorf("expected 2 history entries (deploy+undeploy), got %d", len(history))
	}
}

func TestDeploy_TemplateNotFound(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	engine := deploy.NewEngine(store, "test.local")

	_, err := engine.Deploy(ctx, capability.KindTool, "nonexistent", nil, "admin@test.com", "")
	if err == nil {
		t.Error("expected error for nonexistent template")
	}
}

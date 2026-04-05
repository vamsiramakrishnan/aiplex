package deploy_test

import (
	"context"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func TestDeploy_MCPlex(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	store.PutTemplate(ctx, &models.Template{
		ID:    "kb-search",
		Plane: models.PlaneMCPlex,
		Name:  "KB Search",
		Tools: []models.ToolInfo{
			{Name: "search", Description: "Search docs"},
			{Name: "get_document", Description: "Get a document"},
		},
	})

	engine := deploy.NewEngine(store, "test.local")
	inst, err := engine.Deploy(ctx, models.PlaneMCPlex, "kb-search", nil, "admin@test.com", "My KB")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	if inst.Plane != models.PlaneMCPlex {
		t.Errorf("plane: got %s", inst.Plane)
	}
	if inst.Status != models.StatusRunning {
		t.Errorf("status: got %s", inst.Status)
	}
	if len(inst.Scopes) != 2 {
		t.Errorf("scopes: expected 2, got %d", len(inst.Scopes))
	}
	if inst.SpiffeID == "" {
		t.Error("expected SPIFFE ID")
	}
	if inst.DisplayName != "My KB" {
		t.Errorf("display_name: got %s", inst.DisplayName)
	}

	// Verify history was recorded
	history, _ := store.ListHistory(ctx, inst.ID, 10)
	if len(history) != 1 || history[0].Action != "deploy" {
		t.Errorf("history: expected 1 deploy entry, got %+v", history)
	}
}

func TestDeploy_LLMPlex(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	store.PutTemplate(ctx, &models.Template{
		ID:           "gemini-2.5-flash",
		Plane:        models.PlaneLLMPlex,
		ModelID:      "gemini-2.5-flash",
		Capabilities: []string{"text", "vision"},
	})

	engine := deploy.NewEngine(store, "test.local")
	inst, err := engine.Deploy(ctx, models.PlaneLLMPlex, "gemini-2.5-flash", nil, "admin@test.com", "")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	if inst.SpiffeID != "" {
		t.Error("LLMPlex should not have SPIFFE ID")
	}
	if len(inst.Scopes) != 3 { // model + 2 capabilities
		t.Errorf("scopes: expected 3, got %d: %v", len(inst.Scopes), inst.Scopes)
	}
}

func TestDeploy_A2APlex(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	store.PutTemplate(ctx, &models.Template{
		ID:        "research-agent",
		Plane:     models.PlaneA2APlex,
		TaskTypes: []string{"research", "summarize"},
	})

	engine := deploy.NewEngine(store, "test.local")
	inst, err := engine.Deploy(ctx, models.PlaneA2APlex, "research-agent", nil, "admin@test.com", "Research")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	if len(inst.Scopes) != 2 {
		t.Errorf("scopes: expected 2, got %d", len(inst.Scopes))
	}
	if inst.SpiffeID == "" {
		t.Error("A2APlex should have SPIFFE ID")
	}
}

func TestUndeploy(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	store.PutTemplate(ctx, &models.Template{
		ID:    "kb-search",
		Plane: models.PlaneMCPlex,
		Tools: []models.ToolInfo{{Name: "search"}},
	})

	engine := deploy.NewEngine(store, "test.local")
	inst, _ := engine.Deploy(ctx, models.PlaneMCPlex, "kb-search", nil, "admin@test.com", "")

	err := engine.Undeploy(ctx, inst.ID, "admin@test.com")
	if err != nil {
		t.Fatalf("Undeploy: %v", err)
	}

	got, _ := store.GetInstance(ctx, inst.ID)
	if got.Status != models.StatusTerminated {
		t.Errorf("expected terminated, got %s", got.Status)
	}

	// Verify undeploy history
	history, _ := store.ListHistory(ctx, inst.ID, 10)
	if len(history) != 2 {
		t.Errorf("expected 2 history entries (deploy+undeploy), got %d", len(history))
	}
}

func TestDeploy_TemplateNotFound(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	engine := deploy.NewEngine(store, "test.local")

	_, err := engine.Deploy(ctx, models.PlaneMCPlex, "nonexistent", nil, "admin@test.com", "")
	if err == nil {
		t.Error("expected error for nonexistent template")
	}
}

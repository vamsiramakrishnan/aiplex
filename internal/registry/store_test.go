package registry_test

import (
	"context"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func TestMemoryStore_InstanceCRUD(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()

	// Put
	inst := &models.Instance{
		ID:         "test-123",
		Plane:      models.PlaneMCPlex,
		TemplateID: "kb-search",
		Owner:      "admin@test.com",
		Namespace:  "mcplex",
		Status:     models.StatusRunning,
		Scopes:     []string{"mcp:tools:search"},
	}
	if err := store.PutInstance(ctx, inst); err != nil {
		t.Fatalf("PutInstance: %v", err)
	}

	// Get
	got, err := store.GetInstance(ctx, "test-123")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if got.ID != "test-123" || got.Plane != models.PlaneMCPlex {
		t.Errorf("GetInstance: got %+v", got)
	}

	// List by plane
	list, err := store.ListInstances(ctx, models.PlaneMCPlex)
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("ListInstances: expected 1, got %d", len(list))
	}

	// List all
	list, err = store.ListInstances(ctx, "")
	if err != nil {
		t.Fatalf("ListInstances all: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("ListInstances all: expected 1, got %d", len(list))
	}

	// List wrong plane
	list, err = store.ListInstances(ctx, models.PlaneA2APlex)
	if err != nil {
		t.Fatalf("ListInstances a2aplex: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListInstances a2aplex: expected 0, got %d", len(list))
	}

	// Delete
	if err := store.DeleteInstance(ctx, "test-123"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	_, err = store.GetInstance(ctx, "test-123")
	if err != registry.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestMemoryStore_TemplatePagination(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()

	for i := 0; i < 25; i++ {
		store.PutTemplate(ctx, &models.Template{
			ID:    "tmpl-" + string(rune('a'+i)),
			Plane: models.PlaneMCPlex,
			Name:  "Template " + string(rune('A'+i)),
		})
	}

	page, total, err := store.ListTemplates(ctx, models.PlaneMCPlex, 0, 10)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if total != 25 {
		t.Errorf("expected total 25, got %d", total)
	}
	if len(page) != 10 {
		t.Errorf("expected page size 10, got %d", len(page))
	}

	// Page beyond end
	page, _, err = store.ListTemplates(ctx, models.PlaneMCPlex, 3, 10)
	if err != nil {
		t.Fatalf("ListTemplates page 3: %v", err)
	}
	if len(page) != 0 {
		t.Errorf("expected 0 on page 3, got %d", len(page))
	}
}

func TestMemoryStore_AgentCRUD(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()

	agent := &models.Agent{
		ClientID:      "tutor-agent",
		DisplayName:   "Tutor Agent",
		AuthMethod:    "client_credentials",
		AllowedScopes: []string{"mcp:tools:search", "llm:model:gemini-2.5-flash"},
		Status:        "active",
	}
	if err := store.PutAgent(ctx, agent); err != nil {
		t.Fatalf("PutAgent: %v", err)
	}

	got, err := store.GetAgent(ctx, "tutor-agent")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.DisplayName != "Tutor Agent" {
		t.Errorf("GetAgent: got %+v", got)
	}

	list, err := store.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("ListAgents: expected 1, got %d", len(list))
	}

	if err := store.DeleteAgent(ctx, "tutor-agent"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	_, err = store.GetAgent(ctx, "tutor-agent")
	if err != registry.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_History(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()

	for i := 0; i < 5; i++ {
		store.AppendHistory(ctx, &models.DeployHistory{
			ID:         "h" + string(rune('0'+i)),
			InstanceID: "inst-1",
			Action:     "deploy",
		})
	}

	history, err := store.ListHistory(ctx, "inst-1", 3)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(history) != 3 {
		t.Errorf("expected 3 history entries, got %d", len(history))
	}
	// Most recent first
	if history[0].ID != "h4" {
		t.Errorf("expected most recent first, got %s", history[0].ID)
	}
}

func TestMemoryStore_UserScopes(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()

	scopes := []string{"mcp:tools:search", "llm:model:gemini-2.5-flash"}
	if err := store.SetUserScopes(ctx, "user@test.com", scopes); err != nil {
		t.Fatalf("SetUserScopes: %v", err)
	}

	got, err := store.GetUserScopes(ctx, "user@test.com")
	if err != nil {
		t.Fatalf("GetUserScopes: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 scopes, got %d", len(got))
	}

	// Non-existent user returns nil
	got, err = store.GetUserScopes(ctx, "nobody@test.com")
	if err != nil {
		t.Fatalf("GetUserScopes nonexistent: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for nonexistent user, got %v", got)
	}
}

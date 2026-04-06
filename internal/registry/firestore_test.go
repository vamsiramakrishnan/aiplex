package registry

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

func skipWithoutEmulator(t *testing.T) {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set")
	}
}

func TestFirestoreInstance(t *testing.T) {
	skipWithoutEmulator(t)

	store, err := NewFirestoreStore("test-project", "")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	inst := &models.Instance{
		ID:         "test-instance-1",
		Plane:      models.PlaneMCPlex,
		TemplateID: "kb-search-server",
		Owner:      "test@example.com",
		Namespace:  "mcplex",
		Scopes:     []string{"mcp:tools:search"},
		Status:     models.StatusRunning,
		DeployedAt: time.Now(),
	}

	// Put
	if err := store.PutInstance(ctx, inst); err != nil {
		t.Fatalf("PutInstance failed: %v", err)
	}

	// Get
	retrieved, err := store.GetInstance(ctx, inst.ID)
	if err != nil {
		t.Fatalf("GetInstance failed: %v", err)
	}
	if retrieved.ID != inst.ID || retrieved.Plane != inst.Plane {
		t.Errorf("retrieved instance mismatch: got %v, want %v", retrieved, inst)
	}

	// List
	instances, err := store.ListInstances(ctx, models.PlaneMCPlex)
	if err != nil {
		t.Fatalf("ListInstances failed: %v", err)
	}
	if len(instances) == 0 {
		t.Error("expected at least one instance")
	}

	// Delete
	if err := store.DeleteInstance(ctx, inst.ID); err != nil {
		t.Fatalf("DeleteInstance failed: %v", err)
	}

	// Verify deleted
	_, err = store.GetInstance(ctx, inst.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestFirestoreAgent(t *testing.T) {
	skipWithoutEmulator(t)

	store, err := NewFirestoreStore("test-project", "")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	agent := &models.Agent{
		ClientID:      "test-agent-1",
		DisplayName:   "Test Agent",
		AuthMethod:    "client_credentials",
		GrantTypes:    []string{"client_credentials"},
		AllowedScopes: []string{"mcp:tools:search"},
		RegisteredAt:  time.Now(),
		RegisteredBy:  "admin@example.com",
		Status:        "active",
	}

	// Put
	if err := store.PutAgent(ctx, agent); err != nil {
		t.Fatalf("PutAgent failed: %v", err)
	}

	// Get
	retrieved, err := store.GetAgent(ctx, agent.ClientID)
	if err != nil {
		t.Fatalf("GetAgent failed: %v", err)
	}
	if retrieved.ClientID != agent.ClientID {
		t.Errorf("retrieved agent mismatch: got %v, want %v", retrieved, agent)
	}

	// List
	agents, err := store.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}
	if len(agents) == 0 {
		t.Error("expected at least one agent")
	}

	// Delete
	if err := store.DeleteAgent(ctx, agent.ClientID); err != nil {
		t.Fatalf("DeleteAgent failed: %v", err)
	}

	// Verify deleted
	_, err = store.GetAgent(ctx, agent.ClientID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestFirestoreTemplate(t *testing.T) {
	skipWithoutEmulator(t)

	store, err := NewFirestoreStore("test-project", "")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	template := &models.Template{
		ID:          "test-template-1",
		Source:      "local",
		Plane:       models.PlaneMCPlex,
		Name:        "Test Template",
		Description: "A test template",
		Image:       "gcr.io/test/server:latest",
		Category:    "knowledge",
		Tools: []models.ToolInfo{
			{Name: "search", Description: "Search tool"},
		},
		Verified:  true,
		CreatedAt: time.Now(),
	}

	// Put
	if err := store.PutTemplate(ctx, template); err != nil {
		t.Fatalf("PutTemplate failed: %v", err)
	}

	// Get
	retrieved, err := store.GetTemplate(ctx, template.ID)
	if err != nil {
		t.Fatalf("GetTemplate failed: %v", err)
	}
	if retrieved.ID != template.ID || retrieved.Name != template.Name {
		t.Errorf("retrieved template mismatch: got %v, want %v", retrieved, template)
	}

	// List
	templates, total, err := store.ListTemplates(ctx, models.PlaneMCPlex, 0, 10)
	if err != nil {
		t.Fatalf("ListTemplates failed: %v", err)
	}
	if total == 0 {
		t.Error("expected at least one template")
	}
	if len(templates) == 0 {
		t.Error("expected at least one template in page")
	}
}

func TestFirestoreDeployHistory(t *testing.T) {
	skipWithoutEmulator(t)

	store, err := NewFirestoreStore("test-project", "")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	history := &models.DeployHistory{
		InstanceID:  "test-instance-1",
		Action:      "deploy",
		Plane:       models.PlaneMCPlex,
		TemplateID:  "kb-search-server",
		Owner:       "test@example.com",
		PerformedBy: "test@example.com",
		Timestamp:   time.Now(),
		Success:     true,
	}

	// Append
	if err := store.AppendHistory(ctx, history); err != nil {
		t.Fatalf("AppendHistory failed: %v", err)
	}

	// List
	records, err := store.ListHistory(ctx, history.InstanceID, 10)
	if err != nil {
		t.Fatalf("ListHistory failed: %v", err)
	}
	if len(records) == 0 {
		t.Error("expected at least one history record")
	}
}

func TestFirestoreUserScopes(t *testing.T) {
	skipWithoutEmulator(t)

	store, err := NewFirestoreStore("test-project", "")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	userID := "test-user-1"
	scopes := []string{"mcp:tools:search", "a2a:task:research"}

	// Set
	if err := store.SetUserScopes(ctx, userID, scopes); err != nil {
		t.Fatalf("SetUserScopes failed: %v", err)
	}

	// Get
	retrieved, err := store.GetUserScopes(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserScopes failed: %v", err)
	}
	if len(retrieved) != len(scopes) {
		t.Errorf("scope count mismatch: got %d, want %d", len(retrieved), len(scopes))
	}

	// Get non-existent user (should return nil, not error)
	empty, err := store.GetUserScopes(ctx, "non-existent-user")
	if err != nil {
		t.Fatalf("GetUserScopes for non-existent user failed: %v", err)
	}
	if empty != nil {
		t.Errorf("expected nil scopes for non-existent user, got: %v", empty)
	}
}

func TestFirestoreLLMRouteConfig(t *testing.T) {
	skipWithoutEmulator(t)

	store, err := NewFirestoreStore("test-project", "")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	config := &models.LLMRouteConfig{
		ID:      "test-route-1",
		ModelID: "gemini-2.5-flash",
		Owner:   "test@example.com",
		Backends: []models.LLMBackend{
			{Provider: "google", ModelID: "gemini-2.5-flash", Weight: 100, Enabled: true},
		},
		CreatedAt: time.Now(),
	}

	// Put
	if err := store.PutRouteConfig(ctx, config); err != nil {
		t.Fatalf("PutRouteConfig failed: %v", err)
	}

	// Get
	retrieved, err := store.GetRouteConfig(ctx, config.ModelID)
	if err != nil {
		t.Fatalf("GetRouteConfig failed: %v", err)
	}
	if retrieved.ModelID != config.ModelID {
		t.Errorf("retrieved config mismatch: got %v, want %v", retrieved, config)
	}

	// List
	configs, err := store.ListRouteConfigs(ctx)
	if err != nil {
		t.Fatalf("ListRouteConfigs failed: %v", err)
	}
	if len(configs) == 0 {
		t.Error("expected at least one route config")
	}

	// Delete
	if err := store.DeleteRouteConfig(ctx, config.ModelID); err != nil {
		t.Fatalf("DeleteRouteConfig failed: %v", err)
	}

	// Verify deleted
	_, err = store.GetRouteConfig(ctx, config.ModelID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

func TestFirestoreRoleBinding(t *testing.T) {
	skipWithoutEmulator(t)

	store, err := NewFirestoreStore("test-project", "")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	binding := &models.RoleBinding{
		ID:          "test-binding-1",
		Group:       "aiplex-admins",
		Role:        models.RoleAdmin,
		Scopes:      []string{"mcp:tools:*", "a2a:task:*"},
		Description: "Admin group binding",
		CreatedAt:   time.Now(),
		CreatedBy:   "admin@example.com",
	}

	// Put
	if err := store.PutRoleBinding(ctx, binding); err != nil {
		t.Fatalf("PutRoleBinding failed: %v", err)
	}

	// Get
	retrieved, err := store.GetRoleBinding(ctx, binding.ID)
	if err != nil {
		t.Fatalf("GetRoleBinding failed: %v", err)
	}
	if retrieved.ID != binding.ID || retrieved.Group != binding.Group {
		t.Errorf("retrieved binding mismatch: got %v, want %v", retrieved, binding)
	}

	// List
	bindings, err := store.ListRoleBindings(ctx)
	if err != nil {
		t.Fatalf("ListRoleBindings failed: %v", err)
	}
	if len(bindings) == 0 {
		t.Error("expected at least one role binding")
	}

	// List by group
	groupBindings, err := store.ListRoleBindingsByGroup(ctx, binding.Group)
	if err != nil {
		t.Fatalf("ListRoleBindingsByGroup failed: %v", err)
	}
	if len(groupBindings) == 0 {
		t.Error("expected at least one role binding for group")
	}

	// Delete
	if err := store.DeleteRoleBinding(ctx, binding.ID); err != nil {
		t.Fatalf("DeleteRoleBinding failed: %v", err)
	}

	// Verify deleted
	_, err = store.GetRoleBinding(ctx, binding.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got: %v", err)
	}
}

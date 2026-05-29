package deploy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// PR 11 item 16: refuse runtime mutation in place.

func deployTreasuryConfig(extra map[string]any) map[string]any {
	cfg := map[string]any{
		"runtime": map[string]any{
			"engine":  "tape",
			"durable": true,
			"store":   map[string]any{"type": "sqlite"},
			"reactors": map[string]any{
				"recovery": true, "reconciler": true, "timers": true,
				"outbox": true, "compensation": true,
			},
			"outbox": map[string]any{"sink": "log"},
		},
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

func seedTreasury(t *testing.T, store *registry.MemoryStore) {
	t.Helper()
	if err := store.PutTemplate(context.Background(), &models.Template{
		ID: "treasury", Plane: models.PlaneA2APlex, TaskTypes: []string{"sweep"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRefuseRuntimeMutation_ChangingEngine(t *testing.T) {
	store := registry.NewMemoryStore()
	seedTreasury(t, store)
	engine := deploy.NewEngine(store, "test.local")

	ctx := context.Background()
	if _, err := engine.Deploy(ctx, models.PlaneA2APlex, "treasury",
		deployTreasuryConfig(nil), "owner@x", "Treasury"); err != nil {
		t.Fatal(err)
	}

	// Second deploy with engine=none and same display_name → reject.
	noneConfig := map[string]any{
		"runtime": map[string]any{"engine": "none"},
	}
	_, err := engine.Deploy(ctx, models.PlaneA2APlex, "treasury",
		noneConfig, "owner@x", "Treasury")
	if err == nil {
		t.Fatal("expected ErrRuntimeMutation, got nil")
	}
	if !errors.Is(err, deploy.ErrRuntimeMutation) {
		t.Errorf("expected ErrRuntimeMutation, got %v", err)
	}
}

func TestRefuseRuntimeMutation_SameConfigPasses(t *testing.T) {
	store := registry.NewMemoryStore()
	seedTreasury(t, store)
	engine := deploy.NewEngine(store, "test.local")

	ctx := context.Background()
	if _, err := engine.Deploy(ctx, models.PlaneA2APlex, "treasury",
		deployTreasuryConfig(nil), "owner@x", "Treasury"); err != nil {
		t.Fatal(err)
	}
	// Second deploy with the SAME runtime config (e.g. re-applying the
	// same YAML) is the dev-friendly path.
	if _, err := engine.Deploy(ctx, models.PlaneA2APlex, "treasury",
		deployTreasuryConfig(nil), "owner@x", "Treasury"); err != nil {
		t.Errorf("same-config redeploy should pass, got %v", err)
	}
}

func TestRefuseRuntimeMutation_DifferentDisplayName_TreatedAsDifferentInstance(t *testing.T) {
	store := registry.NewMemoryStore()
	seedTreasury(t, store)
	engine := deploy.NewEngine(store, "test.local")

	ctx := context.Background()
	if _, err := engine.Deploy(ctx, models.PlaneA2APlex, "treasury",
		deployTreasuryConfig(nil), "owner@x", "Treasury Prod"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Deploy(ctx, models.PlaneA2APlex, "treasury",
		map[string]any{"runtime": map[string]any{"engine": "none"}},
		"owner@x", "Treasury Dev"); err != nil {
		t.Errorf("different display name should be a separate instance, got %v", err)
	}
}

func TestRefuseRuntimeMutation_ForceBypasses(t *testing.T) {
	store := registry.NewMemoryStore()
	seedTreasury(t, store)
	engine := deploy.NewEngine(store, "test.local")

	ctx := context.Background()
	if _, err := engine.Deploy(ctx, models.PlaneA2APlex, "treasury",
		deployTreasuryConfig(nil), "owner@x", "Treasury"); err != nil {
		t.Fatal(err)
	}
	bypass := map[string]any{
		"runtime":              map[string]any{"engine": "none"},
		"force_runtime_change": true,
	}
	if _, err := engine.Deploy(ctx, models.PlaneA2APlex, "treasury",
		bypass, "owner@x", "Treasury"); err != nil {
		t.Errorf("force_runtime_change=true should bypass, got %v", err)
	}
}

// PR 11 item 17: tape-server lifecycle status counters.

func TestCountInstancesWithRuntime(t *testing.T) {
	store := registry.NewMemoryStore()
	ctx := context.Background()

	check := func(want int) {
		t.Helper()
		got, err := store.CountInstancesWithRuntime(ctx, models.RuntimeEngineTape)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("expected %d tape instances, got %d", want, got)
		}
	}
	check(0)

	_ = store.PutInstance(ctx, &models.Instance{ID: "a", Runtime: models.TapeRuntime()})
	check(1)
	_ = store.PutInstance(ctx, &models.Instance{ID: "b", Runtime: models.TapeRuntime()})
	check(2)
	_ = store.PutInstance(ctx, &models.Instance{ID: "c", Runtime: models.NoneRuntime()})
	check(2) // non-tape doesn't count

	_ = store.DeleteInstance(ctx, "a")
	check(1)
	_ = store.DeleteInstance(ctx, "b")
	check(0)
}

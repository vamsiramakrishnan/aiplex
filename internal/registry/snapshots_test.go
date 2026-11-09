package registry

import (
	"context"
	"math"
	"sync"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

func TestMemoryLifecycleChangesRequireExplicitWrite(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	input := &models.Instance{ID: "one", Status: models.StatusProvisioning, Scopes: []string{"original"}, Config: map[string]any{"nested": map[string]any{"values": []string{"original"}}}, Health: &models.HealthStatus{Status: "unknown"}, Resources: []models.ResourceReference{{Name: "original"}}}
	if err := store.PutInstance(ctx, input); err != nil {
		t.Fatal(err)
	}
	input.Status = models.StatusRunning
	input.Config["nested"].(map[string]any)["values"].([]string)[0] = "changed"
	got, err := store.GetInstance(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.StatusProvisioning || got.Config["nested"].(map[string]any)["values"].([]string)[0] != "original" {
		t.Fatal("caller mutated stored deployment without a write")
	}
	got.Status = models.StatusFailed
	got.Scopes[0] = "changed"
	got.Health.Status = "changed"
	got.Resources[0].Name = "changed"
	list, _ := store.ListInstances(ctx, "")
	list[0].Config["nested"].(map[string]any)["values"].([]string)[0] = "changed"
	stored, _ := store.GetInstance(ctx, "one")
	if stored.Status != models.StatusProvisioning || stored.Scopes[0] != "original" || stored.Health.Status != "unknown" || stored.Resources[0].Name != "original" || stored.Config["nested"].(map[string]any)["values"].([]string)[0] != "original" {
		t.Fatal("read snapshot changed stored deployment")
	}
	if err := store.PutInstance(ctx, got); err != nil {
		t.Fatal(err)
	}
	stored, _ = store.GetInstance(ctx, "one")
	if stored.Status != models.StatusFailed {
		t.Fatal("explicit lifecycle write was lost")
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 40; j++ {
				item, _ := store.GetInstance(ctx, "one")
				item.Config["nested"].(map[string]any)["values"].([]string)[0] = "local"
				_, _ = store.ListInstances(ctx, "")
			}
		}()
	}
	workers.Wait()
}

func TestMemoryCatalogAndRoutingSnapshotsRemainIsolated(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	template := &models.Template{ID: "t", Name: "A", Plane: models.PlaneLLMPlex, Capabilities: []string{"original"}, AgentCard: map[string]any{"nested": map[string]any{"name": "original"}}, Pricing: &models.Pricing{Input: 1}}
	if err := store.PutTemplate(ctx, template); err != nil {
		t.Fatal(err)
	}
	template.Pricing.Input = 100
	page, _, _ := store.ListTemplates(ctx, "", 0, 10)
	page[0].Capabilities[0] = "changed"
	page[0].AgentCard["nested"].(map[string]any)["name"] = "changed"
	current, _ := store.GetTemplate(ctx, "t")
	if current.Pricing.Input != 1 || current.Capabilities[0] != "original" || current.AgentCard["nested"].(map[string]any)["name"] != "original" {
		t.Fatal("catalog snapshots share mutable state")
	}
	route := &models.LLMRouteConfig{ModelID: "m", Backends: []models.LLMBackend{{Provider: "original"}}, Fallbacks: []string{"original"}, Budget: &models.UsageBudget{MaxDailyTokens: 5}}
	if err := store.PutRouteConfig(ctx, route); err != nil {
		t.Fatal(err)
	}
	route.Backends[0].Provider = "changed"
	routes, _ := store.ListRouteConfigs(ctx)
	routes[0].Fallbacks[0] = "changed"
	routes[0].Budget.MaxDailyTokens = 100
	saved, _ := store.GetRouteConfig(ctx, "m")
	if saved.Backends[0].Provider != "original" || saved.Fallbacks[0] != "original" || saved.Budget.MaxDailyTokens != 5 {
		t.Fatal("route read or caller changed persisted desired state")
	}
	provider := &models.ProviderConfig{Provider: "google", SecretRef: "original"}
	_ = store.PutProviderConfig(ctx, provider)
	provider.SecretRef = "changed"
	config, _ := store.GetProviderConfig(ctx, "google")
	if config.SecretRef != "original" {
		t.Fatal("provider changed without persistence")
	}
	if _, _, err := store.ListTemplates(ctx, "", -1, 10); err == nil {
		t.Fatal("negative pagination accepted")
	}
	if _, _, err := store.ListTemplates(ctx, "", 0, 0); err == nil {
		t.Fatal("zero page size accepted")
	}
	if page, _, err := store.ListTemplates(ctx, "", math.MaxInt, math.MaxInt); err != nil || len(page) != 0 {
		t.Fatal("overflowing catalog page", err)
	}
}

package deploy_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

func customRoute(_ *models.Instance, _ *models.Template, _ string) ([]deploy.Manifest, error) {
	return []deploy.Manifest{{APIVersion: "gateway.networking.k8s.io/v1", Kind: "HTTPRoute", Name: "custom", Namespace: "custom", YAML: "kind: HTTPRoute"}}, nil
}

func TestRouteRegistryIndependentModuleRegistration(t *testing.T) {
	r := deploy.NewRouteRegistry()
	if err := r.Register("custom", customRoute); err != nil {
		t.Fatal(err)
	}
	if r.Has(models.PlaneLLMPlex) {
		t.Fatal("empty composition unexpectedly enabled a bundled module")
	}
	got, err := r.Generate(&models.Instance{Plane: "custom"}, &models.Template{Plane: "custom"}, "gateway")
	if err != nil || len(got) != 1 || got[0].Name != "custom" {
		t.Fatalf("custom module routes: %v, %v", got, err)
	}
	if err := r.Register("custom", customRoute); !errors.Is(err, deploy.ErrRouteGeneratorDuplicate) {
		t.Fatalf("duplicate ownership: %v", err)
	}
	if _, err := deploy.NewRouteRegistry().Generate(&models.Instance{Plane: "custom"}, &models.Template{}, "gateway"); !errors.Is(err, deploy.ErrRouteGeneratorMissing) {
		t.Fatalf("registration leaked across compositions: %v", err)
	}
}

func TestRouteRegistryRejectsInvalidInputs(t *testing.T) {
	r := deploy.NewBuiltinRouteRegistry()
	for _, tc := range []struct {
		name    string
		inst    *models.Instance
		tmpl    *models.Template
		gateway string
	}{
		{"nil instance", nil, &models.Template{}, "gateway"},
		{"nil template", &models.Instance{}, nil, "gateway"},
		{"mismatched ownership", &models.Instance{Plane: models.PlaneLLMPlex}, &models.Template{Plane: models.PlaneMCPlex}, "gateway"},
		{"unknown module", &models.Instance{Plane: "disabled"}, &models.Template{}, "gateway"},
		{"missing gateway", &models.Instance{Plane: models.PlaneLLMPlex}, &models.Template{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := r.Generate(tc.inst, tc.tmpl, tc.gateway); err == nil || len(got) != 0 {
				t.Fatalf("expected no partial resources on error: %v, %v", got, err)
			}
		})
	}
	if err := r.Register("", customRoute); err == nil {
		t.Fatal("empty module accepted")
	}
	if err := r.Register(" bad ", customRoute); err == nil {
		t.Fatal("noncanonical module accepted")
	}
	if err := r.Register("custom", nil); err == nil {
		t.Fatal("nil generator accepted")
	}
}

func TestRouteRegistryGeneratorErrorsAndEmptyResults(t *testing.T) {
	failure := errors.New("module configuration invalid")
	for _, tc := range []struct {
		name      string
		generator deploy.RouteGenerator
		want      error
	}{
		{"generator failure", func(*models.Instance, *models.Template, string) ([]deploy.Manifest, error) {
			return []deploy.Manifest{{Name: "partial"}}, failure
		}, failure},
		{"empty output", func(*models.Instance, *models.Template, string) ([]deploy.Manifest, error) { return nil, nil }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := deploy.NewRouteRegistry()
			if err := r.Register("custom", tc.generator); err != nil {
				t.Fatal(err)
			}
			got, err := r.Generate(&models.Instance{Plane: "custom"}, &models.Template{}, "gateway")
			if len(got) != 0 || err == nil {
				t.Fatalf("partial or successful output: %v, %v", got, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("lost error cause: %v", err)
			}
		})
	}
}

func TestRouteRegistryConcurrentDuplicateRegistration(t *testing.T) {
	r := deploy.NewRouteRegistry()
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Register("custom", customRoute); err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			} else if !errors.Is(err, deploy.ErrRouteGeneratorDuplicate) {
				t.Errorf("registration: %v", err)
			}
			r.Has("custom")
		}()
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("accepted %d owners", accepted)
	}
}

type routeCountingK8s struct{ applied, deleted int }

func (k *routeCountingK8s) Apply(context.Context, deploy.Manifest) error { k.applied++; return nil }
func (k *routeCountingK8s) Delete(context.Context, string, string, string, string) error {
	k.deleted++
	return nil
}

func TestEngineRejectsDisabledMismatchedAndBrokenModulesBeforeSideEffects(t *testing.T) {
	for _, name := range []string{"disabled", "mismatched", "broken"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := registry.NewMemoryStore()
			tmpl := &models.Template{ID: "test", Plane: models.PlaneLLMPlex}
			routes := deploy.NewRouteRegistry()
			plane := models.PlaneLLMPlex
			switch name {
			case "mismatched":
				plane = models.PlaneA2APlex
				routes = deploy.NewBuiltinRouteRegistry()
			case "broken":
				if err := routes.Register(plane, func(*models.Instance, *models.Template, string) ([]deploy.Manifest, error) {
					return nil, errors.New("bad routes")
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.PutTemplate(ctx, tmpl); err != nil {
				t.Fatal(err)
			}
			k8s := &routeCountingK8s{}
			engine := deploy.NewEngineWithK8s(store, k8s, "test.local", "gateway").WithRouteRegistry(routes)
			if _, err := engine.Deploy(ctx, plane, tmpl.ID, nil, "owner", ""); err == nil {
				t.Fatal("invalid module deployment succeeded")
			}
			instances, err := store.ListInstances(ctx, "")
			if err != nil || len(instances) != 0 || k8s.applied != 0 {
				t.Fatalf("side effects before module validation: instances=%v, applies=%d, err=%v", instances, k8s.applied, err)
			}
		})
	}
}

func TestSelectBuiltinRouteModules(t *testing.T) {
	r, err := deploy.NewRouteRegistryForPlanes(models.PlaneLLMPlex)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Has(models.PlaneLLMPlex) || r.Has(models.PlaneMCPlex) {
		t.Fatal("module selection ignored")
	}
	if _, err := deploy.NewRouteRegistryForPlanes("unknown"); !errors.Is(err, deploy.ErrRouteGeneratorMissing) {
		t.Fatalf("unknown module: %v", err)
	}
	if _, err := deploy.NewRouteRegistryForPlanes(models.PlaneLLMPlex, models.PlaneLLMPlex); !errors.Is(err, deploy.ErrRouteGeneratorDuplicate) {
		t.Fatalf("duplicate module: %v", err)
	}
	empty, err := deploy.NewRouteRegistryForPlanes()
	if err != nil || empty.Has(models.PlaneLLMPlex) {
		t.Fatal("empty selection enables routes")
	}
}

func TestEngineCleanupSurvivesDisabledModuleAndMissingTemplate(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	if err := store.PutTemplate(ctx, &models.Template{ID: "model", Plane: models.PlaneLLMPlex, ModelID: "model"}); err != nil {
		t.Fatal(err)
	}
	k8s := &routeCountingK8s{}
	engine := deploy.NewEngineWithK8s(store, k8s, "test.local", "gateway")
	inst, err := engine.Deploy(ctx, models.PlaneLLMPlex, "model", nil, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inst.Resources) == 0 {
		t.Fatal("resource inventory not persisted")
	}
	// Replace the template ID with a nonexistent one to simulate catalog removal.
	inst.TemplateID = "removed"
	if err := store.PutInstance(ctx, inst); err != nil {
		t.Fatal(err)
	}
	engine.WithRouteRegistry(deploy.NewRouteRegistry())
	if err := engine.Undeploy(ctx, inst.ID, "owner"); err != nil {
		t.Fatalf("cleanup requires enabled module or catalog: %v", err)
	}
	if k8s.deleted != len(inst.Resources) {
		t.Fatalf("deleted %d of %d resources", k8s.deleted, len(inst.Resources))
	}
	got, err := store.GetInstance(ctx, inst.ID)
	if err != nil || got.Status != models.StatusTerminated {
		t.Fatalf("cleanup state: %+v, %v", got, err)
	}
}

type failingCleanupK8s struct{ routeCountingK8s }

func (*failingCleanupK8s) Delete(context.Context, string, string, string, string) error {
	return errors.New("cluster unavailable")
}

func TestFailedCleanupDoesNotReportTermination(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	if err := store.PutTemplate(ctx, &models.Template{ID: "model", Plane: models.PlaneLLMPlex, ModelID: "model"}); err != nil {
		t.Fatal(err)
	}
	engine := deploy.NewEngineWithK8s(store, &failingCleanupK8s{}, "test.local", "gateway")
	inst, err := engine.Deploy(ctx, models.PlaneLLMPlex, "model", nil, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Undeploy(ctx, inst.ID, "owner"); err == nil {
		t.Fatal("failed deletion reported success")
	}
	got, err := store.GetInstance(ctx, inst.ID)
	if err != nil || got.Status == models.StatusTerminated {
		t.Fatalf("failed cleanup marked terminated: %+v, %v", got, err)
	}
	history, err := store.ListHistory(ctx, inst.ID, 10)
	if err != nil || len(history) != 2 || history[0].Success {
		t.Fatalf("failed cleanup audit: %+v, %v", history, err)
	}
}

type inventoryCleanupK8s struct{ deleted []models.ResourceReference }

func (*inventoryCleanupK8s) Apply(context.Context, deploy.Manifest) error { return nil }
func (k *inventoryCleanupK8s) Delete(_ context.Context, version, kind, name, namespace string) error {
	k.deleted = append(k.deleted, models.ResourceReference{APIVersion: version, Kind: kind, Name: name, Namespace: namespace})
	return nil
}

func TestUndeployPreservesSharedRuntimeForSiblingInstances(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	inst := tapeInstance()
	inst.TemplateID = "treasury"
	inst.Status = models.StatusRunning
	sibling := *inst
	sibling.ID = "sibling-agent"
	tmpl := &models.Template{ID: "treasury", Plane: models.PlaneA2APlex}
	if err := store.PutTemplate(ctx, tmpl); err != nil {
		t.Fatal(err)
	}
	if err := store.PutInstance(ctx, inst); err != nil {
		t.Fatal(err)
	}
	if err := store.PutInstance(ctx, &sibling); err != nil {
		t.Fatal(err)
	}
	shared := 0
	for _, manifest := range deploy.GenerateManifests(inst, tmpl, "test.local") {
		if manifest.Shared {
			shared++
		}
	}
	if shared != 3 {
		t.Fatalf("expected shared runtime ownership markers, got %d", shared)
	}
	k8s := &inventoryCleanupK8s{}
	engine := deploy.NewEngineWithK8s(store, k8s, "test.local", "gateway").WithRouteRegistry(deploy.NewRouteRegistry())
	if err := engine.Undeploy(ctx, inst.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if len(k8s.deleted) != 5 {
		t.Fatalf("wanted 4 instance resources + route, got %+v", k8s.deleted)
	}
	for _, resource := range k8s.deleted {
		if resource.Namespace == "aiplex-system" || resource.Name == sibling.ID {
			t.Fatalf("deleted resources belonging to shared runtime or sibling: %+v", resource)
		}
	}
	got, err := store.GetInstance(ctx, sibling.ID)
	if err != nil || got.Status != models.StatusRunning {
		t.Fatalf("sibling affected: %+v %v", got, err)
	}
}

func TestCheckedRouteConfigRejectsInjectionAndAmbiguousBackendOwnership(t *testing.T) {
	for _, mutate := range []func(*models.LLMRouteConfig){
		func(c *models.LLMRouteConfig) { c.Backends[0].ModelID = "model\n---\nkind: Secret" },
		func(c *models.LLMRouteConfig) { c.Backends[0].Provider = "google\napiVersion: v1" },
		func(c *models.LLMRouteConfig) { c.Backends[0].SecretRef = "secret\n  unexpected: yes" },
		func(c *models.LLMRouteConfig) { c.Backends[0].Weight = -1 },
		func(c *models.LLMRouteConfig) { c.Backends[0].Weight = 101 },
		func(c *models.LLMRouteConfig) {
			c.Backends[0].Weight = 50
			c.Backends = append(c.Backends, c.Backends[0])
		},
		func(c *models.LLMRouteConfig) { c.Backends[0].Enabled = false },
	} {
		config := &models.LLMRouteConfig{ModelID: "model", Backends: []models.LLMBackend{{Provider: "google", ModelID: "model", Weight: 100, Enabled: true}}}
		mutate(config)
		if got, err := deploy.GenerateRoutesFromConfigChecked(config, "gateway"); err == nil || len(got) != 0 {
			t.Fatalf("unsafe config accepted: %+v %v", config, err)
		}
	}
	config := &models.LLMRouteConfig{ModelID: "model", Backends: []models.LLMBackend{{Provider: "google", ModelID: "publishers/google/models/gemini-2.5-flash", Weight: 100, Enabled: true}}}
	if _, err := deploy.GenerateRoutesFromConfigChecked(config, "gateway"); err != nil {
		t.Fatalf("valid provider model path rejected: %v", err)
	}
}

package deploy_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

type lifecycleStore struct {
	*registry.MemoryStore
	listErr, failRunning, failFailed bool
	getScopesErr, setScopesErr       bool
	scopeWrites                      int
}

func (s *lifecycleStore) GetUserScopes(ctx context.Context, owner string) ([]string, error) {
	if s.getScopesErr {
		return nil, errors.New("owner scopes unavailable")
	}
	return s.MemoryStore.GetUserScopes(ctx, owner)
}
func (s *lifecycleStore) SetUserScopes(ctx context.Context, owner string, scopes []string) error {
	s.scopeWrites++
	if s.setScopesErr {
		return errors.New("owner scopes write unavailable")
	}
	return s.MemoryStore.SetUserScopes(ctx, owner, scopes)
}

func (s *lifecycleStore) ModifyUserScopes(ctx context.Context, owner string, modify func([]string) ([]string, error)) error {
	if s.getScopesErr {
		return errors.New("owner scopes unavailable")
	}
	s.scopeWrites++
	if s.setScopesErr {
		return errors.New("owner scopes write unavailable")
	}
	return s.MemoryStore.ModifyUserScopes(ctx, owner, modify)
}

func (s *lifecycleStore) PutInstance(ctx context.Context, instance *models.Instance) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.failRunning && instance.Status == models.StatusRunning || s.failFailed && instance.Status == models.StatusFailed {
		return errors.New("state persistence unavailable")
	}
	// Emulate a durable backend, which never retains the caller's pointer.
	raw, err := json.Marshal(instance)
	if err != nil {
		return err
	}
	var copy models.Instance
	if err = json.Unmarshal(raw, &copy); err != nil {
		return err
	}
	return s.MemoryStore.PutInstance(ctx, &copy)
}
func (s *lifecycleStore) ListInstances(ctx context.Context, plane models.Plane) ([]models.Instance, error) {
	if s.listErr {
		return nil, errors.New("runtime inventory unavailable")
	}
	return s.MemoryStore.ListInstances(ctx, plane)
}

func lifecycleFixture(t *testing.T) *lifecycleStore {
	t.Helper()
	s := &lifecycleStore{MemoryStore: registry.NewMemoryStore()}
	if err := s.PutTemplate(context.Background(), &models.Template{ID: "cohesion", Plane: models.PlaneMCPlex, Tools: []models.ToolInfo{{Name: "search"}}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDeployPersistenceFailureDoesNotPublishSuccess(t *testing.T) {
	for _, failureStateAlsoUnavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure-state-saved", true: "failure-state-unavailable"}[failureStateAlsoUnavailable], func(t *testing.T) {
			store := lifecycleFixture(t)
			store.failRunning = true
			store.failFailed = failureStateAlsoUnavailable
			k8s := &routeCountingK8s{}
			engine := deploy.NewEngineWithK8s(store, k8s, "test.local", "gateway")
			if instance, err := engine.Deploy(context.Background(), models.PlaneMCPlex, "cohesion", nil, "owner", ""); err == nil || instance != nil {
				t.Fatalf("successful result after failed persistence: %+v %v", instance, err)
			}
			instances, err := store.ListInstances(context.Background(), "")
			if err != nil || len(instances) != 1 {
				t.Fatalf("initial cleanup inventory lost: %v %v", instances, err)
			}
			if instances[0].Status == models.StatusRunning || len(instances[0].Resources) == 0 {
				t.Fatalf("false running state or missing cleanup inventory: %+v", instances[0])
			}
			history, err := store.ListHistory(context.Background(), instances[0].ID, 10)
			if err != nil || len(history) != 1 || history[0].Success {
				t.Fatalf("false success history: %+v %v", history, err)
			}
			if err = engine.Undeploy(context.Background(), instances[0].ID, "owner"); err != nil {
				t.Fatalf("partial deployment cannot clean up: %v", err)
			}
		})
	}
}

func TestDeployRuntimeInventoryFailureStopsBeforeSideEffects(t *testing.T) {
	store := lifecycleFixture(t)
	store.listErr = true
	k8s := &routeCountingK8s{}
	engine := deploy.NewEngineWithK8s(store, k8s, "test.local", "gateway")
	if _, err := engine.Deploy(context.Background(), models.PlaneMCPlex, "cohesion", nil, "owner", ""); err == nil {
		t.Fatal("unknown existing runtime admitted")
	}
	instances, _ := store.MemoryStore.ListInstances(context.Background(), "")
	if len(instances) != 0 || k8s.applied != 0 {
		t.Fatal("runtime guard failed open before side effects")
	}
}

type canceledApplyK8s struct{ cancel context.CancelFunc }

func (k canceledApplyK8s) Apply(context.Context, deploy.Manifest) error {
	k.cancel()
	return context.Canceled
}
func (k canceledApplyK8s) Delete(context.Context, string, string, string, string) error { return nil }

func TestCanceledApplyPersistsFailureAndCleanupInventory(t *testing.T) {
	store := lifecycleFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := deploy.NewEngineWithK8s(store, canceledApplyK8s{cancel}, "test.local", "gateway")
	if _, err := engine.Deploy(ctx, models.PlaneMCPlex, "cohesion", nil, "owner", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause lost: %v", err)
	}
	instances, _ := store.ListInstances(context.Background(), "")
	if len(instances) != 1 || instances[0].Status != models.StatusFailed || len(instances[0].Resources) == 0 {
		t.Fatalf("canceled request lost partial deployment state: %+v", instances)
	}
}

func TestRuntimeRetentionCannotChangeUnderSameLogicalInstance(t *testing.T) {
	store := lifecycleFixture(t)
	engine := deploy.NewEngine(store, "test.local")
	first := deployTreasuryConfig(nil)
	if _, err := engine.Deploy(context.Background(), models.PlaneMCPlex, "cohesion", first, "owner", "Agent"); err != nil {
		t.Fatal(err)
	}
	next := deployTreasuryConfig(nil)
	next["runtime"].(map[string]any)["retention"] = map[string]any{"hot_days": 3, "compact_after_days": 15, "delete_after_days": 90}
	if _, err := engine.Deploy(context.Background(), models.PlaneMCPlex, "cohesion", next, "owner", "Agent"); !errors.Is(err, deploy.ErrRuntimeMutation) {
		t.Fatalf("changed retention omitted from runtime equality: %v", err)
	}
}

func TestOwnerGrantFailureRetainsManualScopesAndCleanupInventory(t *testing.T) {
	for _, readFailure := range []bool{true, false} {
		t.Run(map[bool]string{true: "scope-read-failure", false: "scope-write-failure"}[readFailure], func(t *testing.T) {
			store := lifecycleFixture(t)
			if err := store.MemoryStore.SetUserScopes(context.Background(), "owner", []string{"existing:manual:scope"}); err != nil {
				t.Fatal(err)
			}
			store.getScopesErr = readFailure
			store.setScopesErr = !readFailure
			k8s := &routeCountingK8s{}
			engine := deploy.NewEngineWithK8s(store, k8s, "test.local", "gateway")
			if inst, err := engine.Deploy(context.Background(), models.PlaneMCPlex, "cohesion", nil, "owner", ""); err == nil || inst != nil {
				t.Fatalf("incomplete owner grant reported success: %+v %v", inst, err)
			}
			scopes, err := store.MemoryStore.GetUserScopes(context.Background(), "owner")
			if err != nil || len(scopes) != 1 || scopes[0] != "existing:manual:scope" {
				t.Fatalf("existing manual grants lost: %v %v", scopes, err)
			}
			if readFailure && store.scopeWrites != 0 {
				t.Fatal("unknown manual grants overwritten after scope read failed")
			}
			instances, _ := store.ListInstances(context.Background(), "")
			if len(instances) != 1 || instances[0].Status != models.StatusFailed || len(instances[0].Resources) == 0 {
				t.Fatalf("partial deployment not retained for cleanup: %+v", instances)
			}
			history, _ := store.ListHistory(context.Background(), instances[0].ID, 10)
			if len(history) != 1 || history[0].Success {
				t.Fatalf("incomplete owner grant recorded success: %+v", history)
			}
			if err = engine.Undeploy(context.Background(), instances[0].ID, "owner"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

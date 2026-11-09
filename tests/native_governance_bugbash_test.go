package tests

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/governance"
	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type bugbashNativeDriver struct {
	writes  atomic.Int32
	execute func(context.Context, nativeapi.Operation) (nativeapi.Result, error)
}

func (d *bugbashNativeDriver) Operations() []nativeapi.OperationInfo {
	return []nativeapi.OperationInfo{{Name: "agent.get"}, {Name: "agent.update", Mutating: true}, {Name: "credential.acquire", Credential: true}}
}
func (d *bugbashNativeDriver) ValidateOperation(op nativeapi.Operation) error {
	if op.Name != "agent.get" && op.Name != "agent.update" && op.Name != "credential.acquire" {
		return errors.New("unknown operation")
	}
	if op.Resource != "native-resource" {
		return errors.New("wrong authority resource")
	}
	return nil
}
func (d *bugbashNativeDriver) Execute(ctx context.Context, op nativeapi.Operation) (nativeapi.Result, error) {
	if op.Name == "agent.update" {
		d.writes.Add(1)
	}
	if d.execute != nil {
		return d.execute(ctx, op)
	}
	return nativeapi.Result{StatusCode: 200, Body: json.RawMessage(`{"id":"native-resource"}`), ETag: "native-etag"}, nil
}
func bugbashNativeContext() contracts.RequestContext {
	return contracts.RequestContext{Version: "v1", TenantID: "tenant", RequestID: "request", PrincipalID: "reviewer", ActorID: "agent-actor", ResourceID: "binding"}
}
func bugbashNativeBinding() governance.AgentBinding {
	return governance.AgentBinding{ID: "binding", TenantID: "tenant", DriverRef: "native", Agent: governance.PrincipalRef{Provider: "google", TenantID: "native-authority", ImmutableID: "immutable-agent-id", Kind: "agent"}, Allowed: []governance.Grant{{Operation: "agent.get", Resource: "native-resource"}, {Operation: "agent.update", Resource: "native-resource"}}, Desired: []governance.DesiredOperation{{ID: "change", Operation: nativeapi.Operation{Name: "agent.update", Resource: "native-resource", Payload: json.RawMessage(`{"display_name":"reviewed"}`)}}}}
}
func bugbashNativeConfig(store resources.Store, driver *bugbashNativeDriver) governance.Config {
	return governance.Config{Store: store, Drivers: map[string]governance.DriverRegistration{"native": {Driver: driver, TenantID: "tenant", ProviderRevision: strings.Repeat("a", 64), AllowApply: true}}, Authorize: governance.AuthorizeFunc(func(context.Context, governance.Action) error { return nil }), Approvals: governance.ApprovalFunc(func(_ context.Context, a governance.Approval) error {
		if a.Reference != "reviewed" || a.TenantID != "tenant" || a.PrincipalID != "reviewer" || a.ActorID != "agent-actor" || a.PlanDigest == "" {
			return governance.ErrDenied
		}
		return nil
	}), AllowApply: true}
}
func bugbashSaveNativeBinding(t *testing.T, store resources.Store, binding governance.AgentBinding) uint64 {
	t.Helper()
	payload, e := json.Marshal(binding)
	if e != nil {
		t.Fatal(e)
	}
	record, e := store.Put(context.Background(), resources.Record{TenantID: binding.TenantID, Kind: governance.BindingKind, ID: binding.ID, Spec: payload}, 0)
	if e != nil {
		t.Fatal(e)
	}
	return record.Revision
}
func bugbashNativePlan(t *testing.T, c *governance.Controller, revision uint64) governance.Plan {
	t.Helper()
	plan, e := c.Plan(context.Background(), governance.PlanRequest{Context: bugbashNativeContext(), BindingID: "binding", BindingRevision: revision, DesiredID: "change"})
	if e != nil {
		t.Fatal(e)
	}
	return plan
}
func bugbashNativeApply(plan governance.Plan) governance.ApplyRequest {
	return governance.ApplyRequest{Context: bugbashNativeContext(), PlanID: plan.ID, PlanRevision: plan.Revision, ApprovalRef: "reviewed"}
}

func TestNativeGovernanceBugbashProviderAndCallerDriftDenyBeforeWrite(t *testing.T) {
	store := resources.NewMemoryStore()
	driver := &bugbashNativeDriver{}
	cfg := bugbashNativeConfig(store, driver)
	controller, e := governance.NewController(cfg)
	if e != nil {
		t.Fatal(e)
	}
	revision := bugbashSaveNativeBinding(t, store, bugbashNativeBinding())
	plan := bugbashNativePlan(t, controller, revision)
	caller := bugbashNativeApply(plan)
	caller.Context.PrincipalID = "other-reviewer"
	if _, e = controller.Apply(context.Background(), caller); e == nil {
		t.Fatal("plan transferred to another principal")
	}
	changed := cfg.Drivers["native"]
	changed.ProviderRevision = strings.Repeat("b", 64)
	cfg.Drivers["native"] = changed
	replacement, e := governance.NewController(cfg)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = replacement.Apply(context.Background(), bugbashNativeApply(plan)); !errors.Is(e, governance.ErrStale) {
		t.Fatalf("provider drift accepted: %v", e)
	}
	if driver.writes.Load() != 0 {
		t.Fatal("stale plan reached provider")
	}
}
func TestNativeGovernanceBugbashConcurrentControllersApplyOnce(t *testing.T) {
	store := resources.NewMemoryStore()
	driver := &bugbashNativeDriver{}
	driver.execute = func(context.Context, nativeapi.Operation) (nativeapi.Result, error) {
		time.Sleep(5 * time.Millisecond)
		return nativeapi.Result{StatusCode: 200, Body: json.RawMessage(`{}`)}, nil
	}
	cfg := bugbashNativeConfig(store, driver)
	first, e := governance.NewController(cfg)
	if e != nil {
		t.Fatal(e)
	}
	second, e := governance.NewController(cfg)
	if e != nil {
		t.Fatal(e)
	}
	revision := bugbashSaveNativeBinding(t, store, bugbashNativeBinding())
	plan := bugbashNativePlan(t, first, revision)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			c := first
			if i%2 == 0 {
				c = second
			}
			_, _ = c.Apply(context.Background(), bugbashNativeApply(plan))
		}(i)
	}
	close(start)
	wg.Wait()
	if driver.writes.Load() != 1 {
		t.Fatalf("external writes=%d", driver.writes.Load())
	}
	record, e := store.Get(context.Background(), "tenant", governance.ReceiptKind, plan.ID)
	if e != nil {
		t.Fatal(e)
	}
	var receipt governance.Receipt
	if json.Unmarshal(record.Spec, &receipt) != nil || receipt.State != "succeeded" {
		t.Fatalf("receipt %+v", receipt)
	}
}
func TestNativeGovernanceBugbashCanceledEffectPersistsUnknownAndNeverReplays(t *testing.T) {
	store := resources.NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	driver := &bugbashNativeDriver{execute: func(context.Context, nativeapi.Operation) (nativeapi.Result, error) {
		cancel()
		return nativeapi.Result{StatusCode: 200, Body: json.RawMessage(`{"accepted":true}`)}, nil
	}}
	cfg := bugbashNativeConfig(store, driver)
	c, e := governance.NewController(cfg)
	if e != nil {
		t.Fatal(e)
	}
	revision := bugbashSaveNativeBinding(t, store, bugbashNativeBinding())
	plan := bugbashNativePlan(t, c, revision)
	receipt, e := c.Apply(ctx, bugbashNativeApply(plan))
	if e == nil || receipt.State != "unknown" {
		t.Fatalf("canceled ambiguous effect: %+v %v", receipt, e)
	}
	restarted, e := governance.NewController(cfg)
	if e != nil {
		t.Fatal(e)
	}
	again, _ := restarted.Apply(context.Background(), bugbashNativeApply(plan))
	if again.State != "unknown" || driver.writes.Load() != 1 {
		t.Fatalf("unknown action replayed: %+v writes=%d", again, driver.writes.Load())
	}
	if _, e = restarted.Plan(context.Background(), governance.PlanRequest{Context: bugbashNativeContext(), BindingID: "binding", BindingRevision: revision, DesiredID: "change"}); !errors.Is(e, governance.ErrAlreadyAttempted) {
		t.Fatalf("replanning duplicated unknown action: %v", e)
	}
}
func TestNativeGovernanceBugbashCredentialsNeverBecomePublicPlans(t *testing.T) {
	store := resources.NewMemoryStore()
	driver := &bugbashNativeDriver{}
	c, e := governance.NewController(bugbashNativeConfig(store, driver))
	if e != nil {
		t.Fatal(e)
	}
	binding := bugbashNativeBinding()
	binding.Desired[0].Operation.Payload = json.RawMessage(`{"client_secret":"PRIVATE-SECRET"}`)
	if e = c.ValidateBinding(binding); e == nil {
		t.Fatal("credential-bearing desired payload accepted")
	}
	binding = bugbashNativeBinding()
	binding.Allowed = append(binding.Allowed, governance.Grant{Operation: "credential.acquire", Resource: "native-resource"})
	if e = c.ValidateBinding(binding); e == nil {
		t.Fatal("credential operation accepted in public binding")
	}
	if driver.writes.Load() != 0 {
		t.Fatal("invalid binding reached provider")
	}
	plans, e := store.List(context.Background(), "tenant", governance.PlanKind)
	if e != nil || len(plans) != 0 {
		t.Fatal("invalid payload persisted a plan")
	}
}
func TestNativeGovernanceBugbashApplyDisabledPreservesProvider(t *testing.T) {
	store := resources.NewMemoryStore()
	driver := &bugbashNativeDriver{}
	cfg := bugbashNativeConfig(store, driver)
	cfg.AllowApply = false
	cfg.Approvals = nil
	c, e := governance.NewController(cfg)
	if e != nil {
		t.Fatal(e)
	}
	revision := bugbashSaveNativeBinding(t, store, bugbashNativeBinding())
	plan := bugbashNativePlan(t, c, revision)
	if _, e = c.Apply(context.Background(), bugbashNativeApply(plan)); !errors.Is(e, governance.ErrApplyDisabled) {
		t.Fatal(e)
	}
	if driver.writes.Load() != 0 {
		t.Fatal("disabled apply wrote provider")
	}
}

func TestNativeGovernanceBugbashReadDeniedBeforeProvider(t *testing.T) {
	store := resources.NewMemoryStore()
	var calls atomic.Int32
	driver := &bugbashNativeDriver{execute: func(context.Context, nativeapi.Operation) (nativeapi.Result, error) {
		calls.Add(1)
		return nativeapi.Result{StatusCode: 200, Body: json.RawMessage(`{}`)}, nil
	}}
	cfg := bugbashNativeConfig(store, driver)
	cfg.Authorize = governance.AuthorizeFunc(func(context.Context, governance.Action) error { return governance.ErrDenied })
	c, err := governance.NewController(cfg)
	if err != nil {
		t.Fatal(err)
	}
	revision := bugbashSaveNativeBinding(t, store, bugbashNativeBinding())
	_, err = c.Read(context.Background(), governance.ReadRequest{Context: bugbashNativeContext(), BindingID: "binding", BindingRevision: revision, Operation: nativeapi.Operation{Name: "agent.get", Resource: "native-resource"}})
	if !errors.Is(err, governance.ErrDenied) || calls.Load() != 0 {
		t.Fatalf("denied read reached provider: calls=%d err=%v", calls.Load(), err)
	}
}

func TestNativeGovernanceBugbashProjectionCannotPersistCredentials(t *testing.T) {
	store := resources.NewMemoryStore()
	driver := &bugbashNativeDriver{execute: func(context.Context, nativeapi.Operation) (nativeapi.Result, error) {
		return nativeapi.Result{StatusCode: 200, Body: json.RawMessage(`{"access_token":"PRIVATE-SECRET"}`)}, nil
	}}
	cfg := bugbashNativeConfig(store, driver)
	registration := cfg.Drivers["native"]
	registration.InventoryProjection = func(_ nativeapi.Operation, result nativeapi.Result) (json.RawMessage, error) { return result.Body, nil }
	cfg.Drivers["native"] = registration
	c, err := governance.NewController(cfg)
	if err != nil {
		t.Fatal(err)
	}
	revision := bugbashSaveNativeBinding(t, store, bugbashNativeBinding())
	_, err = c.Read(context.Background(), governance.ReadRequest{Context: bugbashNativeContext(), BindingID: "binding", BindingRevision: revision, Operation: nativeapi.Operation{Name: "agent.get", Resource: "native-resource"}})
	if err == nil || strings.Contains(err.Error(), "PRIVATE-SECRET") {
		t.Fatalf("credential projection accepted or leaked in error: %v", err)
	}
	observations, err := store.List(context.Background(), "tenant", governance.ObservationKind)
	if err != nil || len(observations) != 0 {
		t.Fatal("credential-bearing projection persisted")
	}
}

func TestNativeGovernanceBugbashCancellationBeforeDriverSkipsNativeEffect(t *testing.T) {
	store := resources.NewMemoryStore()
	driver := &bugbashNativeDriver{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := bugbashNativeConfig(store, driver)
	var authorizations atomic.Int32
	cfg.Authorize = governance.AuthorizeFunc(func(_ context.Context, action governance.Action) error {
		if action.Phase == "apply" && authorizations.Add(1) == 2 {
			cancel()
		}
		return nil
	})
	c, err := governance.NewController(cfg)
	if err != nil {
		t.Fatal(err)
	}
	revision := bugbashSaveNativeBinding(t, store, bugbashNativeBinding())
	plan := bugbashNativePlan(t, c, revision)
	_, err = c.Apply(ctx, bugbashNativeApply(plan))
	if err == nil || driver.writes.Load() != 0 {
		t.Fatalf("canceled action reached provider: writes=%d err=%v", driver.writes.Load(), err)
	}
}

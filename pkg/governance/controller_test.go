package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type fixtureDriver struct {
	calls   atomic.Int32
	execute func(context.Context, nativeapi.Operation) (nativeapi.Result, error)
}

func (d *fixtureDriver) Operations() []nativeapi.OperationInfo {
	return []nativeapi.OperationInfo{{Name: "agents.get"}, {Name: "agents.patch", Mutating: true}, {Name: "tokens.get", Credential: true}, {Name: "tokens.create", Credential: true, Mutating: true}}
}
func (d *fixtureDriver) ValidateOperation(op nativeapi.Operation) error {
	if op.Resource != "agents/123" {
		return errors.New("wrong native resource namespace")
	}
	if op.Name != "agents.get" && op.Name != "agents.patch" && op.Name != "tokens.get" && op.Name != "tokens.create" {
		return errors.New("unknown operation")
	}
	if op.Name == "agents.patch" && !bytes.Contains(op.Payload, []byte(`"policy"`)) {
		return errors.New("invalid policy schema")
	}
	return nil
}
func (d *fixtureDriver) Execute(ctx context.Context, op nativeapi.Operation) (nativeapi.Result, error) {
	d.calls.Add(1)
	if d.execute != nil {
		return d.execute(ctx, op)
	}
	return nativeapi.Result{StatusCode: 200, ETag: "native-v1", Body: json.RawMessage(`{"name":"agents/123"}`)}, nil
}
func fixtureBinding() AgentBinding {
	return AgentBinding{ID: "agent-1", TenantID: "app-tenant", DriverRef: "provider-1", Agent: PrincipalRef{Provider: "google", TenantID: "native-authority", ImmutableID: "123", Kind: "agent"}, Allowed: []Grant{{"agents.get", "agents/123"}, {"agents.patch", "agents/123"}}, Desired: []DesiredOperation{{ID: "policy-1", Operation: nativeapi.Operation{Name: "agents.patch", Resource: "agents/123", Payload: json.RawMessage(`{ "policy": { "role": "reader" } }`)}}}}
}
func fixtureContext() contracts.RequestContext {
	return contracts.RequestContext{Version: "v1", TenantID: "app-tenant", RequestID: "req-1", PrincipalID: "caller-1", ActorID: "workload-1", ResourceID: "agent-1"}
}
func fixtureController(t *testing.T, store resources.Store, d *fixtureDriver, modify func(*Config)) (*Controller, resources.Record) {
	t.Helper()
	cfg := Config{Store: store, Drivers: map[string]DriverRegistration{"provider-1": {Driver: d, TenantID: "app-tenant", ProviderRevision: strings.Repeat("a", 64), AllowApply: true}}, AllowApply: true, Authorize: AuthorizeFunc(func(context.Context, Action) error { return nil }), Approvals: ApprovalFunc(func(_ context.Context, a Approval) error {
		if a.Reference != a.PlanDigest {
			return ErrDenied
		}
		return nil
	})}
	if modify != nil {
		modify(&cfg)
	}
	c, err := NewController(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b := fixtureBinding()
	if err = c.ValidateBinding(b); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	saved, err := store.Put(context.Background(), resources.Record{TenantID: b.TenantID, Kind: BindingKind, ID: b.ID, Spec: raw}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c, saved
}
func fixturePlan(t *testing.T, c *Controller, binding resources.Record) Plan {
	t.Helper()
	p, err := c.Plan(context.Background(), PlanRequest{Context: fixtureContext(), BindingID: binding.ID, BindingRevision: binding.Revision, DesiredID: "policy-1"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func fixtureApply(p Plan) ApplyRequest {
	return ApplyRequest{Context: fixtureContext(), PlanID: p.ID, PlanRevision: p.Revision, ApprovalRef: p.Digest}
}

func TestIdentityCoordinatesAndNoSponsorMembership(t *testing.T) {
	b := fixtureBinding()
	b.DelegatedUser = &PrincipalRef{Provider: "entra", TenantID: "directory-2", ImmutableID: "object-2", Kind: "user"}
	b.Sponsor = &PrincipalRef{Provider: "google", TenantID: "directory-3", ImmutableID: "object-3", Kind: "user"}
	b.Registry = &RegistryRef{Provider: "google", TenantID: "native-authority", NativeResourceName: "projects/1/agents/123", DiscoveryURN: "urn:agent:123", RuntimeRef: "reasoningEngines/456"}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	if bytes.Contains(raw, []byte("groups")) {
		t.Fatal("sponsor membership must not be inferred into binding")
	}
	b.Agent.ImmutableID = "agent@example.com"
	if b.Validate() == nil {
		t.Fatal("mutable email accepted as immutable identity")
	}
	b = fixtureBinding()
	b.DelegatedUser = b.Sponsor
	b.Registry = &RegistryRef{Provider: "google", TenantID: "native-authority", NativeResourceName: "urn:agent:123", DiscoveryURN: "urn:agent:123"}
	if b.Validate() == nil {
		t.Fatal("native and discovery coordinates conflated")
	}
}

func TestValidateBeforePersistAndCredentialOperationsBlocked(t *testing.T) {
	store := resources.NewMemoryStore()
	driver := &fixtureDriver{}
	c, _ := fixtureController(t, store, driver, nil)
	for _, payload := range []string{`{"policy":{},"clientSecret":"secret"}`, `{"policy":{},"nested":{"api_key":"secret"}}`, `{"policy":{},"role":"reader","ROLE":"writer"}`} {
		b := fixtureBinding()
		b.Desired[0].Operation.Payload = json.RawMessage(payload)
		if c.ValidateBinding(b) == nil {
			t.Fatalf("unsafe payload admitted: %s", payload)
		}
	}
	b := fixtureBinding()
	b.Desired[0].Operation.Payload = json.RawMessage(`{"unrecognized":true}`)
	if c.ValidateBinding(b) == nil {
		t.Fatal("provider pure validation was bypassed")
	}
	b = fixtureBinding()
	b.Allowed = append(b.Allowed, Grant{"tokens.get", "agents/123"})
	if c.ValidateBinding(b) == nil {
		t.Fatal("credential read exposed in binding")
	}
	b = fixtureBinding()
	b.TenantID = "other-app-tenant"
	if c.ValidateBinding(b) == nil {
		t.Fatal("cross-tenant driver admitted")
	}
	if driver.calls.Load() != 0 {
		t.Fatal("pure validation ran native calls")
	}
}

func TestReadAuthorizationBeforeDriverAndDefaultMetadataOnly(t *testing.T) {
	store := resources.NewMemoryStore()
	driver := &fixtureDriver{execute: func(context.Context, nativeapi.Operation) (nativeapi.Result, error) {
		return nativeapi.Result{StatusCode: 200, Body: json.RawMessage(`{"accessToken":"private","name":"agent"}`)}, nil
	}}
	denied := true
	c, binding := fixtureController(t, store, driver, func(cfg *Config) {
		cfg.Authorize = AuthorizeFunc(func(context.Context, Action) error {
			if denied {
				return ErrDenied
			}
			return nil
		})
	})
	request := ReadRequest{Context: fixtureContext(), BindingID: binding.ID, BindingRevision: binding.Revision, Operation: nativeapi.Operation{Name: "agents.get", Resource: "agents/123"}}
	if _, err := c.Read(context.Background(), request); !errors.Is(err, ErrDenied) || driver.calls.Load() != 0 {
		t.Fatal("unauthorized native read executed")
	}
	denied = false
	observed, err := c.Read(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(observed.Body) != 0 || observed.ResultDigest == "" {
		t.Fatal("default projection must store metadata only")
	}
	rows, _ := store.List(context.Background(), fixtureContext().TenantID, ObservationKind)
	if len(rows) != 1 || bytes.Contains(rows[0].Spec, []byte("private")) {
		t.Fatal("native secret leaked to observation")
	}
	request.Operation.Name = "agents.patch"
	request.Operation.Payload = json.RawMessage(`{"policy":{}}`)
	if _, err = c.Read(context.Background(), request); !errors.Is(err, ErrDenied) {
		t.Fatal("mutation entered inventory path")
	}
}

func TestPlanSavedPayloadProviderSnapshotAndExactApproval(t *testing.T) {
	store := resources.NewMemoryStore()
	driver := &fixtureDriver{}
	var approval Approval
	c, binding := fixtureController(t, store, driver, func(cfg *Config) {
		cfg.Approvals = ApprovalFunc(func(_ context.Context, a Approval) error {
			approval = a
			if a.Reference != a.PlanDigest {
				return ErrDenied
			}
			return nil
		})
	})
	p := fixturePlan(t, c, binding)
	if p.ProviderRevision != strings.Repeat("a", 64) || p.PayloadHash == "" || p.Digest == "" {
		t.Fatal("incomplete plan provenance")
	}
	request := fixtureApply(p)
	request.ApprovalRef = "caller-boolean"
	if _, err := c.Apply(context.Background(), request); !errors.Is(err, ErrDenied) {
		t.Fatal("nonexact approval accepted")
	}
	if driver.calls.Load() != 0 {
		t.Fatal("unapproved mutation executed")
	}
	request = fixtureApply(p)
	request.PlanRevision++
	if _, err := c.Apply(context.Background(), request); !errors.Is(err, ErrStale) {
		t.Fatal("stale plan revision accepted")
	}
	r, err := c.Apply(context.Background(), fixtureApply(p))
	if err != nil || r.State != "succeeded" || r.Readiness != "unverified" {
		t.Fatalf("receipt %+v: %v", r, err)
	}
	if approval.PlanDigest != p.Digest || approval.PlanRevision != p.Revision || approval.ActorID != fixtureContext().ActorID {
		t.Fatal("approval did not bind exact plan and actor")
	}
	if _, err = c.Plan(context.Background(), PlanRequest{Context: fixtureContext(), BindingID: binding.ID, BindingRevision: binding.Revision, DesiredID: "policy-1"}); !errors.Is(err, ErrAlreadyAttempted) {
		t.Fatal("replanning enables repeated native mutation")
	}
}

func TestConcurrentApplyClaimsOneExternalAction(t *testing.T) {
	store := resources.NewMemoryStore()
	entered := make(chan struct{})
	release := make(chan struct{})
	driver := &fixtureDriver{execute: func(context.Context, nativeapi.Operation) (nativeapi.Result, error) {
		close(entered)
		<-release
		return nativeapi.Result{StatusCode: 200}, nil
	}}
	c, binding := fixtureController(t, store, driver, nil)
	p := fixturePlan(t, c, binding)
	var wg sync.WaitGroup
	wg.Add(1)
	var first Receipt
	var firstErr error
	go func() { defer wg.Done(); first, firstErr = c.Apply(context.Background(), fixtureApply(p)) }()
	<-entered
	second, err := c.Apply(context.Background(), fixtureApply(p))
	if err != nil || second.State != "pending" {
		t.Fatalf("concurrent claim: %+v %v", second, err)
	}
	close(release)
	wg.Wait()
	if firstErr != nil || first.State != "succeeded" || driver.calls.Load() != 1 {
		t.Fatalf("calls=%d receipt=%+v error=%v", driver.calls.Load(), first, firstErr)
	}
	last, err := c.Apply(context.Background(), fixtureApply(p))
	if err != nil || last.State != "succeeded" || driver.calls.Load() != 1 {
		t.Fatal("idempotent completed action repeated")
	}
}

func TestUnknownOutcomesNeverReplay(t *testing.T) {
	for _, scenario := range []string{"timeout", "disconnect", "panic", "server-error"} {
		t.Run(scenario, func(t *testing.T) {
			store := resources.NewMemoryStore()
			driver := &fixtureDriver{execute: func(ctx context.Context, _ nativeapi.Operation) (nativeapi.Result, error) {
				switch scenario {
				case "timeout":
					<-ctx.Done()
					return nativeapi.Result{}, ctx.Err()
				case "panic":
					panic("internal credentials must not appear")
				case "server-error":
					return nativeapi.Result{StatusCode: 503}, nil
				default:
					return nativeapi.Result{}, fmt.Errorf("credential=must-not-leak")
				}
			}}
			c, b := fixtureController(t, store, driver, func(cfg *Config) { cfg.OperationTimeout = time.Millisecond })
			p := fixturePlan(t, c, b)
			r, err := c.Apply(context.Background(), fixtureApply(p))
			if err == nil || r.State != "unknown" || strings.Contains(err.Error(), "credential=") {
				t.Fatalf("outcome=%+v err=%v", r, err)
			}
			r, err = c.Apply(context.Background(), fixtureApply(p))
			if err != nil || r.State != "unknown" || driver.calls.Load() != 1 {
				t.Fatal("ambiguous native action replayed")
			}
		})
	}
}

func TestApplyFeatureGatesAndProviderDrift(t *testing.T) {
	for _, scenario := range []string{"global-disabled", "driver-disabled", "provider-drift", "binding-drift", "actor-drift"} {
		t.Run(scenario, func(t *testing.T) {
			store := resources.NewMemoryStore()
			driver := &fixtureDriver{}
			c, b := fixtureController(t, store, driver, nil)
			p := fixturePlan(t, c, b)
			request := fixtureApply(p)
			switch scenario {
			case "global-disabled":
				c.allowApply = false
			case "driver-disabled":
				registration := c.drivers["provider-1"]
				registration.AllowApply = false
				c.drivers["provider-1"] = registration
			case "provider-drift":
				registration := c.drivers["provider-1"]
				registration.ProviderRevision = strings.Repeat("b", 64)
				c.drivers["provider-1"] = registration
			case "binding-drift":
				_, err := store.Put(context.Background(), b, b.Revision)
				if err != nil {
					t.Fatal(err)
				}
			case "actor-drift":
				request.Context.ActorID = "other-actor"
			}
			if _, err := c.Apply(context.Background(), request); err == nil || driver.calls.Load() != 0 {
				t.Fatal("mutation ran against disabled or changed reviewed context")
			}
		})
	}
}

func TestFreshETagProofMatchesActorResourceAndProvider(t *testing.T) {
	store := resources.NewMemoryStore()
	driver := &fixtureDriver{}
	c, b := fixtureController(t, store, driver, func(cfg *Config) {
		d := cfg.Drivers["provider-1"]
		d.ETagReads = map[string]string{"agents.patch": "agents.get"}
		cfg.Drivers["provider-1"] = d
	})
	var binding AgentBinding
	_ = json.Unmarshal(b.Spec, &binding)
	binding.Desired[0].Operation.ETag = "native-v1"
	b.Spec, _ = json.Marshal(binding)
	var err error
	b, err = store.Put(context.Background(), b, b.Revision)
	if err != nil {
		t.Fatal(err)
	}
	request := PlanRequest{Context: fixtureContext(), BindingID: b.ID, BindingRevision: b.Revision, DesiredID: "policy-1"}
	if _, err = c.Plan(context.Background(), request); err == nil {
		t.Fatal("caller-chosen unobserved ETag accepted")
	}
	read := ReadRequest{Context: fixtureContext(), BindingID: b.ID, BindingRevision: b.Revision, Operation: nativeapi.Operation{Name: "agents.get", Resource: "agents/123"}}
	observation, err := c.Read(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Context.ActorID = "other-actor"
	if _, err = c.Plan(context.Background(), request); err == nil {
		t.Fatal("another actor borrowed ETag proof")
	}
	now := c.now
	c.now = func() time.Time { return observation.ObservedAt.Add(c.observationAge + time.Second) }
	if _, err = c.Apply(context.Background(), fixtureApply(p)); err == nil {
		t.Fatal("expired ETag proof accepted")
	}
	c.now = now
	if driver.calls.Load() != 1 {
		t.Fatal("ETag validation executed unintended native calls")
	}
}

type receiptFailureStore struct {
	resources.Store
	finalize bool
}

func (s receiptFailureStore) Put(ctx context.Context, r resources.Record, expected uint64) (resources.Record, error) {
	if r.Kind == ReceiptKind && (s.finalize && expected > 0 || !s.finalize && expected == 0) {
		return resources.Record{}, errors.New("journal unavailable")
	}
	return s.Store.Put(ctx, r, expected)
}
func TestReceiptPersistenceFailurePreservesNoReplay(t *testing.T) {
	for _, finalize := range []bool{false, true} {
		t.Run(fmt.Sprint(finalize), func(t *testing.T) {
			underlying := resources.NewMemoryStore()
			store := receiptFailureStore{Store: underlying, finalize: finalize}
			driver := &fixtureDriver{}
			c, b := fixtureController(t, store, driver, nil)
			p := fixturePlan(t, c, b)
			r, err := c.Apply(context.Background(), fixtureApply(p))
			if err == nil {
				t.Fatal("journal failure accepted")
			}
			if !finalize && driver.calls.Load() != 0 {
				t.Fatal("mutation ran without durable claim")
			}
			if finalize {
				if r.State != "unknown" || driver.calls.Load() != 1 {
					t.Fatal("effect without durable result not classified unknown")
				}
				r, err = c.Apply(context.Background(), fixtureApply(p))
				if err != nil || r.State != "pending" || driver.calls.Load() != 1 {
					t.Fatal("unfinalized action replayed")
				}
			}
		})
	}
}

func TestDeletedReceiptTombstoneDoesNotReenableAction(t *testing.T) {
	store := resources.NewMemoryStore()
	driver := &fixtureDriver{}
	c, b := fixtureController(t, store, driver, nil)
	p := fixturePlan(t, c, b)
	if _, err := c.Apply(context.Background(), fixtureApply(p)); err != nil {
		t.Fatal(err)
	}
	record, err := store.Get(context.Background(), p.TenantID, ReceiptKind, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Delete(context.Background(), record.TenantID, record.Kind, record.ID, record.Revision); err != nil {
		t.Fatal(err)
	}
	r, err := c.Apply(context.Background(), fixtureApply(p))
	if !errors.Is(err, ErrAlreadyAttempted) || r.State != "unknown" || driver.calls.Load() != 1 {
		t.Fatal("deleted journal caused native action replay")
	}
}

func TestInventoryProjectionRedactionAndValidation(t *testing.T) {
	raw := json.RawMessage(`{"name":"agent","clientSecret":"hidden","nested":{"api-key":"hidden","value":"Bearer hidden"},"credential_ref":"ref:1"}`)
	clean, err := RedactInventory(nativeapi.Operation{}, nativeapi.Result{Body: raw})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(clean, []byte("hidden")) || !bytes.Contains(clean, []byte("ref:1")) {
		t.Fatalf("redaction: %s", clean)
	}
	store := resources.NewMemoryStore()
	driver := &fixtureDriver{}
	c, b := fixtureController(t, store, driver, func(cfg *Config) {
		d := cfg.Drivers["provider-1"]
		d.InventoryProjection = func(nativeapi.Operation, nativeapi.Result) (json.RawMessage, error) { return raw, nil }
		cfg.Drivers["provider-1"] = d
	})
	_, err = c.Read(context.Background(), ReadRequest{Context: fixtureContext(), BindingID: b.ID, BindingRevision: b.Revision, Operation: nativeapi.Operation{Name: "agents.get", Resource: "agents/123"}})
	if err == nil {
		t.Fatal("unsafe host projection entered persisted inventory")
	}
	rows, _ := store.List(context.Background(), fixtureContext().TenantID, ObservationKind)
	if len(rows) != 0 {
		t.Fatal("unsafe observation persisted")
	}
}

func TestSavedPlanSealsExactPayloadBytesAndRejectsTampering(t *testing.T) {
	store := resources.NewMemoryStore()
	var executed []byte
	driver := &fixtureDriver{execute: func(_ context.Context, operation nativeapi.Operation) (nativeapi.Result, error) {
		executed = append([]byte(nil), operation.Payload...)
		return nativeapi.Result{StatusCode: 200}, nil
	}}
	c, binding := fixtureController(t, store, driver, nil)
	// A stored resource may contain insignificant JSON whitespace. The approved
	// exact wire operation is sealed as bytes rather than remarshal-normalized.
	var specification AgentBinding
	_ = json.Unmarshal(binding.Spec, &specification)
	binding.Spec, _ = json.MarshalIndent(specification, "", "  ")
	var err error
	binding, err = store.Put(context.Background(), binding, binding.Revision)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(binding.Spec, &specification)
	want := append([]byte(nil), specification.Desired[0].Operation.Payload...)
	p := fixturePlan(t, c, binding)
	if _, err = c.Apply(context.Background(), fixtureApply(p)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(executed, want) {
		t.Fatalf("approved wire bytes changed: got %q want %q", executed, want)
	}
	// A different binding revision creates a separately reviewed plan. Editing
	// its private envelope without changing the public digest is refused.
	binding, err = store.Put(context.Background(), binding, binding.Revision)
	if err != nil {
		t.Fatal(err)
	}
	p = fixturePlan(t, c, binding)
	saved, err := store.Get(context.Background(), p.TenantID, PlanKind, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var envelope savedPlan
	_ = json.Unmarshal(saved.Spec, &envelope)
	envelope.Operation.Payload = []byte(`{"policy":{"role":"owner"}}`)
	saved.Spec, _ = json.Marshal(envelope)
	saved, err = store.Put(context.Background(), saved, saved.Revision)
	if err != nil {
		t.Fatal(err)
	}
	p.Revision = saved.Revision
	if _, err = c.Apply(context.Background(), fixtureApply(p)); !errors.Is(err, ErrStale) {
		t.Fatal("changed operation escaped its approved payload digest")
	}
	if driver.calls.Load() != 1 {
		t.Fatal("tampered operation executed")
	}
}

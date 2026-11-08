package modules

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeModule struct {
	d                           Descriptor
	events                      *[]string
	startErr, readyErr, stopErr error
	stopContextError            error
}

func descriptor(name string) Descriptor {
	return Descriptor{Name: name, Version: "1.0.0", ContractVersion: ContractVersion}
}
func (m *fakeModule) Descriptor() Descriptor { return m.d }
func (m *fakeModule) event(s string) {
	if m.events != nil {
		*m.events = append(*m.events, s+":"+m.d.Name)
	}
}
func (m *fakeModule) Start(context.Context) error { m.event("start"); return m.startErr }
func (m *fakeModule) Ready(context.Context) error { m.event("ready"); return m.readyErr }
func (m *fakeModule) Stop(ctx context.Context) error {
	m.event("stop")
	m.stopContextError = ctx.Err()
	return m.stopErr
}
func register(t *testing.T, r *Registry, m Module) {
	t.Helper()
	if err := r.Register(m); err != nil {
		t.Fatal(err)
	}
}
func dependency(name string) Dependency {
	return Dependency{Name: name, ContractVersion: ContractVersion, MinVersion: "1.0.0"}
}

func TestPlanDeterministicAndDescriptorsImmutable(t *testing.T) {
	r := NewRegistry()
	c := &fakeModule{d: descriptor("consumer")}
	c.d.Dependencies = []Dependency{dependency("identity")}
	c.d.Capabilities = []string{"model.route"}
	c.d.ConfigSchema = []byte(`{"type":"object"}`)
	register(t, r, c)
	register(t, r, &fakeModule{d: descriptor("identity")})
	register(t, r, &fakeModule{d: descriptor("audit")})
	// Caller-owned slices cannot mutate the registration or returned discovery.
	c.d.Capabilities[0] = "changed"
	c.d.ConfigSchema[0] = '['
	ds := r.Descriptors()
	ds[1].Dependencies[0].Name = "bad"
	ds[1].ConfigSchema[0] = '['
	for i := 0; i < 30; i++ {
		p, err := r.Plan()
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, d := range p {
			names = append(names, d.Name)
		}
		if !reflect.DeepEqual(names, []string{"audit", "identity", "consumer"}) {
			t.Fatalf("plan %v", names)
		}
	}
	if got := r.Descriptors()[1].Capabilities[0]; got != "model.route" {
		t.Fatalf("mutable descriptor: %s", got)
	}
	if got := string(r.Descriptors()[1].ConfigSchema); got != `{"type":"object"}` {
		t.Fatalf("mutable schema: %s", got)
	}
}

func TestPlanRejectsIncompatibleGraph(t *testing.T) {
	tests := []struct {
		name    string
		modules []Descriptor
		want    string
	}{
		{"missing", []Descriptor{{Name: "consumer", Version: "1.0.0", ContractVersion: "v1", Dependencies: []Dependency{dependency("missing")}}}, "missing module"},
		{"old", []Descriptor{{Name: "consumer", Version: "1.0.0", ContractVersion: "v1", Dependencies: []Dependency{{Name: "provider", ContractVersion: "v1", MinVersion: "1.2.0"}}}, {Name: "provider", Version: "1.1.9", ContractVersion: "v1"}}, "incompatible"},
		{"major", []Descriptor{{Name: "consumer", Version: "1.0.0", ContractVersion: "v1", Dependencies: []Dependency{dependency("provider")}}, {Name: "provider", Version: "2.0.0", ContractVersion: "v1"}}, "incompatible"},
		{"cycle", []Descriptor{{Name: "alpha", Version: "1.0.0", ContractVersion: "v1", Dependencies: []Dependency{dependency("beta")}}, {Name: "beta", Version: "1.0.0", ContractVersion: "v1", Dependencies: []Dependency{dependency("alpha")}}}, "cycle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			for _, d := range tt.modules {
				register(t, r, &fakeModule{d: d})
			}
			if _, err := r.Plan(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %s, got %v", tt.want, err)
			}
		})
	}
}

func TestOptionalDependencyValidatedWhenPresent(t *testing.T) {
	r := NewRegistry()
	d := descriptor("consumer")
	dep := dependency("provider")
	dep.Optional = true
	d.Dependencies = []Dependency{dep}
	register(t, r, &fakeModule{d: d})
	if _, err := r.Plan(); err != nil {
		t.Fatal(err)
	}
	provider := descriptor("provider")
	provider.Version = "2.0.0"
	register(t, r, &fakeModule{d: provider})
	if _, err := r.Plan(); err == nil {
		t.Fatal("incompatible optional provider accepted")
	}
}

func TestRegistrationValidation(t *testing.T) {
	for _, schema := range []string{`[]`, `null`, `"object"`, `{invalid}`} {
		d := descriptor("test")
		d.ConfigSchema = []byte(schema)
		if err := d.Validate(); err == nil {
			t.Fatalf("accepted schema %q", schema)
		}
	}
	for _, s := range []string{"1", "1.0", "v1.0.0", "1.0.0-beta", "01.0.0", "1.0.-1", "18446744073709551616.0.0"} {
		d := descriptor("test")
		d.Version = s
		if err := d.Validate(); err == nil {
			t.Fatalf("accepted version %q", s)
		}
	}
	for _, p := range []string{"/api/../admin", "//api", "/api/", "/api?secret=yes", "/api%2fadmin", "/api\\admin", "/api\n"} {
		d := descriptor("test")
		d.APIPrefixes = []string{p}
		if err := d.Validate(); err == nil {
			t.Fatalf("accepted prefix %q", p)
		}
	}
	r := NewRegistry()
	d := descriptor("first")
	d.APIPrefixes = []string{"/api/v1/models"}
	register(t, r, &fakeModule{d: d})
	x := descriptor("second")
	x.APIPrefixes = []string{"/api/v1/models/private"}
	if err := r.Register(&fakeModule{d: x}); err == nil {
		t.Fatal("overlapping API prefixes accepted")
	}
	if err := r.Register(&fakeModule{d: d}); err == nil {
		t.Fatal("duplicate accepted")
	}
	var nilModule *fakeModule
	if err := r.Register(nilModule); err == nil {
		t.Fatal("typed nil accepted")
	}
}

func TestRuntimeRollbackAndRequiredReadiness(t *testing.T) {
	var events []string
	r := NewRegistry()
	a := &fakeModule{d: descriptor("identity"), events: &events}
	b := &fakeModule{d: descriptor("models"), events: &events, readyErr: errors.New("unavailable")}
	b.d.Dependencies = []Dependency{dependency("identity")}
	register(t, r, b)
	register(t, r, a)
	runtime := NewRuntime(r)
	if err := runtime.Start(context.Background()); err == nil {
		t.Fatal("required unready module accepted")
	}
	want := []string{"start:identity", "ready:identity", "start:models", "ready:models", "stop:models", "stop:identity"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events %v", events)
	}
	if err := runtime.Ready(context.Background()); err == nil {
		t.Fatal("rolled back runtime ready")
	}
	if err := r.Register(&fakeModule{d: descriptor("late")}); err == nil {
		t.Fatal("late registration accepted")
	}
	b.readyErr = nil
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.readyErr = errors.New("lost identity")
	if err := runtime.Ready(context.Background()); err == nil {
		t.Fatal("lost dependency reported ready")
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type cancelOnStart struct {
	fakeModule
	cancel context.CancelFunc
}

func (m *cancelOnStart) Start(context.Context) error { m.cancel(); return context.Canceled }
func TestRollbackHasUsableContextAndCleanupRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &cancelOnStart{fakeModule: fakeModule{d: descriptor("provider"), stopErr: errors.New("busy")}, cancel: cancel}
	r := NewRegistry()
	register(t, r, m)
	runtime := NewRuntime(r)
	if err := runtime.Start(ctx); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("errors lost: %v", err)
	}
	if m.stopContextError != nil {
		t.Fatalf("rollback inherited canceled context: %v", m.stopContextError)
	}
	if err := runtime.Start(context.Background()); err == nil {
		t.Fatal("restarted with incomplete cleanup")
	}
	m.stopErr = nil
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStopFailureRetriesOnlyFailedDependency(t *testing.T) {
	var events []string
	r := NewRegistry()
	provider := &fakeModule{d: descriptor("identity"), events: &events}
	consumer := &fakeModule{d: descriptor("models"), events: &events}
	consumer.d.Dependencies = []Dependency{dependency("identity")}
	register(t, r, consumer)
	register(t, r, provider)
	runtime := NewRuntime(r)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	events = nil
	provider.stopErr = errors.New("retry cleanup")
	if err := runtime.Stop(context.Background()); err == nil {
		t.Fatal("cleanup failure lost")
	}
	if err := runtime.Ready(context.Background()); err == nil {
		t.Fatal("stopping runtime ready")
	}
	provider.stopErr = nil
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"stop:models", "stop:identity", "stop:identity"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("cleanup order/retry %v", events)
	}
}

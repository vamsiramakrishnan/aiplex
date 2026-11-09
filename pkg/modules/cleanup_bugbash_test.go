package modules

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

type cleanupModule struct {
	d            Descriptor
	dependencies []*cleanupModule
	active       bool
	stops        int
	stopErr      error
	readyErr     error
}

func TestBugbashCleanupGraphMatchesDependencyRetentionModel(t *testing.T) {
	random := rand.New(rand.NewSource(20261109))
	for sample := range 64 {
		registry := NewRegistry()
		var nodes []*cleanupModule
		for i := range 8 {
			node := &cleanupModule{d: descriptor(fmt.Sprintf("module-%d", i))}
			for j := range i {
				if random.Intn(3) == 0 {
					node.dependencies = append(node.dependencies, nodes[j])
					dep := dependency(nodes[j].d.Name)
					dep.Optional = random.Intn(2) == 0
					node.d.Dependencies = append(node.d.Dependencies, dep)
				}
			}
			nodes = append(nodes, node)
			register(t, registry, node)
		}
		mask := 1 + random.Intn((1<<len(nodes))-1)
		expected := map[*cleanupModule]bool{}
		var keep func(*cleanupModule)
		keep = func(node *cleanupModule) {
			if expected[node] {
				return
			}
			expected[node] = true
			for _, provider := range node.dependencies {
				keep(provider)
			}
		}
		for i, node := range nodes {
			if mask&(1<<i) != 0 {
				node.stopErr = errors.New("retry cleanup")
				keep(node)
			}
		}
		runtime := NewRuntime(registry)
		if err := runtime.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := runtime.Stop(context.Background()); err == nil {
			t.Fatal("failed cleanup was acknowledged")
		}
		for _, node := range nodes {
			if node.active != expected[node] {
				t.Fatalf("sample %d mask %d: retained graph differs at %s", sample, mask, node.d.Name)
			}
			node.stopErr = nil
		}
		if err := runtime.Stop(context.Background()); err != nil {
			t.Fatal("retry lost a dependency", sample, err)
		}
		for _, node := range nodes {
			if node.active || node.stops > 2 || (!expected[node] && node.stops != 1) {
				t.Fatal("retry repeated completed cleanup or leaked resources", sample, node)
			}
		}
	}
}

func (m *cleanupModule) Descriptor() Descriptor      { return m.d }
func (m *cleanupModule) Start(context.Context) error { m.active = true; return nil }
func (m *cleanupModule) Ready(context.Context) error { return m.readyErr }
func (m *cleanupModule) Stop(context.Context) error {
	m.stops++
	if m.stopErr != nil {
		return m.stopErr
	}
	for _, dependency := range m.dependencies {
		if !dependency.active {
			return fmt.Errorf("cleanup dependency %s was already stopped", dependency.d.Name)
		}
	}
	m.active = false
	return nil
}

func TestBugbashFailedConsumerCleanupRetainsTransitiveDependencies(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(fmt.Sprintf("startup-rollback=%t", rollback), func(t *testing.T) {
			identity := &cleanupModule{d: descriptor("identity")}
			store := &cleanupModule{d: descriptor("store"), dependencies: []*cleanupModule{identity}}
			store.d.Dependencies = []Dependency{dependency("identity")}
			consumer := &cleanupModule{d: descriptor("quota"), dependencies: []*cleanupModule{store}, stopErr: errors.New("pending durable cleanup")}
			consumer.d.Dependencies = []Dependency{dependency("store")}
			audit := &cleanupModule{d: descriptor("audit")}
			if rollback {
				consumer.readyErr = errors.New("startup not ready")
			}
			registry := NewRegistry()
			for _, module := range []*cleanupModule{consumer, store, identity, audit} {
				register(t, registry, module)
			}
			runtime := NewRuntime(registry)
			err := runtime.Start(context.Background())
			if rollback {
				if err == nil {
					t.Fatal("failed startup returned success")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err = runtime.Stop(context.Background()); err == nil {
					t.Fatal("failed consumer cleanup returned success")
				}
			}
			if !identity.active || !store.active || identity.stops != 0 || store.stops != 0 || audit.active || audit.stops != 1 {
				t.Fatalf("cleanup destroyed a live consumer's dependencies: identity=%+v store=%+v audit=%+v", identity, store, audit)
			}
			if err := runtime.Start(context.Background()); err == nil {
				t.Fatal("restart accepted incomplete cleanup")
			}
			consumer.stopErr = nil
			if err := runtime.Stop(context.Background()); err != nil {
				t.Fatal("cleanup could not retry with its dependencies", err)
			}
			if identity.active || store.active || consumer.active || consumer.stops != 2 || store.stops != 1 || identity.stops != 1 || audit.stops != 1 {
				t.Fatal("cleanup replay repeated success or retained resources")
			}
		})
	}
}

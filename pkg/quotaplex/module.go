package quotaplex

import (
	"context"
	"fmt"
	"sync"

	"github.com/vamsiramakrishnan/aiplex/pkg/modules"
)

// Module adapts the core's readiness to the existing Plex lifecycle. It installs
// no routes or host interceptors. Store lifetime remains with the embedding host.
type Module struct {
	mu      sync.Mutex
	engine  *Engine
	running bool
}

func NewModule(engine *Engine) *Module { return &Module{engine: engine} }

func (*Module) Descriptor() modules.Descriptor {
	return modules.Descriptor{Name: "quotaplex", Version: "0.1.0", ContractVersion: modules.ContractVersion, Capabilities: []string{"quota.admission", "quota.budgets", "quota.rates", "quota.concurrency", "quota.exact-pricing", "quota.policy-revisions"}}
}

func (m *Module) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine == nil {
		return fmt.Errorf("quota engine is required")
	}
	if err := m.engine.Ready(ctx); err != nil {
		return err
	}
	m.running = true
	return nil
}

func (m *Module) Ready(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return fmt.Errorf("quotaplex module is not running")
	}
	return m.engine.Ready(ctx)
}

func (m *Module) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.running = false
	return ctx.Err()
}

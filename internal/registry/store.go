package registry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// Store abstracts persistence for AIPlex resources.
// The production implementation talks to Firestore; tests use MemoryStore.
type Store interface {
	// Instances
	GetInstance(ctx context.Context, id string) (*models.Instance, error)
	ListInstances(ctx context.Context, plane models.Plane) ([]models.Instance, error)
	PutInstance(ctx context.Context, inst *models.Instance) error
	DeleteInstance(ctx context.Context, id string) error

	// Templates (cached catalog entries)
	GetTemplate(ctx context.Context, id string) (*models.Template, error)
	ListTemplates(ctx context.Context, plane models.Plane, page, pageSize int) ([]models.Template, int, error)
	PutTemplate(ctx context.Context, t *models.Template) error

	// Agents
	GetAgent(ctx context.Context, clientID string) (*models.Agent, error)
	ListAgents(ctx context.Context) ([]models.Agent, error)
	PutAgent(ctx context.Context, a *models.Agent) error
	DeleteAgent(ctx context.Context, clientID string) error

	// Deploy history (append-only)
	AppendHistory(ctx context.Context, h *models.DeployHistory) error
	ListHistory(ctx context.Context, instanceID string, limit int) ([]models.DeployHistory, error)

	// User permissions (Dimension B)
	GetUserScopes(ctx context.Context, userID string) ([]string, error)
	SetUserScopes(ctx context.Context, userID string, scopes []string) error
}

// ErrNotFound is returned when a resource does not exist.
var ErrNotFound = fmt.Errorf("not found")

// MemoryStore is an in-memory Store implementation for development and testing.
type MemoryStore struct {
	mu         sync.RWMutex
	instances  map[string]*models.Instance
	templates  map[string]*models.Template
	agents     map[string]*models.Agent
	history    []models.DeployHistory
	userScopes map[string][]string
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		instances:  make(map[string]*models.Instance),
		templates:  make(map[string]*models.Template),
		agents:     make(map[string]*models.Agent),
		userScopes: make(map[string][]string),
	}
}

func (m *MemoryStore) GetInstance(_ context.Context, id string) (*models.Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[id]
	if !ok {
		return nil, ErrNotFound
	}
	return inst, nil
}

func (m *MemoryStore) ListInstances(_ context.Context, plane models.Plane) ([]models.Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []models.Instance
	for _, inst := range m.instances {
		if plane == "" || inst.Plane == plane {
			out = append(out, *inst)
		}
	}
	return out, nil
}

func (m *MemoryStore) PutInstance(_ context.Context, inst *models.Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst.UpdatedAt = time.Now()
	m.instances[inst.ID] = inst
	return nil
}

func (m *MemoryStore) DeleteInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.instances[id]; !ok {
		return ErrNotFound
	}
	delete(m.instances, id)
	return nil
}

func (m *MemoryStore) GetTemplate(_ context.Context, id string) (*models.Template, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.templates[id]
	if !ok {
		return nil, ErrNotFound
	}
	return t, nil
}

func (m *MemoryStore) ListTemplates(_ context.Context, plane models.Plane, page, pageSize int) ([]models.Template, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var filtered []models.Template
	for _, t := range m.templates {
		if plane == "" || t.Plane == plane {
			filtered = append(filtered, *t)
		}
	}
	total := len(filtered)
	start := page * pageSize
	if start >= total {
		return nil, total, nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return filtered[start:end], total, nil
}

func (m *MemoryStore) PutTemplate(_ context.Context, t *models.Template) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.templates[t.ID] = t
	return nil
}

func (m *MemoryStore) GetAgent(_ context.Context, clientID string) (*models.Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.agents[clientID]
	if !ok {
		return nil, ErrNotFound
	}
	return a, nil
}

func (m *MemoryStore) ListAgents(_ context.Context) ([]models.Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.Agent, 0, len(m.agents))
	for _, a := range m.agents {
		out = append(out, *a)
	}
	return out, nil
}

func (m *MemoryStore) PutAgent(_ context.Context, a *models.Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agents[a.ClientID] = a
	return nil
}

func (m *MemoryStore) DeleteAgent(_ context.Context, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.agents[clientID]; !ok {
		return ErrNotFound
	}
	delete(m.agents, clientID)
	return nil
}

func (m *MemoryStore) AppendHistory(_ context.Context, h *models.DeployHistory) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history = append(m.history, *h)
	return nil
}

func (m *MemoryStore) ListHistory(_ context.Context, instanceID string, limit int) ([]models.DeployHistory, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []models.DeployHistory
	// Walk backwards for most-recent-first
	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i].InstanceID == instanceID {
			out = append(out, m.history[i])
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *MemoryStore) GetUserScopes(_ context.Context, userID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	scopes, ok := m.userScopes[userID]
	if !ok {
		return nil, nil
	}
	return scopes, nil
}

func (m *MemoryStore) SetUserScopes(_ context.Context, userID string, scopes []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userScopes[userID] = scopes
	return nil
}

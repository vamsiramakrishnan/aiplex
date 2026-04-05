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

	// LLM route configs
	GetRouteConfig(ctx context.Context, modelID string) (*models.LLMRouteConfig, error)
	ListRouteConfigs(ctx context.Context) ([]models.LLMRouteConfig, error)
	PutRouteConfig(ctx context.Context, rc *models.LLMRouteConfig) error
	DeleteRouteConfig(ctx context.Context, modelID string) error

	// Provider configs
	GetProviderConfig(ctx context.Context, provider string) (*models.ProviderConfig, error)
	ListProviderConfigs(ctx context.Context) ([]models.ProviderConfig, error)
	PutProviderConfig(ctx context.Context, pc *models.ProviderConfig) error

	// LLM usage tracking
	AppendUsage(ctx context.Context, record *models.UsageRecord) error
	GetUsageSummary(ctx context.Context, modelID, agentID, period string) (*models.UsageSummary, error)
	ListUsageRecords(ctx context.Context, modelID, agentID string, since time.Time, limit int) ([]models.UsageRecord, error)

	// A2A delegations
	AppendDelegation(ctx context.Context, d *models.Delegation) error
	GetDelegation(ctx context.Context, id string) (*models.Delegation, error)
	ListDelegations(ctx context.Context, agentID string, limit int) ([]models.Delegation, error)
	UpdateDelegation(ctx context.Context, d *models.Delegation) error

	// Metrics / policy denials
	AppendPolicyDenial(ctx context.Context, d *models.PolicyDenial) error
	ListPolicyDenials(ctx context.Context, limit int) ([]models.PolicyDenial, error)
}

// ErrNotFound is returned when a resource does not exist.
var ErrNotFound = fmt.Errorf("not found")

// MemoryStore is an in-memory Store implementation for development and testing.
type MemoryStore struct {
	mu              sync.RWMutex
	instances       map[string]*models.Instance
	templates       map[string]*models.Template
	agents          map[string]*models.Agent
	history         []models.DeployHistory
	userScopes      map[string][]string
	routeConfigs    map[string]*models.LLMRouteConfig
	providerConfigs map[string]*models.ProviderConfig
	usageRecords    []models.UsageRecord
	delegations     map[string]*models.Delegation
	delegationList  []models.Delegation
	policyDenials   []models.PolicyDenial
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		instances:       make(map[string]*models.Instance),
		templates:       make(map[string]*models.Template),
		agents:          make(map[string]*models.Agent),
		userScopes:      make(map[string][]string),
		routeConfigs:    make(map[string]*models.LLMRouteConfig),
		providerConfigs: make(map[string]*models.ProviderConfig),
		delegations:     make(map[string]*models.Delegation),
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

// ── LLM Route Configs ──

func (m *MemoryStore) GetRouteConfig(_ context.Context, modelID string) (*models.LLMRouteConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rc, ok := m.routeConfigs[modelID]
	if !ok {
		return nil, ErrNotFound
	}
	return rc, nil
}

func (m *MemoryStore) ListRouteConfigs(_ context.Context) ([]models.LLMRouteConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.LLMRouteConfig, 0, len(m.routeConfigs))
	for _, rc := range m.routeConfigs {
		out = append(out, *rc)
	}
	return out, nil
}

func (m *MemoryStore) PutRouteConfig(_ context.Context, rc *models.LLMRouteConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rc.UpdatedAt = time.Now()
	if rc.CreatedAt.IsZero() {
		rc.CreatedAt = time.Now()
	}
	m.routeConfigs[rc.ModelID] = rc
	return nil
}

func (m *MemoryStore) DeleteRouteConfig(_ context.Context, modelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.routeConfigs[modelID]; !ok {
		return ErrNotFound
	}
	delete(m.routeConfigs, modelID)
	return nil
}

// ── Provider Configs ──

func (m *MemoryStore) GetProviderConfig(_ context.Context, provider string) (*models.ProviderConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pc, ok := m.providerConfigs[provider]
	if !ok {
		return nil, ErrNotFound
	}
	return pc, nil
}

func (m *MemoryStore) ListProviderConfigs(_ context.Context) ([]models.ProviderConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.ProviderConfig, 0, len(m.providerConfigs))
	for _, pc := range m.providerConfigs {
		out = append(out, *pc)
	}
	return out, nil
}

func (m *MemoryStore) PutProviderConfig(_ context.Context, pc *models.ProviderConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providerConfigs[pc.Provider] = pc
	return nil
}

// ── Usage Tracking ──

func (m *MemoryStore) AppendUsage(_ context.Context, record *models.UsageRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usageRecords = append(m.usageRecords, *record)
	return nil
}

func (m *MemoryStore) GetUsageSummary(_ context.Context, modelID, agentID, period string) (*models.UsageSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var cutoff time.Time
	now := time.Now()
	switch period {
	case "day":
		cutoff = now.Add(-24 * time.Hour)
	case "week":
		cutoff = now.Add(-7 * 24 * time.Hour)
	case "month":
		cutoff = now.Add(-30 * 24 * time.Hour)
	default:
		cutoff = now.Add(-24 * time.Hour)
		period = "day"
	}

	summary := &models.UsageSummary{ModelID: modelID, AgentID: agentID, Period: period}
	var totalLatency int64
	for _, r := range m.usageRecords {
		if r.Timestamp.Before(cutoff) {
			continue
		}
		if modelID != "" && r.ModelID != modelID {
			continue
		}
		if agentID != "" && r.AgentID != agentID {
			continue
		}
		summary.InputTokens += int64(r.InputTokens)
		summary.OutputTokens += int64(r.OutputTokens)
		summary.TotalTokens += int64(r.TotalTokens)
		summary.TotalCostUSD += r.CostUSD
		summary.RequestCount++
		totalLatency += int64(r.LatencyMs)
		if r.Cached {
			summary.CacheHits++
		}
	}
	if summary.RequestCount > 0 {
		summary.AvgLatencyMs = float64(totalLatency) / float64(summary.RequestCount)
	}
	return summary, nil
}

func (m *MemoryStore) ListUsageRecords(_ context.Context, modelID, agentID string, since time.Time, limit int) ([]models.UsageRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []models.UsageRecord
	for i := len(m.usageRecords) - 1; i >= 0; i-- {
		r := m.usageRecords[i]
		if r.Timestamp.Before(since) {
			continue
		}
		if modelID != "" && r.ModelID != modelID {
			continue
		}
		if agentID != "" && r.AgentID != agentID {
			continue
		}
		out = append(out, r)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// ── A2A Delegations ──

func (m *MemoryStore) AppendDelegation(_ context.Context, d *models.Delegation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delegations[d.ID] = d
	m.delegationList = append(m.delegationList, *d)
	return nil
}

func (m *MemoryStore) GetDelegation(_ context.Context, id string) (*models.Delegation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.delegations[id]
	if !ok {
		return nil, ErrNotFound
	}
	return d, nil
}

func (m *MemoryStore) ListDelegations(_ context.Context, agentID string, limit int) ([]models.Delegation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []models.Delegation
	for i := len(m.delegationList) - 1; i >= 0; i-- {
		d := m.delegationList[i]
		if agentID != "" && d.CallerAgentID != agentID && d.CalleeAgentID != agentID {
			continue
		}
		out = append(out, d)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *MemoryStore) UpdateDelegation(_ context.Context, d *models.Delegation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.delegations[d.ID]; !ok {
		return ErrNotFound
	}
	m.delegations[d.ID] = d
	return nil
}

// ── Policy Denials ──

func (m *MemoryStore) AppendPolicyDenial(_ context.Context, d *models.PolicyDenial) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policyDenials = append(m.policyDenials, *d)
	return nil
}

func (m *MemoryStore) ListPolicyDenials(_ context.Context, limit int) ([]models.PolicyDenial, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []models.PolicyDenial
	for i := len(m.policyDenials) - 1; i >= 0; i-- {
		out = append(out, m.policyDenials[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

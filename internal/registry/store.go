package registry

import (
	"context"
	"fmt"
	"sort"
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

	// SkillsPlex invocations (append-only audit)
	AppendSkillInvocation(ctx context.Context, inv *models.SkillInvocation) error
	ListSkillInvocations(ctx context.Context, agentID, skillName string, limit int) ([]models.SkillInvocation, error)

	// Metrics / policy denials
	AppendPolicyDenial(ctx context.Context, d *models.PolicyDenial) error
	ListPolicyDenials(ctx context.Context, limit int) ([]models.PolicyDenial, error)

	// Counts (efficient dashboard queries)
	CountDelegations(ctx context.Context) (int64, error)
	CountPolicyDenials(ctx context.Context) (int64, error)

	// IAM role bindings (group → role + scopes)
	GetRoleBinding(ctx context.Context, id string) (*models.RoleBinding, error)
	ListRoleBindings(ctx context.Context) ([]models.RoleBinding, error)
	ListRoleBindingsByGroup(ctx context.Context, group string) ([]models.RoleBinding, error)
	PutRoleBinding(ctx context.Context, rb *models.RoleBinding) error
	DeleteRoleBinding(ctx context.Context, id string) error

	// AIPlex ↔ Tape audit ingestion (PR 6).
	//
	// Tape's outbox relay POSTs events to /internal/tape/events; the
	// handler dedupes on (RunID, Seq), quarantines events for unknown
	// agents, and updates the ExecutionRun projection.
	//
	// AppendExecutionEvent returns (true, nil) when the row was newly
	// written, (false, nil) when it was a duplicate (idempotent no-op),
	// or (false, err) on a real error.
	AppendExecutionEvent(ctx context.Context, ev *models.ExecutionEvent) (bool, error)
	QuarantineExecutionEvent(ctx context.Context, q *models.QuarantinedExecutionEvent) error
	GetExecutionRun(ctx context.Context, runID string) (*models.ExecutionRun, error)
	UpsertExecutionRun(ctx context.Context, run *models.ExecutionRun) error
	DeleteExecutionRun(ctx context.Context, runID string) error
	ListExecutionRuns(ctx context.Context, tenantID, agentID string, limit int) ([]models.ExecutionRun, error)
	ListExecutionEvents(ctx context.Context, runID string, fromSeq int64, limit int) ([]models.ExecutionEvent, error)
	// Last-ingest timestamp drives the Runs page health checklist
	// (PR 11 item 13) — "have we received any events recently?"
	LastIngestAt(ctx context.Context) (time.Time, error)

	// AIPlex ↔ Tape operator audit (PR 11 item 8). Distinct from
	// ExecutionEvent so projection rebuild from Tape's outbox doesn't
	// drop operator history.
	AppendOperatorAudit(ctx context.Context, a *models.OperatorAudit) error
	ListOperatorAudit(ctx context.Context, runID string, limit int) ([]models.OperatorAudit, error)

	// Count instances whose Runtime.Engine matches — drives the
	// tape-server lifecycle status endpoint (PR 11 item 17).
	CountInstancesWithRuntime(ctx context.Context, engine models.RuntimeEngine) (int, error)
}

// ErrNotFound is returned when a resource does not exist.
var ErrNotFound = fmt.Errorf("not found")

// MemoryStore is an in-memory Store implementation for development and testing.
type MemoryStore struct {
	configurationRevisions  map[string]configurationRevision
	configurationGeneration int64
	mu                      sync.RWMutex
	instances               map[string]*models.Instance
	templates               map[string]*models.Template
	agents                  map[string]*models.Agent
	history                 []models.DeployHistory
	userScopes              map[string][]string
	routeConfigs            map[string]*models.LLMRouteConfig
	providerConfigs         map[string]*models.ProviderConfig
	usageRecords            []models.UsageRecord
	delegations             map[string]*models.Delegation
	delegationList          []models.Delegation
	skillInvocations        []models.SkillInvocation
	skillInvocationIDs      map[string]struct{}
	policyDenials           []models.PolicyDenial
	roleBindings            map[string]*models.RoleBinding
	// Execution audit (AIPlex ↔ Tape integration PR 6 + PR 11).
	executionEvents     map[string][]models.ExecutionEvent // runID → events (append-only)
	executionEventKeys  map[string]struct{}                // "<runID>/<seq>" idempotency set
	executionRuns       map[string]*models.ExecutionRun    // runID → projected summary
	executionQuarantine []models.QuarantinedExecutionEvent // unknown-agent quarantine
	lastIngest          time.Time                          // PR 11 item 13
	operatorAudit       map[string][]models.OperatorAudit  // runID → audit rows (PR 11 item 8)
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		instances:          make(map[string]*models.Instance),
		templates:          make(map[string]*models.Template),
		agents:             make(map[string]*models.Agent),
		userScopes:         make(map[string][]string),
		routeConfigs:       make(map[string]*models.LLMRouteConfig),
		providerConfigs:    make(map[string]*models.ProviderConfig),
		delegations:        make(map[string]*models.Delegation),
		roleBindings:       make(map[string]*models.RoleBinding),
		executionEvents:    make(map[string][]models.ExecutionEvent),
		executionEventKeys: make(map[string]struct{}),
		executionRuns:      make(map[string]*models.ExecutionRun),
		operatorAudit:      make(map[string][]models.OperatorAudit),
	}
}

func (m *MemoryStore) GetInstance(_ context.Context, id string) (*models.Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[id]
	if !ok {
		return nil, ErrNotFound
	}
	return memorySnapshot(inst), nil
}

func (m *MemoryStore) ListInstances(_ context.Context, plane models.Plane) ([]models.Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []models.Instance
	for _, inst := range m.instances {
		if plane == "" || inst.Plane == plane {
			out = append(out, *memorySnapshot(inst))
		}
	}
	return out, nil
}

func (m *MemoryStore) PutInstance(_ context.Context, inst *models.Instance) error {
	if inst == nil || inst.ID == "" {
		return fmt.Errorf("instance identity required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inst.UpdatedAt = time.Now()
	m.instances[inst.ID] = memorySnapshot(inst)
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
	return memorySnapshot(t), nil
}

func (m *MemoryStore) ListTemplates(_ context.Context, plane models.Plane, page, pageSize int) ([]models.Template, int, error) {
	if page < 0 || pageSize <= 0 {
		return nil, 0, fmt.Errorf("valid template pagination required")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var filtered []models.Template
	for _, t := range m.templates {
		if plane == "" || t.Plane == plane {
			filtered = append(filtered, *memorySnapshot(t))
		}
	}
	total := len(filtered)
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Name != filtered[j].Name {
			return filtered[i].Name < filtered[j].Name
		}
		return filtered[i].ID < filtered[j].ID
	})
	if page > total/pageSize {
		return nil, total, nil
	}
	start := page * pageSize
	if start >= total {
		return nil, total, nil
	}
	end := total
	if pageSize < total-start {
		end = start + pageSize
	}
	return filtered[start:end], total, nil
}

func (m *MemoryStore) PutTemplate(_ context.Context, t *models.Template) error {
	if t == nil || t.ID == "" {
		return fmt.Errorf("template identity required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.templates[t.ID] = memorySnapshot(t)
	return nil
}

func (m *MemoryStore) GetAgent(_ context.Context, clientID string) (*models.Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.agents[clientID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneAuthAgent(a), nil
}

func (m *MemoryStore) ListAgents(_ context.Context) ([]models.Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.Agent, 0, len(m.agents))
	for _, a := range m.agents {
		out = append(out, *cloneAuthAgent(a))
	}
	return out, nil
}

func (m *MemoryStore) PutAgent(ctx context.Context, a *models.Agent) error {
	if a == nil {
		return fmt.Errorf("configuration required")
	}
	return PutLegacyConfiguration(ctx, m, ConfigAgent, a.ClientID, a)
}

func (m *MemoryStore) DeleteAgent(ctx context.Context, clientID string) error {
	return DeleteLegacyConfiguration(ctx, m, ConfigAgent, clientID)
}

func (m *MemoryStore) AppendHistory(_ context.Context, h *models.DeployHistory) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history = append(m.history, *memorySnapshot(h))
	return nil
}

func (m *MemoryStore) ListHistory(_ context.Context, instanceID string, limit int) ([]models.DeployHistory, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []models.DeployHistory
	// Walk backwards for most-recent-first
	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i].InstanceID == instanceID {
			out = append(out, memorySnapshot(m.history[i]))
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
	return cloneAuthScopes(scopes), nil
}

func (m *MemoryStore) SetUserScopes(ctx context.Context, userID string, scopes []string) error {
	value := &configurationScopes{Scopes: scopes}
	return PutLegacyConfiguration(ctx, m, ConfigUserScopes, userID, value)
}

// ── LLM Route Configs ──

func (m *MemoryStore) GetRouteConfig(_ context.Context, modelID string) (*models.LLMRouteConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rc, ok := m.routeConfigs[modelID]
	if !ok {
		return nil, ErrNotFound
	}
	return memorySnapshot(rc), nil
}

func (m *MemoryStore) ListRouteConfigs(_ context.Context) ([]models.LLMRouteConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.LLMRouteConfig, 0, len(m.routeConfigs))
	for _, rc := range m.routeConfigs {
		out = append(out, *memorySnapshot(rc))
	}
	return out, nil
}

func (m *MemoryStore) PutRouteConfig(ctx context.Context, rc *models.LLMRouteConfig) error {
	if rc == nil {
		return fmt.Errorf("configuration required")
	}
	return PutLegacyConfiguration(ctx, m, ConfigRoute, rc.ModelID, rc)
}

func (m *MemoryStore) DeleteRouteConfig(ctx context.Context, modelID string) error {
	return DeleteLegacyConfiguration(ctx, m, ConfigRoute, modelID)
}

// ── Provider Configs ──

func (m *MemoryStore) GetProviderConfig(_ context.Context, provider string) (*models.ProviderConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pc, ok := m.providerConfigs[provider]
	if !ok {
		return nil, ErrNotFound
	}
	return memorySnapshot(pc), nil
}

func (m *MemoryStore) ListProviderConfigs(_ context.Context) ([]models.ProviderConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.ProviderConfig, 0, len(m.providerConfigs))
	for _, pc := range m.providerConfigs {
		out = append(out, *memorySnapshot(pc))
	}
	return out, nil
}

func (m *MemoryStore) PutProviderConfig(ctx context.Context, pc *models.ProviderConfig) error {
	if pc == nil {
		return fmt.Errorf("configuration required")
	}
	return PutLegacyConfiguration(ctx, m, ConfigProvider, pc.Provider, pc)
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
	if _, ok := m.delegations[d.ID]; ok {
		return ErrAlreadyExists
	}
	m.delegations[d.ID] = cloneDelegation(d)
	m.delegationList = append(m.delegationList, *cloneDelegation(d))
	return nil
}

func (m *MemoryStore) GetDelegation(_ context.Context, id string) (*models.Delegation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.delegations[id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneDelegation(d), nil
}

func (m *MemoryStore) ListDelegations(_ context.Context, agentID string, limit int) ([]models.Delegation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.Delegation, 0)
	for i := len(m.delegationList) - 1; i >= 0; i-- {
		d := *cloneDelegation(m.delegations[m.delegationList[i].ID])
		if agentID != "" && d.CallerAgentID != agentID && d.CalleeAgentID != agentID {
			continue
		}
		out = append(out, d)
	}
	sortDelegations(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) UpdateDelegation(_ context.Context, d *models.Delegation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous, ok := m.delegations[d.ID]
	if !ok {
		return ErrNotFound
	}
	if e := delegationTransition(previous, d); e != nil {
		return e
	}
	m.delegations[d.ID] = cloneDelegation(d)
	return nil
}

// ── SkillsPlex Invocations ──

func (m *MemoryStore) AppendSkillInvocation(_ context.Context, inv *models.SkillInvocation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.skillInvocationIDs == nil {
		m.skillInvocationIDs = map[string]struct{}{}
		for _, stored := range m.skillInvocations {
			m.skillInvocationIDs[stored.ID] = struct{}{}
		}
	}
	if _, ok := m.skillInvocationIDs[inv.ID]; ok {
		return ErrAlreadyExists
	}
	m.skillInvocationIDs[inv.ID] = struct{}{}
	m.skillInvocations = append(m.skillInvocations, *inv)
	return nil
}

func (m *MemoryStore) ListSkillInvocations(_ context.Context, agentID, skillName string, limit int) ([]models.SkillInvocation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.SkillInvocation, 0)
	for i := len(m.skillInvocations) - 1; i >= 0; i-- {
		inv := m.skillInvocations[i]
		if agentID != "" && inv.AgentID != agentID {
			continue
		}
		if skillName != "" && inv.SkillName != skillName {
			continue
		}
		out = append(out, inv)
	}
	sortSkillInvocations(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
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

func (m *MemoryStore) CountDelegations(_ context.Context) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int64(len(m.delegationList)), nil
}

func (m *MemoryStore) CountPolicyDenials(_ context.Context) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int64(len(m.policyDenials)), nil
}

// ── IAM Role Bindings ──

func (m *MemoryStore) GetRoleBinding(_ context.Context, id string) (*models.RoleBinding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rb, ok := m.roleBindings[id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneAuthRoleBinding(rb), nil
}

func (m *MemoryStore) ListRoleBindings(_ context.Context) ([]models.RoleBinding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.RoleBinding, 0, len(m.roleBindings))
	for _, rb := range m.roleBindings {
		out = append(out, *cloneAuthRoleBinding(rb))
	}
	return out, nil
}

func (m *MemoryStore) ListRoleBindingsByGroup(_ context.Context, group string) ([]models.RoleBinding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []models.RoleBinding
	for _, rb := range m.roleBindings {
		if rb.Group == group {
			out = append(out, *cloneAuthRoleBinding(rb))
		}
	}
	return out, nil
}

func (m *MemoryStore) PutRoleBinding(ctx context.Context, rb *models.RoleBinding) error {
	if rb == nil {
		return fmt.Errorf("configuration required")
	}
	return PutLegacyConfiguration(ctx, m, ConfigRoleBinding, rb.ID, rb)
}

func (m *MemoryStore) DeleteRoleBinding(ctx context.Context, id string) error {
	return DeleteLegacyConfiguration(ctx, m, ConfigRoleBinding, id)
}

// ── Execution audit (AIPlex ↔ Tape integration PR 6) ──────────────────────

// AppendExecutionEvent stores one event. Returns wrote=true on a fresh
// row, wrote=false on a duplicate (idempotent on (RunID, Seq)).
//
// Out-of-order events are persisted in the order they arrive and sorted
// at read time — the projection (UpsertExecutionRun) doesn't depend on
// ordered ingest because every counter is monotonic-by-kind.
func (m *MemoryStore) AppendExecutionEvent(_ context.Context, ev *models.ExecutionEvent) (bool, error) {
	if ev == nil || ev.RunID == "" {
		return false, fmt.Errorf("execution event: run_id required")
	}
	key := fmt.Sprintf("%s/%d", ev.RunID, ev.Seq)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.executionEventKeys[key]; dup {
		return false, nil
	}
	m.executionEventKeys[key] = struct{}{}
	m.executionEvents[ev.RunID] = append(m.executionEvents[ev.RunID], *ev)
	m.lastIngest = time.Now()
	return true, nil
}

func (m *MemoryStore) QuarantineExecutionEvent(_ context.Context, q *models.QuarantinedExecutionEvent) error {
	if q == nil {
		return fmt.Errorf("quarantine: nil event")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executionQuarantine = append(m.executionQuarantine, *q)
	return nil
}

func (m *MemoryStore) GetExecutionRun(_ context.Context, runID string) (*models.ExecutionRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.executionRuns[runID]
	if !ok {
		return nil, ErrNotFound
	}
	// Defensive copy so callers can't mutate through the pointer.
	cp := *run
	return &cp, nil
}

func (m *MemoryStore) UpsertExecutionRun(_ context.Context, run *models.ExecutionRun) error {
	if run == nil || run.RunID == "" {
		return fmt.Errorf("upsert execution run: run_id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *run
	m.executionRuns[run.RunID] = &cp
	return nil
}

func (m *MemoryStore) ListExecutionRuns(_ context.Context, tenantID, agentID string, limit int) ([]models.ExecutionRun, error) {
	if limit <= 0 {
		limit = 100
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.ExecutionRun, 0, limit)
	for _, r := range m.executionRuns {
		if tenantID != "" && r.TenantID != tenantID {
			continue
		}
		if agentID != "" && r.AgentID != agentID {
			continue
		}
		out = append(out, *r)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *MemoryStore) ListExecutionEvents(_ context.Context, runID string, fromSeq int64, limit int) ([]models.ExecutionEvent, error) {
	if limit <= 0 {
		limit = 1000
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	events := m.executionEvents[runID]
	out := make([]models.ExecutionEvent, 0, len(events))
	for _, e := range events {
		if e.Seq < fromSeq {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	// Sort by seq so callers see a stable timeline regardless of
	// ingest order (out-of-order events are allowed at the wire level).
	sortExecutionEvents(out)
	return out, nil
}

func sortExecutionEvents(es []models.ExecutionEvent) {
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && es[j-1].Seq > es[j].Seq; j-- {
			es[j-1], es[j] = es[j], es[j-1]
		}
	}
}

// ── PR 11 extensions ──────────────────────────────────────────────────────

func (m *MemoryStore) DeleteExecutionRun(_ context.Context, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.executionRuns, runID)
	return nil
}

func (m *MemoryStore) LastIngestAt(_ context.Context) (time.Time, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastIngest, nil
}

func (m *MemoryStore) AppendOperatorAudit(_ context.Context, a *models.OperatorAudit) error {
	if a == nil || a.RunID == "" {
		return fmt.Errorf("operator audit: run_id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *a
	m.operatorAudit[a.RunID] = append(m.operatorAudit[a.RunID], cp)
	return nil
}

func (m *MemoryStore) ListOperatorAudit(_ context.Context, runID string, limit int) ([]models.OperatorAudit, error) {
	if limit <= 0 {
		limit = 100
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := m.operatorAudit[runID]
	if len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	out := make([]models.OperatorAudit, len(rows))
	copy(out, rows)
	return out, nil
}

func (m *MemoryStore) CountInstancesWithRuntime(_ context.Context, engine models.RuntimeEngine) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, inst := range m.instances {
		if inst.Runtime.Engine == engine {
			n++
		}
	}
	return n, nil
}

// Authentication records never share caller-owned mutable scopes/maps with the
// store. Preserve nil versus an explicit empty scope list for role defaults.
func cloneAuthScopes(scopes []string) []string {
	if scopes == nil {
		return nil
	}
	return append(make([]string, 0, len(scopes)), scopes...)
}
func cloneAuthAgent(agent *models.Agent) *models.Agent {
	cloned := *agent
	cloned.GrantTypes = cloneAuthScopes(agent.GrantTypes)
	cloned.AllowedScopes = cloneAuthScopes(agent.AllowedScopes)
	cloned.RedirectURIs = cloneAuthScopes(agent.RedirectURIs)
	if agent.Labels != nil {
		cloned.Labels = make(map[string]string, len(agent.Labels))
		for key, value := range agent.Labels {
			cloned.Labels[key] = value
		}
	}
	return &cloned
}
func cloneAuthRoleBinding(binding *models.RoleBinding) *models.RoleBinding {
	cloned := *binding
	cloned.Scopes = cloneAuthScopes(binding.Scopes)
	return &cloned
}

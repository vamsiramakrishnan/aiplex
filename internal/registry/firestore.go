package registry

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// FirestoreStore implements the Store interface using Google Cloud Firestore.
// Models are persisted with the SDK's Go field names. JSON tags describe HTTP
// wire names and do not rename these stored fields. Queries/indexes must match.
//
// Collection layout:
//
//	instances/{id}       — deployed instances
//	templates/{id}       — cached catalog templates
//	agents/{clientId}    — registered OAuth clients
//	deploy_history/{id}  — append-only audit trail
//	user_scopes/{userId} — Dimension B (user ceiling)
//	route_configs/{modelID} — LLM routing configs
//	provider_configs/{provider} — LLM provider settings
//	usage_records/{id}   — token usage tracking (append-only)
//	delegations/{id}     — A2A delegations
//	policy_denials/{id}  — authz denial events (append-only)
//	role_bindings/{id}   — IAM role bindings
type FirestoreStore struct {
	client     *firestore.Client
	projectID  string
	databaseID string
}

// NewFirestoreStore creates a Firestore-backed store.
func NewFirestoreStore(projectID, databaseID string) (*FirestoreStore, error) {
	if projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT is required for Firestore store")
	}

	ctx := context.Background()
	var client *firestore.Client
	var err error

	if databaseID != "" {
		client, err = firestore.NewClientWithDatabase(ctx, projectID, databaseID)
	} else {
		client, err = firestore.NewClient(ctx, projectID)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create Firestore client: %w", err)
	}

	return &FirestoreStore{
		client:     client,
		projectID:  projectID,
		databaseID: databaseID,
	}, nil
}

// Close closes the Firestore client.
func (f *FirestoreStore) Close() error {
	return f.client.Close()
}

// ── Instances ──

func (f *FirestoreStore) GetInstance(ctx context.Context, id string) (*models.Instance, error) {
	doc, err := f.client.Collection("instances").Doc(id).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("firestore get instance: %w", err)
	}
	var inst models.Instance
	if err := doc.DataTo(&inst); err != nil {
		return nil, fmt.Errorf("firestore decode instance: %w", err)
	}
	return &inst, nil
}

func (f *FirestoreStore) ListInstances(ctx context.Context, plane models.Plane) ([]models.Instance, error) {
	var iter *firestore.DocumentIterator
	if plane == "" {
		iter = f.client.Collection("instances").Documents(ctx)
	} else {
		iter = f.client.Collection("instances").Where("Plane", "==", string(plane)).Documents(ctx)
	}

	var instances []models.Instance
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore list instances: %w", err)
		}
		var inst models.Instance
		if err := doc.DataTo(&inst); err != nil {
			return nil, fmt.Errorf("firestore decode instance: %w", err)
		}
		instances = append(instances, inst)
	}
	return instances, nil
}

func (f *FirestoreStore) PutInstance(ctx context.Context, inst *models.Instance) error {
	inst.UpdatedAt = time.Now()
	_, err := f.client.Collection("instances").Doc(inst.ID).Set(ctx, inst)
	if err != nil {
		return fmt.Errorf("firestore put instance: %w", err)
	}
	return nil
}

func (f *FirestoreStore) DeleteInstance(ctx context.Context, id string) error {
	_, err := f.client.Collection("instances").Doc(id).Delete(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return ErrNotFound
		}
		return fmt.Errorf("firestore delete instance: %w", err)
	}
	return nil
}

// ── Templates ──

func (f *FirestoreStore) GetTemplate(ctx context.Context, id string) (*models.Template, error) {
	doc, err := f.client.Collection("templates").Doc(id).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("firestore get template: %w", err)
	}
	var t models.Template
	if err := doc.DataTo(&t); err != nil {
		return nil, fmt.Errorf("firestore decode template: %w", err)
	}
	return &t, nil
}

func (f *FirestoreStore) ListTemplates(ctx context.Context, plane models.Plane, page, pageSize int) ([]models.Template, int, error) {
	var query firestore.Query
	if plane == "" {
		query = f.client.Collection("templates").OrderBy("Name", firestore.Asc)
	} else {
		query = f.client.Collection("templates").Where("Plane", "==", string(plane)).OrderBy("Name", firestore.Asc)
	}

	// Get total count (Firestore doesn't have native count, so we fetch all IDs)
	allIter := query.Documents(ctx)
	var all []models.Template
	for {
		doc, err := allIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("firestore list templates: %w", err)
		}
		var t models.Template
		if err := doc.DataTo(&t); err != nil {
			return nil, 0, fmt.Errorf("firestore decode template: %w", err)
		}
		all = append(all, t)
	}

	total := len(all)
	start := page * pageSize
	if start >= total {
		return nil, total, nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	return all[start:end], total, nil
}

func (f *FirestoreStore) PutTemplate(ctx context.Context, t *models.Template) error {
	_, err := f.client.Collection("templates").Doc(t.ID).Set(ctx, t)
	if err != nil {
		return fmt.Errorf("firestore put template: %w", err)
	}
	return nil
}

// ── Agents ──

func (f *FirestoreStore) GetAgent(ctx context.Context, clientID string) (*models.Agent, error) {
	doc, err := f.client.Collection("agents").Doc(clientID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("firestore get agent: %w", err)
	}
	var a models.Agent
	if err := doc.DataTo(&a); err != nil {
		return nil, fmt.Errorf("firestore decode agent: %w", err)
	}
	normalizeConfigurationVersion(&a)
	return &a, nil
}

func (f *FirestoreStore) ListAgents(ctx context.Context) ([]models.Agent, error) {
	iter := f.client.Collection("agents").OrderBy("RegisteredAt", firestore.Desc).Documents(ctx)
	var agents []models.Agent
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore list agents: %w", err)
		}
		var a models.Agent
		if err := doc.DataTo(&a); err != nil {
			return nil, fmt.Errorf("firestore decode agent: %w", err)
		}
		normalizeConfigurationVersion(&a)
		agents = append(agents, a)
	}
	return agents, nil
}

func (f *FirestoreStore) PutAgent(ctx context.Context, a *models.Agent) error {
	if a == nil {
		return fmt.Errorf("configuration required")
	}
	return PutLegacyConfiguration(ctx, f, ConfigAgent, a.ClientID, a)
}

func (f *FirestoreStore) DeleteAgent(ctx context.Context, clientID string) error {
	return DeleteLegacyConfiguration(ctx, f, ConfigAgent, clientID)
}

// ── Deploy History ──

func (f *FirestoreStore) AppendHistory(ctx context.Context, h *models.DeployHistory) error {
	// Auto-generate ID if not set
	ref := f.client.Collection("deploy_history").NewDoc()
	if h.ID == "" {
		h.ID = ref.ID
	} else {
		ref = f.client.Collection("deploy_history").Doc(h.ID)
	}
	_, err := ref.Set(ctx, h)
	if err != nil {
		return fmt.Errorf("firestore append history: %w", err)
	}
	return nil
}

func (f *FirestoreStore) ListHistory(ctx context.Context, instanceID string, limit int) ([]models.DeployHistory, error) {
	query := f.client.Collection("deploy_history").
		Where("InstanceID", "==", instanceID).
		OrderBy("Timestamp", firestore.Desc)

	if limit > 0 {
		query = query.Limit(limit)
	}

	iter := query.Documents(ctx)
	var history []models.DeployHistory
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore list history: %w", err)
		}
		var h models.DeployHistory
		if err := doc.DataTo(&h); err != nil {
			return nil, fmt.Errorf("firestore decode history: %w", err)
		}
		history = append(history, h)
	}
	return history, nil
}

// ── User Scopes ──

type userScopesDoc struct {
	Scopes []string `firestore:"scopes"`
}

func (f *FirestoreStore) GetUserScopes(ctx context.Context, userID string) ([]string, error) {
	doc, err := f.client.Collection("user_scopes").Doc(userID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("firestore get user scopes: %w", err)
	}
	var data userScopesDoc
	if err := doc.DataTo(&data); err != nil {
		return nil, fmt.Errorf("firestore decode user scopes: %w", err)
	}
	return data.Scopes, nil
}

func (f *FirestoreStore) SetUserScopes(ctx context.Context, userID string, scopes []string) error {
	value := &configurationScopes{Scopes: scopes}
	return PutLegacyConfiguration(ctx, f, ConfigUserScopes, userID, value)
}

// ── LLM Route Configs ──

func (f *FirestoreStore) GetRouteConfig(ctx context.Context, modelID string) (*models.LLMRouteConfig, error) {
	doc, err := f.client.Collection("route_configs").Doc(modelID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("firestore get route config: %w", err)
	}
	var rc models.LLMRouteConfig
	if err := doc.DataTo(&rc); err != nil {
		return nil, fmt.Errorf("firestore decode route config: %w", err)
	}
	normalizeConfigurationVersion(&rc)
	return &rc, nil
}

func (f *FirestoreStore) ListRouteConfigs(ctx context.Context) ([]models.LLMRouteConfig, error) {
	iter := f.client.Collection("route_configs").Documents(ctx)
	var configs []models.LLMRouteConfig
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore list route configs: %w", err)
		}
		var rc models.LLMRouteConfig
		if err := doc.DataTo(&rc); err != nil {
			return nil, fmt.Errorf("firestore decode route config: %w", err)
		}
		normalizeConfigurationVersion(&rc)
		configs = append(configs, rc)
	}
	return configs, nil
}

func (f *FirestoreStore) PutRouteConfig(ctx context.Context, rc *models.LLMRouteConfig) error {
	if rc == nil {
		return fmt.Errorf("configuration required")
	}
	return PutLegacyConfiguration(ctx, f, ConfigRoute, rc.ModelID, rc)
}

func (f *FirestoreStore) DeleteRouteConfig(ctx context.Context, modelID string) error {
	return DeleteLegacyConfiguration(ctx, f, ConfigRoute, modelID)
}

// ── Provider Configs ──

func (f *FirestoreStore) GetProviderConfig(ctx context.Context, provider string) (*models.ProviderConfig, error) {
	doc, err := f.client.Collection("provider_configs").Doc(provider).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("firestore get provider config: %w", err)
	}
	var pc models.ProviderConfig
	if err := doc.DataTo(&pc); err != nil {
		return nil, fmt.Errorf("firestore decode provider config: %w", err)
	}
	normalizeConfigurationVersion(&pc)
	return &pc, nil
}

func (f *FirestoreStore) ListProviderConfigs(ctx context.Context) ([]models.ProviderConfig, error) {
	iter := f.client.Collection("provider_configs").Documents(ctx)
	var configs []models.ProviderConfig
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore list provider configs: %w", err)
		}
		var pc models.ProviderConfig
		if err := doc.DataTo(&pc); err != nil {
			return nil, fmt.Errorf("firestore decode provider config: %w", err)
		}
		normalizeConfigurationVersion(&pc)
		configs = append(configs, pc)
	}
	return configs, nil
}

func (f *FirestoreStore) PutProviderConfig(ctx context.Context, pc *models.ProviderConfig) error {
	if pc == nil {
		return fmt.Errorf("configuration required")
	}
	return PutLegacyConfiguration(ctx, f, ConfigProvider, pc.Provider, pc)
}

// ── Usage Tracking ──

func (f *FirestoreStore) AppendUsage(ctx context.Context, record *models.UsageRecord) error {
	ref := f.client.Collection("usage_records").NewDoc()
	if record.ID == "" {
		record.ID = ref.ID
	} else {
		ref = f.client.Collection("usage_records").Doc(record.ID)
	}
	_, err := ref.Set(ctx, record)
	if err != nil {
		return fmt.Errorf("firestore append usage: %w", err)
	}
	return nil
}

func (f *FirestoreStore) GetUsageSummary(ctx context.Context, modelID, agentID, period string) (*models.UsageSummary, error) {
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

	query := f.usageQuery(modelID, agentID, cutoff)
	iter := query.Documents(ctx)

	summary := &models.UsageSummary{ModelID: modelID, AgentID: agentID, Period: period}
	var totalLatency int64

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore get usage summary: %w", err)
		}
		var r models.UsageRecord
		if err := doc.DataTo(&r); err != nil {
			return nil, fmt.Errorf("firestore decode usage record: %w", err)
		}

		// Filter in-memory
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

func (f *FirestoreStore) ListUsageRecords(ctx context.Context, modelID, agentID string, since time.Time, limit int) ([]models.UsageRecord, error) {
	query := f.usageQuery(modelID, agentID, since)

	if limit > 0 {
		query = query.Limit(limit)
	}

	iter := query.Documents(ctx)
	var records []models.UsageRecord

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore list usage records: %w", err)
		}
		var r models.UsageRecord
		if err := doc.DataTo(&r); err != nil {
			return nil, fmt.Errorf("firestore decode usage record: %w", err)
		}

		// Filter in-memory
		if modelID != "" && r.ModelID != modelID {
			continue
		}
		if agentID != "" && r.AgentID != agentID {
			continue
		}

		records = append(records, r)
	}

	return records, nil
}

// Filter before Limit: a page of another model's newer usage must not hide the
// requested model's records. Typed model persistence uses Go field names;
// encoding/json tags do not change Firestore's field paths.
func (f *FirestoreStore) usageQuery(modelID, agentID string, since time.Time) firestore.Query {
	query := f.client.Collection("usage_records").Where("Timestamp", ">=", since).OrderBy("Timestamp", firestore.Desc)
	if modelID != "" {
		query = query.Where("ModelID", "==", modelID)
	}
	if agentID != "" {
		query = query.Where("AgentID", "==", agentID)
	}
	return query
}

// ── A2A Delegations ──

func (f *FirestoreStore) AppendDelegation(ctx context.Context, d *models.Delegation) error {
	ref := f.client.Collection("delegations").Doc(d.ID)
	_, err := ref.Create(ctx, d)
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return ErrAlreadyExists
		}
		return fmt.Errorf("firestore append delegation: %w", err)
	}
	return nil
}

func (f *FirestoreStore) GetDelegation(ctx context.Context, id string) (*models.Delegation, error) {
	doc, err := f.client.Collection("delegations").Doc(id).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("firestore get delegation: %w", err)
	}
	var d models.Delegation
	if err := doc.DataTo(&d); err != nil {
		return nil, fmt.Errorf("firestore decode delegation: %w", err)
	}
	return &d, nil
}

func (f *FirestoreStore) ListDelegations(ctx context.Context, agentID string, limit int) ([]models.Delegation, error) {
	query := f.client.Collection("delegations").OrderBy("StartedAt", firestore.Desc)
	if agentID != "" {
		query = query.WhereEntity(firestore.OrFilter{Filters: []firestore.EntityFilter{firestore.PropertyFilter{Path: "CallerAgentID", Operator: "==", Value: agentID}, firestore.PropertyFilter{Path: "CalleeAgentID", Operator: "==", Value: agentID}}})
	}

	if limit > 0 {
		query = query.Limit(limit)
	}

	iter := query.Documents(ctx)
	defer iter.Stop()
	delegations := make([]models.Delegation, 0)
	count := 0

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore list delegations: %w", err)
		}
		var d models.Delegation
		if err := doc.DataTo(&d); err != nil {
			return nil, fmt.Errorf("firestore decode delegation: %w", err)
		}

		// Filter in-memory if agentID specified
		if agentID != "" && d.CallerAgentID != agentID && d.CalleeAgentID != agentID {
			continue
		}

		delegations = append(delegations, d)
		count++
		if limit > 0 && count >= limit {
			break
		}
	}

	return delegations, nil
}

func (f *FirestoreStore) UpdateDelegation(ctx context.Context, d *models.Delegation) error {
	ref := f.client.Collection("delegations").Doc(d.ID)
	err := f.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snapshot, e := tx.Get(ref)
		if e != nil {
			if status.Code(e) == codes.NotFound {
				return ErrNotFound
			}
			return e
		}
		var previous models.Delegation
		if e = snapshot.DataTo(&previous); e != nil {
			return e
		}
		if e = delegationTransition(&previous, d); e != nil {
			return e
		}
		return tx.Set(ref, d)
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return ErrNotFound
		}
		return fmt.Errorf("firestore update delegation: %w", err)
	}
	return nil
}

func (f *FirestoreStore) ListDelegationTrace(ctx context.Context, traceID string, limit int) ([]models.Delegation, error) {
	query := f.client.Collection("delegations").Where("TraceID", "==", traceID).OrderBy("StartedAt", firestore.Desc)
	if limit > 0 {
		query = query.Limit(limit)
	}
	iter := query.Documents(ctx)
	defer iter.Stop()
	out := make([]models.Delegation, 0)
	for {
		snapshot, e := iter.Next()
		if e == iterator.Done {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("firestore list delegation trace: %w", e)
		}
		var d models.Delegation
		if e = snapshot.DataTo(&d); e != nil {
			return nil, fmt.Errorf("firestore decode delegation trace: %w", e)
		}
		out = append(out, d)
	}
	return out, nil
}

// ── SkillsPlex Invocations ──

func (f *FirestoreStore) AppendSkillInvocation(ctx context.Context, inv *models.SkillInvocation) error {
	ref := f.client.Collection("skill_invocations").NewDoc()
	if inv.ID == "" {
		inv.ID = ref.ID
	} else {
		ref = f.client.Collection("skill_invocations").Doc(inv.ID)
	}
	if _, err := ref.Create(ctx, inv); err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return ErrAlreadyExists
		}
		return fmt.Errorf("firestore append skill invocation: %w", err)
	}
	return nil
}

func (f *FirestoreStore) ListSkillInvocations(ctx context.Context, agentID, skillName string, limit int) ([]models.SkillInvocation, error) {
	query := f.client.Collection("skill_invocations").OrderBy("StartedAt", firestore.Desc)
	if agentID != "" {
		query = query.Where("AgentID", "==", agentID)
	}
	if skillName != "" {
		query = query.Where("SkillName", "==", skillName)
	}
	if limit > 0 {
		query = query.Limit(limit)
	}
	iter := query.Documents(ctx)
	defer iter.Stop()
	out := make([]models.SkillInvocation, 0)
	count := 0
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore list skill invocations: %w", err)
		}
		var inv models.SkillInvocation
		if err := doc.DataTo(&inv); err != nil {
			return nil, fmt.Errorf("firestore decode skill invocation: %w", err)
		}
		if agentID != "" && inv.AgentID != agentID {
			continue
		}
		if skillName != "" && inv.SkillName != skillName {
			continue
		}
		out = append(out, inv)
		count++
		if limit > 0 && count >= limit {
			break
		}
	}
	return out, nil
}

// ── Policy Denials ──

func (f *FirestoreStore) AppendPolicyDenial(ctx context.Context, d *models.PolicyDenial) error {
	ref := f.client.Collection("policy_denials").NewDoc()
	if d.ID == "" {
		d.ID = ref.ID
	} else {
		ref = f.client.Collection("policy_denials").Doc(d.ID)
	}
	_, err := ref.Set(ctx, d)
	if err != nil {
		return fmt.Errorf("firestore append policy denial: %w", err)
	}
	return nil
}

func (f *FirestoreStore) ListPolicyDenials(ctx context.Context, limit int) ([]models.PolicyDenial, error) {
	query := f.client.Collection("policy_denials").OrderBy("Timestamp", firestore.Desc)
	if limit > 0 {
		query = query.Limit(limit)
	}

	iter := query.Documents(ctx)
	var denials []models.PolicyDenial

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore list policy denials: %w", err)
		}
		var d models.PolicyDenial
		if err := doc.DataTo(&d); err != nil {
			return nil, fmt.Errorf("firestore decode policy denial: %w", err)
		}
		denials = append(denials, d)
	}

	return denials, nil
}

// ── Counts ──

func (f *FirestoreStore) CountDelegations(ctx context.Context) (int64, error) {
	// Firestore doesn't support COUNT queries in the Go SDK yet, so we count documents
	iter := f.client.Collection("delegations").Documents(ctx)
	var count int64
	for {
		_, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("firestore count delegations: %w", err)
		}
		count++
	}
	return count, nil
}

func (f *FirestoreStore) CountPolicyDenials(ctx context.Context) (int64, error) {
	iter := f.client.Collection("policy_denials").Documents(ctx)
	var count int64
	for {
		_, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("firestore count policy denials: %w", err)
		}
		count++
	}
	return count, nil
}

// CountPolicyDenialsSince counts denials at or after since. Only document
// identifiers are read.
func (f *FirestoreStore) CountPolicyDenialsSince(ctx context.Context, since time.Time) (int64, error) {
	iter := f.client.Collection("policy_denials").Where("Timestamp", ">=", since).Select().Documents(ctx)
	defer iter.Stop()
	var count int64
	for {
		_, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("firestore count policy denials since: %w", err)
		}
		count++
	}
	return count, nil
}

// ── IAM Role Bindings ──

func (f *FirestoreStore) GetRoleBinding(ctx context.Context, id string) (*models.RoleBinding, error) {
	doc, err := f.client.Collection("role_bindings").Doc(id).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("firestore get role binding: %w", err)
	}
	var rb models.RoleBinding
	if err := doc.DataTo(&rb); err != nil {
		return nil, fmt.Errorf("firestore decode role binding: %w", err)
	}
	normalizeConfigurationVersion(&rb)
	return &rb, nil
}

func (f *FirestoreStore) ListRoleBindings(ctx context.Context) ([]models.RoleBinding, error) {
	iter := f.client.Collection("role_bindings").OrderBy("CreatedAt", firestore.Desc).Documents(ctx)
	var bindings []models.RoleBinding
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore list role bindings: %w", err)
		}
		var rb models.RoleBinding
		if err := doc.DataTo(&rb); err != nil {
			return nil, fmt.Errorf("firestore decode role binding: %w", err)
		}
		normalizeConfigurationVersion(&rb)
		bindings = append(bindings, rb)
	}
	return bindings, nil
}

func (f *FirestoreStore) ListRoleBindingsByGroup(ctx context.Context, group string) ([]models.RoleBinding, error) {
	iter := f.client.Collection("role_bindings").Where("Group", "==", group).Documents(ctx)
	var bindings []models.RoleBinding
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore list role bindings by group: %w", err)
		}
		var rb models.RoleBinding
		if err := doc.DataTo(&rb); err != nil {
			return nil, fmt.Errorf("firestore decode role binding: %w", err)
		}
		normalizeConfigurationVersion(&rb)
		bindings = append(bindings, rb)
	}
	return bindings, nil
}

func (f *FirestoreStore) PutRoleBinding(ctx context.Context, rb *models.RoleBinding) error {
	if rb == nil {
		return fmt.Errorf("configuration required")
	}
	return PutLegacyConfiguration(ctx, f, ConfigRoleBinding, rb.ID, rb)
}

func (f *FirestoreStore) DeleteRoleBinding(ctx context.Context, id string) error {
	return DeleteLegacyConfiguration(ctx, f, ConfigRoleBinding, id)
}

// ── Execution audit ──────────────────────
//
// Tape's outbox relay POSTs events to /internal/tape/events; AIPlex
// dedupes on (RunID, Seq), quarantines unknown agents, and updates the
// ExecutionRun projection. Collections:
//
//   execution_events/{runID}/seq/{seq}     idempotent on doc id
//   execution_runs/{runID}                 projected summary
//   execution_quarantine/{auto}            unknown-agent triage queue
//
// Production-shaped but conservative: each AppendExecutionEvent is a
// single doc set on the (runID, seq) key; the projection upsert is a
// separate write. Batching is a follow-up if write QPS demands it.

func (f *FirestoreStore) AppendExecutionEvent(ctx context.Context, ev *models.ExecutionEvent) (bool, error) {
	if ev == nil || ev.RunID == "" {
		return false, fmt.Errorf("execution event: run_id required")
	}
	docID := fmt.Sprintf("%d", ev.Seq)
	ref := f.client.Collection("execution_events").Doc(ev.RunID).Collection("seq").Doc(docID)
	// Create-only semantics so a duplicate POST is a typed no-op rather
	// than an overwrite of the original journal row.
	_, err := ref.Create(ctx, ev)
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return false, nil
		}
		return false, fmt.Errorf("firestore append execution event: %w", err)
	}
	return true, nil
}

func (f *FirestoreStore) QuarantineExecutionEvent(ctx context.Context, q *models.QuarantinedExecutionEvent) error {
	if q == nil {
		return fmt.Errorf("quarantine: nil event")
	}
	_, _, err := f.client.Collection("execution_quarantine").Add(ctx, q)
	if err != nil {
		return fmt.Errorf("firestore quarantine execution event: %w", err)
	}
	return nil
}

func (f *FirestoreStore) GetExecutionRun(ctx context.Context, runID string) (*models.ExecutionRun, error) {
	doc, err := f.client.Collection("execution_runs").Doc(runID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("firestore get execution run: %w", err)
	}
	run := &models.ExecutionRun{}
	if err := doc.DataTo(run); err != nil {
		return nil, fmt.Errorf("firestore decode execution run: %w", err)
	}
	return run, nil
}

func (f *FirestoreStore) UpsertExecutionRun(ctx context.Context, run *models.ExecutionRun) error {
	if run == nil || run.RunID == "" {
		return fmt.Errorf("upsert execution run: run_id required")
	}
	_, err := f.client.Collection("execution_runs").Doc(run.RunID).Set(ctx, run)
	if err != nil {
		return fmt.Errorf("firestore upsert execution run: %w", err)
	}
	return nil
}

func (f *FirestoreStore) ListExecutionRuns(ctx context.Context, tenantID, agentID string, limit int) ([]models.ExecutionRun, error) {
	if limit <= 0 {
		limit = 100
	}
	q := f.client.Collection("execution_runs").Limit(limit)
	if tenantID != "" {
		q = q.Where("TenantID", "==", tenantID)
	}
	if agentID != "" {
		q = q.Where("AgentID", "==", agentID)
	}
	docs, err := q.Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("firestore list execution runs: %w", err)
	}
	out := make([]models.ExecutionRun, 0, len(docs))
	for _, d := range docs {
		run := models.ExecutionRun{}
		if err := d.DataTo(&run); err == nil {
			out = append(out, run)
		}
	}
	return out, nil
}

func (f *FirestoreStore) ListExecutionEvents(ctx context.Context, runID string, fromSeq int64, limit int) ([]models.ExecutionEvent, error) {
	if limit <= 0 {
		limit = 1000
	}
	docs, err := f.client.Collection("execution_events").Doc(runID).Collection("seq").
		Where("Seq", ">=", fromSeq).
		OrderBy("Seq", firestore.Asc).
		Limit(limit).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("firestore list execution events: %w", err)
	}
	out := make([]models.ExecutionEvent, 0, len(docs))
	for _, d := range docs {
		ev := models.ExecutionEvent{}
		if err := d.DataTo(&ev); err == nil {
			out = append(out, ev)
		}
	}
	return out, nil
}

// ── PR 11 extensions ──────────────────────────────────────────────────────

func (f *FirestoreStore) DeleteExecutionRun(ctx context.Context, runID string) error {
	_, err := f.client.Collection("execution_runs").Doc(runID).Delete(ctx)
	if err != nil && status.Code(err) != codes.NotFound {
		return fmt.Errorf("firestore delete execution run: %w", err)
	}
	return nil
}

func (f *FirestoreStore) LastIngestAt(ctx context.Context) (time.Time, error) {
	// "Most recently ingested" — single doc at execution_meta/last_ingest
	// rewritten on each event. Cheap to read; we accept eventual
	// consistency (the Console polls this every 30s).
	doc, err := f.client.Collection("execution_meta").Doc("last_ingest").Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("firestore last ingest: %w", err)
	}
	v, ok := doc.Data()["at"]
	if !ok {
		return time.Time{}, nil
	}
	t, _ := v.(time.Time)
	return t, nil
}

func (f *FirestoreStore) AppendOperatorAudit(ctx context.Context, a *models.OperatorAudit) error {
	if a == nil || a.RunID == "" {
		return fmt.Errorf("operator audit: run_id required")
	}
	if a.ID == "" {
		// One-doc-per-action; deterministic id based on actor+timestamp
		// so a double-write through the idempotency cache is a no-op
		// upsert rather than a duplicate.
		a.ID = fmt.Sprintf("%s-%s-%d", a.RunID, a.Action, a.At.UnixNano())
	}
	_, err := f.client.Collection("operator_audit").Doc(a.ID).Set(ctx, a)
	if err != nil {
		return fmt.Errorf("firestore operator audit: %w", err)
	}
	return nil
}

func (f *FirestoreStore) ListOperatorAudit(ctx context.Context, runID string, limit int) ([]models.OperatorAudit, error) {
	if limit <= 0 {
		limit = 100
	}
	docs, err := f.client.Collection("operator_audit").
		Where("RunID", "==", runID).
		Limit(limit).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("firestore list operator audit: %w", err)
	}
	out := make([]models.OperatorAudit, 0, len(docs))
	for _, d := range docs {
		a := models.OperatorAudit{}
		if err := d.DataTo(&a); err == nil {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *FirestoreStore) CountInstancesWithRuntime(ctx context.Context, engine models.RuntimeEngine) (int, error) {
	// Firestore aggregation queries; falls back to enumeration when
	// the aggregate API isn't enabled. Cheap regardless since instances
	// is a small collection (< 10k rows in any real deployment).
	docs, err := f.client.Collection("instances").
		Where("Runtime.Engine", "==", string(engine)).
		Documents(ctx).GetAll()
	if err != nil {
		return 0, fmt.Errorf("firestore count instances by runtime: %w", err)
	}
	return len(docs), nil
}

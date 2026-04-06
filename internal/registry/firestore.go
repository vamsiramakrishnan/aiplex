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
//
// Collection layout:
//   instances/{id}       — deployed instances
//   templates/{id}       — cached catalog templates
//   agents/{clientId}    — registered OAuth clients
//   deploy_history/{id}  — append-only audit trail
//   user_scopes/{userId} — Dimension B (user ceiling)
//   route_configs/{modelID} — LLM routing configs
//   provider_configs/{provider} — LLM provider settings
//   usage_records/{id}   — token usage tracking (append-only)
//   delegations/{id}     — A2A delegations
//   policy_denials/{id}  — authz denial events (append-only)
//   role_bindings/{id}   — IAM role bindings
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
		iter = f.client.Collection("instances").Where("plane", "==", string(plane)).Documents(ctx)
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
		query = f.client.Collection("templates").OrderBy("name", firestore.Asc)
	} else {
		query = f.client.Collection("templates").Where("plane", "==", string(plane)).OrderBy("name", firestore.Asc)
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
	return &a, nil
}

func (f *FirestoreStore) ListAgents(ctx context.Context) ([]models.Agent, error) {
	iter := f.client.Collection("agents").OrderBy("registered_at", firestore.Desc).Documents(ctx)
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
		agents = append(agents, a)
	}
	return agents, nil
}

func (f *FirestoreStore) PutAgent(ctx context.Context, a *models.Agent) error {
	_, err := f.client.Collection("agents").Doc(a.ClientID).Set(ctx, a)
	if err != nil {
		return fmt.Errorf("firestore put agent: %w", err)
	}
	return nil
}

func (f *FirestoreStore) DeleteAgent(ctx context.Context, clientID string) error {
	_, err := f.client.Collection("agents").Doc(clientID).Delete(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return ErrNotFound
		}
		return fmt.Errorf("firestore delete agent: %w", err)
	}
	return nil
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
		Where("instance_id", "==", instanceID).
		OrderBy("timestamp", firestore.Desc)

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
	_, err := f.client.Collection("user_scopes").Doc(userID).Set(ctx, userScopesDoc{Scopes: scopes})
	if err != nil {
		return fmt.Errorf("firestore set user scopes: %w", err)
	}
	return nil
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
		configs = append(configs, rc)
	}
	return configs, nil
}

func (f *FirestoreStore) PutRouteConfig(ctx context.Context, rc *models.LLMRouteConfig) error {
	rc.UpdatedAt = time.Now()
	if rc.CreatedAt.IsZero() {
		rc.CreatedAt = time.Now()
	}
	_, err := f.client.Collection("route_configs").Doc(rc.ModelID).Set(ctx, rc)
	if err != nil {
		return fmt.Errorf("firestore put route config: %w", err)
	}
	return nil
}

func (f *FirestoreStore) DeleteRouteConfig(ctx context.Context, modelID string) error {
	_, err := f.client.Collection("route_configs").Doc(modelID).Delete(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return ErrNotFound
		}
		return fmt.Errorf("firestore delete route config: %w", err)
	}
	return nil
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
		configs = append(configs, pc)
	}
	return configs, nil
}

func (f *FirestoreStore) PutProviderConfig(ctx context.Context, pc *models.ProviderConfig) error {
	_, err := f.client.Collection("provider_configs").Doc(pc.Provider).Set(ctx, pc)
	if err != nil {
		return fmt.Errorf("firestore put provider config: %w", err)
	}
	return nil
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

	// Query filtered by time, then filter by model/agent in-memory
	query := f.client.Collection("usage_records").Where("timestamp", ">=", cutoff)
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
	query := f.client.Collection("usage_records").
		Where("timestamp", ">=", since).
		OrderBy("timestamp", firestore.Desc)

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

// ── A2A Delegations ──

func (f *FirestoreStore) AppendDelegation(ctx context.Context, d *models.Delegation) error {
	ref := f.client.Collection("delegations").Doc(d.ID)
	_, err := ref.Set(ctx, d)
	if err != nil {
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
	var query firestore.Query
	if agentID != "" {
		// Note: This requires a composite index for multiple OR conditions
		// For simplicity, we'll fetch all and filter in-memory
		query = f.client.Collection("delegations").OrderBy("started_at", firestore.Desc)
	} else {
		query = f.client.Collection("delegations").OrderBy("started_at", firestore.Desc)
	}

	if limit > 0 {
		query = query.Limit(limit * 2) // fetch extra since we'll filter
	}

	iter := query.Documents(ctx)
	var delegations []models.Delegation
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
	_, err := f.client.Collection("delegations").Doc(d.ID).Set(ctx, d)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return ErrNotFound
		}
		return fmt.Errorf("firestore update delegation: %w", err)
	}
	return nil
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
	query := f.client.Collection("policy_denials").OrderBy("timestamp", firestore.Desc)
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
	return &rb, nil
}

func (f *FirestoreStore) ListRoleBindings(ctx context.Context) ([]models.RoleBinding, error) {
	iter := f.client.Collection("role_bindings").OrderBy("created_at", firestore.Desc).Documents(ctx)
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
		bindings = append(bindings, rb)
	}
	return bindings, nil
}

func (f *FirestoreStore) ListRoleBindingsByGroup(ctx context.Context, group string) ([]models.RoleBinding, error) {
	iter := f.client.Collection("role_bindings").Where("group", "==", group).Documents(ctx)
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
		bindings = append(bindings, rb)
	}
	return bindings, nil
}

func (f *FirestoreStore) PutRoleBinding(ctx context.Context, rb *models.RoleBinding) error {
	_, err := f.client.Collection("role_bindings").Doc(rb.ID).Set(ctx, rb)
	if err != nil {
		return fmt.Errorf("firestore put role binding: %w", err)
	}
	return nil
}

func (f *FirestoreStore) DeleteRoleBinding(ctx context.Context, id string) error {
	_, err := f.client.Collection("role_bindings").Doc(id).Delete(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return ErrNotFound
		}
		return fmt.Errorf("firestore delete role binding: %w", err)
	}
	return nil
}

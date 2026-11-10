package aiplex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/routing"
)

// Access points: one stable URL (/ap/{id}/…) that enforces identity, access,
// QoS and quota in front of models, tools and skills.

// Caller kinds an access point can admit.
const (
	CallerKindAgent  = "agent"
	CallerKindUser   = "user"
	CallerKindClient = "client"
)

// AccessPoint is the saved specification of an access point.
type AccessPoint struct {
	ID          string              `json:"id"`
	DisplayName string              `json:"display_name,omitempty"`
	CallerKinds []string            `json:"caller_kinds"`
	Subjects    AccessPointSubjects `json:"subjects"`
	Models      []AccessPointModel  `json:"models,omitempty"`
	Tools       []AccessPointTool   `json:"tools,omitempty"`
	Skills      []AccessPointSkill  `json:"skills,omitempty"`
	QoS         *AccessPointQoS     `json:"qos,omitempty"`
}

// AccessPointSubjects narrows who may call. An empty block admits every
// caller of an allowed kind.
type AccessPointSubjects struct {
	Groups              []quotaplex.GroupRef `json:"groups,omitempty"`
	Principals          []string             `json:"principals,omitempty"`
	Agents              []string             `json:"agents,omitempty"`
	ClientRegistrations []string             `json:"client_registrations,omitempty"`
}

// AccessPointModel maps a client-facing model name to a saved model surface
// (SurfaceRef and NativeModel), or to ordered or weighted Backends with a
// Routing block. Cache turns on the exact-match response cache (off unless
// set). See docs/reliability-and-routing.md.
type AccessPointModel struct {
	Name        string            `json:"name"`
	SurfaceRef  string            `json:"surface_ref,omitempty"`
	NativeModel string            `json:"native_model,omitempty"`
	Backends    []RoutingBackend  `json:"backends,omitempty"`
	Routing     *RoutingPolicy    `json:"routing,omitempty"`
	Cache       *ResponseCacheSet `json:"cache,omitempty"`
}

// AllBackends lists the model's backends: Backends, or the single
// SurfaceRef/NativeModel pair.
func (m AccessPointModel) AllBackends() []RoutingBackend {
	if len(m.Backends) > 0 {
		return m.Backends
	}
	return []RoutingBackend{{SurfaceRef: m.SurfaceRef, NativeModel: m.NativeModel}}
}

// Routing configuration types (pkg/routing).
type (
	RoutingBackend    = routing.Backend
	RoutingPolicy     = routing.Routing
	RoutingRetry      = routing.Retry
	RoutingFallback   = routing.Fallback
	RoutingBreaker    = routing.Breaker
	RoutingLowestCost = routing.LowestCost
	ResponseCacheSet  = routing.Cache
	BreakerState      = routing.BreakerState
)

// AccessPointRouting is the operator view of an access point's routed
// models in the API process that answered: breaker states, recent outcomes
// and cache occupancy. Other replicas keep their own state.
type AccessPointRouting struct {
	AccessPointID string              `json:"access_point_id"`
	ProcessLocal  bool                `json:"process_local"`
	Models        []RoutedModel       `json:"models"`
	CacheStore    *routing.CacheStats `json:"cache_store,omitempty"`
}

// RoutedModel is one routed model of AccessPointRouting.
type RoutedModel struct {
	Name     string          `json:"name"`
	Strategy string          `json:"strategy"`
	Backends []RoutedBackend `json:"backends"`
	Cache    *routing.Cache  `json:"cache,omitempty"`
}

// RoutedBackend is one backend and its breaker.
type RoutedBackend struct {
	SurfaceRef  string       `json:"surface_ref"`
	NativeModel string       `json:"native_model"`
	Weight      int          `json:"weight"`
	Priority    int          `json:"priority"`
	Breaker     BreakerState `json:"breaker"`
}

// GetAccessPointRouting returns the live routing state (operators).
func (c *Client) GetAccessPointRouting(ctx context.Context, id string) (*AccessPointRouting, error) {
	path, err := accessPointPath(id)
	if err != nil {
		return nil, err
	}
	var out AccessPointRouting
	if err = c.do(ctx, http.MethodGet, path+"/routing", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PurgeAccessPointCache removes the cached answers of an access point (or of
// one model when model is not empty) in the API process that answered, and
// returns how many were removed (admins).
func (c *Client) PurgeAccessPointCache(ctx context.Context, id, model string) (int, error) {
	path, err := accessPointPath(id)
	if err != nil {
		return 0, err
	}
	path += "/cache/purge"
	if model != "" {
		path += "?model=" + url.QueryEscape(model)
	}
	var out struct {
		Purged int `json:"purged"`
	}
	if err = c.do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return 0, err
	}
	return out.Purged, nil
}

// AccessPointTool exposes one saved tool connection; Expose optionally narrows
// it to tool names or prefixes ending in '*'.
type AccessPointTool struct {
	ConnectionRef string   `json:"connection_ref"`
	Expose        []string `json:"expose,omitempty"`
}

// AccessPointSkill exposes one deployed skill server.
type AccessPointSkill struct {
	InstanceRef string `json:"instance_ref"`
}

// AccessPointQoS is the quality-of-service class and limits of an access point.
type AccessPointQoS struct {
	Class  string `json:"class"`
	Weight uint32 `json:"weight,omitempty"`
	// Scope is "principal" (default: one allowance per caller principal) or
	// "access_point" (one pool shared by all callers).
	Scope      string            `json:"scope,omitempty"`
	Limits     *AccessPointLimit `json:"limits,omitempty"`
	BudgetRefs []string          `json:"budget_refs,omitempty"`
}

// AccessPointLimit bounds request rate, token rate and concurrency.
type AccessPointLimit struct {
	RequestsPerMinute int64 `json:"requests_per_minute,omitempty"`
	TokensPerMinute   int64 `json:"tokens_per_minute,omitempty"`
	Concurrency       int64 `json:"concurrency,omitempty"`
}

// AccessPointRecord is a stored access point with its revision, the
// optimistic-concurrency token for replace and delete.
type AccessPointRecord struct {
	TenantID string      `json:"tenant_id"`
	Kind     string      `json:"kind"`
	ID       string      `json:"id"`
	Revision uint64      `json:"revision"`
	Spec     AccessPoint `json:"spec"`
}

func accessPointPath(id string) (string, error) {
	if err := validateResourceID(id); err != nil {
		return "", err
	}
	return "/api/v1/access-points/" + url.PathEscape(id), nil
}

// ListAccessPoints returns every access point of the tenant.
func (c *Client) ListAccessPoints(ctx context.Context) ([]AccessPointRecord, error) {
	out := []AccessPointRecord{}
	if err := c.do(ctx, http.MethodGet, "/api/v1/access-points/", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetAccessPoint returns one access point including its current Revision.
func (c *Client) GetAccessPoint(ctx context.Context, id string) (*AccessPointRecord, error) {
	path, err := accessPointPath(id)
	if err != nil {
		return nil, err
	}
	var out AccessPointRecord
	if err = c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutAccessPoint creates (expectedRevision 0) or replaces (expectedRevision
// equal to the stored revision) the access point named by spec.ID.
func (c *Client) PutAccessPoint(ctx context.Context, spec AccessPoint, expectedRevision uint64) (*AccessPointRecord, error) {
	path, err := accessPointPath(spec.ID)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"If-Match": strconv.FormatUint(expectedRevision, 10)}
	var out AccessPointRecord
	if err = c.doWithHeaders(ctx, http.MethodPut, path, spec, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAccessPoint removes an access point; the current revision is required.
func (c *Client) DeleteAccessPoint(ctx context.Context, id string, expectedRevision uint64) error {
	path, err := accessPointPath(id)
	if err != nil {
		return err
	}
	if expectedRevision == 0 {
		return fmt.Errorf("aiplex: positive expected revision required")
	}
	return c.doWithHeaders(ctx, http.MethodDelete, path, nil, nil, map[string]string{"If-Match": strconv.FormatUint(expectedRevision, 10)})
}

// AccessPointQuotaFragment is the reviewable QuotaPlex rendering of a QoS block.
type AccessPointQuotaFragment struct {
	Meters      map[string]quotaplex.Meter  `json:"meters,omitempty"`
	Rates       []quotaplex.RateRule        `json:"rates,omitempty"`
	Concurrency []quotaplex.ConcurrencyRule `json:"concurrency,omitempty"`
	BudgetRefs  []quotaplex.BudgetRule      `json:"budget_refs,omitempty"`
}

// AccessPointSchedulerRoute selects the scheduler flow for access-point traffic.
type AccessPointSchedulerRoute struct {
	ResourceID    string `json:"resource_id"`
	OperationID   string `json:"operation_id"`
	AccessPointID string `json:"access_point_id"`
	FlowID        string `json:"flow_id"`
}

// AccessPointSchedulerFragment is the weighted-fair-queue contribution.
type AccessPointSchedulerFragment struct {
	Class  string                      `json:"class"`
	FlowID string                      `json:"flow_id"`
	Weight uint32                      `json:"weight"`
	Routes []AccessPointSchedulerRoute `json:"routes"`
}

// AccessPointQuotaValidation reports whether the rendered policy compiles.
type AccessPointQuotaValidation struct {
	Valid     bool                 `json:"valid"`
	Validator string               `json:"validator"`
	Reference *quotaplex.PolicyRef `json:"reference,omitempty"`
	Error     string               `json:"error,omitempty"`
}

// AccessPointQuotaPlan is the rendering of a QoS block as quota controls. A
// plan is never applied by this call.
type AccessPointQuotaPlan struct {
	AccessPointID string                        `json:"access_point_id"`
	TenantID      string                        `json:"tenant_id"`
	Applied       bool                          `json:"applied"`
	Fragment      AccessPointQuotaFragment      `json:"fragment"`
	Scheduler     *AccessPointSchedulerFragment `json:"scheduler,omitempty"`
	Policy        quotaplex.Policy              `json:"policy"`
	Validation    AccessPointQuotaValidation    `json:"validation"`
	Warnings      []string                      `json:"warnings"`
}

// PlanAccessPointQuota renders the access point's QoS block as quota controls
// for review (POST /access-points/{id}/quota-plan).
func (c *Client) PlanAccessPointQuota(ctx context.Context, id string) (*AccessPointQuotaPlan, error) {
	path, err := accessPointPath(id)
	if err != nil {
		return nil, err
	}
	var out AccessPointQuotaPlan
	if err = c.do(ctx, http.MethodPost, path+"/quota-plan", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

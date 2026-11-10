package aiplex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// Model surfaces for non-Google providers.
//
// A surface is stored as a revisioned "model-surface" resource. The types here
// give its spec a typed shape (provider, websocket and capacity_policy blocks)
// while every block keeps fields this SDK version does not know in Extra, so a
// read-modify-write never drops a newer server field. Credentials are only ever
// references: ModelSurfaceAuth.SecretRef names a secret, it is never a value.

type (
	// ModelOperation is one catalog operation. A surface must list operations
	// exactly as the provider descriptor returns them.
	ModelOperation = modelapi.Operation
	// ModelProviderDescriptor is the server's catalog for one provider:
	// protocols, credential schemes, default stream usage and operations.
	ModelProviderDescriptor = modelapi.ProviderDescriptor
	// ModelProviderLimits is the last provider rate-limit observation.
	ModelProviderLimits = providerlimits.Limits
	// ModelLimitWindow is one limit window; nil members are unknown.
	ModelLimitWindow = providerlimits.Window
	// ModelCooldownState is the capacity gate's cooldown phase for a surface.
	ModelCooldownState = providerlimits.CooldownState
)

// Cooldown phases reported in ModelCooldownState.Phase.
const (
	CooldownNone    = providerlimits.PhaseNone
	CooldownCooling = providerlimits.PhaseCooling
	CooldownHalf    = providerlimits.PhaseHalf
	CooldownProbing = providerlimits.PhaseProbing
)

// Provider ids accepted by the surface provider block.
const (
	ProviderOpenAI           = modelapi.ProviderOpenAI
	ProviderAnthropic        = modelapi.ProviderAnthropic
	ProviderAzureOpenAI      = modelapi.ProviderAzureOpenAI
	ProviderBedrock          = modelapi.ProviderBedrock
	ProviderOpenAICompatible = modelapi.ProviderOpenAICompatible
	ProviderGeminiDeveloper  = modelapi.ProviderGeminiDeveloper
)

// WebSocket metering modes.
const (
	WebSocketMeteringSession = "session"
	WebSocketMeteringToken   = "token"
)

// ModelProvider is the pinned provider block of a surface.
type ModelProvider struct {
	ID                     string   `json:"id"`
	BasePath               string   `json:"base_path,omitempty"`
	APIVersion             string   `json:"api_version,omitempty"`
	Region                 string   `json:"region,omitempty"`
	Organization           string   `json:"organization,omitempty"`
	Project                string   `json:"project,omitempty"`
	AnthropicVersion       string   `json:"anthropic_version,omitempty"`
	AnthropicBetas         []string `json:"anthropic_betas,omitempty"`
	StreamUsage            string   `json:"stream_usage,omitempty"`
	LimitHeaderPassthrough bool     `json:"limit_header_passthrough,omitempty"`
	UsageSemantics         string   `json:"usage_semantics,omitempty"`
	// Extra holds fields this SDK version does not know, as raw JSON.
	Extra map[string]json.RawMessage `json:"-"`
}

// ModelWebSocket configures session operations. Nil members of the surface
// mean the server defaults; zero numbers are omitted, not sent as zero.
type ModelWebSocket struct {
	Metering          string                     `json:"metering,omitempty"`
	MaxSessionSeconds int64                      `json:"max_session_seconds,omitempty"`
	TokenAllowance    int64                      `json:"token_allowance,omitempty"`
	Extra             map[string]json.RawMessage `json:"-"`
}

// ModelCapacityPolicy opts a provider surface into advisory enforcement of
// provider limit signals. Per surface and per server replica.
type ModelCapacityPolicy struct {
	CooldownEnabled     bool                       `json:"cooldown_enabled,omitempty"`
	MaxCooldownSeconds  int                        `json:"max_cooldown_seconds,omitempty"`
	HeadroomFraction    float64                    `json:"headroom_fraction,omitempty"`
	MinWeight           uint32                     `json:"min_weight,omitempty"`
	DefaultWeight       uint32                     `json:"default_weight,omitempty"`
	SignalMaxAgeSeconds int                        `json:"signal_max_age_seconds,omitempty"`
	Extra               map[string]json.RawMessage `json:"-"`
}

// ModelSurfaceAuth is a credential REFERENCE. SecretRef is the name of a
// tenant secret, never the secret itself.
type ModelSurfaceAuth struct {
	Type      string                     `json:"type"`
	In        string                     `json:"in,omitempty"`
	Name      string                     `json:"name,omitempty"`
	Prefix    string                     `json:"prefix,omitempty"`
	SecretRef string                     `json:"secret_ref,omitempty"`
	Extra     map[string]json.RawMessage `json:"-"`
}

// ModelSurface is the surface block of the spec.
type ModelSurface struct {
	ID         string           `json:"id"`
	Protocol   string           `json:"protocol"`
	Origin     string           `json:"origin"`
	Models     []string         `json:"models,omitempty"`
	Operations []ModelOperation `json:"operations,omitempty"`
	// Provider selects a non-Google provider; nil is a Google Cloud surface.
	Provider  *ModelProvider  `json:"provider,omitempty"`
	WebSocket *ModelWebSocket `json:"websocket,omitempty"`
	// Google Cloud surface fields; empty for provider surfaces.
	APIVersion   string                     `json:"api_version,omitempty"`
	Project      string                     `json:"project,omitempty"`
	Location     string                     `json:"location,omitempty"`
	Publisher    string                     `json:"publisher,omitempty"`
	DataPrefixes []string                   `json:"data_prefixes,omitempty"`
	Agents       []string                   `json:"agents,omitempty"`
	Endpoint     string                     `json:"endpoint,omitempty"`
	Extra        map[string]json.RawMessage `json:"-"`
}

// ModelSurfaceSpec is the spec of a model-surface resource. A provider surface
// has exactly one of Auth and IdentityRef.
type ModelSurfaceSpec struct {
	Surface           ModelSurface               `json:"surface"`
	IdentityRef       string                     `json:"identity_ref,omitempty"`
	NetworkProfileRef string                     `json:"network_profile_ref,omitempty"`
	BillingProject    string                     `json:"billing_project,omitempty"`
	Auth              *ModelSurfaceAuth          `json:"auth,omitempty"`
	CapacityPolicy    *ModelCapacityPolicy       `json:"capacity_policy,omitempty"`
	Extra             map[string]json.RawMessage `json:"-"`
}

func keySet(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

var (
	modelProviderKeys   = keySet("id", "base_path", "api_version", "region", "organization", "project", "anthropic_version", "anthropic_betas", "stream_usage", "limit_header_passthrough", "usage_semantics")
	modelWebSocketKeys  = keySet("metering", "max_session_seconds", "token_allowance")
	modelCapacityKeys   = keySet("cooldown_enabled", "max_cooldown_seconds", "headroom_fraction", "min_weight", "default_weight", "signal_max_age_seconds")
	modelAuthKeys       = keySet("type", "in", "name", "prefix", "secret_ref")
	modelSurfaceKeys    = keySet("id", "protocol", "origin", "models", "operations", "provider", "websocket", "api_version", "project", "location", "publisher", "data_prefixes", "agents", "endpoint")
	modelSurfaceSpecKey = keySet("surface", "identity_ref", "network_profile_ref", "billing_project", "auth", "capacity_policy")
)

// The block types marshal through a method-less alias so the known fields keep
// their tags and Extra (tagged "-") is merged without overriding them.

func (p ModelProvider) MarshalJSON() ([]byte, error) {
	type plain ModelProvider
	return marshalWithExtra(plain(p), p.Extra)
}
func (p *ModelProvider) UnmarshalJSON(data []byte) error {
	type plain ModelProvider
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	extra, err := extraFields(data, modelProviderKeys)
	if err != nil {
		return err
	}
	v.Extra = extra
	*p = ModelProvider(v)
	return nil
}

func (w ModelWebSocket) MarshalJSON() ([]byte, error) {
	type plain ModelWebSocket
	return marshalWithExtra(plain(w), w.Extra)
}
func (w *ModelWebSocket) UnmarshalJSON(data []byte) error {
	type plain ModelWebSocket
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	extra, err := extraFields(data, modelWebSocketKeys)
	if err != nil {
		return err
	}
	v.Extra = extra
	*w = ModelWebSocket(v)
	return nil
}

func (c ModelCapacityPolicy) MarshalJSON() ([]byte, error) {
	type plain ModelCapacityPolicy
	return marshalWithExtra(plain(c), c.Extra)
}
func (c *ModelCapacityPolicy) UnmarshalJSON(data []byte) error {
	type plain ModelCapacityPolicy
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	extra, err := extraFields(data, modelCapacityKeys)
	if err != nil {
		return err
	}
	v.Extra = extra
	*c = ModelCapacityPolicy(v)
	return nil
}

func (a ModelSurfaceAuth) MarshalJSON() ([]byte, error) {
	type plain ModelSurfaceAuth
	return marshalWithExtra(plain(a), a.Extra)
}
func (a *ModelSurfaceAuth) UnmarshalJSON(data []byte) error {
	type plain ModelSurfaceAuth
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	extra, err := extraFields(data, modelAuthKeys)
	if err != nil {
		return err
	}
	v.Extra = extra
	*a = ModelSurfaceAuth(v)
	return nil
}

func (s ModelSurface) MarshalJSON() ([]byte, error) {
	type plain ModelSurface
	return marshalWithExtra(plain(s), s.Extra)
}
func (s *ModelSurface) UnmarshalJSON(data []byte) error {
	type plain ModelSurface
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	extra, err := extraFields(data, modelSurfaceKeys)
	if err != nil {
		return err
	}
	v.Extra = extra
	*s = ModelSurface(v)
	return nil
}

func (s ModelSurfaceSpec) MarshalJSON() ([]byte, error) {
	type plain ModelSurfaceSpec
	return marshalWithExtra(plain(s), s.Extra)
}
func (s *ModelSurfaceSpec) UnmarshalJSON(data []byte) error {
	type plain ModelSurfaceSpec
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	extra, err := extraFields(data, modelSurfaceSpecKey)
	if err != nil {
		return err
	}
	v.Extra = extra
	*s = ModelSurfaceSpec(v)
	return nil
}

// ModelSurfaceRecord is a stored surface with its revision for conditional writes.
type ModelSurfaceRecord struct {
	ID       string
	Revision uint64
	Spec     ModelSurfaceSpec
}

func surfaceRecord(r resources.Record) (ModelSurfaceRecord, error) {
	out := ModelSurfaceRecord{ID: r.ID, Revision: r.Revision}
	if err := json.Unmarshal(r.Spec, &out.Spec); err != nil {
		return out, fmt.Errorf("aiplex: decode model surface %q: %w", r.ID, err)
	}
	return out, nil
}

// ListModelSurfaces returns the tenant's model surfaces, typed.
func (c *Client) ListModelSurfaces(ctx context.Context) ([]ModelSurfaceRecord, error) {
	raw, err := c.ListResources(ctx, ModelSurfaces)
	if err != nil {
		return nil, err
	}
	out := make([]ModelSurfaceRecord, 0, len(raw))
	for _, r := range raw {
		item, err := surfaceRecord(r)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// GetModelSurface returns one surface with its revision.
func (c *Client) GetModelSurface(ctx context.Context, id string) (*ModelSurfaceRecord, error) {
	raw, err := c.GetResource(ctx, ModelSurfaces, id)
	if err != nil {
		return nil, err
	}
	out, err := surfaceRecord(*raw)
	return &out, err
}

// PutModelSurface creates (revision 0) or replaces (current revision) a
// surface. A stale revision fails with an error for which IsRevisionConflict
// is true. The server performs all validation.
func (c *Client) PutModelSurface(ctx context.Context, id string, spec ModelSurfaceSpec, revision uint64) (*ModelSurfaceRecord, error) {
	raw, err := c.PutResource(ctx, ModelSurfaces, id, spec, revision)
	if err != nil {
		return nil, err
	}
	out, err := surfaceRecord(*raw)
	return &out, err
}

// DeleteModelSurface removes a surface at the given positive revision.
func (c *Client) DeleteModelSurface(ctx context.Context, id string, revision uint64) error {
	return c.DeleteResource(ctx, ModelSurfaces, id, revision)
}

// ListModelOperations returns one provider's descriptor: its protocols,
// credential schemes and operation catalog. provider is required.
func (c *Client) ListModelOperations(ctx context.Context, provider string) (*ModelProviderDescriptor, error) {
	if provider == "" {
		return nil, fmt.Errorf("aiplex: provider required")
	}
	var out ModelProviderDescriptor
	if err := c.do(ctx, http.MethodGet, "/api/v1/llm/operations?provider="+url.QueryEscape(provider), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListGoogleModelOperations returns the Google Cloud operation catalog (the
// descriptor endpoint without a provider).
func (c *Client) ListGoogleModelOperations(ctx context.Context) ([]ModelOperation, error) {
	var out []ModelOperation
	if err := c.do(ctx, http.MethodGet, "/api/v1/llm/operations", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ModelProviderIDs lists the non-Google providers the server supports, in
// the order the server presents them.
var ModelProviderIDs = []string{ProviderOpenAI, ProviderAnthropic, ProviderAzureOpenAI, ProviderBedrock, ProviderOpenAICompatible, ProviderGeminiDeveloper}

// SurfaceCapacityPolicyState is the policy a surface enforces and its
// cooldown. Policy has defaults applied; both are nil while the policy is off.
type SurfaceCapacityPolicyState struct {
	Enabled  bool                 `json:"enabled"`
	Policy   *ModelCapacityPolicy `json:"policy,omitempty"`
	Cooldown *ModelCooldownState  `json:"cooldown,omitempty"`
}

// SurfaceCapacity is the last provider limit observation for a surface on the
// replica that answered, plus the capacity policy state. Advisory.
type SurfaceCapacity struct {
	SurfaceID      string                     `json:"surface_id"`
	Observed       bool                       `json:"observed"`
	Scope          string                     `json:"scope"`
	Limits         *ModelProviderLimits       `json:"limits,omitempty"`
	CapacityPolicy SurfaceCapacityPolicyState `json:"capacity_policy"`
}

// GetSurfaceCapacity reads a surface's capacity state. Administrator only.
func (c *Client) GetSurfaceCapacity(ctx context.Context, id string) (*SurfaceCapacity, error) {
	if err := validateResourceID(id); err != nil {
		return nil, err
	}
	var out SurfaceCapacity
	if err := c.do(ctx, http.MethodGet, "/api/v1/llm/surfaces/"+url.PathEscape(id)+"/capacity", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ModelBatch is one durable provider batch the gateway created. An "open"
// batch holds its admitted quota exposure and concurrency slot until its
// results are fetched through the gateway.
type ModelBatch struct {
	ID              string `json:"id"`
	SurfaceID       string `json:"surface_id"`
	Provider        string `json:"provider"`
	BatchID         string `json:"batch_id"`
	AttemptID       string `json:"attempt_id,omitempty"`
	Model           string `json:"model,omitempty"`
	CreateOperation string `json:"create_operation"`
	State           string `json:"state"` // open | settled
	ProviderStatus  string `json:"provider_status,omitempty"`
	Ended           bool   `json:"ended"`
	// Expected is the provider's item count; nil is unknown, never zero.
	Expected *int64 `json:"expected"`
}

// ModelBatchPage is one page of batches; NextCursor is empty on the last page.
type ModelBatchPage struct {
	Batches    []ModelBatch `json:"batches"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// ModelBatchQuery filters the batch listing. State is "", "open" or "settled".
type ModelBatchQuery struct {
	State, SurfaceID, Cursor string
	Limit                    int // 0 selects the server default
}

// ListModelBatches returns one page of the tenant's batches. Administrator only.
func (c *Client) ListModelBatches(ctx context.Context, q ModelBatchQuery) (*ModelBatchPage, error) {
	if q.State != "" && q.State != "open" && q.State != "settled" {
		return nil, fmt.Errorf("aiplex: batch state must be open or settled")
	}
	values := url.Values{}
	for k, v := range map[string]string{"state": q.State, "surface_id": q.SurfaceID, "cursor": q.Cursor} {
		if v != "" {
			values.Set(k, v)
		}
	}
	if q.Limit < 0 {
		return nil, fmt.Errorf("aiplex: batch limit must be positive")
	}
	if q.Limit > 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	var out ModelBatchPage
	if err := c.do(ctx, http.MethodGet, withQuery("/api/v1/llm/batches", values), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

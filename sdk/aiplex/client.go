// Package aiplex provides a Go SDK for the AIPlex control plane API.
//
// Usage:
//
//	c := aiplex.NewClient("https://aiplex.example.com")
//	c.SetToken("eyJhbGci...")
//
//	instances, err := c.ListInstances(ctx, &aiplex.ListInstancesOpts{Plane: "mcplex"})
package aiplex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Client is an AIPlex API client.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	token      string
	userAgent  string
}

// NewClient creates a new AIPlex SDK client.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		userAgent: "aiplex-sdk-go/0.1.0",
	}
}

// SetToken sets the bearer token for authentication.
func (c *Client) SetToken(token string) {
	c.token = token
}

// Error represents an API error response.
type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	StatusCode int    `json:"-"`
	// Details carries structured context, such as {"field": "models/0/surface_ref"}
	// for a specification the server rejected.
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	// TraceID is the W3C trace id of the failed request, when the server
	// reported one. Quote it with RequestID when asking an operator to look.
	TraceID string `json:"trace_id,omitempty"`
}

// Field returns the specification field the server named in a validation
// error, or "".
func (e *Error) Field() string {
	if e == nil || e.Details == nil {
		return ""
	}
	f, _ := e.Details["field"].(string)
	return f
}

func (e *Error) Error() string {
	return fmt.Sprintf("aiplex: %s (%d): %s", e.Code, e.StatusCode, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	return c.doWithHeaders(ctx, method, path, body, out, nil)
}

func (c *Client) doWithHeaders(ctx context.Context, method, path string, body, out any, headers map[string]string) error {
	if err := validateAPIPath(path); err != nil {
		return err
	}
	u := c.BaseURL + path

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("aiplex: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return fmt.Errorf("aiplex: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("aiplex: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var apiErr Error
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err := json.Unmarshal(raw, &apiErr); err != nil {
			return &Error{Code: "unknown", Message: plainErrorText(raw, resp.Status), StatusCode: resp.StatusCode}
		}
		apiErr.StatusCode = resp.StatusCode
		return &apiErr
	}

	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("aiplex: decode response: %w", err)
		}
	}

	return nil
}

// --- Catalog ---

// CatalogPage is a paginated catalog response.
type CatalogPage struct {
	Templates     []Template    `json:"templates"`
	Total         int           `json:"total"`
	Page          int           `json:"page"`
	PageSize      int           `json:"page_size"`
	SourcesFailed []SourceError `json:"sources_failed,omitempty"`
}

type SourceError struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

type Template struct {
	ID           string     `json:"id"`
	Source       string     `json:"source"`
	Plane        string     `json:"plane"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	Image        string     `json:"image,omitempty"`
	Version      string     `json:"version,omitempty"`
	Tools        []ToolInfo `json:"tools,omitempty"`
	TaskTypes    []string   `json:"task_types,omitempty"`
	ModelID      string     `json:"model_id,omitempty"`
	Provider     string     `json:"provider,omitempty"`
	Capabilities []string   `json:"capabilities,omitempty"`
	Category     string     `json:"category"`
	Verified     bool       `json:"verified"`
	Tags         []string   `json:"tags,omitempty"`
	Pricing      *Pricing   `json:"pricing,omitempty"`
}

type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Pricing struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// ListCatalogOpts are options for listing catalog templates.
type ListCatalogOpts struct {
	Plane string
	// Page is zero-based. PageSize zero leaves the server's default (20);
	// the server accepts 1 to 100.
	Page     int
	PageSize int
}

// ListCatalog returns a paginated catalog listing.
func (c *Client) ListCatalog(ctx context.Context, opts *ListCatalogOpts) (*CatalogPage, error) {
	q := url.Values{}
	if opts != nil {
		// Zero means not set; a negative value would silently become the server's default.
		if opts.Page < 0 {
			return nil, fmt.Errorf("aiplex: catalog page must be 0 or more")
		}
		if opts.PageSize < 0 {
			return nil, fmt.Errorf("aiplex: catalog page size must be 0 or more")
		}
		if opts.Plane != "" {
			q.Set("plane", opts.Plane)
		}
		if opts.Page > 0 {
			q.Set("page", fmt.Sprintf("%d", opts.Page))
		}
		if opts.PageSize > 0 {
			q.Set("page_size", fmt.Sprintf("%d", opts.PageSize))
		}
	}
	path := "/api/v1/catalog"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var page CatalogPage
	err := c.do(ctx, "GET", path, nil, &page)
	return &page, err
}

// GetTemplate returns a single catalog template.
func (c *Client) GetTemplate(ctx context.Context, id string) (*Template, error) {
	var t Template
	err := c.do(ctx, "GET", "/api/v1/catalog/"+url.PathEscape(id), nil, &t)
	return &t, err
}

// --- Instances ---

type Instance struct {
	ID          string         `json:"id"`
	Plane       string         `json:"plane"`
	TemplateID  string         `json:"template_id"`
	Owner       string         `json:"owner"`
	Namespace   string         `json:"namespace"`
	SpiffeID    string         `json:"spiffe_id,omitempty"`
	Scopes      []string       `json:"scopes"`
	Config      map[string]any `json:"config,omitempty"`
	Status      string         `json:"status"`
	Replicas    int            `json:"replicas"`
	DisplayName string         `json:"display_name,omitempty"`
	DeployedAt  time.Time      `json:"deployed_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeployedBy  string         `json:"deployed_by"`
}

type DeployRequest struct {
	Plane       string         `json:"plane"`
	TemplateID  string         `json:"template_id"`
	Config      map[string]any `json:"config,omitempty"`
	DisplayName string         `json:"display_name,omitempty"`
}

type DeployHistory struct {
	ID          string         `json:"id"`
	InstanceID  string         `json:"instance_id"`
	Action      string         `json:"action"`
	Plane       string         `json:"plane"`
	TemplateID  string         `json:"template_id,omitempty"`
	Owner       string         `json:"owner"`
	PerformedBy string         `json:"performed_by"`
	Config      map[string]any `json:"config,omitempty"`
	Timestamp   time.Time      `json:"timestamp"`
	DurationMs  int64          `json:"duration_ms,omitempty"`
	Success     bool           `json:"success"`
	Error       string         `json:"error,omitempty"`
}

// ListInstancesOpts filters the instance list.
type ListInstancesOpts struct {
	Plane string
}

// ListInstances returns all instances, optionally filtered by plane.
func (c *Client) ListInstances(ctx context.Context, opts *ListInstancesOpts) ([]Instance, error) {
	path := "/api/v1/instances"
	if opts != nil && opts.Plane != "" {
		path += "?plane=" + url.QueryEscape(opts.Plane)
	}
	var list []Instance
	err := c.do(ctx, "GET", path, nil, &list)
	return list, err
}

// GetInstance returns a single instance by ID.
func (c *Client) GetInstance(ctx context.Context, id string) (*Instance, error) {
	var inst Instance
	err := c.do(ctx, "GET", "/api/v1/instances/"+url.PathEscape(id), nil, &inst)
	return &inst, err
}

// Deploy creates a new instance.
func (c *Client) Deploy(ctx context.Context, req *DeployRequest) (*Instance, error) {
	var inst Instance
	err := c.do(ctx, "POST", "/api/v1/instances", req, &inst)
	return &inst, err
}

// Undeploy terminates and removes an instance.
func (c *Client) Undeploy(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/api/v1/instances/"+url.PathEscape(id), nil, nil)
}

// GetHistory returns deploy history for an instance.
func (c *Client) GetHistory(ctx context.Context, instanceID string) ([]DeployHistory, error) {
	var list []DeployHistory
	err := c.do(ctx, "GET", "/api/v1/instances/"+url.PathEscape(instanceID)+"/history", nil, &list)
	return list, err
}

// --- Agents ---

type Agent struct {
	Revision      int64     `json:"revision"`
	ClientID      string    `json:"client_id"`
	DisplayName   string    `json:"display_name"`
	Description   string    `json:"description,omitempty"`
	AuthMethod    string    `json:"auth_method"`
	GrantTypes    []string  `json:"grant_types"`
	AllowedScopes []string  `json:"allowed_scopes"`
	WIFPrincipal  string    `json:"wif_principal,omitempty"`
	SpiffeID      string    `json:"spiffe_id,omitempty"`
	RedirectURIs  []string  `json:"redirect_uris,omitempty"`
	RegisteredAt  time.Time `json:"registered_at"`
	RegisteredBy  string    `json:"registered_by"`
	Status        string    `json:"status"`
}

type AgentPermissions struct {
	AgentID string                 `json:"agent_id"`
	Ceiling map[string][]ScopeInfo `json:"ceiling"`
}

type ScopeInfo struct {
	Scope       string `json:"scope"`
	Description string `json:"description"`
}

type RegisterAgentRequest struct {
	ClientID      string   `json:"client_id"`
	DisplayName   string   `json:"display_name"`
	Description   string   `json:"description,omitempty"`
	AuthMethod    string   `json:"auth_method"`
	GrantTypes    []string `json:"grant_types"`
	AllowedScopes []string `json:"allowed_scopes"`
	WIFPrincipal  string   `json:"wif_principal,omitempty"`
	RedirectURIs  []string `json:"redirect_uris,omitempty"`
}

// ListAgents returns all registered agents.
func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	var list []Agent
	err := c.do(ctx, "GET", "/api/v1/agents", nil, &list)
	return list, err
}

// GetAgent returns a single agent.
func (c *Client) GetAgent(ctx context.Context, clientID string) (*Agent, error) {
	var a Agent
	err := c.do(ctx, "GET", "/api/v1/agents/"+url.PathEscape(clientID), nil, &a)
	return &a, err
}

// RegisterAgent registers a new agent.
func (c *Client) RegisterAgent(ctx context.Context, req *RegisterAgentRequest) (*Agent, error) {
	var a Agent
	err := c.doWithHeaders(ctx, "POST", "/api/v1/agents", req, &a, map[string]string{"If-Match": "0"})
	return &a, err
}

// DeleteAgent removes an agent.
func (c *Client) DeleteAgent(ctx context.Context, clientID string) error {
	current, err := c.GetAgent(ctx, clientID)
	if err != nil {
		return err
	}
	return c.DeleteAgentAtRevision(ctx, clientID, current.Revision)
}

// GetAgentPermissions returns the cross-plane permission view for an agent.
func (c *Client) GetAgentPermissions(ctx context.Context, clientID string) (*AgentPermissions, error) {
	var p AgentPermissions
	err := c.do(ctx, "GET", "/api/v1/agents/"+url.PathEscape(clientID)+"/permissions", nil, &p)
	return &p, err
}

// --- LLMPlex ---

type LLMRouteConfig struct {
	Revision  int64           `json:"revision"`
	ID        string          `json:"id"`
	ModelID   string          `json:"model_id"`
	Owner     string          `json:"owner"`
	Backends  []LLMBackend    `json:"backends"`
	Fallbacks []string        `json:"fallbacks,omitempty"`
	CacheTTL  int             `json:"cache_ttl_seconds,omitempty"`
	Budget    *UsageBudget    `json:"budget,omitempty"`
	RateLimit *ModelRateLimit `json:"rate_limit,omitempty"`
	// BackendNamingVersion is server-owned native resource naming metadata.
	BackendNamingVersion int `json:"backend_naming_version,omitempty"`
	// RetiredBackends is server-owned cleanup inventory.
	RetiredBackends []string  `json:"retired_backends,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type LLMBackend struct {
	Provider  string `json:"provider"`
	ModelID   string `json:"model_id"`
	Weight    int    `json:"weight"`
	Enabled   bool   `json:"enabled"`
	SecretRef string `json:"secret_ref,omitempty"`
}

type UsageBudget struct {
	MaxDailyCostUSD   float64 `json:"max_daily_cost_usd,omitempty"`
	MaxMonthlyCostUSD float64 `json:"max_monthly_cost_usd,omitempty"`
	MaxDailyTokens    int64   `json:"max_daily_tokens,omitempty"`
	AlertThreshold    float64 `json:"alert_threshold_pct,omitempty"`
}

type ModelRateLimit struct {
	RequestsPerMinute int `json:"requests_per_minute"`
	TokensPerMinute   int `json:"tokens_per_minute,omitempty"`
}

type UsageRecord struct {
	ID           string    `json:"id"`
	ModelID      string    `json:"model_id"`
	Provider     string    `json:"provider"`
	AgentID      string    `json:"agent_id"`
	UserID       string    `json:"user_id"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	TotalTokens  int       `json:"total_tokens"`
	CostUSD      float64   `json:"cost_usd"`
	LatencyMs    int       `json:"latency_ms"`
	Cached       bool      `json:"cached"`
	Timestamp    time.Time `json:"timestamp"`
}

type UsageSummary struct {
	Provider     string  `json:"provider,omitempty"`
	AgentID      string  `json:"agent_id,omitempty"`
	ModelID      string  `json:"model_id,omitempty"`
	Period       string  `json:"period"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	RequestCount int64   `json:"request_count"`
	CacheHits    int64   `json:"cache_hits"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
}

type ProviderConfig struct {
	Revision    int64  `json:"revision"`
	Provider    string `json:"provider"`
	DisplayName string `json:"display_name"`
	BaseURL     string `json:"base_url,omitempty"`
	SecretRef   string `json:"secret_ref"`
	Enabled     bool   `json:"enabled"`
	Region      string `json:"region,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
}

// ListLLMRoutes returns all LLM routing configurations.
func (c *Client) ListLLMRoutes(ctx context.Context) ([]LLMRouteConfig, error) {
	var list []LLMRouteConfig
	err := c.do(ctx, "GET", "/api/v1/llm/routes", nil, &list)
	return list, err
}

// GetLLMRoute returns a single route config.
func (c *Client) GetLLMRoute(ctx context.Context, modelID string) (*LLMRouteConfig, error) {
	var rc LLMRouteConfig
	err := c.do(ctx, "GET", "/api/v1/llm/routes/"+url.PathEscape(modelID), nil, &rc)
	return &rc, err
}

// PutLLMRoute creates or updates a route config.
func (c *Client) PutLLMRoute(ctx context.Context, modelID string, rc *LLMRouteConfig) (*LLMRouteConfig, error) {
	var out LLMRouteConfig
	if rc == nil {
		return nil, fmt.Errorf("route configuration required")
	}
	err := c.doWithHeaders(ctx, "PUT", "/api/v1/llm/routes/"+url.PathEscape(modelID), rc, &out, map[string]string{"If-Match": strconv.FormatInt(rc.Revision, 10)})
	return &out, err
}

// DeleteLLMRoute deletes a route config.
func (c *Client) DeleteLLMRoute(ctx context.Context, modelID string) error {
	current, err := c.GetLLMRoute(ctx, modelID)
	if err != nil {
		return err
	}
	return c.DeleteLLMRouteAtRevision(ctx, modelID, current.Revision)
}

// ListProviders returns all LLM provider configurations.
func (c *Client) ListProviders(ctx context.Context) ([]ProviderConfig, error) {
	var list []ProviderConfig
	err := c.do(ctx, "GET", "/api/v1/llm/providers", nil, &list)
	return list, err
}

// PutProvider creates or updates a provider config.
func (c *Client) PutProvider(ctx context.Context, provider string, cfg *ProviderConfig) (*ProviderConfig, error) {
	var out ProviderConfig
	if cfg == nil {
		return nil, fmt.Errorf("provider configuration required")
	}
	err := c.doWithHeaders(ctx, "PUT", "/api/v1/llm/providers/"+url.PathEscape(provider), cfg, &out, map[string]string{"If-Match": strconv.FormatInt(cfg.Revision, 10)})
	return &out, err
}

// RecordUsage records a single LLM usage event.
func (c *Client) RecordUsage(ctx context.Context, rec *UsageRecord) (*UsageRecord, error) {
	var out UsageRecord
	err := c.do(ctx, "POST", "/api/v1/llm/usage", rec, &out)
	return &out, err
}

// GetUsageSummary returns aggregated usage for a time period.
func (c *Client) GetUsageSummary(ctx context.Context, period string) (*UsageSummary, error) {
	return c.GetUsageSummaryWithOpts(ctx, &UsageSummaryOpts{Period: period})
}

// --- A2APlex ---

type AgentCard struct {
	Capabilities []string         `json:"capabilities,omitempty"`
	Metadata     map[string]any   `json:"metadata,omitempty"`
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	URL          string           `json:"url"`
	Version      string           `json:"version,omitempty"`
	TaskTypes    []TaskTypeInfo   `json:"task_types"`
	AuthSchemes  []AuthSchemeInfo `json:"auth_schemes,omitempty"`
}

type TaskTypeInfo struct {
	OutputSchema map[string]any `json:"output_schema,omitempty"`
	Type         string         `json:"type"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema,omitempty"`
}

type AuthSchemeInfo struct {
	Scheme string         `json:"scheme"`
	Config map[string]any `json:"config,omitempty"`
}

type Delegation struct {
	ReportedBy       string     `json:"reported_by,omitempty"`
	ReportedAt       time.Time  `json:"reported_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	Error            string     `json:"error,omitempty"`
	TraceID          string     `json:"trace_id,omitempty"`
	SpanID           string     `json:"span_id,omitempty"`
	ParentSpanID     string     `json:"parent_span_id,omitempty"`
	ID               string     `json:"id"`
	CallerAgentID    string     `json:"caller_agent_id"`
	CalleeAgentID    string     `json:"callee_agent_id"`
	CallerInstanceID string     `json:"caller_instance_id"`
	CalleeInstanceID string     `json:"callee_instance_id"`
	TaskType         string     `json:"task_type"`
	Status           string     `json:"status"`
	UserID           string     `json:"user_id"`
	StartedAt        time.Time  `json:"started_at"`
	DurationMs       int64      `json:"duration_ms,omitempty"`
	ParentID         string     `json:"parent_id,omitempty"`
}

type DelegationChain struct {
	RootDelegation  Delegation   `json:"root"`
	Children        []Delegation `json:"children,omitempty"`
	Depth           int          `json:"depth"`
	TotalDurationMs int64        `json:"total_duration_ms"`
}

type RecordDelegationRequest struct {
	Status           string     `json:"status,omitempty"`
	StartedAt        time.Time  `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	DurationMs       int64      `json:"duration_ms,omitempty"`
	Error            string     `json:"error,omitempty"`
	TraceID          string     `json:"trace_id,omitempty"`
	SpanID           string     `json:"span_id,omitempty"`
	ParentSpanID     string     `json:"parent_span_id,omitempty"`
	ID               string     `json:"id"`
	CallerAgentID    string     `json:"caller_agent_id"`
	CalleeAgentID    string     `json:"callee_agent_id"`
	CallerInstanceID string     `json:"caller_instance_id"`
	CalleeInstanceID string     `json:"callee_instance_id"`
	TaskType         string     `json:"task_type"`
	UserID           string     `json:"user_id"`
	ParentID         string     `json:"parent_id,omitempty"`
}

type UpdateDelegationRequest struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// GetAgentCard returns the A2A Agent Card for a deployed instance.
func (c *Client) GetAgentCard(ctx context.Context, instanceID string) (*AgentCard, error) {
	var card AgentCard
	err := c.do(ctx, "GET", "/a2a/"+url.PathEscape(instanceID)+"/.well-known/agent.json", nil, &card)
	return &card, err
}

// ListAgentCards returns summary cards for all running A2A agents.
func (c *Client) ListAgentCards(ctx context.Context) ([]map[string]any, error) {
	var list []map[string]any
	err := c.do(ctx, "GET", "/api/v1/a2a/agents", nil, &list)
	return list, err
}

// RecordDelegation records a new agent-to-agent delegation.
func (c *Client) RecordDelegation(ctx context.Context, req *RecordDelegationRequest) (*Delegation, error) {
	var d Delegation
	err := c.do(ctx, "POST", "/api/v1/a2a/delegations", req, &d)
	return &d, err
}

// ListDelegations returns all recorded delegations.
func (c *Client) ListDelegations(ctx context.Context) ([]Delegation, error) {
	var list []Delegation
	err := c.do(ctx, "GET", "/api/v1/a2a/delegations", nil, &list)
	return list, err
}

// GetDelegation returns a single delegation.
func (c *Client) GetDelegation(ctx context.Context, id string) (*Delegation, error) {
	path, err := delegationPath(id)
	if err != nil {
		return nil, err
	}
	var d Delegation
	err = c.do(ctx, "GET", path, nil, &d)
	return &d, err
}

// UpdateDelegation updates the status of a delegation.
func (c *Client) UpdateDelegation(ctx context.Context, id string, req *UpdateDelegationRequest) (*Delegation, error) {
	path, err := delegationPath(id)
	if err != nil {
		return nil, err
	}
	if err = req.Validate(); err != nil {
		return nil, err
	}
	var d Delegation
	err = c.do(ctx, "PATCH", path, req, &d)
	return &d, err
}

// GetDelegationChain returns the full call chain for a delegation.
func (c *Client) GetDelegationChain(ctx context.Context, id string) (*DelegationChain, error) {
	path, err := delegationPath(id)
	if err != nil {
		return nil, err
	}
	var chain DelegationChain
	err = c.do(ctx, "GET", path+"/chain", nil, &chain)
	return &chain, err
}

// --- SkillsPlex ---

// SkillServerSummary describes a running SkillsPlex instance.
type SkillServerSummary struct {
	InstanceID  string   `json:"instance_id"`
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	SkillBundle string   `json:"skill_bundle,omitempty"`
	Skills      []string `json:"skills"`
	Status      string   `json:"status"`
}

// SkillInvocation mirrors models.SkillInvocation for SDK consumers.
type SkillInvocation struct {
	ReportedBy   string    `json:"reported_by,omitempty"`
	ReportedAt   time.Time `json:"reported_at"`
	ID           string    `json:"id"`
	AgentID      string    `json:"agent_id"`
	InstanceID   string    `json:"instance_id"`
	SkillName    string    `json:"skill_name"`
	UserID       string    `json:"user_id,omitempty"`
	Status       string    `json:"status"`
	StartedAt    time.Time `json:"started_at"`
	DurationMs   int64     `json:"duration_ms,omitempty"`
	Error        string    `json:"error,omitempty"`
	TraceID      string    `json:"trace_id,omitempty"`
	SpanID       string    `json:"span_id,omitempty"`
	ParentSpanID string    `json:"parent_span_id,omitempty"`
}

// RecordSkillInvocationRequest is the body sent to POST /api/v1/skills/invocations.
type RecordSkillInvocationRequest struct {
	StartedAt    time.Time `json:"started_at,omitempty"`
	SpanID       string    `json:"span_id,omitempty"`
	ParentSpanID string    `json:"parent_span_id,omitempty"`
	AgentID      string    `json:"agent_id"`
	InstanceID   string    `json:"instance_id"`
	SkillName    string    `json:"skill_name"`
	UserID       string    `json:"user_id,omitempty"`
	Status       string    `json:"status,omitempty"`
	DurationMs   int64     `json:"duration_ms,omitempty"`
	Error        string    `json:"error,omitempty"`
	TraceID      string    `json:"trace_id,omitempty"`
}

// ListSkillServers returns running SkillsPlex instances.
func (c *Client) ListSkillServers(ctx context.Context) ([]SkillServerSummary, error) {
	var list []SkillServerSummary
	err := c.do(ctx, "GET", "/api/v1/skills/servers", nil, &list)
	return list, err
}

// RecordSkillInvocation appends a skill invocation audit record.
func (c *Client) RecordSkillInvocation(ctx context.Context, req *RecordSkillInvocationRequest) (*SkillInvocation, error) {
	var inv SkillInvocation
	err := c.do(ctx, "POST", "/api/v1/skills/invocations", req, &inv)
	return &inv, err
}

// ListSkillInvocationsOpts bounds a ListSkillInvocations call. Limit zero
// leaves the server's default (100); the server accepts 1 to 1000.
type ListSkillInvocationsOpts struct {
	Limit int
}

// ListSkillInvocations returns recent invocations, optionally filtered. Pass at
// most one ListSkillInvocationsOpts to set the limit; without one the server's
// default applies.
func (c *Client) ListSkillInvocations(ctx context.Context, agentID, skillName string, opts ...ListSkillInvocationsOpts) ([]SkillInvocation, error) {
	if len(opts) > 1 {
		return nil, fmt.Errorf("at most one ListSkillInvocationsOpts")
	}
	q := url.Values{}
	if agentID != "" {
		q.Set("agent_id", agentID)
	}
	if skillName != "" {
		q.Set("skill", skillName)
	}
	if len(opts) == 1 {
		// Zero means not set; a negative value would silently become the server's default.
		if opts[0].Limit < 0 {
			return nil, fmt.Errorf("aiplex: skill invocation limit must be 0 or more")
		}
		if opts[0].Limit > 0 {
			q.Set("limit", strconv.Itoa(opts[0].Limit))
		}
	}
	path := "/api/v1/skills/invocations"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var list []SkillInvocation
	err := c.do(ctx, "GET", path, nil, &list)
	return list, err
}

// --- Dashboard ---

type DashboardStats struct {
	TotalInstances      int `json:"total_instances"`
	RunningInstances    int `json:"running_instances"`
	RegisteredAgents    int `json:"registered_agents"`
	ActivePlanes        int `json:"active_planes"`
	MCPlexInstances     int `json:"mcplex_instances"`
	A2APlexInstances    int `json:"a2aplex_instances"`
	SkillsPlexInstances int `json:"skillsplex_instances"`
	LLMPlexInstances    int `json:"llmplex_instances"`
	// DailyCostUSD, DailyTokens, DailyRequests and ToolCalls are null when
	// unknown or unavailable (see UsageSource), never zero.
	DailyCostUSD             *float64 `json:"daily_cost_usd"`
	DailyTokens              *int64   `json:"daily_tokens"`
	DailyRequests            *int64   `json:"daily_requests"`
	DailyUnknownCostRecords  int64    `json:"daily_unknown_cost_records"`
	DailyUnknownUsageRecords int64    `json:"daily_unknown_usage_records"`
	UsageSource              string   `json:"usage_source"`
	ToolCalls                *int64   `json:"tool_calls_24h"`
	A2ADelegations           int64    `json:"a2a_delegations_24h"`
	PolicyDenials            int64    `json:"policy_denials_24h"`
}

type PolicyDenial struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Plane     string    `json:"plane"`
	AgentID   string    `json:"agent_id"`
	UserID    string    `json:"user_id"`
	Action    string    `json:"action"`
	Scope     string    `json:"scope"`
	Reason    string    `json:"reason"`
	RequestID string    `json:"request_id"`
	// TraceID is the W3C trace id of the refused request, when known.
	TraceID string `json:"trace_id,omitempty"`
}

// GetDashboardStats returns the unified dashboard overview.
func (c *Client) GetDashboardStats(ctx context.Context) (*DashboardStats, error) {
	var s DashboardStats
	err := c.do(ctx, "GET", "/api/v1/dashboard/stats", nil, &s)
	return &s, err
}

// ListPolicyDenials returns recent policy denial events.
func (c *Client) ListPolicyDenials(ctx context.Context) ([]PolicyDenial, error) {
	var list []PolicyDenial
	err := c.do(ctx, "GET", "/api/v1/dashboard/denials", nil, &list)
	return list, err
}

// RecordPolicyDenial records a new policy denial (admin). The server assigns
// the id and timestamp and refuses unknown fields and oversized values.
func (c *Client) RecordPolicyDenial(ctx context.Context, d *PolicyDenial) (*PolicyDenial, error) {
	var out PolicyDenial
	err := c.do(ctx, "POST", "/api/v1/dashboard/denials", d, &out)
	return &out, err
}

// --- Auth / User Scopes ---

type UserScopes struct {
	Revision    int64               `json:"revision"`
	UserID      string              `json:"user_id"`
	PrincipalID string              `json:"principal_id,omitempty"`
	Scopes      []string            `json:"scopes"`
	ByPlane     map[string][]string `json:"by_plane"`
}

// GetUserScopes returns manually assigned scopes for a canonical principal.
func (c *Client) GetUserScopes(ctx context.Context, principalID string) (*UserScopes, error) {
	if err := validatePrincipalID(principalID); err != nil {
		return nil, err
	}
	var result UserScopes
	err := c.do(ctx, http.MethodGet, "/auth/users/"+url.PathEscape(principalID)+"/scopes", nil, &result)
	return &result, err
}

// SetUserScopes updates manual grants. The API returns204; read the authoritative
// representation afterward rather than decoding a nonexistent response body.
func (c *Client) SetUserScopes(ctx context.Context, principalID string, scopes []string) (*UserScopes, error) {
	if scopes == nil {
		scopes = []string{}
	} // explicit empty grant set, never JSON null
	if err := validatePrincipalID(principalID); err != nil {
		return nil, err
	}
	current, err := c.GetUserScopes(ctx, principalID)
	if err != nil {
		return nil, err
	}
	return c.SetUserScopesAtRevision(ctx, principalID, scopes, current.Revision)
}

// Health checks the server health.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, "GET", "/healthz", nil, nil)
}

// ── Runs ────────────────────────────

// ExecutionRun is the projected per-run summary returned by /api/v1/runs.
type ExecutionRun struct {
	RunID            string     `json:"run_id"`
	TenantID         string     `json:"tenant_id"`
	AgentID          string     `json:"agent_id"`
	Plane            string     `json:"plane"`
	Actor            string     `json:"actor"`
	Subject          string     `json:"subject"`
	AIPlexInstanceID string     `json:"aiplex_instance_id,omitempty"`
	Status           string     `json:"status"`
	StartedAt        time.Time  `json:"started_at"`
	EndedAt          *time.Time `json:"ended_at,omitempty"`
	DecisionsCount   int64      `json:"decisions_count"`
	EffectsCount     int64      `json:"effects_count"`
	UnknownEffects   int64      `json:"unknown_effects"`
	Obligations      int64      `json:"obligations"`
	PolicyViolations int64      `json:"policy_violations"`
	BudgetUSDCharged float64    `json:"budget_usd_charged"`
	Compacted        bool       `json:"compacted"`
	CompactedAt      *time.Time `json:"compacted_at,omitempty"`
	RetainedUntil    *time.Time `json:"retained_until,omitempty"`
}

// ExecutionEvent is one journaled row in a run's timeline.
type ExecutionEvent struct {
	RunID            string    `json:"run_id"`
	Seq              int64     `json:"seq"`
	TenantID         string    `json:"tenant_id"`
	AgentID          string    `json:"agent_id"`
	Plane            string    `json:"plane"`
	Actor            string    `json:"actor"`
	Subject          string    `json:"subject"`
	AIPlexInstanceID string    `json:"aiplex_instance_id,omitempty"`
	Kind             string    `json:"kind"`
	Scope            string    `json:"scope,omitempty"`
	Tool             string    `json:"tool,omitempty"`
	Timestamp        time.Time `json:"timestamp"`
	PayloadJSON      string    `json:"payload_json,omitempty"`
}

// OperatorAudit is a row from /runs/{id}/operator-audit.
type OperatorAudit struct {
	ID         string    `json:"id"`
	RunID      string    `json:"run_id"`
	Action     string    `json:"action"`
	Actor      string    `json:"actor"`
	At         time.Time `json:"at"`
	Reason     string    `json:"reason,omitempty"`
	GateName   string    `json:"gate_name,omitempty"`
	Resolution string    `json:"resolution,omitempty"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
}

// ListRunsOpts narrows the result set; zero-value returns all.
type ListRunsOpts struct {
	TenantID          string
	AgentID           string
	HasUnknownEffects bool
	HasObligations    bool
	Limit             int
}

// ListRuns returns the most recent runs matching opts.
func (c *Client) ListRuns(ctx context.Context, opts ListRunsOpts) ([]ExecutionRun, error) {
	q := url.Values{}
	if opts.TenantID != "" {
		q.Set("tenant_id", opts.TenantID)
	}
	if opts.AgentID != "" {
		q.Set("agent_id", opts.AgentID)
	}
	if opts.HasUnknownEffects {
		q.Set("has_unknown_effects", "true")
	}
	if opts.HasObligations {
		q.Set("has_obligations", "true")
	}
	if opts.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", opts.Limit))
	}
	path := "/api/v1/runs"
	if s := q.Encode(); s != "" {
		path += "?" + s
	}
	var resp struct {
		Runs []ExecutionRun `json:"runs"`
	}
	if err := c.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Runs, nil
}

// GetRun returns the projected summary for a single run.
func (c *Client) GetRun(ctx context.Context, runID string) (*ExecutionRun, error) {
	var run ExecutionRun
	if err := c.do(ctx, "GET", "/api/v1/runs/"+url.PathEscape(runID), nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// ListRunEventsOpts pages a run's timeline. FromSeq is inclusive (the
// server returns events with seq >= FromSeq); zero values use the server
// defaults (from the start, 1000 events).
type ListRunEventsOpts struct {
	FromSeq int64
	Limit   int
}

// ListRunEvents returns the ordered timeline for a run. Pass at most one
// ListRunEventsOpts to page it; without one the server's defaults apply.
func (c *Client) ListRunEvents(ctx context.Context, runID string, opts ...ListRunEventsOpts) ([]ExecutionEvent, error) {
	if len(opts) > 1 {
		return nil, fmt.Errorf("at most one ListRunEventsOpts")
	}
	path := "/api/v1/runs/" + url.PathEscape(runID) + "/events"
	if len(opts) == 1 {
		q := url.Values{}
		if opts[0].FromSeq > 0 {
			q.Set("from_seq", strconv.FormatInt(opts[0].FromSeq, 10))
		}
		if opts[0].Limit > 0 {
			q.Set("limit", strconv.Itoa(opts[0].Limit))
		}
		if s := q.Encode(); s != "" {
			path += "?" + s
		}
	}
	var resp struct {
		Events []ExecutionEvent `json:"events"`
	}
	if err := c.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Events, nil
}

// ListRunOperatorAudit returns the parallel operator timeline.
func (c *Client) ListRunOperatorAudit(ctx context.Context, runID string) ([]OperatorAudit, error) {
	var resp struct {
		Audit []OperatorAudit `json:"audit"`
	}
	if err := c.do(ctx, "GET", "/api/v1/runs/"+url.PathEscape(runID)+"/operator-audit", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Audit, nil
}

// Operator actions — each returns nil on 202 Accepted.

func (c *Client) RedriveRun(ctx context.Context, runID string) error {
	return c.do(ctx, "POST", "/api/v1/runs/"+url.PathEscape(runID)+"/redrive", struct{}{}, nil)
}

func (c *Client) ReconcileRun(ctx context.Context, runID string) error {
	return c.do(ctx, "POST", "/api/v1/runs/"+url.PathEscape(runID)+"/reconcile", struct{}{}, nil)
}

func (c *Client) CancelRun(ctx context.Context, runID, reason string) error {
	return c.do(ctx, "POST", "/api/v1/runs/"+url.PathEscape(runID)+"/cancel",
		map[string]string{"reason": reason}, nil)
}

func (c *Client) SignalRun(ctx context.Context, runID, gateName, resolutionJSON string) error {
	body := map[string]string{"gate_name": gateName, "resolution_json": resolutionJSON}
	return c.do(ctx, "POST", "/api/v1/runs/"+url.PathEscape(runID)+"/signal", body, nil)
}

func (c *Client) CompensateRun(ctx context.Context, runID string) error {
	return c.do(ctx, "POST", "/api/v1/runs/"+url.PathEscape(runID)+"/compensate", struct{}{}, nil)
}

// CompactRun triggers an out-of-band compaction of a settled run. The
// scheduled retention reactor handles the policy-driven path; this is
// the manual override for ops who want to free payload bytes early.
func (c *Client) CompactRun(ctx context.Context, runID string) error {
	return c.do(ctx, "POST", "/api/v1/runs/"+url.PathEscape(runID)+"/compact", struct{}{}, nil)
}

// Explicit revision methods preserve a selection made by an earlier read.
func (c *Client) DeleteAgentAtRevision(ctx context.Context, id string, revision int64) error {
	if revision <= 0 {
		return fmt.Errorf("positive revision required")
	}
	return c.doWithHeaders(ctx, http.MethodDelete, "/api/v1/agents/"+url.PathEscape(id), nil, nil, map[string]string{"If-Match": strconv.FormatInt(revision, 10)})
}
func (c *Client) DeleteLLMRouteAtRevision(ctx context.Context, id string, revision int64) error {
	if revision <= 0 {
		return fmt.Errorf("positive revision required")
	}
	return c.doWithHeaders(ctx, http.MethodDelete, "/api/v1/llm/routes/"+url.PathEscape(id), nil, nil, map[string]string{"If-Match": strconv.FormatInt(revision, 10)})
}
func (c *Client) SetUserScopesAtRevision(ctx context.Context, id string, scopes []string, revision int64) (*UserScopes, error) {
	if err := validatePrincipalID(id); err != nil {
		return nil, err
	}
	if revision < 0 {
		return nil, fmt.Errorf("nonnegative revision required")
	}
	if scopes == nil {
		scopes = []string{}
	}
	if err := c.doWithHeaders(ctx, http.MethodPut, "/auth/users/"+url.PathEscape(id)+"/scopes", map[string][]string{"scopes": scopes}, nil, map[string]string{"If-Match": strconv.FormatInt(revision, 10)}); err != nil {
		return nil, err
	}
	return c.GetUserScopes(ctx, id)
}

// plainErrorText is the first line of a non-JSON error body (for example the
// gateway's "operator tool credential unavailable"), trimmed and cut at 300
// characters; fallback when it has none. The text may come from a proxy in
// front of the API and is printed to a terminal, so only printable characters
// are kept: control characters (C0 and C1, which include terminal escapes) and
// format characters (bidirectional overrides, zero-width marks) are dropped,
// and any other white space becomes a plain space.
func plainErrorText(body []byte, fallback string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(body)), "\n")
	line = strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case unicode.IsPrint(r):
			return r
		case unicode.IsSpace(r):
			return ' '
		default:
			return -1
		}
	}, line))
	if line == "" {
		return fallback
	}
	if r := []rune(line); len(r) > 300 {
		line = string(r[:300])
	}
	return line
}

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
	"strings"
	"time"
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
}

func (e *Error) Error() string {
	return fmt.Sprintf("aiplex: %s (%d): %s", e.Code, e.StatusCode, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
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

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("aiplex: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var apiErr Error
		if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
			return &Error{Code: "unknown", Message: resp.Status, StatusCode: resp.StatusCode}
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
	ID           string            `json:"id"`
	Source       string            `json:"source"`
	Plane        string            `json:"plane"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Image        string            `json:"image,omitempty"`
	Version      string            `json:"version,omitempty"`
	Tools        []ToolInfo        `json:"tools,omitempty"`
	TaskTypes    []string          `json:"task_types,omitempty"`
	ModelID      string            `json:"model_id,omitempty"`
	Provider     string            `json:"provider,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Category     string            `json:"category"`
	Verified     bool              `json:"verified"`
	Tags         []string          `json:"tags,omitempty"`
	Pricing      *Pricing          `json:"pricing,omitempty"`
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
	Page  int
}

// ListCatalog returns a paginated catalog listing.
func (c *Client) ListCatalog(ctx context.Context, opts *ListCatalogOpts) (*CatalogPage, error) {
	q := url.Values{}
	if opts != nil {
		if opts.Plane != "" {
			q.Set("plane", opts.Plane)
		}
		if opts.Page > 0 {
			q.Set("page", fmt.Sprintf("%d", opts.Page))
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
	ClientID      string   `json:"client_id"`
	DisplayName   string   `json:"display_name"`
	Description   string   `json:"description,omitempty"`
	AuthMethod    string   `json:"auth_method"`
	GrantTypes    []string `json:"grant_types"`
	AllowedScopes []string `json:"allowed_scopes"`
	WIFPrincipal  string   `json:"wif_principal,omitempty"`
	SpiffeID      string   `json:"spiffe_id,omitempty"`
	RedirectURIs  []string `json:"redirect_uris,omitempty"`
	RegisteredAt  time.Time `json:"registered_at"`
	RegisteredBy  string   `json:"registered_by"`
	Status        string   `json:"status"`
}

type AgentPermissions struct {
	AgentID string                       `json:"agent_id"`
	Ceiling map[string][]ScopeInfo       `json:"ceiling"`
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
	err := c.do(ctx, "POST", "/api/v1/agents", req, &a)
	return &a, err
}

// DeleteAgent removes an agent.
func (c *Client) DeleteAgent(ctx context.Context, clientID string) error {
	return c.do(ctx, "DELETE", "/api/v1/agents/"+url.PathEscape(clientID), nil, nil)
}

// GetAgentPermissions returns the cross-plane permission view for an agent.
func (c *Client) GetAgentPermissions(ctx context.Context, clientID string) (*AgentPermissions, error) {
	var p AgentPermissions
	err := c.do(ctx, "GET", "/api/v1/agents/"+url.PathEscape(clientID)+"/permissions", nil, &p)
	return &p, err
}

// --- LLMPlex ---

type LLMRouteConfig struct {
	ID        string       `json:"id"`
	ModelID   string       `json:"model_id"`
	Owner     string       `json:"owner"`
	Backends  []LLMBackend `json:"backends"`
	Fallbacks []string     `json:"fallbacks,omitempty"`
	CacheTTL  int          `json:"cache_ttl_seconds,omitempty"`
	Budget    *UsageBudget `json:"budget,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
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
	err := c.do(ctx, "PUT", "/api/v1/llm/routes/"+url.PathEscape(modelID), rc, &out)
	return &out, err
}

// DeleteLLMRoute deletes a route config.
func (c *Client) DeleteLLMRoute(ctx context.Context, modelID string) error {
	return c.do(ctx, "DELETE", "/api/v1/llm/routes/"+url.PathEscape(modelID), nil, nil)
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
	err := c.do(ctx, "PUT", "/api/v1/llm/providers/"+url.PathEscape(provider), cfg, &out)
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
	var s UsageSummary
	err := c.do(ctx, "GET", "/api/v1/llm/usage/summary?period="+url.QueryEscape(period), nil, &s)
	return &s, err
}

// --- A2APlex ---

type AgentCard struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	URL         string           `json:"url"`
	Version     string           `json:"version,omitempty"`
	TaskTypes   []TaskTypeInfo   `json:"task_types"`
	AuthSchemes []AuthSchemeInfo `json:"auth_schemes,omitempty"`
}

type TaskTypeInfo struct {
	Type        string         `json:"type"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

type AuthSchemeInfo struct {
	Scheme string         `json:"scheme"`
	Config map[string]any `json:"config,omitempty"`
}

type Delegation struct {
	ID               string     `json:"id"`
	CallerAgentID    string     `json:"caller_agent_id"`
	CalleeAgentID    string     `json:"callee_agent_id"`
	CallerInstanceID string     `json:"caller_instance_id"`
	CalleeInstanceID string     `json:"callee_instance_id"`
	TaskType         string     `json:"task_type"`
	Status           string     `json:"status"`
	UserID           string     `json:"user_id"`
	StartedAt        time.Time  `json:"started_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	DurationMs       int64      `json:"duration_ms,omitempty"`
	Error            string     `json:"error,omitempty"`
	ParentID         string     `json:"parent_id,omitempty"`
}

type DelegationChain struct {
	RootDelegation  Delegation   `json:"root"`
	Children        []Delegation `json:"children,omitempty"`
	Depth           int          `json:"depth"`
	TotalDurationMs int64        `json:"total_duration_ms"`
}

type RecordDelegationRequest struct {
	ID               string `json:"id"`
	CallerAgentID    string `json:"caller_agent_id"`
	CalleeAgentID    string `json:"callee_agent_id"`
	CallerInstanceID string `json:"caller_instance_id"`
	CalleeInstanceID string `json:"callee_instance_id"`
	TaskType         string `json:"task_type"`
	UserID           string `json:"user_id"`
	ParentID         string `json:"parent_id,omitempty"`
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
	var d Delegation
	err := c.do(ctx, "GET", "/api/v1/a2a/delegations/"+url.PathEscape(id), nil, &d)
	return &d, err
}

// UpdateDelegation updates the status of a delegation.
func (c *Client) UpdateDelegation(ctx context.Context, id string, req *UpdateDelegationRequest) (*Delegation, error) {
	var d Delegation
	err := c.do(ctx, "PATCH", "/api/v1/a2a/delegations/"+url.PathEscape(id), req, &d)
	return &d, err
}

// GetDelegationChain returns the full call chain for a delegation.
func (c *Client) GetDelegationChain(ctx context.Context, id string) (*DelegationChain, error) {
	var chain DelegationChain
	err := c.do(ctx, "GET", "/api/v1/a2a/delegations/"+url.PathEscape(id)+"/chain", nil, &chain)
	return &chain, err
}

// --- Dashboard ---

type DashboardStats struct {
	TotalInstances   int     `json:"total_instances"`
	RunningInstances int     `json:"running_instances"`
	RegisteredAgents int     `json:"registered_agents"`
	ActivePlanes     int     `json:"active_planes"`
	MCPlexInstances  int     `json:"mcplex_instances"`
	A2APlexInstances int     `json:"a2aplex_instances"`
	LLMPlexInstances int     `json:"llmplex_instances"`
	DailyCostUSD     float64 `json:"daily_cost_usd"`
	DailyTokens      int64   `json:"daily_tokens"`
	DailyRequests    int64   `json:"daily_requests"`
	ToolCalls        int64   `json:"tool_calls_24h"`
	A2ADelegations   int64   `json:"a2a_delegations_24h"`
	PolicyDenials    int64   `json:"policy_denials_24h"`
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

// RecordPolicyDenial records a new policy denial.
func (c *Client) RecordPolicyDenial(ctx context.Context, d *PolicyDenial) (*PolicyDenial, error) {
	var out PolicyDenial
	err := c.do(ctx, "POST", "/api/v1/dashboard/denials", d, &out)
	return &out, err
}

// --- Auth / User Scopes ---

type UserScopes struct {
	UserID string              `json:"user_id"`
	Scopes map[string][]string `json:"scopes"` // plane → scopes
}

// GetUserScopes returns the Dimension B scopes for a user.
func (c *Client) GetUserScopes(ctx context.Context, userID string) (*UserScopes, error) {
	var s UserScopes
	err := c.do(ctx, "GET", "/auth/users/"+url.PathEscape(userID)+"/scopes", nil, &s)
	return &s, err
}

// SetUserScopes sets the Dimension B scopes for a user.
func (c *Client) SetUserScopes(ctx context.Context, userID string, scopes []string) (*UserScopes, error) {
	body := map[string][]string{"scopes": scopes}
	var s UserScopes
	err := c.do(ctx, "PUT", "/auth/users/"+url.PathEscape(userID)+"/scopes", body, &s)
	return &s, err
}

// Health checks the server health.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, "GET", "/healthz", nil, nil)
}

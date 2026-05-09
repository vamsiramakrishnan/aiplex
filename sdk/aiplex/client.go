// Package aiplex provides a Go SDK for the AIPlex control plane API.
//
// Usage:
//
//	c := aiplex.NewClient("https://aiplex.example.com")
//	c.SetToken("eyJhbGci...")
//
//	instances, err := c.ListInstances(ctx, &aiplex.ListInstancesOpts{Kind: "tool"})
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

// --- Capability primitive ---

// Cap is one entry in a JWT's caps claim. It grants the bearer permission to
// perform a subset of actions on a single capability URI.
type Cap struct {
	URI         string         `json:"uri"`
	Actions     []string       `json:"actions,omitempty"`
	Constraints map[string]any `json:"constraints,omitempty"`
	NotBefore   int64          `json:"nbf,omitempty"`
	NotAfter    int64          `json:"exp,omitempty"`
}

// Capability is a typed, addressable, governable unit of agent action.
type Capability struct {
	URI          string         `json:"uri"`
	Kind         string         `json:"kind"`
	Name         string         `json:"name"`
	Version      string         `json:"version"`
	Provider     string         `json:"provider,omitempty"`
	Actions      []string       `json:"actions,omitempty"`
	Description  string         `json:"description,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
	Repository   string         `json:"repository,omitempty"`
	Image        string         `json:"image,omitempty"`
}

// --- Catalog ---

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
	ID           string       `json:"id"`
	Source       string       `json:"source"`
	Kind         string       `json:"kind"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	Image        string       `json:"image,omitempty"`
	Version      string       `json:"version,omitempty"`
	Capabilities []Capability `json:"capabilities,omitempty"`
	ModelID      string       `json:"model_id,omitempty"`
	Provider     string       `json:"provider,omitempty"`
	ModelTags    []string     `json:"model_tags,omitempty"`
	Category     string       `json:"category"`
	Verified     bool         `json:"verified"`
	Tags         []string     `json:"tags,omitempty"`
	Pricing      *Pricing     `json:"pricing,omitempty"`
}

type Pricing struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// ListCatalogOpts are options for listing catalog templates.
type ListCatalogOpts struct {
	Kind string
	Page int
}

// ListCatalog returns a paginated catalog listing.
func (c *Client) ListCatalog(ctx context.Context, opts *ListCatalogOpts) (*CatalogPage, error) {
	q := url.Values{}
	if opts != nil {
		if opts.Kind != "" {
			q.Set("kind", opts.Kind)
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
	ID           string         `json:"id"`
	Kind         string         `json:"kind"`
	TemplateID   string         `json:"template_id"`
	Owner        string         `json:"owner"`
	Namespace    string         `json:"namespace"`
	SpiffeID     string         `json:"spiffe_id,omitempty"`
	Capabilities []Cap          `json:"capabilities"`
	Config       map[string]any `json:"config,omitempty"`
	Status       string         `json:"status"`
	Replicas     int            `json:"replicas"`
	DisplayName  string         `json:"display_name,omitempty"`
	DeployedAt   time.Time      `json:"deployed_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeployedBy   string         `json:"deployed_by"`
}

type DeployRequest struct {
	Kind        string         `json:"kind"`
	TemplateID  string         `json:"template_id"`
	Config      map[string]any `json:"config,omitempty"`
	DisplayName string         `json:"display_name,omitempty"`
}

type DeployHistory struct {
	ID          string         `json:"id"`
	InstanceID  string         `json:"instance_id"`
	Action      string         `json:"action"`
	Kind        string         `json:"kind"`
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
	Kind string
}

// ListInstances returns all instances, optionally filtered by capability kind.
func (c *Client) ListInstances(ctx context.Context, opts *ListInstancesOpts) ([]Instance, error) {
	path := "/api/v1/instances"
	if opts != nil && opts.Kind != "" {
		path += "?kind=" + url.QueryEscape(opts.Kind)
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
	ClientID     string    `json:"client_id"`
	DisplayName  string    `json:"display_name"`
	Description  string    `json:"description,omitempty"`
	AuthMethod   string    `json:"auth_method"`
	GrantTypes   []string  `json:"grant_types"`
	AllowedCaps  []Cap     `json:"allowed_caps"`
	WIFPrincipal string    `json:"wif_principal,omitempty"`
	SpiffeID     string    `json:"spiffe_id,omitempty"`
	RedirectURIs []string  `json:"redirect_uris,omitempty"`
	RegisteredAt time.Time `json:"registered_at"`
	RegisteredBy string    `json:"registered_by"`
	Status       string    `json:"status"`
}

type CapabilityInfo struct {
	URI         string   `json:"uri"`
	Actions     []string `json:"actions,omitempty"`
	Description string   `json:"description"`
}

type AgentPermissions struct {
	AgentID string                      `json:"agent_id"`
	Ceiling map[string][]CapabilityInfo `json:"ceiling"`
}

type RegisterAgentRequest struct {
	ClientID     string   `json:"client_id"`
	DisplayName  string   `json:"display_name"`
	Description  string   `json:"description,omitempty"`
	AuthMethod   string   `json:"auth_method"`
	GrantTypes   []string `json:"grant_types"`
	AllowedCaps  []Cap    `json:"allowed_caps"`
	WIFPrincipal string   `json:"wif_principal,omitempty"`
	RedirectURIs []string `json:"redirect_uris,omitempty"`
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

// GetAgentPermissions returns the cross-kind permission view for an agent.
func (c *Client) GetAgentPermissions(ctx context.Context, clientID string) (*AgentPermissions, error) {
	var p AgentPermissions
	err := c.do(ctx, "GET", "/api/v1/agents/"+url.PathEscape(clientID)+"/permissions", nil, &p)
	return &p, err
}

// --- LLM (kind=model) ---

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

func (c *Client) ListLLMRoutes(ctx context.Context) ([]LLMRouteConfig, error) {
	var list []LLMRouteConfig
	err := c.do(ctx, "GET", "/api/v1/llm/routes", nil, &list)
	return list, err
}

func (c *Client) GetLLMRoute(ctx context.Context, modelID string) (*LLMRouteConfig, error) {
	var rc LLMRouteConfig
	err := c.do(ctx, "GET", "/api/v1/llm/routes/"+url.PathEscape(modelID), nil, &rc)
	return &rc, err
}

func (c *Client) PutLLMRoute(ctx context.Context, modelID string, rc *LLMRouteConfig) (*LLMRouteConfig, error) {
	var out LLMRouteConfig
	err := c.do(ctx, "PUT", "/api/v1/llm/routes/"+url.PathEscape(modelID), rc, &out)
	return &out, err
}

func (c *Client) DeleteLLMRoute(ctx context.Context, modelID string) error {
	return c.do(ctx, "DELETE", "/api/v1/llm/routes/"+url.PathEscape(modelID), nil, nil)
}

func (c *Client) ListProviders(ctx context.Context) ([]ProviderConfig, error) {
	var list []ProviderConfig
	err := c.do(ctx, "GET", "/api/v1/llm/providers", nil, &list)
	return list, err
}

func (c *Client) PutProvider(ctx context.Context, provider string, cfg *ProviderConfig) (*ProviderConfig, error) {
	var out ProviderConfig
	err := c.do(ctx, "PUT", "/api/v1/llm/providers/"+url.PathEscape(provider), cfg, &out)
	return &out, err
}

func (c *Client) RecordUsage(ctx context.Context, rec *UsageRecord) (*UsageRecord, error) {
	var out UsageRecord
	err := c.do(ctx, "POST", "/api/v1/llm/usage", rec, &out)
	return &out, err
}

func (c *Client) GetUsageSummary(ctx context.Context, period string) (*UsageSummary, error) {
	var s UsageSummary
	err := c.do(ctx, "GET", "/api/v1/llm/usage/summary?period="+url.QueryEscape(period), nil, &s)
	return &s, err
}

// --- A2A (kind=task) ---

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

func (c *Client) GetAgentCard(ctx context.Context, instanceID string) (*AgentCard, error) {
	var card AgentCard
	err := c.do(ctx, "GET", "/a2a/"+url.PathEscape(instanceID)+"/.well-known/agent.json", nil, &card)
	return &card, err
}

func (c *Client) ListAgentCards(ctx context.Context) ([]map[string]any, error) {
	var list []map[string]any
	err := c.do(ctx, "GET", "/api/v1/a2a/agents", nil, &list)
	return list, err
}

func (c *Client) RecordDelegation(ctx context.Context, req *RecordDelegationRequest) (*Delegation, error) {
	var d Delegation
	err := c.do(ctx, "POST", "/api/v1/a2a/delegations", req, &d)
	return &d, err
}

func (c *Client) ListDelegations(ctx context.Context) ([]Delegation, error) {
	var list []Delegation
	err := c.do(ctx, "GET", "/api/v1/a2a/delegations", nil, &list)
	return list, err
}

func (c *Client) GetDelegation(ctx context.Context, id string) (*Delegation, error) {
	var d Delegation
	err := c.do(ctx, "GET", "/api/v1/a2a/delegations/"+url.PathEscape(id), nil, &d)
	return &d, err
}

func (c *Client) UpdateDelegation(ctx context.Context, id string, req *UpdateDelegationRequest) (*Delegation, error) {
	var d Delegation
	err := c.do(ctx, "PATCH", "/api/v1/a2a/delegations/"+url.PathEscape(id), req, &d)
	return &d, err
}

func (c *Client) GetDelegationChain(ctx context.Context, id string) (*DelegationChain, error) {
	var chain DelegationChain
	err := c.do(ctx, "GET", "/api/v1/a2a/delegations/"+url.PathEscape(id)+"/chain", nil, &chain)
	return &chain, err
}

// --- Skills (kind=skill) ---

type SkillServerSummary struct {
	InstanceID  string   `json:"instance_id"`
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	SkillBundle string   `json:"skill_bundle,omitempty"`
	Skills      []string `json:"skills"`
	Status      string   `json:"status"`
}

type SkillInvocation struct {
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

type RecordSkillInvocationRequest struct {
	AgentID    string `json:"agent_id"`
	InstanceID string `json:"instance_id"`
	SkillName  string `json:"skill_name"`
	UserID     string `json:"user_id,omitempty"`
	Status     string `json:"status,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
	TraceID    string `json:"trace_id,omitempty"`
}

func (c *Client) ListSkillServers(ctx context.Context) ([]SkillServerSummary, error) {
	var list []SkillServerSummary
	err := c.do(ctx, "GET", "/api/v1/skills/servers", nil, &list)
	return list, err
}

func (c *Client) RecordSkillInvocation(ctx context.Context, req *RecordSkillInvocationRequest) (*SkillInvocation, error) {
	var inv SkillInvocation
	err := c.do(ctx, "POST", "/api/v1/skills/invocations", req, &inv)
	return &inv, err
}

func (c *Client) ListSkillInvocations(ctx context.Context, agentID, skillName string) ([]SkillInvocation, error) {
	q := url.Values{}
	if agentID != "" {
		q.Set("agent_id", agentID)
	}
	if skillName != "" {
		q.Set("skill", skillName)
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
	TotalInstances   int            `json:"total_instances"`
	RunningInstances int            `json:"running_instances"`
	RegisteredAgents int            `json:"registered_agents"`
	ActiveKinds      int            `json:"active_kinds"`
	InstancesByKind  map[string]int `json:"instances_by_kind"`
	DailyCostUSD     float64        `json:"daily_cost_usd"`
	DailyTokens      int64          `json:"daily_tokens"`
	DailyRequests    int64          `json:"daily_requests"`
	ToolCalls        int64          `json:"tool_calls_24h"`
	A2ADelegations   int64          `json:"a2a_delegations_24h"`
	PolicyDenials    int64          `json:"policy_denials_24h"`
}

type PolicyDenial struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Kind      string    `json:"kind"`
	AgentID   string    `json:"agent_id"`
	UserID    string    `json:"user_id"`
	CapURI    string    `json:"cap_uri"`
	Action    string    `json:"action"`
	Reason    string    `json:"reason"`
	RequestID string    `json:"request_id"`
}

func (c *Client) GetDashboardStats(ctx context.Context) (*DashboardStats, error) {
	var s DashboardStats
	err := c.do(ctx, "GET", "/api/v1/dashboard/stats", nil, &s)
	return &s, err
}

func (c *Client) ListPolicyDenials(ctx context.Context) ([]PolicyDenial, error) {
	var list []PolicyDenial
	err := c.do(ctx, "GET", "/api/v1/dashboard/denials", nil, &list)
	return list, err
}

func (c *Client) RecordPolicyDenial(ctx context.Context, d *PolicyDenial) (*PolicyDenial, error) {
	var out PolicyDenial
	err := c.do(ctx, "POST", "/api/v1/dashboard/denials", d, &out)
	return &out, err
}

// --- Memory (kind=memory) ---

// MemoryValue is the on-the-wire shape of a stored memory entry.
type MemoryValue struct {
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
	UpdatedAt time.Time      `json:"updated_at,omitempty"`
	Embedding []float32      `json:"embedding,omitempty"`
}

// MemoryHit is a single search result.
type MemoryHit struct {
	Key   string      `json:"key"`
	Value MemoryValue `json:"value"`
	Score float32     `json:"score,omitempty"`
}

// MemoryListing pages keys by prefix.
type MemoryListing struct {
	Keys     []string `json:"keys"`
	NextPage string   `json:"next_page,omitempty"`
}

// MemoryQuery selects entries for search.
type MemoryQuery struct {
	Embedding []float32      `json:"embedding,omitempty"`
	Text      string         `json:"text,omitempty"`
	TopK      int            `json:"top_k,omitempty"`
	Filter    map[string]any `json:"filter,omitempty"`
}

// MemoryNamespace is a typed handle for a single cap://memory/... URI.
// Callers receive one via Client.Memory(uri) and use it like:
//
//	mem := client.Memory("cap://memory/students/alice/profile@v1")
//	mem.Write(ctx, "lesson-42", aiplex.MemoryValue{Data: map[string]any{"score": 88}})
//	hits, _ := mem.Search(ctx, aiplex.MemoryQuery{Embedding: emb, TopK: 5})
type MemoryNamespace struct {
	c   *Client
	uri string
}

// Memory returns a typed namespace handle. uri must be a cap://memory/... URI;
// callers can substitute path templates ahead of time (e.g. swap "{user}").
func (c *Client) Memory(uri string) *MemoryNamespace {
	return &MemoryNamespace{c: c, uri: uri}
}

// pathBase returns the request path prefix derived from the namespace URI.
// e.g. cap://memory/students/alice/profile@v1 → /cap/memory/students/alice/profile@v1
func (m *MemoryNamespace) pathBase() string {
	rest := strings.TrimPrefix(m.uri, "cap://memory/")
	return "/cap/memory/" + rest
}

// Read fetches a value by key. Returns *aiplex.Error with StatusCode=404 if absent.
func (m *MemoryNamespace) Read(ctx context.Context, key string) (*MemoryValue, error) {
	var v MemoryValue
	err := m.c.do(ctx, "GET", m.pathBase()+"/"+url.PathEscape(key), nil, &v)
	return &v, err
}

// Write stores a value at key. Set ifNoneMatch=true for create-only semantics
// (returns 409 if the key already exists).
func (m *MemoryNamespace) Write(ctx context.Context, key string, v MemoryValue, ifNoneMatch bool) error {
	path := m.pathBase() + "/" + url.PathEscape(key)
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("aiplex: marshal value: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", m.c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", m.c.userAgent)
	if m.c.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.c.token)
	}
	if ifNoneMatch {
		req.Header.Set("If-None-Match", "*")
	}
	resp, err := m.c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("aiplex: write memory: %w", err)
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
	return nil
}

// Delete removes a key from the namespace.
func (m *MemoryNamespace) Delete(ctx context.Context, key string) error {
	return m.c.do(ctx, "DELETE", m.pathBase()+"/"+url.PathEscape(key), nil, nil)
}

// Search runs a vector / text query against the namespace.
func (m *MemoryNamespace) Search(ctx context.Context, q MemoryQuery) ([]MemoryHit, error) {
	var out struct {
		Hits []MemoryHit `json:"hits"`
	}
	err := m.c.do(ctx, "POST", m.pathBase()+"/_search", q, &out)
	return out.Hits, err
}

// List enumerates keys, optionally filtered by prefix and paginated.
func (m *MemoryNamespace) List(ctx context.Context, prefix, cursor string, limit int) (*MemoryListing, error) {
	q := url.Values{}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := m.pathBase() + "/"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out MemoryListing
	err := m.c.do(ctx, "GET", path, nil, &out)
	return &out, err
}

// --- Workflow (kind=workflow) ---

// WorkflowRun mirrors the Run struct emitted by the workflow executor.
type WorkflowRun struct {
	ID          string                  `json:"id"`
	WorkflowURI string                  `json:"workflow_uri"`
	Caller      string                  `json:"caller"`
	StartedAt   time.Time               `json:"started_at"`
	FinishedAt  time.Time               `json:"finished_at,omitempty"`
	Status      string                  `json:"status"`
	Inputs      map[string]any          `json:"inputs,omitempty"`
	Steps       []WorkflowStepResult    `json:"steps,omitempty"`
	Outputs     map[string]any          `json:"outputs,omitempty"`
	Error       string                  `json:"error,omitempty"`
}

// WorkflowStepResult records one step of a workflow run.
type WorkflowStepResult struct {
	StepID     string         `json:"step_id"`
	Cap        string         `json:"cap"`
	Action     string         `json:"action"`
	StartedAt  time.Time      `json:"started_at"`
	DurationMs int64          `json:"duration_ms"`
	Status     string         `json:"status"`
	Output     map[string]any `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// WorkflowHandle is a typed reference to a deployed workflow capability.
// Callers obtain one via Client.Workflow(uri).
type WorkflowHandle struct {
	c   *Client
	uri string
}

// Workflow returns a typed handle for a cap://workflow/... URI.
func (c *Client) Workflow(uri string) *WorkflowHandle {
	return &WorkflowHandle{c: c, uri: uri}
}

// Run executes the workflow with the given inputs and returns the recorded
// WorkflowRun. The token attached to the client is forwarded to each step
// the workflow invokes — every cap call shares one delegation chain.
func (w *WorkflowHandle) Run(ctx context.Context, inputs map[string]any) (*WorkflowRun, error) {
	rest := strings.TrimPrefix(w.uri, "cap://workflow/")
	path := "/cap/workflow/" + rest + "/_invoke"
	body := map[string]any{"input": inputs}
	var run WorkflowRun
	err := w.c.do(ctx, "POST", path, body, &run)
	return &run, err
}

// Describe returns the workflow's spec (without running it).
func (w *WorkflowHandle) Describe(ctx context.Context) (map[string]any, error) {
	rest := strings.TrimPrefix(w.uri, "cap://workflow/")
	path := "/cap/workflow/" + rest + "/_describe"
	var spec map[string]any
	err := w.c.do(ctx, "GET", path, nil, &spec)
	return spec, err
}

// GetRun fetches a previously-recorded run by ID. Useful for polling
// long-running workflows the caller didn't await synchronously.
func (c *Client) GetRun(ctx context.Context, runID string) (*WorkflowRun, error) {
	var run WorkflowRun
	err := c.do(ctx, "GET", "/cap/workflow/runs/"+url.PathEscape(runID), nil, &run)
	return &run, err
}

// --- Agent (kind=agent) ---

// AgentHandle is a typed reference to a hosted agent runtime exposed as a cap.
type AgentHandle struct {
	c   *Client
	uri string
}

// Agent returns a typed handle for a cap://agent/... URI. The agent runtime
// itself (ADK, LangGraph, Letta, custom HTTP) is fronted by AIPlex; calling
// .Invoke() routes through the same gateway as every other cap, so the
// caller's delegation, audit, and revocation properties apply uniformly.
func (c *Client) Agent(uri string) *AgentHandle {
	return &AgentHandle{c: c, uri: uri}
}

// Invoke calls the agent. The shape of input/output is defined by the agent
// runtime; AIPlex passes them through verbatim.
func (a *AgentHandle) Invoke(ctx context.Context, input map[string]any) (map[string]any, error) {
	rest := strings.TrimPrefix(a.uri, "cap://agent/")
	path := "/cap/agent/" + rest + "/_invoke"
	body := map[string]any{"input": input}
	var out map[string]any
	err := a.c.do(ctx, "POST", path, body, &out)
	return out, err
}

// --- Auth / User Caps (Dimension B) ---

type UserCaps struct {
	UserID string           `json:"user_id"`
	Caps   []Cap            `json:"caps"`
	ByKind map[string][]Cap `json:"by_kind"`
}

func (c *Client) GetUserCaps(ctx context.Context, userID string) (*UserCaps, error) {
	var s UserCaps
	err := c.do(ctx, "GET", "/auth/users/"+url.PathEscape(userID)+"/caps", nil, &s)
	return &s, err
}

func (c *Client) SetUserCaps(ctx context.Context, userID string, caps []Cap) error {
	body := map[string]any{"caps": caps}
	return c.do(ctx, "PUT", "/auth/users/"+url.PathEscape(userID)+"/caps", body, nil)
}

// Health checks the server health.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, "GET", "/healthz", nil, nil)
}

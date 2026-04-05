package models

import "time"

// LLMRouteConfig defines routing rules for a model — weights, failover, budgets.
type LLMRouteConfig struct {
	ID          string          `json:"id"`
	ModelID     string          `json:"model_id"`
	Owner       string          `json:"owner"`
	Backends    []LLMBackend    `json:"backends"`
	Fallbacks   []string        `json:"fallbacks,omitempty"` // ordered fallback model IDs
	CacheTTL    int             `json:"cache_ttl_seconds,omitempty"`
	Budget      *UsageBudget    `json:"budget,omitempty"`
	RateLimit   *ModelRateLimit `json:"rate_limit,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// LLMBackend is a weighted backend for load balancing across providers.
type LLMBackend struct {
	Provider  string `json:"provider"`  // google, anthropic, openai, bedrock, ollama
	ModelID   string `json:"model_id"`  // provider-specific model ID
	Weight    int    `json:"weight"`    // load balancing weight (0-100)
	Enabled   bool   `json:"enabled"`
	SecretRef string `json:"secret_ref"` // Secret Manager key for API key
}

// UsageBudget sets cost limits per time period.
type UsageBudget struct {
	MaxDailyCostUSD   float64 `json:"max_daily_cost_usd,omitempty"`
	MaxMonthlyCostUSD float64 `json:"max_monthly_cost_usd,omitempty"`
	MaxDailyTokens    int64   `json:"max_daily_tokens,omitempty"`
	AlertThreshold    float64 `json:"alert_threshold_pct,omitempty"` // alert at N% of budget
}

// ModelRateLimit configures per-model rate limiting.
type ModelRateLimit struct {
	RequestsPerMinute int `json:"requests_per_minute"`
	TokensPerMinute   int `json:"tokens_per_minute,omitempty"`
}

// UsageRecord tracks token consumption for a single LLM call.
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

// UsageSummary aggregates usage over a time period.
type UsageSummary struct {
	ModelID      string  `json:"model_id,omitempty"`
	Provider     string  `json:"provider,omitempty"`
	AgentID      string  `json:"agent_id,omitempty"`
	Period       string  `json:"period"` // "day", "week", "month"
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	RequestCount int64   `json:"request_count"`
	CacheHits    int64   `json:"cache_hits"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
}

// ProviderConfig holds provider-level settings.
type ProviderConfig struct {
	Provider    string `json:"provider"`
	DisplayName string `json:"display_name"`
	BaseURL     string `json:"base_url,omitempty"`
	SecretRef   string `json:"secret_ref"` // Secret Manager reference for API key
	Enabled     bool   `json:"enabled"`
	Region      string `json:"region,omitempty"`     // for Bedrock
	ProjectID   string `json:"project_id,omitempty"` // for Vertex AI
}

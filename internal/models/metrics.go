package models

import "time"

// MetricPoint is a single data point for dashboard charts.
type MetricPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// DashboardStats provides the unified overview for the Console dashboard.
type DashboardStats struct {
	// Counts
	TotalInstances    int `json:"total_instances"`
	RunningInstances  int `json:"running_instances"`
	RegisteredAgents  int `json:"registered_agents"`
	ActivePlanes      int `json:"active_planes"`

	// Per-plane
	MCPlexInstances  int `json:"mcplex_instances"`
	A2APlexInstances int `json:"a2aplex_instances"`
	LLMPlexInstances int `json:"llmplex_instances"`

	// LLM costs (last 24h)
	DailyCostUSD     float64 `json:"daily_cost_usd"`
	DailyTokens      int64   `json:"daily_tokens"`
	DailyRequests    int64   `json:"daily_requests"`

	// Activity (last 24h)
	ToolCalls        int64 `json:"tool_calls_24h"`
	A2ADelegations   int64 `json:"a2a_delegations_24h"`
	PolicyDenials    int64 `json:"policy_denials_24h"`
}

// PolicyDenial records a single OPA/authz denial event.
type PolicyDenial struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Plane     string    `json:"plane"`
	AgentID   string    `json:"agent_id"`
	UserID    string    `json:"user_id"`
	Action    string    `json:"action"`    // e.g. "tools/call:search" or "llm:model:gpt-4.1"
	Scope     string    `json:"scope"`     // the scope that was missing
	Reason    string    `json:"reason"`    // "scope_missing", "budget_exceeded", "rate_limited"
	RequestID string    `json:"request_id"`
}

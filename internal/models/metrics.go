package models

import (
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// MetricPoint is a single data point for dashboard charts.
type MetricPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// DashboardStats provides the unified overview for the Console dashboard.
type DashboardStats struct {
	// Counts
	TotalInstances   int                     `json:"total_instances"`
	RunningInstances int                     `json:"running_instances"`
	RegisteredAgents int                     `json:"registered_agents"`
	ActiveKinds      int                     `json:"active_kinds"`
	InstancesByKind  map[capability.Kind]int `json:"instances_by_kind"`

	// LLM costs (last 24h)
	DailyCostUSD  float64 `json:"daily_cost_usd"`
	DailyTokens   int64   `json:"daily_tokens"`
	DailyRequests int64   `json:"daily_requests"`

	// Activity (last 24h) — kind-specific roll-ups.
	ToolCalls      int64 `json:"tool_calls_24h"`
	A2ADelegations int64 `json:"a2a_delegations_24h"`
	PolicyDenials  int64 `json:"policy_denials_24h"`
}

// PolicyDenial records a single OPA/authz denial event.
type PolicyDenial struct {
	ID        string          `json:"id"`
	Timestamp time.Time       `json:"timestamp"`
	Kind      capability.Kind `json:"kind"`
	AgentID   string          `json:"agent_id"`
	UserID    string          `json:"user_id"`
	CapURI    string          `json:"cap_uri"` // the capability that was requested
	Action    string          `json:"action"`  // requested action
	Reason    string          `json:"reason"`  // "cap_missing", "budget_exceeded", "rate_limited"
	RequestID string          `json:"request_id"`
}

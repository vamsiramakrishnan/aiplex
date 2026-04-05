package models

import "time"

// AgentCard is the A2A Agent Card specification for a deployed A2A agent.
// Served at /.well-known/agent.json for each agent.
type AgentCard struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	URL          string            `json:"url"`
	Version      string            `json:"version,omitempty"`
	TaskTypes    []TaskTypeInfo    `json:"task_types"`
	Capabilities []string          `json:"capabilities,omitempty"`
	AuthSchemes  []AuthSchemeInfo  `json:"auth_schemes,omitempty"`
	Metadata     map[string]any    `json:"metadata,omitempty"`
}

// TaskTypeInfo describes a task type an A2A agent supports.
type TaskTypeInfo struct {
	Type        string         `json:"type"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
}

// AuthSchemeInfo describes an auth scheme an A2A agent supports.
type AuthSchemeInfo struct {
	Scheme string `json:"scheme"` // "bearer", "oauth2", "spiffe"
	Config map[string]any `json:"config,omitempty"`
}

// Delegation records an agent-to-agent task delegation.
type Delegation struct {
	ID              string    `json:"id"`
	CallerAgentID   string    `json:"caller_agent_id"`
	CalleeAgentID   string    `json:"callee_agent_id"`
	CallerInstanceID string   `json:"caller_instance_id"`
	CalleeInstanceID string   `json:"callee_instance_id"`
	TaskType        string    `json:"task_type"`
	Status          string    `json:"status"` // pending, running, completed, failed
	UserID          string    `json:"user_id"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	DurationMs      int64     `json:"duration_ms,omitempty"`
	Error           string    `json:"error,omitempty"`
	ParentID        string    `json:"parent_id,omitempty"` // for nested delegations
}

// DelegationChain represents a full call chain from user through agents.
type DelegationChain struct {
	RootDelegation Delegation   `json:"root"`
	Children       []Delegation `json:"children,omitempty"`
	Depth          int          `json:"depth"`
	TotalDurationMs int64      `json:"total_duration_ms"`
}

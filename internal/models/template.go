package models

import "time"

// Template represents a deployable catalog entry (MCP server, A2A agent, or LLM provider).
type Template struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Plane       Plane  `json:"plane"`
	Name        string `json:"name"`
	Description string `json:"description"`

	// MCPlex / A2APlex
	Image      string `json:"image,omitempty"`
	Repository string `json:"repository,omitempty"`
	Version    string `json:"version,omitempty"`

	// MCPlex
	Tools []ToolInfo `json:"tools,omitempty"`

	// A2APlex
	TaskTypes []string       `json:"task_types,omitempty"`
	AgentCard map[string]any `json:"agent_card,omitempty"`

	// LLMPlex
	ModelID         string   `json:"model_id,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
	FallbackModelID string   `json:"fallback_model_id,omitempty"`

	// Common
	Category       string            `json:"category"`
	ConfigSchema   map[string]any    `json:"config_schema,omitempty"`
	ResourceLimits *ResourceLimits   `json:"resource_limits,omitempty"`
	Verified       bool              `json:"verified"`
	Tags           []string          `json:"tags,omitempty"`
	Pricing        *Pricing          `json:"pricing,omitempty"`
	CreatedAt      time.Time         `json:"created_at,omitempty"`
	UpdatedAt      time.Time         `json:"updated_at,omitempty"`
}

// ToolInfo describes a single MCP tool.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ResourceLimits specifies CPU/memory for a deployment.
type ResourceLimits struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

// Pricing holds per-million-token pricing for LLM providers.
type Pricing struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

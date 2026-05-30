package models

import "time"

// Plane represents one of the three AIPlex planes.
type Plane string

const (
	PlaneMCPlex     Plane = "mcplex"
	PlaneA2APlex    Plane = "a2aplex"
	PlaneLLMPlex    Plane = "llmplex"
	PlaneSkillsPlex Plane = "skillsplex"
)

// InstanceStatus tracks the lifecycle state of a deployed instance.
type InstanceStatus string

const (
	StatusProvisioning InstanceStatus = "provisioning"
	StatusRunning      InstanceStatus = "running"
	StatusDegraded     InstanceStatus = "degraded"
	StatusStopped      InstanceStatus = "stopped"
	StatusFailed       InstanceStatus = "failed"
	StatusTerminated   InstanceStatus = "terminated"
)

// Instance represents a deployed MCP server, A2A agent, or LLM provider.
type Instance struct {
	ID              string            `json:"id"`
	Plane           Plane             `json:"plane"`
	TemplateID      string            `json:"template_id"`
	Owner           string            `json:"owner"`
	Namespace       string            `json:"namespace"`
	SpiffeID        string            `json:"spiffe_id,omitempty"`
	Scopes          []string          `json:"scopes"`
	Config          map[string]any    `json:"config,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Status          InstanceStatus    `json:"status"`
	Replicas        int               `json:"replicas"`
	DisplayName     string            `json:"display_name,omitempty"`
	ResourceVersion int64             `json:"resource_version"`
	DeployedAt      time.Time         `json:"deployed_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	DeployedBy      string            `json:"deployed_by"`
	Health          *HealthStatus     `json:"health,omitempty"`
	// Runtime describes how the agent inside this Instance executes —
	// {Engine: "none"} for the v1 path, {Engine: "tape", ...} for durable
	// agents backed by the Tape substrate. See runtime.go. The field is a
	// value (not a pointer) so consumers never branch on nil; the
	// zero-value (which Validate accepts) means "no durable runtime."
	Runtime RuntimeConfig `json:"runtime"`
}

// HealthStatus captures the last health check result.
type HealthStatus struct {
	LastCheck time.Time `json:"last_check"`
	Status    string    `json:"status"`
	LatencyMs int       `json:"latency_ms"`
}

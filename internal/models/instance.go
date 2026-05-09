package models

import (
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
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

// Instance represents a deployed capability provider — the workload behind one
// or more capability URIs. Tools, A2A agents, skill servers, model proxies and
// memory namespaces are all instances.
type Instance struct {
	ID              string             `json:"id"`
	Kind            capability.Kind    `json:"kind"`
	TemplateID      string             `json:"template_id"`
	Owner           string             `json:"owner"`
	Namespace       string             `json:"namespace"`
	SpiffeID        string             `json:"spiffe_id,omitempty"`
	Capabilities    capability.CapSet  `json:"capabilities"`
	Config          map[string]any     `json:"config,omitempty"`
	Labels          map[string]string  `json:"labels,omitempty"`
	Status          InstanceStatus     `json:"status"`
	Replicas        int                `json:"replicas"`
	DisplayName     string             `json:"display_name,omitempty"`
	ResourceVersion int64              `json:"resource_version"`
	DeployedAt      time.Time          `json:"deployed_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	DeployedBy      string             `json:"deployed_by"`
	Health          *HealthStatus      `json:"health,omitempty"`
}

// HealthStatus captures the last health check result.
type HealthStatus struct {
	LastCheck time.Time `json:"last_check"`
	Status    string    `json:"status"`
	LatencyMs int       `json:"latency_ms"`
}

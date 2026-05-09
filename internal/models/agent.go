package models

import (
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// Agent represents a registered OAuth client (AI agent) that can access AIPlex resources.
type Agent struct {
	ClientID    string   `json:"client_id"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description,omitempty"`
	AuthMethod  string   `json:"auth_method"` // client_credentials, authorization_code, device_code
	GrantTypes  []string `json:"grant_types"`

	// Dimension A: agent ceiling — max capabilities this agent can ever request.
	AllowedCaps capability.CapSet `json:"allowed_caps"`

	// OAuth secret (returned only on registration, never persisted)
	ClientSecret string `json:"client_secret,omitempty" firestore:"-"`

	// Identity
	WIFPrincipal string   `json:"wif_principal,omitempty"`
	SpiffeID     string   `json:"spiffe_id,omitempty"`
	RedirectURIs []string `json:"redirect_uris,omitempty"`

	// Organization
	Labels map[string]string `json:"labels,omitempty"`

	// Metadata
	ResourceVersion int64     `json:"resource_version"`
	RegisteredAt    time.Time `json:"registered_at"`
	RegisteredBy    string    `json:"registered_by"`
	Status          string    `json:"status"` // active, suspended
}

// AgentPermissions provides a cross-kind view of an agent's effective ceiling.
type AgentPermissions struct {
	AgentID string                            `json:"agent_id"`
	Ceiling map[capability.Kind][]CapabilityInfo `json:"ceiling"`
}

// CapabilityInfo describes a single capability with its human-readable label.
type CapabilityInfo struct {
	URI         string   `json:"uri"`
	Actions     []string `json:"actions,omitempty"`
	Description string   `json:"description"`
}

package models

import "time"

// Agent represents a registered OAuth client (AI agent) that can access AIPlex resources.
type Agent struct {
	ClientID    string   `json:"client_id"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description,omitempty"`
	AuthMethod  string   `json:"auth_method"` // client_credentials, authorization_code, device_code
	GrantTypes  []string `json:"grant_types"`

	// Dimension A: agent ceiling — max scopes this agent can ever request
	AllowedScopes []string `json:"allowed_scopes"`

	// Identity
	WIFPrincipal string `json:"wif_principal,omitempty"`
	SpiffeID     string `json:"spiffe_id,omitempty"`
	RedirectURIs []string `json:"redirect_uris,omitempty"`

	// Metadata
	RegisteredAt time.Time `json:"registered_at"`
	RegisteredBy string    `json:"registered_by"`
	Status       string    `json:"status"` // active, suspended
}

// AgentPermissions provides a cross-plane view of an agent's effective permissions.
type AgentPermissions struct {
	AgentID string                    `json:"agent_id"`
	Ceiling map[Plane][]ScopeInfo     `json:"ceiling"`
}

// ScopeInfo describes a single scope with its human-readable description.
type ScopeInfo struct {
	Scope       string `json:"scope"`
	Description string `json:"description"`
}

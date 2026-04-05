package models

import "time"

// IAMRole defines a named role within AIPlex with a set of default scopes
// and permissions. Roles map to groups from WIF identity providers.
type IAMRole string

const (
	RoleAdmin    IAMRole = "admin"
	RoleDeployer IAMRole = "deployer"
	RoleViewer   IAMRole = "viewer"
	RoleAgent    IAMRole = "agent" // for machine identities (WIF workload)
)

// RoleBinding maps an external identity group (from WIF) to an AIPlex role
// and a set of default scopes (Dimension B ceiling).
type RoleBinding struct {
	ID          string   `json:"id"`
	Group       string   `json:"group"`        // IdP group name (e.g. "aiplex-admins")
	Role        IAMRole  `json:"role"`          // AIPlex role
	Scopes      []string `json:"scopes"`        // Default Dimension B scopes for this group
	Description string   `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	CreatedBy   string   `json:"created_by"`
}

// WIFIdentity represents a resolved identity from a WIF token.
type WIFIdentity struct {
	Subject     string   `json:"subject"`      // google.subject
	Email       string   `json:"email"`        // attribute.email
	DisplayName string   `json:"display_name"` // google.display_name
	Groups      []string `json:"groups"`       // attribute.groups
	Domain      string   `json:"domain"`       // attribute.domain (e.g. hd claim)
	Provider    string   `json:"provider"`     // which WIF provider authenticated this user
	PoolID      string   `json:"pool_id"`      // workforce or workload pool ID
	IsWorkforce bool     `json:"is_workforce"` // true = human user, false = machine agent
}

// ResolvedAccess is the result of resolving a WIF identity against role bindings.
// It contains the effective AIPlex roles and merged Dimension B scopes.
type ResolvedAccess struct {
	Identity WIFIdentity `json:"identity"`
	Roles    []IAMRole   `json:"roles"`
	Scopes   []string    `json:"scopes"` // merged Dimension B from all matching role bindings
}

// DefaultRoleScopes defines the built-in scope patterns per role.
// These are used when a RoleBinding doesn't specify explicit scopes.
var DefaultRoleScopes = map[IAMRole][]string{
	RoleAdmin: {
		"mcp:tools:*",
		"mcp:server:*",
		"a2a:task:*",
		"a2a:agent:*",
		"llm:model:*",
		"llm:capability:*",
	},
	RoleDeployer: {
		"mcp:tools:*",
		"mcp:server:*",
		"a2a:task:*",
		"a2a:agent:*",
		"llm:model:*",
	},
	RoleViewer: {
		// Viewers can list/read but not execute — enforced at API level.
		// No tool/task/model execution scopes granted.
	},
	RoleAgent: {
		// Agent scopes are set per-agent at registration (Dimension A).
	},
}

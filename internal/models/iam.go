package models

import (
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// IAMRole defines a named role within AIPlex with a set of default capabilities
// and permissions. Roles map to groups from WIF identity providers.
type IAMRole string

const (
	RoleAdmin    IAMRole = "admin"
	RoleDeployer IAMRole = "deployer"
	RoleViewer   IAMRole = "viewer"
	RoleAgent    IAMRole = "agent" // for machine identities (WIF workload)
)

// RoleBinding maps an external identity group (from WIF) to an AIPlex role
// and a set of default capabilities (Dimension B ceiling).
type RoleBinding struct {
	ID          string            `json:"id"`
	Group       string            `json:"group"`
	Role        IAMRole           `json:"role"`
	Caps        capability.CapSet `json:"caps"`
	Description string            `json:"description,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	CreatedBy   string            `json:"created_by"`
}

// WIFIdentity represents a resolved identity from a WIF token.
type WIFIdentity struct {
	Subject     string   `json:"subject"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	Groups      []string `json:"groups"`
	Domain      string   `json:"domain"`
	Provider    string   `json:"provider"`
	PoolID      string   `json:"pool_id"`
	IsWorkforce bool     `json:"is_workforce"`
}

// ResolvedAccess is the result of resolving a WIF identity against role bindings.
type ResolvedAccess struct {
	Identity WIFIdentity       `json:"identity"`
	Roles    []IAMRole         `json:"roles"`
	Caps     capability.CapSet `json:"caps"`
}

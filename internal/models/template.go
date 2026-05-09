package models

import (
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// Template is a deployable catalog entry. It bundles a Capability spec with
// deployment metadata (image, repo, version, config schema) so the deploy
// engine has everything it needs to provision.
type Template struct {
	ID          string          `json:"id"`
	Source      string          `json:"source"`
	Kind        capability.Kind `json:"kind"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Version     string          `json:"version,omitempty"` // capability version (defaults v1)

	// The capabilities this template provides once deployed.
	// For kind=tool an entry per tool; for kind=task an entry per task type;
	// for kind=model usually a single entry; for kind=skill an entry per skill;
	// for kind=memory an entry per namespace.
	Capabilities []capability.Capability `json:"capabilities,omitempty"`

	// Deployment specifics (kinds tool/task/skill/memory).
	Image      string `json:"image,omitempty"`
	Repository string `json:"repository,omitempty"`

	// kind=task only: raw A2A Agent Card to surface in the catalog.
	AgentCard map[string]any `json:"agent_card,omitempty"`

	// kind=skill only: bundle name (a single skill server can ship many bundles).
	SkillBundle string `json:"skill_bundle,omitempty"`

	// kind=model only.
	ModelID         string                  `json:"model_id,omitempty"`
	Provider        string                  `json:"provider,omitempty"`
	ModelTags       []string                `json:"model_tags,omitempty"` // vision, code-exec, etc.
	FallbackModelID string                  `json:"fallback_model_id,omitempty"`
	Pricing         *capability.Pricing     `json:"pricing,omitempty"`

	// Common.
	Category       string                     `json:"category"`
	ConfigSchema   map[string]any             `json:"config_schema,omitempty"`
	// Config carries default config that the deploy engine forwards to the
	// instance verbatim. Workflow templates use this to ship inline `spec`
	// JSON; tool/skill templates use it for default env vars.
	Config         map[string]any             `json:"config,omitempty"`
	ResourceLimits *capability.ResourceLimits `json:"resource_limits,omitempty"`
	Verified       bool                       `json:"verified"`
	Tags           []string                   `json:"tags,omitempty"`
	CreatedAt      time.Time                  `json:"created_at,omitempty"`
	UpdatedAt      time.Time                  `json:"updated_at,omitempty"`
}

// CapURIs returns the URIs of all capabilities the template provides.
func (t *Template) CapURIs() []string {
	out := make([]string, 0, len(t.Capabilities))
	for _, c := range t.Capabilities {
		out = append(out, c.URI)
	}
	return out
}

// CapSet returns the template's capabilities as a Cap claim set granting
// every action allowed by each capability's kind. Used at deploy time to
// initialise the owner's grants.
func (t *Template) CapSet() capability.CapSet {
	out := make(capability.CapSet, 0, len(t.Capabilities))
	for _, c := range t.Capabilities {
		actions := c.Actions
		if len(actions) == 0 {
			actions = capability.MustSpec(c.Kind).Actions
		}
		out = append(out, capability.Cap{URI: c.URI, Actions: actions})
	}
	return out
}

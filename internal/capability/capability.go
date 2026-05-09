package capability

import "time"

// Capability is the resource record for a deployable, governable action target.
// It pairs a stable URI with provider, schema, attributes, and auth metadata.
type Capability struct {
	URI      string         `json:"uri"`                // canonical "cap://kind/name@version"
	Kind     Kind           `json:"kind"`               // tool|task|model|skill|memory|meta
	Name     string         `json:"name"`               // bare name without prefix
	Version  string         `json:"version"`            // v1, v1.2, latest
	Provider string         `json:"provider,omitempty"` // SPIFFE ID of the workload that serves it
	Actions  []string       `json:"actions,omitempty"`  // subset of KindSpec.Actions; empty means "all actions of the kind"
	Schema   map[string]any `json:"schema,omitempty"`   // optional JSON Schema (per action)
	Attrs    Attrs          `json:"attrs,omitempty"`
	Auth     AuthSpec       `json:"auth,omitempty"`

	// Catalog metadata — kept here so a single Source interface returns one type.
	Source      string    `json:"source,omitempty"`      // origin catalog, e.g. "official-mcp"
	Description string    `json:"description,omitempty"` // human description
	Image       string    `json:"image,omitempty"`       // container image (kinds tool/task/skill)
	Repository  string    `json:"repository,omitempty"`  // source repo URL
	Provider2   string    `json:"provider_label,omitempty"` // human label for kind=model ("google", "anthropic", …)
	Capabilities []string `json:"capabilities,omitempty"`   // freeform tags for kind=model ("vision","code-exec")
	AgentCard    map[string]any `json:"agent_card,omitempty"` // raw A2A agent card for kind=task
	ConfigSchema map[string]any `json:"config_schema,omitempty"`
	ResourceLimits *ResourceLimits `json:"resource_limits,omitempty"`
	Pricing       *Pricing `json:"pricing,omitempty"`
	Category      string   `json:"category,omitempty"`
	Verified      bool     `json:"verified,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// Attrs are policy-relevant attributes attached to a capability.
type Attrs struct {
	SideEffect      string `json:"side_effect,omitempty"`        // read|write|external
	DataClass       string `json:"data_class,omitempty"`         // public|internal|pii|regulated
	CostTier        string `json:"cost_tier,omitempty"`          // free|standard|pay-per-token
	LatencyBudgetMs int    `json:"latency_budget_ms,omitempty"`
	RetentionDays   int    `json:"retention_days,omitempty"`     // memory only
	Backend         string `json:"backend,omitempty"`            // memory only: firestore|alloydb|vertex|letta|local
}

// AuthSpec captures the auth contract of a capability.
type AuthSpec struct {
	RequiredActions []string `json:"required_actions,omitempty"` // empty = all KindSpec.Actions
	Audience        []string `json:"audience,omitempty"`
	StepUpRules     []StepUpRule `json:"step_up_rules,omitempty"`
}

// StepUpRule triggers a runtime step-up consent (see design/21).
type StepUpRule struct {
	When    string `json:"when"`    // e.g. "data_class == regulated"
	Require string `json:"require"` // e.g. "user_present_in_last_minutes(5)"
}

// ResourceLimits specifies CPU/memory for a deployment (kinds tool/task/skill).
type ResourceLimits struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

// Pricing is per-million-token pricing (kind=model).
type Pricing struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// FromURI builds a Capability shell from a URI string. Used in tests and
// constructors that don't carry the full record.
func FromURI(uri string) (*Capability, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}
	return &Capability{
		URI:     u.String(),
		Kind:    u.Kind,
		Name:    u.Name,
		Version: u.Version,
	}, nil
}

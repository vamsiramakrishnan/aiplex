package models

import "time"

// SkillInfo describes a single skill that a SkillsPlex server hosts.
// Mirrors ToolInfo (MCPlex) and TaskTypeInfo (A2APlex) for catalog/permission UX.
type SkillInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version,omitempty"`
	Triggers    []string `json:"triggers,omitempty"` // keywords that should auto-invoke
}

// Skill is the canonical bundle representation served by a skill server.
// Bundles may carry inline content (markdown body) or reference external assets.
type Skill struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	Version      string         `json:"version,omitempty"`
	Triggers     []string       `json:"triggers,omitempty"`
	Content      string         `json:"content,omitempty"`        // inline markdown body
	Scripts      []string       `json:"scripts,omitempty"`        // optional script names included in the bundle
	Metadata     map[string]any `json:"metadata,omitempty"`
	UpdatedAt    time.Time      `json:"updated_at,omitempty"`
}

// SkillInvocation records a single skill invocation by an agent — analogous to
// Delegation (A2APlex) and UsageRecord (LLMPlex).
type SkillInvocation struct {
	ID            string    `json:"id"`
	AgentID       string    `json:"agent_id"`
	InstanceID    string    `json:"instance_id"`
	SkillName     string    `json:"skill_name"`
	UserID        string    `json:"user_id,omitempty"`
	Status        string    `json:"status"` // success, failed
	StartedAt     time.Time `json:"started_at"`
	DurationMs    int64     `json:"duration_ms,omitempty"`
	Error         string    `json:"error,omitempty"`
	TraceID       string    `json:"trace_id,omitempty"`
	SpanID        string    `json:"span_id,omitempty"`
	ParentSpanID  string    `json:"parent_span_id,omitempty"`
}

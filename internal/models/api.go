package models

import "time"

// DeployRequest is the input for a deploy operation.
type DeployRequest struct {
	Plane       Plane          `json:"plane"`
	TemplateID  string         `json:"template_id"`
	Config      map[string]any `json:"config,omitempty"`
	DisplayName string         `json:"display_name,omitempty"`
}

// DeployHistory records a single deploy/undeploy/config change event.
type DeployHistory struct {
	ID          string         `json:"id"`
	InstanceID  string         `json:"instance_id"`
	Action      string         `json:"action"` // deploy, undeploy, config_update, scale, restart
	Plane       Plane          `json:"plane"`
	TemplateID  string         `json:"template_id,omitempty"`
	Owner       string         `json:"owner"`
	PerformedBy string         `json:"performed_by"`
	Config      map[string]any `json:"config,omitempty"`
	Timestamp   time.Time      `json:"timestamp"`
	DurationMs  int64          `json:"duration_ms,omitempty"`
	Success     bool           `json:"success"`
	Error       string         `json:"error,omitempty"`
}

// CatalogPage is a paginated catalog response.
type CatalogPage struct {
	Templates     []Template    `json:"templates"`
	Total         int           `json:"total"`
	Page          int           `json:"page"`
	PageSize      int           `json:"page_size"`
	SourcesFailed []SourceError `json:"sources_failed,omitempty"`
}

// SourceError reports a catalog source that failed during aggregation.
type SourceError struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

// APIError is the standard error response.
type APIError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	TraceID   string         `json:"trace_id,omitempty"`
}

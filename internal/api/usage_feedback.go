package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Feedback on usage records.
//
//	POST /api/v1/usage/records/{id}/feedback   add a judgement (append-only)
//	GET  /api/v1/usage/records/{id}/feedback   history, newest first
//
// The record's own principal or actor may judge it, and so may an operator
// (admin or deployer). Anyone else sees the record as missing. Feedback is
// stored beside the record, never in it. A later judgement supersedes an
// earlier one in the latest-wins view; the earlier one stays in the history.

// feedbackRequest is the POST body.
type feedbackRequest struct {
	// Score is required, between -1 and 1.
	Score *float64 `json:"score"`
	Label string   `json:"label,omitempty"`
	// Comment is accepted only when the operator enabled comments.
	Comment string `json:"comment,omitempty"`
}

const maxFeedbackBody = 4 << 10

// feedbackRecord resolves the tenant and the record, applying the same
// visibility as GetRecord, widened for deployers.
func (h *UsageHandler) feedbackRecord(w http.ResponseWriter, r *http.Request) (tenant string, record usageledger.Record, ok bool) {
	tenant, visibility, ok := h.scope(w, r)
	if !ok {
		return "", usageledger.Record{}, false
	}
	if h.Feedback == nil {
		Error(w, r, http.StatusServiceUnavailable, "FEEDBACK_UNAVAILABLE", "feedback is not configured")
		return "", usageledger.Record{}, false
	}
	if visibility != nil && HasRole(GetWIFAccess(r.Context()), models.RoleDeployer) {
		visibility = nil
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		id = r.PathValue("id")
	}
	if id == "" || len(id) > usageledger.MaxTextLength {
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "usage record not found")
		return "", usageledger.Record{}, false
	}
	record, err := h.Ledger.Get(r.Context(), tenant, id)
	if err != nil {
		h.storeError(w, r, err)
		return "", usageledger.Record{}, false
	}
	if record.TenantID != tenant || !(usageledger.Filter{Visibility: visibility}).Matches(record) {
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "usage record not found")
		return "", usageledger.Record{}, false
	}
	return tenant, record, true
}

// PostFeedback adds feedback to a visible record.
// POST /api/v1/usage/records/{id}/feedback
func (h *UsageHandler) PostFeedback(w http.ResponseWriter, r *http.Request) {
	tenant, record, ok := h.feedbackRecord(w, r)
	if !ok {
		return
	}
	var req feedbackRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, maxFeedbackBody+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.Decode(new(any)) != io.EOF {
		Error(w, r, http.StatusBadRequest, "INVALID_FEEDBACK", "body must be a JSON object with score, and optionally label and comment")
		return
	}
	if req.Score == nil {
		Error(w, r, http.StatusBadRequest, "INVALID_FEEDBACK", "score is required (a number between -1 and 1)")
		return
	}
	by := ""
	if identity := GetWIFIdentity(r.Context()); identity != nil {
		by = identity.PrincipalID
	}
	if by == "" {
		if !h.Development {
			Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "verified identity required")
			return
		}
		by = "development"
	}
	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now().UTC()
	}
	fb := usageledger.Feedback{ID: usageledger.NewFeedbackID(), TenantID: tenant, RecordID: record.ID, By: by, CreatedAt: now,
		Score: *req.Score, Label: req.Label, Comment: req.Comment}
	if err := fb.Validate(h.FeedbackComments); err != nil {
		code, message := "INVALID_FEEDBACK", strings.TrimPrefix(err.Error(), usageledger.ErrInvalid.Error()+": ")
		if req.Comment != "" && !h.FeedbackComments {
			code, message = "COMMENTS_DISABLED", "free-text comments are not enabled on this deployment"
		}
		Error(w, r, http.StatusBadRequest, code, message)
		return
	}
	if err := h.Feedback.Append(r.Context(), fb); err != nil {
		if errors.Is(err, usageledger.ErrInvalid) {
			Error(w, r, http.StatusBadRequest, "INVALID_FEEDBACK", "feedback could not be stored")
			return
		}
		Error(w, r, http.StatusInternalServerError, "FEEDBACK_ERROR", "feedback could not be stored")
		return
	}
	JSON(w, http.StatusCreated, fb)
}

// ListFeedback returns a record's feedback history, newest first.
// GET /api/v1/usage/records/{id}/feedback
func (h *UsageHandler) ListFeedback(w http.ResponseWriter, r *http.Request) {
	tenant, record, ok := h.feedbackRecord(w, r)
	if !ok {
		return
	}
	history, err := h.Feedback.History(r.Context(), tenant, record.ID, usageledger.MaxFeedbackHistory)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "FEEDBACK_ERROR", "feedback unavailable")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"record_id": record.ID, "feedback": history})
}

// UsageConfig is what the Console needs to know about this deployment's
// usage settings. It carries no secret.
type UsageConfig struct {
	// TraceURLTemplate links a trace id to an external trace viewer; empty
	// when not configured.
	TraceURLTemplate string `json:"trace_url_template"`
	// Feedback reports whether feedback is available; FeedbackComments whether
	// free-text comments are accepted.
	Feedback         bool `json:"feedback"`
	FeedbackComments bool `json:"feedback_comments"`
	// TagKeysAllowed are the keys a caller may set with X-AIPlex-Tags.
	TagKeysAllowed []string `json:"tag_keys_allowed"`
}

// Config returns the usage settings.
// GET /api/v1/usage/config
func (h *UsageHandler) Config(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, UsageConfig{TraceURLTemplate: h.TraceURLTemplate, Feedback: h.Feedback != nil, FeedbackComments: h.FeedbackComments && h.Feedback != nil, TagKeysAllowed: h.Tags.AllowedKeys()})
}

// TraceURLTemplateEnv names the operator setting for the trace link template.
const TraceURLTemplateEnv = "AIPLEX_TRACE_URL_TEMPLATE"

// ValidateTraceURLTemplate checks a trace viewer link template: an http or
// https URL containing "{trace_id}". An empty template is valid (no links).
func ValidateTraceURLTemplate(template string) error {
	if template == "" {
		return nil
	}
	if !strings.Contains(template, "{trace_id}") {
		return errors.New(TraceURLTemplateEnv + " must contain {trace_id}")
	}
	probe := strings.ReplaceAll(template, "{trace_id}", strings.Repeat("0", 32))
	u, err := url.Parse(probe)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return errors.New(TraceURLTemplateEnv + " must be an http or https URL without credentials")
	}
	return nil
}

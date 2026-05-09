package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// A2AHandler serves A2APlex delegation and Agent Card endpoints.
type A2AHandler struct {
	store registry.Store
}

// NewA2AHandler creates an A2APlex API handler.
func NewA2AHandler(store registry.Store) *A2AHandler {
	return &A2AHandler{store: store}
}

// ── Agent Card Discovery ──

// GetAgentCard returns the A2A Agent Card for a deployed agent instance.
// GET /a2a/{instanceId}/.well-known/agent.json
func (h *A2AHandler) GetAgentCard(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("instanceId")

	inst, err := h.store.GetInstance(r.Context(), instanceID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "instance not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	if inst.Kind != capability.KindTask {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "instance is not an A2A agent")
		return
	}

	tmpl, err := h.store.GetTemplate(r.Context(), inst.TemplateID)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	// Build Agent Card from instance capabilities (kind=task) and template metadata.
	taskNames := make([]string, 0, len(inst.Capabilities))
	for _, c := range inst.Capabilities {
		if u, err := capability.ParseURI(c.URI); err == nil {
			taskNames = append(taskNames, u.Name)
		}
	}

	card := models.AgentCard{
		Name:         tmpl.Name,
		Description:  tmpl.Description,
		URL:          fmt.Sprintf("/a2a/%s", instanceID),
		Version:      tmpl.Version,
		Capabilities: taskNames,
		AuthSchemes: []models.AuthSchemeInfo{
			{Scheme: "bearer", Config: map[string]any{"issuer": "https://aiplex.example.com/auth/realms/aiplex"}},
			{Scheme: "spiffe"},
		},
	}

	for _, tt := range taskNames {
		card.TaskTypes = append(card.TaskTypes, models.TaskTypeInfo{
			Type:        tt,
			Description: fmt.Sprintf("Task type: %s", tt),
		})
	}

	if tmpl.AgentCard != nil {
		card.Metadata = tmpl.AgentCard
	}

	if err := deploy.ValidateAgentCard(&card); err != nil {
		Error(w, r, http.StatusInternalServerError, "INVALID_AGENT_CARD", err.Error())
		return
	}

	JSON(w, http.StatusOK, card)
}

// ListAgentCards returns Agent Cards for all deployed A2A agents.
// GET /api/v1/a2a/agents
func (h *A2AHandler) ListAgentCards(w http.ResponseWriter, r *http.Request) {
	instances, err := h.store.ListInstances(r.Context(), capability.KindTask)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	var cards []map[string]any
	for _, inst := range instances {
		if inst.Status != models.StatusRunning {
			continue
		}
		tmpl, _ := h.store.GetTemplate(r.Context(), inst.TemplateID)
		name := inst.DisplayName
		if name == "" && tmpl != nil {
			name = tmpl.Name
		}
		taskTypes := make([]string, 0, len(inst.Capabilities))
		for _, c := range inst.Capabilities {
			if u, err := capability.ParseURI(c.URI); err == nil {
				taskTypes = append(taskTypes, u.Name)
			}
		}
		cards = append(cards, map[string]any{
			"instance_id": inst.ID,
			"name":        name,
			"url":         fmt.Sprintf("/a2a/%s", inst.ID),
			"task_types":  taskTypes,
			"status":      inst.Status,
		})
	}
	JSON(w, http.StatusOK, cards)
}

// ── Delegation Tracking ──

// RecordDelegation records an agent-to-agent delegation event.
// POST /api/v1/a2a/delegations
func (h *A2AHandler) RecordDelegation(w http.ResponseWriter, r *http.Request) {
	var d models.Delegation
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if d.StartedAt.IsZero() {
		d.StartedAt = time.Now()
	}
	if d.Status == "" {
		d.Status = "pending"
	}
	if err := h.fillTraceContext(r, &d); err != nil {
		Error(w, r, http.StatusInternalServerError, "TRACE_CONTEXT", err.Error())
		return
	}

	if err := h.store.AppendDelegation(r.Context(), &d); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusCreated, d)
}

// fillTraceContext populates TraceID/SpanID/ParentSpanID for a new delegation:
//   - If a W3C `traceparent` header is present, parse and use it.
//   - Else if a parent delegation exists, inherit its TraceID and use parent's
//     SpanID as ParentSpanID.
//   - Else generate a fresh trace.
func (h *A2AHandler) fillTraceContext(r *http.Request, d *models.Delegation) error {
	if d.TraceID != "" && d.SpanID != "" {
		return nil
	}
	if tp := r.Header.Get("traceparent"); tp != "" {
		if tid, sid, ok := parseTraceparent(tp); ok {
			if d.TraceID == "" {
				d.TraceID = tid
			}
			if d.ParentSpanID == "" {
				d.ParentSpanID = sid
			}
		}
	}
	if d.TraceID == "" && d.ParentID != "" {
		if parent, err := h.store.GetDelegation(r.Context(), d.ParentID); err == nil && parent != nil {
			d.TraceID = parent.TraceID
			if d.ParentSpanID == "" {
				d.ParentSpanID = parent.SpanID
			}
		}
	}
	if d.TraceID == "" {
		d.TraceID = randomHex(16)
	}
	if d.SpanID == "" {
		d.SpanID = randomHex(8)
	}
	return nil
}

// parseTraceparent parses a W3C traceparent header of the form
// "00-<32hex trace>-<16hex span>-<2hex flags>". Returns trace, span, ok.
func parseTraceparent(tp string) (string, string, bool) {
	parts := strings.Split(tp, "-")
	if len(parts) != 4 {
		return "", "", false
	}
	if len(parts[1]) != 32 || len(parts[2]) != 16 {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// randomHex returns 2*n hex characters of cryptographic randomness.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// GetDelegation returns a single delegation by ID.
// GET /api/v1/a2a/delegations/{id}
func (h *A2AHandler) GetDelegation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := h.store.GetDelegation(r.Context(), id)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "delegation not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, d)
}

// ListDelegations returns recent delegations, optionally filtered by agent.
// GET /api/v1/a2a/delegations?agent_id=X&limit=50
func (h *A2AHandler) ListDelegations(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	delegations, err := h.store.ListDelegations(r.Context(), agentID, 50)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, delegations)
}

// UpdateDelegation updates a delegation's status (e.g., completed, failed).
// PATCH /api/v1/a2a/delegations/{id}
func (h *A2AHandler) UpdateDelegation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := h.store.GetDelegation(r.Context(), id)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "delegation not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	var update struct {
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}

	existing.Status = update.Status
	existing.Error = update.Error
	if update.Status == "completed" || update.Status == "failed" {
		now := time.Now()
		existing.CompletedAt = &now
		existing.DurationMs = now.Sub(existing.StartedAt).Milliseconds()
	}

	if err := h.store.UpdateDelegation(r.Context(), existing); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, existing)
}

// GetDelegationChain returns the full call chain for a root delegation.
// GET /api/v1/a2a/delegations/{id}/chain
func (h *A2AHandler) GetDelegationChain(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	root, err := h.store.GetDelegation(r.Context(), id)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "delegation not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	// Find all children by walking the delegation list
	all, _ := h.store.ListDelegations(r.Context(), "", 1000)
	var children []models.Delegation
	for _, d := range all {
		if d.ParentID == id {
			children = append(children, d)
		}
	}

	chain := models.DelegationChain{
		RootDelegation:  *root,
		Children:        children,
		Depth:           1 + len(children),
		TotalDurationMs: root.DurationMs,
	}
	for _, c := range children {
		chain.TotalDurationMs += c.DurationMs
	}

	JSON(w, http.StatusOK, chain)
}

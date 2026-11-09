package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// A2AHandler serves A2APlex delegation and Agent Card endpoints.
type A2AHandler struct {
	store       registry.A2APlexStore
	authSchemes []models.AuthSchemeInfo
}

type A2AOption func(*A2AHandler)

// WithAgentCardAuthSchemes advertises only authentication configured by the
// host for the instance gateway. An omitted option means unspecified, never
// anonymous. Stored SPIFFE labels alone do not prove gateway authentication.
func WithAgentCardAuthSchemes(schemes []models.AuthSchemeInfo) A2AOption {
	frozen := cloneAuthSchemes(schemes)
	return func(h *A2AHandler) { h.authSchemes = cloneAuthSchemes(frozen) }
}
func cloneAuthSchemes(schemes []models.AuthSchemeInfo) []models.AuthSchemeInfo {
	b, e := json.Marshal(schemes)
	if e != nil {
		return nil
	}
	var out []models.AuthSchemeInfo
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

// NewA2AHandler creates an A2APlex API handler.
func NewA2AHandler(store registry.A2APlexStore, options ...A2AOption) *A2AHandler {
	h := &A2AHandler{store: store}
	for _, option := range options {
		if option != nil {
			option(h)
		}
	}
	return h
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

	if inst.Plane != models.PlaneA2APlex {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "instance is not an A2A agent")
		return
	}
	if inst.Status != models.StatusRunning {
		Error(w, r, http.StatusConflict, "INSTANCE_UNAVAILABLE", "agent instance is not running")
		return
	}

	tmpl, err := h.store.GetTemplate(r.Context(), inst.TemplateID)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if tmpl == nil || tmpl.Plane != models.PlaneA2APlex {
		Error(w, r, http.StatusServiceUnavailable, "TEMPLATE_UNAVAILABLE", "agent instance template is unavailable")
		return
	}
	tasks := deployedTasks(inst, tmpl)

	// Build Agent Card from template + instance
	card := models.AgentCard{
		Name:         tmpl.Name,
		Description:  tmpl.Description,
		URL:          fmt.Sprintf("/a2a/%s", instanceID),
		Version:      tmpl.Version,
		Capabilities: tasks,
		AuthSchemes:  cloneAuthSchemes(h.authSchemes),
	}

	// Build task type info from template
	for _, tt := range tasks {
		card.TaskTypes = append(card.TaskTypes, models.TaskTypeInfo{
			Type:        tt,
			Description: fmt.Sprintf("Task type: %s", tt),
		})
	}

	// Merge any custom agent card data from template
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
	instances, err := h.store.ListInstances(r.Context(), models.PlaneA2APlex)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	cards := make([]map[string]any, 0)
	for _, inst := range instances {
		if inst.Status != models.StatusRunning {
			continue
		}
		tmpl, e := h.store.GetTemplate(r.Context(), inst.TemplateID)
		if e != nil || tmpl == nil || tmpl.Plane != models.PlaneA2APlex {
			Error(w, r, http.StatusServiceUnavailable, "TEMPLATE_UNAVAILABLE", "agent instance template is unavailable")
			return
		}
		name := inst.DisplayName
		if name == "" && tmpl != nil {
			name = tmpl.Name
		}
		cards = append(cards, map[string]any{
			"instance_id": inst.ID,
			"name":        name,
			"url":         fmt.Sprintf("/a2a/%s", inst.ID),
			"task_types":  prefixedCapabilities(deployedTasks(&inst, tmpl), "a2a:task:"),
			"status":      inst.Status,
		})
	}
	JSON(w, http.StatusOK, cards)
}

// ── Delegation Tracking ──

// RecordDelegation records reported history, not a live delegation request.
// Verified collector attribution is separate from the reported subject IDs.
// POST /api/v1/a2a/delegations
func (h *A2AHandler) RecordDelegation(w http.ResponseWriter, r *http.Request) {
	var d models.Delegation
	if err := telemetryJSON(r, &d); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if d.ID == "" {
		d.ID = "del-" + randomHex(16)
	}
	if !telemetryID.MatchString(d.ID) || !telemetryLabel(d.CallerAgentID, true) || !telemetryLabel(d.CalleeAgentID, true) || !telemetryLabel(d.TaskType, true) || !telemetryLabel(d.CallerInstanceID, false) || !telemetryLabel(d.CalleeInstanceID, false) || !telemetryLabel(d.UserID, false) || len(d.Error) > 8192 {
		Error(w, r, 400, "INVALID_DELEGATION", "valid caller, callee, task and record identifiers are required")
		return
	}
	now := time.Now().UTC()
	d.ReportedBy = reportedBy(r)
	d.ReportedAt = now
	if d.StartedAt.IsZero() {
		d.StartedAt = now
	}
	if d.Status == "" {
		d.Status = "pending"
	}
	if d.Status != "pending" && d.Status != "running" && d.Status != "completed" && d.Status != "failed" {
		Error(w, r, 400, "INVALID_STATUS", "delegation status must be pending, running, completed or failed")
		return
	}
	if err := validateDelegationTiming(&d, now); err != nil {
		Error(w, r, 400, "INVALID_DELEGATION", err.Error())
		return
	}
	parent, err := h.delegationParent(r, &d)
	if err != nil {
		Error(w, r, 400, "INVALID_PARENT", err.Error())
		return
	}
	if parent != nil {
		if d.UserID == "" {
			d.UserID = parent.UserID
		}
		if d.CallerInstanceID == "" {
			d.CallerInstanceID = parent.CalleeInstanceID
		}
	}
	if err := h.fillTraceContext(r, &d, parent); err != nil {
		Error(w, r, http.StatusBadRequest, "TRACE_CONTEXT", err.Error())
		return
	}

	if err := h.store.AppendDelegation(r.Context(), &d); err != nil {
		if errors.Is(err, registry.ErrAlreadyExists) {
			Error(w, r, http.StatusConflict, "ALREADY_EXISTS", "delegation record already exists")
			return
		}
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
func (h *A2AHandler) fillTraceContext(r *http.Request, d *models.Delegation, parent *models.Delegation) error {
	if e := validateTelemetryTrace(d.TraceID, d.SpanID, d.ParentSpanID); e != nil {
		return e
	}
	if tp := r.Header.Get("traceparent"); tp != "" {
		if tid, sid, ok := parseTraceparent(tp); ok {
			if d.TraceID != "" && d.TraceID != tid {
				return fmt.Errorf("reported trace and traceparent disagree")
			}
			if d.ParentSpanID != "" && d.ParentSpanID != sid {
				return fmt.Errorf("reported parent span and traceparent disagree")
			}
			if d.TraceID == "" {
				d.TraceID = tid
			}
			if d.ParentSpanID == "" {
				d.ParentSpanID = sid
			}
		} else {
			return fmt.Errorf("invalid W3C traceparent")
		}
	}
	if parent != nil {
		if d.TraceID != "" && d.TraceID != parent.TraceID || d.ParentSpanID != "" && d.ParentSpanID != parent.SpanID {
			return fmt.Errorf("nested delegation trace does not match parent")
		}
		d.TraceID = parent.TraceID
		if d.ParentSpanID == "" {
			d.ParentSpanID = parent.SpanID
		}
	}
	if d.TraceID == "" {
		d.TraceID = randomHex(16)
	}
	if d.SpanID == "" {
		d.SpanID = randomHex(8)
	}
	if e := validateTelemetryTrace(d.TraceID, d.SpanID, d.ParentSpanID); e != nil {
		return e
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
	if len(parts[0]) != 2 || parts[0] == "ff" || len(parts[3]) != 2 || parts[0] != strings.ToLower(parts[0]) || parts[3] != strings.ToLower(parts[3]) || !traceHex(parts[1], 16) || !traceHex(parts[2], 8) {
		return "", "", false
	}
	if _, e := hex.DecodeString(parts[0] + parts[3]); e != nil {
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
	limit, e := telemetryLimit(r, 50)
	if e != nil {
		Error(w, r, 400, "INVALID_LIMIT", e.Error())
		return
	}
	delegations, err := h.store.ListDelegations(r.Context(), agentID, limit)
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
	if err := telemetryJSON(r, &update); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if update.Status != "pending" && update.Status != "running" && update.Status != "completed" && update.Status != "failed" || len(update.Error) > 8192 {
		Error(w, r, 400, "INVALID_STATUS", "valid delegation status is required")
		return
	}
	if existing.Status == "completed" || existing.Status == "failed" || existing.Status == "running" && update.Status == "pending" {
		Error(w, r, 409, "INVALID_TRANSITION", "delegation cannot reverse or repeat a final transition")
		return
	}

	existing.Status = update.Status
	existing.Error = update.Error
	if update.Status == "completed" || update.Status == "failed" {
		now := time.Now().UTC()
		existing.CompletedAt = &now
		existing.DurationMs = now.Sub(existing.StartedAt).Milliseconds()
	}
	if e := validateDelegationTiming(existing, time.Now().UTC()); e != nil {
		Error(w, r, 400, "INVALID_DELEGATION", e.Error())
		return
	}

	if err := h.store.UpdateDelegation(r.Context(), existing); err != nil {
		if errors.Is(err, registry.ErrConflict) {
			Error(w, r, 409, "INVALID_TRANSITION", "delegation changed or transition is invalid")
			return
		}
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

	// Refuse a partial graph instead of silently dropping older descendants.
	var all []models.Delegation
	var e error
	if traces, ok := h.store.(registry.DelegationTraceReader); ok && root.TraceID != "" {
		all, e = traces.ListDelegationTrace(r.Context(), root.TraceID, 10001)
	} else {
		all, e = h.store.ListDelegations(r.Context(), "", 10001)
	}
	if e != nil {
		Error(w, r, 500, "STORE_ERROR", "delegation graph unavailable")
		return
	}
	if len(all) > 10000 {
		Error(w, r, 422, "CHAIN_LIMIT", "delegation history exceeds bounded chain scan")
		return
	}
	adjacent := map[string][]models.Delegation{}
	for _, d := range all {
		adjacent[d.ParentID] = append(adjacent[d.ParentID], d)
	}
	for key := range adjacent {
		sort.Slice(adjacent[key], func(i, j int) bool {
			a, b := adjacent[key][i], adjacent[key][j]
			if a.StartedAt.Equal(b.StartedAt) {
				return a.ID < b.ID
			}
			return a.StartedAt.Before(b.StartedAt)
		})
	}

	chain := models.DelegationChain{
		RootDelegation:  *root,
		Children:        make([]models.Delegation, 0),
		Depth:           1,
		TotalDurationMs: root.DurationMs,
	}
	seen := map[string]bool{id: true}
	var visit func(string, int) bool
	visit = func(parent string, depth int) bool {
		if depth > 100 {
			return false
		}
		for _, child := range adjacent[parent] {
			if seen[child.ID] {
				return false
			}
			seen[child.ID] = true
			chain.Children = append(chain.Children, child)
			chain.TotalDurationMs += child.DurationMs
			if depth+1 > chain.Depth {
				chain.Depth = depth + 1
			}
			if !visit(child.ID, depth+1) {
				return false
			}
		}
		return true
	}
	if !visit(id, 1) {
		Error(w, r, 422, "INVALID_CHAIN", "delegation graph contains a cycle or exceeds depth limit")
		return
	}

	JSON(w, http.StatusOK, chain)
}

func prefixedCapabilities(values []string, prefix string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = prefix + v
	}
	return out
}

func (h *A2AHandler) delegationParent(r *http.Request, d *models.Delegation) (*models.Delegation, error) {
	if d.ParentID == "" {
		return nil, nil
	}
	if !telemetryID.MatchString(d.ParentID) {
		return nil, fmt.Errorf("invalid parent identifier")
	}
	seen := map[string]bool{d.ID: true}
	id := d.ParentID
	var parent *models.Delegation
	for depth := 0; id != ""; depth++ {
		if depth >= 100 || seen[id] {
			return nil, fmt.Errorf("delegation parent chain contains a cycle or exceeds depth limit")
		}
		seen[id] = true
		p, e := h.store.GetDelegation(r.Context(), id)
		if e != nil || p == nil {
			return nil, fmt.Errorf("delegation parent is unavailable")
		}
		if parent == nil {
			parent = p
		}
		id = p.ParentID
	}
	if parent.CalleeAgentID != d.CallerAgentID || parent.CalleeInstanceID != "" && d.CallerInstanceID != "" && parent.CalleeInstanceID != d.CallerInstanceID || parent.UserID != "" && d.UserID != "" && parent.UserID != d.UserID || d.StartedAt.Before(parent.StartedAt) {
		return nil, fmt.Errorf("nested delegation caller, user or chronology does not match its parent")
	}
	return parent, nil
}

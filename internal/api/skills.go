package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// SkillsHandler serves SkillsPlex catalog, instance discovery, and invocation
// audit endpoints. It mirrors A2AHandler in shape and naming so the Console
// and CLI can reuse the same patterns.
type SkillsHandler struct {
	store registry.SkillsPlexStore
}

// NewSkillsHandler creates a SkillsPlex API handler.
func NewSkillsHandler(store registry.SkillsPlexStore) *SkillsHandler {
	return &SkillsHandler{store: store}
}

// ListSkillServers returns a summary of every running SkillsPlex instance with
// the skills it advertises. GET /api/v1/skills/servers
func (h *SkillsHandler) ListSkillServers(w http.ResponseWriter, r *http.Request) {
	instances, err := h.store.ListInstances(r.Context(), models.PlaneSkillsPlex)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	type serverSummary struct {
		InstanceID  string   `json:"instance_id"`
		Name        string   `json:"name"`
		URL         string   `json:"url"`
		SkillBundle string   `json:"skill_bundle,omitempty"`
		Skills      []string `json:"skills"`
		Status      string   `json:"status"`
	}

	out := make([]serverSummary, 0, len(instances))
	for _, inst := range instances {
		if inst.Status != models.StatusRunning {
			continue
		}
		tmpl, e := h.store.GetTemplate(r.Context(), inst.TemplateID)
		if e != nil || tmpl == nil || tmpl.Plane != models.PlaneSkillsPlex {
			Error(w, r, http.StatusServiceUnavailable, "TEMPLATE_UNAVAILABLE", "skill instance template is unavailable")
			return
		}
		name := inst.DisplayName
		bundle := ""
		if tmpl != nil {
			if name == "" {
				name = tmpl.Name
			}
			bundle = tmpl.SkillBundle
		}
		out = append(out, serverSummary{
			InstanceID:  inst.ID,
			Name:        name,
			URL:         "/skills/" + inst.ID,
			SkillBundle: bundle,
			Skills:      prefixedCapabilities(skillNames(deployedSkills(&inst, tmpl)), "skill:invoke:"),
			Status:      string(inst.Status),
		})
	}
	JSON(w, http.StatusOK, out)
}

// GetSkillsManifest returns the merged Skill catalog for a deployed skill server.
// Discovered instance scopes determine the callable skills; template metadata
// enriches matching entries. GET /skills/{instanceId}/.well-known/skills.json
func (h *SkillsHandler) GetSkillsManifest(w http.ResponseWriter, r *http.Request) {
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
	if inst.Plane != models.PlaneSkillsPlex {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "instance is not a SkillsPlex server")
		return
	}
	if inst.Status != models.StatusRunning {
		Error(w, r, http.StatusConflict, "INSTANCE_UNAVAILABLE", "skill server is not running")
		return
	}

	tmpl, err := h.store.GetTemplate(r.Context(), inst.TemplateID)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if tmpl == nil || tmpl.Plane != models.PlaneSkillsPlex {
		Error(w, r, http.StatusServiceUnavailable, "TEMPLATE_UNAVAILABLE", "skill instance template is unavailable")
		return
	}

	JSON(w, http.StatusOK, skillsManifest(inst, tmpl, deployedSkills(inst, tmpl)))
}

// skillsManifest is the manifest shape shared by per-instance and
// access-point skill discovery.
func skillsManifest(inst *models.Instance, tmpl *models.Template, skills []models.SkillInfo) map[string]any {
	return map[string]any{
		"instance_id":  inst.ID,
		"name":         tmpl.Name,
		"description":  tmpl.Description,
		"version":      tmpl.Version,
		"url":          "/skills/" + inst.ID,
		"skill_bundle": tmpl.SkillBundle,
		"skills":       skills,
		"scopes":       inst.Scopes,
	}
}

// RecordInvocation appends caller-reported historical telemetry; it does not
// execute a skill or prove that live authorization succeeded. If the invocation
// failed with a "missing scope:" error (the form returned by aiplex-authz),
// a reported_scope_missing PolicyDenial is also appended, distinguishable from
// a denial produced by the actual authorization path.
// POST /api/v1/skills/invocations
func (h *SkillsHandler) RecordInvocation(w http.ResponseWriter, r *http.Request) {
	var inv models.SkillInvocation
	if err := telemetryJSON(r, &inv); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if !telemetryLabel(inv.SkillName, true) || !telemetryLabel(inv.AgentID, true) || !telemetryLabel(inv.InstanceID, false) || !telemetryLabel(inv.UserID, false) || len(inv.Error) > 8192 || inv.DurationMs < 0 {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "valid skill_name, agent_id and telemetry timing are required")
		return
	}
	now := time.Now().UTC()
	inv.ReportedBy = reportedBy(r)
	inv.ReportedAt = now
	if inv.StartedAt.IsZero() {
		inv.StartedAt = now
	}
	if inv.Status == "" {
		inv.Status = "success"
	}
	if inv.Status != "success" && inv.Status != "failed" {
		Error(w, r, 400, "INVALID_STATUS", "skill status must be success or failed")
		return
	}
	if inv.StartedAt.After(now.Add(5 * time.Minute)) {
		Error(w, r, 400, "INVALID_TIME", "skill start cannot be in the future")
		return
	}
	if inv.ID == "" {
		inv.ID = "inv-" + randHex(8)
	}
	if !telemetryID.MatchString(inv.ID) {
		Error(w, r, 400, "INVALID_ID", "invalid skill invocation id")
		return
	}
	if tp := r.Header.Get("traceparent"); tp != "" {
		trace, span, ok := parseTraceparent(tp)
		if !ok || inv.TraceID != "" && inv.TraceID != trace || inv.ParentSpanID != "" && inv.ParentSpanID != span {
			Error(w, r, 400, "TRACE_CONTEXT", "reported trace and traceparent disagree or are invalid")
			return
		}
		inv.TraceID = trace
		inv.ParentSpanID = span
	}
	if inv.TraceID == "" {
		inv.TraceID = randHex(16)
	}
	if inv.SpanID == "" {
		inv.SpanID = randHex(8)
	}
	if e := validateTelemetryTrace(inv.TraceID, inv.SpanID, inv.ParentSpanID); e != nil {
		Error(w, r, 400, "TRACE_CONTEXT", e.Error())
		return
	}

	if err := h.store.AppendSkillInvocation(r.Context(), &inv); err != nil {
		if errors.Is(err, registry.ErrAlreadyExists) {
			Error(w, r, 409, "ALREADY_EXISTS", "skill invocation record already exists")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	if inv.Status == "failed" {
		if scope := extractMissingScope(inv.Error); scope != "" {
			denial := &models.PolicyDenial{
				ID:        "den-" + randHex(8),
				Timestamp: time.Now(),
				Plane:     string(models.PlaneSkillsPlex),
				AgentID:   inv.AgentID,
				UserID:    inv.UserID,
				Action:    "skills/invoke:" + inv.SkillName,
				Scope:     scope,
				Reason:    "reported_scope_missing",
				RequestID: GetRequestID(r.Context()),
			}
			// Best-effort — do not fail the audit write if denial recording fails.
			_ = h.store.AppendPolicyDenial(r.Context(), denial)
		}
	}

	JSON(w, http.StatusCreated, inv)
}

// extractMissingScope parses the "missing scope: <scope>" form returned by
// aiplex-authz and the OPA policy for scope-missing denials. Returns an empty
// string when the error is not a scope-missing failure.
func extractMissingScope(errMsg string) string {
	const prefix = "missing scope:"
	idx := strings.Index(errMsg, prefix)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(errMsg[idx+len(prefix):])
	if rest == "" {
		return ""
	}
	// Stop at first whitespace or punctuation so callers can append context.
	for i, c := range rest {
		if c == ' ' || c == '\n' || c == ',' || c == ';' {
			return rest[:i]
		}
	}
	return rest
}

// ListInvocations returns recent skill invocations, optionally filtered by
// agent_id or skill_name. GET /api/v1/skills/invocations?agent_id=X&skill=Y
func (h *SkillsHandler) ListInvocations(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	skillName := r.URL.Query().Get("skill")
	limit, e := telemetryLimit(r, 100)
	if e != nil {
		Error(w, r, 400, "INVALID_LIMIT", e.Error())
		return
	}
	invs, err := h.store.ListSkillInvocations(r.Context(), agentID, skillName, limit)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, invs)
}

func skillNames(skills []models.SkillInfo) []string {
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = s.Name
	}
	return names
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

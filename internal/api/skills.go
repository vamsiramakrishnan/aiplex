package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// SkillsHandler serves SkillsPlex catalog, instance discovery, and invocation
// audit endpoints.
type SkillsHandler struct {
	store registry.Store
}

// NewSkillsHandler creates a SkillsPlex API handler.
func NewSkillsHandler(store registry.Store) *SkillsHandler {
	return &SkillsHandler{store: store}
}

// ListSkillServers returns a summary of every running skill instance.
// GET /api/v1/skills/servers
func (h *SkillsHandler) ListSkillServers(w http.ResponseWriter, r *http.Request) {
	instances, err := h.store.ListInstances(r.Context(), capability.KindSkill)
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
		tmpl, _ := h.store.GetTemplate(r.Context(), inst.TemplateID)
		name := inst.DisplayName
		bundle := ""
		if tmpl != nil {
			if name == "" {
				name = tmpl.Name
			}
			bundle = tmpl.SkillBundle
		}
		skills := make([]string, 0, len(inst.Capabilities))
		for _, c := range inst.Capabilities {
			if u, err := capability.ParseURI(c.URI); err == nil {
				skills = append(skills, u.Name)
			}
		}
		out = append(out, serverSummary{
			InstanceID:  inst.ID,
			Name:        name,
			URL:         "/skills/" + inst.ID,
			SkillBundle: bundle,
			Skills:      skills,
			Status:      string(inst.Status),
		})
	}
	JSON(w, http.StatusOK, out)
}

// GetSkillsManifest returns the merged skill catalog for a deployed skill server.
// GET /skills/{instanceId}/.well-known/skills.json
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
	if inst.Kind != capability.KindSkill {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "instance is not a skill server")
		return
	}

	tmpl, err := h.store.GetTemplate(r.Context(), inst.TemplateID)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	skills := make([]models.SkillInfo, 0, len(tmpl.Capabilities))
	for _, c := range tmpl.Capabilities {
		skills = append(skills, models.SkillInfo{
			Name:        c.Name,
			Description: c.Description,
			Version:     c.Version,
			Triggers:    c.Tags,
		})
	}

	manifest := map[string]any{
		"instance_id":  instanceID,
		"name":         tmpl.Name,
		"description":  tmpl.Description,
		"version":      tmpl.Version,
		"url":          "/skills/" + instanceID,
		"skill_bundle": tmpl.SkillBundle,
		"skills":       skills,
		"capabilities": inst.Capabilities,
	}
	JSON(w, http.StatusOK, manifest)
}

// RecordInvocation appends a skill invocation audit record.
// POST /api/v1/skills/invocations
func (h *SkillsHandler) RecordInvocation(w http.ResponseWriter, r *http.Request) {
	var inv models.SkillInvocation
	if err := json.NewDecoder(r.Body).Decode(&inv); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if inv.SkillName == "" {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "skill_name is required")
		return
	}
	if inv.StartedAt.IsZero() {
		inv.StartedAt = time.Now()
	}
	if inv.Status == "" {
		inv.Status = "success"
	}
	if inv.ID == "" {
		inv.ID = "inv-" + randHex(8)
	}
	if inv.TraceID == "" {
		inv.TraceID = randHex(16)
	}
	if inv.SpanID == "" {
		inv.SpanID = randHex(8)
	}

	if err := h.store.AppendSkillInvocation(r.Context(), &inv); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	if inv.Status == "failed" {
		if uri := extractMissingCap(inv.Error); uri != "" {
			denial := &models.PolicyDenial{
				ID:        "den-" + randHex(8),
				Timestamp: time.Now(),
				Kind:      capability.KindSkill,
				AgentID:   inv.AgentID,
				UserID:    inv.UserID,
				CapURI:    uri,
				Action:    "invoke",
				Reason:    "cap_missing",
				RequestID: GetRequestID(r.Context()),
			}
			_ = h.store.AppendPolicyDenial(r.Context(), denial)
		}
	}

	JSON(w, http.StatusCreated, inv)
}

// extractMissingCap parses error messages of the form "missing cap: cap://…"
// returned by aiplex-authz / the OPA policy. Empty string for non-matching errors.
func extractMissingCap(errMsg string) string {
	const prefix = "missing cap:"
	idx := strings.Index(errMsg, prefix)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(errMsg[idx+len(prefix):])
	for i, c := range rest {
		if c == ' ' || c == '\n' || c == ',' || c == ';' {
			return rest[:i]
		}
	}
	return rest
}

// ListInvocations returns recent skill invocations.
// GET /api/v1/skills/invocations?agent_id=X&skill=Y
func (h *SkillsHandler) ListInvocations(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	skillName := r.URL.Query().Get("skill")
	invs, err := h.store.ListSkillInvocations(r.Context(), agentID, skillName, 100)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, invs)
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

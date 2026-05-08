package api

import (
	"encoding/json"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// DashboardHandler serves unified observability endpoints.
type DashboardHandler struct {
	store registry.Store
}

// NewDashboardHandler creates a dashboard API handler.
func NewDashboardHandler(store registry.Store) *DashboardHandler {
	return &DashboardHandler{store: store}
}

// GetStats returns the unified dashboard overview.
// GET /api/v1/dashboard/stats
func (h *DashboardHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Instance counts
	allInstances, _ := h.store.ListInstances(ctx, "")
	mcplex, _ := h.store.ListInstances(ctx, models.PlaneMCPlex)
	a2aplex, _ := h.store.ListInstances(ctx, models.PlaneA2APlex)
	llmplex, _ := h.store.ListInstances(ctx, models.PlaneLLMPlex)
	skillsplex, _ := h.store.ListInstances(ctx, models.PlaneSkillsPlex)
	agents, _ := h.store.ListAgents(ctx)

	running := 0
	planes := map[models.Plane]bool{}
	for _, inst := range allInstances {
		if inst.Status == models.StatusRunning {
			running++
		}
		planes[inst.Plane] = true
	}

	// LLM usage (last 24h)
	usage, _ := h.store.GetUsageSummary(ctx, "", "", "day")

	// Delegations count (efficient — no full fetch)
	delegationCount, _ := h.store.CountDelegations(ctx)

	// Policy denial count (efficient — no full fetch)
	denialCount, _ := h.store.CountPolicyDenials(ctx)

	stats := models.DashboardStats{
		TotalInstances:   len(allInstances),
		RunningInstances: running,
		RegisteredAgents: len(agents),
		ActivePlanes:     len(planes),
		MCPlexInstances:     len(mcplex),
		A2APlexInstances:    len(a2aplex),
		LLMPlexInstances:    len(llmplex),
		SkillsPlexInstances: len(skillsplex),
		DailyCostUSD:     usage.TotalCostUSD,
		DailyTokens:      usage.TotalTokens,
		DailyRequests:    usage.RequestCount,
		A2ADelegations:   delegationCount,
		PolicyDenials:    denialCount,
	}

	JSON(w, http.StatusOK, stats)
}

// ListPolicyDenials returns recent authorization denials.
// GET /api/v1/dashboard/denials?limit=50
func (h *DashboardHandler) ListPolicyDenials(w http.ResponseWriter, r *http.Request) {
	denials, err := h.store.ListPolicyDenials(r.Context(), 50)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, denials)
}

// RecordPolicyDenial records a policy denial event (called by ext_authz/OPA).
// POST /api/v1/dashboard/denials
func (h *DashboardHandler) RecordPolicyDenial(w http.ResponseWriter, r *http.Request) {
	var d models.PolicyDenial
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if err := h.store.AppendPolicyDenial(r.Context(), &d); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusCreated, d)
}

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

	// Derive every plane count from the same snapshot instead of issuing five
	// potentially inconsistent database reads. An unavailable source is not zero.
	allInstances, err := h.store.ListInstances(ctx, "")
	if err != nil {
		Error(w, r, http.StatusServiceUnavailable, "STATS_UNAVAILABLE", "instance inventory unavailable")
		return
	}
	agents, err := h.store.ListAgents(ctx)
	if err != nil {
		Error(w, r, http.StatusServiceUnavailable, "STATS_UNAVAILABLE", "agent inventory unavailable")
		return
	}

	running := 0
	planes := map[models.Plane]bool{}
	counts := map[models.Plane]int{}
	for _, inst := range allInstances {
		if inst.Status == models.StatusRunning {
			running++
		}
		planes[inst.Plane] = true
		counts[inst.Plane]++
	}

	// LLM usage (last 24h)
	usage, err := h.store.GetUsageSummary(ctx, "", "", "day")
	if err != nil || usage == nil {
		Error(w, r, http.StatusServiceUnavailable, "STATS_UNAVAILABLE", "usage reporting unavailable")
		return
	}

	// Delegations count (efficient — no full fetch)
	delegationCount, err := h.store.CountDelegations(ctx)
	if err != nil {
		Error(w, r, http.StatusServiceUnavailable, "STATS_UNAVAILABLE", "delegation reporting unavailable")
		return
	}

	// Policy denial count (efficient — no full fetch)
	denialCount, err := h.store.CountPolicyDenials(ctx)
	if err != nil {
		Error(w, r, http.StatusServiceUnavailable, "STATS_UNAVAILABLE", "policy reporting unavailable")
		return
	}

	stats := models.DashboardStats{
		TotalInstances:      len(allInstances),
		RunningInstances:    running,
		RegisteredAgents:    len(agents),
		ActivePlanes:        len(planes),
		MCPlexInstances:     counts[models.PlaneMCPlex],
		A2APlexInstances:    counts[models.PlaneA2APlex],
		LLMPlexInstances:    counts[models.PlaneLLMPlex],
		SkillsPlexInstances: counts[models.PlaneSkillsPlex],
		DailyCostUSD:        usage.TotalCostUSD,
		DailyTokens:         usage.TotalTokens,
		DailyRequests:       usage.RequestCount,
		A2ADelegations:      delegationCount,
		PolicyDenials:       denialCount,
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

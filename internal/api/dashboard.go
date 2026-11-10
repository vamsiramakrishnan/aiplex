package api

import (
	"math/big"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// DashboardHandler serves unified observability endpoints.
type DashboardHandler struct {
	store registry.Store
	// Ledger and Tenant, when set, make the 24-hour usage figures come from the
	// usage ledger (the source of the usage API); otherwise only the legacy
	// registry usage records are available.
	Ledger usageledger.Store
	Tenant func(*http.Request) (string, error)
	// Now is the clock; nil uses time.Now.
	Now func() time.Time

	denialMu    sync.Mutex
	denialLimit *rate.Limiter
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

	var usage models.DashboardStats
	if h.Ledger != nil && h.Tenant != nil {
		tenant, terr := h.Tenant(r)
		if terr != nil || tenant == "" {
			Error(w, r, http.StatusForbidden, "TENANT_REQUIRED", "verified tenant binding required")
			return
		}
		u, lerr := h.ledgerUsage(r, tenant)
		if lerr != nil {
			Error(w, r, http.StatusServiceUnavailable, "STATS_UNAVAILABLE", "usage reporting unavailable")
			return
		}
		usage = u
	} else {
		// No ledger: the legacy registry usage records (empty unless something
		// writes them). Their totals are reported as such.
		legacy, uerr := h.store.GetUsageSummary(ctx, "", "", "day")
		if uerr != nil || legacy == nil {
			Error(w, r, http.StatusServiceUnavailable, "STATS_UNAVAILABLE", "usage reporting unavailable")
			return
		}
		cost, tokens, requests := legacy.TotalCostUSD, legacy.TotalTokens, legacy.RequestCount
		usage = models.DashboardStats{DailyCostUSD: &cost, DailyTokens: &tokens, DailyRequests: &requests, UsageSource: "legacy-store"}
	}

	// Delegations count (efficient — no full fetch)
	delegationCount, err := h.store.CountDelegations(ctx)
	if err != nil {
		Error(w, r, http.StatusServiceUnavailable, "STATS_UNAVAILABLE", "delegation reporting unavailable")
		return
	}

	// Policy denials in the last 24 hours (the field is policy_denials_24h).
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	denialCount, err := h.store.CountPolicyDenialsSince(ctx, now.Add(-24*time.Hour))
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
		DailyCostUSD:        usage.DailyCostUSD,
		DailyTokens:         usage.DailyTokens,
		DailyRequests:       usage.DailyRequests,
		ToolCalls:           usage.ToolCalls,

		DailyUnknownCostRecords:  usage.DailyUnknownCostRecords,
		DailyUnknownUsageRecords: usage.DailyUnknownUsageRecords,
		UsageSource:              usage.UsageSource,
		A2ADelegations:           delegationCount,
		PolicyDenials:            denialCount,
	}

	JSON(w, http.StatusOK, stats)
}

// ledgerUsage totals the last 24 hours of the tenant's ledger. Unknown stays
// unknown: tokens and cost are null when no record carried them, and the
// records that lacked them are counted separately.
func (h *DashboardHandler) ledgerUsage(r *http.Request, tenant string) (models.DashboardStats, error) {
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	summary, err := usageledger.Summarize(r.Context(), h.Ledger, tenant, usageledger.SummaryQuery{
		// Managed exchanges only: harness-reported records can describe the
		// same requests, so they are not added to these totals.
		Filter:  usageledger.Filter{Since: now.Add(-24 * time.Hour), Source: usageledger.SourceManaged},
		GroupBy: []string{"plane", "tool_name"},
	})
	if err != nil {
		return models.DashboardStats{}, err
	}
	var requests, toolCalls, tokens, unknownUsage, unknownCost int64
	tokensReported := false
	usd := new(big.Rat)
	usdReported := false
	for _, g := range summary.Groups {
		requests += g.Requests
		if g.Key["plane"] == usageledger.PlaneMCP && g.Key["tool_name"] != "" {
			toolCalls += g.Requests
		}
		unknownUsage += g.UnknownUsageRecords
		unknownCost += g.UnknownCostRecords
		for _, name := range []string{"input_tokens", "output_tokens"} {
			if v, ok := g.Tokens[name]; ok {
				tokens += v
				tokensReported = true
			}
		}
		for _, c := range g.Cost {
			if c.Currency != "USD" {
				continue
			}
			amount, ok := new(big.Int).SetString(c.Amount, 10)
			if !ok {
				continue
			}
			scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(c.Scale)), nil)
			usd.Add(usd, new(big.Rat).SetFrac(amount, scale))
			usdReported = true
		}
	}
	out := models.DashboardStats{DailyRequests: &requests, ToolCalls: &toolCalls, DailyUnknownCostRecords: unknownCost, DailyUnknownUsageRecords: unknownUsage, UsageSource: "ledger"}
	if tokensReported {
		out.DailyTokens = &tokens
	}
	if usdReported {
		f, _ := usd.Float64()
		out.DailyCostUSD = &f
	}
	return out, nil
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

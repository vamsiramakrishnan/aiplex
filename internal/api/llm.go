package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/internal/secrets"
)

// LLMHandler serves LLMPlex routing, provider, and usage endpoints.
type LLMHandler struct {
	store       registry.Store
	k8s         deploy.K8sClient
	gatewayName string
	secrets     *secrets.Manager
}

// NewLLMHandler creates an LLMPlex API handler.
func NewLLMHandler(store registry.Store, k8s deploy.K8sClient, gatewayName string, sm *secrets.Manager) *LLMHandler {
	return &LLMHandler{store: store, k8s: k8s, gatewayName: gatewayName, secrets: sm}
}

// ── Route Configs ──

// ListRouteConfigs returns all LLM routing configurations.
// GET /api/v1/llm/routes
func (h *LLMHandler) ListRouteConfigs(w http.ResponseWriter, r *http.Request) {
	configs, err := h.store.ListRouteConfigs(r.Context())
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, configs)
}

// GetRouteConfig returns a single route config by model ID.
// GET /api/v1/llm/routes/{modelId}
func (h *LLMHandler) GetRouteConfig(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("modelId")
	rc, err := h.store.GetRouteConfig(r.Context(), modelID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "route config not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, rc)
}

// PutRouteConfig creates or updates an LLM routing configuration.
// PUT /api/v1/llm/routes/{modelId}
func (h *LLMHandler) PutRouteConfig(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("modelId")
	var rc models.LLMRouteConfig
	if err := json.NewDecoder(r.Body).Decode(&rc); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	rc.ModelID = modelID
	rc.ID = "route-" + modelID

	// Validate weights sum to 100
	totalWeight := 0
	for _, b := range rc.Backends {
		totalWeight += b.Weight
	}
	if len(rc.Backends) > 0 && totalWeight != 100 {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "backend weights must sum to 100")
		return
	}

	if err := h.store.PutRouteConfig(r.Context(), &rc); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	// Generate and apply Envoy LLMRoute + AIServiceBackend CRDs
	manifests := deploy.GenerateRoutesFromConfig(&rc, h.gatewayName)
	for _, m := range manifests {
		if err := h.k8s.Apply(r.Context(), m); err != nil {
			zerolog.Ctx(r.Context()).Warn().Err(err).
				Str("kind", m.Kind).Str("name", m.Name).
				Msg("failed to apply LLM route CRD")
		}
	}

	JSON(w, http.StatusOK, rc)
}

// DeleteRouteConfig removes an LLM routing configuration.
// DELETE /api/v1/llm/routes/{modelId}
func (h *LLMHandler) DeleteRouteConfig(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("modelId")
	if err := h.store.DeleteRouteConfig(r.Context(), modelID); err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "route config not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	// Clean up Envoy CRDs
	if err := h.k8s.Delete(r.Context(), "aigateway.envoyproxy.io/v1alpha1", "LLMRoute", "llm-"+modelID, "aiplex-system"); err != nil {
		zerolog.Ctx(r.Context()).Warn().Err(err).Msg("failed to delete LLMRoute CRD")
	}

	w.WriteHeader(http.StatusNoContent)
}

// ── Provider Configs ──

// ListProviders returns all configured LLM providers.
// GET /api/v1/llm/providers
func (h *LLMHandler) ListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.store.ListProviderConfigs(r.Context())
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, providers)
}

// PutProvider creates or updates a provider configuration.
// PUT /api/v1/llm/providers/{provider}
func (h *LLMHandler) PutProvider(w http.ResponseWriter, r *http.Request) {
	providerName := r.PathValue("provider")
	var pc models.ProviderConfig
	if err := json.NewDecoder(r.Body).Decode(&pc); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	pc.Provider = providerName

	// Validate Secret Manager reference exists
	if h.secrets != nil && pc.SecretRef != "" {
		exists, err := h.secrets.Exists(r.Context(), pc.SecretRef)
		if err != nil {
			zerolog.Ctx(r.Context()).Warn().Err(err).Str("secret", pc.SecretRef).Msg("secret validation failed")
		} else if !exists {
			Error(w, r, http.StatusBadRequest, "SECRET_NOT_FOUND",
				fmt.Sprintf("secret %q not found in Secret Manager — create it first with: gcloud secrets create %s --data-file=key.txt", pc.SecretRef, pc.SecretRef))
			return
		}
	}

	if err := h.store.PutProviderConfig(r.Context(), &pc); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, pc)
}

// ── Usage / Cost Tracking ──

// RecordUsage records a single LLM usage event.
// POST /api/v1/llm/usage
func (h *LLMHandler) RecordUsage(w http.ResponseWriter, r *http.Request) {
	var record models.UsageRecord
	if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	record.TotalTokens = record.InputTokens + record.OutputTokens

	// Auto-calculate cost if pricing is known
	if record.CostUSD == 0 {
		tmpl, err := h.store.GetTemplate(r.Context(), record.ModelID)
		if err == nil && tmpl.Pricing != nil {
			record.CostUSD = (float64(record.InputTokens) * tmpl.Pricing.Input / 1_000_000) +
				(float64(record.OutputTokens) * tmpl.Pricing.Output / 1_000_000)
		}
	}

	// Check budget BEFORE recording usage — reject if over limit
	if rc, err := h.store.GetRouteConfig(r.Context(), record.ModelID); err == nil && rc.Budget != nil {
		summary, _ := h.store.GetUsageSummary(r.Context(), record.ModelID, "", "day")
		if rc.Budget.MaxDailyCostUSD > 0 && summary.TotalCostUSD+record.CostUSD > rc.Budget.MaxDailyCostUSD {
			w.Header().Set("X-Budget-Warning", "daily_cost_exceeded")
			Error(w, r, http.StatusTooManyRequests, "BUDGET_EXCEEDED",
				"daily cost budget exceeded for this model")
			return
		}
		if rc.Budget.MaxDailyTokens > 0 && summary.TotalTokens+int64(record.TotalTokens) > rc.Budget.MaxDailyTokens {
			w.Header().Set("X-Budget-Warning", "daily_tokens_exceeded")
			Error(w, r, http.StatusTooManyRequests, "BUDGET_EXCEEDED",
				"daily token budget exceeded for this model")
			return
		}

		// Monthly cost check
		if rc.Budget.MaxMonthlyCostUSD > 0 {
			monthlySummary, _ := h.store.GetUsageSummary(r.Context(), record.ModelID, "", "month")
			if monthlySummary.TotalCostUSD+record.CostUSD > rc.Budget.MaxMonthlyCostUSD {
				w.Header().Set("X-Budget-Warning", "monthly_cost_exceeded")
				Error(w, r, http.StatusTooManyRequests, "BUDGET_EXCEEDED",
					fmt.Sprintf("monthly cost budget exceeded: $%.2f / $%.2f",
						monthlySummary.TotalCostUSD+record.CostUSD, rc.Budget.MaxMonthlyCostUSD))
				return
			}
		}
	}

	// Per-minute rate limiting
	if rc, err := h.store.GetRouteConfig(r.Context(), record.ModelID); err == nil && rc.RateLimit != nil {
		// Count requests in the last minute
		minuteAgo := time.Now().Add(-1 * time.Minute)
		recentRecords, _ := h.store.ListUsageRecords(r.Context(), record.ModelID, "", minuteAgo, 0)

		if rc.RateLimit.RequestsPerMinute > 0 && len(recentRecords) >= rc.RateLimit.RequestsPerMinute {
			w.Header().Set("Retry-After", "60")
			Error(w, r, http.StatusTooManyRequests, "RATE_LIMITED",
				fmt.Sprintf("rate limit exceeded: %d/%d requests per minute",
					len(recentRecords), rc.RateLimit.RequestsPerMinute))
			return
		}

		if rc.RateLimit.TokensPerMinute > 0 {
			var minuteTokens int64
			for _, rec := range recentRecords {
				minuteTokens += int64(rec.TotalTokens)
			}
			if minuteTokens+int64(record.TotalTokens) > int64(rc.RateLimit.TokensPerMinute) {
				w.Header().Set("Retry-After", "60")
				Error(w, r, http.StatusTooManyRequests, "RATE_LIMITED",
					fmt.Sprintf("token rate limit exceeded: %d/%d tokens per minute",
						minuteTokens+int64(record.TotalTokens), rc.RateLimit.TokensPerMinute))
				return
			}
		}
	}

	if err := h.store.AppendUsage(r.Context(), &record); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	JSON(w, http.StatusCreated, record)
}

// GetUsageSummary returns aggregated usage stats.
// GET /api/v1/llm/usage/summary?model_id=X&agent_id=Y&period=day
func (h *LLMHandler) GetUsageSummary(w http.ResponseWriter, r *http.Request) {
	modelID := r.URL.Query().Get("model_id")
	agentID := r.URL.Query().Get("agent_id")
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "day"
	}

	summary, err := h.store.GetUsageSummary(r.Context(), modelID, agentID, period)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, summary)
}

// ListUsageRecords returns recent usage records.
// GET /api/v1/llm/usage?model_id=X&agent_id=Y&limit=50
func (h *LLMHandler) ListUsageRecords(w http.ResponseWriter, r *http.Request) {
	modelID := r.URL.Query().Get("model_id")
	agentID := r.URL.Query().Get("agent_id")
	since := time.Now().Add(-24 * time.Hour) // default: last 24h

	records, err := h.store.ListUsageRecords(r.Context(), modelID, agentID, since, 100)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, records)
}

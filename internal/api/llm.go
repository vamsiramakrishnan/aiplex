package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/internal/secrets"
)

// LLMHandler serves LLMPlex routing, provider, and usage endpoints.
type LLMHandler struct {
	store       registry.LLMPlexStore
	k8s         deploy.K8sClient
	gatewayName string
	secrets     *secrets.Manager
	configMu    sync.Mutex
	ledger      reportedUsageLedger
}

// NewLLMHandler creates an LLMPlex API handler.
func NewLLMHandler(store registry.LLMPlexStore, k8s deploy.K8sClient, gatewayName string, sm *secrets.Manager) *LLMHandler {
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
	if !llmResourceName("llm-"+modelID) || modelID == "" {
		Error(w, r, http.StatusBadRequest, "INVALID_MODEL", "valid route model identifier required")
		return
	}
	rc, err := h.store.GetRouteConfig(r.Context(), modelID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "route config not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	configurationETag(w, rc.Revision)
	JSON(w, http.StatusOK, rc)
}

// PutRouteConfig creates or updates an LLM routing configuration.
// PUT /api/v1/llm/routes/{modelId}
func (h *LLMHandler) PutRouteConfig(w http.ResponseWriter, r *http.Request) {
	h.configMu.Lock()
	defer h.configMu.Unlock()
	modelID := r.PathValue("modelId")
	if !llmResourceName("llm-"+modelID) || modelID == "" {
		Error(w, r, http.StatusBadRequest, "INVALID_MODEL", "valid route model identifier required")
		return
	}
	var rc models.LLMRouteConfig
	if err := llmReadJSON(r, &rc); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if rc.ModelID != "" && rc.ModelID != modelID || rc.ID != "" && rc.ID != "route-"+modelID {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "route identity must match path")
		return
	}
	rc.ModelID = modelID
	rc.ID = "route-" + modelID
	rc.BackendNamingVersion = 2 // caller metadata cannot choose native resource identities
	versioned, generation, ok := configurationStore(w, r, h.store)
	if !ok {
		return
	}
	previous, err := h.store.GetRouteConfig(r.Context(), modelID)
	if err != nil && !errors.Is(err, registry.ErrNotFound) {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", "route configuration unavailable")
		return
	}
	currentRevision := int64(0)
	if previous != nil {
		currentRevision = previous.Revision
	}
	expected, ok := configurationExpected(w, r, currentRevision)
	if !ok {
		return
	}
	if previous != nil && previous.DeletionPending {
		Error(w, r, 409, "ROUTE_DELETION_PENDING", "finish deleting the saved route before replacing it")
		return
	}
	rc.ActivationPending, rc.DeletionPending = true, false
	if previous != nil {
		rc.CreatedAt = previous.CreatedAt
	} else {
		rc.CreatedAt = time.Time{}
	}
	manifests, err := h.routeManifests(r.Context(), &rc)
	if err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if previous != nil && previous.ActivationPending && !sameDesiredRoute(previous, &rc) {
		Error(w, r, 409, "ROUTE_ACTIVATION_PENDING", "retry the saved desired route before replacing its unfinished activation")
		return
	}

	if previous != nil && !sameBackendNames(previous, &rc) {
		if err := h.routeNotReferenced(r.Context(), modelID); err != nil {
			Error(w, r, http.StatusConflict, "ROUTE_IN_USE", "remove fallback references before changing this route's backend inventory")
			return
		}
	}
	if h.k8s == nil {
		Error(w, r, http.StatusServiceUnavailable, "DEPLOYMENT_UNAVAILABLE", "route deployment adapter unavailable")
		return
	}
	if err := h.setRetiredBackends(previous, &rc); err != nil {
		Error(w, r, http.StatusConflict, "INVALID_CLEANUP_INVENTORY", "saved backend ownership inventory requires operator review")
		return
	}
	if err := h.checkBackendOwnership(r.Context(), modelID, append(backendNames(&rc), rc.RetiredBackends...)); err != nil {
		Error(w, r, http.StatusConflict, "BACKEND_OWNERSHIP_CONFLICT", "backend names overlap another route inventory; review route ownership before applying")
		return
	}

	if !putConfiguration(w, r, versioned, registry.ConfigRoute, modelID, &rc, expected, generation) {
		return
	}

	w.Header().Set("X-LLM-Configuration", "saved")
	w.Header().Set("X-LLM-Policy-Mode", "reporting-only")
	if rc.CacheTTL > 0 {
		w.Header().Add("X-LLM-Configuration-Warning", "cache_ttl_metadata_not_applied_to_legacy_route")
	}
	// Submit backend dependencies before the route. An error leaves desired state
	// available for an explicit retry; no cross-system rollback is implied.
	for _, kind := range []string{"AIServiceBackend", "LLMRoute"} {
		for _, m := range manifests {
			if m.Kind == kind {
				if err := h.k8s.Apply(r.Context(), m); err != nil {
					w.Header().Set("X-LLM-Activation", "failed")
					Error(w, r, http.StatusBadGateway, "ROUTE_APPLY_FAILED", "desired route saved; deployment submission failed; inspect and retry the saved configuration")
					return
				}
			}
		}
	}
	if previous != nil {
		if err := h.cleanupBackends(r.Context(), previous, &rc); err != nil {
			w.Header().Set("X-LLM-Activation", "cleanup-pending")
			Error(w, r, http.StatusBadGateway, "ROUTE_CLEANUP_FAILED", "desired route saved and submitted; old backend cleanup failed; inspect and retry")
			return
		}
	}
	rc.RetiredBackends = nil
	rc.ActivationPending = false
	if !putConfiguration(w, r, versioned, registry.ConfigRoute, modelID, &rc, rc.Revision, -1) {
		w.Header().Set("X-LLM-Activation", "cleanup-pending")
		return
	}

	w.Header().Set("X-LLM-Activation", "submitted-unverified")

	JSON(w, http.StatusOK, rc)
}

// DeleteRouteConfig removes an LLM routing configuration.
// DELETE /api/v1/llm/routes/{modelId}
func (h *LLMHandler) DeleteRouteConfig(w http.ResponseWriter, r *http.Request) {
	h.configMu.Lock()
	defer h.configMu.Unlock()
	modelID := r.PathValue("modelId")
	if !llmResourceName("llm-"+modelID) || modelID == "" {
		Error(w, r, http.StatusBadRequest, "INVALID_MODEL", "valid route model identifier required")
		return
	}
	versioned, generation, ok := configurationStore(w, r, h.store)
	if !ok {
		return
	}
	config, err := h.store.GetRouteConfig(r.Context(), modelID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "route config not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	expected, ok := configurationExpected(w, r, config.Revision)
	if !ok {
		return
	}
	if config.ActivationPending {
		Error(w, r, 409, "ROUTE_ACTIVATION_PENDING", "finish the saved route activation before deletion")
		return
	}

	if err := h.routeNotReferenced(r.Context(), modelID); err != nil {
		Error(w, r, http.StatusConflict, "ROUTE_IN_USE", "remove fallback references before deleting this route")
		return
	}
	if h.k8s == nil {
		Error(w, r, http.StatusServiceUnavailable, "DEPLOYMENT_UNAVAILABLE", "route deployment adapter unavailable")
		return
	}
	if err := h.checkBackendOwnership(r.Context(), modelID, append(backendNames(config), config.RetiredBackends...)); err != nil {
		Error(w, r, http.StatusConflict, "BACKEND_OWNERSHIP_CONFLICT", "backend names overlap another route inventory; desired state retained")
		return
	}
	config.DeletionPending = true
	if !putConfiguration(w, r, versioned, registry.ConfigRoute, modelID, config, expected, generation) {
		return
	}

	if err := h.k8s.Delete(r.Context(), "aigateway.envoyproxy.io/v1alpha1", "LLMRoute", "llm-"+modelID, "aiplex-system"); err != nil {
		Error(w, r, http.StatusBadGateway, "ROUTE_DELETE_FAILED", "desired configuration retained; route deletion failed")
		return
	}
	if err := h.cleanupBackends(r.Context(), config, nil); err != nil {
		Error(w, r, http.StatusBadGateway, "ROUTE_CLEANUP_FAILED", "desired configuration retained; backend cleanup failed")
		return
	}
	if err := versioned.DeleteConfiguration(r.Context(), registry.ConfigRoute, modelID, config.Revision, -1); err != nil {
		configurationError(w, r, err)
		return
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
	h.configMu.Lock()
	defer h.configMu.Unlock()
	providerName := r.PathValue("provider")
	var pc models.ProviderConfig
	if err := llmReadJSON(r, &pc); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if pc.Provider != "" && pc.Provider != providerName {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "provider identity must match path")
		return
	}
	pc.Provider = providerName
	if err := h.validateProvider(r.Context(), pc); err != nil {
		Error(w, r, http.StatusBadRequest, "INVALID_PROVIDER", err.Error())
		return
	}
	versioned, generation, ok := configurationStore(w, r, h.store)
	if !ok {
		return
	}
	current, err := versioned.GetConfiguration(r.Context(), registry.ConfigProvider, providerName)
	if err != nil && !errors.Is(err, registry.ErrNotFound) {
		configurationError(w, r, err)
		return
	}
	expected, ok := configurationExpected(w, r, current.Revision)
	if !ok {
		return
	}
	if !pc.Enabled {
		if err := h.providerNotReferenced(r.Context(), providerName); err != nil {
			Error(w, r, http.StatusConflict, "PROVIDER_IN_USE", "remove enabled route backends before disabling this provider")
			return
		}
	}

	if !putConfiguration(w, r, versioned, registry.ConfigProvider, providerName, &pc, expected, generation) {
		return
	}
	w.Header().Set("X-LLM-Configuration", "saved")
	w.Header().Set("X-LLM-Activation", "configuration-only")
	if pc.BaseURL != "" || pc.Region != "" || pc.ProjectID != "" {
		w.Header().Add("X-LLM-Configuration-Warning", "provider_endpoint_metadata_not_applied_to_legacy_routes")
	}
	w.Header().Add("X-LLM-Configuration-Warning", "credential_reference_requires_operator_managed_kubernetes_secret")
	w.Header().Add("X-LLM-Configuration-Warning", "provider_defaults_do_not_rewrite_existing_backend_references")
	JSON(w, http.StatusOK, pc)
}

// ── Usage / Cost Tracking ──

// RecordUsage records a single LLM usage event.
// POST /api/v1/llm/usage
func (h *LLMHandler) RecordUsage(w http.ResponseWriter, r *http.Request) {
	var record models.UsageRecord
	if err := llmReadJSON(r, &record); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid usage report")
		return
	}
	if err := validateLLMUsage(record); err != nil {
		Error(w, r, http.StatusBadRequest, "INVALID_USAGE", err.Error())
		return
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}
	record.TotalTokens = record.InputTokens + record.OutputTokens
	if record.CostUSD == 0 {
		tmpl, err := h.store.GetTemplate(r.Context(), record.ModelID)
		if err == nil && tmpl != nil && tmpl.Pricing != nil && validLLMPrice(tmpl.Pricing.Input) && validLLMPrice(tmpl.Pricing.Output) {
			record.CostUSD = float64(record.InputTokens)*tmpl.Pricing.Input/1_000_000 + float64(record.OutputTokens)*tmpl.Pricing.Output/1_000_000
			if !validLLMPrice(record.CostUSD) {
				Error(w, r, http.StatusBadRequest, "INVALID_USAGE", "calculated reporting cost exceeds supported range")
				return
			}
		} else {
			w.Header().Set("X-LLM-Price-Warning", "reporting_price_unavailable")
		}
	}
	// This endpoint reports already-consumed work. Store the report even when a
	// configured reporting threshold is exceeded; rejecting it cannot undo spend.
	if err := h.store.AppendUsage(r.Context(), &record); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", "usage report could not be persisted")
		return
	}
	h.ledger.record(r, record)
	w.Header().Set("X-LLM-Policy-Mode", "reporting-only")
	h.usageWarnings(w, r, record)
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
	if period != "day" && period != "week" && period != "month" {
		Error(w, r, http.StatusBadRequest, "INVALID_PERIOD", "period must be day, week or month")
		return
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
	if raw := r.URL.Query().Get("since"); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			Error(w, r, http.StatusBadRequest, "INVALID_SINCE", "since must be an RFC3339 timestamp")
			return
		}
		since = value
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 1000 {
			Error(w, r, http.StatusBadRequest, "INVALID_LIMIT", "limit must be between 1 and 1000")
			return
		}
		limit = value
	}

	records, err := h.store.ListUsageRecords(r.Context(), modelID, agentID, since, limit)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, records)
}

// Pending activation may be retried only for the same desired effect; a new
// intent cannot overtake an unacknowledged native operation.
func sameDesiredRoute(a, b *models.LLMRouteConfig) bool {
	x, y := *a, *b
	x.Revision, y.Revision = 0, 0
	x.ActivationPending, y.ActivationPending = false, false
	x.DeletionPending, y.DeletionPending = false, false
	x.RetiredBackends, y.RetiredBackends = nil, nil
	x.CreatedAt, y.CreatedAt = time.Time{}, time.Time{}
	x.UpdatedAt, y.UpdatedAt = time.Time{}, time.Time{}
	// Creation metadata is server-owned and not a desired routing effect.
	rawX, _ := json.Marshal(x)
	rawY, _ := json.Marshal(y)
	return reflect.DeepEqual(rawX, rawY)
}

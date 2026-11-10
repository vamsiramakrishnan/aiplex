package api

import (
	"errors"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/routing"
)

// AccessPointRoutingView is the operator view of an access point's routed
// models: each backend's live circuit-breaker state and recent outcomes in
// this process, and the cache configuration.
type AccessPointRoutingView struct {
	AccessPointID string `json:"access_point_id"`
	// ProcessLocal is always true: breakers and the cache live in one API
	// process; other replicas keep their own.
	ProcessLocal bool                `json:"process_local"`
	Models       []RoutedModelView   `json:"models"`
	CacheStore   *routing.CacheStats `json:"cache_store,omitempty"`
}

// RoutedModelView is one routed model.
type RoutedModelView struct {
	Name     string              `json:"name"`
	Strategy string              `json:"strategy"`
	Backends []RoutedBackendView `json:"backends"`
	Cache    *routing.Cache      `json:"cache,omitempty"`
}

// RoutedBackendView is one backend and its breaker.
type RoutedBackendView struct {
	SurfaceRef  string               `json:"surface_ref"`
	NativeModel string               `json:"native_model"`
	Weight      int                  `json:"weight"`
	Priority    int                  `json:"priority"`
	Breaker     routing.BreakerState `json:"breaker"`
}

// loadRoutedAccessPoint reads an access point for the operator endpoints.
func (h *ConnectedHandler) loadRoutedAccessPoint(w http.ResponseWriter, r *http.Request) (string, AccessPoint, bool) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return "", AccessPoint{}, false
	}
	_, ap, err := h.loadAccessPointRecord(r.Context(), tenant, idParam(r))
	if err != nil {
		accessPointLoadError(w, r, err, func(err error) bool { return errors.Is(err, resources.ErrNotFound) })
		return "", AccessPoint{}, false
	}
	return tenant, ap, true
}

// AccessPointRouting serves GET /api/v1/access-points/{id}/routing
// (operators): live breaker states and recent outcomes of this process.
func (h *ConnectedHandler) AccessPointRouting(w http.ResponseWriter, r *http.Request) {
	tenant, ap, ok := h.loadRoutedAccessPoint(w, r)
	if !ok {
		return
	}
	breakers := h.routeBreakers()
	view := AccessPointRoutingView{AccessPointID: ap.ID, ProcessLocal: true, Models: []RoutedModelView{}}
	cached := false
	for _, m := range ap.Models {
		if !m.routed() {
			continue
		}
		model := RoutedModelView{Name: m.Name, Strategy: m.Routing.Effective().Strategy, Backends: []RoutedBackendView{}, Cache: m.Cache}
		for _, b := range m.allBackends() {
			model.Backends = append(model.Backends, RoutedBackendView{SurfaceRef: b.SurfaceRef, NativeModel: b.NativeModel, Weight: b.EffectiveWeight(), Priority: b.Priority, Breaker: breakers.State(routing.BreakerKey(tenant, ap.ID, m.Name, b))})
		}
		cached = cached || m.Cache != nil
		view.Models = append(view.Models, model)
	}
	if cached {
		stats := h.responseCache().Stats()
		view.CacheStore = &stats
	}
	w.Header().Set("Cache-Control", "no-store")
	JSON(w, http.StatusOK, view)
}

// AccessPointCachePurge serves POST /api/v1/access-points/{id}/cache/purge
// (admins): removes this process's cached answers of the access point, or of
// one model with ?model=.
func (h *ConnectedHandler) AccessPointCachePurge(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdministrator(w, r) {
		return
	}
	tenant, ap, ok := h.loadRoutedAccessPoint(w, r)
	if !ok {
		return
	}
	model := r.URL.Query().Get("model")
	if model != "" {
		found := false
		for _, m := range ap.Models {
			found = found || m.Name == model
		}
		if !found {
			Error(w, r, http.StatusNotFound, "MODEL_NOT_FOUND", "the access point has no such model")
			return
		}
	}
	n := h.responseCache().Purge(tenant, ap.ID, model)
	JSON(w, http.StatusOK, map[string]any{"access_point_id": ap.ID, "model": model, "purged": n, "process_local": true})
}

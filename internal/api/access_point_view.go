package api

import (
	"errors"
	"net/http"
	"sort"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// AccessPointCallerView is what a caller without an operator role may read
// about an access point that admits it: enough to connect, nothing about who
// else may call it or what it is built from.
type AccessPointCallerView struct {
	ID          string                      `json:"id"`
	DisplayName string                      `json:"display_name,omitempty"`
	Endpoints   []AccessPointCallerEndpoint `json:"endpoints"`
}

// AccessPointCallerEndpoint is one client-facing URL of an access point.
type AccessPointCallerEndpoint struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

func (h *ConnectedHandler) accessPointCallerView(ap AccessPoint) AccessPointCallerView {
	base := h.publicBase() + "/ap/" + ap.ID
	view := AccessPointCallerView{ID: ap.ID, DisplayName: ap.DisplayName, Endpoints: []AccessPointCallerEndpoint{}}
	if len(ap.Models) > 0 {
		view.Endpoints = append(view.Endpoints, AccessPointCallerEndpoint{Kind: "models", URL: base + "/v1/models"})
	}
	if len(ap.Tools) > 0 && h.moduleEnabled("mcplex") {
		view.Endpoints = append(view.Endpoints,
			AccessPointCallerEndpoint{Kind: "mcp", URL: h.publicBase() + accessPointResourcePath(ap.ID, "")},
			AccessPointCallerEndpoint{Kind: "mcp_catalog", URL: base + "/v0.1/servers"})
	}
	if len(ap.Skills) > 0 && h.moduleEnabled("skillsplex") {
		view.Endpoints = append(view.Endpoints, AccessPointCallerEndpoint{Kind: "skills", URL: base + "/skills"})
	}
	return view
}

// accessPointReads serves GET /api/v1/access-points[/{id}]. Admins and
// deployers read the revisioned resource. Every other verified caller reads
// only the access points that admit it, as AccessPointCallerView; one that
// does not admit the caller answers exactly like a missing one.
func (h *ConnectedHandler) accessPointReads(resource http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if privilegedOrDevelopment(r, h.Development, models.RoleAdmin, models.RoleDeployer) {
			resource(w, r)
			return
		}
		tenant, ok := h.tenant(w, r)
		if !ok {
			return
		}
		identity := GetWIFIdentity(r.Context())
		if identity == nil || identity.PrincipalID == "" {
			Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "verified identity required")
			return
		}
		caller, err := h.resolveAccessPointCaller(r.Context(), tenant, identity)
		if err != nil {
			Error(w, r, http.StatusServiceUnavailable, "CALLER_UNAVAILABLE", "caller resolution is unavailable")
			return
		}
		if id := idParam(r); id != "" {
			_, ap, err := h.loadAccessPointRecord(r.Context(), tenant, id)
			if err == nil && ap.admits(identity, caller) {
				JSON(w, http.StatusOK, h.accessPointCallerView(ap))
				return
			}
			if err != nil && !errors.Is(err, resources.ErrNotFound) && !errors.Is(err, errAccessPointRecordInvalid) {
				Error(w, r, http.StatusServiceUnavailable, "ACCESS_POINT_UNAVAILABLE", "access point configuration is unavailable")
				return
			}
			resourceError(w, r, resources.ErrNotFound)
			return
		}
		records, err := h.Store.List(r.Context(), tenant, AccessPointKind)
		if err != nil {
			resourceError(w, r, err)
			return
		}
		views := []AccessPointCallerView{}
		for _, record := range records {
			if record.TenantID != tenant || record.Kind != AccessPointKind {
				continue
			}
			ap, err := decodeAccessPoint(record.ID, record.Spec)
			if err != nil || !ap.admits(identity, caller) {
				continue
			}
			views = append(views, h.accessPointCallerView(ap))
		}
		sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
		JSON(w, http.StatusOK, views)
	}
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// ClientHandler registers clients and produces reviewed configuration plans.
// Tenant comes only from a host-supplied trusted identity resolver; destinations
// are looked up by reference in that same tenant, never accepted as request URLs.
type ClientHandler struct {
	store        resources.Store
	tenant       func(*http.Request) (string, error)
	profiles     *clientplex.Registry
	destinations clientplex.DestinationResolver
}

func NewClientHandler(store resources.Store, tenant func(*http.Request) (string, error), destinations clientplex.DestinationResolver) *ClientHandler {
	return &ClientHandler{store: store, tenant: tenant, destinations: destinations, profiles: clientplex.NewBuiltinRegistry()}
}
func (h *ClientHandler) WithProfiles(profiles *clientplex.Registry) *ClientHandler {
	if profiles != nil {
		h.profiles = profiles
	}
	return h
}

func (h *ClientHandler) tenantID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h.tenant == nil {
		Error(w, r, http.StatusServiceUnavailable, "TENANT_UNAVAILABLE", "tenant resolver unavailable")
		return "", false
	}
	tenant, e := h.tenant(r)
	if e != nil || tenant == "" {
		Error(w, r, http.StatusForbidden, "TENANT_REQUIRED", "trusted tenant required")
		return "", false
	}
	return tenant, true
}
func (h *ClientHandler) Profiles(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, h.profiles.Profiles())
}
func (h *ClientHandler) List(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenantID(w, r)
	if !ok {
		return
	}
	all, e := h.store.List(r.Context(), tenant, clientplex.Kind)
	if e != nil {
		h.failure(w, r, e)
		return
	}
	JSON(w, http.StatusOK, all)
}
func (h *ClientHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenantID(w, r)
	if !ok {
		return
	}
	record, e := h.store.Get(r.Context(), tenant, clientplex.Kind, r.PathValue("id"))
	if e != nil {
		h.failure(w, r, e)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", record.Revision))
	JSON(w, http.StatusOK, record)
}
func (h *ClientHandler) Put(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenantID(w, r)
	if !ok {
		return
	}
	var registration clientplex.Registration
	if e := clientReadJSON(r, &registration); e != nil {
		Error(w, r, http.StatusBadRequest, "INVALID_REGISTRATION", e.Error())
		return
	}
	id := r.PathValue("id")
	if registration.ID != "" && registration.ID != id {
		Error(w, r, http.StatusBadRequest, "INVALID_REGISTRATION", "path and registration id differ")
		return
	}
	registration.ID = id
	if e := h.validateReferences(r.Context(), tenant, registration); e != nil {
		Error(w, r, http.StatusBadRequest, "INVALID_REFERENCE", e.Error())
		return
	}
	if _, e := h.profiles.BuildPlan(r.Context(), tenant, registration, h.destinations); e != nil {
		Error(w, r, http.StatusBadRequest, "INVALID_REGISTRATION", e.Error())
		return
	}
	expected, e := clientRevision(r, false)
	if e != nil {
		Error(w, r, http.StatusBadRequest, "INVALID_REVISION", e.Error())
		return
	}
	spec, _ := json.Marshal(registration)
	record, e := h.store.Put(r.Context(), resources.Record{TenantID: tenant, Kind: clientplex.Kind, ID: id, Spec: spec}, expected)
	if e != nil {
		h.failure(w, r, e)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", record.Revision))
	status := http.StatusOK
	if expected == 0 {
		status = http.StatusCreated
	}
	JSON(w, status, record)
}
func (h *ClientHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenantID(w, r)
	if !ok {
		return
	}
	expected, e := clientRevision(r, true)
	if e != nil {
		Error(w, r, http.StatusPreconditionRequired, "REVISION_REQUIRED", e.Error())
		return
	}
	if e = h.store.Delete(r.Context(), tenant, clientplex.Kind, r.PathValue("id"), expected); e != nil {
		h.failure(w, r, e)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *ClientHandler) Plan(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenantID(w, r)
	if !ok {
		return
	}
	record, e := h.store.Get(r.Context(), tenant, clientplex.Kind, r.PathValue("id"))
	if e != nil {
		h.failure(w, r, e)
		return
	}
	var registration clientplex.Registration
	if e = json.Unmarshal(record.Spec, &registration); e != nil {
		h.failure(w, r, e)
		return
	}
	if e := h.validateReferences(r.Context(), tenant, registration); e != nil {
		Error(w, r, http.StatusConflict, "CLIENT_PLAN_UNAVAILABLE", e.Error())
		return
	}
	plan, e := h.profiles.BuildPlan(r.Context(), tenant, registration, h.destinations)
	if e != nil {
		Error(w, r, http.StatusConflict, "CLIENT_PLAN_UNAVAILABLE", e.Error())
		return
	}
	JSON(w, http.StatusOK, plan)
}
func (h *ClientHandler) failure(w http.ResponseWriter, r *http.Request, e error) {
	switch {
	case errors.Is(e, resources.ErrNotFound):
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "client registration not found")
	case errors.Is(e, resources.ErrConflict):
		Error(w, r, http.StatusConflict, "REVISION_CONFLICT", "client registration changed; read the current revision")
	default:
		Error(w, r, http.StatusInternalServerError, "CLIENT_STORE_ERROR", "client registration store unavailable")
	}
}
func clientReadJSON(r *http.Request, v any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(v); e != nil {
		if strings.Contains(e.Error(), `unknown field "platform"`) {
			return fmt.Errorf(`platform is no longer a registration field and must be removed from the document; it never changed a plan`)
		}
		return e
	}
	if e := decoder.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("request must contain one JSON object")
	}
	return nil
}
func clientRevision(r *http.Request, required bool) (uint64, error) {
	raw := r.Header.Get("If-Match")
	if raw == "" {
		if required {
			return 0, fmt.Errorf("If-Match revision required")
		}
		return 0, nil
	}
	if strings.HasPrefix(raw, "\"") {
		if len(raw) < 3 || !strings.HasSuffix(raw, "\"") {
			return 0, fmt.Errorf("invalid If-Match revision")
		}
		raw = raw[1 : len(raw)-1]
	}
	value, e := strconv.ParseUint(raw, 10, 64)
	if e != nil || value == 0 {
		return 0, fmt.Errorf("If-Match must contain a positive numeric revision")
	}
	return value, nil
}

func (h *ClientHandler) validateReferences(ctx context.Context, tenant string, r clientplex.Registration) error {
	if r.NetworkProfileRef != "" {
		if _, e := h.store.Get(ctx, tenant, "network-profile", r.NetworkProfileRef); e != nil {
			return fmt.Errorf("network profile is unavailable in this tenant")
		}
	}
	return nil
}

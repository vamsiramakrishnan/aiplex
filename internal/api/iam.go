package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// IAMHandler serves IAM role binding and WIF identity resolution endpoints.
type IAMHandler struct {
	store registry.Store
	wif   *auth.WIFValidator
}

// NewIAMHandler creates an IAM handler.
func NewIAMHandler(store registry.Store, wif *auth.WIFValidator) *IAMHandler {
	return &IAMHandler{store: store, wif: wif}
}

// defaultRoleCaps returns the built-in cap ceiling for each role. Admin gets
// every kind; deployer can act on tools/tasks/models/skills; viewer is empty;
// agent caps come from per-agent registration.
func defaultRoleCaps(role models.IAMRole) capability.CapSet {
	switch role {
	case models.RoleAdmin:
		var out capability.CapSet
		for _, k := range capability.AllKinds() {
			out = append(out, capability.Cap{URI: fmt.Sprintf("cap://%s/*@v1", k)})
		}
		return out
	case models.RoleDeployer:
		return capability.CapSet{
			{URI: "cap://tool/*@v1"},
			{URI: "cap://task/*@v1"},
			{URI: "cap://model/*@v1"},
			{URI: "cap://skill/*@v1"},
			{URI: "cap://memory/*@v1"},
			{URI: "cap://meta/deploy@v1", Actions: []string{"create", "delete"}},
		}
	}
	return nil
}

// ListRoleBindings returns all group→role mappings.
func (h *IAMHandler) ListRoleBindings(w http.ResponseWriter, r *http.Request) {
	group := r.URL.Query().Get("group")

	var bindings []models.RoleBinding
	var err error
	if group != "" {
		bindings, err = h.store.ListRoleBindingsByGroup(r.Context(), group)
	} else {
		bindings, err = h.store.ListRoleBindings(r.Context())
	}
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, bindings)
}

func (h *IAMHandler) GetRoleBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rb, err := h.store.GetRoleBinding(r.Context(), id)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "role binding not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, rb)
}

func (h *IAMHandler) CreateRoleBinding(w http.ResponseWriter, r *http.Request) {
	var rb models.RoleBinding
	if err := json.NewDecoder(r.Body).Decode(&rb); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if rb.Group == "" || rb.Role == "" {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "group and role are required")
		return
	}

	switch rb.Role {
	case models.RoleAdmin, models.RoleDeployer, models.RoleViewer, models.RoleAgent:
	default:
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST",
			fmt.Sprintf("invalid role %q; must be one of: admin, deployer, viewer, agent", rb.Role))
		return
	}

	if rb.ID == "" {
		rb.ID = fmt.Sprintf("rb-%s-%s", rb.Group, rb.Role)
	}
	rb.CreatedAt = time.Now()
	rb.CreatedBy = extractOwner(r)

	if len(rb.Caps) == 0 {
		rb.Caps = defaultRoleCaps(rb.Role)
	}

	if err := h.store.PutRoleBinding(r.Context(), &rb); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusCreated, rb)
}

func (h *IAMHandler) UpdateRoleBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := h.store.GetRoleBinding(r.Context(), id)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "role binding not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	var update models.RoleBinding
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}

	update.ID = existing.ID
	update.CreatedAt = existing.CreatedAt
	update.CreatedBy = existing.CreatedBy
	if update.Group == "" {
		update.Group = existing.Group
	}
	if update.Role == "" {
		update.Role = existing.Role
	}

	if err := h.store.PutRoleBinding(r.Context(), &update); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, update)
}

func (h *IAMHandler) DeleteRoleBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteRoleBinding(r.Context(), id); err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "role binding not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ResolveIdentity extracts the caller's WIF identity from the request token,
// resolves group→role mappings, syncs Dimension B caps, and returns the result.
func (h *IAMHandler) ResolveIdentity(w http.ResponseWriter, r *http.Request) {
	identity, err := h.wif.ExtractIdentity(r)
	if err != nil {
		Error(w, r, http.StatusUnauthorized, "AUTH_ERROR", err.Error())
		return
	}

	access, err := h.wif.SyncUserCaps(r.Context(), identity)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "RESOLVE_ERROR",
			"failed to resolve access: "+err.Error())
		return
	}

	JSON(w, http.StatusOK, access)
}

// ListRoles returns the available AIPlex roles and their default cap ceilings.
func (h *IAMHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	type roleInfo struct {
		Role        models.IAMRole    `json:"role"`
		DefaultCaps capability.CapSet `json:"default_caps"`
	}

	roles := []roleInfo{
		{Role: models.RoleAdmin, DefaultCaps: defaultRoleCaps(models.RoleAdmin)},
		{Role: models.RoleDeployer, DefaultCaps: defaultRoleCaps(models.RoleDeployer)},
		{Role: models.RoleViewer, DefaultCaps: defaultRoleCaps(models.RoleViewer)},
		{Role: models.RoleAgent, DefaultCaps: defaultRoleCaps(models.RoleAgent)},
	}
	JSON(w, http.StatusOK, roles)
}

func (h *IAMHandler) ValidateWIFPrincipal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Principal string `json:"principal"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}

	if err := auth.ValidateWIFPrincipal(req.Principal); err != nil {
		JSON(w, http.StatusOK, map[string]any{
			"valid": false,
			"error": err.Error(),
		})
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"valid":     true,
		"principal": req.Principal,
	})
}

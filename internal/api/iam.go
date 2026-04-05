package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// IAMHandler serves IAM role binding and WIF identity resolution endpoints.
type IAMHandler struct {
	store    registry.Store
	wif      *auth.WIFValidator
}

// NewIAMHandler creates an IAM handler.
func NewIAMHandler(store registry.Store, wif *auth.WIFValidator) *IAMHandler {
	return &IAMHandler{store: store, wif: wif}
}

// ListRoleBindings returns all group→role mappings.
// GET /api/v1/iam/role-bindings
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

// GetRoleBinding returns a single role binding.
// GET /api/v1/iam/role-bindings/{id}
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

// CreateRoleBinding creates a new group→role mapping.
// POST /api/v1/iam/role-bindings
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

	// Validate role
	switch rb.Role {
	case models.RoleAdmin, models.RoleDeployer, models.RoleViewer, models.RoleAgent:
		// valid
	default:
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST",
			fmt.Sprintf("invalid role %q; must be one of: admin, deployer, viewer, agent", rb.Role))
		return
	}

	// Generate ID if not provided
	if rb.ID == "" {
		rb.ID = fmt.Sprintf("rb-%s-%s", rb.Group, rb.Role)
	}

	rb.CreatedAt = time.Now()
	rb.CreatedBy = extractOwner(r)

	// If no explicit scopes provided, populate from role defaults
	if len(rb.Scopes) == 0 {
		rb.Scopes = models.DefaultRoleScopes[rb.Role]
	}

	if err := h.store.PutRoleBinding(r.Context(), &rb); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusCreated, rb)
}

// UpdateRoleBinding updates an existing role binding.
// PUT /api/v1/iam/role-bindings/{id}
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

	// Preserve immutable fields
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

// DeleteRoleBinding removes a group→role mapping.
// DELETE /api/v1/iam/role-bindings/{id}
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
// resolves group→role mappings, syncs Dimension B scopes, and returns the result.
// GET /api/v1/iam/whoami
func (h *IAMHandler) ResolveIdentity(w http.ResponseWriter, r *http.Request) {
	identity, err := h.wif.ExtractIdentity(r)
	if err != nil {
		Error(w, r, http.StatusUnauthorized, "AUTH_ERROR", err.Error())
		return
	}

	access, err := h.wif.SyncUserScopes(r.Context(), identity)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "RESOLVE_ERROR",
			"failed to resolve access: "+err.Error())
		return
	}

	JSON(w, http.StatusOK, access)
}

// ListRoles returns the available AIPlex roles and their default scopes.
// GET /api/v1/iam/roles
func (h *IAMHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	type roleInfo struct {
		Role          models.IAMRole `json:"role"`
		DefaultScopes []string       `json:"default_scopes"`
	}

	roles := []roleInfo{
		{Role: models.RoleAdmin, DefaultScopes: models.DefaultRoleScopes[models.RoleAdmin]},
		{Role: models.RoleDeployer, DefaultScopes: models.DefaultRoleScopes[models.RoleDeployer]},
		{Role: models.RoleViewer, DefaultScopes: models.DefaultRoleScopes[models.RoleViewer]},
		{Role: models.RoleAgent, DefaultScopes: models.DefaultRoleScopes[models.RoleAgent]},
	}
	JSON(w, http.StatusOK, roles)
}

// ValidateWIFPrincipal validates a WIF principal string without creating any resources.
// POST /api/v1/iam/validate-principal
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
			"valid":   false,
			"error":   err.Error(),
		})
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"valid":     true,
		"principal": req.Principal,
	})
}

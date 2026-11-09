package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// IAMHandler serves IAM role binding and WIF identity resolution endpoints.
type IAMHandler struct {
	store registry.RoleBindingStore
	wif   *auth.WIFValidator
}

// NewIAMHandler creates an IAM handler.
func NewIAMHandler(store registry.RoleBindingStore, wif *auth.WIFValidator) *IAMHandler {
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
	if err := decodeAuthJSON(r, &rb, true); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if err := validateIAMRoleBinding(&rb); err != nil {
		Error(w, r, 400, "BAD_REQUEST", err.Error())
		return
	}
	if rb.ID == "" {
		// Bare group labels are not globally unique across configured issuers.
		raw, _ := json.Marshal([]string{rb.Provider, rb.PoolID, rb.Group, string(rb.Role)})
		digest := sha256.Sum256(raw)
		rb.ID = "rb-" + hex.EncodeToString(digest[:16])
	}
	if strings.ContainsAny(rb.ID, "/\\\x00\r\n") || len(rb.ID) > 256 {
		Error(w, r, 400, "BAD_REQUEST", "invalid role binding ID")
		return
	}
	if _, err := h.store.GetRoleBinding(r.Context(), rb.ID); err == nil {
		Error(w, r, 409, "CONFLICT", "role binding already exists")
		return
	} else if !errors.Is(err, registry.ErrNotFound) {
		Error(w, r, 500, "STORE_ERROR", "role bindings unavailable")
		return
	}

	rb.CreatedAt = time.Now()
	rb.CreatedBy = extractOwner(r)

	// If no explicit scopes provided, populate from role defaults
	if rb.Scopes == nil {
		rb.Scopes = append([]string{}, models.DefaultRoleScopes[rb.Role]...)
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

	var fields map[string]json.RawMessage
	if decodeAuthJSON(r, &fields, false) != nil {
		Error(w, r, 400, "BAD_REQUEST", "invalid request body")
		return
	}
	if fields == nil {
		Error(w, r, 400, "BAD_REQUEST", "role binding object is required")
		return
	}
	for key := range fields {
		switch key {
		case "id", "group", "role", "provider", "pool_id", "scopes", "description", "created_at", "created_by":
		default:
			Error(w, r, 400, "BAD_REQUEST", "unknown role binding field")
			return
		}
	}
	raw, _ := json.Marshal(fields)
	var update models.RoleBinding
	if json.Unmarshal(raw, &update) != nil {
		Error(w, r, 400, "BAD_REQUEST", "invalid role binding fields")
		return
	}
	update.ID, update.CreatedAt, update.CreatedBy = existing.ID, existing.CreatedAt, existing.CreatedBy
	if _, present := fields["group"]; !present {
		update.Group = existing.Group
	}
	if _, present := fields["role"]; !present {
		update.Role = existing.Role
	}
	if _, present := fields["provider"]; !present {
		update.Provider = existing.Provider
	}
	if _, present := fields["pool_id"]; !present {
		update.PoolID = existing.PoolID
	}
	if _, present := fields["description"]; !present {
		update.Description = existing.Description
	}
	if _, present := fields["scopes"]; !present {
		update.Scopes = existing.Scopes
		if update.Role != existing.Role {
			update.Scopes = append([]string{}, models.DefaultRoleScopes[update.Role]...)
		}
	}
	if err := validateIAMRoleBinding(&update); err != nil {
		Error(w, r, 400, "BAD_REQUEST", err.Error())
		return
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
	if err := decodeAuthJSON(r, &req, true); err != nil {
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

func validateIAMRoleBinding(binding *models.RoleBinding) error {
	if strings.TrimSpace(binding.Group) == "" || len(binding.Group) > 512 || strings.ContainsAny(binding.Group, "\x00\r\n") {
		return fmt.Errorf("bounded group identity is required")
	}
	switch binding.Role {
	case models.RoleAdmin, models.RoleDeployer, models.RoleViewer, models.RoleAgent:
	default:
		return fmt.Errorf("invalid role; expected admin, deployer, viewer or agent")
	}
	if len(binding.Provider) > 2048 || len(binding.PoolID) > 512 || strings.ContainsAny(binding.Provider+binding.PoolID, "\x00\r\n") {
		return fmt.Errorf("invalid identity source restriction")
	}
	if binding.Scopes != nil {
		scopes, err := auth.NormalizeScopes(binding.Scopes)
		if err != nil {
			return err
		}
		binding.Scopes = scopes
	}
	return nil
}

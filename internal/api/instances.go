package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// InstanceHandler serves instance lifecycle endpoints.
type InstanceHandler struct {
	store  registry.Store
	engine *deploy.Engine
}

// NewInstanceHandler creates an instance API handler.
func NewInstanceHandler(store registry.Store, engine *deploy.Engine) *InstanceHandler {
	return &InstanceHandler{store: store, engine: engine}
}

// List returns all instances, optionally filtered by plane.
// GET /api/v1/instances?plane=mcplex
func (h *InstanceHandler) List(w http.ResponseWriter, r *http.Request) {
	plane := models.Plane(r.URL.Query().Get("plane"))
	instances, err := h.store.ListInstances(r.Context(), plane)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, instances)
}

// Get returns a single instance by ID.
// GET /api/v1/instances/{id}
func (h *InstanceHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inst, err := h.store.GetInstance(r.Context(), id)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "instance not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, inst)
}

// Deploy creates a new instance from a template.
// POST /api/v1/instances
func (h *InstanceHandler) Deploy(w http.ResponseWriter, r *http.Request) {
	var req models.DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if req.Plane == "" || req.TemplateID == "" {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "plane and template_id are required")
		return
	}

	owner := extractOwner(r)

	inst, err := h.engine.Deploy(r.Context(), req.Plane, req.TemplateID, req.Config, owner, req.DisplayName)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "DEPLOY_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusCreated, inst)
}

// Undeploy tears down an instance.
// DELETE /api/v1/instances/{id}
func (h *InstanceHandler) Undeploy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	owner := extractOwner(r)

	if err := h.engine.Undeploy(r.Context(), id, owner); err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "instance not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "UNDEPLOY_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// History returns the deploy history for an instance.
// GET /api/v1/instances/{id}/history
func (h *InstanceHandler) History(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	history, err := h.store.ListHistory(r.Context(), id, 50)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, history)
}

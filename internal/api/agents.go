package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// AgentHandler serves agent registration and permission endpoints.
type AgentHandler struct {
	store registry.Store
	hydra *auth.HydraClient
}

// NewAgentHandler creates an agent API handler.
func NewAgentHandler(store registry.Store, hydra ...*auth.HydraClient) *AgentHandler {
	h := &AgentHandler{store: store}
	if len(hydra) > 0 {
		h.hydra = hydra[0]
	}
	return h
}

// List returns all registered agents.
// GET /api/v1/agents
func (h *AgentHandler) List(w http.ResponseWriter, r *http.Request) {
	agents, err := h.store.ListAgents(r.Context())
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, agents)
}

// Get returns a single agent by client ID.
// GET /api/v1/agents/{clientId}
func (h *AgentHandler) Get(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("clientId")
	agent, err := h.store.GetAgent(r.Context(), clientID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "agent not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusOK, agent)
}

// Register creates a new agent (OAuth client).
// POST /api/v1/agents
func (h *AgentHandler) Register(w http.ResponseWriter, r *http.Request) {
	var agent models.Agent
	if err := json.NewDecoder(r.Body).Decode(&agent); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if agent.ClientID == "" || agent.DisplayName == "" {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "client_id and display_name are required")
		return
	}

	// Check if agent already exists
	if _, err := h.store.GetAgent(r.Context(), agent.ClientID); err == nil {
		Error(w, r, http.StatusConflict, "CONFLICT", "agent already exists")
		return
	}

	// Validate WIF principal if provided
	if agent.WIFPrincipal != "" {
		if err := auth.ValidateWIFPrincipal(agent.WIFPrincipal); err != nil {
			Error(w, r, http.StatusBadRequest, "INVALID_WIF_PRINCIPAL", err.Error())
			return
		}
	}

	agent.RegisteredAt = time.Now()
	agent.Status = "active"
	agent.ResourceVersion = 1

	// Create OAuth client in Hydra (non-fatal — agent is registered locally even if Hydra is unavailable)
	if h.hydra != nil {
		grantTypes := agent.GrantTypes
		if len(grantTypes) == 0 {
			grantTypes = []string{"client_credentials"}
		}
		oauthClient := auth.OAuthClient{
			ClientID:                agent.ClientID,
			ClientName:              agent.DisplayName,
			GrantTypes:              grantTypes,
			Scope:                   strings.Join(agent.AllowedScopes, " "),
			RedirectURIs:            agent.RedirectURIs,
			TokenEndpointAuthMethod: "client_secret_basic",
		}
		if err := h.hydra.CreateClient(r.Context(), oauthClient); err != nil {
			// Log but don't fail — Hydra may not be available in dev mode
			zerolog.Ctx(r.Context()).Warn().Err(err).
				Str("client_id", agent.ClientID).
				Msg("failed to create Hydra OAuth client")
		}
	}

	if err := h.store.PutAgent(r.Context(), &agent); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	JSON(w, http.StatusCreated, agent)
}

// Delete removes an agent registration.
// DELETE /api/v1/agents/{clientId}
func (h *AgentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("clientId")

	// Delete OAuth client from Hydra
	if h.hydra != nil {
		if err := h.hydra.DeleteClient(r.Context(), clientID); err != nil {
			zerolog.Ctx(r.Context()).Warn().Err(err).
				Str("client_id", clientID).
				Msg("failed to delete Hydra OAuth client")
		}
	}

	if err := h.store.DeleteAgent(r.Context(), clientID); err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "agent not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetPermissions returns a cross-plane view of an agent's effective permissions.
// GET /api/v1/agents/{clientId}/permissions
func (h *AgentHandler) GetPermissions(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("clientId")
	agent, err := h.store.GetAgent(r.Context(), clientID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "agent not found")
			return
		}
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	// Group scopes by plane
	ceiling := make(map[models.Plane][]models.ScopeInfo)
	for _, scope := range agent.AllowedScopes {
		var plane models.Plane
		switch {
		case len(scope) > 4 && scope[:4] == "mcp:":
			plane = models.PlaneMCPlex
		case len(scope) > 4 && scope[:4] == "a2a:":
			plane = models.PlaneA2APlex
		case len(scope) > 4 && scope[:4] == "llm:":
			plane = models.PlaneLLMPlex
		default:
			continue
		}
		ceiling[plane] = append(ceiling[plane], models.ScopeInfo{
			Scope:       scope,
			Description: scope, // TODO: resolve from Hydra scope metadata
		})
	}

	JSON(w, http.StatusOK, models.AgentPermissions{
		AgentID: clientID,
		Ceiling: ceiling,
	})
}

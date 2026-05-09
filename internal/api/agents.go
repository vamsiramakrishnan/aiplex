package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/capability"
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

	validAuthMethods := map[string]bool{
		"client_credentials": true,
		"authorization_code": true,
		"device_code":        true,
	}
	if !validAuthMethods[agent.AuthMethod] {
		Error(w, r, http.StatusBadRequest, "INVALID_AUTH_METHOD",
			fmt.Sprintf("auth_method must be one of: client_credentials, authorization_code, device_code; got %q", agent.AuthMethod))
		return
	}

	for _, c := range agent.AllowedCaps {
		if _, err := capability.ParseURI(c.URI); err != nil {
			Error(w, r, http.StatusBadRequest, "INVALID_CAPABILITY", err.Error())
			return
		}
	}

	if agent.AuthMethod == "authorization_code" {
		if len(agent.RedirectURIs) == 0 {
			Error(w, r, http.StatusBadRequest, "MISSING_REDIRECT_URIS",
				"authorization_code flow requires at least one redirect_uri")
			return
		}
		for _, uri := range agent.RedirectURIs {
			if !strings.HasPrefix(uri, "https://") && !strings.HasPrefix(uri, "http://localhost") {
				Error(w, r, http.StatusBadRequest, "INVALID_REDIRECT_URI",
					fmt.Sprintf("redirect_uri %q must use HTTPS (or http://localhost for dev)", uri))
				return
			}
		}
	}

	if _, err := h.store.GetAgent(r.Context(), agent.ClientID); err == nil {
		Error(w, r, http.StatusConflict, "CONFLICT", "agent already exists")
		return
	}

	if agent.WIFPrincipal != "" {
		if err := auth.ValidateWIFPrincipal(agent.WIFPrincipal); err != nil {
			Error(w, r, http.StatusBadRequest, "INVALID_WIF_PRINCIPAL", err.Error())
			return
		}
	}

	agent.RegisteredAt = time.Now()
	agent.Status = "active"
	agent.ResourceVersion = 1

	if h.hydra != nil {
		grantTypes := agent.GrantTypes
		if len(grantTypes) == 0 {
			grantTypes = []string{"client_credentials"}
		}
		oauthClient := auth.OAuthClient{
			ClientID:                agent.ClientID,
			ClientName:              agent.DisplayName,
			GrantTypes:              grantTypes,
			Scope:                   strings.Join(agent.AllowedCaps.URIs(), " "),
			RedirectURIs:            agent.RedirectURIs,
			TokenEndpointAuthMethod: "client_secret_basic",
		}
		resp, err := h.hydra.CreateClient(r.Context(), oauthClient)
		if err != nil {
			zerolog.Ctx(r.Context()).Warn().Err(err).
				Str("client_id", agent.ClientID).
				Msg("failed to create Hydra OAuth client")
		} else if resp != nil {
			agent.ClientSecret = resp.ClientSecret
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

// GetPermissions returns a cross-kind view of an agent's effective ceiling.
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

	descs := h.capDescriptions(r.Context())

	ceiling := make(map[capability.Kind][]models.CapabilityInfo)
	for _, c := range agent.AllowedCaps {
		uri, err := capability.ParseURI(c.URI)
		if err != nil {
			continue
		}
		desc := descs[c.URI]
		if desc == "" {
			desc = humanizeCap(uri)
		}
		ceiling[uri.Kind] = append(ceiling[uri.Kind], models.CapabilityInfo{
			URI:         c.URI,
			Actions:     c.Actions,
			Description: desc,
		})
	}

	JSON(w, http.StatusOK, models.AgentPermissions{
		AgentID: clientID,
		Ceiling: ceiling,
	})
}

// capDescriptions builds a URI→description map from all known templates.
func (h *AgentHandler) capDescriptions(ctx context.Context) map[string]string {
	out := map[string]string{}
	for _, kind := range capability.AllKinds() {
		templates, _, err := h.store.ListTemplates(ctx, kind, 0, 1000)
		if err != nil {
			continue
		}
		for _, t := range templates {
			for _, c := range t.Capabilities {
				if c.Description != "" {
					out[c.URI] = c.Description
				} else {
					out[c.URI] = fmt.Sprintf("%s — %s", t.Name, c.Name)
				}
			}
		}
	}
	return out
}

// humanizeCap renders a capability URI as a fallback description.
func humanizeCap(u capability.URI) string {
	return fmt.Sprintf("%s: %s@%s", u.Kind, u.Name, u.Version)
}

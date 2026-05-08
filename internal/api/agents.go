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

	// Validate auth method
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

	// Validate scope format
	for _, scope := range agent.AllowedScopes {
		if !strings.HasPrefix(scope, "mcp:") && !strings.HasPrefix(scope, "a2a:") && !strings.HasPrefix(scope, "llm:") {
			Error(w, r, http.StatusBadRequest, "INVALID_SCOPE",
				fmt.Sprintf("scope %q must start with mcp:, a2a:, or llm:", scope))
			return
		}
	}

	// Validate redirect URIs for authorization_code flow
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
		resp, err := h.hydra.CreateClient(r.Context(), oauthClient)
		if err != nil {
			// Log but don't fail — Hydra may not be available in dev mode
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

	descs := h.scopeDescriptions(r.Context())

	// Group scopes by plane
	ceiling := make(map[models.Plane][]models.ScopeInfo)
	for _, scope := range agent.AllowedScopes {
		var plane models.Plane
		switch {
		case strings.HasPrefix(scope, "mcp:"):
			plane = models.PlaneMCPlex
		case strings.HasPrefix(scope, "a2a:"):
			plane = models.PlaneA2APlex
		case strings.HasPrefix(scope, "llm:"):
			plane = models.PlaneLLMPlex
		default:
			continue
		}
		desc := descs[scope]
		if desc == "" {
			desc = humanizeScope(scope)
		}
		ceiling[plane] = append(ceiling[plane], models.ScopeInfo{
			Scope:       scope,
			Description: desc,
		})
	}

	JSON(w, http.StatusOK, models.AgentPermissions{
		AgentID: clientID,
		Ceiling: ceiling,
	})
}

// scopeDescriptions builds a scope→description map from all known templates.
// Tools, task types, models, and capabilities all become entries; scopes
// without template metadata fall back to humanizeScope.
func (h *AgentHandler) scopeDescriptions(ctx context.Context) map[string]string {
	out := map[string]string{}
	planes := []models.Plane{models.PlaneMCPlex, models.PlaneA2APlex, models.PlaneLLMPlex}
	for _, plane := range planes {
		templates, _, err := h.store.ListTemplates(ctx, plane, 1, 1000)
		if err != nil {
			continue
		}
		for _, t := range templates {
			for _, tool := range t.Tools {
				if tool.Description != "" {
					out["mcp:tools:"+tool.Name] = tool.Description
				}
			}
			for _, task := range t.TaskTypes {
				if _, exists := out["a2a:task:"+task]; !exists {
					out["a2a:task:"+task] = fmt.Sprintf("%s — task type %q", t.Name, task)
				}
			}
			if t.ModelID != "" {
				label := t.Name
				if t.Provider != "" {
					label = fmt.Sprintf("%s (%s)", t.Name, t.Provider)
				}
				out["llm:model:"+t.ModelID] = label
			}
			for _, cap := range t.Capabilities {
				out["llm:capability:"+cap] = "Capability: " + cap
			}
		}
	}
	return out
}

// humanizeScope renders a scope string into a fallback description when no
// template metadata is available (e.g. "mcp:tools:search_curriculum" →
// "MCP tool: search_curriculum").
func humanizeScope(scope string) string {
	parts := strings.SplitN(scope, ":", 3)
	if len(parts) < 3 {
		return scope
	}
	plane, kind, name := parts[0], parts[1], parts[2]
	switch plane {
	case "mcp":
		return fmt.Sprintf("MCP %s: %s", strings.TrimSuffix(kind, "s"), name)
	case "a2a":
		return fmt.Sprintf("A2A %s: %s", kind, name)
	case "llm":
		return fmt.Sprintf("LLM %s: %s", kind, name)
	}
	return scope
}

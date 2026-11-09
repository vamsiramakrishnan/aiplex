package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
)

// AgentHandler serves agent registration and permission endpoints.
type AgentHandler struct {
	store registry.AgentStore
	hydra *auth.HydraClient
}

// NewAgentHandler creates an agent API handler.
func NewAgentHandler(store registry.AgentStore, hydra ...*auth.HydraClient) *AgentHandler {
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
	configurationETag(w, agent.Revision)
	JSON(w, http.StatusOK, agent)
}

// Register creates a new agent (OAuth client).
// POST /api/v1/agents
func (h *AgentHandler) Register(w http.ResponseWriter, r *http.Request) {
	var agent models.Agent
	if err := decodeAuthJSON(r, &agent, true); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if !agentClientID.MatchString(agent.ClientID) || agent.ClientID == "." || agent.ClientID == ".." || strings.TrimSpace(agent.DisplayName) == "" {
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

	var err error
	agent.AllowedScopes, err = auth.NormalizeScopes(agent.AllowedScopes)
	if err != nil {
		Error(w, r, 400, "INVALID_SCOPE", err.Error())
		return
	}
	for _, scope := range agent.AllowedScopes {
		if !strings.HasPrefix(scope, "mcp:") && !strings.HasPrefix(scope, "a2a:") && !strings.HasPrefix(scope, "llm:") && !strings.HasPrefix(scope, "skill:") && !strings.HasPrefix(scope, "aiplex:") {
			Error(w, r, 400, "INVALID_SCOPE", "scope must belong to a configured Aiplex namespace")
			return
		}
	}
	primary := map[string]string{"client_credentials": "client_credentials", "authorization_code": "authorization_code", "device_code": "urn:ietf:params:oauth:grant-type:device_code"}[agent.AuthMethod]
	if len(agent.GrantTypes) == 0 {
		agent.GrantTypes = []string{primary}
	}
	found := false
	seenGrants := map[string]bool{}
	for _, grant := range agent.GrantTypes {
		if seenGrants[grant] || (grant != primary && (grant != "refresh_token" || agent.AuthMethod == "client_credentials")) {
			Error(w, r, 400, "INVALID_GRANT_TYPES", "grant_types must match the selected auth_method")
			return
		}
		seenGrants[grant] = true
		if grant == primary {
			found = true
		}
	}
	if !found {
		Error(w, r, 400, "INVALID_GRANT_TYPES", "grant_types must include the selected auth_method")
		return
	}
	if agent.SpiffeID != "" && !validAgentSPIFFE(agent.SpiffeID) {
		Error(w, r, 400, "INVALID_SPIFFE_ID", "credential-free immutable SPIFFE subject required")
		return
	}
	// Caller-supplied credentials are never persisted or echoed as issued data.
	if agent.ClientSecret != "" {
		Error(w, r, 400, "INVALID_CLIENT_SECRET", "client credentials are issued by the configured authorization server")
		return
	}

	// Validate redirect URIs for authorization_code flow
	if agent.AuthMethod == "authorization_code" {
		if len(agent.RedirectURIs) == 0 {
			Error(w, r, http.StatusBadRequest, "MISSING_REDIRECT_URIS",
				"authorization_code flow requires at least one redirect_uri")
			return
		}
		for _, uri := range agent.RedirectURIs {
			if err := toolauth.ValidateRedirectURI(uri); err != nil {
				Error(w, r, http.StatusBadRequest, "INVALID_REDIRECT_URI",
					fmt.Sprintf("redirect_uri %q must use HTTPS (or http://localhost for dev)", uri))
				return
			}
		}
	}

	versioned, generation, ok := configurationStore(w, r, h.store)
	if !ok {
		return
	}
	expected, ok := configurationExpected(w, r, 0)
	if !ok {
		return
	}
	// Check if agent already exists
	if _, err := h.store.GetAgent(r.Context(), agent.ClientID); err == nil {
		Error(w, r, http.StatusConflict, "CONFLICT", "agent already exists")
		return
	} else if !errors.Is(err, registry.ErrNotFound) {
		Error(w, r, 500, "STORE_ERROR", "agent registry unavailable")
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
	agent.RegisteredBy = extractOwner(r)
	agent.Status = "active"
	agent.ResourceVersion = 1

	// Reserve this identity before an external write. Another replica cannot
	// create the same client or delete credentials belonging to the winning call.
	if h.hydra != nil {
		agent.Status = "provisioning"
	}
	persisted := agent
	if !putConfiguration(w, r, versioned, registry.ConfigAgent, agent.ClientID, &persisted, expected, generation) {
		return
	}
	agent.Revision, agent.ResourceVersion = persisted.Revision, persisted.ResourceVersion
	// A configured authorization server must successfully issue the OAuth client
	// before an active local registration can be persisted.
	if h.hydra != nil {
		grantTypes := agent.GrantTypes
		oauthClient := auth.OAuthClient{
			ClientID:                agent.ClientID,
			ClientName:              agent.DisplayName,
			GrantTypes:              grantTypes,
			Scope:                   strings.Join(agent.AllowedScopes, " "),
			RedirectURIs:            agent.RedirectURIs,
			TokenEndpointAuthMethod: "client_secret_basic",
		}
		if agent.AuthMethod == "authorization_code" {
			oauthClient.ResponseTypes = []string{"code"}
		}
		resp, err := h.hydra.CreateClient(r.Context(), oauthClient)
		if err != nil {
			zerolog.Ctx(r.Context()).Warn().Err(err).Str("client_id", agent.ClientID).Msg("Hydra OAuth client issuance failed")
			Error(w, r, http.StatusBadGateway, "OAUTH_REGISTRATION_FAILED", "authorization server did not issue the client")
			return
		}
		if resp == nil || resp.ClientID != agent.ClientID || resp.ClientSecret == "" {
			Error(w, r, http.StatusBadGateway, "OAUTH_REGISTRATION_FAILED", "authorization server returned incomplete credentials; provisioning claim retained for reconciliation")
			return
		}
		agent.ClientSecret = resp.ClientSecret
	}

	// Secrets are returned once, never stored even by the memory backend.
	if h.hydra != nil {
		persisted = agent
		persisted.ClientSecret = ""
		persisted.Status = "active"
		if !putConfiguration(w, r, versioned, registry.ConfigAgent, agent.ClientID, &persisted, agent.Revision, -1) {
			return
		}
		agent.Status = "active"
		agent.Revision, agent.ResourceVersion = persisted.Revision, persisted.ResourceVersion
	}
	configurationETag(w, agent.Revision)
	JSON(w, http.StatusCreated, agent)
}

// Delete removes an agent registration.
// DELETE /api/v1/agents/{clientId}
func (h *AgentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("clientId")
	versioned, generation, ok := configurationStore(w, r, h.store)
	if !ok {
		return
	}
	agent, err := h.store.GetAgent(r.Context(), clientID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, 404, "NOT_FOUND", "agent not found")
		} else {
			Error(w, r, 500, "STORE_ERROR", "agent registry unavailable")
		}
		return
	}

	expected, ok := configurationExpected(w, r, agent.Revision)
	if !ok {
		return
	}
	agent.Status = "deleting"
	if !putConfiguration(w, r, versioned, registry.ConfigAgent, clientID, agent, expected, generation) {
		return
	}

	// Delete OAuth client from Hydra
	if h.hydra != nil {
		if err := h.hydra.DeleteClient(r.Context(), clientID); err != nil {
			zerolog.Ctx(r.Context()).Warn().Err(err).Str("client_id", clientID).Msg("failed to delete Hydra OAuth client")
			Error(w, r, http.StatusBadGateway, "OAUTH_REVOCATION_FAILED", "authorization server did not revoke the client")
			return
		}
	}

	if err := versioned.DeleteConfiguration(r.Context(), registry.ConfigAgent, clientID, agent.Revision, -1); err != nil {
		configurationError(w, r, err)
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
		case strings.HasPrefix(scope, "skill:"):
			plane = models.PlaneSkillsPlex
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
	planes := []models.Plane{models.PlaneMCPlex, models.PlaneA2APlex, models.PlaneLLMPlex, models.PlaneSkillsPlex}
	for _, plane := range planes {
		for page := 0; ; page++ {
			templates, total, err := h.store.ListTemplates(ctx, plane, page, 1000)
			if err != nil {
				break
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
				for _, skill := range t.Skills {
					key := "skill:invoke:" + skill.Name
					if skill.Description != "" {
						out[key] = skill.Description
					} else if _, exists := out[key]; !exists {
						out[key] = fmt.Sprintf("%s — skill %q", t.Name, skill.Name)
					}
				}
				if t.SkillBundle != "" {
					out["skill:bundle:"+t.SkillBundle] = fmt.Sprintf("%s bundle", t.Name)
				}
			}
			if len(templates) == 0 || (page+1)*1000 >= total {
				break
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
	case "skill":
		return fmt.Sprintf("Skill %s: %s", kind, name)
	}
	return scope
}

var agentClientID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,127}$`)

func validAgentSPIFFE(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "spiffe" || parsed.Host == "" || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" || strings.ContainsAny(raw, "% \r\n\t") || strings.Contains(raw, "\\") {
		return false
	}
	for _, part := range strings.Split(parsed.Path, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return !strings.Contains(parsed.Path, "//")
}

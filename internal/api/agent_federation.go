package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// AgentFederationHandler serves cross-cloud agent identity presets, bindings,
// setup plans and token dry-runs (AgentPlex).
type AgentFederationHandler struct {
	Bindings *agentfederation.Store
	Agents   registry.AgentStore
	Engine   *agentfederation.Engine
	Tenant   TenantResolver
	// DefaultAudience is the AIPlex audience suggested to presets ("" = none).
	DefaultAudience string
	// PublicURL is used in rendered plans.
	PublicURL string
	// LiveEnabled reports whether bindings authenticate live requests.
	LiveEnabled bool
	// ValidateJWKS optionally checks a JWKS URL against outbound policy at write time.
	ValidateJWKS func(string) error
}

// AgentLookup adapts the agent registry for the verification engine.
func AgentLookup(store registry.AgentStore) agentfederation.AgentLookup {
	return func(ctx context.Context, clientID string) (agentfederation.Agent, error) {
		agent, err := store.GetAgent(ctx, clientID)
		if errors.Is(err, registry.ErrNotFound) {
			return agentfederation.Agent{}, agentfederation.ErrNotFound
		}
		if err != nil {
			return agentfederation.Agent{}, err
		}
		return agentfederation.Agent{ClientID: agent.ClientID, Active: agent.Status == "active", ActorSubject: auth.AgentActorSubject(agent)}, nil
	}
}

// Mount registers the routes under /api/v1. Writes require the admin role;
// reads require admin or deployer. POST verify/plan are additionally subject
// to the control-plane write guard (admin or deployer).
func (h *AgentFederationHandler) Mount(r chi.Router) {
	readers := RequireRole(models.RoleAdmin, models.RoleDeployer)
	r.Get("/agents/identity-presets", h.Presets)
	r.With(readers).Post("/agents/identities/verify", h.Verify)
	r.With(readers).Get("/agents/{clientId}/identities", h.List)
	r.With(readers).Get("/agents/{clientId}/identities/{id}", h.Get)
	r.With(RequireRole(models.RoleAdmin)).Put("/agents/{clientId}/identities/{id}", h.Put)
	r.With(RequireRole(models.RoleAdmin)).Delete("/agents/{clientId}/identities/{id}", h.Delete)
	r.With(readers).Post("/agents/{clientId}/identities/{id}/plan", h.Plan)
}

type presetCatalog struct {
	Presets         []agentfederation.Preset `json:"presets"`
	DefaultAudience string                   `json:"default_audience,omitempty"`
	LiveEnabled     bool                     `json:"live_authentication"`
}

// Presets GET /api/v1/agents/identity-presets
func (h *AgentFederationHandler) Presets(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, presetCatalog{Presets: agentfederation.Catalog(h.DefaultAudience), DefaultAudience: h.DefaultAudience, LiveEnabled: h.LiveEnabled})
}

// BindingView is the API representation of a binding with the canonical
// identities a matching token authenticates as.
type BindingView struct {
	agentfederation.Binding
	PrincipalID string `json:"principal_id"`
	ActorID     string `json:"actor_id,omitempty"`
}

func (h *AgentFederationHandler) view(ctx context.Context, tenant string, b agentfederation.Binding) BindingView {
	v := BindingView{Binding: b, PrincipalID: auth.CanonicalPrincipalID(b.Issuer, auth.FederationPoolID(tenant), b.Subject)}
	if h.Agents != nil {
		if agent, err := h.Agents.GetAgent(ctx, b.ClientID); err == nil {
			v.ActorID = auth.CanonicalPrincipalID(b.Issuer, auth.FederationPoolID(tenant), auth.AgentActorSubject(agent))
		}
	}
	return v
}

func (h *AgentFederationHandler) tenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h.Tenant == nil {
		Error(w, r, 403, "TENANT_REQUIRED", "verified tenant binding required")
		return "", false
	}
	t, err := h.Tenant(r)
	if err != nil || t == "" {
		Error(w, r, 403, "TENANT_REQUIRED", "verified tenant binding required")
		return "", false
	}
	return t, true
}

func federationParam(r *http.Request, name string) string {
	if v := chi.URLParam(r, name); v != "" {
		return v
	}
	return r.PathValue(name)
}

func (h *AgentFederationHandler) bindingError(w http.ResponseWriter, r *http.Request, err error) {
	var dup *agentfederation.DuplicateSubjectError
	switch {
	case errors.As(err, &dup):
		Error(w, r, http.StatusConflict, "SUBJECT_ALREADY_BOUND", dup.Error())
	case errors.Is(err, resources.ErrNotFound):
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "identity binding not found")
	case errors.Is(err, resources.ErrConflict):
		Error(w, r, http.StatusConflict, "REVISION_CONFLICT", "identity binding revision changed; send the current revision in If-Match")
	default:
		Error(w, r, http.StatusInternalServerError, "RESOURCE_ERROR", "identity binding store unavailable")
	}
}

func setRevision(w http.ResponseWriter, revision uint64) {
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", revision))
}

// List GET /api/v1/agents/{clientId}/identities
func (h *AgentFederationHandler) List(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	bindings, err := h.Bindings.List(r.Context(), tenant, federationParam(r, "clientId"))
	if err != nil {
		h.bindingError(w, r, err)
		return
	}
	out := make([]BindingView, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, h.view(r.Context(), tenant, b))
	}
	JSON(w, http.StatusOK, out)
}

// Get GET /api/v1/agents/{clientId}/identities/{id}
func (h *AgentFederationHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	b, err := h.Bindings.Get(r.Context(), tenant, federationParam(r, "clientId"), federationParam(r, "id"))
	if err != nil {
		h.bindingError(w, r, err)
		return
	}
	setRevision(w, b.Revision)
	JSON(w, http.StatusOK, h.view(r.Context(), tenant, b))
}

func strictJSON(r *http.Request, limit int64, value any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return fmt.Errorf("bounded JSON body required")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid JSON body")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing JSON body")
	}
	return nil
}

// Put PUT /api/v1/agents/{clientId}/identities/{id}
// Without If-Match it creates; with If-Match it replaces that revision.
func (h *AgentFederationHandler) Put(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	clientID, id := federationParam(r, "clientId"), federationParam(r, "id")
	expected, err := revision(r)
	if err != nil {
		Error(w, r, 400, "INVALID_REVISION", "If-Match must be a revision")
		return
	}
	var req agentfederation.BindingRequest
	if err := strictJSON(r, 64<<10, &req); err != nil {
		Error(w, r, 400, "INVALID_BINDING", err.Error())
		return
	}
	binding, err := agentfederation.NewBinding(clientID, id, req, h.DefaultAudience)
	if err != nil {
		Error(w, r, 400, "INVALID_BINDING", err.Error())
		return
	}
	if h.ValidateJWKS != nil {
		if err := h.ValidateJWKS(binding.JWKSURL); err != nil {
			Error(w, r, 400, "INVALID_BINDING", "JWKS URL rejected by outbound endpoint policy: "+err.Error())
			return
		}
	}
	if h.Agents == nil {
		Error(w, r, 503, "AGENT_REGISTRY_UNAVAILABLE", "agent registry unavailable")
		return
	}
	agent, err := h.Agents.GetAgent(r.Context(), clientID)
	if errors.Is(err, registry.ErrNotFound) {
		Error(w, r, 404, "AGENT_NOT_FOUND", "register the agent before binding an external identity to it")
		return
	} else if err != nil {
		Error(w, r, 503, "AGENT_REGISTRY_UNAVAILABLE", "agent registry unavailable")
		return
	}
	if agent.Status != "active" {
		Error(w, r, 409, "AGENT_NOT_ACTIVE", "agent registration is not active")
		return
	}
	saved, err := h.Bindings.Put(r.Context(), tenant, binding, expected)
	if err != nil {
		h.bindingError(w, r, err)
		return
	}
	setRevision(w, saved.Revision)
	status := http.StatusOK
	if expected == 0 {
		status = http.StatusCreated
	}
	JSON(w, status, h.view(r.Context(), tenant, saved))
}

// Delete DELETE /api/v1/agents/{clientId}/identities/{id} (If-Match required)
func (h *AgentFederationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	expected, err := revision(r)
	if err != nil || expected == 0 {
		Error(w, r, 428, "REVISION_REQUIRED", "If-Match revision required")
		return
	}
	if err := h.Bindings.Delete(r.Context(), tenant, federationParam(r, "clientId"), federationParam(r, "id"), expected); err != nil {
		h.bindingError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Plan POST /api/v1/agents/{clientId}/identities/{id}/plan
func (h *AgentFederationHandler) Plan(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	b, err := h.Bindings.Get(r.Context(), tenant, federationParam(r, "clientId"), federationParam(r, "id"))
	if err != nil {
		h.bindingError(w, r, err)
		return
	}
	view := h.view(r.Context(), tenant, b)
	plan, err := agentfederation.RenderPlan(b, agentfederation.PlanContext{APIBaseURL: h.PublicURL, PrincipalID: view.PrincipalID, ActorID: view.ActorID, FederationEnabled: h.LiveEnabled})
	if err != nil {
		Error(w, r, 400, "INVALID_PLAN", err.Error())
		return
	}
	JSON(w, http.StatusOK, plan)
}

// VerifyRequest is the dry-run body. The token is used only in memory.
type VerifyRequest struct {
	Token     string `json:"token"`
	ClientID  string `json:"client_id,omitempty"`
	BindingID string `json:"binding_id,omitempty"`
}

// VerifyResponse never contains the token.
type VerifyResponse struct {
	*agentfederation.Result
	PrincipalID        string `json:"principal_id,omitempty"`
	ActorID            string `json:"actor_id,omitempty"`
	LiveAuthentication bool   `json:"live_authentication"`
}

// Verify POST /api/v1/agents/identities/verify
func (h *AgentFederationHandler) Verify(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	var req VerifyRequest
	if err := strictJSON(r, 80<<10, &req); err != nil {
		// Never echo the body: it may contain the token.
		Error(w, r, 400, "INVALID_REQUEST", "body must be {\"token\": \"...\", \"client_id\"?: \"...\", \"binding_id\"?: \"...\"}")
		return
	}
	if (req.ClientID == "") != (req.BindingID == "") {
		Error(w, r, 400, "INVALID_REQUEST", "client_id and binding_id must be supplied together")
		return
	}
	input := agentfederation.Input{Token: req.Token, Finder: h.Bindings.Tenant(tenant)}
	if req.ClientID != "" {
		b, err := h.Bindings.Get(r.Context(), tenant, req.ClientID, req.BindingID)
		if err != nil {
			h.bindingError(w, r, err)
			return
		}
		input.Pinned = &b
	}
	result := h.Engine.Evaluate(r.Context(), input)
	req.Token = ""
	response := VerifyResponse{Result: result, LiveAuthentication: h.LiveEnabled}
	if matched := result.MatchedBinding(); matched != nil {
		pool := auth.FederationPoolID(tenant)
		response.PrincipalID = auth.CanonicalPrincipalID(matched.Issuer, pool, matched.Subject)
		response.ActorID = auth.CanonicalPrincipalID(matched.Issuer, pool, result.ActorSubject)
	}
	JSON(w, http.StatusOK, response)
}

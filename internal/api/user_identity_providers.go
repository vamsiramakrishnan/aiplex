package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/userfederation"
)

// UserIdentityProviderHandler serves workforce user identity presets, providers,
// setup plans and token dry-runs.
type UserIdentityProviderHandler struct {
	Providers *userfederation.Store
	Engine    *userfederation.Engine
	Tenant    TenantResolver
	// PublicURL is used in rendered plans.
	PublicURL string
	// LiveEnabled reports whether providers authenticate live requests.
	LiveEnabled bool
	// ValidateJWKS optionally checks a JWKS URL against outbound policy at write time.
	ValidateJWKS func(string) error
}

// Mount registers the routes under /api/v1. Writes require the admin role;
// reads, plans and dry-runs require admin or deployer.
func (h *UserIdentityProviderHandler) Mount(r chi.Router) {
	readers := RequireRole(models.RoleAdmin, models.RoleDeployer)
	r.Get("/identity/user-provider-presets", h.Presets)
	r.With(readers).Post("/identity/user-providers/verify", h.Verify)
	r.With(readers).Get("/identity/user-providers", h.List)
	r.With(readers).Get("/identity/user-providers/{id}", h.Get)
	r.With(RequireRole(models.RoleAdmin)).Put("/identity/user-providers/{id}", h.Put)
	r.With(RequireRole(models.RoleAdmin)).Delete("/identity/user-providers/{id}", h.Delete)
	r.With(readers).Post("/identity/user-providers/{id}/plan", h.Plan)
}

type userPresetCatalog struct {
	Presets     []userfederation.Preset `json:"presets"`
	LiveEnabled bool                    `json:"live_authentication"`
}

// Presets GET /api/v1/identity/user-provider-presets
func (h *UserIdentityProviderHandler) Presets(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, userPresetCatalog{Presets: userfederation.Catalog(), LiveEnabled: h.LiveEnabled})
}

// UserProviderView is the API representation of a provider with the identity
// pool its users authenticate in.
type UserProviderView struct {
	userfederation.Provider
	// PrincipalPool is the pool ID to scope role bindings to; principal IDs are
	// CanonicalPrincipalID(issuer, principal_pool, subject).
	PrincipalPool string `json:"principal_pool"`
}

func (h *UserIdentityProviderHandler) view(tenant string, p userfederation.Provider) UserProviderView {
	return UserProviderView{Provider: p, PrincipalPool: auth.UserFederationPoolID(tenant)}
}

func (h *UserIdentityProviderHandler) tenant(w http.ResponseWriter, r *http.Request) (string, bool) {
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

func (h *UserIdentityProviderHandler) providerError(w http.ResponseWriter, r *http.Request, err error) {
	var dup *userfederation.DuplicateSourceError
	switch {
	case errors.As(err, &dup):
		Error(w, r, http.StatusConflict, "SOURCE_ALREADY_CONFIGURED", dup.Error())
	case errors.Is(err, resources.ErrNotFound):
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "user identity provider not found")
	case errors.Is(err, resources.ErrConflict):
		Error(w, r, http.StatusConflict, "REVISION_CONFLICT", "user identity provider revision changed; send the current revision in If-Match")
	default:
		Error(w, r, http.StatusInternalServerError, "RESOURCE_ERROR", "user identity provider store unavailable")
	}
}

// List GET /api/v1/identity/user-providers
func (h *UserIdentityProviderHandler) List(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	providers, err := h.Providers.List(r.Context(), tenant)
	if err != nil {
		h.providerError(w, r, err)
		return
	}
	out := make([]UserProviderView, 0, len(providers))
	for _, p := range providers {
		out = append(out, h.view(tenant, p))
	}
	JSON(w, http.StatusOK, out)
}

// Get GET /api/v1/identity/user-providers/{id}
func (h *UserIdentityProviderHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	p, err := h.Providers.Get(r.Context(), tenant, federationParam(r, "id"))
	if err != nil {
		h.providerError(w, r, err)
		return
	}
	setRevision(w, p.Revision)
	JSON(w, http.StatusOK, h.view(tenant, p))
}

// Put PUT /api/v1/identity/user-providers/{id}
// Without If-Match it creates; with If-Match it replaces that revision.
func (h *UserIdentityProviderHandler) Put(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	expected, err := revision(r)
	if err != nil {
		Error(w, r, 400, "INVALID_REVISION", "If-Match must be a revision")
		return
	}
	var req userfederation.ProviderRequest
	if err := strictJSON(r, 64<<10, &req); err != nil {
		Error(w, r, 400, "INVALID_PROVIDER", err.Error())
		return
	}
	provider, err := userfederation.NewProvider(federationParam(r, "id"), req)
	if err != nil {
		Error(w, r, 400, "INVALID_PROVIDER", err.Error())
		return
	}
	if h.ValidateJWKS != nil {
		if err := h.ValidateJWKS(provider.JWKSURL); err != nil {
			Error(w, r, 400, "INVALID_PROVIDER", "JWKS URL rejected by outbound endpoint policy: "+err.Error())
			return
		}
	}
	saved, err := h.Providers.Put(r.Context(), tenant, provider, expected)
	if err != nil {
		h.providerError(w, r, err)
		return
	}
	setRevision(w, saved.Revision)
	status := http.StatusOK
	if expected == 0 {
		status = http.StatusCreated
	}
	JSON(w, status, h.view(tenant, saved))
}

// Delete DELETE /api/v1/identity/user-providers/{id} (If-Match required)
func (h *UserIdentityProviderHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	expected, err := revision(r)
	if err != nil || expected == 0 {
		Error(w, r, 428, "REVISION_REQUIRED", "If-Match revision required")
		return
	}
	if err := h.Providers.Delete(r.Context(), tenant, federationParam(r, "id"), expected); err != nil {
		h.providerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Plan POST /api/v1/identity/user-providers/{id}/plan
func (h *UserIdentityProviderHandler) Plan(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	p, err := h.Providers.Get(r.Context(), tenant, federationParam(r, "id"))
	if err != nil {
		h.providerError(w, r, err)
		return
	}
	plan, err := userfederation.RenderPlan(p, userfederation.PlanContext{APIBaseURL: h.PublicURL, PrincipalPool: auth.UserFederationPoolID(tenant), FederationEnabled: h.LiveEnabled})
	if err != nil {
		Error(w, r, 400, "INVALID_PLAN", err.Error())
		return
	}
	JSON(w, http.StatusOK, plan)
}

// UserVerifyRequest is the dry-run body. The token is used only in memory.
type UserVerifyRequest struct {
	Token      string `json:"token"`
	ProviderID string `json:"provider_id,omitempty"`
}

// UserVerifyResponse never contains the token.
type UserVerifyResponse struct {
	*userfederation.Result
	PrincipalID        string `json:"principal_id,omitempty"`
	PrincipalPool      string `json:"principal_pool,omitempty"`
	IsWorkforce        bool   `json:"is_workforce"`
	LiveAuthentication bool   `json:"live_authentication"`
}

// Verify POST /api/v1/identity/user-providers/verify
func (h *UserIdentityProviderHandler) Verify(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	var req UserVerifyRequest
	if err := strictJSON(r, 80<<10, &req); err != nil {
		// Never echo the body: it may contain the token.
		Error(w, r, 400, "INVALID_REQUEST", "body must be {\"token\": \"...\", \"provider_id\"?: \"...\"}")
		return
	}
	input := userfederation.Input{Token: req.Token, Finder: h.Providers.Tenant(tenant)}
	if req.ProviderID != "" {
		p, err := h.Providers.Get(r.Context(), tenant, req.ProviderID)
		if err != nil {
			h.providerError(w, r, err)
			return
		}
		input.Pinned = &p
	}
	result := h.Engine.Evaluate(r.Context(), input)
	req.Token = ""
	response := UserVerifyResponse{Result: result, LiveAuthentication: h.LiveEnabled}
	if provider := result.MatchedProvider(); provider != nil {
		pool := auth.UserFederationPoolID(tenant)
		response.PrincipalID = auth.CanonicalPrincipalID(provider.Issuer, pool, result.VerifiedSubject())
		response.PrincipalPool = pool
		response.IsWorkforce = true
	}
	JSON(w, http.StatusOK, response)
}

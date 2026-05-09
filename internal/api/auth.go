package api

import (
	"encoding/json"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// AuthHandler serves Ory Hydra webhook endpoints.
type AuthHandler struct {
	hydra   *auth.HydraClient
	consent *auth.ConsentHandler
	store   registry.Store
}

// NewAuthHandler creates auth webhook handlers.
func NewAuthHandler(hydra *auth.HydraClient, store registry.Store) *AuthHandler {
	return &AuthHandler{
		hydra:   hydra,
		consent: auth.NewConsentHandler(hydra, store),
		store:   store,
	}
}

// ConsentGet handles the consent challenge redirect from Hydra. Returns the
// agent + user ceilings + the grantable subset (A ∩ B ∩ C) so the Console can
// render the consent UI.
//
// GET /auth/consent
func (h *AuthHandler) ConsentGet(w http.ResponseWriter, r *http.Request) {
	challenge := r.URL.Query().Get("consent_challenge")
	if challenge == "" {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "missing consent_challenge")
		return
	}

	cr, err := h.hydra.GetConsentRequest(r.Context(), challenge)
	if err != nil {
		Error(w, r, http.StatusBadGateway, "HYDRA_ERROR", "failed to fetch consent request: "+err.Error())
		return
	}

	agent, err := h.store.GetAgent(r.Context(), cr.Client.ClientID)
	if err != nil {
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "agent not registered: "+cr.Client.ClientID)
		return
	}

	userCaps, _ := h.store.GetUserCaps(r.Context(), cr.Subject)

	// Hydra's `requested_scope` carries cap URIs.
	requested := make(capability.CapSet, 0, len(cr.RequestedScope))
	for _, uri := range cr.RequestedScope {
		requested = append(requested, capability.Cap{URI: uri})
	}

	grantable := agent.AllowedCaps.Intersect(userCaps).Intersect(requested)

	JSON(w, http.StatusOK, map[string]any{
		"challenge":       challenge,
		"subject":         cr.Subject,
		"client_id":       cr.Client.ClientID,
		"client_name":     agent.DisplayName,
		"requested_caps":  requested,
		"grantable_caps":  grantable,
	})
}

// ConsentAccept processes the user's consent decision.
// POST /auth/consent
func (h *AuthHandler) ConsentAccept(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Challenge string `json:"challenge"`
		Deny      bool   `json:"deny"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}

	if req.Deny {
		redirectURL, err := h.hydra.RejectConsent(r.Context(), req.Challenge, "user_denied", "User denied consent")
		if err != nil {
			Error(w, r, http.StatusBadGateway, "HYDRA_ERROR", "consent rejection failed: "+err.Error())
			return
		}
		JSON(w, http.StatusOK, map[string]string{"redirect_to": redirectURL})
		return
	}

	redirectURL, err := h.consent.HandleConsent(r.Context(), req.Challenge)
	if err != nil {
		Error(w, r, http.StatusBadGateway, "HYDRA_ERROR", "consent acceptance failed: "+err.Error())
		return
	}

	JSON(w, http.StatusOK, map[string]string{"redirect_to": redirectURL})
}

// TokenHook is called by Hydra before issuing each token.
// It injects the RFC 8693 actor claim with the agent's SPIFFE ID.
// POST /auth/token-hook
func (h *AuthHandler) TokenHook(w http.ResponseWriter, r *http.Request) {
	var hookReq struct {
		Subject string `json:"subject"`
		Client  struct {
			ClientID string `json:"client_id"`
		} `json:"client"`
		GrantedScopes []string `json:"granted_scopes"`
		Session       struct {
			AccessToken map[string]any `json:"access_token"`
		} `json:"session"`
	}
	if err := json.NewDecoder(r.Body).Decode(&hookReq); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid hook request")
		return
	}

	agent, err := h.store.GetAgent(r.Context(), hookReq.Client.ClientID)
	if err != nil {
		JSON(w, http.StatusOK, map[string]any{"session": hookReq.Session})
		return
	}

	session := hookReq.Session
	if session.AccessToken == nil {
		session.AccessToken = make(map[string]any)
	}
	session.AccessToken["act"] = map[string]string{
		"sub": agent.SpiffeID,
	}

	JSON(w, http.StatusOK, map[string]any{
		"session": map[string]any{
			"access_token": session.AccessToken,
		},
	})
}

// LoginRedirect handles the Kratos login challenge.
// GET /auth/login
func (h *AuthHandler) LoginRedirect(w http.ResponseWriter, r *http.Request) {
	challenge := r.URL.Query().Get("login_challenge")
	if challenge == "" {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "missing login_challenge")
		return
	}
	JSON(w, http.StatusOK, map[string]string{
		"challenge": challenge,
		"login_url": "/ui/login?login_challenge=" + challenge,
	})
}

// GetUserCaps returns Dimension B (user ceiling) for a user.
// GET /auth/users/{userId}/caps
func (h *AuthHandler) GetUserCaps(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userId")
	caps, err := h.store.GetUserCaps(r.Context(), userID)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	byKind := map[capability.Kind]capability.CapSet{}
	for _, c := range caps {
		if u, err := capability.ParseURI(c.URI); err == nil {
			byKind[u.Kind] = append(byKind[u.Kind], c)
		}
	}
	JSON(w, http.StatusOK, map[string]any{
		"user_id": userID,
		"caps":    caps,
		"by_kind": byKind,
	})
}

// SetUserCaps updates Dimension B for a user.
// PUT /auth/users/{userId}/caps
func (h *AuthHandler) SetUserCaps(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userId")
	var req struct {
		Caps capability.CapSet `json:"caps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if err := h.store.SetUserCaps(r.Context(), userID, req.Caps); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

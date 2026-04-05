package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
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

// ConsentGet handles the consent challenge redirect from Hydra.
// Hydra redirects the user here with ?consent_challenge=<challenge>.
// In production, this renders the consent UI in the Console.
// GET /auth/consent
func (h *AuthHandler) ConsentGet(w http.ResponseWriter, r *http.Request) {
	challenge := r.URL.Query().Get("consent_challenge")
	if challenge == "" {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "missing consent_challenge")
		return
	}

	// Fetch consent request details from Hydra
	cr, err := h.hydra.GetConsentRequest(r.Context(), challenge)
	if err != nil {
		Error(w, r, http.StatusBadGateway, "HYDRA_ERROR", "failed to fetch consent request: "+err.Error())
		return
	}

	// Look up agent ceiling (Dimension A) and user ceiling (Dimension B)
	agent, err := h.store.GetAgent(r.Context(), cr.Client.ClientID)
	if err != nil {
		Error(w, r, http.StatusNotFound, "NOT_FOUND", "agent not registered: "+cr.Client.ClientID)
		return
	}

	userScopes, _ := h.store.GetUserScopes(r.Context(), cr.Subject)

	// Compute grantable scopes = A ∩ B ∩ requested
	agentSet := toSet(agent.AllowedScopes)
	userSet := toSet(userScopes)
	var grantable []string
	for _, s := range cr.RequestedScope {
		if agentSet[s] && userSet[s] {
			grantable = append(grantable, s)
		}
	}

	// Return consent details for the Console to render
	JSON(w, http.StatusOK, map[string]any{
		"challenge":        challenge,
		"subject":          cr.Subject,
		"client_id":        cr.Client.ClientID,
		"client_name":      agent.DisplayName,
		"requested_scopes": cr.RequestedScope,
		"grantable_scopes": grantable,
	})
}

// ConsentAccept processes the user's consent decision.
// POST /auth/consent
func (h *AuthHandler) ConsentAccept(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Challenge     string   `json:"challenge"`
		GrantedScopes []string `json:"granted_scopes"`
		Deny          bool     `json:"deny"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}

	if req.Deny {
		// TODO: call hydra.RejectConsent
		JSON(w, http.StatusOK, map[string]string{"redirect_to": "/consent-denied"})
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

	// Look up the agent to get its SPIFFE ID
	agent, err := h.store.GetAgent(r.Context(), hookReq.Client.ClientID)
	if err != nil {
		// Agent not registered — still issue token but without act claim
		JSON(w, http.StatusOK, map[string]any{"session": hookReq.Session})
		return
	}

	// Inject act claim (RFC 8693)
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
// Hydra redirects here when authentication is needed.
// GET /auth/login
func (h *AuthHandler) LoginRedirect(w http.ResponseWriter, r *http.Request) {
	challenge := r.URL.Query().Get("login_challenge")
	if challenge == "" {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "missing login_challenge")
		return
	}

	// In production, redirect to Kratos self-service login UI
	// For now, return the challenge so the Console can handle it
	JSON(w, http.StatusOK, map[string]string{
		"challenge":  challenge,
		"login_url":  "/ui/login?login_challenge=" + challenge,
	})
}

// UserScopes manages Dimension B (user ceiling).
// GET /auth/users/{userId}/scopes
func (h *AuthHandler) GetUserScopes(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userId")
	scopes, err := h.store.GetUserScopes(r.Context(), userID)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	// Group by plane for easier display
	grouped := auth.ScopesByPlane(scopes)
	JSON(w, http.StatusOK, map[string]any{
		"user_id": userID,
		"scopes":  scopes,
		"by_plane": grouped,
	})
}

// SetUserScopes updates Dimension B for a user.
// PUT /auth/users/{userId}/scopes
func (h *AuthHandler) SetUserScopes(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userId")
	var req struct {
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if err := h.store.SetUserScopes(r.Context(), userID, req.Scopes); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[strings.TrimSpace(item)] = true
	}
	return s
}

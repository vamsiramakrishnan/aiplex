package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
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

	if strings.TrimSpace(cr.Subject) == "" || strings.TrimSpace(cr.Client.ClientID) == "" {
		Error(w, r, http.StatusBadGateway, "HYDRA_ERROR", "consent request requires subject and client")
		return
	}
	if access := GetWIFAccess(r.Context()); access != nil && !consentCallerMatches(access, cr.Subject) {
		Error(w, r, http.StatusForbidden, "FORBIDDEN", "consent subject does not match verified caller")
		return
	}
	// Look up agent ceiling (Dimension A) and user ceiling (Dimension B)
	agent, err := h.store.GetAgent(r.Context(), cr.Client.ClientID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, http.StatusNotFound, "NOT_FOUND", "agent not registered")
		} else {
			Error(w, r, http.StatusServiceUnavailable, "STORE_ERROR", "agent policy unavailable")
		}
		return
	}
	if agent.Status != "active" {
		Error(w, r, http.StatusForbidden, "OAUTH_AGENT_DENIED", "registered active agent required")
		return
	}
	var userScopes []string
	if access := GetWIFAccess(r.Context()); access != nil {
		userScopes = access.Scopes
	} else {
		userScopes, err = h.store.GetUserScopes(r.Context(), cr.Subject)
		if err != nil {
			Error(w, r, http.StatusInternalServerError, "STORE_ERROR", "failed to resolve consent scopes")
			return
		}
	}

	// Compute grantable scopes = A ∩ B ∩ requested
	var grantable []string
	for _, s := range cr.RequestedScope {
		if auth.ScopeAllowed(agent.AllowedScopes, s) && auth.ScopeAllowed(userScopes, s) {
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
		Challenge     string    `json:"challenge"`
		GrantedScopes *[]string `json:"granted_scopes"`
		Deny          bool      `json:"deny"`
	}
	if err := decodeAuthJSON(r, &req, true); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}

	if strings.TrimSpace(req.Challenge) == "" {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "consent challenge is required")
		return
	}
	if req.Deny {
		cr, err := h.hydra.GetConsentRequest(r.Context(), req.Challenge)
		if err != nil {
			Error(w, r, http.StatusBadGateway, "HYDRA_ERROR", "failed to fetch consent request")
			return
		}
		if strings.TrimSpace(cr.Subject) == "" || strings.TrimSpace(cr.Client.ClientID) == "" {
			Error(w, r, http.StatusBadGateway, "HYDRA_ERROR", "consent request requires subject and client")
			return
		}
		if access := GetWIFAccess(r.Context()); access != nil && !consentCallerMatches(access, cr.Subject) {
			Error(w, r, http.StatusForbidden, "FORBIDDEN", "consent subject does not match verified caller")
			return
		}
		redirectURL, err := h.hydra.RejectConsent(r.Context(), req.Challenge, "user_denied", "User denied consent")
		if err != nil {
			Error(w, r, http.StatusBadGateway, "HYDRA_ERROR", "consent rejection failed: "+err.Error())
			return
		}
		JSON(w, http.StatusOK, map[string]string{"redirect_to": redirectURL})
		return
	}

	if req.GrantedScopes == nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "granted_scopes is required")
		return
	}
	var redirectURL string
	var err error
	if access := GetWIFAccess(r.Context()); access != nil {
		redirectURL, err = h.consent.HandleConsentForAccess(r.Context(), req.Challenge, *req.GrantedScopes, access)
	} else {
		redirectURL, err = h.consent.HandleConsentScopes(r.Context(), req.Challenge, *req.GrantedScopes)
	}
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
	// Current Hydra sends request.client_id and session.extra; refresh hooks
	// also have requester/top-level forms. Reject conflicting identities rather
	// than resolving an ambiguous hook by field precedence.
	type hookRequest struct {
		ClientID        string   `json:"client_id"`
		GrantedScopes   []string `json:"granted_scopes"`
		RequestedScopes []string `json:"requested_scopes"`
	}
	var input struct {
		ClientID string `json:"client_id"`
		Client   struct {
			ClientID string `json:"client_id"`
		} `json:"client"`
		Request       hookRequest `json:"request"`
		Requester     hookRequest `json:"requester"`
		GrantedScopes []string    `json:"granted_scopes"`
		Session       struct {
			AccessToken map[string]any `json:"access_token"`
			Extra       map[string]any `json:"extra"`
			IDToken     map[string]any `json:"id_token"`
		} `json:"session"`
	}
	if decodeAuthJSON(r, &input, false) != nil {
		Error(w, r, 400, "BAD_REQUEST", "invalid hook request")
		return
	}
	clientID := ""
	for _, id := range []string{input.Request.ClientID, input.Requester.ClientID, input.ClientID, input.Client.ClientID} {
		if id == "" {
			continue
		}
		if clientID != "" && clientID != id {
			Error(w, r, 400, "BAD_REQUEST", "conflicting hook client identities")
			return
		}
		clientID = id
	}
	if clientID == "" {
		Error(w, r, 400, "BAD_REQUEST", "hook client identity required")
		return
	}
	agent, err := h.store.GetAgent(r.Context(), clientID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			Error(w, r, 403, "OAUTH_AGENT_DENIED", "registered active agent required")
		} else {
			Error(w, r, 503, "STORE_ERROR", "agent policy unavailable")
		}
		return
	}
	if agent.Status != "active" {
		Error(w, r, 403, "OAUTH_AGENT_DENIED", "registered active agent required")
		return
	}
	// Check all supplied current/refresh scope coordinates. Older Hydra hooks
	// may leave granted_scopes empty, so requested scopes are checked as well.
	scopes := append([]string(nil), input.GrantedScopes...)
	for _, request := range []hookRequest{input.Request, input.Requester} {
		scopes = append(scopes, request.GrantedScopes...)
		scopes = append(scopes, request.RequestedScopes...)
	}
	if _, err = auth.NormalizeScopes(scopes); err != nil {
		Error(w, r, 400, "BAD_REQUEST", "invalid hook scopes")
		return
	}
	for _, scope := range scopes {
		if !auth.ScopeAllowed(agent.AllowedScopes, scope) {
			Error(w, r, 403, "OAUTH_SCOPE_DENIED", "granted scopes exceed current agent permissions")
			return
		}
	}
	accessToken := input.Session.Extra
	if accessToken == nil {
		accessToken = input.Session.AccessToken
	}
	if accessToken == nil {
		accessToken = map[string]any{}
	}
	accessToken["act"] = map[string]string{"sub": auth.AgentActorSubject(agent)}
	idToken := input.Session.IDToken
	if claims, ok := idToken["id_token_claims"].(map[string]any); ok {
		idToken, _ = claims["ext"].(map[string]any)
	}
	session := map[string]any{"access_token": accessToken}
	if idToken != nil {
		session["id_token"] = idToken
	}
	JSON(w, http.StatusOK, map[string]any{"session": session})
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
		"challenge": challenge,
		"login_url": "/ui/login?login_challenge=" + challenge,
	})
}

// UserScopes manages Dimension B (user ceiling).
// GET /auth/users/{userId}/scopes
func (h *AuthHandler) GetUserScopes(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userId")
	if !auth.IsCanonicalPrincipalID(userID) {
		Error(w, r, 400, "INVALID_PRINCIPAL", "canonical principal ID from iam/whoami is required")
		return
	}
	scopes, err := h.store.GetUserScopes(r.Context(), userID)
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if scopes == nil {
		scopes = []string{}
	}

	// Group by plane for easier display
	grouped := auth.ScopesByPlane(scopes)
	JSON(w, http.StatusOK, map[string]any{
		"user_id":  userID,
		"scopes":   scopes,
		"by_plane": grouped,
	})
}

// SetUserScopes updates Dimension B for a user.
// PUT /auth/users/{userId}/scopes
func (h *AuthHandler) SetUserScopes(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userId")
	if !auth.IsCanonicalPrincipalID(userID) {
		Error(w, r, 400, "INVALID_PRINCIPAL", "canonical principal ID from iam/whoami is required")
		return
	}
	var req struct {
		Scopes *[]string `json:"scopes"`
	}
	if err := decodeAuthJSON(r, &req, true); err != nil {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if req.Scopes == nil {
		Error(w, r, 400, "BAD_REQUEST", "scopes array is required; use an empty array to clear grants")
		return
	}
	normalized, err := auth.NormalizeScopes(*req.Scopes)
	if err != nil {
		Error(w, r, 400, "INVALID_SCOPE", err.Error())
		return
	}
	if err := h.store.SetUserScopes(r.Context(), userID, normalized); err != nil {
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

// Bounded JSON prevents user-facing identity mutations and trusted hook bodies
// from accepting a valid prefix followed by an ignored second request.
func decodeAuthJSON(r *http.Request, destination any, strict bool) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return fmt.Errorf("bounded JSON required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("single JSON request required")
	}
	return nil
}
func consentCallerMatches(access *models.ResolvedAccess, subject string) bool {
	identity := access.Identity
	return identity.Subject != "" && identity.Provider != "" && identity.PrincipalID == auth.CanonicalPrincipalID(identity.Provider, identity.PoolID, identity.Subject) && subject == identity.PrincipalID
}

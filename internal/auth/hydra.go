package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// HydraClient wraps the Ory Hydra Admin API.
type HydraClient struct {
	adminURL   string
	httpClient *http.Client
}

// NewHydraClient creates a Hydra admin client.
func NewHydraClient(adminURL string) *HydraClient {
	return &HydraClient{
		adminURL:   adminURL,
		httpClient: &http.Client{},
	}
}

// OAuthClient represents a Hydra OAuth2 client (maps to an AIPlex agent).
type OAuthClient struct {
	ClientID      string   `json:"client_id"`
	ClientName    string   `json:"client_name"`
	GrantTypes    []string `json:"grant_types"`
	Scope         string   `json:"scope"`
	RedirectURIs  []string `json:"redirect_uris,omitempty"`
	TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
}

// CreateClient registers a new OAuth client in Hydra.
func (h *HydraClient) CreateClient(ctx context.Context, client OAuthClient) error {
	body, _ := json.Marshal(client)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.adminURL+"/admin/clients", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("hydra create client: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("hydra create client: status %d", resp.StatusCode)
	}
	return nil
}

// DeleteClient removes an OAuth client from Hydra.
func (h *HydraClient) DeleteClient(ctx context.Context, clientID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, h.adminURL+"/admin/clients/"+clientID, nil)
	if err != nil {
		return err
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("hydra delete client: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("hydra delete client: status %d", resp.StatusCode)
	}
	return nil
}

// CreateScope registers a scope in Hydra (via scope metadata).
func (h *HydraClient) CreateScope(ctx context.Context, scope, description string) error {
	// Hydra doesn't have a dedicated scope registry — scopes are validated
	// against the client's allowed_scope. This is a no-op placeholder for
	// when we need scope metadata tracking.
	_ = ctx
	_ = scope
	_ = description
	return nil
}

// ConsentRequest represents a Hydra consent challenge.
type ConsentRequest struct {
	Challenge string   `json:"challenge"`
	Subject   string   `json:"subject"`
	Client    struct {
		ClientID string `json:"client_id"`
	} `json:"client"`
	RequestedScope []string `json:"requested_scope"`
}

// GetConsentRequest fetches a consent challenge from Hydra.
func (h *HydraClient) GetConsentRequest(ctx context.Context, challenge string) (*ConsentRequest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		h.adminURL+"/admin/oauth2/auth/requests/consent?consent_challenge="+challenge, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hydra get consent: %w", err)
	}
	defer resp.Body.Close()
	var cr ConsentRequest
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, err
	}
	return &cr, nil
}

// AcceptConsent accepts a consent request with the granted scopes.
func (h *HydraClient) AcceptConsent(ctx context.Context, challenge string, grantedScopes []string, actClaim map[string]string) (string, error) {
	payload := map[string]any{
		"grant_scope": grantedScopes,
		"session": map[string]any{
			"access_token": map[string]any{
				"act": actClaim,
			},
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		h.adminURL+"/admin/oauth2/auth/requests/consent/accept?consent_challenge="+challenge,
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("hydra accept consent: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		RedirectTo string `json:"redirect_to"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.RedirectTo, nil
}

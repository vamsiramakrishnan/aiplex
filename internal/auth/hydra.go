package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
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

// CreateClientResponse contains the response from Hydra client creation.
type CreateClientResponse struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// CreateClient registers a new OAuth client in Hydra and returns the client_secret.
func (h *HydraClient) CreateClient(ctx context.Context, client OAuthClient) (*CreateClientResponse, error) {
	body, _ := json.Marshal(client)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.adminURL+"/admin/clients", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hydra create client: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("hydra create client: status %d: %s", resp.StatusCode, body)
	}

	// Parse response to get client_secret
	var result CreateClientResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("hydra decode response: %w", err)
	}
	return &result, nil
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

// CreateScope is deprecated. Use UpdateClientScopes instead.
func (h *HydraClient) CreateScope(ctx context.Context, scope, description string) error {
	// Hydra doesn't have a dedicated scope registry — scopes are validated
	// against the client's allowed_scope. This is a no-op placeholder for
	// when we need scope metadata tracking.
	_ = ctx
	_ = scope
	_ = description
	return nil
}

// UpdateClientScopes patches an OAuth client's allowed scopes in Hydra.
func (h *HydraClient) UpdateClientScopes(ctx context.Context, clientID string, scopes []string) error {
	patch := map[string]any{
		"scope": strings.Join(scopes, " "),
	}
	data, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "PATCH", h.adminURL+"/admin/clients/"+clientID, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("hydra update scopes: status %d: %s", resp.StatusCode, body)
	}
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

// AcceptConsent accepts a consent request, granting the given capability set.
// The granted caps are emitted in the JWT under both `grant_scope` (URIs only,
// for Hydra's audience-binding) and a structured `caps` claim that policy reads.
func (h *HydraClient) AcceptConsent(ctx context.Context, challenge string, granted capability.CapSet, actClaim map[string]string) (string, error) {
	uris := granted.URIs()
	payload := map[string]any{
		"grant_scope": uris,
		"session": map[string]any{
			"access_token": map[string]any{
				"act":  actClaim,
				"caps": granted,
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

// RejectConsent rejects a consent request.
func (h *HydraClient) RejectConsent(ctx context.Context, challenge, errorID, errorDescription string) (string, error) {
	payload := map[string]any{
		"error":             errorID,
		"error_description": errorDescription,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		h.adminURL+"/admin/oauth2/auth/requests/consent/reject?consent_challenge="+challenge,
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("hydra reject consent: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		RedirectTo string `json:"redirect_to"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.RedirectTo, nil
}

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HydraClient wraps the Ory Hydra Admin API.
type HydraClient struct {
	adminURL   string
	httpClient *http.Client
}

// NewHydraClient creates a Hydra admin client.
func NewHydraClient(adminURL string) *HydraClient {
	return &HydraClient{
		adminURL:   strings.TrimRight(adminURL, "/"),
		httpClient: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

// OAuthClient represents a Hydra OAuth2 client (maps to an AIPlex agent).
type OAuthClient struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	GrantTypes              []string `json:"grant_types"`
	Scope                   string   `json:"scope"`
	RedirectURIs            []string `json:"redirect_uris,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("hydra create client: status %d", resp.StatusCode)
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
	if !agentHydraClientID(clientID) {
		return fmt.Errorf("invalid OAuth client identity")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, h.adminURL+"/admin/clients/"+url.PathEscape(clientID), nil)
	if err != nil {
		return err
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("hydra delete client: %w", err)
	}
	defer resp.Body.Close()
	if (resp.StatusCode < 200 || resp.StatusCode >= 300) && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("hydra delete client: status %d", resp.StatusCode)
	}
	return nil
}

// CreateScope is deprecated. Use UpdateClientScopes instead.
func (h *HydraClient) CreateScope(ctx context.Context, scope, description string) error {
	// Scope discovery cannot implicitly extend an agent's reviewed ceiling.
	return fmt.Errorf("Hydra has no scope registry; update an explicit OAuth client instead")
}

// UpdateClientScopes patches an OAuth client's allowed scopes in Hydra.
func (h *HydraClient) UpdateClientScopes(ctx context.Context, clientID string, scopes []string) error {
	if !agentHydraClientID(clientID) {
		return fmt.Errorf("invalid OAuth client identity")
	}
	normalized, err := NormalizeScopes(scopes)
	if err != nil {
		return err
	}
	patch := []map[string]any{{"op": "replace", "path": "/scope", "value": strings.Join(normalized, " ")}}
	data, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "PATCH", h.adminURL+"/admin/clients/"+url.PathEscape(clientID), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("hydra update scopes: status %d", resp.StatusCode)
	}
	return nil
}

// ConsentRequest represents a Hydra consent challenge.
type ConsentRequest struct {
	Challenge string `json:"challenge"`
	Subject   string `json:"subject"`
	Client    struct {
		ClientID string `json:"client_id"`
	} `json:"client"`
	RequestedScope []string `json:"requested_scope"`
}

// GetConsentRequest fetches a consent challenge from Hydra.
func (h *HydraClient) GetConsentRequest(ctx context.Context, challenge string) (*ConsentRequest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		h.adminURL+"/admin/oauth2/auth/requests/consent?consent_challenge="+url.QueryEscape(challenge), nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hydra get consent: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("hydra get consent: status %d", resp.StatusCode)
	}
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
		h.adminURL+"/admin/oauth2/auth/requests/consent/accept?consent_challenge="+url.QueryEscape(challenge),
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("hydra consent decision: status %d", resp.StatusCode)
	}
	var result struct {
		RedirectTo string `json:"redirect_to"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("hydra consent response: %w", err)
	}
	if result.RedirectTo == "" {
		return "", fmt.Errorf("hydra consent response has no redirect")
	}
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
		h.adminURL+"/admin/oauth2/auth/requests/consent/reject?consent_challenge="+url.QueryEscape(challenge),
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("hydra consent decision: status %d", resp.StatusCode)
	}
	var result struct {
		RedirectTo string `json:"redirect_to"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("hydra consent response: %w", err)
	}
	if result.RedirectTo == "" {
		return "", fmt.Errorf("hydra consent response has no redirect")
	}
	return result.RedirectTo, nil
}

func agentHydraClientID(id string) bool {
	return id != "" && id != "." && id != ".." && len(id) <= 128 && !strings.ContainsAny(id, "/%?#\\ \t\r\n\x00")
}

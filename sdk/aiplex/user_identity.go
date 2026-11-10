package aiplex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/vamsiramakrishnan/aiplex/pkg/userfederation"
)

// Workforce user identity providers. A provider pins an external issuer and
// audience so verified people get canonical principals and, when the provider
// verifies them, group memberships for role bindings and quota eligibility.

// UserIdentityPreset is one entry of the provider preset catalog.
type UserIdentityPreset = userfederation.Preset

// UserIdentityPlan is the rendered setup plan for a provider.
type UserIdentityPlan = userfederation.Plan

// UserIdentityPresetCatalog is the response of the preset catalog route.
type UserIdentityPresetCatalog struct {
	Presets []UserIdentityPreset `json:"presets"`
	// LiveAuthentication is false when providers can be dry-run but do not yet
	// authenticate live requests on this server.
	LiveAuthentication bool `json:"live_authentication"`
}

// Preset returns the preset with the given ID.
func (c UserIdentityPresetCatalog) Preset(id string) (UserIdentityPreset, bool) {
	for _, p := range c.Presets {
		if p.ID == id {
			return p, true
		}
	}
	return UserIdentityPreset{}, false
}

var userProviderRequestKeys = map[string]bool{"preset": true, "inputs": true, "required_claims": true, "display_name": true, "disabled": true}

// UserIdentityProviderRequest is the PUT body for creating or replacing a
// provider. Fields this SDK version does not know (for example a directory,
// client claim or overlap acknowledgement) go in Extra and are sent and
// received unchanged, so newer server fields are never dropped.
type UserIdentityProviderRequest struct {
	Preset         string
	Inputs         map[string]string
	RequiredClaims map[string]string
	DisplayName    string
	Disabled       bool
	// Extra holds any other top-level fields, as raw JSON.
	Extra map[string]json.RawMessage
}

type userProviderRequestWire struct {
	Preset         string            `json:"preset"`
	Inputs         map[string]string `json:"inputs"`
	RequiredClaims map[string]string `json:"required_claims,omitempty"`
	DisplayName    string            `json:"display_name,omitempty"`
	Disabled       bool              `json:"disabled,omitempty"`
}

// MarshalJSON writes the known fields and then Extra; Extra never overrides a
// known field.
func (r UserIdentityProviderRequest) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(userProviderRequestWire{r.Preset, r.Inputs, r.RequiredClaims, r.DisplayName, r.Disabled}, r.Extra)
}

// UnmarshalJSON reads the known fields and keeps the rest in Extra.
func (r *UserIdentityProviderRequest) UnmarshalJSON(data []byte) error {
	var wire userProviderRequestWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	extra, err := extraFields(data, userProviderRequestKeys)
	if err != nil {
		return err
	}
	*r = UserIdentityProviderRequest{Preset: wire.Preset, Inputs: wire.Inputs, RequiredClaims: wire.RequiredClaims, DisplayName: wire.DisplayName, Disabled: wire.Disabled, Extra: extra}
	return nil
}

func marshalWithExtra(known any, extra map[string]json.RawMessage) ([]byte, error) {
	raw, err := json.Marshal(known)
	if err != nil {
		return nil, err
	}
	if len(extra) == 0 {
		return raw, nil
	}
	merged := map[string]json.RawMessage{}
	if err = json.Unmarshal(raw, &merged); err != nil {
		return nil, err
	}
	for k, v := range extra {
		if _, taken := merged[k]; !taken {
			merged[k] = v
		}
	}
	return json.Marshal(merged)
}

func extraFields(data []byte, known map[string]bool) (map[string]json.RawMessage, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	var extra map[string]json.RawMessage
	for k, v := range all {
		if !known[k] {
			if extra == nil {
				extra = map[string]json.RawMessage{}
			}
			extra[k] = v
		}
	}
	return extra, nil
}

// UserIdentityProvider is a stored provider with the derived trust tuple, the
// principal pool its users authenticate in and its Revision. Unknown fields
// the server returns are kept in Extra.
type UserIdentityProvider struct {
	userfederation.Provider
	// PrincipalPool is the pool ID to scope role bindings to.
	PrincipalPool string
	Extra         map[string]json.RawMessage
}

// Warnings returns the warnings the server attached to a write that succeeded
// with an acknowledged risk (for example an acknowledged issuer overlap).
func (p UserIdentityProvider) Warnings() []string {
	var out []string
	if raw, ok := p.Extra["warnings"]; ok {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

type userProviderWire struct {
	userfederation.Provider
	PrincipalPool string `json:"principal_pool"`
}

// UnmarshalJSON decodes the known fields and retains every other field.
func (p *UserIdentityProvider) UnmarshalJSON(data []byte) error {
	var wire userProviderWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	known := map[string]bool{"principal_pool": true}
	if raw, err := json.Marshal(wire.Provider); err == nil {
		var keys map[string]json.RawMessage
		if json.Unmarshal(raw, &keys) == nil {
			for k := range keys {
				known[k] = true
			}
		}
	}
	extra, err := extraFields(data, known)
	if err != nil {
		return err
	}
	*p = UserIdentityProvider{Provider: wire.Provider, PrincipalPool: wire.PrincipalPool, Extra: extra}
	return nil
}

// MarshalJSON writes the provider followed by its extra fields.
func (p UserIdentityProvider) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(userProviderWire{p.Provider, p.PrincipalPool}, p.Extra)
}

// UserIdentityVerifyRequest is the dry-run body. The token is sent once and
// never stored. ProviderID optionally pins one provider.
type UserIdentityVerifyRequest struct {
	Token      string `json:"token"`
	ProviderID string `json:"provider_id,omitempty"`
}

// String redacts the token so a request is safe to log.
func (r UserIdentityVerifyRequest) String() string {
	return fmt.Sprintf("UserIdentityVerifyRequest{provider_id:%q token:<redacted>}", r.ProviderID)
}

// GoString implements fmt.GoStringer for %#v.
func (r UserIdentityVerifyRequest) GoString() string { return r.String() }

// UserIdentityVerifyResult is the outcome of a token dry-run. It never
// contains the token.
type UserIdentityVerifyResult struct {
	userfederation.Result
	PrincipalID        string `json:"principal_id,omitempty"`
	PrincipalPool      string `json:"principal_pool,omitempty"`
	IsWorkforce        bool   `json:"is_workforce"`
	LiveAuthentication bool   `json:"live_authentication"`
}

func userProviderPath(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("aiplex: provider ID is required")
	}
	if err := validateResourceID(id); err != nil {
		return "", err
	}
	return "/api/v1/identity/user-providers/" + url.PathEscape(id), nil
}

// ListUserIdentityPresets returns the preset catalog.
func (c *Client) ListUserIdentityPresets(ctx context.Context) (*UserIdentityPresetCatalog, error) {
	var out UserIdentityPresetCatalog
	if err := c.do(ctx, http.MethodGet, "/api/v1/identity/user-provider-presets", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListUserIdentityProviders lists the tenant's providers.
func (c *Client) ListUserIdentityProviders(ctx context.Context) ([]UserIdentityProvider, error) {
	out := []UserIdentityProvider{}
	if err := c.do(ctx, http.MethodGet, "/api/v1/identity/user-providers", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetUserIdentityProvider returns one provider including its current Revision.
func (c *Client) GetUserIdentityProvider(ctx context.Context, id string) (*UserIdentityProvider, error) {
	path, err := userProviderPath(id)
	if err != nil {
		return nil, err
	}
	var out UserIdentityProvider
	if err = c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutUserIdentityProvider creates (expectedRevision 0) or replaces
// (expectedRevision equal to the stored revision) a provider.
func (c *Client) PutUserIdentityProvider(ctx context.Context, id string, req UserIdentityProviderRequest, expectedRevision uint64) (*UserIdentityProvider, error) {
	path, err := userProviderPath(id)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	if expectedRevision > 0 {
		headers["If-Match"] = strconv.FormatUint(expectedRevision, 10)
	}
	var out UserIdentityProvider
	if err = c.doWithHeaders(ctx, http.MethodPut, path, req, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteUserIdentityProvider removes a provider; the current revision is required.
func (c *Client) DeleteUserIdentityProvider(ctx context.Context, id string, expectedRevision uint64) error {
	path, err := userProviderPath(id)
	if err != nil {
		return err
	}
	if expectedRevision == 0 {
		return fmt.Errorf("aiplex: positive expected revision required")
	}
	return c.doWithHeaders(ctx, http.MethodDelete, path, nil, nil, map[string]string{"If-Match": strconv.FormatUint(expectedRevision, 10)})
}

// PlanUserIdentityProvider renders the setup steps for a provider.
func (c *Client) PlanUserIdentityProvider(ctx context.Context, id string) (*UserIdentityPlan, error) {
	path, err := userProviderPath(id)
	if err != nil {
		return nil, err
	}
	var out UserIdentityPlan
	if err = c.do(ctx, http.MethodPost, path+"/plan", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// VerifyUserIdentityToken dry-runs a user token against the tenant's providers
// (or one pinned provider). A failed verification is a successful call with
// Valid=false; inspect Checks for the reasons.
func (c *Client) VerifyUserIdentityToken(ctx context.Context, req UserIdentityVerifyRequest) (*UserIdentityVerifyResult, error) {
	if req.Token == "" {
		return nil, fmt.Errorf("aiplex: token is required")
	}
	var out UserIdentityVerifyResult
	if err := c.do(ctx, http.MethodPost, "/api/v1/identity/user-providers/verify", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

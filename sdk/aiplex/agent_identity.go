package aiplex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
)

// Cross-cloud agent identity federation (AgentPlex). Bindings tie an external
// workload identity (GCP, AWS, Azure, GitHub Actions, Kubernetes, SPIFFE, ...)
// to a registered agent.

// AgentIdentityPreset is one entry of the identity preset catalog.
type AgentIdentityPreset = agentfederation.Preset

// AgentIdentityField describes one input a preset requires or accepts.
type AgentIdentityField = agentfederation.Field

// AgentIdentityPlan is the rendered setup plan for a binding.
type AgentIdentityPlan = agentfederation.Plan

// AgentIdentityStep is one step of a setup plan.
type AgentIdentityStep = agentfederation.Step

// AgentIdentityCheck is one verification check outcome (pass, fail, skipped).
type AgentIdentityCheck = agentfederation.Check

// Check statuses reported by token verification.
const (
	AgentIdentityCheckPass    = agentfederation.StatusPass
	AgentIdentityCheckFail    = agentfederation.StatusFail
	AgentIdentityCheckSkipped = agentfederation.StatusSkipped
)

// AgentIdentityPresetCatalog is the response of the preset catalog route.
type AgentIdentityPresetCatalog struct {
	Presets         []AgentIdentityPreset `json:"presets"`
	DefaultAudience string                `json:"default_audience,omitempty"`
	// LiveAuthentication is false when bindings can be dry-run but do not yet
	// authenticate live requests on this server.
	LiveAuthentication bool `json:"live_authentication"`
}

// Preset returns the preset with the given ID.
func (c AgentIdentityPresetCatalog) Preset(id string) (AgentIdentityPreset, bool) {
	for _, p := range c.Presets {
		if p.ID == id {
			return p, true
		}
	}
	return AgentIdentityPreset{}, false
}

// AgentIdentityRequest is the PUT body for creating or replacing a binding.
type AgentIdentityRequest struct {
	Preset         string            `json:"preset"`
	Inputs         map[string]string `json:"inputs"`
	RequiredClaims map[string]string `json:"required_claims,omitempty"`
	DisplayName    string            `json:"display_name,omitempty"`
	Disabled       bool              `json:"disabled,omitempty"`
	// AcknowledgeOverlap saves the binding even though a user identity
	// provider of the tenant trusts the same issuer and audience. It is not
	// persisted: every write that keeps the overlap must acknowledge it again.
	AcknowledgeOverlap bool `json:"acknowledge_overlap,omitempty"`
}

// AgentIdentityBinding is a stored binding with the canonical identities a
// matching token authenticates as. Revision is the optimistic-concurrency token.
type AgentIdentityBinding struct {
	agentfederation.Binding
	PrincipalID string `json:"principal_id"`
	ActorID     string `json:"actor_id,omitempty"`
	// Warnings are returned on writes that succeeded with an acknowledged
	// risk (for example an acknowledged issuer overlap).
	Warnings []string `json:"warnings,omitempty"`
}

// AgentIdentityVerifyRequest is the dry-run body. ClientID and BindingID must
// be given together or not at all. The token is sent once and never stored.
type AgentIdentityVerifyRequest struct {
	Token     string `json:"token"`
	ClientID  string `json:"client_id,omitempty"`
	BindingID string `json:"binding_id,omitempty"`
}

// String redacts the token so a request is safe to log.
func (r AgentIdentityVerifyRequest) String() string {
	return fmt.Sprintf("AgentIdentityVerifyRequest{client_id:%q binding_id:%q token:<redacted>}", r.ClientID, r.BindingID)
}

// GoString implements fmt.GoStringer for %#v.
func (r AgentIdentityVerifyRequest) GoString() string { return r.String() }

// AgentIdentityVerifyResult is the outcome of a token dry-run.
type AgentIdentityVerifyResult struct {
	agentfederation.Result
	PrincipalID        string `json:"principal_id,omitempty"`
	ActorID            string `json:"actor_id,omitempty"`
	LiveAuthentication bool   `json:"live_authentication"`
}

func agentIdentityPath(agentID, bindingID string) (string, error) {
	if err := validateResourceID(agentID); err != nil {
		return "", err
	}
	path := "/api/v1/agents/" + url.PathEscape(agentID) + "/identities"
	if bindingID == "" {
		return path, nil
	}
	if err := validateResourceID(bindingID); err != nil {
		return "", err
	}
	return path + "/" + url.PathEscape(bindingID), nil
}

func requireBindingID(id string) error {
	if id == "" {
		return fmt.Errorf("aiplex: binding ID is required")
	}
	return nil
}

// ListAgentIdentityPresets returns the preset catalog and whether live
// authentication is enabled on the server.
func (c *Client) ListAgentIdentityPresets(ctx context.Context) (*AgentIdentityPresetCatalog, error) {
	var out AgentIdentityPresetCatalog
	if err := c.do(ctx, http.MethodGet, "/api/v1/agents/identity-presets", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAgentIdentities lists the identity bindings of an agent.
func (c *Client) ListAgentIdentities(ctx context.Context, agentID string) ([]AgentIdentityBinding, error) {
	path, err := agentIdentityPath(agentID, "")
	if err != nil {
		return nil, err
	}
	out := []AgentIdentityBinding{}
	if err = c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetAgentIdentity returns one binding including its current Revision.
func (c *Client) GetAgentIdentity(ctx context.Context, agentID, bindingID string) (*AgentIdentityBinding, error) {
	if err := requireBindingID(bindingID); err != nil {
		return nil, err
	}
	path, err := agentIdentityPath(agentID, bindingID)
	if err != nil {
		return nil, err
	}
	var out AgentIdentityBinding
	if err = c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutAgentIdentity creates (expectedRevision 0) or replaces (expectedRevision
// equal to the stored revision) a binding.
func (c *Client) PutAgentIdentity(ctx context.Context, agentID, bindingID string, req AgentIdentityRequest, expectedRevision uint64) (*AgentIdentityBinding, error) {
	if err := requireBindingID(bindingID); err != nil {
		return nil, err
	}
	path, err := agentIdentityPath(agentID, bindingID)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	if expectedRevision > 0 {
		headers["If-Match"] = fmt.Sprintf("\"%d\"", expectedRevision)
	}
	var out AgentIdentityBinding
	if err = c.doWithHeaders(ctx, http.MethodPut, path, req, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAgentIdentity removes a binding; the current revision is required.
func (c *Client) DeleteAgentIdentity(ctx context.Context, agentID, bindingID string, expectedRevision uint64) error {
	if err := requireBindingID(bindingID); err != nil {
		return err
	}
	path, err := agentIdentityPath(agentID, bindingID)
	if err != nil {
		return err
	}
	if expectedRevision == 0 {
		return fmt.Errorf("aiplex: positive expected revision required")
	}
	return c.doWithHeaders(ctx, http.MethodDelete, path, nil, nil, map[string]string{"If-Match": fmt.Sprintf("\"%d\"", expectedRevision)})
}

// PlanAgentIdentity renders the setup steps for a binding.
func (c *Client) PlanAgentIdentity(ctx context.Context, agentID, bindingID string) (*AgentIdentityPlan, error) {
	if err := requireBindingID(bindingID); err != nil {
		return nil, err
	}
	path, err := agentIdentityPath(agentID, bindingID)
	if err != nil {
		return nil, err
	}
	var out AgentIdentityPlan
	if err = c.do(ctx, http.MethodPost, path+"/plan", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// VerifyAgentIdentityToken dry-runs a workload token against the bindings
// (or one pinned binding). A failed verification is a successful call with
// Valid=false; inspect Checks for the reasons.
func (c *Client) VerifyAgentIdentityToken(ctx context.Context, req AgentIdentityVerifyRequest) (*AgentIdentityVerifyResult, error) {
	if req.Token == "" {
		return nil, fmt.Errorf("aiplex: token is required")
	}
	if (req.ClientID == "") != (req.BindingID == "") {
		return nil, fmt.Errorf("aiplex: client ID and binding ID must be supplied together")
	}
	var out AgentIdentityVerifyResult
	if err := c.do(ctx, http.MethodPost, "/api/v1/agents/identities/verify", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

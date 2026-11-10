package aiplex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/vamsiramakrishnan/aiplex/pkg/hooks"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Request-hook policies bind an ordered list of hooks (redact, size_limit,
// http sidecar) to a scope: the tenant, an access point, a model surface or
// a tool connection. Operator only; see docs/request-hooks.md.
type (
	HookPolicy = hooks.Policy
	HookScope  = hooks.Scope
	Hook       = hooks.Hook
)

// HookPolicyResource is a stored hook policy with its revision. Pass
// Revision to PutHookPolicy / DeleteHookPolicy.
type HookPolicyResource struct {
	ID       string     `json:"id"`
	Revision uint64     `json:"revision"`
	Policy   HookPolicy `json:"policy"`
}

type hookPolicyRecord struct {
	ID       string          `json:"id"`
	Revision uint64          `json:"revision"`
	Spec     json.RawMessage `json:"spec"`
}

func (r hookPolicyRecord) resource() (HookPolicyResource, error) {
	out := HookPolicyResource{ID: r.ID, Revision: r.Revision}
	if err := json.Unmarshal(r.Spec, &out.Policy); err != nil {
		return out, fmt.Errorf("aiplex: decode hook policy %q: %w", r.ID, err)
	}
	return out, nil
}

const hookPoliciesPath = "/api/v1/hooks/policies/"

// ListHookPolicies returns the tenant's hook policies.
func (c *Client) ListHookPolicies(ctx context.Context) ([]HookPolicyResource, error) {
	var raw []hookPolicyRecord
	if err := c.do(ctx, http.MethodGet, hookPoliciesPath, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]HookPolicyResource, 0, len(raw))
	for _, r := range raw {
		item, err := r.resource()
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// GetHookPolicy returns one hook policy with its current revision.
func (c *Client) GetHookPolicy(ctx context.Context, id string) (*HookPolicyResource, error) {
	if err := validateResourceID(id); err != nil {
		return nil, err
	}
	var raw hookPolicyRecord
	if err := c.do(ctx, http.MethodGet, hookPoliciesPath+url.PathEscape(id), nil, &raw); err != nil {
		return nil, err
	}
	out, err := raw.resource()
	return &out, err
}

// PutHookPolicy creates (revision 0) or replaces (current revision) a
// policy. The policy is validated locally first and again by the server,
// which also checks sidecar endpoints against the outbound endpoint policy.
func (c *Client) PutHookPolicy(ctx context.Context, policy HookPolicy, revision uint64) (*HookPolicyResource, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	var raw hookPolicyRecord
	headers := map[string]string{"If-Match": strconv.FormatUint(revision, 10)}
	if err := c.doWithHeaders(ctx, http.MethodPut, hookPoliciesPath+url.PathEscape(policy.ID), policy, &raw, headers); err != nil {
		return nil, err
	}
	out, err := raw.resource()
	return &out, err
}

// DeleteHookPolicy removes a policy at the given positive revision.
func (c *Client) DeleteHookPolicy(ctx context.Context, id string, revision uint64) error {
	if err := validateResourceID(id); err != nil {
		return err
	}
	if revision == 0 {
		return fmt.Errorf("aiplex: positive expected revision required")
	}
	return c.doWithHeaders(ctx, http.MethodDelete, hookPoliciesPath+url.PathEscape(id), nil, nil, map[string]string{"If-Match": strconv.FormatUint(revision, 10)})
}

// HookPolicyTest is a dry-run sample. The server never stores it.
type HookPolicyTest struct {
	Phase string   `json:"phase,omitempty"` // request (default) or response
	Plane string   `json:"plane,omitempty"` // llmplex (default) or mcplex
	Texts []string `json:"texts"`
}

// HookPolicyTestResult is what the policy decided on the sample. Texts holds
// the output after transforms and is empty when the sample was refused.
type HookPolicyTestResult struct {
	Policy           string                   `json:"policy"`
	Phase            string                   `json:"phase"`
	Decision         string                   `json:"decision"`
	Code             string                   `json:"code,omitempty"`
	Message          string                   `json:"message,omitempty"`
	Texts            []string                 `json:"texts,omitempty"`
	Results          []usageledger.HookResult `json:"results"`
	UninspectedParts int                      `json:"uninspected_parts"`
}

// TestHookPolicy runs a saved policy's hooks over sample texts.
func (c *Client) TestHookPolicy(ctx context.Context, id string, sample HookPolicyTest) (*HookPolicyTestResult, error) {
	if err := validateResourceID(id); err != nil {
		return nil, err
	}
	var out HookPolicyTestResult
	if err := c.do(ctx, http.MethodPost, hookPoliciesPath+url.PathEscape(id)+"/test", sample, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

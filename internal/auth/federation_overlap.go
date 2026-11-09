package auth

import (
	"context"
	"fmt"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/userfederation"
)

// Overlap between agent identity bindings and user identity providers.
//
// How each side selects a token today (from unverified routing claims, before
// any signature check):
//
//   - an agent identity binding claims a token whose iss equals the binding
//     issuer and whose sub OR oid equals the bound subject (audience is not
//     part of selection; it is checked during verification, where the token's
//     aud must contain the binding audience);
//   - a user identity provider claims a token whose iss equals the provider
//     issuer and whose aud contains the provider audience (subject is not part
//     of selection: a provider trusts every user of the issuer).
//
// A token claimed by both is rejected at request time (ErrAmbiguousFederation).
//
// Write-time predicate. A binding B and a provider P of the same tenant
// overlap when B.Issuer == P.Issuer and B.Audience == P.Audience. Then every
// token B is meant to accept (iss = B.Issuer, sub/oid = B.Subject, aud ∋
// B.Audience) is also claimed by P, so the binding can never authenticate and
// the bound workload is rejected as ambiguous. Disabled bindings and providers
// still overlap, because selection ignores the disabled flag.
//
// With equal issuers but different audiences, only a token that carries both
// audiences (a multi-audience token) is claimed by both. That is not a
// configuration conflict: it stays rejected at request time and is reported by
// both dry-runs (federation_overlap check), but it does not block writes.
func Overlaps(b agentfederation.Binding, p userfederation.Provider) bool {
	return b.Issuer == p.Issuer && b.Audience == p.Audience
}

// OverlapError names the resource of the other kind that already trusts the
// same issuer and audience.
type OverlapError struct {
	// Kind is "agent-identity-binding" or "user-identity-provider".
	Kind string
	// ID is "clientId/bindingId" for bindings, the provider ID for providers.
	ID       string
	Issuer   string
	Audience string
}

func (e *OverlapError) Error() string {
	if e.Kind == "user-identity-provider" {
		return fmt.Sprintf("user identity provider %q already trusts issuer %q with audience %q; tokens of this agent identity would be claimed by both and rejected at request time. Use a distinct audience for agent tokens, or resend with acknowledge_overlap: true", e.ID, e.Issuer, e.Audience)
	}
	return fmt.Sprintf("agent identity binding %q already trusts issuer %q with audience %q; that agent's tokens would be claimed by both and rejected at request time. Use a distinct audience for agent tokens, or resend with acknowledge_overlap: true", e.ID, e.Issuer, e.Audience)
}

// Overlap error codes returned by the API (HTTP 409).
const (
	CodeOverlapsUserProvider = "IDENTITY_OVERLAPS_USER_PROVIDER"
	CodeOverlapsAgentBinding = "IDENTITY_OVERLAPS_AGENT_BINDING"
)

// BindingOverlap returns the tenant's user identity provider that overlaps b.
func BindingOverlap(ctx context.Context, providers *userfederation.Store, tenant string, b agentfederation.Binding) (*OverlapError, error) {
	if providers == nil {
		return nil, nil
	}
	list, err := providers.ByIssuer(ctx, tenant, b.Issuer)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		if Overlaps(b, p) {
			return &OverlapError{Kind: "user-identity-provider", ID: p.ID, Issuer: p.Issuer, Audience: p.Audience}, nil
		}
	}
	return nil, nil
}

// ProviderOverlap returns the tenant's agent identity binding that overlaps p.
func ProviderOverlap(ctx context.Context, bindings *agentfederation.Store, tenant string, p userfederation.Provider) (*OverlapError, error) {
	if bindings == nil {
		return nil, nil
	}
	list, err := bindings.ByIssuer(ctx, tenant, p.Issuer)
	if err != nil {
		return nil, err
	}
	for _, b := range list {
		if Overlaps(b, p) {
			return &OverlapError{Kind: "agent-identity-binding", ID: b.ClientID + "/" + b.ID, Issuer: b.Issuer, Audience: b.Audience}, nil
		}
	}
	return nil, nil
}

// TokenClaimants applies the live selection rules of both kinds to a token and
// returns the binding and provider that would claim it (either may be nil).
// Both non-nil means live authentication rejects the token as ambiguous.
func TokenClaimants(ctx context.Context, tenant, token string, bindings *agentfederation.Store, providers *userfederation.Store) (*agentfederation.Binding, *userfederation.Provider, error) {
	var agent *agentfederation.Binding
	var user *userfederation.Provider
	var err error
	if bindings != nil {
		agent, err = (&AgentFederation{Tenant: tenant, Bindings: bindings, Engine: &agentfederation.Engine{}}).candidate(ctx, token)
		if err != nil {
			return nil, nil, err
		}
	}
	if providers != nil {
		user, err = (&UserFederation{Tenant: tenant, Providers: providers, Engine: &userfederation.Engine{}}).candidate(ctx, token)
		if err != nil {
			return nil, nil, err
		}
	}
	return agent, user, nil
}

// CheckFederationOverlap is the dry-run check reporting a token claimed by both kinds.
const CheckFederationOverlap = "federation_overlap"

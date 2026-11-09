package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/pkg/userfederation"
)

// ErrAmbiguousFederation is returned when a token matches both an agent
// identity binding and a user identity provider. It is deliberately not
// ErrNoFederatedBinding: the request is rejected rather than guessed.
var ErrAmbiguousFederation = errors.New("token matches both an agent identity binding and a user identity provider")

// WorkforceFederatedVerifier is the richer form of FederatedVerifier for
// fallbacks that can authenticate human users. isWorkforce becomes the
// identity's workforce flag.
type WorkforceFederatedVerifier interface {
	VerifyFederatedIdentity(r *http.Request, token string) (claims map[string]any, poolID string, isWorkforce bool, err error)
}

// UserFederationPoolID is the identity pool for users authenticated through
// user identity providers of one tenant. It is distinct from every static
// source, from agent federation and from other tenants.
func UserFederationPoolID(tenant string) string { return "user-federation/" + tenant }

// UserKeyResolver resolves provider JWKS endpoints through the same pinned
// outbound policy as agent bindings.
type UserKeyResolver struct{ Keys *JWKSKeyResolver }

func (k UserKeyResolver) ForProvider(_ context.Context, p userfederation.Provider) (userfederation.SignatureVerifier, error) {
	return k.Keys.ForTrust(p.Issuer, p.Audience, p.JWKSURL)
}

// UserFederation authenticates human users whose tokens match a user identity
// provider of one tenant. The resulting identity is
//
//	iss = provider issuer, sub = verified subject (sub or oid) -> PrincipalID
//
// with the workforce flag set, no actor, and groups only when verified from the
// provider's pinned claim. It grants no scopes by itself.
type UserFederation struct {
	Tenant    string
	Providers *userfederation.Store
	Engine    *userfederation.Engine
}

// candidate selects the provider that claims the token from unverified routing
// claims (issuer and audience). nil means no provider claims it.
func (f *UserFederation) candidate(ctx context.Context, token string) (*userfederation.Provider, error) {
	if f == nil || f.Tenant == "" || f.Providers == nil || f.Engine == nil {
		return nil, nil
	}
	routing, err := routingClaims(token)
	if err != nil {
		return nil, nil
	}
	issuer, _ := routing["iss"].(string)
	if issuer == "" {
		return nil, nil
	}
	providers, err := f.Providers.ByIssuer(ctx, f.Tenant, issuer)
	if err != nil {
		return nil, fmt.Errorf("user identity providers unavailable")
	}
	candidate, _, problem := userfederation.SelectProvider(providers, issuer, routing["aud"])
	if problem != "" {
		return nil, fmt.Errorf("%s", problem)
	}
	return candidate, nil
}

func (f *UserFederation) verify(r *http.Request, token string, candidate *userfederation.Provider) (map[string]any, string, error) {
	result := f.Engine.Evaluate(r.Context(), userfederation.Input{Token: token, Pinned: candidate, Live: true})
	claims, provider := result.VerifiedClaims(), result.MatchedProvider()
	if claims == nil || provider == nil {
		failed := "verification"
		if check := result.Failed(); check != nil {
			failed = check.Name
		}
		return nil, "", fmt.Errorf("user identity provider rejected token at %s check", failed)
	}
	// Claims are rebuilt from scratch: act, azp, roles, email, hd, tenancy and
	// every other external claim are dropped so an IdP cannot inject them.
	normalized := map[string]any{
		"iss": provider.Issuer,
		"sub": result.VerifiedSubject(),
		"aud": provider.Audience,
	}
	for _, name := range []string{"exp", "iat", "nbf"} {
		if value, ok := claims[name]; ok {
			normalized[name] = value
		}
	}
	// Groups are carried only when read from the pinned claim of the verified
	// token; unavailable groups leave the claim absent, never empty.
	if groups := result.VerifiedGroups(); groups.Available {
		list := make([]any, len(groups.List))
		for i, g := range groups.List {
			list[i] = g
		}
		normalized["groups"] = list
	}
	return normalized, UserFederationPoolID(f.Tenant), nil
}

// VerifyFederatedIdentity implements WorkforceFederatedVerifier.
func (f *UserFederation) VerifyFederatedIdentity(r *http.Request, token string) (map[string]any, string, bool, error) {
	candidate, err := f.candidate(r.Context(), token)
	if err != nil {
		return nil, "", false, err
	}
	if candidate == nil {
		return nil, "", false, ErrNoFederatedBinding
	}
	claims, pool, err := f.verify(r, token, candidate)
	return claims, pool, true, err
}

// VerifyFederated implements FederatedVerifier.
func (f *UserFederation) VerifyFederated(r *http.Request, token string) (map[string]any, string, error) {
	claims, pool, _, err := f.VerifyFederatedIdentity(r, token)
	return claims, pool, err
}

// FederationChain combines agent identity bindings and user identity providers
// as the fallback identity source. Precedence, after statically configured
// sources (which always win): a token claimed by exactly one of the two is
// verified by it; a token claimed by both is rejected (fail closed); a token
// claimed by neither falls through to the ordinary "no trusted identity source".
type FederationChain struct {
	Agent *AgentFederation
	User  *UserFederation
}

func (c *FederationChain) VerifyFederatedIdentity(r *http.Request, token string) (map[string]any, string, bool, error) {
	agent, err := c.Agent.candidate(r.Context(), token)
	if err != nil {
		return nil, "", false, err
	}
	user, err := c.User.candidate(r.Context(), token)
	if err != nil {
		return nil, "", false, err
	}
	switch {
	case agent != nil && user != nil:
		return nil, "", false, ErrAmbiguousFederation
	case agent != nil:
		claims, pool, err := c.Agent.VerifyFederated(r, token)
		return claims, pool, false, err
	case user != nil:
		claims, pool, err := c.User.verify(r, token, user)
		return claims, pool, true, err
	}
	return nil, "", false, ErrNoFederatedBinding
}

func (c *FederationChain) VerifyFederated(r *http.Request, token string) (map[string]any, string, error) {
	claims, pool, _, err := c.VerifyFederatedIdentity(r, token)
	return claims, pool, err
}

// WithUserFederation enables user identity providers as a fallback identity
// source alongside any agent federation already configured. Static sources
// keep precedence. Configure before serving.
func (v *WIFValidator) WithUserFederation(u *UserFederation) *WIFValidator {
	switch current := v.federation.(type) {
	case nil:
		v.federation = &FederationChain{User: u}
	case *AgentFederation:
		v.federation = &FederationChain{Agent: current, User: u}
	case *FederationChain:
		v.federation = &FederationChain{Agent: current.Agent, User: u}
	default:
		v.federation = &FederationChain{User: u}
	}
	return v
}

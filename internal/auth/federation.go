package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/agentidentity"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
)

// ErrNoFederatedBinding means no agent identity binding claims the token; the
// validator then reports the ordinary "no trusted identity source" failure.
var ErrNoFederatedBinding = errors.New("no agent identity binding matches token")

// FederatedVerifier is consulted by WIFValidator only when no statically
// configured identity source matches a token. It returns verified, normalised
// claims and the identity pool they belong to.
type FederatedVerifier interface {
	VerifyFederated(r *http.Request, token string) (claims map[string]any, poolID string, err error)
}

// WithFederation enables binding-based workload federation as a fallback
// identity source. Static sources always take precedence. Configure before
// serving; it is not safe to call concurrently with ExtractIdentity.
func (v *WIFValidator) WithFederation(f FederatedVerifier) *WIFValidator {
	if chain, ok := v.federation.(*FederationChain); ok {
		if agent, isAgent := f.(*AgentFederation); isAgent {
			v.federation = &FederationChain{Agent: agent, User: chain.User}
			return v
		}
	}
	v.federation = f
	return v
}

// FederationPoolID is the identity pool for workloads authenticated through
// agent identity bindings of one tenant. It keeps their canonical principal IDs
// distinct from any statically configured source and from other tenants.
func FederationPoolID(tenant string) string { return "agent-federation/" + tenant }

// JWKSKeyResolver resolves binding JWKS endpoints through an outbound
// endpoint policy pinned to exactly the binding's JWKS origin. DNS answers are
// validated at dial time (no metadata, link-local or unapproved private
// addresses) and redirects are never followed.
type JWKSKeyResolver struct {
	network endpointpolicy.Config
	mu      sync.Mutex
	cache   map[string]*JWKSVerifier
}

// NewJWKSKeyResolver takes the operator's outbound network settings
// (private CIDRs, loopback opt-in, TLS roots); AllowedOrigins is replaced by
// each binding's JWKS origin.
func NewJWKSKeyResolver(network endpointpolicy.Config) *JWKSKeyResolver {
	network.AllowedOrigins = nil
	return &JWKSKeyResolver{network: network, cache: map[string]*JWKSVerifier{}}
}

func (k *JWKSKeyResolver) ForBinding(_ context.Context, b agentfederation.Binding) (agentfederation.SignatureVerifier, error) {
	return k.ForTrust(b.Issuer, b.Audience, b.JWKSURL)
}

// ForTrust resolves the signature verifier for one issuer, audience and JWKS
// URL. Agent bindings and user identity providers share it.
func (k *JWKSKeyResolver) ForTrust(issuer, audience, jwksURL string) (agentfederation.SignatureVerifier, error) {
	cacheKey := issuer + "\x00" + audience + "\x00" + jwksURL
	k.mu.Lock()
	defer k.mu.Unlock()
	if v := k.cache[cacheKey]; v != nil {
		return v, nil
	}
	u, err := url.Parse(jwksURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid JWKS URL")
	}
	policy := k.network
	policy.AllowedOrigins = []string{u.Scheme + "://" + u.Host}
	client, err := endpointpolicy.NewClient(policy)
	if err != nil {
		return nil, fmt.Errorf("JWKS origin rejected by endpoint policy: %w", err)
	}
	verifier, err := NewJWKSVerifier(JWKSConfig{Issuer: issuer, Audience: audience, JWKSURL: jwksURL, HTTPClient: client})
	if err != nil {
		return nil, err
	}
	if len(k.cache) >= 256 {
		k.cache = map[string]*JWKSVerifier{}
	}
	k.cache[cacheKey] = verifier
	return verifier, nil
}

// AgentFederation authenticates workload tokens that match an agent identity
// binding in one tenant. The resulting identity is:
//
//	iss = external issuer, sub = bound external subject  -> PrincipalID
//	azp = agent client ID, act.sub = AgentActorSubject   -> ActorID
//
// and is resolved through the ordinary role-binding and manual-grant path;
// a binding grants no scopes by itself.
type AgentFederation struct {
	Tenant   string
	Bindings *agentfederation.Store
	Engine   *agentfederation.Engine
}

type verifiedClaims map[string]any

func (c verifiedClaims) Verify(context.Context, string) (map[string]any, error) {
	return map[string]any(c), nil
}

func (f *AgentFederation) VerifyFederated(r *http.Request, token string) (map[string]any, string, error) {
	if f == nil || f.Tenant == "" || f.Bindings == nil || f.Engine == nil {
		return nil, "", ErrNoFederatedBinding
	}
	candidate, err := f.candidate(r.Context(), token)
	if err != nil {
		return nil, "", err
	}
	if candidate == nil {
		return nil, "", ErrNoFederatedBinding
	}
	result := f.Engine.Evaluate(r.Context(), agentfederation.Input{Token: token, Pinned: candidate, Live: true})
	claims, binding := result.VerifiedClaims(), result.MatchedBinding()
	if claims == nil || binding == nil {
		failed := "verification"
		if check := result.Failed(); check != nil {
			failed = check.Name
		}
		return nil, "", fmt.Errorf("agent identity binding rejected token at %s check", failed)
	}
	if _, bound := claims["cnf"]; bound {
		if binding.Preset != agentfederation.PresetGCPAgentIdentity {
			return nil, "", fmt.Errorf("certificate-bound token cannot be verified for this binding")
		}
		cfg, err := agentIdentityConfig(*binding)
		if err != nil {
			return nil, "", err
		}
		native, err := agentidentity.New(cfg, verifiedClaims(claims))
		if err != nil {
			return nil, "", err
		}
		if _, err = native.VerifyRequest(r, token); err != nil {
			return nil, "", fmt.Errorf("agent identity certificate binding rejected: %w", err)
		}
	}
	normalized := map[string]any{
		"iss": binding.Issuer,
		"sub": binding.Subject,
		"aud": claims["aud"],
		"azp": binding.ClientID,
		"act": map[string]any{"sub": result.ActorSubject},
	}
	// Only lifetime claims are carried over. Groups, domain (hd), email and any
	// delegation claim from the external issuer are deliberately dropped so an
	// external token cannot acquire AIPlex roles or tenancy by claim name.
	for _, name := range []string{"exp", "iat", "nbf"} {
		if value, ok := claims[name]; ok {
			normalized[name] = value
		}
	}
	return normalized, FederationPoolID(f.Tenant), nil
}

// candidate selects, from unverified routing claims only, the binding that
// claims the token (nil when none). More than one match is an error.
func (f *AgentFederation) candidate(ctx context.Context, token string) (*agentfederation.Binding, error) {
	if f == nil || f.Tenant == "" || f.Bindings == nil || f.Engine == nil {
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
	var candidate *agentfederation.Binding
	for _, claim := range []string{"sub", "oid"} {
		subject, _ := routing[claim].(string)
		if subject == "" {
			continue
		}
		b, err := f.Bindings.FindBySubject(ctx, f.Tenant, issuer, claim, subject)
		if errors.Is(err, agentfederation.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("agent identity bindings unavailable")
		}
		if candidate != nil {
			return nil, fmt.Errorf("token matches more than one agent identity binding")
		}
		bound := b
		candidate = &bound
	}
	return candidate, nil
}

func agentIdentityConfig(b agentfederation.Binding) (agentidentity.Config, error) {
	u, err := url.Parse(b.Subject)
	if err != nil {
		return agentidentity.Config{}, err
	}
	cfg := agentidentity.Config{Issuer: b.Issuer, Audience: b.Audience, TrustDomain: u.Host, AllowedSubjects: []string{b.Subject}, BindingMode: agentidentity.UnboundAllowed}
	if !strings.HasSuffix(b.Issuer, "/"+u.Host) {
		return agentidentity.Config{}, fmt.Errorf("agent identity binding issuer and trust domain disagree")
	}
	return cfg, cfg.Validate()
}

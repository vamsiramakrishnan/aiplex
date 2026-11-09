package userfederation

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/directory"
)

// Check names, in evaluation order. They are part of the API contract.
const (
	CheckTokenShape        = "token_shape"
	CheckIssuer            = "issuer"
	CheckSignature         = "signature"
	CheckAudience          = "audience"
	CheckLifetime          = "lifetime"
	CheckSubject           = "subject"
	CheckRequiredClaims    = "required_claims"
	CheckProofOfPossession = "proof_of_possession"
	CheckClient            = "client"
	CheckGroups            = "groups"
	CheckProvider          = "provider"
)

// Check statuses are shared with agent identity verification.
type CheckStatus = agentfederation.CheckStatus

const (
	StatusPass    = agentfederation.StatusPass
	StatusFail    = agentfederation.StatusFail
	StatusSkipped = agentfederation.StatusSkipped
)

// Check is one verification step. Reasons never contain the token and only
// echo issuer, subject and audience values, never arbitrary claim values.
type Check = agentfederation.Check

// SignatureVerifier verifies a JWS against pinned keys without applying policy.
type SignatureVerifier = agentfederation.SignatureVerifier

// KeyResolver returns the signature verifier for a provider's JWKS. It must
// honour the operator's outbound endpoint policy.
type KeyResolver interface {
	ForProvider(context.Context, Provider) (SignatureVerifier, error)
}

// DirectoryLookup carries the verified facts a directory lookup may use.
type DirectoryLookup struct {
	// Subject is the verified immutable subject (sub or oid).
	Subject string
	// Email is the verified email (email claim with email_verified true), or "".
	Email string
}

// DirectoryResolver resolves a verified user's groups through the provider's
// directory. It returns an Available view, or an error whose message is safe
// to show to an operator (no directory bodies, no credentials).
type DirectoryResolver interface {
	ResolveGroups(ctx context.Context, tenant string, p Provider, lookup DirectoryLookup, now time.Time) (Groups, error)
}

// ProviderFinder locates providers within one tenant.
type ProviderFinder interface {
	ByIssuer(ctx context.Context, issuer string) ([]Provider, error)
}

// ProviderRef identifies the provider a token was evaluated against.
type ProviderRef struct {
	ID     string `json:"id"`
	Preset string `json:"preset"`
}

// Result is the ordered outcome of a verification. Claims are never serialised.
type Result struct {
	Valid    bool         `json:"valid"`
	Checks   []Check      `json:"checks"`
	Issuer   string       `json:"issuer,omitempty"`
	Subject  string       `json:"subject,omitempty"`
	Audience []string     `json:"audience,omitempty"`
	Provider *ProviderRef `json:"provider,omitempty"`
	// GroupsAvailable is true only for a valid token whose pinned claim carried a
	// well-formed list. Groups is then the verified list (possibly empty);
	// otherwise Groups is omitted and GroupsNote says why. Unavailable is not empty.
	GroupsAvailable bool      `json:"groups_available"`
	Groups          *[]string `json:"groups,omitempty"`
	GroupsNote      string    `json:"groups_note,omitempty"`
	// GroupsSource says where the groups came from: "token",
	// "directory:<kind>" or "unavailable".
	GroupsSource string `json:"groups_source,omitempty"`
	// ClientID is the verified client application the token was issued to
	// (from the provider's client_claim); empty when not configured or absent.
	ClientID string `json:"client_id,omitempty"`

	claims   map[string]any
	provider *Provider
	subject  string
	groups   Groups
	client   string
}

// VerifiedClaims returns signature-verified claims only when every check passed.
func (r *Result) VerifiedClaims() map[string]any {
	if r == nil || !r.Valid {
		return nil
	}
	return r.claims
}

// MatchedProvider returns the provider a valid result matched.
func (r *Result) MatchedProvider() *Provider {
	if r == nil || !r.Valid {
		return nil
	}
	return r.provider
}

// VerifiedSubject returns the immutable subject of a valid result.
func (r *Result) VerifiedSubject() string {
	if r == nil || !r.Valid {
		return ""
	}
	return r.subject
}

// VerifiedGroups returns the group view of a valid result.
func (r *Result) VerifiedGroups() Groups {
	if r == nil || !r.Valid {
		return Groups{}
	}
	return r.groups
}

// VerifiedClient returns the verified client application of a valid result.
func (r *Result) VerifiedClient() string {
	if r == nil || !r.Valid {
		return ""
	}
	return r.client
}

// Failed returns the first failing check, if any.
func (r *Result) Failed() *Check {
	for i := range r.Checks {
		if r.Checks[i].Status == StatusFail {
			return &r.Checks[i]
		}
	}
	return nil
}

// Engine evaluates presented tokens against providers.
type Engine struct {
	Keys KeyResolver
	// Directory resolves groups for providers with a directory block. Without
	// it such providers report groups as unavailable.
	Directory DirectoryResolver
	Now       func() time.Time
	// Skew tolerated for nbf and iat (not for exp). Defaults to 30s.
	Skew time.Duration
}

// Input selects the provider: Pinned evaluates against one provider; otherwise
// Finder selects it from the token's issuer and audience.
type Input struct {
	Token  string
	Pinned *Provider
	Finder ProviderFinder
	Live   bool
	// Tenant is the AIPlex tenant the provider belongs to (directory lookups).
	Tenant string
}

// SelectProvider picks, among the providers of an issuer, the one whose audience
// the (unverified) token names. candidate is the exact match; reference is the
// provider used to explain a failure. More than one audience match is ambiguous
// and reported through problem so callers fail closed.
func SelectProvider(providers []Provider, issuer string, aud any) (candidate, reference *Provider, problem string) {
	auds := agentfederation.Audiences(aud)
	var matches []int
	for i := range providers {
		if providers[i].Issuer == issuer && agentfederation.Contains(auds, providers[i].Audience) {
			matches = append(matches, i)
		}
	}
	switch {
	case len(matches) > 1:
		return nil, nil, fmt.Sprintf("token matches %d user identity providers (%s and %s); remove one so the audience is unambiguous", len(matches), providers[matches[0]].ID, providers[matches[1]].ID)
	case len(matches) == 1:
		p := providers[matches[0]]
		return &p, &p, ""
	}
	for i := range providers {
		if providers[i].Issuer == issuer {
			p := providers[i]
			return nil, &p, ""
		}
	}
	return nil, nil, ""
}

// Evaluate runs every check in order and never returns or logs the token.
func (e *Engine) Evaluate(ctx context.Context, in Input) *Result {
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	skew := e.Skew
	if skew == 0 {
		skew = 30 * time.Second
	}
	res := &Result{}
	add := func(name string, status CheckStatus, reason string) {
		res.Checks = append(res.Checks, Check{Name: name, Status: status, Reason: reason})
	}
	order := []string{CheckTokenShape, CheckIssuer, CheckSignature, CheckAudience, CheckLifetime, CheckSubject, CheckRequiredClaims, CheckProofOfPossession, CheckClient, CheckGroups, CheckProvider}
	skipRest := func(from int, reason string) {
		for _, name := range order[from:] {
			add(name, StatusSkipped, reason)
		}
	}

	// 1. Token shape.
	claims, alg, problem := agentfederation.ParseCompactJWT(in.Token)
	if problem != "" {
		add(CheckTokenShape, StatusFail, problem)
		skipRest(1, "token could not be parsed")
		return res
	}
	add(CheckTokenShape, StatusPass, "compact "+alg+" JWS with a key ID")
	issuer, _ := claims["iss"].(string)
	res.Issuer, res.Audience = agentfederation.Clip(issuer), agentfederation.Audiences(claims["aud"])
	for i := range res.Audience {
		res.Audience[i] = agentfederation.Clip(res.Audience[i])
	}
	if len(res.Audience) > 8 {
		res.Audience = res.Audience[:8]
	}

	var candidate, reference *Provider
	var selectionProblem string
	if in.Pinned != nil {
		candidate, reference = in.Pinned, in.Pinned
	} else if in.Finder != nil && issuer != "" {
		providers, err := in.Finder.ByIssuer(ctx, issuer)
		if err != nil {
			selectionProblem = "user identity provider store unavailable"
		} else {
			candidate, reference, selectionProblem = SelectProvider(providers, issuer, claims["aud"])
		}
	}
	if candidate != nil {
		res.Provider = &ProviderRef{ID: candidate.ID, Preset: candidate.Preset}
	}
	subjectClaim := "sub"
	if reference != nil {
		subjectClaim = reference.SubjectClaim
	}
	if s, _ := claims[subjectClaim].(string); s != "" {
		res.Subject = agentfederation.Clip(s)
	}

	// 2. Issuer.
	switch {
	case issuer == "":
		add(CheckIssuer, StatusFail, "token has no iss claim")
	case selectionProblem != "":
		add(CheckIssuer, StatusFail, selectionProblem)
	case reference == nil:
		add(CheckIssuer, StatusFail, fmt.Sprintf("no user identity provider in this tenant trusts issuer %q", agentfederation.Clip(issuer)))
	case reference.Issuer != issuer:
		add(CheckIssuer, StatusFail, fmt.Sprintf("token issuer %q does not exactly match the provider issuer %q", agentfederation.Clip(issuer), reference.Issuer))
	default:
		add(CheckIssuer, StatusPass, fmt.Sprintf("issuer %q matches provider %s", reference.Issuer, reference.ID))
	}
	if reference == nil || selectionProblem != "" || reference.Issuer != issuer {
		skipRest(2, "no provider to evaluate against")
		return res
	}

	// 3. Signature.
	unverified := ""
	var verifiedClaims map[string]any
	if e.Keys == nil {
		add(CheckSignature, StatusFail, "signing key resolution is not configured")
		unverified = " (claims not verified: signature check failed)"
	} else if verifier, err := e.Keys.ForProvider(ctx, *reference); err != nil {
		add(CheckSignature, StatusFail, "signing keys unavailable for "+reference.JWKSURL+": "+err.Error())
		unverified = " (claims not verified: signature check failed)"
	} else if verified, err := verifier.VerifySignature(ctx, in.Token); err != nil {
		add(CheckSignature, StatusFail, err.Error()+" (keys from "+reference.JWKSURL+")")
		unverified = " (claims not verified: signature check failed)"
	} else {
		add(CheckSignature, StatusPass, "signature verified with a key from "+reference.JWKSURL)
		verifiedClaims = verified
		claims = verified
	}

	// 4. Audience.
	auds := agentfederation.Audiences(claims["aud"])
	if agentfederation.Contains(auds, reference.Audience) {
		add(CheckAudience, StatusPass, fmt.Sprintf("audience includes %q", reference.Audience)+unverified)
	} else if len(auds) == 0 {
		add(CheckAudience, StatusFail, fmt.Sprintf("token has no aud claim; request it with audience %q", reference.Audience)+unverified)
	} else {
		shown := make([]string, 0, len(auds))
		for i, a := range auds {
			if i == 4 {
				break
			}
			shown = append(shown, fmt.Sprintf("%q", agentfederation.Clip(a)))
		}
		add(CheckAudience, StatusFail, fmt.Sprintf("token audience [%s] does not include the provider audience %q", strings.Join(shown, ", "), reference.Audience)+unverified)
	}

	// 5. Lifetime.
	if lifetimeProblem, exp := agentfederation.LifetimeProblem(claims, now, skew); lifetimeProblem != "" {
		add(CheckLifetime, StatusFail, lifetimeProblem+unverified)
	} else {
		add(CheckLifetime, StatusPass, "token is within its validity window, expires "+time.Unix(exp, 0).UTC().Format(time.RFC3339)+unverified)
	}

	// 6. Subject: any user of the issuer, identified by the immutable claim.
	subject, _ := claims[reference.SubjectClaim].(string)
	switch {
	case subject == "":
		add(CheckSubject, StatusFail, fmt.Sprintf("token has no %s claim", reference.SubjectClaim)+unverified)
	case agentfederation.ExactValue("subject", subject) != nil:
		add(CheckSubject, StatusFail, fmt.Sprintf("token %s claim is not a bounded identifier", reference.SubjectClaim)+unverified)
	default:
		add(CheckSubject, StatusPass, fmt.Sprintf("%s %q identifies the user", reference.SubjectClaim, agentfederation.Clip(subject))+unverified)
	}

	// 7. Required claims (tenant pinning such as hd or tid). Values are never echoed.
	if len(reference.RequiredClaims) == 0 {
		add(CheckRequiredClaims, StatusPass, "provider requires no additional claims")
	} else {
		names := make([]string, 0, len(reference.RequiredClaims))
		for name := range reference.RequiredClaims {
			names = append(names, name)
		}
		sort.Strings(names)
		var missing, mismatched []string
		for _, name := range names {
			raw, ok := claims[name]
			if !ok {
				missing = append(missing, name)
				continue
			}
			value, isString := raw.(string)
			if n, isNumber := raw.(json.Number); isNumber {
				value, isString = n.String(), true
			}
			if !isString || value != reference.RequiredClaims[name] {
				mismatched = append(mismatched, name)
			}
		}
		if len(missing) > 0 || len(mismatched) > 0 {
			var parts []string
			if len(missing) > 0 {
				parts = append(parts, "missing: "+strings.Join(missing, ", "))
			}
			if len(mismatched) > 0 {
				parts = append(parts, "value differs from the provider: "+strings.Join(mismatched, ", "))
			}
			add(CheckRequiredClaims, StatusFail, "required claims not satisfied ("+strings.Join(parts, "; ")+")"+unverified)
		} else {
			add(CheckRequiredClaims, StatusPass, "required claims match: "+strings.Join(names, ", ")+unverified)
		}
	}

	// 8. Proof of possession: user tokens are bearer tokens.
	if _, bound := claims["cnf"]; bound {
		add(CheckProofOfPossession, StatusFail, "token carries a cnf confirmation claim, which user identity providers cannot verify; request a bearer token")
	} else {
		add(CheckProofOfPossession, StatusPass, "bearer token (no cnf confirmation claim)")
	}

	// 9. Client application. Only the verified claim is ever carried.
	client := ""
	switch {
	case reference.ClientClaim == "":
		add(CheckClient, StatusPass, "provider has no client_claim; requests are not attributed to a client application")
	default:
		raw, present := claims[reference.ClientClaim]
		value, isString := raw.(string)
		valid := present && isString && agentfederation.ExactValue("client", value) == nil
		if valid {
			client = value
		}
		switch {
		case len(reference.ClientIDs) > 0 && !valid:
			add(CheckClient, StatusFail, fmt.Sprintf("token has no usable %s claim, and provider %s accepts only its listed client_ids", reference.ClientClaim, reference.ID)+unverified)
		case len(reference.ClientIDs) > 0 && !agentfederation.Contains(reference.ClientIDs, value):
			add(CheckClient, StatusFail, fmt.Sprintf("client application %q (claim %s) is not in the provider's client_ids", agentfederation.Clip(value), reference.ClientClaim)+unverified)
		case !valid:
			add(CheckClient, StatusPass, fmt.Sprintf("token has no usable %s claim; the request is not attributed to a client application", reference.ClientClaim)+unverified)
		default:
			add(CheckClient, StatusPass, fmt.Sprintf("issued to client application %q (claim %s)", agentfederation.Clip(value), reference.ClientClaim)+unverified)
		}
	}

	// Everything so far must hold before groups are read from claims.
	trusted := verifiedClaims != nil
	for _, c := range res.Checks {
		if c.Status != StatusPass {
			trusted = false
		}
	}

	// 10. Groups. Unavailable groups do not fail authentication: they are simply
	// absent, so every group-based rule fails closed.
	var groups Groups
	switch {
	case !trusted:
		add(CheckGroups, StatusSkipped, "groups are read only from a fully verified token")
	default:
		groups = e.groups(ctx, in.Tenant, reference, candidate, claims, subject)
		switch {
		case groups.Available && groups.Source == GroupSourceToken:
			add(CheckGroups, StatusPass, fmt.Sprintf("source token: %d group(s) read from verified claim %q", len(groups.List), reference.GroupClaim))
		case groups.Available:
			add(CheckGroups, StatusPass, fmt.Sprintf("source %s: %d group(s) resolved transitively, observed %s, valid until %s", groups.Source, len(groups.List), groups.ObservedAt.UTC().Format(time.RFC3339), groups.ExpiresAt.UTC().Format(time.RFC3339)))
		default:
			add(CheckGroups, StatusSkipped, "source unavailable: "+groups.Reason)
		}
	}

	// 11. Provider.
	switch {
	case candidate == nil:
		add(CheckProvider, StatusFail, "no user identity provider matches this issuer and audience")
	case candidate.Disabled:
		add(CheckProvider, StatusFail, fmt.Sprintf("provider %s is disabled", candidate.ID))
	default:
		add(CheckProvider, StatusPass, fmt.Sprintf("authenticates as a workforce user via provider %s", candidate.ID))
	}

	res.Valid = verifiedClaims != nil
	for _, c := range res.Checks {
		if c.Status == StatusFail {
			res.Valid = false
		}
	}
	if res.Valid {
		res.claims, res.provider, res.subject, res.groups, res.client = verifiedClaims, candidate, subject, groups, client
		res.ClientID = client
		res.GroupsSource = groups.Source
		if groups.Available {
			list := append([]string{}, groups.List...)
			res.GroupsAvailable, res.Groups = true, &list
		} else {
			res.GroupsNote = groups.Reason
		}
	}
	return res
}

// groups computes the group view of a fully verified token: token groups, or
// the provider's directory when token groups are unavailable (mode fallback)
// or always (mode always). A directory failure leaves groups unavailable; it
// never falls back to an empty list, and in mode always never to token groups.
// Directory lookups use the engine clock, so dry-runs and live requests share
// one cache timeline.
func (e *Engine) groups(ctx context.Context, tenant string, reference, candidate *Provider, claims map[string]any, subject string) Groups {
	groups := ExtractGroups(claims, reference.Trust)
	if groups.Available {
		groups.Source = GroupSourceToken
	} else {
		groups.Source = GroupSourceUnavailable
	}
	dir := reference.Directory
	if dir == nil || (dir.Mode != directory.ModeAlways && groups.Available) {
		return groups
	}
	unavailable := func(reason string) Groups {
		return Groups{Source: GroupSourceUnavailable, Reason: reason}
	}
	source := GroupSourceDirectory(dir.Kind)
	switch {
	case candidate == nil || candidate.Disabled:
		return unavailable(source + " not consulted: no enabled provider matches this token")
	case e.Directory == nil:
		return unavailable(source + " lookups are not configured on this server")
	case tenant == "":
		return unavailable(source + " not consulted: tenant unknown")
	}
	lookup := DirectoryLookup{Subject: subject}
	if email, _ := claims["email"].(string); email != "" {
		if verified, _ := claims["email_verified"].(bool); verified {
			lookup.Email = email
		}
	}
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	resolved, err := e.Directory.ResolveGroups(ctx, tenant, *candidate, lookup, now)
	if err != nil {
		return unavailable(source + " lookup failed: " + err.Error())
	}
	if !resolved.Available || resolved.List == nil {
		return unavailable(source + " returned no complete view")
	}
	resolved.Source = source
	return resolved
}

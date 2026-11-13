package agentfederation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"
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
	CheckBinding           = "binding"
)

type CheckStatus string

const (
	StatusPass    CheckStatus = "pass"
	StatusFail    CheckStatus = "fail"
	StatusSkipped CheckStatus = "skipped"
)

// Check is one verification step. Reasons never contain the token and only
// echo issuer, subject and audience values, never arbitrary claim values.
type Check struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
	Reason string      `json:"reason"`
}

// BindingRef identifies the binding a token was evaluated against.
type BindingRef struct {
	ClientID string `json:"client_id"`
	ID       string `json:"id"`
	Preset   string `json:"preset"`
}

// Result is the ordered outcome of a verification. Claims is never serialised.
type Result struct {
	Valid    bool        `json:"valid"`
	Checks   []Check     `json:"checks"`
	Issuer   string      `json:"issuer,omitempty"`
	Subject  string      `json:"subject,omitempty"`
	Audience []string    `json:"audience,omitempty"`
	Binding  *BindingRef `json:"binding,omitempty"`
	// ActorSubject is the agent actor subject the token authenticates as.
	ActorSubject string `json:"actor_subject,omitempty"`

	claims  map[string]any
	binding *Binding
}

// VerifiedClaims returns signature-verified claims only when every check passed.
func (r *Result) VerifiedClaims() map[string]any {
	if r == nil || !r.Valid {
		return nil
	}
	return r.claims
}

// MatchedBinding returns the binding a valid result matched.
func (r *Result) MatchedBinding() *Binding {
	if r == nil || !r.Valid {
		return nil
	}
	return r.binding
}

// Failed returns the first failing check, if any.
func (r *Result) Failed() *Check {
	for i := range r.Checks {
		if r.Checks[i].Status != StatusPass {
			return &r.Checks[i]
		}
	}
	return nil
}

// SignatureVerifier verifies a JWS against pinned keys and returns its claims
// without applying issuer, audience, subject or lifetime policy.
type SignatureVerifier interface {
	VerifySignature(context.Context, string) (map[string]any, error)
}

// KeyResolver returns the signature verifier for a binding's JWKS. It must
// honour the operator's outbound endpoint policy.
type KeyResolver interface {
	ForBinding(context.Context, Binding) (SignatureVerifier, error)
}

// BindingFinder locates bindings within one tenant.
type BindingFinder interface {
	FindBySubject(ctx context.Context, issuer, claim, subject string) (Binding, error)
	ByIssuer(ctx context.Context, issuer string) ([]Binding, error)
}

// Agent is the registry view the engine needs.
type Agent struct {
	ClientID     string
	Active       bool
	ActorSubject string
}

// AgentLookup returns the registered agent or ErrNotFound.
type AgentLookup func(context.Context, string) (Agent, error)

// Engine evaluates presented tokens against bindings.
type Engine struct {
	Keys   KeyResolver
	Agents AgentLookup
	Now    func() time.Time
	// Skew tolerated for nbf and iat (not for exp). Defaults to 30s.
	Skew time.Duration
}

// Input selects the binding: Pinned evaluates against one specific binding;
// otherwise Finder looks the binding up from the token's issuer and subject.
type Input struct {
	Token  string
	Pinned *Binding
	Finder BindingFinder
	// Live is set on the authentication path: a certificate-bound token must
	// then be checked by the caller against the request's mTLS certificate.
	Live bool
}

const maxTokenBytes = 64 << 10

var subjectClaims = []string{"sub", "oid"}

type parsedToken struct {
	alg, typ string
	claims   map[string]any
}

func parseToken(token string) (parsedToken, string) {
	if token == "" {
		return parsedToken{}, "no token was supplied"
	}
	if len(token) > maxTokenBytes {
		return parsedToken{}, "token exceeds the 64 KiB limit"
	}
	if strings.ContainsAny(token, " \t\r\n\x00") {
		return parsedToken{}, "token contains whitespace; supply the compact JWT only (without a Bearer prefix)"
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		if len(parts) == 5 {
			return parsedToken{}, "token is a JWE (5 segments); AIPlex requires a signed JWT (JWS) with 3 segments"
		}
		return parsedToken{}, fmt.Sprintf("token has %d dot-separated segments; a signed JWT has 3", len(parts))
	}
	rawHeader, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return parsedToken{}, "JWT header is not base64url (unpadded)"
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if json.Unmarshal(rawHeader, &header) != nil {
		return parsedToken{}, "JWT header is not a JSON object"
	}
	if header.Alg != "RS256" && header.Alg != "ES256" {
		alg := header.Alg
		if len(alg) > 16 {
			alg = alg[:16]
		}
		return parsedToken{}, fmt.Sprintf("JWT algorithm %q is not supported; request an RS256 or ES256 token", alg)
	}
	if header.Kid == "" {
		return parsedToken{}, "JWT header has no key ID (kid)"
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return parsedToken{}, "JWT payload is not base64url (unpadded)"
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var claims map[string]any
	if decoder.Decode(&claims) != nil || claims == nil {
		return parsedToken{}, "JWT payload is not a JSON object"
	}
	if decoder.Decode(new(any)) != io.EOF {
		return parsedToken{}, "JWT payload has trailing data"
	}
	if _, err := base64.RawURLEncoding.DecodeString(parts[2]); err != nil || parts[2] == "" {
		return parsedToken{}, "JWT signature segment is empty or not base64url"
	}
	return parsedToken{alg: header.Alg, typ: header.Typ, claims: claims}, ""
}

func stringClaim(claims map[string]any, name string) string {
	s, _ := claims[name].(string)
	return s
}

func audiences(raw any) []string {
	switch v := raw.(type) {
	case string:
		return []string{v}
	case []any:
		out := []string{}
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func numeric(raw any) (int64, bool) {
	n, ok := raw.(json.Number)
	if !ok {
		return 0, false
	}
	if i, err := n.Int64(); err == nil {
		return i, true
	}
	f, err := n.Float64()
	if err != nil {
		return 0, false
	}
	return int64(f), true
}

// clip bounds echoed identity values so a hostile token cannot inflate output.
func clip(s string) string {
	if len(s) > 256 {
		return s[:256] + "…"
	}
	return s
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
	skipRest := func(from int, reason string) {
		order := []string{CheckTokenShape, CheckIssuer, CheckSignature, CheckAudience, CheckLifetime, CheckSubject, CheckRequiredClaims, CheckProofOfPossession, CheckBinding}
		for _, name := range order[from:] {
			add(name, StatusSkipped, reason)
		}
	}

	// 1. Token shape.
	parsed, problem := parseToken(in.Token)
	if problem != "" {
		add(CheckTokenShape, StatusFail, problem)
		skipRest(1, "token could not be parsed")
		return res
	}
	add(CheckTokenShape, StatusPass, "compact "+parsed.alg+" JWS with a key ID")
	claims := parsed.claims
	issuer := stringClaim(claims, "iss")
	res.Issuer, res.Audience = clip(issuer), audiences(claims["aud"])
	for i := range res.Audience {
		res.Audience[i] = clip(res.Audience[i])
	}
	if len(res.Audience) > 8 {
		res.Audience = res.Audience[:8]
	}

	// Select the binding: pinned, or by (issuer, subject claim).
	var candidate *Binding
	var reference *Binding
	var selectionProblem string
	if in.Pinned != nil {
		candidate = in.Pinned
	} else if in.Finder != nil && issuer != "" {
		var matches []Binding
		for _, claim := range subjectClaims {
			value := stringClaim(claims, claim)
			if value == "" {
				continue
			}
			b, err := in.Finder.FindBySubject(ctx, issuer, claim, value)
			if err == nil {
				matches = append(matches, b)
			} else if !errors.Is(err, ErrNotFound) {
				selectionProblem = "binding store unavailable"
			}
		}
		if len(matches) > 1 {
			selectionProblem = fmt.Sprintf("token matches %d bindings (%s/%s and %s/%s); remove one so the subject is unambiguous", len(matches), matches[0].ClientID, matches[0].ID, matches[1].ClientID, matches[1].ID)
		} else if len(matches) == 1 {
			candidate = &matches[0]
		}
	}
	reference = candidate
	if reference == nil && in.Finder != nil && issuer != "" && selectionProblem == "" {
		if same, err := in.Finder.ByIssuer(ctx, issuer); err == nil && len(same) > 0 {
			reference = &same[0]
		}
	}
	if candidate != nil {
		res.Binding = &BindingRef{ClientID: candidate.ClientID, ID: candidate.ID, Preset: candidate.Preset}
	}
	subjectClaim := "sub"
	if reference != nil {
		subjectClaim = reference.SubjectClaim
	}
	res.Subject = clip(stringClaim(claims, subjectClaim))

	// 2. Issuer.
	switch {
	case issuer == "":
		add(CheckIssuer, StatusFail, "token has no iss claim")
	case selectionProblem != "":
		add(CheckIssuer, StatusFail, selectionProblem)
	case reference == nil:
		add(CheckIssuer, StatusFail, fmt.Sprintf("no agent identity binding in this tenant trusts issuer %q", clip(issuer)))
	case reference.Issuer != issuer:
		add(CheckIssuer, StatusFail, fmt.Sprintf("token issuer %q does not exactly match the binding issuer %q", clip(issuer), reference.Issuer))
	default:
		add(CheckIssuer, StatusPass, fmt.Sprintf("issuer %q matches binding %s/%s", reference.Issuer, reference.ClientID, reference.ID))
	}
	if reference == nil {
		skipRest(2, "no binding to evaluate against")
		return res
	}

	// 3. Signature.
	unverified := ""
	verifiedClaims := map[string]any(nil)
	if e.Keys == nil {
		add(CheckSignature, StatusFail, "signing key resolution is not configured")
		unverified = " (claims not verified: signature check failed)"
	} else if verifier, err := e.Keys.ForBinding(ctx, *reference); err != nil {
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
	auds := audiences(claims["aud"])
	if slices.Contains(auds, reference.Audience) {
		add(CheckAudience, StatusPass, fmt.Sprintf("audience includes %q", reference.Audience)+unverified)
	} else if len(auds) == 0 {
		add(CheckAudience, StatusFail, fmt.Sprintf("token has no aud claim; request it with audience %q", reference.Audience)+unverified)
	} else {
		shown := make([]string, 0, len(auds))
		for i, a := range auds {
			if i == 4 {
				break
			}
			shown = append(shown, fmt.Sprintf("%q", clip(a)))
		}
		add(CheckAudience, StatusFail, fmt.Sprintf("token audience [%s] does not include the binding audience %q", strings.Join(shown, ", "), reference.Audience)+unverified)
	}

	// 5. Lifetime.
	lifetimeProblem, exp := LifetimeProblem(claims, now, skew)
	if lifetimeProblem != "" {
		add(CheckLifetime, StatusFail, lifetimeProblem+unverified)
	} else {
		add(CheckLifetime, StatusPass, "token is within its validity window, expires "+time.Unix(exp, 0).UTC().Format(time.RFC3339)+unverified)
	}

	// 6. Subject.
	subject := stringClaim(claims, reference.SubjectClaim)
	switch {
	case subject == "":
		add(CheckSubject, StatusFail, fmt.Sprintf("token has no %s claim", reference.SubjectClaim)+unverified)
	case candidate == nil:
		add(CheckSubject, StatusFail, fmt.Sprintf("no binding for issuer %q pins %s %q (closest binding: %s/%s expects %q)", clip(issuer), reference.SubjectClaim, clip(subject), reference.ClientID, reference.ID, reference.Subject)+unverified)
	case subject != reference.Subject:
		add(CheckSubject, StatusFail, fmt.Sprintf("token %s %q does not exactly match the binding subject %q", reference.SubjectClaim, clip(subject), reference.Subject)+unverified)
	default:
		add(CheckSubject, StatusPass, fmt.Sprintf("%s %q matches", reference.SubjectClaim, reference.Subject)+unverified)
	}

	// 7. Required claims. Values are never echoed.
	if len(reference.RequiredClaims) == 0 {
		add(CheckRequiredClaims, StatusPass, "binding requires no additional claims")
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
		switch {
		case len(missing) > 0 || len(mismatched) > 0:
			var parts []string
			if len(missing) > 0 {
				parts = append(parts, "missing: "+strings.Join(missing, ", "))
			}
			if len(mismatched) > 0 {
				parts = append(parts, "value differs from the binding: "+strings.Join(mismatched, ", "))
			}
			add(CheckRequiredClaims, StatusFail, "required claims not satisfied ("+strings.Join(parts, "; ")+")"+unverified)
		default:
			add(CheckRequiredClaims, StatusPass, "required claims match: "+strings.Join(names, ", ")+unverified)
		}
	}

	// 8. Proof of possession.
	if _, bound := claims["cnf"]; !bound {
		add(CheckProofOfPossession, StatusPass, "bearer token (no cnf confirmation claim)")
	} else if reference.Preset == PresetGCPAgentIdentity {
		if in.Live {
			add(CheckProofOfPossession, StatusPass, "certificate-bound token; the caller verifies the mTLS certificate binding")
		} else {
			add(CheckProofOfPossession, StatusPass, "certificate-bound token; live requests must present the bound client certificate over mTLS")
		}
	} else {
		add(CheckProofOfPossession, StatusFail, "token carries a cnf confirmation claim, which this preset cannot verify; request a bearer token")
	}

	// 9. Binding and agent.
	switch {
	case candidate == nil:
		add(CheckBinding, StatusFail, "no agent identity binding matches this issuer and subject")
	case candidate.Disabled:
		add(CheckBinding, StatusFail, fmt.Sprintf("binding %s/%s is disabled", candidate.ClientID, candidate.ID))
	case e.Agents == nil:
		add(CheckBinding, StatusFail, "agent registry is not configured")
	default:
		agent, err := e.Agents(ctx, candidate.ClientID)
		switch {
		case errors.Is(err, ErrNotFound):
			add(CheckBinding, StatusFail, fmt.Sprintf("binding %s/%s references agent %q, which is not registered", candidate.ClientID, candidate.ID, candidate.ClientID))
		case err != nil:
			add(CheckBinding, StatusFail, "agent registry unavailable")
		case !agent.Active || agent.ActorSubject == "":
			add(CheckBinding, StatusFail, fmt.Sprintf("agent %q is not active", candidate.ClientID))
		default:
			add(CheckBinding, StatusPass, fmt.Sprintf("authenticates as agent %q via binding %s", candidate.ClientID, candidate.ID))
			res.ActorSubject = agent.ActorSubject
		}
	}

	res.Valid = verifiedClaims != nil
	for _, c := range res.Checks {
		if c.Status != StatusPass {
			res.Valid = false
		}
	}
	if res.Valid {
		res.claims = verifiedClaims
		res.binding = candidate
	}
	return res
}

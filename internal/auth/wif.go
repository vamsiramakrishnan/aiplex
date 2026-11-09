package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// WIFValidator validates Workload Identity Federation and Workforce Identity
// Federation tokens, extracts identity claims, and resolves AIPlex roles
// from group memberships.
type WIFValidator struct {
	store           IdentityStore
	workforcePoolID string
	workloadPoolID  string
	trustedIssuers  map[string]bool
	verifier        TokenVerifier
	isWorkforce     bool
	sources         []IdentitySource
	configErr       error
}

// IdentityStore is the persistence surface required by identity resolution.
// Stored user scopes are manual grants only, keyed by canonical principal ID.
type IdentityStore interface {
	ListRoleBindings(context.Context) ([]models.RoleBinding, error)
	GetUserScopes(context.Context, string) ([]string, error)
}

// IdentitySource pins one verified identity domain. Tokens may select a source
// by issuer/audience for dispatch, but only verified claims establish identity.
type IdentitySource struct {
	Issuer      string
	Audience    string
	Verifier    TokenVerifier
	PoolID      string
	IsWorkforce bool
}

// WIFConfig configures the WIF validator.
type WIFConfig struct {
	WorkforcePoolID string
	WorkloadPoolID  string
	TrustedIssuers  []string
	Verifier        TokenVerifier
	IsWorkforce     bool
	Sources         []IdentitySource
}

// NewWIFValidator creates a WIF validator.
func NewWIFValidator(store IdentityStore, cfg WIFConfig) *WIFValidator {
	issuers := make(map[string]bool, len(cfg.TrustedIssuers))
	for _, iss := range cfg.TrustedIssuers {
		issuers[iss] = true
	}
	v := &WIFValidator{
		store:           store,
		workforcePoolID: cfg.WorkforcePoolID,
		workloadPoolID:  cfg.WorkloadPoolID,
		trustedIssuers:  issuers,
		verifier:        cfg.Verifier,
		isWorkforce:     cfg.IsWorkforce,
		sources:         append([]IdentitySource(nil), cfg.Sources...),
	}
	v.configErr = validateSources(v.sources)
	return v
}

func NewWIFValidatorFromSources(store IdentityStore, sources []IdentitySource) (*WIFValidator, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("at least one identity source is required")
	}
	if err := validateSources(sources); err != nil {
		return nil, err
	}
	return NewWIFValidator(store, WIFConfig{Sources: sources}), nil
}

func validateSources(sources []IdentitySource) error {
	for i, source := range sources {
		if source.Issuer == "" || source.PoolID == "" || source.Verifier == nil {
			return fmt.Errorf("identity source requires issuer, pool and verifier")
		}
		for _, previous := range sources[:i] {
			if previous.Issuer == source.Issuer && (previous.Audience == source.Audience || previous.Audience == "" || source.Audience == "") {
				return fmt.Errorf("ambiguous identity sources for issuer %q", source.Issuer)
			}
		}
	}
	return nil
}

// ExtractIdentity accepts identity facts only after cryptographic verification.
// A proxy assertion header does not by itself establish a trusted boundary.
func (v *WIFValidator) ExtractIdentity(r *http.Request) (*models.WIFIdentity, error) {
	// Try IAP JWT first, then standard Authorization header
	token := r.Header.Get("X-Goog-Iap-Jwt-Assertion")
	if token == "" {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			return nil, fmt.Errorf("no authentication token found")
		}
		token = strings.TrimPrefix(auth, "Bearer ")
	}

	if v.configErr != nil {
		return nil, v.configErr
	}
	verifier := v.verifier
	poolID, isWorkforce := v.workloadPoolID, v.isWorkforce
	if isWorkforce {
		poolID = v.workforcePoolID
	}
	var selected *IdentitySource
	if len(v.sources) > 0 {
		routing, err := routingClaims(token)
		if err != nil {
			return nil, err
		}
		for i := range v.sources {
			source := &v.sources[i]
			if routing["iss"] == source.Issuer && (source.Audience == "" || hasAudience(routing["aud"], source.Audience)) {
				if selected != nil {
					return nil, fmt.Errorf("ambiguous identity token source")
				}
				selected = source
			}
		}
		if selected == nil {
			return nil, fmt.Errorf("no trusted identity source matches token")
		}
		verifier, poolID, isWorkforce = selected.Verifier, selected.PoolID, selected.IsWorkforce
	}
	if verifier == nil {
		return nil, fmt.Errorf("identity token verifier is not configured")
	}
	var claims map[string]any
	var err error
	if requestVerifier, ok := verifier.(RequestTokenVerifier); ok {
		claims, err = requestVerifier.VerifyRequest(r, token)
	} else {
		claims, err = verifier.Verify(r.Context(), token)
	}
	if err != nil {
		return nil, fmt.Errorf("verify identity token: %w", err)
	}
	if selected != nil && (claims["iss"] != selected.Issuer || (selected.Audience != "" && !hasAudience(claims["aud"], selected.Audience))) {
		return nil, fmt.Errorf("verified identity source does not match configuration")
	}

	identity := &models.WIFIdentity{}

	// Standard claims
	if sub, ok := claims["sub"].(string); ok {
		identity.Subject = sub
	}
	if email, ok := claims["email"].(string); ok {
		identity.Email = email
	}
	if name, ok := claims["name"].(string); ok {
		identity.DisplayName = name
	}

	// Google WIF-specific claims
	if gcip, ok := claims["google"].(map[string]any); ok {
		if sign, ok := gcip["sign_in_attributes"].(map[string]any); ok {
			if groups, ok := sign["groups"].([]any); ok {
				for _, g := range groups {
					if gs, ok := g.(string); ok {
						identity.Groups = append(identity.Groups, gs)
					}
				}
			}
		}
	}

	// Direct group claims (Azure AD, Okta)
	if groups, ok := claims["groups"].([]any); ok && len(identity.Groups) == 0 {
		for _, g := range groups {
			if gs, ok := g.(string); ok {
				identity.Groups = append(identity.Groups, gs)
			}
		}
	}

	// Google Workspace domain (hd claim)
	if hd, ok := claims["hd"].(string); ok {
		identity.Domain = hd
	}

	// Provider type is trusted deployment configuration, never guessed from
	// substrings in an issuer supplied by the request.
	issuer, _ := claims["iss"].(string)
	if issuer == "" || (len(v.trustedIssuers) > 0 && !v.trustedIssuers[issuer]) {
		return nil, fmt.Errorf("untrusted identity issuer")
	}
	identity.Provider = issuer
	identity.IsWorkforce = isWorkforce
	identity.PoolID = poolID
	if identity.Subject == "" {
		return nil, fmt.Errorf("token contains no immutable subject")
	}
	identity.PrincipalID = CanonicalPrincipalID(identity.Provider, identity.PoolID, identity.Subject)
	identity.Groups = mergeScopes(nil, identity.Groups)
	identity.AuthorizedParty, _ = claims["azp"].(string)
	if actor, ok := claims["act"].(map[string]any); ok {
		identity.ActorSubject, _ = actor["sub"].(string)
		if identity.ActorSubject == "" {
			return nil, fmt.Errorf("delegated actor requires immutable subject")
		}
		identity.ActorID = CanonicalPrincipalID(identity.Provider, identity.PoolID, identity.ActorSubject)
	}

	return identity, nil
}

// routingClaims decodes only enough unverified data to select a configured
// verifier. These claims must never be used as identity facts.
func routingClaims(token string) (map[string]any, error) {
	if len(token) > 64*1024 {
		return nil, fmt.Errorf("identity token exceeds size limit")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("identity source requires JWT token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid identity token encoding")
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil, fmt.Errorf("invalid identity token payload")
	}
	return claims, nil
}

// ResolveAccess takes a WIF identity, looks up role bindings for the
// identity's groups, and returns the merged roles and Dimension B scopes.
func (v *WIFValidator) ResolveAccess(ctx context.Context, identity *models.WIFIdentity) (*models.ResolvedAccess, error) {
	if identity == nil || identity.Subject == "" || identity.Provider == "" {
		return nil, fmt.Errorf("verified identity is required")
	}
	if v.store == nil {
		return nil, fmt.Errorf("identity store is not configured")
	}
	bindings, err := v.store.ListRoleBindings(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list role bindings: %w", err)
	}

	groupSet := make(map[string]bool, len(identity.Groups))
	for _, g := range identity.Groups {
		groupSet[g] = true
	}

	roleSet := make(map[models.IAMRole]bool)
	scopeSet := make(map[string]bool)
	access := &models.ResolvedAccess{Identity: *identity}

	for _, binding := range bindings {
		// Bare group labels are not globally unique. Multi-domain deployments must
		// scope each binding to its identity source to prevent cross-IdP collisions.
		if len(v.sources) > 1 && (binding.Provider == "" || binding.PoolID == "") {
			continue
		}
		if !groupSet[binding.Group] || (binding.Provider != "" && binding.Provider != identity.Provider) || (binding.PoolID != "" && binding.PoolID != identity.PoolID) {
			continue
		}
		roleSet[binding.Role] = true
		access.MatchedBindings = append(access.MatchedBindings, binding.ID)

		// Use binding-specific scopes if provided, otherwise use role defaults
		scopes := binding.Scopes
		if scopes == nil {
			scopes = models.DefaultRoleScopes[binding.Role]
		}
		for _, s := range scopes {
			scopeSet[s] = true
		}
	}

	for role := range roleSet {
		access.Roles = append(access.Roles, role)
	}
	for scope := range scopeSet {
		access.Scopes = append(access.Scopes, scope)
	}

	sort.Slice(access.Roles, func(i, j int) bool { return access.Roles[i] < access.Roles[j] })
	sort.Strings(access.Scopes)
	sort.Strings(access.MatchedBindings)
	return access, nil
}

// SyncUserScopes computes fresh effective scopes without persisting derived
// grants. Historical versions mixed group grants into manual scopes; those
// records require an explicit migration because their provenance was lost.
func (v *WIFValidator) SyncUserScopes(ctx context.Context, identity *models.WIFIdentity) (*models.ResolvedAccess, error) {
	access, err := v.ResolveAccess(ctx, identity)
	if err != nil {
		return nil, err
	}
	principalID := CanonicalPrincipalID(identity.Provider, identity.PoolID, identity.Subject)
	access.Identity.PrincipalID = principalID
	manual, err := v.store.GetUserScopes(ctx, principalID)
	if err != nil {
		return nil, fmt.Errorf("resolve manual scopes: %w", err)
	}
	access.GroupScopes = append([]string(nil), access.Scopes...)
	access.ManualScopes = mergeScopes(nil, manual)
	access.Scopes = mergeScopes(manual, access.GroupScopes)
	return access, nil
}

// CanonicalPrincipalID is stable across email changes, scoped by issuer and
// pool, and safe as a document key. Cross-provider linking must be explicit.
func CanonicalPrincipalID(issuer, pool, subject string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + pool + "\x00" + subject))
	return "principal-" + hex.EncodeToString(sum[:])
}

// ValidateWIFPrincipal checks that a WIF principal string is well-formed.
// Valid formats:
//   - principal://iam.googleapis.com/projects/{project}/locations/global/workloadIdentityPools/{pool}/subject/{sub}
//   - principalSet://iam.googleapis.com/projects/{project}/locations/global/workloadIdentityPools/{pool}/group/{group}
//   - principalSet://iam.googleapis.com/projects/{project}/locations/global/workloadIdentityPools/{pool}/attribute.{attr}/{value}
func ValidateWIFPrincipal(principal string) error {
	if principal == "" {
		return nil
	}
	uri, err := url.Parse(principal)
	if err != nil || (uri.Scheme != "principal" && uri.Scheme != "principalset") || uri.Host != "iam.googleapis.com" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" || strings.ContainsAny(principal, " \t\r\n") {
		return fmt.Errorf("invalid WIF principal URI")
	}
	parts := strings.Split(strings.TrimPrefix(uri.Path, "/"), "/")
	var tail []string
	switch {
	case len(parts) >= 7 && parts[0] == "projects" && parts[2] == "locations" && parts[3] == "global" && parts[4] == "workloadIdentityPools" && parts[5] != "":
		if _, err := strconv.ParseUint(parts[1], 10, 64); err != nil {
			return fmt.Errorf("WIF workload principal requires numeric project number")
		}
		tail = parts[6:]
	case len(parts) >= 5 && parts[0] == "locations" && parts[1] == "global" && parts[2] == "workforcePools" && parts[3] != "":
		tail = parts[4:]
	default:
		return fmt.Errorf("WIF principal requires a complete workforce or workload pool resource")
	}
	if len(tail) == 1 && tail[0] == "*" && uri.Scheme == "principalset" {
		return nil
	}
	if len(tail) < 2 || strings.Join(tail[1:], "/") == "" {
		return fmt.Errorf("WIF principal selector requires a value")
	}
	if uri.Scheme == "principal" && tail[0] == "subject" {
		return nil
	}
	if uri.Scheme == "principalset" && (tail[0] == "group" || (strings.HasPrefix(tail[0], "attribute.") && len(tail[0]) > len("attribute."))) {
		return nil
	}
	return fmt.Errorf("WIF principal scheme and selector do not match")
}

// mergeScopes combines two scope lists, deduplicating entries.
// Wildcard scopes (e.g. mcp:tools:*) subsume specific scopes in the same
// namespace, but we keep both for explicit auditability.
func mergeScopes(existing, additional []string) []string {
	set := make(map[string]bool, len(existing)+len(additional))
	for _, s := range existing {
		if strings.TrimSpace(s) != "" {
			set[s] = true
		}
	}
	for _, s := range additional {
		if strings.TrimSpace(s) != "" {
			set[s] = true
		}
	}
	merged := make([]string, 0, len(set))
	for s := range set {
		merged = append(merged, s)
	}
	sort.Strings(merged)
	return merged
}

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// WIFValidator validates Workload Identity Federation and Workforce Identity
// Federation tokens, extracts identity claims, and resolves AIPlex roles
// from group memberships.
type WIFValidator struct {
	store              registry.Store
	workforcePoolID    string
	workloadPoolID     string
	trustedIssuers     map[string]bool
	httpClient         *http.Client
}

// WIFConfig configures the WIF validator.
type WIFConfig struct {
	WorkforcePoolID string
	WorkloadPoolID  string
	TrustedIssuers  []string
}

// NewWIFValidator creates a WIF validator.
func NewWIFValidator(store registry.Store, cfg WIFConfig) *WIFValidator {
	issuers := make(map[string]bool, len(cfg.TrustedIssuers))
	for _, iss := range cfg.TrustedIssuers {
		issuers[iss] = true
	}
	return &WIFValidator{
		store:           store,
		workforcePoolID: cfg.WorkforcePoolID,
		workloadPoolID:  cfg.WorkloadPoolID,
		trustedIssuers:  issuers,
		httpClient:      &http.Client{Timeout: 10 * time.Second},
	}
}

// ExtractIdentity parses a JWT from an Authorization header and extracts
// the WIF identity claims. The JWT has already been verified by IAP/Envoy
// before reaching AIPlex API, so we decode without re-verifying the signature.
// IAP sets x-goog-iap-jwt-assertion; Envoy ext_authz verifies the token.
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

	claims, err := decodeJWTClaims(token)
	if err != nil {
		return nil, fmt.Errorf("failed to decode token: %w", err)
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

	// Determine pool type from issuer
	if iss, ok := claims["iss"].(string); ok {
		identity.Provider = iss
		// Workforce tokens come through IAP/goog-iap; workload tokens are
		// STS-issued with the workload pool audience
		if strings.Contains(iss, "accounts.google.com") ||
			strings.Contains(iss, "login.microsoftonline.com") {
			identity.IsWorkforce = true
			identity.PoolID = v.workforcePoolID
		} else {
			identity.IsWorkforce = false
			identity.PoolID = v.workloadPoolID
		}
	}

	if identity.Subject == "" && identity.Email == "" {
		return nil, fmt.Errorf("token contains no identifiable subject or email")
	}

	return identity, nil
}

// ResolveAccess takes a WIF identity, looks up role bindings for the
// identity's groups, and returns the merged roles and Dimension B scopes.
func (v *WIFValidator) ResolveAccess(ctx context.Context, identity *models.WIFIdentity) (*models.ResolvedAccess, error) {
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
		if !groupSet[binding.Group] {
			continue
		}
		roleSet[binding.Role] = true

		// Use binding-specific scopes if provided, otherwise use role defaults
		scopes := binding.Scopes
		if len(scopes) == 0 {
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

	return access, nil
}

// SyncUserScopes resolves a WIF identity's access and updates Dimension B
// in the store. This is called on each authenticated request so that
// Dimension B stays in sync with group membership changes in the IdP.
func (v *WIFValidator) SyncUserScopes(ctx context.Context, identity *models.WIFIdentity) (*models.ResolvedAccess, error) {
	access, err := v.ResolveAccess(ctx, identity)
	if err != nil {
		return nil, err
	}

	if len(access.Scopes) == 0 {
		return access, nil
	}

	// Use email as user ID, falling back to subject
	userID := identity.Email
	if userID == "" {
		userID = identity.Subject
	}

	// Merge with any manually-assigned scopes (don't overwrite manual grants)
	existing, _ := v.store.GetUserScopes(ctx, userID)
	merged := mergeScopes(existing, access.Scopes)

	if err := v.store.SetUserScopes(ctx, userID, merged); err != nil {
		return nil, fmt.Errorf("failed to sync user scopes: %w", err)
	}

	access.Scopes = merged
	return access, nil
}

// ValidateWIFPrincipal checks that a WIF principal string is well-formed.
// Valid formats:
//   - principal://iam.googleapis.com/projects/{project}/locations/global/workloadIdentityPools/{pool}/subject/{sub}
//   - principalSet://iam.googleapis.com/projects/{project}/locations/global/workloadIdentityPools/{pool}/group/{group}
//   - principalSet://iam.googleapis.com/projects/{project}/locations/global/workloadIdentityPools/{pool}/attribute.{attr}/{value}
func ValidateWIFPrincipal(principal string) error {
	if principal == "" {
		return nil // optional field
	}
	if !strings.HasPrefix(principal, "principal://iam.googleapis.com/") &&
		!strings.HasPrefix(principal, "principalSet://iam.googleapis.com/") {
		return fmt.Errorf("WIF principal must start with principal:// or principalSet://iam.googleapis.com/")
	}
	if !strings.Contains(principal, "workloadIdentityPools/") &&
		!strings.Contains(principal, "workforcePools/") {
		return fmt.Errorf("WIF principal must reference a workloadIdentityPools or workforcePools resource")
	}
	return nil
}

// decodeJWTClaims decodes the payload of a JWT without verifying the signature.
// Signature verification is handled upstream by IAP or Envoy ext_authz.
func decodeJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid JWT payload encoding: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("invalid JWT payload JSON: %w", err)
	}

	return claims, nil
}

// mergeScopes combines two scope lists, deduplicating entries.
// Wildcard scopes (e.g. mcp:tools:*) subsume specific scopes in the same
// namespace, but we keep both for explicit auditability.
func mergeScopes(existing, additional []string) []string {
	set := make(map[string]bool, len(existing)+len(additional))
	for _, s := range existing {
		set[s] = true
	}
	for _, s := range additional {
		set[s] = true
	}
	merged := make([]string, 0, len(set))
	for s := range set {
		merged = append(merged, s)
	}
	return merged
}

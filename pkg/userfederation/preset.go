// Package userfederation lets a tenant trust an external identity provider for
// human users (workforce federation). It mirrors pkg/agentfederation: a preset
// turns operator inputs into an exact trust tuple, a provider persists that
// tuple tenant-scoped, and the verification engine explains, check by check,
// whether a presented token satisfies a provider.
//
// A provider differs from an agent binding in one respect that shapes every
// default here: it trusts a whole issuer and audience for many users, not one
// subject. Multi-tenant IdPs therefore pin their tenant-scoping claim (hd, tid),
// and the source of group membership is always explicit.
//
// Nothing here provisions IdP-side trust. Setup plans are rendered text.
package userfederation

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
)

// Preset identifiers. They are part of the public API contract.
const (
	PresetGoogleWorkforcePool = "google-workforce-pool"
	PresetGoogleWorkspace     = "google-workspace"
	PresetEntraID             = "entra-id"
	PresetOkta                = "okta"
	PresetOIDC                = "oidc"
)

// Group sources.
const (
	GroupsFromClaim               = "claim"
	GroupsFromMembershipAuthority = "membership-authority"
)

// Field is an operator input description (shared with agent identity presets).
type Field = agentfederation.Field

// Preset is one catalog entry. Derivation is server-side and deterministic.
type Preset struct {
	ID           string  `json:"id"`
	DisplayName  string  `json:"display_name"`
	Vendor       string  `json:"vendor"`
	Description  string  `json:"description"`
	Inputs       []Field `json:"inputs"`
	IssuerShape  string  `json:"issuer_shape"`
	SubjectShape string  `json:"subject_shape"`
	// TenantPinning documents the claim that scopes a multi-tenant issuer to one organisation.
	TenantPinning string `json:"tenant_pinning,omitempty"`
	// GroupSources lists the group sources the preset allows.
	GroupSources []string `json:"group_sources"`
	// AllowsRequiredClaims reports whether extra exact-match claims may be added.
	AllowsRequiredClaims bool     `json:"allows_required_claims"`
	Algorithms           []string `json:"algorithms"`
	Notes                []string `json:"notes,omitempty"`
	// ConfirmedAgainstDocs lists what was checked against vendor documentation;
	// OperatorSupplied lists the values the preset refuses to guess.
	OperatorSupplied []string `json:"operator_supplied,omitempty"`
	derive           func(in map[string]string) (Trust, error)
	defaultGroupName string // default group claim when groups_from=claim ("" = must be explicit)
}

// Trust is the derived, exact trust tuple a provider pins.
type Trust struct {
	Issuer       string `json:"issuer"`
	JWKSURL      string `json:"jwks_url"`
	Audience     string `json:"audience"`
	SubjectClaim string `json:"subject_claim"`
	// RequiredClaims are exact-match claims, including the pinned tenant claim.
	RequiredClaims map[string]string `json:"required_claims,omitempty"`
	// GroupsFrom is "claim" or "membership-authority".
	GroupsFrom string `json:"groups_from"`
	// GroupClaim names the verified claim carrying groups (GroupsFrom=claim only).
	GroupClaim string `json:"group_claim,omitempty"`
}

var supportedAlgorithms = []string{"RS256", "ES256"}

var (
	guid      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	dnsDomain = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	groupPath = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_:-]{0,127}(\.[A-Za-z0-9_:-]{1,128}){0,4}$`)
)

func groupFields(claimDescription string) []Field {
	return []Field{
		{Name: "groups_from", Label: "Group source", Description: "Where group membership comes from. `claim` reads a named claim of the verified token; `membership-authority` takes groups from the configured membership authority (see docs/membership-authority.md) and ignores any group claim in the token. There is no default: choose explicitly. A missing, null, malformed or overage group claim means groups are unavailable, never an empty membership.", Required: true, Enum: []string{GroupsFromClaim, GroupsFromMembershipAuthority}},
		{Name: "group_claim", Label: "Group claim", Description: claimDescription, Example: "groups"},
	}
}

func audienceField(description, example string) Field {
	return Field{Name: "audience", Label: "Audience", Description: description, Required: true, Example: example}
}

// Catalog returns all presets in a stable order.
func Catalog() []Preset { return presetList() }

// Lookup returns one preset by ID.
func Lookup(id string) (Preset, bool) {
	for _, p := range presetList() {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// Derive validates operator inputs strictly and returns the exact trust tuple.
// Unknown inputs are rejected; there are no defaults for audience or group source.
func Derive(presetID string, inputs map[string]string, extraClaims map[string]string) (Trust, error) {
	preset, ok := Lookup(presetID)
	if !ok {
		return Trust{}, fmt.Errorf("unknown user identity preset %q", presetID)
	}
	known := map[string]Field{}
	for _, f := range preset.Inputs {
		known[f.Name] = f
	}
	clean := map[string]string{}
	for name, value := range inputs {
		field, ok := known[name]
		if !ok {
			return Trust{}, fmt.Errorf("input %q is not defined by preset %s", name, presetID)
		}
		if value != strings.TrimSpace(value) || len(value) > 1024 || strings.ContainsAny(value, "\x00\r\n\t") {
			return Trust{}, fmt.Errorf("input %q must be a single bounded value without surrounding whitespace", name)
		}
		if len(field.Enum) > 0 && value != "" && !agentfederation.Contains(field.Enum, value) {
			return Trust{}, fmt.Errorf("input %q must be one of %s", name, strings.Join(field.Enum, ", "))
		}
		if value != "" {
			clean[name] = value
		}
	}
	for _, f := range preset.Inputs {
		if _, set := clean[f.Name]; !set && f.Default != "" {
			clean[f.Name] = f.Default
		}
		if f.Required && clean[f.Name] == "" {
			return Trust{}, fmt.Errorf("input %q is required for preset %s", f.Name, presetID)
		}
	}
	if err := agentfederation.ValidAudience(clean["audience"]); err != nil {
		return Trust{}, err
	}
	trust, err := preset.derive(clean)
	if err != nil {
		return Trust{}, err
	}
	trust.Audience = clean["audience"]
	trust.GroupsFrom = clean["groups_from"]
	switch trust.GroupsFrom {
	case GroupsFromClaim:
		if !agentfederation.Contains(preset.GroupSources, GroupsFromClaim) {
			return Trust{}, fmt.Errorf("preset %s cannot read groups from a token claim; use groups_from %q", presetID, GroupsFromMembershipAuthority)
		}
		trust.GroupClaim = clean["group_claim"]
		if trust.GroupClaim == "" {
			trust.GroupClaim = preset.defaultGroupName
		}
		if trust.GroupClaim == "" {
			return Trust{}, fmt.Errorf("group_claim is required when groups_from is %q", GroupsFromClaim)
		}
		if !validGroupClaim(trust.GroupClaim) {
			return Trust{}, fmt.Errorf("group_claim %q is not a valid claim name or reserved", trust.GroupClaim)
		}
	case GroupsFromMembershipAuthority:
		if clean["group_claim"] != "" {
			return Trust{}, fmt.Errorf("group_claim must be empty when groups_from is %q", GroupsFromMembershipAuthority)
		}
	}
	if len(extraClaims) > 0 && !preset.AllowsRequiredClaims {
		return Trust{}, fmt.Errorf("preset %s does not accept additional required claims", presetID)
	}
	for name, value := range extraClaims {
		if trust.RequiredClaims == nil {
			trust.RequiredClaims = map[string]string{}
		}
		if existing, ok := trust.RequiredClaims[name]; ok && existing != value {
			return Trust{}, fmt.Errorf("required claim %q conflicts with the value pinned by the preset", name)
		}
		trust.RequiredClaims[name] = value
	}
	if err := trust.Validate(); err != nil {
		return Trust{}, err
	}
	return trust, nil
}

// reservedGroupClaims are identity or control claims that can never carry groups.
var reservedGroupClaims = map[string]bool{"iss": true, "sub": true, "aud": true, "exp": true, "nbf": true, "iat": true, "jti": true, "act": true, "azp": true, "cnf": true, "scope": true, "scp": true, "tid": true, "oid": true, "hd": true, "email": true}

func validGroupClaim(name string) bool {
	return groupPath.MatchString(name) && !reservedGroupClaims[name] && !reservedGroupClaims[strings.SplitN(name, ".", 2)[0]]
}

// Validate checks a derived trust tuple: https only, exact issuer, explicit
// audience, an immutable subject claim, an explicit group source, no wildcards.
func (t Trust) Validate() error {
	if err := agentfederation.ValidHTTPSURL(t.Issuer, "issuer", true); err != nil {
		return err
	}
	if err := agentfederation.ValidHTTPSURL(t.JWKSURL, "JWKS URL", false); err != nil {
		return err
	}
	if err := agentfederation.ValidAudience(t.Audience); err != nil {
		return err
	}
	if t.SubjectClaim != "sub" && t.SubjectClaim != "oid" {
		return fmt.Errorf("subject claim must be sub or oid")
	}
	if len(t.RequiredClaims) > 16 {
		return fmt.Errorf("at most 16 required claims")
	}
	for name, value := range t.RequiredClaims {
		if !agentfederation.ValidClaimName(name) {
			return fmt.Errorf("required claim name %q is invalid or reserved (issuer, subject, audience and lifetime are checked separately)", name)
		}
		if err := agentfederation.ExactValue("required claim "+name, value); err != nil {
			return err
		}
	}
	switch t.GroupsFrom {
	case GroupsFromClaim:
		if !validGroupClaim(t.GroupClaim) {
			return fmt.Errorf("group claim is required when groups_from is %q", GroupsFromClaim)
		}
	case GroupsFromMembershipAuthority:
		if t.GroupClaim != "" {
			return fmt.Errorf("group claim must be empty when groups_from is %q", GroupsFromMembershipAuthority)
		}
	default:
		return fmt.Errorf("groups_from must be %q or %q", GroupsFromClaim, GroupsFromMembershipAuthority)
	}
	return nil
}

func issuerOrigin(issuer string) error {
	if err := agentfederation.ValidHTTPSURL(issuer, "issuer", true); err != nil {
		return err
	}
	return nil
}

func presetList() []Preset {
	claimOrAuthority := []string{GroupsFromClaim, GroupsFromMembershipAuthority}
	presets := []Preset{
		{
			ID: PresetGoogleWorkforcePool, DisplayName: "Google Workforce Identity Federation pool", Vendor: "google",
			Description:      "Users federated into a Google Cloud workforce pool. The issuer and signing keys that a third-party verifier can use for pool-issued tokens are not published in the documentation AIPlex was checked against, so both are operator inputs.",
			IssuerShape:      "operator input (exact iss of the tokens you present)",
			SubjectShape:     "sub",
			GroupSources:     claimOrAuthority,
			OperatorSupplied: []string{"issuer", "jwks_url", "group_claim"},
			Inputs: append([]Field{
				{Name: "issuer", Label: "Token issuer", Description: "Exact `iss` of the tokens you will present. AIPlex does not guess the workforce pool issuer: copy it from a decoded token or the issuer's discovery document.", Required: true},
				{Name: "jwks_url", Label: "JWKS URL", Description: "Exact public HTTPS `jwks_uri` of that issuer. It must be reachable from AIPlex under the outbound endpoint policy.", Required: true},
				{Name: "pool_id", Label: "Workforce pool ID", Description: "Optional. Documents which pool the tokens come from and is used in the rendered plan; it is not matched against the token.", Example: "acme-employees"},
				audienceField("Exact `aud` of the tokens (for example the workforce provider resource name or OAuth client ID your IdP issues for AIPlex).", "//iam.googleapis.com/locations/global/workforcePools/acme-employees/providers/acme-oidc"),
			}, groupFields("Name of the verified claim that carries groups, for example google.groups or a custom claim. A dotted name reaches into a nested object. Required when groups_from is `claim`; AIPlex has no verified default for this issuer.")...),
			Notes:                []string{"Google documents the workforce STS token exchange and introspection endpoints but no issuer or JWKS for relying parties; if your tokens are the upstream IdP's ID tokens, the entra-id, okta or oidc preset is usually the more direct description of the trust.", "The token must be a signed RS256 or ES256 JWT with a key ID."},
			AllowsRequiredClaims: true,
			derive: func(in map[string]string) (Trust, error) {
				if err := issuerOrigin(in["issuer"]); err != nil {
					return Trust{}, err
				}
				if p := in["pool_id"]; p != "" && !regexp.MustCompile(`^[a-z][a-z0-9-]{2,61}[a-z0-9]$`).MatchString(p) {
					return Trust{}, fmt.Errorf("pool_id must be a workforce pool ID")
				}
				return Trust{Issuer: in["issuer"], JWKSURL: in["jwks_url"], SubjectClaim: "sub"}, nil
			},
		},
		{
			ID: PresetGoogleWorkspace, DisplayName: "Google Workspace sign-in", Vendor: "google",
			Description:      "Google ID tokens for users of one Workspace domain. accounts.google.com signs tokens for every Google account, so the Workspace domain is pinned through the hd claim.",
			IssuerShape:      "https://accounts.google.com",
			SubjectShape:     "sub (stable Google account ID)",
			TenantPinning:    "hd = Workspace primary domain (required)",
			GroupSources:     []string{GroupsFromMembershipAuthority},
			OperatorSupplied: []string{"audience"},
			Inputs: append([]Field{
				{Name: "hosted_domain", Label: "Workspace domain", Description: "Primary Workspace domain (lower-case). Pinned as the hd claim, so personal Google accounts and other organisations are rejected.", Required: true, Example: "example.com"},
				audienceField("OAuth client ID of the Google Cloud OAuth client your users sign in with for AIPlex (the aud of the ID token). No default.", "1234567890-abc.apps.googleusercontent.com"),
			}, groupFields("Not used: Google ID tokens carry no group claim. Leave empty.")[0]),
			Notes: []string{"Google ID tokens do not carry group membership; groups_from must be membership-authority."},
			derive: func(in map[string]string) (Trust, error) {
				d := in["hosted_domain"]
				if !dnsDomain.MatchString(d) {
					return Trust{}, fmt.Errorf("hosted_domain must be a lower-case DNS domain")
				}
				return Trust{Issuer: "https://accounts.google.com", JWKSURL: "https://www.googleapis.com/oauth2/v3/certs", SubjectClaim: "sub", RequiredClaims: map[string]string{"hd": d}}, nil
			},
		},
		{
			ID: PresetEntraID, DisplayName: "Microsoft Entra ID", Vendor: "microsoft",
			Description:      "Entra ID tokens for users of one tenant, issued to the Entra application that represents AIPlex.",
			IssuerShape:      "https://login.microsoftonline.com/{tenant}/v2.0 (or https://sts.windows.net/{tenant}/ for v1.0 tokens)",
			SubjectShape:     "oid (immutable object ID; sub is pairwise per application)",
			TenantPinning:    "tid = tenant GUID (required); the tenant is also part of the issuer",
			GroupSources:     claimOrAuthority,
			defaultGroupName: "groups",
			Inputs: append([]Field{
				{Name: "tenant_id", Label: "Entra tenant ID", Description: "Directory (tenant) GUID, lower-case. Common, organizations and consumers endpoints are never accepted.", Required: true, Example: "72f988bf-86f1-41af-91ab-2d7cd011db47"},
				audienceField("Exact `aud`: for v2.0 tokens the client ID of the Entra application representing AIPlex; for v1.0 tokens the Application ID URI or client ID. No default.", "11112222-bbbb-3333-cccc-4444dddd5555"),
				{Name: "token_version", Label: "Token version", Description: "Access token version the AIPlex application issues (requestedAccessTokenVersion). Determines the issuer. ID tokens are v2.0.", Enum: []string{"2.0", "1.0"}, Default: "2.0"},
			}, groupFields("Claim carrying groups. Defaults to `groups` (group object IDs); `roles` carries app roles. Entra omits the claim and sends hasgroups or _claim_names instead when a user has too many groups; AIPlex then treats groups as unavailable, not empty.")...),
			Notes:                []string{"Configure the groups claim on the application (Token configuration > Groups claim). 'Groups assigned to the application' avoids most overage.", "Entra limits groups to 200 per JWT; beyond that the token carries an overage indicator and AIPlex reports groups as unavailable. Use groups_from membership-authority if overage is expected."},
			AllowsRequiredClaims: true,
			derive: func(in map[string]string) (Trust, error) {
				tenant := in["tenant_id"]
				if !guid.MatchString(tenant) {
					return Trust{}, fmt.Errorf("tenant_id must be a lower-case GUID")
				}
				issuer := "https://login.microsoftonline.com/" + tenant + "/v2.0"
				if in["token_version"] == "1.0" {
					issuer = "https://sts.windows.net/" + tenant + "/"
				}
				return Trust{Issuer: issuer, JWKSURL: "https://login.microsoftonline.com/" + tenant + "/discovery/v2.0/keys", SubjectClaim: "oid", RequiredClaims: map[string]string{"tid": tenant}}, nil
			},
		},
		{
			ID: PresetOkta, DisplayName: "Okta", Vendor: "okta",
			Description:      "Okta ID or access tokens from an authorization server. Issuer and JWKS URL are operator inputs copied from the server's discovery document.",
			IssuerShape:      "https://{okta-domain} (org server) or https://{okta-domain}/oauth2/{server-id} (custom server)",
			SubjectShape:     "sub",
			GroupSources:     claimOrAuthority,
			defaultGroupName: "groups",
			OperatorSupplied: []string{"issuer", "jwks_url"},
			Inputs: append([]Field{
				{Name: "issuer", Label: "Authorization server issuer", Description: "Exact `issuer` from the authorization server's discovery document (/.well-known/openid-configuration or /.well-known/oauth-authorization-server).", Required: true, Example: "https://acme.okta.com/oauth2/default"},
				{Name: "jwks_url", Label: "JWKS URL", Description: "Exact `jwks_uri` from the same discovery document.", Required: true, Example: "https://acme.okta.com/oauth2/default/v1/keys"},
				audienceField("Exact `aud`: for custom authorization servers the audience configured on the server; for ID tokens the client ID. No default.", "api://aiplex"),
			}, groupFields("Claim carrying groups. Defaults to `groups`. Okta does not emit it by default: add a groups claim with a filter to the ID token or authorization server, and request the groups scope.")...),
			Notes:                []string{"Okta documents that groups are included only when the groups scope is requested and a groups claim is configured, limited by the claim's filter."},
			AllowsRequiredClaims: true,
			derive: func(in map[string]string) (Trust, error) {
				if err := issuerOrigin(in["issuer"]); err != nil {
					return Trust{}, err
				}
				return Trust{Issuer: in["issuer"], JWKSURL: in["jwks_url"], SubjectClaim: "sub"}, nil
			},
		},
		{
			ID: PresetOIDC, DisplayName: "Generic OIDC provider", Vendor: "other",
			Description:      "Any OIDC issuer that signs RS256/ES256 JWTs with a public JWKS. Every value is explicit.",
			IssuerShape:      "explicit",
			SubjectShape:     "sub or oid (explicit)",
			GroupSources:     claimOrAuthority,
			OperatorSupplied: []string{"issuer", "jwks_url", "group_claim"},
			Inputs: append([]Field{
				{Name: "issuer", Label: "Issuer", Description: "Exact iss value.", Required: true},
				{Name: "jwks_url", Label: "JWKS URL", Description: "Exact public HTTPS JWKS URL.", Required: true},
				{Name: "subject_claim", Label: "Subject claim", Description: "Immutable user identifier claim: sub, or oid where the IdP's sub is not stable.", Enum: []string{"sub", "oid"}, Default: "sub"},
				audienceField("Exact audience.", "https://aiplex.example.com"),
			}, groupFields("Name of the verified claim that carries groups. Required when groups_from is `claim`.")...),
			Notes:                []string{"If the issuer serves several organisations, add a tenant-scoping claim under required_claims so another organisation's users are rejected."},
			AllowsRequiredClaims: true,
			derive: func(in map[string]string) (Trust, error) {
				if err := issuerOrigin(in["issuer"]); err != nil {
					return Trust{}, err
				}
				return Trust{Issuer: in["issuer"], JWKSURL: in["jwks_url"], SubjectClaim: in["subject_claim"]}, nil
			},
		},
	}
	for i := range presets {
		presets[i].Algorithms = append([]string(nil), supportedAlgorithms...)
	}
	return presets
}

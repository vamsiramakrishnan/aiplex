package userfederation

import (
	"fmt"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
)

// Step is one reviewable setup step. Plans are text; nothing is executed.
type Step = agentfederation.Step

// Plan is the rendered IdP-side setup and token acquisition for one provider.
type Plan struct {
	Provider ProviderRef `json:"provider"`
	Vendor   string      `json:"vendor"`
	Issuer   string      `json:"issuer"`
	Audience string      `json:"audience"`
	// SubjectClaim is the immutable identifier that becomes the canonical principal.
	SubjectClaim string `json:"subject_claim"`
	GroupsFrom   string `json:"groups_from"`
	GroupClaim   string `json:"group_claim,omitempty"`
	// PrincipalPool is the pool ID to use in role bindings for these users.
	PrincipalPool string   `json:"principal_pool"`
	Steps         []Step   `json:"steps"`
	TokenSnippet  Step     `json:"token_snippet"`
	AIPlex        []Step   `json:"aiplex_steps"`
	Notes         []string `json:"notes,omitempty"`
}

// PlanContext supplies server facts used in rendered text.
type PlanContext struct {
	APIBaseURL string
	// PrincipalPool is the identity pool of this tenant's federated users.
	PrincipalPool string
	// FederationEnabled reports whether live authentication is switched on.
	FederationEnabled bool
}

// RenderPlan renders setup steps for a provider. Only commands whose syntax is
// certain are emitted; console navigation is described in prose and should be
// checked against the vendor's current documentation.
func RenderPlan(p Provider, pc PlanContext) (Plan, error) {
	preset, ok := Lookup(p.Preset)
	if !ok {
		return Plan{}, fmt.Errorf("unknown user identity preset %q", p.Preset)
	}
	plan := Plan{Provider: ProviderRef{ID: p.ID, Preset: p.Preset}, Vendor: preset.Vendor, Issuer: p.Issuer, Audience: p.Audience, SubjectClaim: p.SubjectClaim, GroupsFrom: p.GroupsFrom, GroupClaim: p.GroupClaim, PrincipalPool: pc.PrincipalPool, Notes: append([]string(nil), preset.Notes...)}
	api := strings.TrimSuffix(pc.APIBaseURL, "/")
	if api == "" {
		api = "https://AIPLEX_HOST"
	}
	callAIPlex := "curl -sS -H \"Authorization: Bearer $TOKEN\" " + agentfederation.ShellQuote(api+"/api/v1/iam/whoami")
	signIn := Step{Title: "Obtain a token", Description: "Users sign in through the IdP with an interactive flow (authorization code with PKCE for apps and IDEs, device authorization for CLIs) requesting audience " + p.Audience + ", and send the resulting signed JWT as `Authorization: Bearer <token>`. AIPlex never sees or stores the user's password.", Language: "bash", Content: "TOKEN=<token from your sign-in flow>\n" + callAIPlex}

	switch p.Preset {
	case PresetGoogleWorkforcePool:
		plan.Steps = []Step{
			{Title: "Decide which token you present", Description: "Google does not document an issuer or key set for relying parties that verify workforce pool tokens. Present a signed RS256/ES256 JWT whose `iss` is exactly " + p.Issuer + " and whose keys are served at " + p.JWKSURL + " over public HTTPS. If you present the upstream IdP's ID token instead, consider the entra-id, okta or oidc preset."},
			{Title: "Confirm the audience", Description: "The token's aud must include " + p.Audience + "."},
		}
		plan.TokenSnippet = signIn
	case PresetGoogleWorkspace:
		plan.Steps = []Step{
			{Title: "Create an OAuth client", Description: "In the Google Cloud console (APIs & Services > Credentials) create an OAuth 2.0 client of the type that fits your app. Its client ID is the audience " + p.Audience + ". Restrict the consent screen to internal users of the Workspace organisation where possible."},
			{Title: "Request an ID token", Description: "Use the scopes `openid email`. Authorization endpoint https://accounts.google.com/o/oauth2/v2/auth, token endpoint https://oauth2.googleapis.com/token; the response includes an `id_token`. AIPlex accepts it only when the hd claim equals " + p.RequiredClaims["hd"] + "."},
		}
		plan.Notes = append(plan.Notes, "Google ID tokens carry no group claim; role bindings that match groups need the membership authority.")
		plan.TokenSnippet = signIn
	case PresetEntraID:
		plan.Steps = []Step{
			{Title: "Register the AIPlex application", Description: "In the Microsoft Entra admin center (tenant " + p.RequiredClaims["tid"] + ") register an application for AIPlex. Its client ID (v2.0) or Application ID URI (v1.0) must equal the audience " + p.Audience + "."},
			{Title: "Add the groups claim", Description: "Under Token configuration add a groups claim. Prefer 'Groups assigned to the application' to stay below the 200-group token limit. Users with too many groups get an overage indicator instead of a list; AIPlex then reports their groups as unavailable (not empty)."},
			{Title: "Allow users", Description: "Under Enterprise applications, require assignment and assign the users or groups who may sign in."},
		}
		plan.TokenSnippet = Step{Title: "Obtain a token", Description: "Sign users in with the OAuth 2.0 authorization code flow with PKCE (or device code flow for CLIs) against https://login.microsoftonline.com/" + p.RequiredClaims["tid"] + "/oauth2/v2.0/, requesting a scope of the AIPlex application. Identity is the immutable oid claim.", Language: "bash", Content: "TOKEN=<token from your sign-in flow>\n" + callAIPlex}
	case PresetOkta:
		plan.Steps = []Step{
			{Title: "Confirm issuer and keys", Description: "The discovery document of the authorization server must report issuer " + p.Issuer + " and jwks_uri " + p.JWKSURL + "."},
			{Title: "Configure the audience", Description: "On the authorization server (Security > API) set the audience to " + p.Audience + ", or use the application's client ID for ID tokens."},
			{Title: "Configure the groups claim", Description: "Add a groups claim to the token with a filter that selects the groups AIPlex should see, and request the `groups` scope. Okta does not include it otherwise."},
		}
		plan.TokenSnippet = signIn
	default:
		plan.Steps = []Step{{Title: "Configure the issuer", Description: "Ensure " + p.Issuer + " signs RS256 or ES256 tokens for audience " + p.Audience + " with keys at " + p.JWKSURL + ", reachable under the AIPlex outbound endpoint policy, and that the " + p.SubjectClaim + " claim is an immutable user identifier."}}
		plan.TokenSnippet = signIn
	}
	if p.GroupsFrom == GroupsFromClaim {
		plan.Steps = append(plan.Steps, Step{Title: "Groups from the token", Description: "AIPlex reads groups from the verified claim `" + p.GroupClaim + "` only. If it is missing, null, malformed or flagged as overage, the user authenticates without groups and cannot match group role bindings."})
	} else {
		plan.Steps = append(plan.Steps, Step{Title: "Groups from the membership authority", Description: "Token claims are ignored for groups. Configure a membership authority (docs/membership-authority.md) so group-based role bindings and quota eligibility can resolve."})
	}

	plan.AIPlex = []Step{
		{Title: "Dry-run a token", Description: "Paste a freshly acquired token. The response lists every check, the resulting principal ID and the verified group list (or why groups are unavailable); the token is never stored or echoed.", Language: "bash", Content: "curl -sS -X POST -H \"Authorization: Bearer $ADMIN_TOKEN\" -H 'Content-Type: application/json' -d \"{\\\"token\\\": \\\"$TOKEN\\\", \\\"provider_id\\\": \\\"" + p.ID + "\\\"}\" " + agentfederation.ShellQuote(api+"/api/v1/identity/user-providers/verify")},
		{Title: "Grant permissions", Description: "A provider grants no scopes by itself. Users authenticate as principals in pool `" + pc.PrincipalPool + "` under issuer " + p.Issuer + "; create role bindings scoped to that provider and pool, or grant scopes to a principal directly."},
	}
	if !pc.FederationEnabled {
		plan.AIPlex = append(plan.AIPlex, Step{Title: "Enable live federation", Description: "Live authentication with user identity providers is off on this server. Set AIPLEX_USER_FEDERATION=enabled (with AIPLEX_TENANT_ID) and restart; dry-run verification works either way."})
	}
	return plan, nil
}

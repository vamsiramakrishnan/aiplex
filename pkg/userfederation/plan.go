package userfederation

import (
	"fmt"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/directory"
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
	// Directory summarises the directory group source, when configured.
	Directory *PlanDirectory `json:"directory,omitempty"`
	// ClientClaim is the verified claim carried as the authorized party ("" = none).
	ClientClaim string   `json:"client_claim,omitempty"`
	ClientIDs   []string `json:"client_ids,omitempty"`
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
	// SecretName maps a secret reference to the deployment's secret name in
	// the tenant's namespace (nil = unknown; then no command is rendered).
	SecretName func(ref string) string
	// DirectoryCacheTTL is the server's directory membership cache lifetime.
	DirectoryCacheTTL string
}

// PlanDirectory describes the directory group source of a provider.
type PlanDirectory struct {
	Kind   string `json:"kind"`
	Mode   string `json:"mode"`
	Source string `json:"source"`
}

// RenderPlan renders setup steps for a provider. Only commands whose syntax is
// certain are emitted; console navigation is described in prose and should be
// checked against the vendor's current documentation.
func RenderPlan(p Provider, pc PlanContext) (Plan, error) {
	preset, ok := Lookup(p.Preset)
	if !ok {
		return Plan{}, fmt.Errorf("unknown user identity preset %q", p.Preset)
	}
	plan := Plan{Provider: ProviderRef{ID: p.ID, Preset: p.Preset}, Vendor: preset.Vendor, Issuer: p.Issuer, Audience: p.Audience, SubjectClaim: p.SubjectClaim, GroupsFrom: p.GroupsFrom, GroupClaim: p.GroupClaim, ClientClaim: p.ClientClaim, ClientIDs: append([]string(nil), p.ClientIDs...), PrincipalPool: pc.PrincipalPool, Notes: append([]string(nil), preset.Notes...)}
	api := strings.TrimSuffix(pc.APIBaseURL, "/")
	if api == "" {
		api = "https://AIPLEX_HOST"
	}
	callAIPlex := "curl -sS -H \"Authorization: Bearer $TOKEN\" " + agentfederation.ShellQuote(api+"/api/v1/iam/whoami")
	signIn := Step{Title: "Obtain a token", Description: "Users sign in through the IdP with an interactive flow (authorization code with PKCE for apps and IDEs, device authorization for CLIs) requesting audience " + p.Audience + ", and send the resulting signed JWT as `Authorization: Bearer <token>`. AIPlex never sees or stores the user's password.", Language: "bash", Content: "TOKEN=<token from your sign-in flow>\n" + callAIPlex}

	switch p.Preset {
	case PresetGoogleWorkforcePool:
		plan.Steps = []Step{
			{Title: "Decide which token you present", Description: "Google does not document an issuer or key set for relying parties that verify workforce pool tokens. Present a signed RS256/ES256/ES384 JWT whose `iss` is exactly " + p.Issuer + " and whose keys are served at " + p.JWKSURL + " over public HTTPS. If you present the upstream IdP's ID token instead, consider the entra-id, okta or oidc preset."},
			{Title: "Confirm the audience", Description: "The token's aud must include " + p.Audience + "."},
		}
		plan.TokenSnippet = signIn
	case PresetGoogleWorkspace:
		plan.Steps = []Step{
			{Title: "Create an OAuth client", Description: "In the Google Cloud console (APIs & Services > Credentials) create an OAuth 2.0 client of the type that fits your app. Its client ID is the audience " + p.Audience + ". Restrict the consent screen to internal users of the Workspace organisation where possible."},
			{Title: "Request an ID token", Description: "Use the scopes `openid email`. Authorization endpoint https://accounts.google.com/o/oauth2/v2/auth, token endpoint https://oauth2.googleapis.com/token; the response includes an `id_token`. AIPlex accepts it only when the hd claim equals " + p.RequiredClaims["hd"] + "."},
		}
		plan.Notes = append(plan.Notes, "Google ID tokens carry no group claim; role bindings that match groups need a google-cloud-identity directory (or a quota membership authority).")
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
		plan.Steps = []Step{{Title: "Configure the issuer", Description: "Ensure " + p.Issuer + " signs RS256, ES256 or ES384 tokens for audience " + p.Audience + " with keys at " + p.JWKSURL + ", reachable under the AIPlex outbound endpoint policy, and that the " + p.SubjectClaim + " claim is an immutable user identifier."}}
		plan.TokenSnippet = signIn
	}
	switch {
	case p.Directory != nil && p.Directory.Mode == directory.ModeAlways:
		plan.Steps = append(plan.Steps, Step{Title: "Groups from the directory", Description: "Token group claims are ignored. Every verified user's groups are resolved transitively through the " + p.Directory.Kind + " directory; if the directory is unreachable, throttles or returns an incomplete view, the user's groups are unavailable (group rules fail closed) and token groups are never used instead."})
	case p.Directory != nil:
		plan.Steps = append(plan.Steps, Step{Title: "Groups from the token, then the directory", Description: "AIPlex reads groups from the verified claim `" + p.GroupClaim + "`. When it is missing, null, malformed or replaced by an overage indicator, groups are resolved transitively through the " + p.Directory.Kind + " directory instead. A directory failure leaves groups unavailable, never empty."})
	case p.GroupsFrom == GroupsFromClaim:
		plan.Steps = append(plan.Steps, Step{Title: "Groups from the token", Description: "AIPlex reads groups from the verified claim `" + p.GroupClaim + "` only. If it is missing, null, malformed or flagged as overage, the user authenticates without groups and cannot match group role bindings. Add a directory block to resolve those users through the IdP's directory."})
	default:
		plan.Steps = append(plan.Steps, Step{Title: "Groups from the membership authority", Description: "Token claims are ignored for groups. Add a directory block to this provider, or configure a quota membership authority (docs/membership-authority.md); without either, users have no group view and group-based role bindings never match."})
	}
	if p.Directory != nil {
		plan.Directory = &PlanDirectory{Kind: p.Directory.Kind, Mode: p.Directory.Mode, Source: GroupSourceDirectory(p.Directory.Kind)}
		plan.Steps = append(plan.Steps, directorySteps(p, pc, api)...)
	}
	if p.ClientClaim != "" {
		desc := "Requests are attributed to the client application named by the verified `" + p.ClientClaim + "` claim."
		if len(p.ClientIDs) > 0 {
			desc += " Only tokens issued to " + strings.Join(p.ClientIDs, ", ") + " are accepted."
		}
		plan.Steps = append(plan.Steps, Step{Title: "Client attribution", Description: desc})
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

func secretStep(pc PlanContext, title, ref, what, env string) Step {
	step := Step{Title: title, Description: "Store " + what + " as secret reference `" + ref + "` in this tenant's secret namespace. AIPlex reads it only for directory lookups and sends it only to the vendor's pinned origin."}
	if pc.SecretName != nil {
		name := pc.SecretName(ref)
		step.Description += " With Secret Manager the secret is named " + name + "."
		step.Language = "bash"
		step.Content = "printf '%s' \"$" + env + "\" | gcloud secrets create " + agentfederation.ShellQuote(name) + " --data-file=-"
	}
	return step
}

// directorySteps renders the directory-side setup. Console navigation is prose
// to be checked against the vendor's current documentation; commands appear
// only where their syntax is certain.
func directorySteps(p Provider, pc PlanContext, api string) []Step {
	d := p.Directory
	var steps []Step
	switch d.Kind {
	case directory.KindEntraGraph:
		steps = append(steps,
			Step{Title: "Register a directory-reader application", Description: "In the Microsoft Entra admin center of tenant " + p.RequiredClaims["tid"] + ", register a single-tenant application for AIPlex directory lookups. Its Application (client) ID must equal directory.credential.client_id (" + d.Credential.ClientID + ")."},
			Step{Title: "Grant the Graph application permission", Description: "Under API permissions add the Microsoft Graph application permission User.Read.All (the least-privileged application permission documented for GET /users/{id}/transitiveMemberOf; GroupMember.Read.All also works) and grant admin consent. AIPlex calls https://graph.microsoft.com/v1.0/users/{oid}/transitiveMemberOf with an app-only token from https://login.microsoftonline.com/" + p.RequiredClaims["tid"] + "/oauth2/v2.0/token. To read groups with hidden membership, Member.Read.Hidden is also required."},
			secretStep(pc, "Create and store a client secret", d.Credential.ClientSecretRef, "a client secret of that application (Certificates & secrets > New client secret)", "CLIENT_SECRET"),
		)
	case directory.KindGoogleCloudIdentity:
		steps = append(steps,
			Step{Title: "Enable the Cloud Identity API", Description: "Enable the API in the Google Cloud project whose credentials AIPlex uses.", Language: "bash", Content: "gcloud services enable cloudidentity.googleapis.com"},
			Step{Title: "Let the AIPlex principal read group memberships", Description: "searchTransitiveGroups requires permission to view the membership of every group in the result (otherwise PERMISSION_DENIED) and a Google Workspace Enterprise Standard/Plus, Enterprise for Education or Cloud Identity Premium edition. In the Google Admin console, assign the service account behind identity profile `" + d.Credential.IdentityRef + "` an admin role that can read groups and their members (check Google's current documentation for the role name)."},
			Step{Title: "Configure the identity profile", Description: "Identity profile `" + d.Credential.IdentityRef + "` must be of type gcp-adc for this tenant, with scope https://www.googleapis.com/auth/cloud-identity.groups.readonly (or the default cloud-platform), and allowed_origins including https://cloudidentity.googleapis.com, which must also be allowed by the outbound endpoint policy. Users are looked up by their verified email (email_verified must be true)."},
		)
	case directory.KindOkta:
		if d.Credential.APITokenRef != "" {
			steps = append(steps,
				Step{Title: "Create a read-only API token", Description: "Create a dedicated Okta service admin with a read-only administrator role and, signed in as that admin, create an API token (Security > API > Tokens). API tokens inherit the privileges of the admin that created them. AIPlex sends it as `Authorization: SSWS <token>` to " + d.OrgURL + " only."},
				secretStep(pc, "Store the API token", d.Credential.APITokenRef, "the API token", "OKTA_API_TOKEN"),
			)
		} else {
			steps = append(steps,
				Step{Title: "Create an OAuth service app", Description: "Create an API Services app (client ID " + d.Credential.ClientID + ") with public key / private key client authentication (private_key_jwt), register its public key" + func() string {
					if d.Credential.KeyID != "" {
						return " with key ID " + d.Credential.KeyID
					}
					return ""
				}() + ", grant the Okta API scope okta.users.read and assign it a read-only administrator role. DPoP-bound service apps are not supported by AIPlex."},
				secretStep(pc, "Store the private key", d.Credential.PrivateKeyRef, "the PEM private key (RSA, or EC P-256)", "OKTA_PRIVATE_KEY_PEM"),
			)
		}
		steps = append(steps, Step{Title: "Allow the Okta org origin", Description: "AIPlex calls " + d.OrgURL + "/api/v1/users/{id}/groups; the outbound endpoint policy must allow " + d.OrgURL + ". Group identifiers are the Okta group " + map[string]string{directory.GroupIDObjectID: "IDs", directory.GroupIDName: "names (profile.name)"}[d.GroupID] + "; write role bindings with the same identifiers."})
	}
	ttl := pc.DirectoryCacheTTL
	if ttl == "" {
		ttl = "at most five minutes"
	}
	steps = append(steps, Step{Title: "Force re-resolution after a group change", Description: "Resolved memberships are cached for " + ttl + " per server process. After removing a user from a group, an operator or webhook can invalidate the cached view on each server so the next request resolves it again.", Language: "bash", Content: "curl -sS -X POST -H \"Authorization: Bearer $ADMIN_TOKEN\" -H 'Content-Type: application/json' -d '{\"subject\": \"<" + p.SubjectClaim + " of the user>\"}' " + agentfederation.ShellQuote(api+"/api/v1/identity/user-providers/"+p.ID+"/membership/invalidate")})
	return steps
}

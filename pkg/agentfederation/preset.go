// Package agentfederation binds external workload identities (AWS, Azure, GCP,
// GitHub Actions, Kubernetes, SPIFFE or any OIDC issuer) to registered AIPlex
// agents. A preset turns a few operator inputs into an exact trust tuple
// (issuer, JWKS location, audience, subject and required claims); a binding
// persists that tuple tenant-scoped; the verification engine explains, check
// by check, whether a presented token satisfies a binding.
//
// Nothing here provisions cloud-side trust. Setup plans are rendered text for
// operator review.
package agentfederation

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentidentity"
)

// Preset identifiers. They are part of the public API contract.
const (
	PresetGCPServiceAccount = "gcp-service-account"
	PresetGCPAgentIdentity  = "gcp-agent-identity"
	PresetAWSSTSWebIdentity = "aws-sts-web-identity"
	PresetAWSEKSIRSA        = "aws-eks-irsa"
	PresetKubernetes        = "kubernetes"
	PresetAzureManagedID    = "azure-managed-identity"
	PresetGitHubActions     = "github-actions"
	PresetSPIFFEOIDC        = "spiffe-oidc"
	PresetOIDC              = "oidc"
)

// Field describes one operator input for a preset form.
type Field struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
	Example     string   `json:"example,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     string   `json:"default,omitempty"`
}

// Preset is one catalog entry. Derivation is server-side and deterministic.
type Preset struct {
	ID          string  `json:"id"`
	DisplayName string  `json:"display_name"`
	Cloud       string  `json:"cloud"`
	Description string  `json:"description"`
	Inputs      []Field `json:"inputs"`
	// SubjectShape documents the subject the binding will pin.
	SubjectShape string `json:"subject_shape"`
	// IssuerShape documents the issuer the binding will pin.
	IssuerShape string `json:"issuer_shape"`
	// AllowsRequiredClaims reports whether extra exact-match claims may be added.
	AllowsRequiredClaims bool `json:"allows_required_claims"`
	// Algorithms the verifier accepts for this preset.
	Algorithms []string `json:"algorithms"`
	// Notes are caveats an operator should read before binding.
	Notes  []string `json:"notes,omitempty"`
	derive func(in map[string]string, aud string) (Trust, error)
}

// Trust is the derived, exact trust tuple a binding pins.
type Trust struct {
	Issuer         string            `json:"issuer"`
	JWKSURL        string            `json:"jwks_url"`
	Audience       string            `json:"audience"`
	SubjectClaim   string            `json:"subject_claim"`
	Subject        string            `json:"subject"`
	RequiredClaims map[string]string `json:"required_claims,omitempty"`
}

var supportedAlgorithms = []string{"RS256", "ES256"}

const audienceDescription = "Exact audience (aud) the workload requests for AIPlex. Defaults to the AIPlex federation audience when the server has one configured; the stored binding always records it explicitly."

func audienceField(required bool, def string) Field {
	return Field{Name: "audience", Label: "Audience", Description: audienceDescription, Required: required, Example: "https://aiplex.example.com", Default: def}
}

// Catalog returns all presets in a stable order. defaultAudience is the server
// configured AIPlex audience ("" when none); it is surfaced as the default of
// the audience field for presets where the workload may choose its audience.
func Catalog(defaultAudience string) []Preset {
	presets := presetList()
	for i := range presets {
		for j := range presets[i].Inputs {
			if presets[i].Inputs[j].Name == "audience" && presets[i].ID != PresetAzureManagedID && defaultAudience != "" {
				presets[i].Inputs[j].Default = defaultAudience
			}
		}
	}
	return presets
}

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
// Unknown inputs are rejected. defaultAudience is used only when the preset
// permits a default and the operator left audience empty.
func Derive(presetID string, inputs map[string]string, extraClaims map[string]string, defaultAudience string) (Trust, error) {
	preset, ok := Lookup(presetID)
	if !ok {
		return Trust{}, fmt.Errorf("unknown identity preset %q", presetID)
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
		if len(field.Enum) > 0 && value != "" && !contains(field.Enum, value) {
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
	}
	audience := clean["audience"]
	if audience == "" && preset.ID != PresetAzureManagedID {
		audience = defaultAudience
	}
	for _, f := range preset.Inputs {
		if f.Required && clean[f.Name] == "" && !(f.Name == "audience" && audience != "") {
			return Trust{}, fmt.Errorf("input %q is required for preset %s", f.Name, presetID)
		}
	}
	if err := validAudience(audience); err != nil {
		return Trust{}, err
	}
	trust, err := preset.derive(clean, audience)
	if err != nil {
		return Trust{}, err
	}
	trust.Audience = audience
	if len(extraClaims) > 0 && !preset.AllowsRequiredClaims {
		return Trust{}, fmt.Errorf("preset %s does not accept additional required claims", presetID)
	}
	for name, value := range extraClaims {
		if trust.RequiredClaims == nil {
			trust.RequiredClaims = map[string]string{}
		}
		if existing, ok := trust.RequiredClaims[name]; ok && existing != value {
			return Trust{}, fmt.Errorf("required claim %q conflicts with the value derived by the preset", name)
		}
		trust.RequiredClaims[name] = value
	}
	if err := trust.Validate(); err != nil {
		return Trust{}, err
	}
	return trust, nil
}

var (
	claimName       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)
	reservedClaims  = map[string]bool{"iss": true, "sub": true, "aud": true, "exp": true, "nbf": true, "iat": true, "jti": true, "act": true, "azp": true, "cnf": true}
	digits          = regexp.MustCompile(`^[0-9]+$`)
	guid            = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	gcpSAEmail      = regexp.MustCompile(`^[a-z][a-z0-9-]{4,61}[a-z0-9]@[a-z0-9.-]+\.iam\.gserviceaccount\.com$|^[0-9]+-compute@developer\.gserviceaccount\.com$`)
	awsRoleARN      = regexp.MustCompile(`^arn:aws(-[a-z]+)*:iam::[0-9]{12}:role/[A-Za-z0-9+=,.@_/-]{1,512}$`)
	awsRegion       = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]{1,2}$`)
	eksOIDCID       = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)
	k8sName         = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
	k8sNamespace    = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	githubOwnerRepo = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)
	githubRef       = regexp.MustCompile(`^refs/(heads|tags)/[^\s*?\[\]~^:\\]{1,255}$`)
)

// Validate checks a derived trust tuple: https only, exact issuer, explicit
// audience, a concrete subject and no wildcards anywhere.
func (t Trust) Validate() error {
	if err := validHTTPSURL(t.Issuer, "issuer", true); err != nil {
		return err
	}
	if err := validHTTPSURL(t.JWKSURL, "JWKS URL", false); err != nil {
		return err
	}
	if err := validAudience(t.Audience); err != nil {
		return err
	}
	if t.SubjectClaim != "sub" && t.SubjectClaim != "oid" {
		return fmt.Errorf("subject claim must be sub or oid")
	}
	if err := exactValue("subject", t.Subject); err != nil {
		return err
	}
	if len(t.RequiredClaims) > 16 {
		return fmt.Errorf("at most 16 required claims")
	}
	for name, value := range t.RequiredClaims {
		if !claimName.MatchString(name) || reservedClaims[name] {
			return fmt.Errorf("required claim name %q is invalid or reserved (issuer, subject, audience and lifetime are checked separately)", name)
		}
		if err := exactValue("required claim "+name, value); err != nil {
			return err
		}
	}
	return nil
}

func exactValue(label, value string) error {
	if value == "" || len(value) > 512 || value != strings.TrimSpace(value) || strings.ContainsAny(value, "*\x00\r\n\t") {
		return fmt.Errorf("%s must be an exact, non-empty value without wildcards", label)
	}
	return nil
}

func validAudience(aud string) error {
	if err := exactValue("audience", aud); err != nil {
		return fmt.Errorf("an explicit audience is required (no wildcards)")
	}
	return nil
}

// validHTTPSURL requires an absolute https URL without credentials, query or
// fragment. Issuers additionally have no trailing slash normalisation: they are
// compared byte-for-byte with the token's iss claim.
//
// A JWKS URL (never an issuer) may also be plain http on a loopback host.
// Whether that host is reachable is the outbound endpoint policy's decision:
// it refuses loopback unless the operator opted in, which the API permits only
// in development mode, both when the URL is saved and when keys are fetched.
func validHTTPSURL(raw, label string, issuer bool) error {
	u, err := url.Parse(raw)
	scheme := err == nil && (u.Scheme == "https" || (!issuer && u.Scheme == "http" && loopbackHost(u.Hostname())))
	if err != nil || !scheme || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(raw, "*\\ \r\n\t") {
		return fmt.Errorf("%s must be an exact https URL without credentials, query, fragment or wildcards", label)
	}
	if issuer && strings.Contains(u.Path, "//") {
		return fmt.Errorf("issuer path is malformed")
	}
	return nil
}

func issuerOrigin(issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("issuer must be the bare https origin reported by the provider")
	}
	return nil
}

func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func presetList() []Preset {
	presets := []Preset{
		{
			ID: PresetGCPServiceAccount, DisplayName: "Google Cloud service account", Cloud: "gcp",
			Description:  "Google-signed ID token for a service account, from the metadata server (Compute Engine, Cloud Run, GKE Workload Identity) or service-account impersonation.",
			IssuerShape:  "https://accounts.google.com",
			SubjectShape: "service account numeric unique ID (sub)",
			Inputs: []Field{
				{Name: "service_account_unique_id", Label: "Service account unique ID", Description: "Numeric unique ID of the service account (gcloud iam service-accounts describe EMAIL --format='value(uniqueId)'). The email is mutable-by-recreation; the unique ID is not.", Required: true, Example: "107517467455664443765"},
				{Name: "service_account_email", Label: "Service account email", Description: "Optional. When set, the token must also carry this email claim, so the workload must request a full-format token (format=full / --include-email).", Example: "agent@my-project.iam.gserviceaccount.com"},
				audienceField(true, ""),
			},
			derive: func(in map[string]string, _ string) (Trust, error) {
				id := in["service_account_unique_id"]
				if !digits.MatchString(id) || len(id) < 15 || len(id) > 25 {
					return Trust{}, fmt.Errorf("service_account_unique_id must be the numeric unique ID")
				}
				t := Trust{Issuer: "https://accounts.google.com", JWKSURL: "https://www.googleapis.com/oauth2/v3/certs", SubjectClaim: "sub", Subject: id}
				if email := in["service_account_email"]; email != "" {
					if !gcpSAEmail.MatchString(email) {
						return Trust{}, fmt.Errorf("service_account_email must be a service account email")
					}
					t.RequiredClaims = map[string]string{"email": email}
				}
				return t, nil
			},
		},
		{
			ID: PresetGCPAgentIdentity, DisplayName: "Google Cloud Agent Identity", Cloud: "gcp",
			Description:  "Google STS agent-pool assertion with a SPIFFE subject, verified by the existing Agent Identity verifier (certificate binding enforced on live mTLS requests).",
			IssuerShape:  "https://sts.googleapis.com/v1/{organizations|projects}/{number}/locations/global/workloadIdentityPools/agents.global.{org|proj}-{number}.system.id.goog",
			SubjectShape: "spiffe://agents.global.{org|proj}-{number}.system.id.goog/resources/...",
			Inputs: []Field{
				{Name: "owner_type", Label: "Trust domain owner", Description: "Whether the agent trust domain belongs to an organization or a project.", Required: true, Enum: []string{"organizations", "projects"}},
				{Name: "owner_number", Label: "Organization or project number", Description: "Numeric organization or project number that owns the agent trust domain.", Required: true, Example: "123456789012"},
				{Name: "agent_spiffe_id", Label: "Agent SPIFFE ID", Description: "Exact SPIFFE ID of the agent principal (under /resources/...).", Required: true, Example: "spiffe://agents.global.proj-123456789012.system.id.goog/resources/aiplatform/projects/123456789012/locations/us-central1/reasoningEngines/42"},
				audienceField(true, ""),
			},
			Notes: []string{"If the same issuer is already configured as a static native_agent_identity source, that source authenticates the token first and this binding is used only for dry-run verification."},
			derive: func(in map[string]string, aud string) (Trust, error) {
				number := in["owner_number"]
				if !digits.MatchString(number) || len(number) > 20 {
					return Trust{}, fmt.Errorf("owner_number must be numeric")
				}
				short := "org"
				if in["owner_type"] == "projects" {
					short = "proj"
				}
				domain := "agents.global." + short + "-" + number + ".system.id.goog"
				cfg := agentidentity.Config{Issuer: "https://sts.googleapis.com/v1/" + in["owner_type"] + "/" + number + "/locations/global/workloadIdentityPools/" + domain, Audience: aud, TrustDomain: domain, AllowedSubjects: []string{in["agent_spiffe_id"]}, BindingMode: agentidentity.UnboundAllowed}
				jwks, err := cfg.JWKSURL()
				if err != nil {
					return Trust{}, fmt.Errorf("agent identity inputs rejected: %v", err)
				}
				return Trust{Issuer: cfg.Issuer, JWKSURL: jwks, SubjectClaim: "sub", Subject: in["agent_spiffe_id"]}, nil
			},
		},
		{
			ID: PresetAWSSTSWebIdentity, DisplayName: "AWS IAM role (outbound web identity federation)", Cloud: "aws",
			Description:  "JWT minted by AWS STS GetWebIdentityToken for an IAM role (EC2, ECS, Lambda, AgentCore or any role session). Requires outbound identity federation to be enabled on the account.",
			IssuerShape:  "account-specific issuer URL reported by AWS (operator input)",
			SubjectShape: "IAM role ARN (sub)",
			Inputs: []Field{
				{Name: "issuer", Label: "Account issuer URL", Description: "Exact account-specific issuer URL, copied from `aws iam get-outbound-web-identity-federation-info` or the IAM console (Account settings). AIPlex does not guess this value.", Required: true, Example: "https://abc123-def456-ghi789-jkl012.tokens.sts.global.api.aws"},
				{Name: "role_arn", Label: "IAM role ARN", Description: "ARN of the IAM role whose sessions represent this agent. It is the token subject.", Required: true, Example: "arn:aws:iam::123456789012:role/aiplex-agent"},
				{Name: "region", Label: "AWS region", Description: "Optional. Region used in the rendered token command (GetWebIdentityToken requires a regional STS endpoint).", Example: "us-east-1"},
				audienceField(true, ""),
			},
			Notes: []string{"GetWebIdentityToken signs with ES384 or RS256; AIPlex verifies RS256 and ES256 only, so request --signing-algorithm RS256.", "Account and organization claims are nested under https://sts.amazonaws.com/ and are not matched; the role ARN subject already pins the account."},
			derive: func(in map[string]string, _ string) (Trust, error) {
				if err := validHTTPSURL(in["issuer"], "issuer", true); err != nil {
					return Trust{}, err
				}
				if err := issuerOrigin(in["issuer"]); err != nil {
					return Trust{}, err
				}
				if !awsRoleARN.MatchString(in["role_arn"]) {
					return Trust{}, fmt.Errorf("role_arn must be an IAM role ARN")
				}
				if r := in["region"]; r != "" && !awsRegion.MatchString(r) {
					return Trust{}, fmt.Errorf("region must be an AWS region code")
				}
				issuer := strings.TrimSuffix(in["issuer"], "/")
				return Trust{Issuer: issuer, JWKSURL: issuer + "/.well-known/jwks.json", SubjectClaim: "sub", Subject: in["role_arn"]}, nil
			},
		},
		{
			ID: PresetAWSEKSIRSA, DisplayName: "Amazon EKS service account", Cloud: "aws",
			Description:  "Projected Kubernetes service account token from an EKS cluster's public OIDC issuer, requested with the AIPlex audience.",
			IssuerShape:  "https://oidc.eks.{region}.amazonaws.com/id/{oidc_id}",
			SubjectShape: "system:serviceaccount:{namespace}:{service_account}",
			Inputs: []Field{
				{Name: "region", Label: "Cluster region", Description: "AWS region of the EKS cluster.", Required: true, Example: "us-west-2"},
				{Name: "oidc_id", Label: "Cluster OIDC ID", Description: "Final path segment of the cluster OIDC issuer (aws eks describe-cluster --query cluster.identity.oidc.issuer).", Required: true, Example: "EXAMPLED539D4633E53DE1B71EXAMPLE"},
				{Name: "cluster_name", Label: "Cluster name", Description: "Optional. Used only in rendered commands."},
				{Name: "namespace", Label: "Namespace", Description: "Kubernetes namespace of the agent's service account.", Required: true, Example: "agents"},
				{Name: "service_account", Label: "Service account", Description: "Kubernetes service account name.", Required: true, Example: "research-agent"},
				audienceField(true, ""),
			},
			derive: func(in map[string]string, _ string) (Trust, error) {
				if !awsRegion.MatchString(in["region"]) || !eksOIDCID.MatchString(in["oidc_id"]) {
					return Trust{}, fmt.Errorf("region and oidc_id must be the EKS cluster's region code and OIDC ID")
				}
				if n := in["cluster_name"]; n != "" && !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`).MatchString(n) {
					return Trust{}, fmt.Errorf("cluster_name is invalid")
				}
				subject, err := k8sSubject(in)
				if err != nil {
					return Trust{}, err
				}
				issuer := "https://oidc.eks." + in["region"] + ".amazonaws.com/id/" + in["oidc_id"]
				return Trust{Issuer: issuer, JWKSURL: issuer + "/keys", SubjectClaim: "sub", Subject: subject}, nil
			},
		},
		{
			ID: PresetKubernetes, DisplayName: "Kubernetes service account", Cloud: "kubernetes",
			Description:  "Projected service account token from any Kubernetes cluster (GKE, AKS, on-prem) whose OIDC issuer and JWKS are reachable over HTTPS.",
			IssuerShape:  "cluster service-account issuer (operator input)",
			SubjectShape: "system:serviceaccount:{namespace}:{service_account}",
			Inputs: []Field{
				{Name: "issuer", Label: "Cluster issuer", Description: "Exact `issuer` from `kubectl get --raw /.well-known/openid-configuration`.", Required: true, Example: "https://container.googleapis.com/v1/projects/p/locations/us-central1/clusters/c"},
				{Name: "jwks_url", Label: "JWKS URL", Description: "Exact public HTTPS `jwks_uri` from the same discovery document. It must be reachable from AIPlex under the outbound endpoint policy.", Required: true},
				{Name: "namespace", Label: "Namespace", Description: "Kubernetes namespace of the agent's service account.", Required: true, Example: "agents"},
				{Name: "service_account", Label: "Service account", Description: "Kubernetes service account name.", Required: true, Example: "research-agent"},
				audienceField(true, ""),
			},
			derive: func(in map[string]string, _ string) (Trust, error) {
				subject, err := k8sSubject(in)
				if err != nil {
					return Trust{}, err
				}
				return Trust{Issuer: in["issuer"], JWKSURL: in["jwks_url"], SubjectClaim: "sub", Subject: subject}, nil
			},
		},
		{
			ID: PresetAzureManagedID, DisplayName: "Azure managed identity", Cloud: "azure",
			Description:  "Microsoft Entra access token issued to a system- or user-assigned managed identity for the AIPlex resource application.",
			IssuerShape:  "https://login.microsoftonline.com/{tenant}/v2.0 (or https://sts.windows.net/{tenant}/ for v1.0 tokens)",
			SubjectShape: "managed identity object (principal) ID (oid)",
			Inputs: []Field{
				{Name: "tenant_id", Label: "Entra tenant ID", Description: "Directory (tenant) GUID.", Required: true, Example: "72f988bf-86f1-41af-91ab-2d7cd011db47"},
				{Name: "object_id", Label: "Managed identity object ID", Description: "Object (principal) ID of the managed identity's service principal (az identity show --query principalId). Matched against the immutable oid claim.", Required: true},
				{Name: "managed_identity_client_id", Label: "Managed identity client ID", Description: "Optional. Client ID of a user-assigned identity; used only in the rendered token command when a host has several identities."},
				{Name: "audience", Label: "Audience", Description: "Exact aud of the token: for v2.0 tokens the client ID of the Entra application representing AIPlex; for v1.0 tokens the Application ID URI or client ID requested as the resource. No default.", Required: true, Example: "api://aiplex"},
				{Name: "token_version", Label: "Token version", Description: "Access token version the AIPlex application issues (its requestedAccessTokenVersion). Determines the issuer.", Enum: []string{"2.0", "1.0"}, Default: "2.0"},
			},
			Notes: []string{"The token version, and therefore the issuer, is set by the resource application's manifest, not by the caller."},
			derive: func(in map[string]string, _ string) (Trust, error) {
				tenant, oid := strings.ToLower(in["tenant_id"]), strings.ToLower(in["object_id"])
				if !guid.MatchString(tenant) || !guid.MatchString(oid) || tenant != in["tenant_id"] || oid != in["object_id"] {
					return Trust{}, fmt.Errorf("tenant_id and object_id must be lower-case GUIDs")
				}
				if c := in["managed_identity_client_id"]; c != "" && !guid.MatchString(c) {
					return Trust{}, fmt.Errorf("managed_identity_client_id must be a lower-case GUID")
				}
				issuer := "https://login.microsoftonline.com/" + tenant + "/v2.0"
				if in["token_version"] == "1.0" {
					issuer = "https://sts.windows.net/" + tenant + "/"
				}
				return Trust{Issuer: issuer, JWKSURL: "https://login.microsoftonline.com/" + tenant + "/discovery/v2.0/keys", SubjectClaim: "oid", Subject: oid, RequiredClaims: map[string]string{"tid": tenant}}, nil
			},
		},
		{
			ID: PresetGitHubActions, DisplayName: "GitHub Actions workflow", Cloud: "github",
			Description:  "GitHub Actions OIDC token for one repository, pinned to an environment or an exact branch/tag ref.",
			IssuerShape:  "https://token.actions.githubusercontent.com",
			SubjectShape: "repo:{owner}/{repo}:environment:{env} or repo:{owner}/{repo}:ref:{ref}",
			Inputs: []Field{
				{Name: "repository", Label: "Repository", Description: "owner/repo.", Required: true, Example: "octo-org/octo-repo"},
				{Name: "environment", Label: "Environment", Description: "Deployment environment the job references. Set exactly one of environment or ref.", Example: "production"},
				{Name: "ref", Label: "Git ref", Description: "Exact branch or tag ref (refs/heads/... or refs/tags/...) for jobs without an environment. Set exactly one of environment or ref.", Example: "refs/heads/main"},
				{Name: "repository_id", Label: "Repository ID", Description: "Numeric repository ID. Recommended: pinned as the repository_id claim so a deleted-and-recreated repository with the same name is rejected. Required for the immutable subject format."},
				{Name: "repository_owner_id", Label: "Owner ID", Description: "Numeric owner ID. Pinned as repository_owner_id when set. Required for the immutable subject format."},
				{Name: "subject_format", Label: "Subject format", Description: "`name` (repo:owner/repo:...) or `immutable` (repo:owner@ID/repo@ID:...), which GitHub uses for repositories that opted in or were created after it became the default.", Enum: []string{"name", "immutable"}, Default: "name"},
				audienceField(true, ""),
			},
			AllowsRequiredClaims: true,
			Notes:                []string{"pull_request subjects are not offered: they are shared by every pull request, including from forks.", "Pin job_workflow_ref as an additional required claim to restrict a reusable workflow."},
			derive: func(in map[string]string, _ string) (Trust, error) {
				repo := in["repository"]
				if !githubOwnerRepo.MatchString(repo) {
					return Trust{}, fmt.Errorf("repository must be owner/repo")
				}
				env, ref := in["environment"], in["ref"]
				if (env == "") == (ref == "") {
					return Trust{}, fmt.Errorf("set exactly one of environment or ref")
				}
				if ref != "" && !githubRef.MatchString(ref) {
					return Trust{}, fmt.Errorf("ref must be an exact refs/heads/... or refs/tags/... ref")
				}
				if env != "" && (len(env) > 255 || strings.ContainsAny(env, "*?")) {
					return Trust{}, fmt.Errorf("environment must be an exact environment name")
				}
				claims := map[string]string{}
				for _, name := range []string{"repository_id", "repository_owner_id"} {
					if v := in[name]; v != "" {
						if !digits.MatchString(v) {
							return Trust{}, fmt.Errorf("%s must be numeric", name)
						}
						claims[name] = v
					}
				}
				owner, name, _ := strings.Cut(repo, "/")
				prefix := "repo:" + repo
				if in["subject_format"] == "immutable" {
					if claims["repository_id"] == "" || claims["repository_owner_id"] == "" {
						return Trust{}, fmt.Errorf("the immutable subject format requires repository_id and repository_owner_id")
					}
					prefix = "repo:" + owner + "@" + claims["repository_owner_id"] + "/" + name + "@" + claims["repository_id"]
				}
				subject := prefix + ":ref:" + ref
				if env != "" {
					subject = prefix + ":environment:" + strings.ReplaceAll(env, ":", "%3A")
				}
				if len(claims) == 0 {
					claims = nil
				}
				return Trust{Issuer: "https://token.actions.githubusercontent.com", JWKSURL: "https://token.actions.githubusercontent.com/.well-known/jwks", SubjectClaim: "sub", Subject: subject, RequiredClaims: claims}, nil
			},
		},
		{
			ID: PresetSPIFFEOIDC, DisplayName: "SPIFFE JWT-SVID (OIDC discovery)", Cloud: "spiffe",
			Description:  "JWT-SVID from a SPIRE (or other SPIFFE) deployment that publishes its bundle through an OIDC discovery provider.",
			IssuerShape:  "OIDC discovery provider issuer (operator input)",
			SubjectShape: "spiffe://{trust-domain}/{path}",
			Inputs: []Field{
				{Name: "issuer", Label: "Issuer", Description: "Exact issuer of the OIDC discovery provider (the iss in JWT-SVIDs).", Required: true},
				{Name: "jwks_url", Label: "JWKS URL", Description: "Exact public HTTPS jwks_uri of the discovery provider.", Required: true},
				{Name: "spiffe_id", Label: "SPIFFE ID", Description: "Exact SPIFFE ID of the agent workload.", Required: true, Example: "spiffe://example.org/ns/agents/sa/research"},
				audienceField(true, ""),
			},
			AllowsRequiredClaims: true,
			derive: func(in map[string]string, _ string) (Trust, error) {
				u, err := url.Parse(in["spiffe_id"])
				if err != nil || u.Scheme != "spiffe" || u.Host == "" || u.Path == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" || strings.Contains(u.Path, "//") || strings.Contains(u.Path, "/../") || strings.Contains(u.Path, "/./") {
					return Trust{}, fmt.Errorf("spiffe_id must be an exact SPIFFE ID")
				}
				return Trust{Issuer: in["issuer"], JWKSURL: in["jwks_url"], SubjectClaim: "sub", Subject: in["spiffe_id"]}, nil
			},
		},
		{
			ID: PresetOIDC, DisplayName: "Generic OIDC workload", Cloud: "other",
			Description:  "Any OIDC issuer that signs RS256/ES256 JWTs with a public JWKS. Every value is explicit.",
			IssuerShape:  "explicit",
			SubjectShape: "explicit",
			Inputs: []Field{
				{Name: "issuer", Label: "Issuer", Description: "Exact iss value.", Required: true},
				{Name: "jwks_url", Label: "JWKS URL", Description: "Exact public HTTPS JWKS URL.", Required: true},
				{Name: "subject", Label: "Subject", Description: "Exact, immutable sub value of the workload.", Required: true},
				audienceField(true, ""),
			},
			AllowsRequiredClaims: true,
			derive: func(in map[string]string, _ string) (Trust, error) {
				return Trust{Issuer: in["issuer"], JWKSURL: in["jwks_url"], SubjectClaim: "sub", Subject: in["subject"]}, nil
			},
		},
	}
	for i := range presets {
		presets[i].Algorithms = append([]string(nil), supportedAlgorithms...)
	}
	return presets
}

func k8sSubject(in map[string]string) (string, error) {
	if !k8sNamespace.MatchString(in["namespace"]) || !k8sName.MatchString(in["service_account"]) {
		return "", fmt.Errorf("namespace and service_account must be Kubernetes names")
	}
	return "system:serviceaccount:" + in["namespace"] + ":" + in["service_account"], nil
}

// loopbackHost reports a literal loopback address or localhost.
func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

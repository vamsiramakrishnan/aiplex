package agentfederation

import (
	"fmt"
	"net/url"
	"strings"
)

// Step is one reviewable setup step. Plans are text; nothing is executed.
type Step struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	// Language of Content: bash, yaml, json, or empty for prose-only steps.
	Language string `json:"language,omitempty"`
	Content  string `json:"content,omitempty"`
}

// Plan is the rendered cloud-side setup for one binding.
type Plan struct {
	Binding      BindingRef `json:"binding"`
	Cloud        string     `json:"cloud"`
	Issuer       string     `json:"issuer"`
	Audience     string     `json:"audience"`
	Subject      string     `json:"subject"`
	Steps        []Step     `json:"steps"`
	TokenSnippet Step       `json:"token_snippet"`
	AIPlex       []Step     `json:"aiplex_steps"`
	Notes        []string   `json:"notes,omitempty"`
}

// PlanContext supplies server facts used in rendered commands.
type PlanContext struct {
	// APIBaseURL is the public AIPlex origin (may be empty).
	APIBaseURL string
	// PrincipalID / ActorID are the canonical identities the binding yields.
	PrincipalID string
	ActorID     string
	// FederationEnabled reports whether live authentication is switched on.
	FederationEnabled bool
}

// shellQuote single-quotes a value for POSIX shells.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

func placeholder(v, name string) string {
	if v == "" {
		return name
	}
	return v
}

// RenderPlan renders setup steps for a binding. Only commands whose syntax is
// documented by the provider are emitted; everything else is prose.
func RenderPlan(b Binding, pc PlanContext) (Plan, error) {
	preset, ok := Lookup(b.Preset)
	if !ok {
		return Plan{}, fmt.Errorf("unknown identity preset %q", b.Preset)
	}
	p := Plan{Binding: BindingRef{ClientID: b.ClientID, ID: b.ID, Preset: b.Preset}, Cloud: preset.Cloud, Issuer: b.Issuer, Audience: b.Audience, Subject: b.Subject, Notes: append([]string(nil), preset.Notes...)}
	aud := b.Audience
	qAud := url.QueryEscape(aud)
	api := strings.TrimSuffix(pc.APIBaseURL, "/")
	if api == "" {
		api = "https://AIPLEX_HOST"
	}
	callAIPlex := "curl -sS -H \"Authorization: Bearer $TOKEN\" " + shellQuote(api+"/api/v1/iam/whoami")
	in := b.Inputs

	switch b.Preset {
	case PresetGCPServiceAccount:
		email := placeholder(in["service_account_email"], "SA_EMAIL")
		p.Steps = []Step{
			{Title: "Confirm the service account unique ID", Description: "The binding pins the numeric unique ID (" + b.Subject + "), not the email.", Language: "bash", Content: "gcloud iam service-accounts describe " + shellQuote(email) + " --format='value(uniqueId)'"},
			{Title: "Run the agent as the service account", Description: "Attach the service account to the Compute Engine VM, Cloud Run service or GKE workload (via Workload Identity) that runs the agent. No key file is needed: the metadata server issues ID tokens for the attached account."},
			{Title: "Or allow impersonation (local / CI callers)", Description: "Only if a different principal mints tokens by impersonation: grant it the OpenID token creator role on the service account. Replace MEMBER with that principal.", Language: "bash", Content: "gcloud iam service-accounts add-iam-policy-binding " + shellQuote(email) + " --member=MEMBER --role=roles/iam.serviceAccountOpenIdTokenCreator"},
		}
		format := ""
		if in["service_account_email"] != "" {
			format = "&format=full"
			p.Notes = append(p.Notes, "The binding requires the email claim: request full-format tokens (format=full on the metadata server, --include-email with gcloud).")
		}
		p.TokenSnippet = Step{Title: "Acquire a token", Description: "From inside Google Cloud (metadata server). With impersonation instead: gcloud auth print-identity-token --impersonate-service-account=" + email + " --audiences=" + shellQuote(aud) + " --include-email", Language: "bash", Content: "TOKEN=$(curl -sS -H 'Metadata-Flavor: Google' " + shellQuote("http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity?audience="+qAud+format) + ")\n" + callAIPlex}
	case PresetGCPAgentIdentity:
		p.Steps = []Step{
			{Title: "Provision the agent identity", Description: "Deploy the agent on a Google Cloud runtime that issues Agent Identity credentials in trust domain " + spiffeDomain(b.Subject) + " and confirm its SPIFFE ID is exactly " + b.Subject + "."},
			{Title: "Request the AIPlex audience", Description: "Configure the agent runtime to request assertions with audience " + aud + "."},
			{Title: "Certificate binding", Description: "If assertions are certificate-bound (cnf.x5t#S256), the agent must call AIPlex over mutual TLS with the bound certificate, and AIPlex must terminate TLS directly with a client CA configured."},
		}
		p.TokenSnippet = Step{Title: "Acquire a token", Description: "Obtain the Agent Identity assertion from the runtime's credential API for audience " + aud + " and send it as a bearer token (with the bound client certificate when certificate-bound)."}
	case PresetAWSSTSWebIdentity:
		region := placeholder(in["region"], "REGION")
		policy := `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "sts:GetWebIdentityToken",
      "Resource": "*",
      "Condition": {
        "ForAllValues:StringEquals": { "sts:IdentityTokenAudience": ` + jsonString(aud) + ` },
        "Null": { "sts:IdentityTokenAudience": "false" },
        "NumericLessThanEquals": { "sts:DurationSeconds": 900 }
      }
    }
  ]
}`
		p.Steps = []Step{
			{Title: "Enable outbound identity federation (once per account)", Language: "bash", Content: "aws iam enable-outbound-web-identity-federation"},
			{Title: "Confirm the account issuer URL", Description: "The binding pins issuer " + b.Issuer + "; it must equal the issuer reported here.", Language: "bash", Content: "aws iam get-outbound-web-identity-federation-info"},
			{Title: "Allow the role to mint tokens for AIPlex only", Description: "Attach this policy to " + b.Subject + ".", Language: "json", Content: policy},
		}
		p.TokenSnippet = Step{Title: "Acquire a token", Description: "Run as the role. GetWebIdentityToken needs a regional STS endpoint; RS256 is required because AIPlex does not verify ES384.", Language: "bash", Content: "TOKEN=$(aws sts get-web-identity-token --region " + region + " --endpoint-url https://sts." + region + ".amazonaws.com --audience " + shellQuote(aud) + " --signing-algorithm RS256 --duration-seconds 300 --query WebIdentityToken --output text)\n" + callAIPlex}
	case PresetAWSEKSIRSA, PresetKubernetes:
		if b.Preset == PresetAWSEKSIRSA {
			cluster := placeholder(in["cluster_name"], "CLUSTER")
			p.Steps = append(p.Steps, Step{Title: "Confirm the cluster OIDC issuer", Description: "Must print exactly " + b.Issuer + ".", Language: "bash", Content: "aws eks describe-cluster --name " + shellQuote(cluster) + " --region " + in["region"] + " --query cluster.identity.oidc.issuer --output text"})
			p.Notes = append(p.Notes, "This token is separate from the IRSA token for AWS APIs (audience sts.amazonaws.com); project a second token with the AIPlex audience.")
		} else {
			p.Steps = append(p.Steps, Step{Title: "Confirm the cluster issuer and JWKS", Description: "issuer must be exactly " + b.Issuer + " and jwks_uri exactly " + b.JWKSURL + ". The JWKS must be publicly reachable over HTTPS and allowed by the AIPlex outbound endpoint policy.", Language: "bash", Content: "kubectl get --raw /.well-known/openid-configuration"})
		}
		ns, sa := in["namespace"], in["service_account"]
		manifest := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: ` + sa + `
  namespace: ` + ns + `
---
# Add to the agent's Pod template:
spec:
  serviceAccountName: ` + sa + `
  containers:
    - name: agent
      volumeMounts:
        - name: aiplex-token
          mountPath: /var/run/secrets/aiplex
          readOnly: true
  volumes:
    - name: aiplex-token
      projected:
        sources:
          - serviceAccountToken:
              path: token
              audience: ` + yamlString(aud) + `
              expirationSeconds: 3600`
		p.Steps = append(p.Steps, Step{Title: "Project a service account token with the AIPlex audience", Description: "The kubelet refreshes the file before expiry; read it on every call.", Language: "yaml", Content: manifest})
		p.TokenSnippet = Step{Title: "Acquire a token", Language: "bash", Content: "TOKEN=$(cat /var/run/secrets/aiplex/token)\n" + callAIPlex}
	case PresetAzureManagedID:
		p.Steps = []Step{
			{Title: "Represent AIPlex as an Entra application", Description: "Register (or reuse) an application for AIPlex in tenant " + in["tenant_id"] + ". Its Application ID URI or client ID must equal the binding audience " + aud + ". Set the manifest's requestedAccessTokenVersion to match token version " + in["token_version"] + " (it determines the issuer " + b.Issuer + ")."},
			{Title: "Confirm the managed identity object ID", Description: "For a user-assigned identity (replace NAME and RG); for a system-assigned VM identity use `az vm identity show`. The output must be " + b.Subject + ".", Language: "bash", Content: "az identity show --name NAME --resource-group RG --query principalId -o tsv"},
		}
		query := "api-version=2018-02-01&resource=" + qAud
		if c := in["managed_identity_client_id"]; c != "" {
			query += "&client_id=" + c
		}
		p.TokenSnippet = Step{Title: "Acquire a token", Description: "From the Azure host (Instance Metadata Service). Alternatively: az login --identity, then az account get-access-token --resource " + shellQuote(aud) + " --query accessToken -o tsv", Language: "bash", Content: "TOKEN=$(curl -sS -H Metadata:true " + shellQuote("http://169.254.169.254/metadata/identity/oauth2/token?"+query) + " | python3 -c 'import sys,json; print(json.load(sys.stdin)[\"access_token\"])')\n" + callAIPlex}
	case PresetGitHubActions:
		env := in["environment"]
		jobEnv := ""
		if env != "" {
			jobEnv = "\n    environment: " + yamlString(env)
		}
		on := "workflow_dispatch:"
		if strings.HasPrefix(in["ref"], "refs/heads/") {
			on = "push:\n    branches: [" + yamlString(strings.TrimPrefix(in["ref"], "refs/heads/")) + "]"
		}
		workflow := `on:
  ` + on + `
permissions:
  id-token: write
  contents: read
jobs:
  call-aiplex:
    runs-on: ubuntu-latest` + jobEnv + `
    steps:
      - name: Call AIPlex as agent ` + b.ClientID + `
        run: |
          TOKEN=$(curl -sS -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=` + qAud + `" | jq -r .value)
          echo "::add-mask::$TOKEN"
          ` + callAIPlex
		p.Steps = []Step{{Title: "Workflow for " + in["repository"], Description: "The job must produce subject " + b.Subject + ". Jobs that reference an environment get an environment subject; others get the ref subject.", Language: "yaml", Content: workflow}}
		p.TokenSnippet = Step{Title: "Acquire a token", Description: "Inside a job with id-token: write.", Language: "bash", Content: "TOKEN=$(curl -sS -H \"Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN\" \"$ACTIONS_ID_TOKEN_REQUEST_URL&audience=" + qAud + "\" | jq -r .value)\n" + callAIPlex}
	case PresetSPIFFEOIDC:
		p.Steps = []Step{
			{Title: "Publish the trust bundle", Description: "Run an OIDC discovery provider for the trust domain so that issuer " + b.Issuer + " serves its keys at " + b.JWKSURL + " over public HTTPS, allowed by the AIPlex outbound endpoint policy."},
			{Title: "Register the workload", Description: "Ensure the agent workload is issued SPIFFE ID " + b.Subject + "."},
		}
		p.TokenSnippet = Step{Title: "Acquire a token", Description: "Fetch a JWT-SVID for audience " + aud + " from the SPIFFE Workload API (for example with the go-spiffe workloadapi client) and send it as a bearer token."}
	default:
		p.Steps = []Step{{Title: "Configure the issuer", Description: "Ensure " + b.Issuer + " signs RS256 or ES256 tokens with subject " + b.Subject + " and audience " + aud + ", with keys at " + b.JWKSURL + " reachable under the AIPlex outbound endpoint policy."}}
		p.TokenSnippet = Step{Title: "Acquire a token", Description: "Obtain a token for audience " + aud + " from your issuer and send it as `Authorization: Bearer <token>`."}
	}

	p.AIPlex = []Step{
		{Title: "Dry-run a token", Description: "Paste a freshly acquired token. The response lists every check; the token is never stored or echoed.", Language: "bash", Content: "curl -sS -X POST -H \"Authorization: Bearer $ADMIN_TOKEN\" -H 'Content-Type: application/json' " + shellQuote(api+"/api/v1/agents/identities/verify") + " -d \"{\\\"token\\\":\\\"$TOKEN\\\",\\\"client_id\\\":\\\"" + b.ClientID + "\\\",\\\"binding_id\\\":\\\"" + b.ID + "\\\"}\""},
	}
	if pc.PrincipalID != "" {
		p.AIPlex = append(p.AIPlex, Step{Title: "Grant permissions", Description: "The workload authenticates as principal " + pc.PrincipalID + " acting for agent " + b.ClientID + " (actor " + pc.ActorID + "). It receives only scopes granted to that principal; grant them explicitly.", Language: "bash", Content: "curl -sS -X PUT -H \"Authorization: Bearer $ADMIN_TOKEN\" -H 'Content-Type: application/json' " + shellQuote(api+"/auth/users/"+pc.PrincipalID+"/scopes") + " -d '{\"scopes\":[\"mcp:tools:EXAMPLE\"]}'"})
	}
	if !pc.FederationEnabled {
		p.AIPlex = append(p.AIPlex, Step{Title: "Enable live federation", Description: "Live authentication with bindings is off on this server. Set AIPLEX_AGENT_FEDERATION=enabled (with AIPLEX_TENANT_ID) and restart; dry-run verification works either way."})
	}
	return p, nil
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func yamlString(s string) string { return jsonString(s) }

func spiffeDomain(id string) string {
	u, err := url.Parse(id)
	if err != nil {
		return ""
	}
	return u.Host
}

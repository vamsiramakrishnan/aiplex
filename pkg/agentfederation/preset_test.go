package agentfederation

import (
	"slices"
	"strings"
	"testing"
)

func TestCatalogListsPlanPresets(t *testing.T) {
	want := []string{PresetGCPServiceAccount, PresetGCPAgentIdentity, PresetAWSSTSWebIdentity, PresetAWSEKSIRSA, PresetKubernetes, PresetAzureManagedID, PresetGitHubActions, PresetSPIFFEOIDC, PresetOIDC}
	catalog := Catalog("https://aiplex.example.com")
	if len(catalog) != len(want) {
		t.Fatalf("catalog has %d presets, want %d", len(catalog), len(want))
	}
	for i, p := range catalog {
		if p.ID != want[i] || p.DisplayName == "" || p.Cloud == "" || len(p.Inputs) == 0 || len(p.Algorithms) == 0 {
			t.Fatalf("preset %d incomplete: %+v", i, p)
		}
		for _, f := range p.Inputs {
			if f.Description == "" || f.Label == "" {
				t.Fatalf("preset %s field %s lacks description", p.ID, f.Name)
			}
			if f.Name == "audience" {
				if p.ID == PresetAzureManagedID && f.Default != "" {
					t.Fatalf("azure audience must have no default")
				}
				if p.ID != PresetAzureManagedID && f.Default != "https://aiplex.example.com" {
					t.Fatalf("preset %s audience default not surfaced", p.ID)
				}
			}
		}
	}
}

func TestDerivePresets(t *testing.T) {
	const aud = "https://aiplex.example.com"
	tests := []struct {
		name    string
		preset  string
		inputs  map[string]string
		extra   map[string]string
		defAud  string
		want    Trust
		wantErr string
	}{
		{name: "gcp service account", preset: PresetGCPServiceAccount, inputs: map[string]string{"service_account_unique_id": "107517467455664443765", "service_account_email": "agent-one@proj-1.iam.gserviceaccount.com", "audience": aud},
			want: Trust{Issuer: "https://accounts.google.com", JWKSURL: "https://www.googleapis.com/oauth2/v3/certs", Audience: aud, SubjectClaim: "sub", Subject: "107517467455664443765", RequiredClaims: map[string]string{"email": "agent-one@proj-1.iam.gserviceaccount.com"}}},
		{name: "gcp default audience", preset: PresetGCPServiceAccount, inputs: map[string]string{"service_account_unique_id": "107517467455664443765"}, defAud: aud,
			want: Trust{Issuer: "https://accounts.google.com", JWKSURL: "https://www.googleapis.com/oauth2/v3/certs", Audience: aud, SubjectClaim: "sub", Subject: "107517467455664443765"}},
		{name: "gcp missing audience", preset: PresetGCPServiceAccount, inputs: map[string]string{"service_account_unique_id": "107517467455664443765"}, wantErr: "audience"},
		{name: "gcp email as subject rejected", preset: PresetGCPServiceAccount, inputs: map[string]string{"service_account_unique_id": "agent@proj.iam.gserviceaccount.com", "audience": aud}, wantErr: "numeric"},
		{name: "gcp agent identity", preset: PresetGCPAgentIdentity, inputs: map[string]string{"owner_type": "projects", "owner_number": "123456789012", "agent_spiffe_id": "spiffe://agents.global.proj-123456789012.system.id.goog/resources/aiplatform/agent-1", "audience": aud},
			want: Trust{Issuer: "https://sts.googleapis.com/v1/projects/123456789012/locations/global/workloadIdentityPools/agents.global.proj-123456789012.system.id.goog", JWKSURL: "https://sts.googleapis.com/v1/projects/123456789012/locations/global/workloadIdentityPools/agents.global.proj-123456789012.system.id.goog/openid/jwks", Audience: aud, SubjectClaim: "sub", Subject: "spiffe://agents.global.proj-123456789012.system.id.goog/resources/aiplatform/agent-1"}},
		{name: "gcp agent identity foreign trust domain", preset: PresetGCPAgentIdentity, inputs: map[string]string{"owner_type": "projects", "owner_number": "123456789012", "agent_spiffe_id": "spiffe://agents.global.proj-999.system.id.goog/resources/x", "audience": aud}, wantErr: "agent identity inputs rejected"},
		{name: "aws sts", preset: PresetAWSSTSWebIdentity, inputs: map[string]string{"issuer": "https://abc123-def456.tokens.sts.global.api.aws", "role_arn": "arn:aws:iam::123456789012:role/aiplex-agent", "region": "us-east-1", "audience": aud},
			want: Trust{Issuer: "https://abc123-def456.tokens.sts.global.api.aws", JWKSURL: "https://abc123-def456.tokens.sts.global.api.aws/.well-known/jwks.json", Audience: aud, SubjectClaim: "sub", Subject: "arn:aws:iam::123456789012:role/aiplex-agent"}},
		{name: "aws sts issuer required", preset: PresetAWSSTSWebIdentity, inputs: map[string]string{"role_arn": "arn:aws:iam::123456789012:role/a", "audience": aud}, wantErr: `"issuer" is required`},
		{name: "aws sts http issuer", preset: PresetAWSSTSWebIdentity, inputs: map[string]string{"issuer": "http://abc.tokens.sts.global.api.aws", "role_arn": "arn:aws:iam::123456789012:role/a", "audience": aud}, wantErr: "https"},
		{name: "aws sts issuer with path", preset: PresetAWSSTSWebIdentity, inputs: map[string]string{"issuer": "https://abc.tokens.sts.global.api.aws/x", "role_arn": "arn:aws:iam::123456789012:role/a", "audience": aud}, wantErr: "bare https origin"},
		{name: "aws sts wildcard role", preset: PresetAWSSTSWebIdentity, inputs: map[string]string{"issuer": "https://abc.tokens.sts.global.api.aws", "role_arn": "arn:aws:iam::123456789012:role/*", "audience": aud}, wantErr: "role ARN"},
		{name: "aws sts user arn", preset: PresetAWSSTSWebIdentity, inputs: map[string]string{"issuer": "https://abc.tokens.sts.global.api.aws", "role_arn": "arn:aws:iam::123456789012:user/bob", "audience": aud}, wantErr: "role ARN"},
		{name: "eks", preset: PresetAWSEKSIRSA, inputs: map[string]string{"region": "us-west-2", "oidc_id": "EXAMPLED539D4633E53DE1B71EXAMPLE", "namespace": "agents", "service_account": "research", "audience": aud},
			want: Trust{Issuer: "https://oidc.eks.us-west-2.amazonaws.com/id/EXAMPLED539D4633E53DE1B71EXAMPLE", JWKSURL: "https://oidc.eks.us-west-2.amazonaws.com/id/EXAMPLED539D4633E53DE1B71EXAMPLE/keys", Audience: aud, SubjectClaim: "sub", Subject: "system:serviceaccount:agents:research"}},
		{name: "eks bad namespace", preset: PresetAWSEKSIRSA, inputs: map[string]string{"region": "us-west-2", "oidc_id": "EXAMPLED539D4633E53DE1B71EXAMPLE", "namespace": "*", "service_account": "research", "audience": aud}, wantErr: "Kubernetes names"},
		{name: "kubernetes", preset: PresetKubernetes, inputs: map[string]string{"issuer": "https://k8s.example.com", "jwks_url": "https://k8s.example.com/openid/v1/jwks", "namespace": "agents", "service_account": "summarizer", "audience": aud},
			want: Trust{Issuer: "https://k8s.example.com", JWKSURL: "https://k8s.example.com/openid/v1/jwks", Audience: aud, SubjectClaim: "sub", Subject: "system:serviceaccount:agents:summarizer"}},
		{name: "kubernetes jwks with query", preset: PresetKubernetes, inputs: map[string]string{"issuer": "https://k8s.example.com", "jwks_url": "https://k8s.example.com/jwks?x=1", "namespace": "agents", "service_account": "s", "audience": aud}, wantErr: "JWKS URL"},
		{name: "azure v2", preset: PresetAzureManagedID, inputs: map[string]string{"tenant_id": "72f988bf-86f1-41af-91ab-2d7cd011db47", "object_id": "0b8f2f4a-1111-4222-8333-944455556666", "audience": "api://aiplex"},
			want: Trust{Issuer: "https://login.microsoftonline.com/72f988bf-86f1-41af-91ab-2d7cd011db47/v2.0", JWKSURL: "https://login.microsoftonline.com/72f988bf-86f1-41af-91ab-2d7cd011db47/discovery/v2.0/keys", Audience: "api://aiplex", SubjectClaim: "oid", Subject: "0b8f2f4a-1111-4222-8333-944455556666", RequiredClaims: map[string]string{"tid": "72f988bf-86f1-41af-91ab-2d7cd011db47"}}},
		{name: "azure v1", preset: PresetAzureManagedID, inputs: map[string]string{"tenant_id": "72f988bf-86f1-41af-91ab-2d7cd011db47", "object_id": "0b8f2f4a-1111-4222-8333-944455556666", "audience": "api://aiplex", "token_version": "1.0"},
			want: Trust{Issuer: "https://sts.windows.net/72f988bf-86f1-41af-91ab-2d7cd011db47/", JWKSURL: "https://login.microsoftonline.com/72f988bf-86f1-41af-91ab-2d7cd011db47/discovery/v2.0/keys", Audience: "api://aiplex", SubjectClaim: "oid", Subject: "0b8f2f4a-1111-4222-8333-944455556666", RequiredClaims: map[string]string{"tid": "72f988bf-86f1-41af-91ab-2d7cd011db47"}}},
		{name: "azure audience never defaulted", preset: PresetAzureManagedID, inputs: map[string]string{"tenant_id": "72f988bf-86f1-41af-91ab-2d7cd011db47", "object_id": "0b8f2f4a-1111-4222-8333-944455556666"}, defAud: aud, wantErr: "audience"},
		{name: "azure upper-case guid", preset: PresetAzureManagedID, inputs: map[string]string{"tenant_id": "72F988BF-86F1-41AF-91AB-2D7CD011DB47", "object_id": "0b8f2f4a-1111-4222-8333-944455556666", "audience": "api://aiplex"}, wantErr: "lower-case"},
		{name: "azure bad token version", preset: PresetAzureManagedID, inputs: map[string]string{"tenant_id": "72f988bf-86f1-41af-91ab-2d7cd011db47", "object_id": "0b8f2f4a-1111-4222-8333-944455556666", "audience": "api://aiplex", "token_version": "3"}, wantErr: "one of"},
		{name: "github environment", preset: PresetGitHubActions, inputs: map[string]string{"repository": "octo-org/octo-repo", "environment": "prod:eu", "repository_id": "456789", "audience": aud},
			want: Trust{Issuer: "https://token.actions.githubusercontent.com", JWKSURL: "https://token.actions.githubusercontent.com/.well-known/jwks", Audience: aud, SubjectClaim: "sub", Subject: "repo:octo-org/octo-repo:environment:prod%3Aeu", RequiredClaims: map[string]string{"repository_id": "456789"}}},
		{name: "github ref with extra claim", preset: PresetGitHubActions, inputs: map[string]string{"repository": "octo-org/octo-repo", "ref": "refs/heads/main", "audience": aud}, extra: map[string]string{"job_workflow_ref": "octo-org/wf/.github/workflows/deploy.yml@refs/heads/main"},
			want: Trust{Issuer: "https://token.actions.githubusercontent.com", JWKSURL: "https://token.actions.githubusercontent.com/.well-known/jwks", Audience: aud, SubjectClaim: "sub", Subject: "repo:octo-org/octo-repo:ref:refs/heads/main", RequiredClaims: map[string]string{"job_workflow_ref": "octo-org/wf/.github/workflows/deploy.yml@refs/heads/main"}}},
		{name: "github immutable", preset: PresetGitHubActions, inputs: map[string]string{"repository": "octo-org/octo-repo", "ref": "refs/heads/main", "repository_id": "456789", "repository_owner_id": "123456", "subject_format": "immutable", "audience": aud},
			want: Trust{Issuer: "https://token.actions.githubusercontent.com", JWKSURL: "https://token.actions.githubusercontent.com/.well-known/jwks", Audience: aud, SubjectClaim: "sub", Subject: "repo:octo-org@123456/octo-repo@456789:ref:refs/heads/main", RequiredClaims: map[string]string{"repository_id": "456789", "repository_owner_id": "123456"}}},
		{name: "github both env and ref", preset: PresetGitHubActions, inputs: map[string]string{"repository": "o/r", "environment": "e", "ref": "refs/heads/main", "audience": aud}, wantErr: "exactly one"},
		{name: "github wildcard ref", preset: PresetGitHubActions, inputs: map[string]string{"repository": "o/r", "ref": "refs/heads/*", "audience": aud}, wantErr: "exact"},
		{name: "github conflicting extra claim", preset: PresetGitHubActions, inputs: map[string]string{"repository": "o/r", "ref": "refs/heads/main", "repository_id": "1", "audience": aud}, extra: map[string]string{"repository_id": "2"}, wantErr: "conflicts"},
		{name: "github reserved extra claim", preset: PresetGitHubActions, inputs: map[string]string{"repository": "o/r", "ref": "refs/heads/main", "audience": aud}, extra: map[string]string{"sub": "x"}, wantErr: "reserved"},
		{name: "spiffe", preset: PresetSPIFFEOIDC, inputs: map[string]string{"issuer": "https://oidc.example.org", "jwks_url": "https://oidc.example.org/keys", "spiffe_id": "spiffe://example.org/ns/agents/sa/research", "audience": aud},
			want: Trust{Issuer: "https://oidc.example.org", JWKSURL: "https://oidc.example.org/keys", Audience: aud, SubjectClaim: "sub", Subject: "spiffe://example.org/ns/agents/sa/research"}},
		{name: "spiffe not a spiffe id", preset: PresetSPIFFEOIDC, inputs: map[string]string{"issuer": "https://oidc.example.org", "jwks_url": "https://oidc.example.org/keys", "spiffe_id": "https://example.org/x", "audience": aud}, wantErr: "SPIFFE"},
		{name: "oidc wildcard subject", preset: PresetOIDC, inputs: map[string]string{"issuer": "https://idp.example.com", "jwks_url": "https://idp.example.com/jwks", "subject": "agent-*", "audience": aud}, wantErr: "wildcards"},
		{name: "oidc wildcard audience", preset: PresetOIDC, inputs: map[string]string{"issuer": "https://idp.example.com", "jwks_url": "https://idp.example.com/jwks", "subject": "agent-1", "audience": "*"}, wantErr: "audience"},
		{name: "oidc credentials in issuer", preset: PresetOIDC, inputs: map[string]string{"issuer": "https://u:p@idp.example.com", "jwks_url": "https://idp.example.com/jwks", "subject": "agent-1", "audience": aud}, wantErr: "https URL"},
		{name: "unknown input", preset: PresetOIDC, inputs: map[string]string{"issuer": "https://idp.example.com", "jwks_url": "https://idp.example.com/jwks", "subject": "a", "audience": aud, "subject_pattern": "x"}, wantErr: "not defined"},
		{name: "surrounding whitespace", preset: PresetOIDC, inputs: map[string]string{"issuer": "https://idp.example.com", "jwks_url": "https://idp.example.com/jwks", "subject": " a", "audience": aud}, wantErr: "whitespace"},
		{name: "extra claims on fixed preset", preset: PresetAzureManagedID, inputs: map[string]string{"tenant_id": "72f988bf-86f1-41af-91ab-2d7cd011db47", "object_id": "0b8f2f4a-1111-4222-8333-944455556666", "audience": "api://aiplex"}, extra: map[string]string{"x": "y"}, wantErr: "does not accept"},
		{name: "unknown preset", preset: "aws-*", inputs: map[string]string{}, wantErr: "unknown identity preset"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Derive(tc.preset, tc.inputs, tc.extra, tc.defAud)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Issuer != tc.want.Issuer || got.JWKSURL != tc.want.JWKSURL || got.Audience != tc.want.Audience || got.SubjectClaim != tc.want.SubjectClaim || got.Subject != tc.want.Subject || len(got.RequiredClaims) != len(tc.want.RequiredClaims) {
				t.Fatalf("got %+v\nwant %+v", got, tc.want)
			}
			for k, v := range tc.want.RequiredClaims {
				if got.RequiredClaims[k] != v {
					t.Fatalf("required claim %s = %q, want %q", k, got.RequiredClaims[k], v)
				}
			}
		})
	}
}

func TestRenderPlanForEveryPreset(t *testing.T) {
	const aud = "https://aiplex.example.com"
	inputs := map[string]map[string]string{
		PresetGCPServiceAccount: {"service_account_unique_id": "107517467455664443765", "service_account_email": "agent-one@proj-1.iam.gserviceaccount.com"},
		PresetGCPAgentIdentity:  {"owner_type": "organizations", "owner_number": "42", "agent_spiffe_id": "spiffe://agents.global.org-42.system.id.goog/resources/a"},
		PresetAWSSTSWebIdentity: {"issuer": "https://abc.tokens.sts.global.api.aws", "role_arn": "arn:aws:iam::123456789012:role/agent", "region": "eu-west-1"},
		PresetAWSEKSIRSA:        {"region": "us-west-2", "oidc_id": "EXAMPLED539D4633E53DE1B71EXAMPLE", "cluster_name": "prod", "namespace": "agents", "service_account": "research"},
		PresetKubernetes:        {"issuer": "https://k8s.example.com", "jwks_url": "https://k8s.example.com/jwks", "namespace": "agents", "service_account": "research"},
		PresetAzureManagedID:    {"tenant_id": "72f988bf-86f1-41af-91ab-2d7cd011db47", "object_id": "0b8f2f4a-1111-4222-8333-944455556666", "audience": "api://aiplex"},
		PresetGitHubActions:     {"repository": "octo-org/octo-repo", "environment": "production"},
		PresetSPIFFEOIDC:        {"issuer": "https://oidc.example.org", "jwks_url": "https://oidc.example.org/keys", "spiffe_id": "spiffe://example.org/agent"},
		PresetOIDC:              {"issuer": "https://idp.example.com", "jwks_url": "https://idp.example.com/jwks", "subject": "agent-1"},
	}
	for _, p := range Catalog("") {
		b, err := NewBinding("tutor-agent", "prod", BindingRequest{Preset: p.ID, Inputs: inputs[p.ID]}, aud)
		if err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
		plan, err := RenderPlan(b, PlanContext{APIBaseURL: "https://aiplex.example.com", PrincipalID: "principal-x", ActorID: "principal-y"})
		if err != nil || len(plan.Steps) == 0 || (plan.TokenSnippet.Content == "" && plan.TokenSnippet.Description == "") || len(plan.AIPlex) < 2 {
			t.Fatalf("%s: incomplete plan %+v (%v)", p.ID, plan, err)
		}
		if plan.Issuer != b.Issuer || plan.Subject != b.Subject || plan.Audience != b.Audience {
			t.Fatalf("%s: plan does not echo the binding trust tuple", p.ID)
		}
	}
	b, _ := NewBinding("tutor-agent", "aws", BindingRequest{Preset: PresetAWSSTSWebIdentity, Inputs: inputs[PresetAWSSTSWebIdentity]}, aud)
	plan, _ := RenderPlan(b, PlanContext{})
	if !strings.Contains(plan.TokenSnippet.Content, "--signing-algorithm ES384") || !strings.Contains(plan.TokenSnippet.Content, "https://sts.eu-west-1.amazonaws.com") {
		t.Fatalf("AWS token snippet must request ES384 from a regional endpoint: %s", plan.TokenSnippet.Content)
	}
	if p, _ := Lookup(PresetAWSSTSWebIdentity); strings.Contains(strings.Join(p.Notes, " "), "request --signing-algorithm RS256") || !slices.Contains(p.Algorithms, "ES384") {
		t.Fatalf("AWS preset still describes the RS256 workaround: %+v", p)
	}
	b, _ = NewBinding("tutor-agent", "gha", BindingRequest{Preset: PresetGitHubActions, Inputs: inputs[PresetGitHubActions]}, aud)
	plan, _ = RenderPlan(b, PlanContext{})
	if !strings.Contains(plan.Steps[0].Content, "id-token: write") || !strings.Contains(plan.Steps[0].Content, `environment: "production"`) || !strings.Contains(plan.Steps[0].Content, "audience=https%3A%2F%2Faiplex.example.com") {
		t.Fatalf("GitHub workflow incomplete:\n%s", plan.Steps[0].Content)
	}
}

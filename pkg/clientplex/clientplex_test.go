package clientplex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func gatewayRegistration() Registration {
	return Registration{ID: "developer", ProfileID: "codex", Mode: "gateway", Protocol: Responses, DestinationRef: "models", PrincipalBinding: "principal:developer", ModelID: "declared-model", AuthMode: "gateway-key", CredentialEnv: "AIPLEX_TOKEN", CredentialRef: "credentials/developer"}
}
func destination(protocol string) ResolverFunc {
	return func(_ context.Context, tenant, id string) (Destination, error) {
		if tenant != "tenant-a" || id != "models" {
			panic("untrusted destination lookup")
		}
		return Destination{ID: id, Provider: "gateway", Endpoint: "https://models.internal.example/v1", Protocols: []string{protocol}, Availability: "configured"}, nil
	}
}
func TestCodexRequiresResponsesAndPreservesCredentialReferences(t *testing.T) {
	r := gatewayRegistration()
	if _, e := BuildPlan(context.Background(), "tenant-a", r, destination(OpenAIChat)); e == nil {
		t.Fatal("chat-only gateway accepted for Codex")
	}
	p, e := BuildPlan(context.Background(), "tenant-a", r, destination(Responses))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(p.Artifacts[0].Content, `wire_api = "responses"`) || len(p.CredentialBindings) != 1 || p.CredentialBindings[0].Reference != r.CredentialRef {
		t.Fatalf("wrong plan: %+v", p)
	}
	if strings.Contains(p.Artifacts[0].Content, r.CredentialRef) {
		t.Fatal("credential reference incorrectly embedded as secret")
	}
}
func TestCodexDirectGCPAndTokenHelperMutualExclusion(t *testing.T) {
	r := Registration{ID: "developer", ProfileID: "codex", Mode: "direct", Protocol: Responses, PrincipalBinding: "principal:developer", ProjectID: "example-project", Location: "global", AuthMode: "adc", CredentialFile: "/etc/adc.json"}
	p, e := BuildPlan(context.Background(), "tenant-a", r, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(p.Artifacts[0].Content, `model_provider = "gcp"`) || !strings.Contains(p.Artifacts[0].Content, `credential_file = "/etc/adc.json"`) {
		t.Fatal(p.Artifacts)
	}
	r = gatewayRegistration()
	r.AuthMode = "token-helper"
	if e = r.Validate(); e == nil {
		t.Fatal("missing helper accepted")
	}
	r.TokenHelper = &TokenHelper{Command: "/usr/local/bin/token-helper", Args: []string{"--audience", "models"}}
	p, e = BuildPlan(context.Background(), "tenant-a", r, destination(Responses))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(p.Artifacts[0].Content, "env_key") || strings.Contains(p.Artifacts[0].Content, "requires_openai_auth") {
		t.Fatal("helper combined with conflicting auth")
	}
}
func TestAntigravityACPCannotUseChatProtocol(t *testing.T) {
	r := gatewayRegistration()
	r.ProfileID = "antigravity-acp"
	r.Protocol = OpenAIChat
	if e := r.Validate(); e == nil {
		t.Fatal("ACP chat mode accepted")
	}
	r.ProfileID = "antigravity"
	p, e := BuildPlan(context.Background(), "tenant-a", r, destination(OpenAIChat))
	if e != nil {
		t.Fatal(e)
	}
	if len(p.PreserveHeaders) != 2 || p.CredentialBindings[0].Environment != "AIPLEX_TOKEN" || p.Artifacts[len(p.Artifacts)-1].Content != "export AGY_LLM_GATEWAY_API_KEY=\"$AIPLEX_TOKEN\"\n" {
		t.Fatal(p)
	}
	if !strings.Contains(strings.Join(p.Warnings, " "), "PAYG") {
		t.Fatal("billing distinction absent")
	}
}
func TestClaudeDesktopUsesBootstrapNotMessagesEnvironment(t *testing.T) {
	r := Registration{ID: "desktop", ProfileID: "claude-desktop", Mode: "claude-apps-gateway", Protocol: ClaudeApps, PrincipalBinding: "principal:user", DestinationRef: "models", AuthMode: "oidc"}
	p, e := BuildPlan(context.Background(), "tenant-a", r, destination(ClaudeApps))
	if e != nil {
		t.Fatal(e)
	}
	var artifact map[string]any
	if e = json.Unmarshal([]byte(p.Artifacts[0].Content), &artifact); e != nil {
		t.Fatal(e)
	}
	if artifact["bootstrapUrl"] != "https://models.internal.example/v1/user/bootstrap" || artifact["env"] != nil {
		t.Fatal(artifact)
	}
}
func TestClaudeAppsNativeProtocolAndVertexBinding(t *testing.T) {
	r := Registration{ID: "cli", ProfileID: "claude-code", Mode: "claude-apps-gateway", Protocol: Messages, PrincipalBinding: "principal:user", DestinationRef: "models", AuthMode: "oidc"}
	if e := r.Validate(); e == nil {
		t.Fatal("native sign-in treated as generic messages")
	}
	r = gatewayRegistration()
	r.ProfileID = "claude-code"
	r.Protocol = VertexAnthropic
	resolver := ResolverFunc(func(context.Context, string, string) (Destination, error) {
		return Destination{ID: "models", Endpoint: "https://gateway.internal/vertex", Protocols: []string{VertexAnthropic}, ProjectID: "approved-project", Location: "us-east5"}, nil
	})
	r.ProjectID = "another-project"
	if _, e := BuildPlan(context.Background(), "tenant-a", r, resolver); e == nil {
		t.Fatal("destination project overridden")
	}
	r.ProjectID = ""
	if _, e := BuildPlan(context.Background(), "tenant-a", r, resolver); e != nil {
		t.Fatal(e)
	}
}
func TestMaliciousURLsAndConfigDataAreRejectedOrEscaped(t *testing.T) {
	r := gatewayRegistration()
	for _, endpoint := range []string{"http://gateway.example/v1", "https://user:secret@gateway.example/v1", "https://gateway.example/v1?key=secret", "https://gateway.example/a/../v1", "https://gateway.example/%2e%2e"} {
		resolver := ResolverFunc(func(context.Context, string, string) (Destination, error) {
			return Destination{ID: "models", Endpoint: endpoint, Protocols: []string{Responses}}, nil
		})
		if _, e := BuildPlan(context.Background(), "tenant-a", r, resolver); e == nil {
			t.Fatalf("unsafe endpoint accepted %q", endpoint)
		}
	}
	r.ModelID = "model\n[model_providers.evil]"
	if e := r.Validate(); e == nil {
		t.Fatal("newline model accepted")
	}
	r = gatewayRegistration()
	r.CredentialEnv = "TOKEN;curl"
	if e := r.Validate(); e == nil {
		t.Fatal("invalid env accepted")
	}
}
func TestProfilesAreImmutableCopies(t *testing.T) {
	p := Profiles()
	p[0].Protocols[0] = "evil"
	if Profiles()[0].Protocols[0] == "evil" {
		t.Fatal("shared profile mutated")
	}
}

func TestProfileRegistryIsPerCompositionAndDefensivelyCopied(t *testing.T) {
	registry := NewRegistry()
	profile := Profile{ID: "other-client", Name: "Other", Surface: "cli", Modes: []string{"gateway"}, Protocols: []string{Responses}}
	renderer := func(p *Plan, _ Registration, _ Destination) error {
		p.Artifacts = append(p.Artifacts, Artifact{Target: "other.conf", Format: "json", MergeStrategy: "reviewed-fragment", Content: `{}`})
		return nil
	}
	if e := registry.Register(profile, renderer); e != nil {
		t.Fatal(e)
	}
	profile.Protocols[0] = "evil"
	if registry.Profiles()[0].Protocols[0] != Responses {
		t.Fatal("profile mutated")
	}
	if e := registry.Register(profile, renderer); e == nil {
		t.Fatal("duplicate accepted")
	}
	r := gatewayRegistration()
	r.ProfileID = "other-client"
	p, e := registry.BuildPlan(context.Background(), "tenant-a", r, destination(Responses))
	if e != nil || p.Artifacts[0].Target != "other.conf" {
		t.Fatal(p, e)
	}
	if _, e = BuildPlan(context.Background(), "tenant-a", r, destination(Responses)); e == nil {
		t.Fatal("profile leaked across composition")
	}
}

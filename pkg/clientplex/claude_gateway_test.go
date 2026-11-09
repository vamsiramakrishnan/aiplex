package clientplex

import (
	"encoding/json"
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func gatewayServer() GatewayServerSpec {
	return GatewayServerSpec{PublicURL: "https://claude.internal.example", ProjectID: "example-project", Location: "us-east5", OIDCIssuer: "https://idp.example/tenant/v2.0", OIDCClientID: "application-id", GroupsClaim: "roles", OIDCScopes: []string{"openid", "profile", "email", "offline_access"}, OIDCClientSecret: CredentialBinding{"secrets/oidc", "OIDC_SECRET"}, SessionSecret: CredentialBinding{"secrets/session", "SESSION_SECRET"}, PostgresURL: CredentialBinding{"secrets/postgres", "POSTGRES_URL"}, PostgresHost: "postgres.internal.example", AllowedCIDRs: []string{"10.0.0.0/8"}, TrustedProxies: []string{"10.1.0.0/16"}, Models: []GatewayModel{{"claude-alias", "claude-model-enabled-in-project"}}, Policies: []GroupModelRule{{Groups: []string{"engineering"}, Models: []string{"claude-alias"}, Desktop: true}}, TelemetryURL: "https://otel.internal.example:4318"}
}
func TestClaudeGatewayServerUsesADCAndSecretReferences(t *testing.T) {
	spec := gatewayServer()
	plan, e := RenderClaudeGateway(spec)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Join(plan.Command, " ") != "claude gateway --config gateway.yaml" {
		t.Fatal(plan.Command)
	}
	var config map[string]any
	if e = yaml.Unmarshal([]byte(plan.Artifact.Content), &config); e != nil {
		t.Fatal(e)
	}
	upstream := config["upstreams"].([]any)[0].(map[string]any)
	if upstream["provider"] != "vertex" || len(upstream["auth"].(map[string]any)) != 0 {
		t.Fatal("not using ADC", upstream)
	}
	if config["store"].(map[string]any)["postgres_url"] != "${POSTGRES_URL}" || config["session"].(map[string]any)["jwt_secret"] != "${SESSION_SECRET}" {
		t.Fatal("secret refs not preserved")
	}
	policy := config["managed"].(map[string]any)["policies"].([]any)[0].(map[string]any)
	if policy["desktop"] == nil {
		t.Fatal("desktop not opted in")
	}
	telemetry := config["telemetry"].(map[string]any)["forward_to"].([]any)[0].(map[string]any)
	if telemetry["metrics"] != true || telemetry["logs"] != false || telemetry["traces"] != false {
		t.Fatal("unexpected telemetry content")
	}
}
func TestClaudeGatewayServerRequiresCompleteSafeInputs(t *testing.T) {
	for _, mutate := range []func(*GatewayServerSpec){func(s *GatewayServerSpec) { s.PostgresURL.Environment = "postgres://secret" }, func(s *GatewayServerSpec) { s.OIDCClientSecret.Reference = "" }, func(s *GatewayServerSpec) { s.AllowedCIDRs = nil }, func(s *GatewayServerSpec) { s.Policies[0].Models = []string{"undeclared"} }, func(s *GatewayServerSpec) { s.TelemetryURL = "http://collector.example" }, func(s *GatewayServerSpec) { s.PublicURL = "https://gateway.example/path" }} {
		spec := gatewayServer()
		mutate(&spec)
		if _, e := RenderClaudeGateway(spec); e == nil {
			t.Fatal("incomplete config accepted")
		}
	}
}
func TestTOMLStringEscapesAreJSONSubset(t *testing.T) {
	for _, input := range []string{"model\a", "path\v", string([]byte{1, 2, 3}), "quote\"\\"} {
		quoted := q(input)
		var decoded string
		if e := json.Unmarshal([]byte(quoted), &decoded); e != nil || decoded != input {
			t.Fatalf("invalid portable quoting %q %v", quoted, e)
		}
		if strings.Contains(quoted, `\x`) || strings.Contains(quoted, `\a`) || strings.Contains(quoted, `\v`) {
			t.Fatalf("Go-only escape emitted %q", quoted)
		}
	}
}

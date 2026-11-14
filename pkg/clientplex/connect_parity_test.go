package clientplex_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/harness"
	"github.com/vamsiramakrishnan/aiplex/internal/harness/fragment"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

const gatewayURL = "https://gateway.internal.example/ap/dev"

func resolver(protocol string) clientplex.ResolverFunc {
	return func(_ context.Context, _, id string) (clientplex.Destination, error) {
		return clientplex.Destination{ID: id, Provider: "gateway", Endpoint: gatewayURL, Protocols: []string{protocol}, Availability: "configured"}, nil
	}
}

func claudeRegistration() clientplex.Registration {
	return clientplex.Registration{ID: "claude", ProfileID: "claude-code", Mode: "gateway", Protocol: clientplex.Messages, DestinationRef: "models", PrincipalBinding: "principal:dev", ModelID: "claude-example", AuthMode: "token-helper", TokenHelper: &clientplex.TokenHelper{Command: "/usr/local/bin/aiplex", Args: []string{"--context", "work space", "auth", "print-token"}}}
}

func geminiRegistration() clientplex.Registration {
	return clientplex.Registration{ID: "gemini", ProfileID: "gemini-cli", Mode: "gateway", Protocol: clientplex.GenAI, DestinationRef: "models", PrincipalBinding: "principal:dev", ModelID: "gemini-example", AuthMode: "gateway-key", CredentialEnv: "GEMINI_API_KEY", CredentialRef: "credentials/dev"}
}

func plan(t *testing.T, r clientplex.Registration, protocol string) clientplex.Plan {
	t.Helper()
	p, err := clientplex.BuildPlan(context.Background(), "tenant-a", r, resolver(protocol))
	if err != nil {
		t.Fatal(err)
	}
	if p.SupportStatus != "configured-unverified" {
		t.Fatalf("support status %q", p.SupportStatus)
	}
	return p
}

func artifact(t *testing.T, p clientplex.Plan, target string) string {
	t.Helper()
	for _, a := range p.Artifacts {
		if a.Target == target {
			return a.Content
		}
	}
	t.Fatalf("no artifact %s in %+v", target, p.Artifacts)
	return ""
}

func connectFixture() *aiplex.AccessPointConnect {
	return &aiplex.AccessPointConnect{
		AccessPointID: "dev",
		BaseURLs:      map[string]string{"anthropic": gatewayURL, "genai": gatewayURL},
		Models: []aiplex.ConnectModel{
			{Name: "claude-example", Protocols: []string{"anthropic-messages"}},
			{Name: "gemini-example", Protocols: []string{"google-genai"}},
		},
	}
}

// connectPlan runs `aiplex connect --write`'s planner against an empty home and
// returns the bytes it would write to file, with the plan for its Remaining steps.
func connectPlan(t *testing.T, plan func(home string) (*harness.Plan, error), file ...string) (string, *harness.Plan) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	p, err := plan(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Changes {
		if c.Path == filepath.Join(append([]string{home}, file...)...) {
			return string(c.After), p
		}
	}
	t.Fatalf("connect --write planned no %v: %+v", file, p.Changes)
	return "", nil
}

// remainingHas reports whether a step `aiplex connect` prints contains line.
func remainingHas(p *harness.Plan, line string) bool {
	return slices.ContainsFunc(p.Remaining, func(step string) bool { return strings.Contains(step, line) })
}

// One helper argv feeds both sides, so the same inputs give the same bytes.
var helperArgv = []string{"/usr/local/bin/aiplex", "--context", "work space", "auth", "print-token"}

func TestClaudeCodeTokenHelperPlanMatchesConnectWrite(t *testing.T) {
	r := claudeRegistration()
	r.TokenHelper = &clientplex.TokenHelper{Command: helperArgv[0], Args: helperArgv[1:]}
	p := plan(t, r, clientplex.Messages)
	got := artifact(t, p, "~/.claude/settings.json")
	written, connect := connectPlan(t, func(home string) (*harness.Plan, error) {
		return harness.PlanClaudeCode(connectFixture(), harness.Options{Home: home, Model: "claude-example", TokenHelper: true, HelperArgv: helperArgv})
	}, ".claude", "settings.json")
	if got != written {
		t.Fatalf("ClientPlex and connect --write differ\nplan: %s\nwrite: %s", got, written)
	}
	if !strings.Contains(got, `"apiKeyHelper": "/usr/local/bin/aiplex --context 'work space' auth print-token"`) {
		t.Fatalf("helper line missing or unquoted:\n%s", got)
	}
	if !remainingHas(connect, "`"+fragment.ShellCommand(helperArgv)+"`") {
		t.Fatalf("connect does not name the same helper command: %q", connect.Remaining)
	}
	for _, b := range p.CredentialBindings {
		if b.Environment == "ANTHROPIC_AUTH_TOKEN" {
			t.Fatalf("ANTHROPIC_AUTH_TOKEN would take precedence over apiKeyHelper: %+v", b)
		}
	}
	if strings.Contains(got, "credentials/") {
		t.Fatal("credential reference embedded in the fragment")
	}
}

func TestClaudeCodeEnvOnlyPlanMatchesConnectWrite(t *testing.T) {
	r := claudeRegistration()
	r.AuthMode, r.TokenHelper = "gateway-key", nil
	r.CredentialEnv, r.CredentialRef = harness.TokenEnv, "credentials/dev"
	p := plan(t, r, clientplex.Messages)
	written, connect := connectPlan(t, func(home string) (*harness.Plan, error) {
		return harness.PlanClaudeCode(connectFixture(), harness.Options{Home: home, Model: "claude-example"})
	}, ".claude", "settings.json")
	if got := artifact(t, p, "~/.claude/settings.json"); got != written {
		t.Fatalf("ClientPlex and connect --write differ\nplan: %s\nwrite: %s", got, written)
	}
	// The copy line the plan carries is the step connect prints.
	line := strings.TrimSpace(artifact(t, p, "workstation shell"))
	if !remainingHas(connect, line) {
		t.Fatalf("connect prints no step containing %q: %q", line, connect.Remaining)
	}
	if p.CredentialBindings[0].Environment != harness.TokenEnv {
		t.Fatalf("binding %+v", p.CredentialBindings)
	}
}

func TestClaudeCodeTokenHelperRefusals(t *testing.T) {
	cases := map[string]func(*clientplex.Registration){
		"relative command":       func(r *clientplex.Registration) { r.TokenHelper.Command = "aiplex" },
		"missing helper":         func(r *clientplex.Registration) { r.TokenHelper = nil },
		"timeout with no field":  func(r *clientplex.Registration) { r.TokenHelper.TimeoutMS = 5000 },
		"refresh with no field":  func(r *clientplex.Registration) { r.TokenHelper.RefreshIntervalMS = 60000 },
		"credential ref unused":  func(r *clientplex.Registration) { r.CredentialRef = "credentials/dev" },
		"vertex gateway":         func(r *clientplex.Registration) { r.Protocol = clientplex.VertexAnthropic },
		"newline in command":     func(r *clientplex.Registration) { r.TokenHelper.Command = "/bin/a\nb" },
		"not a token-helper too": func(r *clientplex.Registration) { r.AuthMode = "oidc" },
	}
	for name, mutate := range cases {
		r := claudeRegistration()
		mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: registration accepted", name)
		}
	}
}

func TestGeminiCLIIsABuiltinProfileAndMatchesConnectWrite(t *testing.T) {
	var found bool
	for _, p := range clientplex.Profiles() {
		if p.ID == "gemini-cli" {
			found = true
			if len(p.Modes) != 1 || p.Modes[0] != "gateway" || len(p.Protocols) != 1 || p.Protocols[0] != clientplex.GenAI {
				t.Fatalf("profile %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("gemini-cli is not a built-in profile")
	}
	r := geminiRegistration()
	r.CredentialEnv = harness.TokenEnv // where `aiplex connect` tells the user the token lives
	p := plan(t, r, clientplex.GenAI)
	got := artifact(t, p, "~/.gemini/settings.json")
	written, connect := connectPlan(t, func(home string) (*harness.Plan, error) {
		return harness.PlanGeminiCLI(connectFixture(), harness.Options{Home: home, Model: "gemini-example"})
	}, ".gemini", "settings.json")
	if got != written {
		t.Fatalf("ClientPlex and connect --write differ\nplan: %s\nwrite: %s", got, written)
	}
	env := artifact(t, p, "workstation environment")
	if env != "GOOGLE_GEMINI_BASE_URL="+gatewayURL+"\n" {
		t.Fatalf("environment %q", env)
	}
	// The environment and copy lines are steps `aiplex connect` prints too.
	if line := "export " + strings.TrimSpace(env); !remainingHas(connect, line) {
		t.Fatalf("connect prints no step containing %q: %q", line, connect.Remaining)
	}
	if line := strings.TrimSpace(artifact(t, p, "workstation shell")); !remainingHas(connect, line) {
		t.Fatalf("connect prints no step containing %q: %q", line, connect.Remaining)
	}
	if len(p.CredentialBindings) != 1 || p.CredentialBindings[0].Reference != "credentials/dev" || p.CredentialBindings[0].Environment != harness.TokenEnv {
		t.Fatalf("bindings %+v", p.CredentialBindings)
	}
	if strings.Contains(got+env, "credentials/dev") {
		t.Fatal("credential reference embedded in the plan")
	}
}

// credential_env names the variable the launcher already holds the credential
// in; Gemini CLI reads only GEMINI_API_KEY, so the plan carries the copy line.
func TestGeminiCLIOtherCredentialEnvRendersCopyLine(t *testing.T) {
	r := geminiRegistration()
	r.CredentialEnv = "AIPLEX_TOKEN"
	p := plan(t, r, clientplex.GenAI)
	if got := artifact(t, p, "workstation shell"); got != "export GEMINI_API_KEY=\"$AIPLEX_TOKEN\"\n" {
		t.Fatalf("copy line %q", got)
	}
	if p.CredentialBindings[0].Environment != "AIPLEX_TOKEN" {
		t.Fatalf("binding %+v", p.CredentialBindings)
	}
}

// Every auth_mode the type knows, for Gemini CLI: only gateway-key is accepted,
// and a refusal names the field.
func TestGeminiCLIAuthModes(t *testing.T) {
	for _, mode := range []string{"", "adc", "workforce", "gateway-key", "token-helper", "oidc", "bogus"} {
		r := geminiRegistration()
		r.AuthMode = mode
		if mode == "token-helper" {
			r.TokenHelper = &clientplex.TokenHelper{Command: "/usr/local/bin/aiplex"}
			r.CredentialEnv, r.CredentialRef = "", ""
		}
		err := r.Validate()
		if mode == "gateway-key" {
			if err != nil {
				t.Errorf("gateway-key refused: %v", err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "auth_mode") {
			t.Errorf("auth_mode %q for gemini-cli: want a refusal naming auth_mode, got %v", mode, err)
		}
	}
}

func TestGeminiCLIRefusals(t *testing.T) {
	r := geminiRegistration()
	r.AuthMode, r.CredentialEnv, r.CredentialRef = "token-helper", "", ""
	r.TokenHelper = &clientplex.TokenHelper{Command: "/usr/local/bin/aiplex"}
	err := r.Validate()
	if err == nil || !strings.Contains(err.Error(), "no credential helper setting") {
		t.Fatalf("token-helper for Gemini CLI: %v", err)
	}
	cases := map[string]func(*clientplex.Registration){
		"direct mode":      func(r *clientplex.Registration) { r.Mode, r.DestinationRef = "direct", "" },
		"chat protocol":    func(r *clientplex.Registration) { r.Protocol = clientplex.OpenAIChat },
		"managed (unused)": func(r *clientplex.Registration) { r.Managed = true },
	}
	for name, mutate := range cases {
		r := geminiRegistration()
		mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: registration accepted", name)
		}
	}
}

// platform was validated and never read by any renderer; it is gone. A record
// stored while it existed still loads and plans, and the field is not written
// back.
func TestStoredRegistrationWithPlatformStillLoads(t *testing.T) {
	stored := `{"id":"developer","profile_id":"codex","mode":"gateway","protocol":"openai-responses","destination_ref":"models","principal_binding":"principal:dev","auth_mode":"gateway-key","credential_env":"AIPLEX_TOKEN","credential_ref":"credentials/dev","platform":"linux"}`
	var r clientplex.Registration
	if err := json.Unmarshal([]byte(stored), &r); err != nil {
		t.Fatal(err)
	}
	if p := plan(t, r, clientplex.Responses); len(p.Artifacts) == 0 {
		t.Fatal("no artifact")
	}
	out, _ := json.Marshal(r)
	if strings.Contains(string(out), "platform") {
		t.Fatalf("platform written back: %s", out)
	}
}

package clientplex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
)

func combinationResolver(protocol string) clientplex.ResolverFunc {
	return func(_ context.Context, _, id string) (clientplex.Destination, error) {
		return clientplex.Destination{ID: id, Provider: "gateway", Endpoint: gatewayURL, Protocols: []string{protocol}, Availability: "configured"}, nil
	}
}

func claudeBase(mode, protocol, auth string) clientplex.Registration {
	r := clientplex.Registration{ID: "claude", ProfileID: "claude-code", Mode: mode, Protocol: protocol, PrincipalBinding: "principal:dev", AuthMode: auth}
	if mode != "direct" {
		r.DestinationRef = "models"
	}
	switch auth {
	case "gateway-key":
		r.CredentialEnv, r.CredentialRef = "ANTHROPIC_AUTH_TOKEN", "credentials/dev"
	case "token-helper":
		r.TokenHelper = &clientplex.TokenHelper{Command: "/usr/local/bin/aiplex", Args: []string{"auth", "print-token"}}
	}
	if mode == "direct" || protocol == clientplex.VertexAnthropic {
		r.ProjectID, r.Location = "example-project", "us-east5"
	}
	return r
}

func baseRegistration(profile, mode, protocol, auth string) clientplex.Registration {
	r := clientplex.Registration{ID: "dev", ProfileID: profile, Mode: mode, Protocol: protocol, PrincipalBinding: "principal:dev", AuthMode: auth}
	if mode != "direct" {
		r.DestinationRef = "models"
	}
	if mode == "direct" {
		r.ProjectID, r.Location = "example-project", "us-east5"
	}
	switch auth {
	case "gateway-key":
		r.CredentialEnv, r.CredentialRef = "AIPLEX_TOKEN", "credentials/dev"
	case "token-helper":
		r.TokenHelper = &clientplex.TokenHelper{Command: "/usr/local/bin/aiplex", Args: []string{"auth", "print-token"}}
		r.CredentialRef = "credentials/dev"
	}
	return r
}

func planOf(r clientplex.Registration) (clientplex.Plan, error) {
	return clientplex.BuildPlan(context.Background(), "tenant-a", r, combinationResolver(r.Protocol))
}

func planJSON(r clientplex.Registration) (string, error) {
	p, err := planOf(r)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(p)
	return string(b), nil
}

func cloneRegistration(r clientplex.Registration) clientplex.Registration {
	if r.TokenHelper != nil {
		h := *r.TokenHelper
		r.TokenHelper = &h
	}
	return r
}

// A plan minus its warnings: what the client is told to configure.
func substance(p clientplex.Plan) string {
	p.Warnings = nil
	b, _ := json.Marshal(p)
	return string(b)
}

var toggles = []struct {
	field string
	set   func(*clientplex.Registration)
}{
	{"project_id", func(r *clientplex.Registration) { r.ProjectID = "other-project" }},
	{"location", func(r *clientplex.Registration) { r.Location = "europe-west1" }},
	{"model_id", func(r *clientplex.Registration) { r.ModelID = "model-example" }},
	{"allowed_models", func(r *clientplex.Registration) { r.AllowedModels = []string{"model-example"} }},
	{"credential_env", func(r *clientplex.Registration) { r.CredentialEnv = "OTHER_TOKEN" }},
	{"credential_ref", func(r *clientplex.Registration) { r.CredentialRef = "credentials/other" }},
	{"managed", func(r *clientplex.Registration) { r.Managed = true }},
}

// Walks every accepted combination of every profile and sets each optional field
// in turn. One rule: the field is used (the plan's artifacts, bindings or
// services change), or it is refused with an error that names it, or the plan
// carries a warning that names it. Claude Desktop's managed flag is the one
// accepted field that changes nothing and says nothing, because its plan is
// already the managed configuration.
func TestEveryRegistrationFieldIsUsedRefusedOrNamedInAWarning(t *testing.T) {
	var bases []clientplex.Registration
	for _, mode := range []string{"gateway", "direct", "claude-apps-gateway"} {
		for _, protocol := range []string{clientplex.Messages, clientplex.VertexAnthropic, clientplex.ClaudeApps} {
			for _, auth := range []string{"gateway-key", "token-helper", "oidc", "adc", "workforce", ""} {
				bases = append(bases, claudeBase(mode, protocol, auth))
			}
		}
	}
	bases = append(bases,
		baseRegistration("codex", "gateway", clientplex.Responses, "gateway-key"),
		baseRegistration("codex", "gateway", clientplex.Responses, "token-helper"),
		baseRegistration("codex", "direct", clientplex.Responses, "adc"),
		baseRegistration("claude-desktop", "claude-apps-gateway", clientplex.ClaudeApps, "oidc"),
		baseRegistration("gemini-cli", "gateway", clientplex.GenAI, "gateway-key"),
		baseRegistration("antigravity", "gateway", clientplex.GenAI, "gateway-key"),
		baseRegistration("antigravity", "gateway", clientplex.OpenAIChat, "gateway-key"),
		baseRegistration("antigravity-acp", "gateway", clientplex.GenAI, "gateway-key"),
		baseRegistration("antigravity", "direct", clientplex.GenAI, "adc"),
		baseRegistration("antigravity", "direct", clientplex.GenAI, "workforce"),
	)
	table := map[string]bool{}
	accepted := 0
	for _, base := range bases {
		for _, managed := range []bool{false, true} {
			r := cloneRegistration(base)
			r.Managed = managed
			basePlan, err := planOf(r)
			if err != nil {
				continue
			}
			accepted++
			if managed && r.ProfileID == "claude-code" && !strings.Contains(fmt.Sprint(basePlan.Artifacts), "(MDM)") {
				t.Errorf("%s/%s/%s: managed is accepted but no managed-settings target is rendered", r.ProfileID, r.Mode, r.AuthMode)
			}
			for _, tg := range toggles {
				variant := cloneRegistration(r)
				tg.set(&variant)
				if substance(basePlan) == "" || reflect.DeepEqual(variant, r) {
					continue // the toggle sets what is already set
				}
				row := fmt.Sprintf("%-16s %-20s %-12s %-15s", r.ProfileID, r.Mode, r.AuthMode, tg.field)
				got, err := planOf(variant)
				switch {
				case err != nil:
					if !strings.Contains(err.Error(), tg.field) {
						t.Errorf("%s: refused for another reason: %v", row, err)
					}
					table[row+" refused"] = true
				case substance(got) != substance(basePlan):
					table[row+" used"] = true
				case slices.ContainsFunc(got.Warnings, func(w string) bool { return strings.Contains(w, tg.field) }):
					table[row+" warning"] = true
				case tg.field == "managed" && (r.ProfileID == "claude-desktop" || r.ProfileID == "antigravity-acp"):
					table[row+" accepted, no warning (plan is already the managed configuration)"] = true
				default:
					t.Errorf("%s: accepted and changes nothing, with no warning naming it", row)
				}
			}
		}
	}
	if accepted == 0 {
		t.Fatal("no combination was accepted")
	}
	var rows []string
	for row := range table {
		rows = append(rows, row)
	}
	slices.Sort(rows)
	t.Log("\n" + strings.Join(rows, "\n"))
}

func TestFieldsThatPromiseControlAreRefusedWhereThePlanCannotDeliverIt(t *testing.T) {
	cases := []struct {
		name   string
		r      clientplex.Registration
		mutate func(*clientplex.Registration)
		field  string
	}{
		{"codex direct allowed_models", baseRegistration("codex", "direct", clientplex.Responses, "adc"), func(r *clientplex.Registration) { r.AllowedModels = []string{"m"} }, "allowed_models"},
		{"claude direct allowed_models", claudeBase("direct", clientplex.VertexAnthropic, "adc"), func(r *clientplex.Registration) { r.AllowedModels = []string{"m"} }, "allowed_models"},
		{"antigravity direct allowed_models", baseRegistration("antigravity", "direct", clientplex.GenAI, "adc"), func(r *clientplex.Registration) { r.AllowedModels = []string{"m"} }, "allowed_models"},
		{"antigravity direct managed", baseRegistration("antigravity", "direct", clientplex.GenAI, "adc"), func(r *clientplex.Registration) { r.Managed = true }, "managed"},
		{"codex gateway managed", baseRegistration("codex", "gateway", clientplex.Responses, "gateway-key"), func(r *clientplex.Registration) { r.Managed = true }, "managed"},
		{"gemini managed", geminiRegistration(), func(r *clientplex.Registration) { r.Managed = true }, "managed"},
	}
	for _, c := range cases {
		r := c.r
		c.mutate(&r)
		// write: Validate is what PUT runs; plan: BuildPlan runs it again on a stored record.
		for step, run := range map[string]func() error{
			"write": r.Validate,
			"plan":  func() error { _, err := planOf(r); return err },
		} {
			err := run()
			if err == nil || !strings.Contains(err.Error(), c.field) || !strings.Contains(err.Error(), "remove") {
				t.Errorf("%s (%s): want a refusal naming %s and what to remove, got %v", c.name, step, c.field, err)
			}
		}
		// read: a stored record carrying the field still decodes.
		var back clientplex.Registration
		raw, _ := json.Marshal(r)
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Errorf("%s: stored record unreadable: %v", c.name, err)
		}
	}
}

// Fields with no effect are accepted and each is named in one plan warning: a
// registration valid when stored keeps its plan.
func TestInertFieldsAreAcceptedWithOneWarningEach(t *testing.T) {
	cases := []struct {
		name   string
		r      clientplex.Registration
		mutate func(*clientplex.Registration)
		fields []string
	}{
		{"codex gateway project and location (Console starter switched to gateway)", baseRegistration("codex", "gateway", clientplex.Responses, "gateway-key"), func(r *clientplex.Registration) { r.ProjectID, r.Location = "your-project", "global" }, []string{"project_id", "location"}},
		{"codex token-helper credential_env", baseRegistration("codex", "gateway", clientplex.Responses, "token-helper"), func(r *clientplex.Registration) { r.CredentialEnv = "AIPLEX_TOKEN" }, []string{"credential_env"}},
		{"claude messages project and location", claudeBase("gateway", clientplex.Messages, "gateway-key"), func(r *clientplex.Registration) { r.ProjectID, r.Location = "example-project", "us-east5" }, []string{"project_id", "location"}},
		{"claude direct credentials", claudeBase("direct", clientplex.VertexAnthropic, "adc"), func(r *clientplex.Registration) { r.CredentialEnv, r.CredentialRef = "AIPLEX_TOKEN", "credentials/dev" }, []string{"credential_env", "credential_ref"}},
		{"claude apps gateway", claudeBase("claude-apps-gateway", clientplex.ClaudeApps, "oidc"), func(r *clientplex.Registration) {
			r.ModelID, r.ProjectID, r.Location, r.CredentialEnv, r.CredentialRef = "m", "example-project", "us-east5", "AIPLEX_TOKEN", "credentials/dev"
		}, []string{"model_id", "project_id", "location", "credential_env", "credential_ref"}},
		{"antigravity direct model_id", baseRegistration("antigravity", "direct", clientplex.GenAI, "adc"), func(r *clientplex.Registration) { r.ModelID = "m" }, []string{"model_id"}},
	}
	for _, c := range cases {
		base, err := planOf(c.r)
		if err != nil {
			t.Fatalf("%s: base refused: %v", c.name, err)
		}
		r := cloneRegistration(c.r)
		c.mutate(&r)
		if err := r.Validate(); err != nil { // write
			t.Errorf("%s: write refused: %v", c.name, err)
			continue
		}
		raw, _ := json.Marshal(r) // read
		var back clientplex.Registration
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		p, err := planOf(back) // plan
		if err != nil {
			t.Errorf("%s: plan refused: %v", c.name, err)
			continue
		}
		for _, f := range c.fields {
			n := 0
			for _, w := range p.Warnings {
				if strings.Contains(w, "field "+f+" has no effect") {
					n++
				}
			}
			if n != 1 {
				t.Errorf("%s: want one warning for %s, got %d in %q", c.name, f, n, p.Warnings)
			}
		}
		if len(base.Artifacts) != len(p.Artifacts) {
			t.Errorf("%s: an inert field changed the artifacts", c.name)
		}
	}
}

// managed on Claude Desktop is true and harmless: its plan is already the
// managed configuration, so nothing changes and nothing is said.
func TestClaudeDesktopManagedIsAcceptedWithoutWarning(t *testing.T) {
	r := baseRegistration("claude-desktop", "claude-apps-gateway", clientplex.ClaudeApps, "oidc")
	before, err := planOf(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Managed = true
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	after, err := planOf(r)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("managed changed the Claude Desktop plan or added a warning:\n%+v\n%+v", before, after)
	}
}

// managed on Claude Code in apps-gateway mode changes the plan: the login
// settings move from the user file to the managed-settings target, with
// parentSettingsBehavior merge, and the user-file version requirement goes away.
func TestClaudeCodeAppsGatewayManagedChangesThePlan(t *testing.T) {
	r := claudeBase("claude-apps-gateway", clientplex.ClaudeApps, "oidc")
	user, err := planOf(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Managed = true
	managed, err := planOf(r)
	if err != nil {
		t.Fatal(err)
	}
	if user.Artifacts[0].Target != "~/.claude/settings.json" || managed.Artifacts[0].Target != "Claude Code managed-settings.json (MDM)" {
		t.Fatalf("targets: %q vs %q", user.Artifacts[0].Target, managed.Artifacts[0].Target)
	}
	if !strings.Contains(managed.Artifacts[0].Content, `"parentSettingsBehavior": "merge"`) || strings.Contains(user.Artifacts[0].Content, "parentSettingsBehavior") {
		t.Fatalf("content: %s vs %s", managed.Artifacts[0].Content, user.Artifacts[0].Content)
	}
	if len(user.VersionRequirements) != 1 || len(managed.VersionRequirements) != 0 {
		t.Fatalf("version requirements: %v vs %v", user.VersionRequirements, managed.VersionRequirements)
	}
}

// allowed_models in a gateway mode is checked against the destination's declared
// models and enforced nowhere at request time; the plan says so.
func TestAllowedModelsInGatewayModeCarriesTheNotARestrictionWarning(t *testing.T) {
	for _, r := range []clientplex.Registration{
		baseRegistration("codex", "gateway", clientplex.Responses, "gateway-key"),
		claudeBase("gateway", clientplex.Messages, "gateway-key"),
		geminiRegistration(),
		baseRegistration("claude-desktop", "claude-apps-gateway", clientplex.ClaudeApps, "oidc"),
	} {
		r.AllowedModels = []string{"model-example"}
		p, err := planOf(r)
		if err != nil {
			t.Fatalf("%s: %v", r.ProfileID, err)
		}
		if !slices.ContainsFunc(p.Warnings, func(w string) bool {
			return strings.Contains(w, "allowed_models") && strings.Contains(w, "not enforced by the client configuration")
		}) {
			t.Errorf("%s: no allowed_models warning in %q", r.ProfileID, p.Warnings)
		}
	}
}

func TestClaudeCodeTokenHelperStaysStrictAboutUnusedCredentials(t *testing.T) {
	for _, mutate := range []func(*clientplex.Registration){
		func(r *clientplex.Registration) { r.CredentialEnv = "AIPLEX_TOKEN" },
		func(r *clientplex.Registration) { r.CredentialRef = "credentials/dev" },
	} {
		r := claudeBase("gateway", clientplex.Messages, "token-helper")
		mutate(&r)
		if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "credential_") {
			t.Errorf("accepted or unnamed: %v", err)
		}
	}
}

// credential_env is where the launcher holds the credential. The binding names
// that variable and the line copies it into the one the client reads, so a
// launcher that follows the binding and then runs the line ends with the
// credential, never an empty value.
func TestCredentialEnvBindingAndCopyLineAgree(t *testing.T) {
	for name, tc := range map[string]struct {
		r        clientplex.Registration
		protocol string
		clientEn string
	}{
		"claude-code": {claudeBase("gateway", clientplex.Messages, "gateway-key"), clientplex.Messages, "ANTHROPIC_AUTH_TOKEN"},
		"gemini-cli":  {geminiRegistration(), clientplex.GenAI, "GEMINI_API_KEY"},
	} {
		r := tc.r
		r.CredentialEnv = "AIPLEX_TOKEN"
		p := plan(t, r, tc.protocol)
		if len(p.CredentialBindings) != 1 || p.CredentialBindings[0].Environment != "AIPLEX_TOKEN" || p.CredentialBindings[0].Reference != r.CredentialRef {
			t.Fatalf("%s: binding %+v", name, p.CredentialBindings)
		}
		if got, want := artifact(t, p, "workstation shell"), "export "+tc.clientEn+"=\"$AIPLEX_TOKEN\"\n"; got != want {
			t.Fatalf("%s: line %q want %q", name, got, want)
		}
		// With the client's own name there is nothing to copy.
		r.CredentialEnv = tc.clientEn
		p = plan(t, r, tc.protocol)
		if p.CredentialBindings[0].Environment != tc.clientEn {
			t.Fatalf("%s: binding %+v", name, p.CredentialBindings)
		}
		for _, a := range p.Artifacts {
			if a.Target == "workstation shell" {
				t.Fatalf("%s: unneeded copy line", name)
			}
		}
	}
}

// Registrations stored before the refusals existed still read; the plan of one
// that carries a refused field is refused with the field named.
func TestStoredRegistrationWithRefusedFieldLoadsButDoesNotPlan(t *testing.T) {
	stored := `{"id":"dev","profile_id":"codex","mode":"direct","protocol":"openai-responses","principal_binding":"principal:dev","auth_mode":"adc","project_id":"example-project","location":"global","allowed_models":["gpt-example"]}`
	var r clientplex.Registration
	if err := json.Unmarshal([]byte(stored), &r); err != nil {
		t.Fatal(err)
	}
	_, err := planJSON(r)
	if err == nil || !strings.Contains(err.Error(), "allowed_models") || !strings.Contains(err.Error(), "remove") {
		t.Fatalf("plan: %v", err)
	}
}

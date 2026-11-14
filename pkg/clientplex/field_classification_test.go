package clientplex_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
)

func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return name
}

// Every Registration field must be classified here. A field added later is in
// neither list until a developer decides whether it is toggled by the matrix in
// unused_fields_test.go (it can be used, refused or warned about) or structural,
// so it cannot slip past the rule unnoticed.
func TestEveryRegistrationFieldIsClassified(t *testing.T) {
	structural := map[string]string{
		"id":                  "identity; validated by pattern, never rendered as a setting",
		"profile_id":          "selects the profile; the matrix walks profiles",
		"mode":                "selects the mode; the matrix walks modes",
		"protocol":            "selects the protocol; the matrix walks protocols",
		"auth_mode":           "selects the auth mode; the matrix walks auth modes",
		"destination_ref":     "resolved to the destination every gateway plan uses",
		"principal_binding":   "identity binding stored and shown, required, not a client setting",
		"network_profile_ref": "copied into the plan and checked against the tenant's network profiles",
		"gateway_server":      "renders the gateway server artifact; refused outside claude-apps-gateway mode",
		"credential_file":     "validated as an absolute Codex direct path and rendered there; refused elsewhere",
		"token_helper":        "refused unless auth_mode is token-helper; walked by the token-helper bases",
	}
	toggled := map[string]bool{}
	for _, tg := range toggles {
		toggled[tg.field] = true
	}
	typ := reflect.TypeOf(clientplex.Registration{})
	present := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := jsonName(typ.Field(i))
		present[name] = true
		if _, ok := structural[name]; !ok && !toggled[name] {
			t.Errorf("Registration field %q is not classified: add it to the toggles of TestEveryRegistrationFieldIsUsedRefusedOrNamedInAWarning (and to fieldEffects in fields.go) if it can be used, refused or warned about, or to the structural list of this test with the reason it is not a per-mode setting", name)
		}
	}
	for name := range structural {
		if !present[name] {
			t.Errorf("the structural list names %q, which is not a Registration field", name)
		}
	}
	for _, tg := range toggles {
		if !present[tg.field] {
			t.Errorf("the toggle list names %q, which is not a Registration field", tg.field)
		}
	}
}

// Every refused field is named in one message, with what to remove.
func TestEveryRefusedFieldIsNamedInOneMessage(t *testing.T) {
	r := baseRegistration("codex", "direct", clientplex.Responses, "adc")
	r.AllowedModels = []string{"m"}
	r.Managed = true
	err := r.Validate()
	if err == nil || !strings.Contains(err.Error(), "remove allowed_models") || !strings.Contains(err.Error(), "remove managed") {
		t.Fatalf("want both fields named with what to remove, got %v", err)
	}
	if _, perr := planOf(r); perr == nil || perr.Error() != err.Error() {
		t.Fatalf("plan message differs from the write message: %v vs %v", perr, err)
	}
}

// The Console's Antigravity and ACP prefills and the Overview's synthetic
// registration carry credential_env, which Antigravity gateway mode requires: no
// warning calls it inert. Equal to the client's own variable there is no copy
// line; another name binds the reference to it and adds the line.
func TestAntigravityGatewayCredentialEnvIsUsed(t *testing.T) {
	for _, profile := range []string{"antigravity", "antigravity-acp"} {
		prefill := baseRegistration(profile, "gateway", clientplex.GenAI, "gateway-key") // the Console prefill's keys
		overview := prefill                                                              // the Overview's synthetic registration
		overview.ModelID, overview.AllowedModels, overview.CredentialRef = "gemini-example", []string{"gemini-example"}, "aiplex-access-token"
		for name, r := range map[string]clientplex.Registration{"prefill": prefill, "overview": overview} {
			p := plan(t, r, clientplex.GenAI)
			for _, w := range p.Warnings {
				if strings.Contains(w, "has no effect") {
					t.Errorf("%s/%s: %s", profile, name, w)
				}
			}
			if p.CredentialBindings[0].Environment != "AIPLEX_TOKEN" || artifact(t, p, "workstation shell") != "export AGY_LLM_GATEWAY_API_KEY=\"$AIPLEX_TOKEN\"\n" {
				t.Errorf("%s/%s: binding %+v and line disagree", profile, name, p.CredentialBindings)
			}
		}
		own := prefill
		own.CredentialEnv = "AGY_LLM_GATEWAY_API_KEY"
		p := plan(t, own, clientplex.GenAI)
		if p.CredentialBindings[0].Environment != "AGY_LLM_GATEWAY_API_KEY" {
			t.Errorf("%s: binding %+v", profile, p.CredentialBindings)
		}
		for _, a := range p.Artifacts {
			if a.Target == "workstation shell" {
				t.Errorf("%s: copy line for the client's own variable", profile)
			}
		}
	}
}

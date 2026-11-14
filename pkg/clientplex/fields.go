package clientplex

import (
	"fmt"
	"strings"
)

// effects classifies the optional registration fields that are set, for one
// profile and mode, by what the plan does with them. A field the plan uses
// appears in neither list. One rule applies to every profile:
//
//   - refused: a field that promises control over the client (allowed_models,
//     managed) where the plan does not deliver that control and nothing else
//     enforces it. Rendering a plan would leave the operator believing in a
//     restriction that does not exist, so validation refuses it (400 on write,
//     409 on plan) naming the field and what to change.
//   - noEffect: any other field the plan does not use. It is accepted, so a
//     registration that was valid when stored keeps its plan, and the plan
//     carries one warning per field.
//   - allowedNote: allowed_models in a gateway mode: checked against the
//     destination's declared models when the registration is saved or planned,
//     and enforced nowhere at request time (see Registration.AllowedModels).
type effects struct {
	refused     []fieldRefusal
	noEffect    []string
	allowedNote bool
}

type fieldRefusal struct{ field, advice string }

func (e effects) refusal() error {
	if len(e.refused) == 0 {
		return nil
	}
	parts := make([]string, len(e.refused))
	for i, f := range e.refused {
		parts[i] = f.field + ": " + f.advice
	}
	return fmt.Errorf("%s", strings.Join(parts, "; "))
}

func (e effects) warnings(r Registration) []string {
	var out []string
	for _, f := range e.noEffect {
		out = append(out, fmt.Sprintf("Registration field %s has no effect for profile %s in %s mode with auth_mode %q; the plan ignores it.", f, r.ProfileID, r.Mode, r.AuthMode))
	}
	if e.allowedNote {
		out = append(out, "Registration field allowed_models is not enforced by the client configuration. It is only checked against the destination's declared model ids when the registration is saved or planned; the destination's own model list decides what is served (an Aiplex access point answers 404 MODEL_NOT_FOUND for a model outside its list, a model surface refuses one outside its allowlist, and any other gateway decides for itself).")
	}
	return out
}

func (r Registration) fieldEffects() effects {
	set := map[string]bool{"project_id": r.ProjectID != "", "location": r.Location != "", "model_id": r.ModelID != "", "allowed_models": len(r.AllowedModels) > 0, "credential_env": r.CredentialEnv != "", "credential_ref": r.CredentialRef != "", "managed": r.Managed}
	var e effects
	none := func(names ...string) {
		for _, n := range names {
			if set[n] {
				e.noEffect = append(e.noEffect, n)
			}
		}
	}
	refuse := func(name, advice string) {
		if set[name] {
			e.refused = append(e.refused, fieldRefusal{name, advice})
		}
	}
	noManaged := fmt.Sprintf("%s has no managed configuration that this plan renders in %s mode, so managed would promise a policy it does not deliver; remove managed", r.ProfileID, r.Mode)
	noAllowed := "in direct mode the client calls Google Cloud itself and no gateway restricts its models, so allowed_models would promise a restriction that does not exist; remove allowed_models, or use a gateway mode and enforce the list at the destination"
	// Antigravity renders allowed_models into its model list, so it is used there.
	e.allowedNote = set["allowed_models"] && r.Mode != "direct" && !strings.HasPrefix(r.ProfileID, "antigravity")
	switch {
	case r.ProfileID == "codex":
		refuse("managed", noManaged)
		if r.Mode == "direct" {
			refuse("allowed_models", noAllowed)
			none("credential_env", "credential_ref")
		} else {
			none("project_id", "location")
			if r.AuthMode == "token-helper" {
				none("credential_env")
			}
		}
	case r.ProfileID == "claude-code":
		switch {
		case r.Mode == "direct":
			refuse("allowed_models", noAllowed)
			none("credential_env", "credential_ref")
		case r.Mode == "claude-apps-gateway":
			none("model_id", "project_id", "location", "credential_env", "credential_ref")
		default:
			if r.Protocol != VertexAnthropic {
				none("project_id", "location")
			}
			if r.AuthMode == "token-helper" { // new in this change: nothing stored can carry these
				refuse("credential_env", "the Claude Code token helper supplies the credential, so credential_env would be unused; remove credential_env")
				refuse("credential_ref", "the Claude Code token helper supplies the credential, so credential_ref would be unused; remove credential_ref")
			}
		}
	case r.ProfileID == "claude-desktop":
		none("model_id", "project_id", "location", "credential_env", "credential_ref") // managed: the plan is already the managed configuration
	case r.ProfileID == "gemini-cli": // new in this change: nothing stored can carry these
		refuse("managed", noManaged)
		refuse("project_id", "Gemini CLI plans use a gateway only; remove project_id")
		refuse("location", "Gemini CLI plans use a gateway only; remove location")
	case strings.HasPrefix(r.ProfileID, "antigravity"):
		if r.Mode == "direct" {
			refuse("allowed_models", noAllowed)
			refuse("managed", noManaged)
			none("model_id", "credential_env", "credential_ref")
			if r.AuthMode == "workforce" {
				none("project_id")
			}
		} else {
			none("project_id", "location")
		}
	}
	return e
}

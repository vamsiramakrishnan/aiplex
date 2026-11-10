package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// YAML route backends use the same snake_case names as JSON (model_id, not
// modelid): the manifest used to lose them, so every YAML route failed with
// "invalid backend configuration".
func TestManifestYAMLBackendsKeepTheirFields(t *testing.T) {
	m, err := loadManifest(writeManifest(t, "m.yaml", `
version: v1
routes:
  - model_id: gemini-2.5-flash
    backends:
      - provider: google
        model_id: gemini-2.5-flash
        weight: 80
        enabled: true
    budget:
      max_daily_cost_usd: 10
      alert_threshold_pct: 80
`))
	if err != nil {
		t.Fatal(err)
	}
	b := m.Routes[0].Backends[0]
	if b.ModelID != "gemini-2.5-flash" || b.Weight != 80 || !b.Enabled || m.Routes[0].Budget == nil || m.Routes[0].Budget.MaxDailyCostUSD != 10 {
		t.Fatalf("backend = %+v budget = %+v", b, m.Routes[0].Budget)
	}
	if problems := validateManifest(m); len(problems) != 0 {
		t.Fatal(problems)
	}
}

// validate and apply share one definition: apply names its instances (name),
// not an id, and routes by model_id, not a name.
func TestValidateAcceptsWhatApplyAccepts(t *testing.T) {
	good := `
version: v1
instances:
  - name: KB
    plane: mcplex
    template: kb
agents:
  - client_id: a
    display_name: A
    auth_method: authorization_code
    grant_types: [authorization_code]
    allowed_scopes: [llm:model:m]
    redirect_uris: ["http://localhost:1/cb"]
routes:
  - model_id: m
    backends: [{provider: openai, model_id: m, weight: 100, enabled: true}]
`
	m, err := loadManifest(writeManifest(t, "good.yaml", good))
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateManifest(m); len(problems) != 0 {
		t.Fatalf("valid manifest rejected: %v", problems)
	}
	for name, tc := range map[string]struct{ body, want string }{
		"instance without template":  {"instances: [{name: x, plane: mcplex}]", "instances[0]: missing required field 'template'"},
		"instance without name":      {"instances: [{plane: mcplex, template: t}]", "instances[0]: missing required field 'name'"},
		"bad plane":                  {"instances: [{name: x, plane: nope, template: t}]", "invalid plane"},
		"agent without id":           {"agents: [{display_name: x}]", "client_id"},
		"auth code without redirect": {"agents: [{client_id: a, auth_method: authorization_code}]", "redirect_uris"},
		"redirect on client creds":   {"agents: [{client_id: a, auth_method: client_credentials, redirect_uris: ['https://x/cb']}]", "redirect_uris apply only"},
		"route without model":        {"routes: [{backends: [{provider: p, model_id: m}]}]", "model_id"},
		"route without backends":     {"routes: [{model_id: m}]", "at least one backend"},
		"empty":                      {"version: v1", "at least one of"},
		"wrong version":              {"version: v2\nagents: [{client_id: a}]", "not supported"},
	} {
		m, err := loadManifest(writeManifest(t, "bad.yaml", tc.body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := strings.Join(validateManifest(m), "\n"); !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q not in %q", name, tc.want, got)
		}
	}
	// A field neither command knows is an error, not silently ignored.
	if _, err := loadManifest(writeManifest(t, "typo.yaml", "agents: [{client_id: a, redirects: []}]")); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field accepted: %v", err)
	}
}

// Every manifest shipped in examples/ passes validate.
func TestShippedExampleManifestsAreValid(t *testing.T) {
	for _, rel := range []string{"quickstart.yaml", "quickstart.json", "mcplex-only.yaml", "durable-tape-runtime.yaml", "aiplex-tape-treasury/treasury.yaml"} {
		m, err := loadManifest(filepath.Join("..", "..", "examples", rel))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if problems := validateManifest(m); len(problems) != 0 {
			t.Errorf("%s: %v", rel, problems)
		}
	}
}

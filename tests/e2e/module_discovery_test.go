//go:build e2e

package e2e

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
	"github.com/vamsiramakrishnan/aiplex/pkg/modules"
)

func TestModuleDiscoveryLeadsToWorkingCommandsAndRoutes(t *testing.T) {
	e := start(t, func(o *devstack.Options) { o.Quota = true })
	out := e.mustCLI("modules", "list", "-o", "json").Out
	var descriptors []modules.Descriptor
	if err := json.Unmarshal([]byte(out), &descriptors); err != nil {
		t.Fatal(err)
	}
	table := e.mustCLI("modules", "list", "-o", "wide").Out
	contains(t, table, "CLI", "module list")
	for _, test := range []struct {
		module, prefix, read string
		commands             []string
	}{
		{"mcplex", "/api/v1/mcp", "/auth-types", []string{"mcp", "tools", "tool-auth"}},
		{"llmplex", "/api/v1/llm", "/operations", []string{"llm"}},
		{"a2aplex", "/api/v1/a2a", "/agents", []string{"a2a"}},
		{"skillsplex", "/api/v1/skills", "/servers", []string{"skills"}},
		{"clientplex", "/api/v1/clients", "/profiles", []string{"clients"}},
		{"quotaplex", "/api/v1/quota", "/policy", []string{"quota", "budgets"}},
	} {
		t.Run(test.module, func(t *testing.T) {
			i := slices.IndexFunc(descriptors, func(d modules.Descriptor) bool { return d.Name == test.module })
			if i < 0 || !slices.Contains(descriptors[i].APIPrefixes, test.prefix) {
				t.Fatalf("%s did not advertise %s", test.module, test.prefix)
			}
			if test.module == "quotaplex" {
				for _, capability := range []string{"quota.managed-model", "quota.managed-tool", "quota.managed-agent"} {
					if !slices.Contains(descriptors[i].Capabilities, capability) {
						t.Errorf("discovery omitted supported quota capability %s", capability)
					}
				}
			}
			var names []string
			for _, command := range descriptors[i].Commands {
				names = append(names, command.Name)
				help := e.mustCLI(command.Name, "--help").Out
				contains(t, help, "aiplex "+command.Name+" ", "advertised command help")
			}
			for _, name := range test.commands {
				if !slices.Contains(names, name) {
					t.Errorf("discovery omitted CLI command %s: %v", name, names)
				}
			}
			contains(t, table, strings.Join(names, ", "), "module commands")
			if got := e.do("GET", test.prefix+test.read, "", e.auth()...); got.Status != 200 {
				t.Fatalf("discovered API prefix %s: %d %s", test.prefix, got.Status, got.Body)
			}
		})
	}
}

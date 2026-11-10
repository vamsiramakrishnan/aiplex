package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Terraform warns about, and ignores, a tfvars entry with no matching
// variable. Everything aiplex init writes, and every name the example shows,
// must be declared in deploy/terraform.
func TestInitTFVarsAreDeclared(t *testing.T) {
	files, _ := filepath.Glob("../../deploy/terraform/*.tf")
	declared := map[string]bool{}
	decl := regexp.MustCompile(`(?m)^variable\s+"([a-z0-9_]+)"`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range decl.FindAllStringSubmatch(string(b), -1) {
			declared[m[1]] = true
		}
	}
	if len(declared) == 0 {
		t.Fatal("no Terraform variables found")
	}
	assign := regexp.MustCompile(`(?m)^#?\s*([a-z][a-z0-9_]*)\s*=`)
	example, err := os.ReadFile("../../deploy/terraform/terraform.tfvars.example")
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"aiplex init":              renderTFVars("p", "us-central1", "d.example", "a@example.com", "1", "w.example", "cid", "4", tfvarsExtras{}),
		"terraform.tfvars.example": string(example),
	} {
		for _, m := range assign.FindAllStringSubmatch(content, -1) {
			if !declared[m[1]] {
				t.Errorf("%s sets %s, which deploy/terraform does not declare", name, m[1])
			}
		}
	}
}

// New installs get the dedicated network; re-running init over an existing
// tfvars keeps what it has, because changing a live cluster's network replaces
// the cluster.
func TestInitChoosesDedicatedNetworkOnlyForNewInstalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terraform.tfvars")

	fresh := tfvarsExtrasFor(path)
	out := renderTFVars("p", "us-central1", "d.example", "", "", "", "", "2", fresh)
	if !strings.Contains(out, `cluster_network = "aiplex"`) {
		t.Fatalf("new install should use the dedicated network:\n%s", out)
	}
	if !strings.Contains(out, `# aws_account_id = "123456789012"`) || strings.Contains(out, "\naws_account_id") {
		t.Fatalf("unset identity providers must stay commented out:\n%s", out)
	}

	// An existing file written before cluster_network existed means a cluster
	// on the default network.
	if err := os.WriteFile(path, []byte("project_id = \"p\"\naws_account_id = \"210987654321\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kept := tfvarsExtrasFor(path)
	out = renderTFVars("p", "us-central1", "d.example", "", "", "", "", "2", kept)
	if !strings.Contains(out, `cluster_network = "default"`) || !strings.Contains(out, `aws_account_id = "210987654321"`) {
		t.Fatalf("existing settings were not kept:\n%s", out)
	}
	if err := os.WriteFile(path, []byte("cluster_network = \"aiplex\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := tfvarsExtrasFor(path).ClusterNetwork; got != "aiplex" {
		t.Fatalf("existing aiplex network not kept: %q", got)
	}
}

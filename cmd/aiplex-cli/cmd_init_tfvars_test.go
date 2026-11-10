package main

import (
	"os"
	"path/filepath"
	"regexp"
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
		"aiplex init":              renderTFVars("p", "us-central1", "d.example", "a@example.com", "1", "w.example", "cid", "4"),
		"terraform.tfvars.example": string(example),
	} {
		for _, m := range assign.FindAllStringSubmatch(content, -1) {
			if !declared[m[1]] {
				t.Errorf("%s sets %s, which deploy/terraform does not declare", name, m[1])
			}
		}
	}
}

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestModuleCommandRejectsDisabledBeforeOperation(t *testing.T) {
	operation := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/modules" {
			t.Errorf("unexpected operation %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode([]aiplex.ModuleDescriptor{})
	}))
	defer server.Close()
	t.Setenv("AIPLEX_URL", server.URL)
	oldURL := apiURL
	apiURL = ""
	t.Cleanup(func() { apiURL = oldURL })
	cmd := &cobra.Command{Use: "operation", RunE: func(*cobra.Command, []string) error { operation = true; return nil }}
	guardModuleCommand(cmd, "llmplex")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not enabled") || operation {
		t.Fatalf("must fail before operation: %v, executed=%v", err, operation)
	}
}

func TestModuleFactoriesHaveGuardsAndHelpIsOffline(t *testing.T) {
	root := &cobra.Command{Use: "aiplex"}
	registerModuleCommands(root)
	for _, name := range []string{"llm", "a2a", "skills"} {
		command, _, err := root.Find([]string{name})
		if err != nil || command == root || command.Annotations["aiplex.module"] == "" {
			t.Fatalf("module %s missing registration", name)
		}
		for _, child := range command.Commands() {
			if child.PreRunE == nil {
				t.Errorf("unguarded child %s", child.Name())
			}
		}
	}
	root.SetArgs([]string{"llm", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

package main

import (
	"testing"
)

func TestClientsCommandsAreOfflineDiscoverable(t *testing.T) {
	command := clientsCmd()
	seen := map[string]bool{}
	for _, child := range command.Commands() {
		seen[child.Name()] = true
	}
	for _, name := range []string{"profiles", "list", "get", "plan", "register", "delete"} {
		if !seen[name] {
			t.Fatal("missing command", name)
		}
	}
	if command.Name() != "clients" {
		t.Fatal(command.Name())
	}
}

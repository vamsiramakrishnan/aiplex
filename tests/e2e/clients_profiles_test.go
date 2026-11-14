//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// The profile list is served by the real API and printed by the real CLI.
func TestClientsProfilesListsGeminiCLI(t *testing.T) {
	e := seeded(t)
	r := e.mustCLI("clients", "profiles")
	if !strings.Contains(r.Out, "gemini-cli") || !strings.Contains(r.Out, "google-genai") {
		t.Fatalf("gemini-cli missing from the client profiles:\n%s", r)
	}
}

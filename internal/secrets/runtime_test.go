package secrets

import "testing"

func TestRuntimeSecretVersionsRejectAliasesAndCrossProjectCoordinates(t *testing.T) {
	manager := &Manager{projectID: "project"}
	for _, test := range []struct {
		name    string
		allowed bool
	}{{"projects/project/secrets/aiplex-tool-session-a/versions/1", true}, {"projects/other/secrets/aiplex-tool-session-a/versions/1", false}, {"projects/project/secrets/aiplex-tool-session-a/versions/latest", false}, {"projects/project/secrets/aiplex-tool-session-a/versions/01", false}, {"projects/project/secrets/aiplex-tool-session-a/versions/0", false}, {"projects/project/secrets/aiplex-tool-session-a/versions/1/../latest", false}, {"projects/project/secrets/aiplex-tool-session-a/versions/-1", false}} {
		if got := manager.exactSecretVersion(test.name); got != test.allowed {
			t.Fatal(test.name, got)
		}
	}
}

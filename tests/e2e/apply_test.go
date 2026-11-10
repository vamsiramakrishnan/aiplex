//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
)

// `aiplex apply` converges: the second run of the same manifest reports every
// object unchanged and writes nothing, a changed route is updated at the
// revision just read, and an agent that differs fails naming the field.
func TestApplyIsIdempotent(t *testing.T) {
	e := start(t)
	// The examples manifest has instances; they need Kubernetes, so use its
	// agents and routes through a copy without them.
	raw, err := os.ReadFile("../../examples/quickstart.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	from := strings.Index(text, "instances:")
	to := strings.Index(text, "# Register AI agents")
	if from < 0 || to < from {
		t.Fatal("examples/quickstart.yaml layout changed; update this test")
	}
	body := text[:from] + text[to:]
	write := func(name, content string) string {
		path := t.TempDir() + "/" + name
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	path := write("quickstart.yaml", body)

	r := e.mustCLI("apply", "-f", path, "--dry-run")
	contains(t, r.Out, "would create", "dry run plans creation")
	contains(t, r.Out, "Plan (dry run, nothing written): 3 created, 0 updated, 0 unchanged", "dry run summary")
	if r = e.cli("agents", "permissions", "tutor-agent"); r.Code == 0 {
		t.Fatalf("dry run registered the agent: %s", r)
	}

	r = e.mustCLI("apply", "-f", path)
	contains(t, r.Out, "created       agent tutor-agent", "first apply creates the agent")
	contains(t, r.Out, "Summary: 3 created, 0 updated, 0 unchanged, 0 skipped, 0 failed", "first summary")

	r = e.mustCLI("apply", "-f", path)
	contains(t, r.Out, "unchanged     agent tutor-agent", "second apply leaves the agent")
	contains(t, r.Out, "Summary: 0 created, 0 updated, 3 unchanged, 0 skipped, 0 failed", "second summary")

	// Change a route: it is updated in place, then stable again.
	changed := strings.Replace(body, "weight: 80", "weight: 70", 1)
	changed = strings.Replace(changed, "weight: 20", "weight: 30", 1)
	if changed == body {
		t.Fatal("test edit did not change the manifest")
	}
	path2 := write("changed.yaml", changed)
	r = e.mustCLI("apply", "-f", path2)
	contains(t, r.Out, "updated       route gemini-2.5-flash", "changed route is updated")
	contains(t, r.Out, "Summary: 0 created, 1 updated, 2 unchanged, 0 skipped, 0 failed", "update summary")
	r = e.mustCLI("apply", "-f", path2)
	contains(t, r.Out, "0 created, 0 updated, 3 unchanged", "stable after update")

	// An agent cannot be updated in place: the failure names the field and the
	// exit status is non-zero.
	diff := strings.Replace(changed, "- mcp:tools:get_document", "- mcp:tools:other", 1)
	path3 := write("agent.yaml", diff)
	r = e.cli("apply", "-f", path3)
	if r.Code == 0 {
		t.Fatalf("a differing agent should fail: %s", r)
	}
	contains(t, r.Out+r.Err, "allowed_scopes", "failure names the field")
}

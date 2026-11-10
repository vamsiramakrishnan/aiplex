//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectWriteNeverPrintsExistingSecrets(t *testing.T) {
	e := seeded(t)
	settings := filepath.Join(e.home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o755)
	orig := `{"env":{"ANTHROPIC_AUTH_TOKEN":"MARK-e2e-existing-secret-1"},"apiKey":"MARK-e2e-existing-secret-2"}`
	os.WriteFile(settings, []byte(orig), 0o600)

	for _, args := range [][]string{
		{"connect", "local", "--client", "claude-code", "--write", "--dry-run"},
		{"connect", "local", "--client", "claude-code", "--write", "--yes"},
		{"connect", "--undo", "--client", "claude-code", "--dry-run"},
		{"connect", "--undo", "--client", "claude-code", "--yes"},
	} {
		r := e.mustCLI(args...)
		notContains(t, r.Out+r.Err, "MARK-e2e-existing-secret", strings.Join(args, " "))
		notContains(t, r.Out+r.Err, e.stack.Token, strings.Join(args, " "))
	}
	if fileText(t, settings) != orig {
		t.Errorf("undo must restore the original bytes:\n%s", fileText(t, settings))
	}
}

func TestConnectTokenHelperUsesTheSelectedContext(t *testing.T) {
	e := seeded(t)
	settings := filepath.Join(e.home, ".claude", "settings.json")

	// Credentials from the environment only: the helper could not work later.
	r := e.cli("connect", "local", "--client", "claude-code", "--write", "--token-helper", "--yes")
	if r.Code != 2 || !strings.Contains(r.Err, "stored") {
		t.Fatalf("want a refusal asking for a stored context:\n%s", r)
	}
	if _, err := os.Stat(settings); err == nil {
		t.Fatal("nothing may be written")
	}

	for _, name := range []string{"prod", "staging"} {
		e.cliEnv(nil, "config", "set-context", name, "--url", e.stack.APIURL)
		if r := e.cliEnv(nil, "login", "--context", name, "--token", e.stack.Token); r.Code != 0 {
			t.Fatal(r)
		}
	}
	run := func(ctx string) result {
		return e.cliEnv(nil, "--context", ctx, "connect", "local", "--client", "claude-code", "--write", "--token-helper", "--yes")
	}
	if r := run("prod"); r.Code != 0 {
		t.Fatal(r)
	}
	contains(t, fileText(t, settings), `"apiKeyHelper": "aiplex --context prod auth print-token"`, "helper names the context")
	notContains(t, fileText(t, settings), e.stack.Token, "settings.json")
	r = run("prod")
	contains(t, r.Out, "Nothing to change.", "same context is idempotent")
	r = run("staging")
	contains(t, r.Out, "staging", "a different context is a visible diff")
	contains(t, fileText(t, settings), "aiplex --context staging auth print-token", "helper follows the context")

	// The helper itself honours --context.
	e.cliEnv(nil, "config", "set-context", "a", "--url", e.stack.APIURL)
	e.cliEnv(nil, "config", "set-context", "b", "--url", e.stack.APIURL)
	e.cliEnv(nil, "login", "--context", "a", "--token", "token-for-context-a")
	e.cliEnv(nil, "login", "--context", "b", "--token", "token-for-context-b")
	e.cliEnv(nil, "config", "use-context", "a")
	if got := strings.TrimSpace(e.cliEnv(nil, "--context", "b", "auth", "print-token").Out); got != "token-for-context-b" {
		t.Errorf("--context b printed %q", got)
	}
	if got := strings.TrimSpace(e.cliEnv(nil, "auth", "print-token").Out); got != "token-for-context-a" {
		t.Errorf("the current context printed %q", got)
	}
}

//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The real `aiplex dev token --issuer branchyard --tenant acme` prints a token
// on stdout and, on stderr, the bind command; the block of
// docs/local-development.md that pipes that command runs as written, and the
// token then authenticates as the bound agent. Flags that do not fit the
// branchyard issuer are refused.
func TestDevBranchyardDocumentedSteps(t *testing.T) {
	sh, run, _ := realDev(t)

	tok, hint, code := run(nil, "dev", "token", "--issuer", "branchyard", "--tenant", "acme")
	tok = strings.TrimSpace(tok)
	if code != 0 || strings.Count(tok, ".") != 2 {
		t.Fatalf("dev token --issuer branchyard: %d %q %s", code, tok, hint)
	}
	notContains(t, hint, tok, "token help prints the token")
	for _, want := range []string{"branchyard:acme", "--preset branchyard", "--input server=http://127.0.0.1:", "--input tenant=acme", "/.well-known/jwks.json"} {
		contains(t, hint, want, "stderr help")
	}
	if out, _, code := run([]string{"AIPLEX_TOKEN=" + tok}, "whoami"); code == 0 {
		t.Fatalf("branchyard token accepted before a binding: %s", out)
	}

	for name, args := range map[string][]string{
		"no tenant":            {"--issuer", "branchyard"},
		"user instead":         {"--issuer", "branchyard", "--user", "alice"},
		"user and tenant":      {"--issuer", "branchyard", "--tenant", "acme", "--user", "alice"},
		"role":                 {"--issuer", "branchyard", "--tenant", "acme", "--role", "admin"},
		"groups":               {"--issuer", "branchyard", "--tenant", "acme", "--groups", "eng"},
		"bad tenant":           {"--issuer", "branchyard", "--tenant", "acme/admin"},
		"tenant with workload": {"--issuer", "workload", "--user", "ci-1", "--tenant", "acme"},
	} {
		if out, errOut, code := run(nil, append([]string{"dev", "token"}, args...)...); code == 0 {
			t.Fatalf("%s: minted %s %s", name, out, errOut)
		}
	}

	raw, err := os.ReadFile(filepath.Join(repoDir, "docs", "local-development.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(raw), "bind it. Binding the agent and using the token:")
	_, block, ok2 := strings.Cut(after, "```sh\n")
	if !ok || !ok2 {
		t.Fatal("branchyard steps not found in docs/local-development.md")
	}
	block, _, _ = strings.Cut(block, "```")
	for _, want := range []string{"aiplex agents register branchyard-agent", "aiplex dev token --issuer branchyard --tenant acme", "whoami"} {
		if !strings.Contains(block, want) {
			t.Fatalf("documented block lacks %q:\n%s", want, block)
		}
	}
	out, code := sh(block)
	if code != 0 {
		t.Fatalf("documented branchyard steps failed (%d):\n%s\n--- block ---\n%s", code, out, block)
	}
	// whoami prints the federated subject; tests/e2e/identity_branchyard_test.go
	// checks the agent over HTTP and in the ledger.
	if !strings.Contains(out, "Identity: branchyard:acme") {
		t.Fatalf("whoami after the documented steps does not show the Branchyard subject:\n%s", out)
	}
	// The token printed before the binding now works too, and is not the developer.
	if out, errOut, code := run([]string{"AIPLEX_TOKEN=" + tok}, "whoami"); code != 0 || !strings.Contains(out, "Identity: branchyard:acme") || !strings.Contains(out, "Roles:    none") {
		t.Fatalf("whoami with the first token after binding: %d %s %s", code, out, errOut)
	}
}

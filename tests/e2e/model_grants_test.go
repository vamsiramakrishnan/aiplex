//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// Admission lets a caller reach an access point; calling a model also needs
// the caller's own grants (the model and the surface that serves it). A 403
// for a missing grant names exactly what is missing and the command to add it.
func TestAdmittedNoRoleCallerNeedsModelGrants(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	bobID := auth.CanonicalPrincipalID(e.stack.IssuerURL, devstack.DevPoolID, "bob")
	bob, err := e.stack.Issuer.Mint(map[string]any{"sub": "bob", "email": "bob@localhost"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	carolID := auth.CanonicalPrincipalID(e.stack.IssuerURL, devstack.DevPoolID, "carol")
	carol, err := e.stack.Issuer.Mint(map[string]any{"sub": "carol", "email": "carol@localhost"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// The harness has no `aiplex dev` state, so admit by principal ID (under
	// `aiplex dev` the email bob@localhost resolves to the same principal).
	e.mustCLI("access-points", "create", "team", "--model", "mock-gpt=local-models", "--allow-users", bobID)
	chat := func(token string) httpResp {
		return e.do("POST", "/ap/team/v1/chat/completions", `{"model":"mock-gpt","messages":[{"role":"user","content":"hi"}]}`,
			"Authorization", "Bearer "+token, "Content-Type", "application/json")
	}

	// Admitted, no grant: refused, and the refusal says what to grant.
	r := chat(bob)
	if r.Status != 403 {
		t.Fatalf("admitted but not granted: %d %s", r.Status, r.Body)
	}
	for _, want := range []string{"admits you", "llm:model:mock-gpt", "llm:surface:local-models:", "--scopes llm:surface:local-models:*,llm:model:mock-gpt", "aiplex agents user-scopes set " + bobID, "permission_error"} {
		contains(t, r.Body, want, "403 for a missing grant")
	}

	// The model grant alone is not enough, and the message now names only the surface.
	e.mustCLI("agents", "user-scopes", "set", bobID, "--scopes", "llm:model:mock-gpt")
	r = chat(bob)
	if r.Status != 403 {
		t.Fatalf("model grant only: %d %s", r.Status, r.Body)
	}
	contains(t, r.Body, "llm:surface:local-models:", "surface still missing")
	notContains(t, r.Body, "missing the grant(s): llm:model", "model grant already held")

	// The command the CLI documents (model plus surface) makes the call work.
	e.mustCLI("agents", "user-scopes", "set", bobID, "--scopes", "llm:model:mock-gpt,llm:surface:local-models:*")
	if r = chat(bob); r.Status != 200 {
		t.Fatalf("admitted and granted: %d %s", r.Status, r.Body)
	}
	contains(t, r.Body, "mock model", "mock reply")

	// Not admitted: refused as before, with the same grants held, and without
	// the missing-grant explanation (it would reveal that the access point exists).
	e.mustCLI("agents", "user-scopes", "set", carolID, "--scopes", "llm:model:mock-gpt,llm:surface:local-models:*")
	r = chat(carol)
	if r.Status != 403 && r.Status != 404 {
		t.Fatalf("not admitted: %d %s", r.Status, r.Body)
	}
	notContains(t, strings.ToLower(r.Body), "admits you", "not-admitted caller told about grants")
	notContains(t, r.Body, "user-scopes", "not-admitted caller told about grants")
}

// `aiplex llm costs` reads the usage ledger: a priced model call shows its
// cost and tokens, never $0.00.
func TestLLMCostsReadsTheLedger(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	if r := e.post("/ap/local/v1/chat/completions", `{"model":"mock-gpt","messages":[{"role":"user","content":"hi"}]}`); r.Status != 200 {
		t.Fatalf("chat: %d %s", r.Status, r.Body)
	}
	out := e.mustCLI("llm", "costs").Out
	notContains(t, out, "$0.00", "llm costs")
	contains(t, out, "Requests:      1", "llm costs")
	notContains(t, out, "Total Tokens:  unknown", "llm costs tokens")
	if usage := e.mustCLI("llm", "usage").Out; !strings.Contains(usage, "mock-gpt") {
		t.Fatalf("llm usage should list the ledger record:\n%s", usage)
	}
}

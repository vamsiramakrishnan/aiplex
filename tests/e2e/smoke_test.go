//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// TestSeededStackServesAChatCompletion is the first-run path: the seeded
// access point answers an OpenAI chat completion from the mock provider and
// the exchange appears in the usage ledger with an estimated cost.
func TestSeededStackServesAChatCompletion(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	seeded, err := e.stack.Seed(ctx, devstack.SeedOptions{})
	if err != nil {
		t.Fatalf("seed: %v\n%s", err, e.log.String())
	}
	r := e.post("/ap/"+seeded.AccessPoint+"/v1/chat/completions", `{"model":"mock-gpt","messages":[{"role":"user","content":"hello there"}]}`)
	if r.Status != 200 {
		t.Fatalf("chat: %d %s", r.Status, r.Body)
	}
	contains(t, r.Body, "Hello from the AIPlex mock model", "chat reply")
	out := e.mustCLI("usage", "records")
	contains(t, out.Out, "mock-gpt", "usage records")
}

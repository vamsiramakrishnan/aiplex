//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// 7. State is in memory: after a restart the CLI must explain what happened
// instead of printing a bare 404, and with the API stopped it must say how
// to start one.
func TestRestartAndStoppedServerErrorsAreClear(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	e.mustCLI("access-points", "get", "local")
	if r := e.post("/ap/local/v1/chat/completions", `{"model":"mock-gpt","messages":[{"role":"user","content":"x"}]}`); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}

	if err := e.stack.Restart(ctx); err != nil {
		t.Fatalf("restart: %v\n%s", err, e.log.String())
	}
	// The token is still valid (same issuer); the state is gone.
	r := e.cli("access-points", "get", "local")
	if r.Code == 0 {
		t.Fatalf("access point survived an in-memory restart: %s", r)
	}
	contains(t, r.Err, "keeps state in memory", "404 after restart explains in-memory state")
	r = e.mustCLI("usage", "records")
	notContains(t, r.Out, "mock-gpt", "usage after restart")
	resp := e.post("/ap/local/v1/chat/completions", `{"model":"mock-gpt","messages":[]}`)
	if resp.Status != 404 {
		t.Fatalf("access point request after restart: %d %s", resp.Status, resp.Body)
	}
	if _, ok := resp.JSON(t)["error"].(map[string]any); !ok {
		t.Fatalf("missing access point not in the OpenAI error shape: %s", resp.Body)
	}

	// Re-seeding works on the restarted API.
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatalf("re-seed after restart: %v", err)
	}

	// Stopped API.
	e.stack.StopAPI()
	r = e.cli("access-points", "list")
	if r.Code == 0 {
		t.Fatalf("list against a stopped API succeeded: %s", r)
	}
	contains(t, r.Err, "aiplex dev", "connection refused hint")
	r = e.cli("dev", "env")
	if r.Code == 0 {
		t.Fatalf("dev env without a running aiplex dev: %s", r)
	}
	contains(t, r.Err, "aiplex dev", "dev env hint")
}

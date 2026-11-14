//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// The delegation history and agent connections through the CLI against the
// real API. No connected cloud agent can be reached locally, so the invoke
// case proves only that the call reaches the server and that the server's
// refusal comes back with the right exit code and without the payload.
func TestA2ADelegationsAndConnections(t *testing.T) {
	e := start(t)

	r := e.mustCLI("a2a", "delegate", "--caller", "tutor-e2e", "--callee", "research-e2e", "--task", "research", "-o", "json")
	var d struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(r.Out), &d); err != nil || d.ID == "" {
		t.Fatalf("delegate: %v\n%s", err, r)
	}

	r = e.mustCLI("a2a", "delegations", "list", "--agent", "tutor-e2e", "--limit", "5")
	if !strings.Contains(r.Out, d.ID) || !strings.Contains(r.Out, "not proof") {
		t.Fatalf("list: %s", r)
	}
	if r = e.mustCLI("a2a", "delegations", "list", "--agent", "nobody-e2e"); !strings.Contains(r.Out, "No delegations for agent nobody-e2e") {
		t.Fatalf("the agent filter must reach the server: %s", r)
	}
	if r = e.cli("a2a", "delegations", "list", "--limit", "0"); r.Code != 2 {
		t.Fatalf("--limit 0 must exit 2: %s", r)
	}
	if r = e.mustCLI("a2a", "delegations", "get", d.ID); !strings.Contains(r.Out, "tutor-e2e") || !strings.Contains(r.Out, "Duration: unknown") {
		t.Fatalf("get: %s", r)
	}
	if r = e.mustCLI("a2a", "delegations", "chain", d.ID); !strings.Contains(r.Out, "tutor-e2e -> research-e2e") {
		t.Fatalf("chain: %s", r)
	}
	if r = e.mustCLI("a2a", "delegations", "update", d.ID, "--status", "failed", "--error", "upstream timeout"); !strings.Contains(r.Out, "-> failed") {
		t.Fatalf("update: %s", r)
	}
	if r = e.mustCLI("a2a", "delegations", "get", d.ID); !strings.Contains(r.Out, "upstream timeout") || strings.Contains(r.Out, "Duration: unknown") {
		t.Fatalf("after update: %s", r)
	}
	// A final record cannot change: the server's 409 is exit code 5.
	if r = e.cli("a2a", "delegations", "update", d.ID, "--status", "completed", "--clear-error"); r.Code != 5 {
		t.Fatalf("a final record must exit 5: %s", r)
	}
	if r = e.cli("a2a", "delegations", "get", "no-such-delegation"); r.Code != 4 {
		t.Fatalf("unknown delegation must exit 4: %s", r)
	}
	if r = e.cli("a2a", "delegations", "get", "../x"); r.Code != 2 {
		t.Fatalf("a malformed ID must exit 2: %s", r)
	}

	// Creating an agent connection needs a configured native identity and a
	// pinned HTTPS cloud origin, which the local stack does not have, so the
	// connection commands are exercised against the empty list and an unknown
	// connection: each still reaches the real API.
	if r = e.mustCLI("a2a", "connections", "list"); !strings.Contains(r.Out, "No agent connections") {
		t.Fatalf("connections list: %s", r)
	}
	if r = e.cli("a2a", "connections", "get", "vertex-e2e"); r.Code != 4 {
		t.Fatalf("an unknown connection must exit 4: %s", r)
	}
	if r = e.cli("a2a", "connections", "delete", "vertex-e2e", "--yes"); r.Code != 4 {
		t.Fatalf("deleting an unknown connection must exit 4: %s", r)
	}
	secret := "payload-secret-e2e"
	r = e.cli("a2a", "invoke", "vertex-e2e", "--operation", "invoke", "--payload", `{"input":"`+secret+`"}`)
	if r.Code != 4 || strings.Contains(r.Out+r.Err, secret) {
		t.Fatalf("invoking an unknown connection must reach the server (exit 4) and not echo the payload: %s", r)
	}
}

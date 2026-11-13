//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// `aiplex mcp connections call` sends a real tools/call through the gateway:
// the built-in echo tool answers, and a deny rule added with the CLI makes the
// same call fail as a denial (exit 3) without reaching the tool server.
func TestMCPConnectionsCallThroughGatewayAndPolicyDenial(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}

	ok := e.mustCLI("mcp", "connections", "call", "local-tools", "echo", "--args", `{"text":"hello from the cli"}`)
	contains(t, ok.Out, "hello from the cli", "call prints the tool's text")

	raw := e.mustCLI("mcp", "connections", "call", "local-tools", "echo", "--args", `{"text":"json please"}`, "-o", "json")
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw.Out), &result); err != nil || len(result.Content) != 1 || result.Content[0].Text != "json please" {
		t.Fatalf("-o json: %q %v", raw.Out, err)
	}

	e.mustCLI("mcp", "connections", "rules", "add", "local-tools", "--id", "no-secrets", "--effect", "deny",
		"--when", "has(args.text) && args.text.contains('secret')", "--message", "secret text may not be echoed")

	before := len(e.stack.MCP.CallBodies())
	denied := e.cli("mcp", "connections", "call", "local-tools", "echo", "--args", `{"text":"a secret"}`)
	if denied.Code != 3 {
		t.Fatalf("a denied call must exit 3: %s", denied)
	}
	for _, want := range []string{"denied", "secret text may not be echoed", "no-secrets"} {
		contains(t, denied.Out+denied.Err, want, "denial output")
	}
	if len(e.stack.MCP.CallBodies()) != before {
		t.Fatal("a denied call reached the tool server")
	}
	if strings.Contains(denied.Out+denied.Err, e.stack.Token) {
		t.Fatal("the token was printed")
	}

	// The same connection still serves a call the rule does not match.
	contains(t, e.mustCLI("mcp", "connections", "call", "local-tools", "echo", "--args", `{"text":"fine"}`).Out, "fine", "permitted call")

	// Bad input exits 2 without reaching the server.
	if r := e.cli("mcp", "connections", "call", "local-tools", "echo", "--args", `[1]`); r.Code != 2 {
		t.Fatalf("non-object arguments must exit 2: %s", r)
	}
	if r := e.cli("mcp", "connections", "call", "no-such-connection", "echo"); r.Code != 4 {
		t.Fatalf("an unknown connection must exit 4: %s", r)
	}
}

// `aiplex tool-auth register` reaches the real handler. The seeded local-tools
// connection exists but has no OAuth client profile, so the handler is entered
// and refuses it; an unknown connection is a different refusal (not found).
func TestToolAuthRegisterReachesServer(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	r := e.cli("tool-auth", "register", "local-tools")
	// 502 REGISTRATION_FAILED from the handler's Prepare step: exit 1, with the
	// registration error, which only the handler can produce.
	if r.Code != 1 || !strings.Contains(r.Err, "client profile requires id, name and redirects") {
		t.Fatalf("register on a connection without a client profile: %s", r)
	}
	if r := e.cli("tool-auth", "register", "no-such-connection"); r.Code != 4 {
		t.Fatalf("an unknown connection must exit 4: %s", r)
	}
}

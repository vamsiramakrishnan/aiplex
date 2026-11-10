//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

const policyMarker = "planted-policy-e2e-7d2f"

func (c *mcpClient) rpcData(r rpcResponse) map[string]string {
	c.e.t.Helper()
	if r.Error == nil {
		c.e.t.Fatalf("expected a JSON-RPC error: %s", r.Raw)
	}
	out := map[string]string{}
	_ = json.Unmarshal(r.Error.Data, &out)
	return out
}

// Policy rules on tool calls, through the real binaries: deny by argument,
// allow-list by group, unknown group membership, evaluation errors, list
// filtering, the operator dry run and the ledger.
func TestToolPolicyRulesEndToEnd(t *testing.T) {
	e := start(t)
	open := devstack.ToolConnectionSpec("open", e.stack.MCPURL, devstack.MockTools, nil, nil)
	e.mustCLI("resources", "put", "tool-connection", "open", writeFile(t, "open.json", open), "0")
	strict := devstack.ToolConnectionSpec("strict", e.stack.MCPURL, devstack.MockTools, nil, nil)
	e.mustCLI("resources", "put", "tool-connection", "strict", writeFile(t, "strict.json", strict), "0")
	e.mustCLI("access-points", "apply", "-f", writeFile(t, "ap.json", map[string]any{"id": "tools", "caller_kinds": []string{"user"}, "subjects": map[string]any{},
		"tools": []map[string]any{{"connection_ref": "open"}, {"connection_ref": "strict"}}}))

	// Rules through the CLI.
	e.mustCLI("mcp", "connections", "rules", "add", "open", "--id", "no-classified", "--effect", "deny",
		"--when", "has(args.text) && args.text.contains('classified')", "--message", "classified text may not be echoed")
	e.mustCLI("mcp", "connections", "rules", "add", "open", "--id", "no-destructive", "--effect", "deny",
		"--when", "has(tool.annotations.destructive) && tool.annotations.destructive", "--message", "destructive tools are blocked")
	e.mustCLI("mcp", "connections", "rules", "add", "open", "--id", "groups", "--effect", "allow",
		"--when", "caller.groups_known && any_in(caller.groups, ['eng', '"+devstack.DevGroup+"'])")
	e.mustCLI("mcp", "connections", "rules", "add", "strict", "--id", "needs-n", "--effect", "deny", "--when", "args.n > 5")

	if r := e.cli("mcp", "connections", "rules", "add", "open", "--id", "bad", "--effect", "deny", "--when", "caller.email == 'x'"); r.Code == 0 || !strings.Contains(r.Err+r.Out, "caller has no field") {
		t.Fatalf("a rule that does not check was saved: %s", r)
	}
	if r := e.cli("mcp", "connections", "rules", "add", "open", "--id", "bad", "--effect", "deny", "--when", "args.x +"); r.Code == 0 {
		t.Fatalf("a rule that does not parse was saved: %s", r)
	}
	list := e.mustCLI("mcp", "connections", "rules", "list", "open")
	for _, want := range []string{"no-classified", "no-destructive", "groups", "allow"} {
		contains(t, list.Out, want, "rules list")
	}
	contains(t, e.mustCLI("mcp", "connections", "get", "open").Out, "Rules:    3", "get shows rules")

	grant := func(sub string, claims map[string]any) string {
		claims["sub"], claims["email"] = sub, sub+"@localhost"
		token, err := e.stack.Issuer.Mint(claims, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		id := auth.CanonicalPrincipalID(e.stack.IssuerURL, devstack.DevPoolID, sub)
		e.mustCLI("agents", "user-scopes", "set", id, "--scopes", "mcp:tools:*")
		return token
	}
	bob := grant("bob", map[string]any{"groups": []string{"eng"}})
	dana := grant("dana", map[string]any{"groups": []string{"sales"}})
	carol := grant("carol", map[string]any{}) // no groups claim: membership unknown
	client := func(token string) *mcpClient {
		c := &mcpClient{e: e, path: "/ap/tools/mcp"}
		if token != "" {
			c.extraHdr = []string{"Authorization", "Bearer " + token}
		}
		c.initialize("2025-06-18")
		return c
	}

	admin := client("")
	t.Run("list hides tools every call would be denied", func(t *testing.T) {
		names := admin.tools()
		for _, want := range []string{"open__echo", "open__add", "strict__echo"} {
			if !has(names, want) {
				t.Fatalf("tools/list lacks %s: %v", want, names)
			}
		}
		if has(names, "open__reset_counter") {
			t.Fatalf("destructive tool listed despite the deny rule: %v", names)
		}
		if !has(names, "strict__reset_counter") {
			t.Fatalf("a connection without that rule hid a tool: %v", names)
		}
	})

	t.Run("deny by argument value", func(t *testing.T) {
		before := len(e.stack.MCP.CallBodies())
		denied := admin.call("open__echo", map[string]any{"text": "classified " + policyMarker})
		data := admin.rpcData(denied)
		if denied.Error.Code != -32001 || data["reason"] != "tool_policy_denied" || data["rule"] != "no-classified" || denied.Error.Message != "classified text may not be echoed" {
			t.Fatalf("denial: %s", denied.Raw)
		}
		if string(denied.ID) == "" || strings.Contains(denied.Raw, policyMarker) {
			t.Fatalf("denial lost the request id or echoed the arguments: %s", denied.Raw)
		}
		if len(e.stack.MCP.CallBodies()) != before {
			t.Fatal("a refused call reached the tool server")
		}
		if got := callText(t, admin.call("open__echo", map[string]any{"text": "fine"})); got != "fine" {
			t.Fatalf("permitted echo = %q", got)
		}
		d := admin.rpcData(admin.call("open__reset_counter", map[string]any{}))
		if d["rule"] != "no-destructive" {
			t.Fatalf("destructive call: %v", d)
		}
	})

	t.Run("allow-list by group, unknown membership is not empty", func(t *testing.T) {
		if got := callText(t, client(bob).call("open__echo", map[string]any{"text": "bob"})); got != "bob" {
			t.Fatalf("eng member = %q", got)
		}
		for name, token := range map[string]string{"sales member": dana, "unknown groups": carol} {
			d := client(token).call("open__echo", map[string]any{"text": "x"})
			data := (&mcpClient{e: e}).rpcData(d)
			if data["reason"] != "tool_policy_denied" || data["rule"] != "" {
				t.Fatalf("%s: %s", name, d.Raw)
			}
		}
	})

	t.Run("evaluation error denies with its own class", func(t *testing.T) {
		d := admin.call("strict__echo", map[string]any{"text": "no n"})
		data := admin.rpcData(d)
		if data["reason"] != "tool_policy_error" || data["rule"] != "needs-n" {
			t.Fatalf("%s", d.Raw)
		}
		if got := callText(t, admin.call("strict__echo", map[string]any{"text": "ok", "n": 1})); got != "ok" {
			t.Fatalf("echo = %q", got)
		}
		if d := admin.rpcData(admin.call("strict__echo", map[string]any{"text": "big", "n": 9})); d["reason"] != "tool_policy_denied" {
			t.Fatalf("deny rule did not match: %v", d)
		}
	})

	t.Run("dry run", func(t *testing.T) {
		r := e.mustCLI("mcp", "connections", "rules", "test", "open", "--tool", "echo", "--args", `{"text":"classified `+policyMarker+`"}`, "--group", "eng")
		contains(t, r.Out, "Decision: deny", "dry run decision")
		contains(t, r.Out, "no-classified", "dry run rule")
		contains(t, r.Out, "Caller sees: classified text may not be echoed", "dry run message")
		r = e.mustCLI("mcp", "connections", "rules", "test", "open", "--tool", "echo", "--args", `{"text":"hi"}`, "--group", "sales", "-o", "json")
		var out struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		_ = json.Unmarshal([]byte(r.Out), &out)
		if out.Decision != "deny" || out.Reason != "no_allow_rule_matched" {
			t.Fatalf("%s", r.Out)
		}
		r = e.mustCLI("mcp", "connections", "rules", "test", "open", "--tool", "echo", "--args", `{"text":"hi"}`, "--groups-unknown")
		contains(t, r.Out, "Decision: deny", "unknown membership")
		// Operators only.
		resp := e.do("POST", "/api/v1/mcp/connections/open/rules/test", `{"tool":"echo","subject":{}}`, "Authorization", "Bearer "+bob, "Content-Type", "application/json")
		if resp.Status != 403 {
			t.Fatalf("a caller without a role ran the dry run: %d %s", resp.Status, resp.Body)
		}
		// The dry run recorded nothing.
		contains(t, e.mustCLI("usage", "records", "--plane", "mcplex", "-o", "json").Out, "tool_policy_denied", "ledger sanity")
	})

	t.Run("ledger holds the class and rule, never arguments", func(t *testing.T) {
		all := e.mustCLI("usage", "records", "--plane", "mcplex", "-o", "json")
		contains(t, all.Out, `"error_class": "tool_policy_denied"`, "denied class")
		contains(t, all.Out, `"error_class": "tool_policy_error"`, "error class")
		contains(t, all.Out, `"tool_policy_rule": "no-classified"`, "rule id")
		contains(t, all.Out, `"tool_policy_rule": "needs-n"`, "error rule id")
		notContains(t, all.Out, policyMarker, "ledger")
		notContains(t, e.log.String(), policyMarker, "API log")
	})

	t.Run("removing rules needs confirmation without a terminal", func(t *testing.T) {
		if r := e.cli("mcp", "connections", "rules", "remove", "open", "no-classified"); r.Code != 2 {
			t.Fatalf("remove without --yes and without a terminal: %s", r)
		}
		e.mustCLI("mcp", "connections", "rules", "remove", "open", "no-classified", "--yes")
		if got := callText(t, admin.call("open__echo", map[string]any{"text": "classified again"})); got != "classified again" {
			t.Fatalf("echo after removal = %q", got)
		}
		if r := e.cli("mcp", "connections", "rules", "remove", "open", "no-classified", "--yes"); r.Code != 4 {
			t.Fatalf("removing a missing rule: %s", r)
		}
	})
}

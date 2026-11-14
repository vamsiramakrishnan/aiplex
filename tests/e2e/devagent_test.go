//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// The seeded agent connection reaches the mock agent runtime on loopback over
// plain http, with a bearer token the stack made up for this run. The call is
// recorded the way a real one is, a delegation record stays reported history,
// and the token is nowhere a user can read it.
func TestDevMockAgentInvocation(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	seeded, err := e.stack.Seed(ctx, devstack.SeedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if seeded.AgentConnection != devstack.SeedAgentConnection || !strings.HasPrefix(e.stack.AgentURL, "http://127.0.0.1:") {
		t.Fatalf("seeded %+v agent %s", seeded, e.stack.AgentURL)
	}
	token := e.stack.Agent.Token
	if len(token) < 20 {
		t.Fatalf("token %q is not a generated token", token)
	}
	var seen []string // everything a user could read, checked for the token at the end
	see := func(rs ...result) {
		for _, r := range rs {
			seen = append(seen, r.Out, r.Err)
		}
	}

	list := e.mustCLI("a2a", "connections", "list")
	get := e.mustCLI("a2a", "connections", "get", "local-agent", "-o", "json")
	see(list, get)
	contains(t, list.Out, "local-agent", "connection listed")
	contains(t, list.Out, e.stack.AgentURL, "endpoint shown")
	contains(t, get.Out, `"identity_ref": "`+devstack.AgentIdentityRef+`"`, "connection names its identity, not a token")

	t.Run("the mock refuses a caller without its token", func(t *testing.T) {
		resp := e.rawPost(e.stack.AgentURL+"/v1/"+devstack.MockAgentResource+":query", `{}`)
		if resp != 401 {
			t.Fatalf("mock agent without a token: %d", resp)
		}
		if e.stack.Agent.Calls() != 0 {
			t.Fatalf("an unauthenticated request was served")
		}
	})

	t.Run("invoke through the CLI and the API", func(t *testing.T) {
		r := e.mustCLI("a2a", "invoke", "local-agent", "--operation", "query", "--payload", `{"input":{"message":"hello"}}`)
		see(r)
		contains(t, r.Out, "mock-agent: you said hello", "reply through the CLI")
		api := e.post("/api/v1/a2a/connections/local-agent/invoke", `{"operation":"query","payload":{"input":{"message":"again"}}}`)
		if api.Status != 200 || !strings.Contains(api.Body, "mock-agent: you said again") {
			t.Fatalf("invoke through the API: %d %s", api.Status, api.Body)
		}
		seen = append(seen, api.Body)
		if got := e.stack.Agent.Calls(); got != 2 {
			t.Fatalf("mock agent served %d calls, want 2", got)
		}
		// The upstream got the caller's payload, and no caller-controlled
		// credential or destination.
		contains(t, strings.Join(e.stack.Agent.Bodies(), "\n"), `"message":"hello"`, "payload reached the agent")
	})

	t.Run("recorded the way a real invocation is", func(t *testing.T) {
		out := e.mustCLI("usage", "records", "--plane", "a2aplex", "-o", "json")
		see(out)
		var doc struct {
			Records []map[string]any `json:"records"`
		}
		if err := json.Unmarshal([]byte(out.Out), &doc); err != nil || len(doc.Records) != 2 {
			t.Fatalf("a2aplex records: %v %s", err, out.Out)
		}
		for _, rec := range doc.Records {
			if rec["plane"] != "a2aplex" || rec["provider"] != "google-agent-runtime" || rec["resource_id"] != "local-agent" || rec["operation"] != "query" || rec["status"] != "ok" || rec["http_status"] != float64(200) {
				t.Fatalf("record: %v", rec)
			}
			if rec["principal_display"] != "developer@localhost" {
				t.Fatalf("caller: %v", rec)
			}
			// The mock reports no token counts or price: they are unknown, not 0.
			if rec["usage"] != nil || rec["cost"].(map[string]any)["basis"] != "unknown" {
				t.Fatalf("usage or cost invented for a mock agent: %v", rec)
			}
		}
		table := e.mustCLI("usage", "records", "--plane", "a2aplex")
		contains(t, table.Out, "local-agent (query)", "default view")
		contains(t, table.Out, "unknown", "unknown counters shown as unknown")
	})

	t.Run("a failed call is recorded as failed", func(t *testing.T) {
		// The mock serves the query wire only; stream-query is a 404 from it.
		r := e.cli("a2a", "invoke", "local-agent", "--operation", "stream-query", "-o", "json")
		see(r)
		out := e.mustCLI("usage", "records", "--plane", "a2aplex", "-o", "json")
		contains(t, out.Out, `"http_status": 404`, "the upstream's status is recorded")
		notContains(t, out.Out, "mock-agent: you said", "record holds no payload or reply")
	})

	t.Run("a reported delegation is reported history, not authorization", func(t *testing.T) {
		bob := e.mustMint(`{"user":"bob"}`)
		invoke := func() httpResp {
			return e.do("POST", "/api/v1/a2a/connections/local-agent/invoke", `{"operation":"query","payload":{}}`, append(bearer(bob.Token), "Content-Type", "application/json")...)
		}
		before := e.stack.Agent.Calls()
		if r := invoke(); r.Status != 403 {
			t.Fatalf("a caller with no grant invoked the agent: %d %s", r.Status, r.Body)
		}
		d := e.mustCLI("a2a", "delegate", "--caller", "bob", "--callee", "local-agent", "--task", "summarise", "-o", "json")
		see(d)
		var rec map[string]any
		if err := json.Unmarshal([]byte(d.Out), &rec); err != nil {
			t.Fatal(err, d.Out)
		}
		if rec["reported_by"] == nil || rec["reported_by"] == "" || rec["reported_at"] == nil {
			t.Fatalf("a delegation is stored with who reported it: %v", rec)
		}
		if _, has := rec["duration_ms"]; has {
			t.Fatalf("a pending delegation shows a duration it does not have: %v", rec)
		}
		if r := invoke(); r.Status != 403 || e.stack.Agent.Calls() != before {
			t.Fatalf("a delegation record authorized an invocation: %d %s (calls %d -> %d)", r.Status, r.Body, before, e.stack.Agent.Calls())
		}
	})

	t.Run("the token is never shown", func(t *testing.T) {
		see(e.mustCLI("usage", "records", "--plane", "a2aplex", "-o", "wide"))
		see(e.mustCLI("a2a", "delegations", "list", "-o", "json"))
		notContains(t, strings.Join(seen, "\n"), token, "CLI and API output")
		notContains(t, e.log.String(), token, "API log")
		// The connections file holds the variable's name, not its value.
		notContains(t, e.log.String(), token, "log")
	})
}

// rawPost sends a POST with no Authorization header to any URL and returns the
// status.
func (e *env) rawPost(url, body string) int {
	e.t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// With --quota the agent connection has a request-count binding, so it is
// invoked rather than refused for having no protected quota binding.
func TestDevMockAgentUnderQuota(t *testing.T) {
	e := start(t, func(o *devstack.Options) { o.Quota = true })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	r := e.mustCLI("a2a", "invoke", "local-agent", "--operation", "query")
	contains(t, r.Out, "mock-agent: hello", "reply under quota")
}

// An agent runtime that refuses the gateway's credential is answered as 502
// with a code that says so (the caller's token was fine), relaying none of the
// runtime's body, and the call is recorded as an upstream authentication failure.
func TestDevAgentRejectingTheGatewayCredential(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	e.stack.Agent.RotateToken("a-different-token-the-api-does-not-hold")
	r := e.post("/api/v1/a2a/connections/local-agent/invoke", `{"operation":"query","payload":{}}`)
	if r.Status != 502 || !strings.Contains(r.Body, "AGENT_CREDENTIAL_REJECTED") || !strings.Contains(r.Body, "local-agent") {
		t.Fatalf("agent refusal: %d %s", r.Status, r.Body)
	}
	notContains(t, r.Body, "missing or invalid bearer token", "the runtime's body is relayed")
	c := e.cli("a2a", "invoke", "local-agent", "--operation", "query")
	if c.Code == 0 {
		t.Fatalf("invoke succeeded: %s", c)
	}
	contains(t, c.Err, "refused the gateway's credential", "message")
	contains(t, c.Err, "Your token was accepted", "hint says the caller's token was fine")
	notContains(t, c.Err, "did not accept your token", "caller blamed")
	recs := e.mustCLI("usage", "records", "--plane", "a2aplex", "-o", "json").Out
	contains(t, recs, `"error_class": "upstream_auth"`, "ledger class")
}

//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// A verified caller with no role (only manual model and tool grants, set by
// the admin) in development mode: it can use an access point that admits it
// and read its own usage, and is refused configuration, other callers' usage
// and access points that do not admit it.
func TestNoRoleCallerSeesOnlyWhatAdmitsIt(t *testing.T) {
	e := start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	bobID := auth.CanonicalPrincipalID(e.stack.IssuerURL, devstack.DevPoolID, "bob")
	bob, err := e.stack.Issuer.Mint(map[string]any{"sub": "bob", "email": "bob@localhost", "groups": []string{"eng"}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// Admin: grant bob the seeded model and tools (Dimension B), add an access
	// point that admits only someone else, and make a request of its own.
	e.mustCLI("agents", "user-scopes", "set", bobID, "--scopes", "llm:model:mock-gpt,llm:surface:local-models:*,mcp:tools:*")
	private := map[string]any{"id": "private", "caller_kinds": []string{"user"}, "subjects": map[string]any{"principals": []string{"principal-someone-else"}},
		"models": []map[string]string{{"name": "mock-gpt", "surface_ref": "local-models", "native_model": "mock-gpt"}}}
	e.mustCLI("access-points", "apply", "-f", writeFile(t, "private.json", private))
	if r := e.post("/ap/local/v1/chat/completions", `{"model":"mock-gpt","messages":[{"role":"user","content":"admin"}]}`); r.Status != 200 {
		t.Fatalf("admin chat: %d %s", r.Status, r.Body)
	}
	as := func(method, path, body string, extra ...string) httpResp {
		return e.do(method, path, body, append([]string{"Authorization", "Bearer " + bob, "Content-Type", "application/json"}, extra...)...)
	}

	t.Run("uses the admitting access point", func(t *testing.T) {
		r := as("POST", "/ap/local/v1/chat/completions", `{"model":"mock-gpt","messages":[{"role":"user","content":"bob here"}]}`)
		if r.Status != 200 {
			t.Fatalf("bob chat: %d %s", r.Status, r.Body)
		}
		c := &mcpClient{e: e, path: "/ap/local/mcp", extraHdr: []string{"Authorization", "Bearer " + bob}}
		c.initialize("2025-06-18")
		if got := callText(t, c.call("local-tools__echo", map[string]any{"text": "bob"})); got != "bob" {
			t.Fatalf("bob echo = %q", got)
		}
	})

	t.Run("filtered access-point view", func(t *testing.T) {
		r := as("GET", "/api/v1/access-points", "")
		if r.Status != 200 {
			t.Fatalf("list: %d %s", r.Status, r.Body)
		}
		contains(t, r.Body, `"local"`, "admitting access point listed")
		notContains(t, r.Body, `"private"`, "non-admitting access point listed")
		notContains(t, r.Body, "surface_ref", "configuration leaked to a no-role caller")
		if r := as("GET", "/api/v1/access-points/private", ""); r.Status != 404 {
			t.Fatalf("non-admitting access point read: %d %s", r.Status, r.Body)
		}
		if r := as("POST", "/ap/private/v1/chat/completions", `{"model":"mock-gpt","messages":[]}`); r.Status != 403 && r.Status != 404 {
			t.Fatalf("non-admitting access point call: %d %s", r.Status, r.Body)
		}
	})

	t.Run("connect for the admitted caller", func(t *testing.T) {
		r := as("GET", "/api/v1/overview/access-points/local/connect", "")
		if r.Status != 200 {
			t.Fatalf("connect: %d %s", r.Status, r.Body)
		}
		contains(t, r.Body, "/ap/local/v1", "connect base URL")
		notContains(t, r.Body, "surface_ref", "operator detail in connect")
		notContains(t, r.Body, bob, "token echoed by connect")
		// The same route as an access point that does not admit bob, and as
		// one that does not exist, answer identically.
		hidden := as("GET", "/api/v1/overview/access-points/private/connect", "")
		missing := as("GET", "/api/v1/overview/access-points/nonexistent/connect", "")
		if hidden.Status != 404 || hidden.Status != missing.Status || hidden.Body != missing.Body {
			t.Fatalf("hidden %d %q vs missing %d %q", hidden.Status, hidden.Body, missing.Status, missing.Body)
		}
		// The rest of the overview stays operator-only.
		if r := as("GET", "/api/v1/overview/access-points/local", ""); r.Status != 403 {
			t.Fatalf("overview detail: %d %s", r.Status, r.Body)
		}
		// And the CLI works for this caller.
		out := e.cliEnv([]string{"AIPLEX_URL=" + e.stack.APIURL, "AIPLEX_TOKEN=" + bob}, "connect", "local", "--client", "claude-code")
		if out.Code != 0 {
			t.Fatalf("aiplex connect as bob: %s", out)
		}
		contains(t, out.Out, "mock-claude", "claude-code config for bob")
	})

	t.Run("own usage only", func(t *testing.T) {
		r := as("GET", "/api/v1/usage/records", "")
		if r.Status != 200 {
			t.Fatalf("own usage: %d %s", r.Status, r.Body)
		}
		var page struct {
			Records []struct {
				PrincipalID string `json:"principal_id"`
			} `json:"records"`
		}
		if err := json.Unmarshal([]byte(r.Body), &page); err != nil || len(page.Records) == 0 {
			t.Fatalf("own usage body: %v %s", err, r.Body)
		}
		for _, rec := range page.Records {
			if rec.PrincipalID != bobID {
				t.Fatalf("bob sees another principal's record: %s", rec.PrincipalID)
			}
		}
		// Through the CLI too.
		out := e.cliEnv([]string{"AIPLEX_URL=" + e.stack.APIURL, "AIPLEX_TOKEN=" + bob}, "usage", "records", "-o", "json")
		if out.Code != 0 || strings.Contains(out.Out, e.stack.Token) {
			t.Fatalf("bob CLI usage: %s", out)
		}
	})

	t.Run("configuration refused", func(t *testing.T) {
		for _, path := range []string{"/api/v1/llm/surfaces/", "/api/v1/mcp/connections/", "/api/v1/iam/role-bindings"} {
			if r := as("GET", path, ""); r.Status != 403 {
				t.Fatalf("GET %s as no-role caller: %d %.200s", path, r.Status, r.Body)
			}
		}
		if r := as("PUT", "/api/v1/access-points/bobs", `{"id":"bobs","caller_kinds":["user"],"subjects":{}}`, "If-Match", "0"); r.Status != 403 {
			t.Fatalf("no-role write: %d %s", r.Status, r.Body)
		}
		out := e.cliEnv([]string{"AIPLEX_URL=" + e.stack.APIURL, "AIPLEX_TOKEN=" + bob}, "llm", "surfaces", "list")
		if out.Code == 0 {
			t.Fatalf("no-role CLI surfaces list: %s", out)
		}
		contains(t, out.Err, "aiplex whoami --server", "403 hint")
	})
}

//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// A managed tool call's usage record carries the response size and the
// gateway-measured duration, with QuotaPlex off (the gateway reports them to
// the ledger) and on (the quota lease's terminal observation carries them).
func TestToolCallRecordsResponseBytesAndDuration(t *testing.T) {
	for name, quota := range map[string]bool{"quotaplex off": false, "quotaplex on": true} {
		t.Run(name, func(t *testing.T) {
			e := start(t, func(o *devstack.Options) { o.Quota = quota })
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
				t.Fatal(err)
			}
			token, err := e.stack.Issuer.Mint(map[string]any{"sub": "toolie", "email": "toolie@localhost", "groups": []string{"eng"}}, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			e.mustCLI("agents", "user-scopes", "set", auth.CanonicalPrincipalID(e.stack.IssuerURL, devstack.DevPoolID, "toolie"), "--scopes", "mcp:tools:*")
			c := &mcpClient{e: e, path: "/ap/local/mcp", extraHdr: []string{"Authorization", "Bearer " + token}}
			c.initialize("2025-06-18")
			if got := callText(t, c.call("local-tools__echo", map[string]any{"text": "measure me"})); got != "measure me" {
				t.Fatalf("echo = %q", got)
			}
			r := e.mustCLI("usage", "records", "-o", "json")
			var page struct {
				Records []struct {
					Operation string           `json:"operation"`
					Usage     map[string]int64 `json:"usage"`
				} `json:"records"`
			}
			if err := json.Unmarshal([]byte(r.Out), &page); err != nil {
				t.Fatalf("%v\n%s", err, r.Out)
			}
			found := false
			for _, rec := range page.Records {
				if rec.Operation != "tools/call" {
					continue
				}
				found = true
				if rec.Usage["tool_response_bytes"] <= 0 || rec.Usage["tool_duration_milliseconds"] < 1 {
					t.Fatalf("tool call usage: %+v", rec.Usage)
				}
				t.Logf("tool call recorded: tool_response_bytes=%d tool_duration_milliseconds=%d", rec.Usage["tool_response_bytes"], rec.Usage["tool_duration_milliseconds"])
			}
			if !found {
				t.Fatalf("no tools/call record:\n%s", r.Out)
			}
			r = e.mustCLI("usage", "records", "--plane", "mcplex", "-o", "wide")
			contains(t, r.Out, "tool-response", "usage records table shows the response size")
		})
	}
}

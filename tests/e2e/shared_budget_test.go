//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// One principal's request budget is shared across independently dispatched planes.
func TestSharedRequestBudgetAcrossModelToolAndAgent(t *testing.T) {
	e, _ := budgetEnv(t)
	e.mustCLI("budgets", "create", "shared", "--unit", "requests", "--amount", "3", "--window", "day", "--group", "aiplex-local-admins")
	if r := e.chat(e.stack.Token, "mock-gpt"); r.Status != 200 {
		t.Fatalf("model: %d %s", r.Status, r.Body)
	}
	c := &mcpClient{e: e, path: "/ap/local/mcp"}
	c.initialize("2025-06-18")
	if got := callText(t, c.call("local-tools__echo", map[string]any{"text": "shared"})); got != "shared" {
		t.Fatalf("tool: %q", got)
	}
	agent := `{"operation":"query","payload":{"input":{"message":"shared"}}}`
	r := e.post("/api/v1/a2a/connections/local-agent/invoke", agent)
	if r.Status != 200 || !strings.Contains(r.Body, "mock-agent: you said shared") {
		t.Fatalf("agent: %d %s", r.Status, r.Body)
	}
	before := e.stack.Agent.Calls()
	denied := e.post("/api/v1/a2a/connections/local-agent/invoke", agent)
	if denied.Status != 429 || !strings.Contains(denied.Body, "QUOTA_BUDGET_EXHAUSTED") || e.stack.Agent.Calls() != before {
		t.Fatalf("agent denial: %d %s; calls %d -> %d", denied.Status, denied.Body, before, e.stack.Agent.Calls())
	}
	out := e.mustCLI("usage", "records", "--budget", "shared", "-o", "json")
	var doc struct {
		Records []struct {
			Plane            string `json:"plane"`
			PrincipalDisplay string `json:"principal_display"`
		} `json:"records"`
	}
	if err := json.Unmarshal([]byte(out.Out), &doc); err != nil {
		t.Fatalf("records: %v %s", err, out.Out)
	}
	planes := map[string]bool{}
	for _, rec := range doc.Records {
		if rec.PrincipalDisplay != "developer@localhost" {
			t.Fatalf("unexpected principal: %+v", rec)
		}
		planes[rec.Plane] = true
	}
	if len(doc.Records) != 3 || !planes["llmplex"] || !planes["mcplex"] || !planes["a2aplex"] {
		t.Fatalf("budget records: %s", out.Out)
	}
}

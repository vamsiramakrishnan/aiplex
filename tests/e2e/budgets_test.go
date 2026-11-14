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

// budgetEnv starts the local stack with QuotaPlex on (generated request-count
// policy plus a placeholder per-request tariff on the primary surface only)
// and returns a caller factory.
func budgetEnv(t *testing.T) (*env, func(sub string, groups ...string) string) {
	t.Helper()
	e := start(t, func(o *devstack.Options) { o.Quota, o.QuotaTariff = true, true })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stack.Seed(ctx, devstack.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	caller := func(sub string, groups ...string) string {
		claims := map[string]any{"sub": sub, "email": sub + "@localhost"}
		if groups != nil {
			claims["groups"] = groups
		}
		token, err := e.stack.Issuer.Mint(claims, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		id := auth.CanonicalPrincipalID(e.stack.IssuerURL, devstack.DevPoolID, sub)
		e.mustCLI("agents", "user-scopes", "set", id, "--scopes", "llm:model:mock-gpt,llm:surface:local-models:*,llm:model:mock-claude,llm:surface:local-messages:*")
		return token
	}
	return e, caller
}

func (e *env) chat(token, model string) httpResp {
	e.t.Helper()
	return e.do("POST", "/ap/local/v1/chat/completions", `{"model":"`+model+`","messages":[{"role":"user","content":"hello budgets"}]}`,
		"Authorization", "Bearer "+token, "Content-Type", "application/json")
}

func (e *env) claude(token string) httpResp {
	e.t.Helper()
	return e.do("POST", "/ap/local/v1/messages", `{"model":"mock-claude","max_tokens":8,"messages":[{"role":"user","content":"hello budgets"}]}`,
		"Authorization", "Bearer "+token, "Content-Type", "application/json", "anthropic-version", "2023-06-01")
}

func (e *env) asCaller(token string, args ...string) result {
	return e.cliEnv([]string{"AIPLEX_URL=" + e.stack.APIURL, "AIPLEX_TOKEN=" + token}, args...)
}

func budgetStatusOf(t *testing.T, r result) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(r.Out), &out); err != nil {
		t.Fatalf("status json: %v\n%s", err, r)
	}
	return out
}

func TestBudgetsTeamTreeUnknownPriceStatusAndLedgerAgree(t *testing.T) {
	e, caller := budgetEnv(t)
	alice, bob := caller("alice", "eng"), caller("bob", "eng")
	carol, dave := caller("carol", "sales"), caller("dave", []string{}...)
	fin, flo := caller("fin", "finance"), caller("flo", "flaggers")

	// A tree: org above the eng and sales teams; two cost budgets beside it.
	e.mustCLI("budgets", "create", "org", "--name", "Whole organisation", "--unit", "requests", "--amount", "6", "--window", "day")
	e.mustCLI("budgets", "create", "eng", "--name", "Engineering team", "--parent", "org", "--unit", "requests", "--amount", "3", "--window", "day", "--group", "eng")
	e.mustCLI("budgets", "create", "sales", "--name", "Sales team", "--parent", "org", "--unit", "requests", "--amount", "10", "--window", "day", "--group", "sales")
	e.mustCLI("budgets", "create", "spend", "--name", "Finance spend", "--unit", "cost", "--currency", "USD", "--amount", "0.004", "--window", "month", "--group", "finance")
	e.mustCLI("budgets", "create", "spend-flag", "--name", "Flagged spend", "--unit", "cost", "--currency", "USD", "--amount", "0.010", "--window", "month", "--group", "flaggers", "--on-unknown-cost", "allow_and_flag")

	// Validation: a rolling window and a cross-currency child are refused.
	if r := e.cli("budgets", "create", "bad", "--unit", "requests", "--amount", "1", "--window", "rolling"); r.Code == 0 {
		t.Fatalf("rolling window accepted: %s", r)
	}
	if r := e.cli("budgets", "create", "bad", "--parent", "spend", "--unit", "cost", "--currency", "EUR", "--amount", "1", "--window", "month"); r.Code == 0 || !strings.Contains(r.Err+r.Out, "currenc") {
		t.Fatalf("cross-currency child accepted: %s", r)
	}

	// Preview says which budgets a request would draw from, and why.
	r := e.mustCLI("budgets", "preview", "--group", "eng", "--access-point", "local")
	contains(t, r.Out, "eng", "preview draws")
	contains(t, r.Out, "via eng", "preview parent")
	contains(t, r.Out, "group:eng", "preview reason")

	// Team budget: two members spend it; the next request, from either member,
	// is refused with the quota error naming the budget.
	for i := 0; i < 2; i++ {
		if resp := e.chat(alice, "mock-gpt"); resp.Status != 200 {
			t.Fatalf("alice %d: %d %s", i, resp.Status, resp.Body)
		}
	}
	if resp := e.chat(bob, "mock-gpt"); resp.Status != 200 {
		t.Fatalf("bob: %d %s", resp.Status, resp.Body)
	}
	for _, token := range []string{alice, bob} {
		resp := e.chat(token, "mock-gpt")
		if resp.Status != 429 {
			t.Fatalf("exhausted team budget: %d %s", resp.Status, resp.Body)
		}
		contains(t, resp.Body, `\"Engineering team\" (eng)`, "refusal names the budget to its member")
		contains(t, resp.Body, "QUOTA_BUDGET_EXHAUSTED", "quota error code")
		if resp.Header.Get("Retry-After") == "" {
			t.Fatalf("a budget refusal says when to retry: %v", resp.Header)
		}
	}

	// Parent exhaustion blocks every child. Carol (sales) spends the rest of org.
	for i := 0; i < 3; i++ {
		if resp := e.chat(carol, "mock-gpt"); resp.Status != 200 {
			t.Fatalf("carol %d: %d %s", i, resp.Status, resp.Body)
		}
	}
	resp := e.chat(carol, "mock-gpt")
	if resp.Status != 429 {
		t.Fatalf("parent exhausted: %d %s", resp.Status, resp.Body)
	}
	contains(t, resp.Body, `a budget above \"Sales team\"`, "refusal names the child, not the parent")
	notContains(t, resp.Body, "Whole organisation", "parent name hidden from a caller who reaches it through a child")
	// A caller in no budget group is unaffected.
	if resp = e.chat(dave, "mock-gpt"); resp.Status != 200 {
		t.Fatalf("non-member refused: %d %s", resp.Status, resp.Body)
	}

	// A token without a verified group view is refused, not treated as empty.
	noGroups := caller("erin")
	if resp = e.chat(noGroups, "mock-gpt"); resp.Status != 503 {
		t.Fatalf("unknown groups: %d %s", resp.Status, resp.Body)
	}
	contains(t, resp.Body, "QUOTA_MEMBERSHIP_UNAVAILABLE", "unknown membership refusal")

	// Cost budget with a model that has no price: refused by default.
	resp = e.claude(fin)
	if resp.Status != 403 {
		t.Fatalf("unpriced model under a cost budget: %d %s", resp.Status, resp.Body)
	}
	contains(t, resp.Body, "QUOTA_BUDGET_COST_UNKNOWN", "unknown price code")
	contains(t, resp.Body, "Finance spend", "unknown price names the budget")
	// A priced model draws the tariff: 0.004 USD is four requests.
	for i := 0; i < 4; i++ {
		if resp = e.chat(fin, "mock-gpt"); resp.Status != 200 {
			t.Fatalf("fin %d: %d %s", i, resp.Status, resp.Body)
		}
	}
	if resp = e.chat(fin, "mock-gpt"); resp.Status != 429 {
		t.Fatalf("cost budget exhausted: %d %s", resp.Status, resp.Body)
	}
	contains(t, resp.Body, "remaining 0.000000 USD", "remaining is shown in the budget's currency")
	// allow_and_flag admits the unpriced model and counts it as unknown cost.
	if resp = e.claude(flo); resp.Status != 200 {
		t.Fatalf("allow_and_flag refused: %d %s", resp.Status, resp.Body)
	}

	// Status: operators see everything; a member sees only its own budget.
	op := budgetStatusOf(t, e.mustCLI("budgets", "status", "eng", "-o", "json"))
	if op["spent"] != "3" || op["remaining"] != "0" || op["limit"] != "3" {
		t.Fatalf("eng status: %v", op)
	}
	orgStatus := budgetStatusOf(t, e.mustCLI("budgets", "status", "org", "-o", "json"))
	if orgStatus["spent"] != "6" || len(orgStatus["children"].([]any)) != 2 {
		t.Fatalf("org status: %v", orgStatus)
	}
	flagStatus := budgetStatusOf(t, e.mustCLI("budgets", "status", "spend-flag", "-o", "json"))
	if flagStatus["unknown_cost_requests"] != float64(1) || flagStatus["spent"] != "0.000000" {
		t.Fatalf("unknown cost is counted, not charged: %v", flagStatus)
	}
	events, _ := op["threshold_events"].([]any)
	if len(events) < 3 {
		t.Fatalf("50, 80 and 100 percent events: %v", op["threshold_events"])
	}
	member := e.asCaller(alice, "budgets", "status", "eng", "-o", "json")
	if member.Code != 0 {
		t.Fatalf("member status: %s", member)
	}
	ms := budgetStatusOf(t, member)
	if ms["spent"] != "3" || ms["children"] != nil || ms["top_spenders"] != nil || ms["unknown_cost_requests"] != nil {
		t.Fatalf("a member sees spent and remaining only: %v", ms)
	}
	for _, args := range [][]string{{"budgets", "status", "sales"}, {"budgets", "status", "spend"}, {"budgets", "list"}, {"budgets", "get", "eng"}} {
		if r := e.asCaller(alice, args...); r.Code == 0 {
			t.Fatalf("%v succeeded for a member of another budget: %s", args, r)
		}
	}
	if r := e.asCaller(dave, "budgets", "status", "eng"); r.Code == 0 {
		t.Fatalf("non-member read a budget: %s", r)
	}

	// The ledger agrees with the counters: summary by budget.
	r = e.mustCLI("usage", "summary", "--by", "budget", "-o", "json")
	var summary struct {
		Groups []struct {
			Key                map[string]string `json:"key"`
			Requests           int64             `json:"requests"`
			UnknownCostRecords int64             `json:"unknown_cost_records"`
			Cost               []struct {
				Currency      string `json:"currency"`
				Basis         string `json:"basis"`
				AmountDecimal string `json:"amount_decimal"`
			} `json:"cost"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(r.Out), &summary); err != nil {
		t.Fatalf("%v\n%s", err, r.Out)
	}
	byBudget := map[string]int{}
	for i, g := range summary.Groups {
		byBudget[g.Key["budget"]] = i
	}
	for budget, want := range map[string]int64{"eng": 3, "sales": 3, "org": 6, "spend": 4, "spend-flag": 1} {
		i, ok := byBudget[budget]
		if !ok || summary.Groups[i].Requests != want {
			t.Fatalf("ledger requests for %s = %v, want %d\n%s", budget, summary.Groups, want, r.Out)
		}
	}
	spend := summary.Groups[byBudget["spend"]]
	if len(spend.Cost) != 1 || spend.Cost[0].AmountDecimal != "0.004000" || spend.Cost[0].Basis != "settled" {
		t.Fatalf("ledger cost for spend: %+v", spend.Cost)
	}
	spendStatus := budgetStatusOf(t, e.mustCLI("budgets", "status", "spend", "-o", "json"))
	if spendStatus["spent"] != spend.Cost[0].AmountDecimal {
		t.Fatalf("budget counter %v and ledger %v disagree", spendStatus["spent"], spend.Cost[0].AmountDecimal)
	}
	// The budget could not price that request (QuotaPlex has no price for the
	// surface), so it counts it as unknown cost and charges nothing. The ledger
	// prices from its own price resources: it may still hold an estimate, and
	// it labels that basis "estimated", never "settled".
	if flagGroup := summary.Groups[byBudget["spend-flag"]]; flagGroup.Requests != 1 || (len(flagGroup.Cost) > 0 && flagGroup.Cost[0].Basis == "settled") {
		t.Fatalf("flagged request in the ledger: %+v", flagGroup)
	}
	r = e.mustCLI("usage", "records", "--budget", "eng", "-o", "json")
	if strings.Count(r.Out, `"budget_ids"`) != 3 {
		t.Fatalf("records filtered by budget:\n%s", r.Out)
	}

	// who shows the budgets that apply to a subject.
	who := e.mustCLI("who", "user", auth.CanonicalPrincipalID(e.stack.IssuerURL, devstack.DevPoolID, "alice"))
	contains(t, who.Out, "Budgets", "who lists budgets")

	// Deleting a parent with children is refused.
	if r := e.cli("budgets", "delete", "org", "--yes"); r.Code == 0 {
		t.Fatalf("parent with children deleted: %s", r)
	}
}

// A cost budget counts tool calls only when it sets include_tool_calls. The
// local tariff prices a model call at 0.001 USD and a tool call at 0.002 USD.
func TestBudgetsCostCountsToolCallsOnlyWhenOptedIn(t *testing.T) {
	e, caller := budgetEnv(t)
	tia, mo := caller("tia", "toolers"), caller("mo", "modelers")
	for _, sub := range []string{"tia", "mo"} {
		id := auth.CanonicalPrincipalID(e.stack.IssuerURL, devstack.DevPoolID, sub)
		e.mustCLI("agents", "user-scopes", "set", id, "--scopes", "llm:model:mock-gpt,llm:surface:local-models:*,mcp:tools:*")
	}
	e.mustCLI("budgets", "create", "tool-spend", "--name", "Tool spend", "--unit", "cost", "--currency", "USD", "--amount", "0.005", "--window", "month", "--group", "toolers", "--include-tool-calls")
	e.mustCLI("budgets", "create", "model-spend", "--name", "Model spend", "--unit", "cost", "--currency", "USD", "--amount", "0.001", "--window", "month", "--group", "modelers")
	if r := e.cli("budgets", "create", "tok", "--unit", "tokens", "--amount", "10", "--window", "day", "--include-tool-calls"); r.Code == 0 || !strings.Contains(r.Err+r.Out, "include_tool_calls") {
		t.Fatalf("a token budget accepted include_tool_calls: %s", r)
	}
	r := e.mustCLI("budgets", "preview", "--group", "toolers", "--group", "modelers", "--plane", "mcplex")
	contains(t, r.Out, "tool-spend", "opted-in budget draws on the tool plane")
	contains(t, r.Out, "Skipped model-spend", "budget that did not opt in is skipped on the tool plane")

	mcp := func(token string) *mcpClient {
		c := &mcpClient{e: e, path: "/ap/local/mcp", extraHdr: []string{"Authorization", "Bearer " + token}}
		c.initialize("2025-06-18")
		return c
	}
	called := func() int { return len(e.stack.MCP.CallBodies()) }

	// Not opted in: tool calls pass through a 0.001 USD model budget untouched.
	moTools := mcp(mo)
	for i := 0; i < 3; i++ {
		if got := callText(t, moTools.call("local-tools__echo", map[string]any{"text": "mo"})); got != "mo" {
			t.Fatalf("mo echo %d = %q", i, got)
		}
	}
	if resp := e.chat(mo, "mock-gpt"); resp.Status != 200 {
		t.Fatalf("mo chat: %d %s", resp.Status, resp.Body)
	}
	if resp := e.chat(mo, "mock-gpt"); resp.Status != 429 {
		t.Fatalf("model-spend still counts model calls: %d %s", resp.Status, resp.Body)
	}
	if st := budgetStatusOf(t, e.mustCLI("budgets", "status", "model-spend", "-o", "json")); st["spent"] != "0.001000" || st["include_tool_calls"] != nil {
		t.Fatalf("model-spend status: %v", st)
	}

	// Opted in: two tool calls (0.004) and a model call (0.001) spend 0.005;
	// the next tool call is refused before it reaches the tool server.
	tiaTools := mcp(tia)
	for i := 0; i < 2; i++ {
		callText(t, tiaTools.call("local-tools__echo", map[string]any{"text": "tia"}))
	}
	if resp := e.chat(tia, "mock-gpt"); resp.Status != 200 {
		t.Fatalf("tia chat: %d %s", resp.Status, resp.Body)
	}
	before := called()
	refused := tiaTools.call("local-tools__echo", map[string]any{"text": "over"})
	if refused.Error == nil && refused.Status == 200 {
		t.Fatalf("exhausted tool budget admitted a call: %s", refused.Raw)
	}
	contains(t, refused.Raw, "is exhausted", "tool refusal says the budget is exhausted")
	if called() != before {
		t.Fatalf("the refused tool call reached the tool server")
	}
	st := budgetStatusOf(t, e.mustCLI("budgets", "status", "tool-spend", "-o", "json"))
	if st["spent"] != "0.005000" || st["reserved"] != "0.000000" || st["include_tool_calls"] != true {
		t.Fatalf("tool-spend status: %v", st)
	}
	t.Logf("tool refusal: status=%d %s", refused.Status, refused.Raw)
	t.Logf("tool-spend status: %v", st)
}

// Local state is in memory: a restart of the API forgets budgets and counters.
func TestBudgetsLocalRestartLosesInMemoryState(t *testing.T) {
	e, caller := budgetEnv(t)
	alice := caller("alice", "eng")
	e.mustCLI("budgets", "create", "eng", "--unit", "requests", "--amount", "1", "--window", "day", "--group", "eng")
	if resp := e.chat(alice, "mock-gpt"); resp.Status != 200 {
		t.Fatalf("%d %s", resp.Status, resp.Body)
	}
	if resp := e.chat(alice, "mock-gpt"); resp.Status != 429 {
		t.Fatalf("budget not enforced: %d %s", resp.Status, resp.Body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := e.stack.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	// The development store holds budgets and counters in memory only.
	r := e.mustCLI("budgets", "list")
	contains(t, r.Out, "No budgets", "budgets after a local restart")
}

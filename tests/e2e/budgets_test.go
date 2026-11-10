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

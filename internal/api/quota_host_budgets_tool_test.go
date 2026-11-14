package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/budgets"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// toolBudgetFixture is a managed tool connection and a model binding on one
// quota host with tenant budgets attached. A tool call costs 2000 micro-USD
// when priced; a model call costs 1000.
type toolBudgetFixture struct {
	h     *ConnectedHandler
	quota *QuotaHost
	now   time.Time
	calls *atomic.Int32
}

func toolBudgetNew(t *testing.T, pricedTools bool, upstream http.HandlerFunc, all ...budgets.Budget) *toolBudgetFixture {
	t.Helper()
	calls := &atomic.Int32{}
	if upstream == nil {
		upstream = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, managedToolResponse)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		upstream(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
	endpoint := server.URL + "/mcp"

	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	config := quotaHostTestConfig(now)
	policy := config.Tenants[0].Policy
	policy.Meters["calls"] = quotaplex.Meter{ID: "calls", Unit: "calls", Billable: true}
	policy.Meters["usd"] = quotaplex.Meter{ID: "usd", Unit: "money", Currency: "USD", Scale: 6}
	policy.PriceBooks = []quotaplex.PriceBook{{ID: "model-tariff", Revision: 1, Match: quotaplex.Match{Plane: "llmplex"}, Prices: []quotaplex.UnitPrice{{MeterID: "requests", MoneyMeterID: "usd", Numerator: 1000, Denominator: 1}}}}
	if pricedTools {
		policy.PriceBooks = append(policy.PriceBooks, quotaplex.PriceBook{ID: "tool-tariff", Revision: 1, Match: quotaplex.Match{Plane: "mcplex"}, Prices: []quotaplex.UnitPrice{{MeterID: "calls", MoneyMeterID: "usd", Numerator: 2000, Denominator: 1}}})
	}
	for i := range policy.Budgets {
		policy.Budgets[i].Limit = 1000
	}
	policy.Concurrency[0].Limit = 1000
	policy.Rates[0].Rate, policy.Rates[0].Burst = 1000, 1000
	config.Tenants[0].Policy = policy
	config.Bindings = append(config.Bindings, QuotaBinding{ID: "tool-search", TenantID: "tenant", Plane: "mcplex", ResourceID: "surface", OperationID: "tools/call:search", Profile: "request-count", FundingAccountID: "team", Provider: "mcp", Meters: map[string]string{"requests": "requests", "tool_calls": "calls"}})
	quota, err := NewQuotaHost(context.Background(), config, quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := budgets.NewTree(all)
	if err != nil {
		t.Fatal(err)
	}
	quota.SetBudgets(&staticBudgets{tree: tree})

	store := resources.NewMemoryStore()
	h := cohesionHandler(t, store, strings.TrimSuffix(endpoint, "/mcp"))
	h.Quota = quota
	cohesionRecord(t, store, "tool-connection", "surface", managedToolConnection(endpoint))
	return &toolBudgetFixture{h: h, quota: quota, now: now, calls: calls}
}

// tool calls the managed tool as sub (in group "engineering") and returns the
// recorder.
func (f *toolBudgetFixture) tool(t *testing.T, sub, key string) *httptest.ResponseRecorder {
	t.Helper()
	return f.toolWithin(t, sub, key, 0)
}

// toolWithin is tool with a caller deadline (0 for none).
func (f *toolBudgetFixture) toolWithin(t *testing.T, sub, key string, deadline time.Duration) *httptest.ResponseRecorder {
	t.Helper()
	identity := quotaHostTestIdentity(t, f.now, func(c map[string]any) { c["sub"] = sub })
	access := &models.ResolvedAccess{Identity: *identity, Scopes: []string{"mcp:tools:search"}}
	f.h.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) {
		copied := *access
		return &copied, nil
	}
	r := trustedConnectedRequest("/api/v1/mcp/connections/surface/invoke", managedToolBody, []string{"mcp:tools:search"})
	ctx := context.WithValue(r.Context(), keyWIFIdentity, identity)
	ctx = context.WithValue(ctx, keyWIFAccess, access)
	r = r.WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	if deadline > 0 {
		c, cancel := context.WithTimeout(r.Context(), deadline)
		defer cancel()
		r = r.WithContext(c)
	}
	w := httptest.NewRecorder()
	f.h.NativeTool(w, r)
	return w
}

// toolAdmission asks the quota host directly to admit a tool call and returns
// the refusal. The managed tool gateway renders a refusal's status and message
// but not its code, so the code is asserted here. An admitted lease is
// aborted, which releases it.
func (f *toolBudgetFixture) toolAdmission(t *testing.T, sub, key string) *execution.StatusError {
	t.Helper()
	identity := quotaHostTestIdentity(t, f.now, func(c map[string]any) { c["sub"] = sub })
	r, in := quotaHostTestInvocation(identity, key, managedToolBody)
	in.Plane, in.OperationID, in.Model = "mcplex", "tools/call:search", ""
	in.Target = quotaplex.Target{Provider: "mcp"}
	lease, err := f.quota.Prepare(r.Context(), r, in)
	if err == nil {
		if err = lease.Abort(r.Context()); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	var failure *execution.StatusError
	if !errors.As(err, &failure) {
		t.Fatalf("not a status error: %v", err)
	}
	return failure
}

// model runs one model call to settlement and returns the admission error.
func (f *toolBudgetFixture) model(t *testing.T, sub, key string) error {
	t.Helper()
	identity := quotaHostTestIdentity(t, f.now, func(c map[string]any) { c["sub"] = sub })
	r, in := quotaHostTestInvocation(identity, key, `{"k":"`+key+`"}`)
	lease, err := f.quota.Prepare(r.Context(), r, in)
	if err != nil {
		return err
	}
	if err = lease.BeforeDispatch(r.Context()); err != nil {
		t.Fatal(err)
	}
	if err = lease.Resource(r.Context(), execution.Resource{Kind: "response", ID: "native-" + key}); err != nil {
		t.Fatal(err)
	}
	if err = lease.Observe(r.Context(), execution.Observation{Source: "upstream", ResourceID: "native-" + key, Counters: map[string]int64{"requests": 1}, Cumulative: true, Terminal: true}); err != nil {
		t.Fatal(err)
	}
	return lease.Finish(r.Context(), execution.Result{StatusCode: 200, ResponseComplete: true, Terminal: true})
}

func (f *toolBudgetFixture) balance(t *testing.T, b budgets.Budget) quotaplex.DrawBalance {
	t.Helper()
	engine := f.quota.Engines()["tenant"]
	scale, err := budgetDrawScale(b, engine.PolicyMeters())
	if err != nil {
		t.Fatal(err)
	}
	draw, err := b.Draw(scale)
	if err != nil {
		t.Fatal(err)
	}
	bal, err := engine.DrawBalance(context.Background(), "tenant", draw, f.now)
	if err != nil {
		t.Fatal(err)
	}
	return bal
}

func toolCostBudget(id, amount string, include bool) budgets.Budget {
	b := costBudget(id, "Tool spend "+id, amount, "", "engineering")
	b.IncludeToolCalls = include
	return b
}

// toolRefusal returns the refusal body; the JSON-RPC error may escape quotes.
func toolRefusal(w *httptest.ResponseRecorder) string {
	var v any
	raw := w.Body.String()
	if json.Unmarshal(w.Body.Bytes(), &v) == nil {
		if b, err := json.Marshal(v); err == nil {
			raw = string(b)
		}
	}
	return strings.ReplaceAll(raw, `\"`, `"`)
}

// The criterion most likely to be missed: a budget that has not opted in is
// untouched by tool calls, whether or not they are priced, whether or not it
// is exhausted.
func TestToolCallsDoNotDrawFromABudgetThatDidNotOptIn(t *testing.T) {
	for _, priced := range []bool{true, false} {
		// A zero cost budget would refuse anything it counts.
		plain := toolCostBudget("plain", "0", false)
		f := toolBudgetNew(t, priced, nil, plain)
		for i, key := range []string{"t1", "t2", "t3"} {
			if w := f.tool(t, "alice", key); w.Code != 200 {
				t.Fatalf("priced=%v call %d: a budget without include_tool_calls refused a tool call: %d %s", priced, i, w.Code, w.Body.String())
			}
		}
		if f.calls.Load() != 3 {
			t.Fatalf("priced=%v: tool server saw %d calls", priced, f.calls.Load())
		}
		if bal := f.balance(t, plain); bal.Consumed != 0 || bal.Held != 0 || bal.UnknownCost != 0 {
			t.Fatalf("priced=%v: tool calls drew from a budget that did not opt in: %+v", priced, bal)
		}
		// It still counts model calls, as before: exhausted at zero.
		if err := f.model(t, "alice", "m1"); err == nil {
			t.Fatalf("priced=%v: the model call should still draw from the cost budget", priced)
		}
	}
}

// A budget record saved before include_tool_calls existed, read from the
// resource store through the same cache the API uses, still ignores tool
// calls and still counts model calls.
func TestStoredOldFormatCostBudgetIgnoresToolCalls(t *testing.T) {
	f := toolBudgetNew(t, true, nil)
	source := auth.MembershipSource("https://quota-identity.test", "people")
	old := `{"id":"legacy","display_name":"Legacy spend","limit":{"unit":"cost","currency":"USD","amount":"0.001"},"window":{"kind":"calendar","unit":"month","time_zone":"UTC"},"members":{"groups":[{"source":"` + source + `","id":"engineering"}]},"on_unknown_cost":"deny","thresholds":[50,100]}`
	if _, err := f.h.Store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: budgets.Kind, ID: "legacy", Spec: json.RawMessage(old)}, 0); err != nil {
		t.Fatal(err)
	}
	f.quota.SetBudgets(&BudgetCache{Store: f.h.Store})
	legacy, err := budgets.Decode("legacy", []byte(old))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a", "b"} {
		if w := f.tool(t, "alice", key); w.Code != 200 {
			t.Fatalf("an old-format budget refused a tool call: %d %s", w.Code, w.Body.String())
		}
	}
	if bal := f.balance(t, legacy); bal.Consumed != 0 || bal.Held != 0 {
		t.Fatalf("tool calls drew from an old-format budget: %+v", bal)
	}
	if err = f.model(t, "alice", "m1"); err != nil {
		t.Fatal(err)
	}
	if bal := f.balance(t, legacy); bal.Consumed != 1000 {
		t.Fatalf("model calls still draw: %+v", bal)
	}
	if err = f.model(t, "alice", "m2"); err == nil {
		t.Fatal("the old budget is still enforced for model calls")
	}
}

// The API refuses include_tool_calls where it means nothing, saves it on a
// cost budget, and reports it in preview and status.
func TestBudgetAPIIncludeToolCallsValidationPreviewAndStatus(t *testing.T) {
	b := budgetAPINew(t, true)
	members := `,"members":{"groups":[` + apGroup("platform-eng") + `]}`
	for _, c := range []struct{ id, unit, amount, why string }{{"r", "requests", "5", "every plane"}, {"k", "tokens", "5", "no token meters"}} {
		w := b.call("PUT", "/api/v1/budgets/"+c.id, budgetJSON(c.id, "", "X", c.unit, c.amount, `,"include_tool_calls":true`))
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"field":"include_tool_calls"`) || !strings.Contains(w.Body.String(), c.why) {
			t.Fatalf("%s budget with include_tool_calls: %d %s", c.unit, w.Code, w.Body.String())
		}
	}
	if w := b.call("PUT", "/api/v1/budgets/tools", budgetJSON("tools", "", "Tool spend", "cost", "5.00", members+`,"include_tool_calls":true`)); w.Code != 200 {
		t.Fatalf("opt-in save: %d %s", w.Code, w.Body.String())
	}
	if w := b.call("PUT", "/api/v1/budgets/models", budgetJSON("models", "", "Model spend", "cost", "5.00", members)); w.Code != 200 {
		t.Fatalf("plain save: %d %s", w.Code, w.Body.String())
	}
	if w := b.call("GET", "/api/v1/budgets/tools", ""); !strings.Contains(w.Body.String(), `"include_tool_calls":true`) {
		t.Fatalf("get: %s", w.Body.String())
	}
	if w := b.call("GET", "/api/v1/budgets/models", ""); strings.Contains(w.Body.String(), "include_tool_calls") {
		t.Fatalf("a budget that did not opt in carries the field: %s", w.Body.String())
	}
	w := b.call("POST", "/api/v1/budgets/preview", `{"subject":{"groups":[`+apGroup("platform-eng")+`]},"plane":"mcplex"}`)
	var out BudgetPreview
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	if len(out.Draws) != 1 || out.Draws[0].ID != "tools" || !out.Draws[0].IncludeToolCalls || len(out.Skipped) != 1 || out.Skipped[0].ID != "models" || !strings.Contains(out.Skipped[0].Reason, "include_tool_calls") {
		t.Fatalf("tool-plane preview: %+v", out)
	}
	w = b.call("GET", "/api/v1/budgets/tools/status", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"include_tool_calls":true`) {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
}

// Exhausted: refused with the budget refusal before the tool server is
// contacted, and nothing is held or consumed.
func TestToolCallOverAnOptedInBudgetIsRefusedBeforeDispatch(t *testing.T) {
	// 0.003 USD covers one tool call (0.002) but not two.
	spend := toolCostBudget("spend", "0.003", true)
	f := toolBudgetNew(t, true, nil, spend)
	if w := f.tool(t, "alice", "first"); w.Code != 200 {
		t.Fatalf("first tool call: %d %s", w.Code, w.Body.String())
	}
	if bal := f.balance(t, spend); bal.Consumed != 2000 || bal.Held != 0 {
		t.Fatalf("a priced tool call draws its cost: %+v", bal)
	}
	w := f.tool(t, "alice", "second")
	raw := toolRefusal(w)
	if w.Code != 429 || !strings.Contains(raw, `budget "Tool spend spend" (spend) is exhausted; remaining 0.001000 USD`) || w.Header().Get("Retry-After") == "" {
		t.Fatalf("exhausted budget: %d %s", w.Code, raw)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("the refused call reached the tool server: %d calls", f.calls.Load())
	}
	if failure := f.toolAdmission(t, "alice", "direct"); failure == nil || failure.Status != 429 || failure.Code != "QUOTA_BUDGET_EXHAUSTED" {
		t.Fatalf("the existing budget refusal code: %+v", failure)
	}
	if bal := f.balance(t, spend); bal.Consumed != 2000 || bal.Held != 0 {
		t.Fatalf("a refused call changed the budget: %+v", bal)
	}
}

// Opted in, no price for the tool: refused with the cost-unknown code, before
// dispatch, never counted as zero.
func TestToolCallWithoutAPriceUnderAnOptedInBudgetIsCostUnknown(t *testing.T) {
	spend := toolCostBudget("spend", "10.00", true)
	f := toolBudgetNew(t, false, nil, spend)
	w := f.tool(t, "alice", "unpriced")
	raw := toolRefusal(w)
	if w.Code != 403 || !strings.Contains(raw, `budget "Tool spend spend" (spend) is a cost budget and this tool call has no price`) {
		t.Fatalf("unpriced tool call: %d %s", w.Code, raw)
	}
	if f.calls.Load() != 0 {
		t.Fatal("an uncostable tool call reached the tool server")
	}
	if failure := f.toolAdmission(t, "alice", "direct"); failure == nil || failure.Status != 403 || failure.Code != "QUOTA_BUDGET_COST_UNKNOWN" {
		t.Fatalf("the existing cost-unknown code: %+v", failure)
	}
	if bal := f.balance(t, spend); bal.Consumed != 0 || bal.Held != 0 || bal.UnknownCost != 0 {
		t.Fatalf("%+v", bal)
	}
	// allow_and_flag still means what it says: admitted, counted as unknown.
	flag := toolCostBudget("flag", "10.00", true)
	flag.OnUnknownCost = budgets.OnUnknownCostAllowAndFlag
	f = toolBudgetNew(t, false, nil, flag)
	if w = f.tool(t, "alice", "flagged"); w.Code != 200 {
		t.Fatalf("allow_and_flag: %d %s", w.Code, w.Body.String())
	}
	if bal := f.balance(t, flag); bal.UnknownCost != 1 || bal.Consumed != 0 || bal.Held != 0 {
		t.Fatalf("%+v", bal)
	}
}

// An outcome that cannot be known keeps its hold against the budget.
func TestToolCallWithUnknownOutcomeKeepsItsBudgetHold(t *testing.T) {
	endings := map[string]struct {
		upstream http.HandlerFunc
		deadline time.Duration
	}{
		// The response starts and is cut short.
		"truncated response": {upstream: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"rpc","result":`)
		}},
		// The request reached the tool; no answer before the caller's deadline.
		"timeout after send": {deadline: 300 * time.Millisecond, upstream: func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
			}
		}},
		// The request reached the tool; the connection is reset before any answer.
		"connection reset after send": {upstream: func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			if tcp, ok := conn.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0)
			}
			_ = conn.Close()
		}},
	}
	for name, ending := range endings {
		t.Run(name, func(t *testing.T) {
			spend := toolCostBudget("spend", "0.003", true)
			f := toolBudgetNew(t, true, ending.upstream, spend)
			w := f.toolWithin(t, "alice", "uncertain", ending.deadline)
			if w.Code == 200 || f.calls.Load() != 1 {
				t.Fatalf("%s: %d %s calls=%d", name, w.Code, w.Body.String(), f.calls.Load())
			}
			info, err := f.quota.Engines()["tenant"].GetAttempt(context.Background(), "tenant", w.Header().Get(execution.AttemptHeader))
			if err != nil || info.Decision.State != "unknown" {
				t.Fatalf("the outcome must be recorded as unknown, not settled or released: %+v %v", info.Decision, err)
			}
			if bal := f.balance(t, spend); bal.Held != 2000 || bal.Consumed != 0 || bal.Available != 1000 {
				t.Fatalf("an unknown outcome must keep its hold, never release it as zero: %+v", bal)
			}
			// The retained hold is enforced: a second call no longer fits.
			if w = f.tool(t, "alice", "after-unknown"); w.Code != 429 || f.calls.Load() != 1 {
				t.Fatalf("the held amount was not enforced: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

// allow_and_flag with a priced model and an unpriced tool, in both orders:
// one counter, the model's cost consumed, the tool call counted unknown, and
// neither call refused.
func TestFlaggedBudgetMixesPricedModelAndUnpricedTool(t *testing.T) {
	for _, order := range []string{"model-first", "tool-first"} {
		t.Run(order, func(t *testing.T) {
			spend := toolCostBudget("spend", "0.010", true)
			spend.OnUnknownCost = budgets.OnUnknownCostAllowAndFlag
			f := toolBudgetNew(t, false, nil, spend)
			steps := []string{"model", "tool", "model", "tool"}
			if order == "tool-first" {
				steps = []string{"tool", "model", "tool", "model"}
			}
			for i, s := range steps {
				key := s + string(rune('a'+i))
				if s == "model" {
					if err := f.model(t, "alice", key); err != nil {
						t.Fatalf("step %d model: %v", i, err)
					}
				} else if w := f.tool(t, "alice", key); w.Code != 200 {
					t.Fatalf("step %d tool: %d %s", i, w.Code, w.Body.String())
				}
			}
			if bal := f.balance(t, spend); bal.Consumed != 2000 || bal.Held != 0 || bal.UnknownCost != 2 {
				t.Fatalf("priced spend consumed, unpriced counted as unknown: %+v", bal)
			}
		})
	}
}

// The same defect without tools: a priced and an unpriced model sharing one
// allow_and_flag budget.
func TestFlaggedBudgetMixesPricedAndUnpricedModels(t *testing.T) {
	for _, order := range []string{"priced-first", "unpriced-first"} {
		t.Run(order, func(t *testing.T) {
			flag := costBudget("flag", "Flagged", "0.010000", budgets.OnUnknownCostAllowAndFlag, "engineering")
			f := budgetHostNew(t, flag)
			resources := []string{"", "surface2", "", "surface2"}
			if order == "unpriced-first" {
				resources = []string{"surface2", "", "surface2", ""}
			}
			for i, res := range resources {
				if _, err := f.call(t, "alice", "engineering", "k"+string(rune('a'+i)), res); err != nil {
					t.Fatalf("step %d (%q): %v", i, res, err)
				}
			}
			if bal := f.balance(t, flag); bal.Consumed != 2000 || bal.Held != 0 || bal.UnknownCost != 2 {
				t.Fatalf("%+v", bal)
			}
		})
	}
}

// Deployment actions run on the tool plane but are not tool calls for a cost
// budget: an opted-in budget that counts a member's tool call neither charges
// nor refuses the same member's deployment action, even when exhausted. A
// request budget still counts the deployment, as before.
func TestDeploymentActionsAreNotCountedByIncludeToolCalls(t *testing.T) {
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer probe.Close()
	d := deploymentAPITestFixture(t, false, probe.URL)
	now := time.Now().UTC()
	config := quotaHostTestConfig(now)
	policy := config.Tenants[0].Policy
	policy.Meters["requests"] = quotaplex.Meter{ID: "requests", Unit: "requests", Billable: true}
	policy.Meters["calls"] = quotaplex.Meter{ID: "calls", Unit: "calls"}
	policy.Meters["usd"] = quotaplex.Meter{ID: "usd", Unit: "money", Currency: "USD", Scale: 6}
	// Every tool-plane call, deployments included, has a price: excluding
	// deployments must not depend on the price book.
	policy.PriceBooks = []quotaplex.PriceBook{{ID: "tool-tariff", Revision: 1, Match: quotaplex.Match{Plane: "mcplex"}, Prices: []quotaplex.UnitPrice{{MeterID: "requests", MoneyMeterID: "usd", Numerator: 2000, Denominator: 1}}}}
	for i := range policy.Budgets {
		policy.Budgets[i].Limit = 1000
	}
	policy.Concurrency[0].Limit = 1000
	policy.Rates[0].Rate, policy.Rates[0].Burst = 1000, 1000
	config.Tenants[0].Policy = policy
	config.Bindings = []QuotaBinding{{ID: "tool-search", TenantID: "tenant", Plane: "mcplex", ResourceID: "surface", OperationID: "tools/call:search", Profile: "request-count", FundingAccountID: "team", Provider: "mcp", Meters: map[string]string{"requests": "requests", "tool_calls": "calls"}}}
	for _, phase := range []string{"plan", "execute", "observe"} {
		config.Bindings = append(config.Bindings, QuotaBinding{ID: phase, TenantID: "tenant", Plane: "mcplex", ResourceID: "network", OperationID: "tools/call:anvil." + phase + ":infra.networks.create", Profile: "request-count", FundingAccountID: "team", Provider: "anvil", Meters: map[string]string{"requests": "requests"}})
	}
	quota, err := NewQuotaHost(context.Background(), config, quotaplex.NewMemoryStore(), time.Now, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// One tool call (0.002 USD) exhausts the cost budget. The owner of the
	// deployment fixture is in group "engineering".
	spend := toolCostBudget("spend", "0.002", true)
	count := reqBudget("count", "", "Every call", "100", "engineering")
	tree, err := budgets.NewTree([]budgets.Budget{spend, count})
	if err != nil {
		t.Fatal(err)
	}
	quota.SetBudgets(&staticBudgets{tree: tree})
	d.h.Quota = quota
	balance := func(b budgets.Budget) quotaplex.DrawBalance {
		t.Helper()
		engine := quota.Engines()["tenant"]
		scale, _ := budgetDrawScale(b, engine.PolicyMeters())
		draw, _ := b.Draw(scale)
		bal, err := engine.DrawBalance(context.Background(), "tenant", draw, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		return bal
	}

	// A tool call by the deployment owner is counted.
	r, in := quotaHostTestInvocation(d.owner, "tool-call", managedToolBody)
	in.Plane, in.OperationID, in.Model, in.Target = "mcplex", "tools/call:search", "", quotaplex.Target{Provider: "mcp"}
	lease, err := quota.Prepare(r.Context(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.BeforeDispatch(r.Context()); err != nil {
		t.Fatal(err)
	}
	if err = lease.Resource(r.Context(), execution.Resource{Kind: "response", ID: "tool-response"}); err != nil {
		t.Fatal(err)
	}
	if err = lease.Observe(r.Context(), execution.Observation{Source: "upstream", ResourceID: "tool-response", Counters: map[string]int64{"requests": 1, "tool_calls": 1}, Cumulative: true, Terminal: true}); err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish(r.Context(), execution.Result{StatusCode: 200, ResponseComplete: true, Terminal: true}); err != nil {
		t.Fatal(err)
	}
	if bal := balance(spend); bal.Consumed != 2000 || bal.Available != 0 {
		t.Fatalf("the tool call must draw from the opted-in budget: %+v", bal)
	}
	// The budget is exhausted for tool calls...
	r, in = quotaHostTestInvocation(d.owner, "tool-call-2", managedToolBody)
	in.Plane, in.OperationID, in.Model, in.Target = "mcplex", "tools/call:search", "", quotaplex.Target{Provider: "mcp"}
	if _, err = quota.Prepare(r.Context(), r, in); err == nil {
		t.Fatal("an exhausted opted-in budget admitted a tool call")
	}
	// ...but the same member's deployment action is neither refused nor charged.
	d.plan(t)
	if bal := balance(spend); bal.Consumed != 2000 || bal.Held != 0 {
		t.Fatalf("a deployment action drew from include_tool_calls: %+v", bal)
	}
	if bal := balance(count); bal.Consumed+bal.Held != 2 {
		t.Fatalf("a request budget still counts the tool call and the deployment action: %+v", bal)
	}
}

// A model call and a tool call draw from the same opted-in budget.
func TestModelAndToolCallsDrawFromOneOptedInBudget(t *testing.T) {
	spend := toolCostBudget("spend", "0.004", true)
	f := toolBudgetNew(t, true, nil, spend)
	if err := f.model(t, "alice", "m1"); err != nil {
		t.Fatal(err)
	}
	if w := f.tool(t, "bob", "t1"); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if bal := f.balance(t, spend); bal.Consumed != 3000 || bal.Available != 1000 {
		t.Fatalf("model 1000 + tool 2000: %+v", bal)
	}
	// 1000 left: a tool call (2000) is refused before dispatch; a model call fits.
	if w := f.tool(t, "alice", "t2"); w.Code != 429 || f.calls.Load() != 1 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if err := f.model(t, "bob", "m2"); err != nil {
		t.Fatal(err)
	}
	err := f.model(t, "alice", "m3")
	if failure := quotaHostTestStatus(t, err, 429); failure.Code != "QUOTA_BUDGET_EXHAUSTED" {
		t.Fatalf("%+v", failure)
	}
	if bal := f.balance(t, spend); bal.Consumed != 4000 || bal.Held != 0 {
		t.Fatalf("%+v", bal)
	}
}

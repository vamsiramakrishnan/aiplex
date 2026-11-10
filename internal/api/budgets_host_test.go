package api

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/pkg/budgets"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

type staticBudgets struct {
	mu   sync.Mutex
	tree *budgets.Tree
}

func (s *staticBudgets) Tree(context.Context, string) (*budgets.Tree, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tree, nil
}

var budgetTestGroup = func(id string) quotaplex.GroupRef {
	return quotaplex.GroupRef{Source: auth.MembershipSource("https://quota-identity.test", "people"), ID: id}
}

func reqBudget(id, parent, name string, limit string, groups ...string) budgets.Budget {
	b := budgets.Budget{ID: id, Parent: parent, DisplayName: name, Limit: budgets.Limit{Unit: budgets.UnitRequests, Amount: limit}, Window: budgets.Window{Kind: budgets.WindowCalendar, Unit: "month", TimeZone: "UTC"}}
	for _, g := range groups {
		b.Members.Groups = append(b.Members.Groups, budgetTestGroup(g))
	}
	return b
}

func costBudget(id, name, amount, onUnknown string, groups ...string) budgets.Budget {
	b := reqBudget(id, "", name, "1", groups...)
	b.Limit = budgets.Limit{Unit: budgets.UnitCost, Currency: "USD", Amount: amount}
	b.OnUnknownCost = onUnknown
	return b
}

type budgetHostFixture struct {
	*quotaHostTestFixture
	source *staticBudgets
}

// budgetHostNew builds a quota host whose policy meters requests, prices them
// per call in USD for surface "surface" only, and binds a second surface the
// price book does not cover.
func budgetHostNew(t *testing.T, all ...budgets.Budget) *budgetHostFixture {
	t.Helper()
	f := &quotaHostTestFixture{now: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), store: quotaplex.NewMemoryStore()}
	f.config = quotaHostTestConfig(f.now)
	policy := f.config.Tenants[0].Policy
	policy.Meters["requests"] = quotaplex.Meter{ID: "requests", Unit: "requests", Billable: true}
	policy.Meters["usd"] = quotaplex.Meter{ID: "usd", Unit: "money", Currency: "USD", Scale: 6}
	policy.PriceBooks = []quotaplex.PriceBook{{ID: "tariff", Revision: 1, Match: quotaplex.Match{ResourceID: "surface"}, Prices: []quotaplex.UnitPrice{{MeterID: "requests", MoneyMeterID: "usd", Numerator: 1000, Denominator: 1}}}}
	for i := range policy.Budgets {
		policy.Budgets[i].Limit = 1000
	}
	policy.Concurrency[0].Limit = 1000
	policy.Rates[0].Rate, policy.Rates[0].Burst = 1000, 1000
	f.config.Tenants[0].Policy = policy
	second := f.config.Bindings[0]
	second.ID, second.ResourceID = "model-call-unpriced", "surface2"
	f.config.Bindings = append(f.config.Bindings, second)
	var err error
	f.host, err = NewQuotaHost(context.Background(), f.config, f.store, func() time.Time { return f.now }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := budgets.NewTree(all)
	if err != nil {
		t.Fatal(err)
	}
	src := &staticBudgets{tree: tree}
	f.host.SetBudgets(src)
	return &budgetHostFixture{quotaHostTestFixture: f, source: src}
}

// complete runs one request to a settled terminal state. It returns the
// admission error, if any.
func (f *budgetHostFixture) call(t *testing.T, sub, group, key, resource string) (string, error) {
	t.Helper()
	identity := quotaHostTestIdentity(t, f.now, func(c map[string]any) {
		c["sub"] = sub
		if group == "" {
			delete(c, "groups")
		} else {
			c["groups"] = []any{group}
		}
	})
	r, in := quotaHostTestInvocation(identity, key, `{"k":"`+key+`"}`)
	if resource != "" {
		in.ResourceID, in.Context.ResourceID, in.Target.ResourceID = resource, resource, resource
	}
	lease, err := f.host.Prepare(r.Context(), r, in)
	if err != nil {
		return "", err
	}
	attempt := quotaHostTestAttempt(t, lease)
	if err = lease.BeforeDispatch(r.Context()); err != nil {
		t.Fatal(err)
	}
	if err = lease.Resource(r.Context(), execution.Resource{Kind: "response", ID: "native-" + key}); err != nil {
		t.Fatal(err)
	}
	if err = lease.Observe(r.Context(), execution.Observation{Source: "upstream", ResourceID: "native-" + key, Counters: map[string]int64{"requests": 1}, Cumulative: true, Terminal: true}); err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish(r.Context(), execution.Result{StatusCode: 200, ResponseComplete: true, Terminal: true}); err != nil {
		t.Fatal(err)
	}
	return attempt, nil
}

func (f *budgetHostFixture) balance(t *testing.T, b budgets.Budget) quotaplex.DrawBalance {
	t.Helper()
	engine := f.host.Engines()["tenant"]
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

func TestBudgetGroupExhaustionNamesTheBudgetToItsMembers(t *testing.T) {
	team := reqBudget("team", "", "Platform team", "2", "engineering")
	f := budgetHostNew(t, team)
	for i, sub := range []string{"alice", "bob"} {
		if _, err := f.call(t, sub, "engineering", "k"+string(rune('a'+i)), ""); err != nil {
			t.Fatalf("member request %d: %v", i, err)
		}
	}
	_, err := f.call(t, "carol", "engineering", "kc", "")
	failure := quotaHostTestStatus(t, err, 429)
	if failure.Code != "QUOTA_BUDGET_EXHAUSTED" || !strings.Contains(failure.Message, `"Platform team" (team)`) || !strings.Contains(failure.Message, "remaining 0 requests") || failure.RetryAfter <= 0 {
		t.Fatalf("the refusal must name the budget to a member and say when it resets: %+v", failure)
	}
	// A caller outside the group draws from nothing and is unaffected.
	if _, err = f.call(t, "dave", "sales", "kd", ""); err != nil {
		t.Fatalf("non-member was refused: %v", err)
	}
	if bal := f.balance(t, team); bal.Consumed != 2 || bal.Held != 0 {
		t.Fatalf("%+v", bal)
	}
}

func TestBudgetParentExhaustionBlocksChildrenWithoutNamingTheParent(t *testing.T) {
	org := reqBudget("org", "", "Whole organisation", "3")
	a := reqBudget("a", "org", "Team A", "10", "alpha")
	b := reqBudget("b", "org", "Team B", "10", "beta")
	f := budgetHostNew(t, org, a, b)
	for i, c := range []struct{ sub, group string }{{"u1", "alpha"}, {"u2", "alpha"}, {"u3", "beta"}} {
		if _, err := f.call(t, c.sub, c.group, "p"+string(rune('a'+i)), ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, group := range []string{"alpha", "beta"} {
		_, err := f.call(t, "u9", group, "blocked-"+group, "")
		failure := quotaHostTestStatus(t, err, 429)
		if failure.Code != "QUOTA_BUDGET_EXHAUSTED" || strings.Contains(failure.Message, "Whole organisation") || strings.Contains(failure.Message, "(org)") || !strings.Contains(failure.Message, "a budget above") {
			t.Fatalf("a caller who reaches a budget only through a child learns the child's name, not the parent's: %+v", failure)
		}
	}
	if bal := f.balance(t, org); bal.Consumed != 3 {
		t.Fatalf("every child request draws from the parent too: %+v", bal)
	}
}

func TestBudgetUnknownGroupMembershipRefusesTheRequest(t *testing.T) {
	f := budgetHostNew(t, reqBudget("team", "", "Team", "100", "engineering"))
	_, err := f.call(t, "alice", "", "unknown-groups", "")
	failure := quotaHostTestStatus(t, err, 503)
	if failure.Code != "QUOTA_MEMBERSHIP_UNAVAILABLE" {
		t.Fatalf("unknown membership is not empty membership; the request must be refused: %+v", failure)
	}
	if bal := f.balance(t, reqBudget("team", "", "Team", "100", "engineering")); bal.Consumed != 0 || bal.Held != 0 {
		t.Fatalf("a refused request touched the budget: %+v", bal)
	}
}

func TestBudgetCostUnknownPriceDeniesByDefaultAndFlagsOnRequest(t *testing.T) {
	deny := costBudget("deny", "Spend cap", "0.005000", "", "engineering")
	flag := costBudget("flag", "Flagged cap", "0.005000", budgets.OnUnknownCostAllowAndFlag, "flaggers")
	f := budgetHostNew(t, deny, flag)
	// Priced surface: each request costs 1000 micro-USD against a 5000 cap.
	for i := 0; i < 5; i++ {
		if _, err := f.call(t, "alice", "engineering", "priced"+string(rune('a'+i)), ""); err != nil {
			t.Fatalf("priced request %d: %v", i, err)
		}
	}
	if bal := f.balance(t, deny); bal.Consumed != 5000 || bal.Available != 0 {
		t.Fatalf("%+v", bal)
	}
	_, err := f.call(t, "alice", "engineering", "priced-over", "")
	if failure := quotaHostTestStatus(t, err, 429); failure.Code != "QUOTA_BUDGET_EXHAUSTED" || !strings.Contains(failure.Message, "0.000000 USD") {
		t.Fatalf("%+v", failure)
	}
	// A surface with no price cannot be costed: refused, not counted as zero.
	_, err = f.call(t, "alice", "engineering", "unpriced", "surface2")
	failure := quotaHostTestStatus(t, err, 403)
	if failure.Code != "QUOTA_BUDGET_COST_UNKNOWN" || !strings.Contains(failure.Message, "Spend cap") {
		t.Fatalf("%+v", failure)
	}
	if bal := f.balance(t, deny); bal.Consumed != 5000 || bal.UnknownCost != 0 {
		t.Fatalf("a refused unpriced request must not count: %+v", bal)
	}
	// allow_and_flag admits it and counts it as unknown cost, not as zero.
	if _, err = f.call(t, "flo", "flaggers", "unpriced-flag", "surface2"); err != nil {
		t.Fatalf("allow_and_flag refused: %v", err)
	}
	if bal := f.balance(t, flag); bal.UnknownCost != 1 || bal.Consumed != 0 || bal.Held != 0 {
		t.Fatalf("%+v", bal)
	}
}

func TestBudgetCostWithoutAMoneyMeterIsNeverZero(t *testing.T) {
	eur := costBudget("eur", "Euro cap", "1.00", "", "engineering")
	eur.Limit.Currency = "EUR"
	f := budgetHostNew(t, eur)
	_, err := f.call(t, "alice", "engineering", "eur", "")
	failure := quotaHostTestStatus(t, err, 403)
	if failure.Code != "QUOTA_BUDGET_UNSUPPORTED_OPERATION" {
		t.Fatalf("a currency the policy cannot meter must refuse, not admit: %+v", failure)
	}
}

func TestBudgetAbortedAttemptDrawsNothingAndSettledAttemptRecordsIDs(t *testing.T) {
	org := reqBudget("org", "", "Org", "10")
	team := reqBudget("team", "org", "Team", "10", "engineering")
	f := budgetHostNew(t, org, team)
	identity := quotaHostTestIdentity(t, f.now, nil)
	r, in := quotaHostTestInvocation(identity, "held", `{}`)
	lease, err := f.host.Prepare(r.Context(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	attempt := quotaHostTestAttempt(t, lease)
	if bal := f.balance(t, team); bal.Held != 1 {
		t.Fatalf("admission reserves the request: %+v", bal)
	}
	// A hook or grant denial, or a cache hit, releases the lease before dispatch.
	if err = lease.Abort(r.Context()); err != nil {
		t.Fatal(err)
	}
	if bal := f.balance(t, team); bal.Held != 0 || bal.Consumed != 0 {
		t.Fatalf("an undispatched attempt must release its hold: %+v", bal)
	}
	if ids := f.host.budgetsDrawn(context.Background(), "tenant", attempt); len(ids) != 0 {
		t.Fatalf("an aborted attempt drew nothing: %v", ids)
	}
	settled, err := f.call(t, "alice", "engineering", "settled", "")
	if err != nil {
		t.Fatal(err)
	}
	if ids := f.host.budgetsDrawn(context.Background(), "tenant", settled); strings.Join(ids, ",") != "org,team" {
		t.Fatalf("the ledger records the leaf and every ancestor: %v", ids)
	}
}

func TestBudgetConcurrentRequestsNeverExceedTheLimit(t *testing.T) {
	team := reqBudget("team", "", "Team", "7", "engineering")
	f := budgetHostNew(t, team)
	var ok, refused atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.call(t, "u"+string(rune('a'+i%20)), "engineering", "race-"+string(rune('a'+i%26))+string(rune('A'+i/26)), "")
			if err == nil {
				ok.Add(1)
			} else {
				refused.Add(1)
			}
		}(i)
	}
	wg.Wait()
	bal := f.balance(t, team)
	if ok.Load() != 7 || refused.Load() != 33 || bal.Consumed != 7 || bal.Held != 0 {
		t.Fatalf("exactly the limit may be admitted: ok=%d refused=%d %+v", ok.Load(), refused.Load(), bal)
	}
}

func TestBudgetFactsComeOnlyFromVerifiedInputs(t *testing.T) {
	identity := quotaHostTestIdentity(t, time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), nil)
	principal := quotaplex.Subject{ID: identity.PrincipalID, Kind: "human", Membership: quotaplex.Membership{Groups: []quotaplex.GroupRef{budgetTestGroup("engineering")}}}
	facts := budgetFacts(principal, nil, identity, quotaplex.Target{AccessPointID: "ide", ClientID: "reg"}, "apollo")
	if facts.PrincipalID != identity.PrincipalID || len(facts.Groups) != 1 || facts.AccessPointID != "ide" || facts.ClientID != "reg" || facts.Project != "apollo" || len(facts.AgentIDs) != 0 {
		t.Fatalf("%+v", facts)
	}
	workload := *identity
	workload.IsWorkforce = false
	if got := budgetFacts(principal, nil, &workload, quotaplex.Target{}, ""); len(got.AgentIDs) != 1 || got.AgentIDs[0] != principal.ID {
		t.Fatalf("a workload principal acting as itself is an agent: %+v", got)
	}
	actor := quotaplex.Subject{ID: "spiffe://agent", Kind: "agent"}
	if got := budgetFacts(principal, &actor, identity, quotaplex.Target{}, ""); len(got.AgentIDs) != 1 || got.AgentIDs[0] != "spiffe://agent" {
		t.Fatalf("%+v", got)
	}
}

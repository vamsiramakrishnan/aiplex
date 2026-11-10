package quotaplex

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func drawPolicy(now time.Time) Policy {
	p := engineFixturePolicy(now)
	p.Budgets, p.Rates, p.Concurrency = nil, nil, nil
	// A control the draws do not depend on, so the policy compiles.
	p.Concurrency = []ConcurrencyRule{{ID: "active", Scope: Scope{Kind: "tenant"}, Limit: 1000}}
	return p
}

func drawPeriod(now time.Time) Period {
	return Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(23 * time.Hour)}
}

func drawEngine(t *testing.T, now *atomic.Int64, policy Policy) *Engine {
	t.Helper()
	clock := func() time.Time { return time.Unix(0, now.Load()).UTC() }
	engine := engineFixture(t, policy, clock, NewMemoryStore())
	engineActivate(t, engine, "")
	return engine
}

func drawSettle(t *testing.T, e *Engine, d Demand, usage Vector, event string) {
	t.Helper()
	claim, err := e.ClaimDispatch(context.Background(), d.Context.TenantID, d.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Settle(context.Background(), Settlement{TenantID: d.Context.TenantID, AttemptID: d.AttemptID, EventID: event, Claim: claim, Usage: usage}); err != nil {
		t.Fatal(err)
	}
}

func TestDrawTokensSumMetersAndDenyNamesBudget(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var now atomic.Int64
	now.Store(base.UnixNano())
	e := drawEngine(t, &now, drawPolicy(base))
	draw := BudgetDraw{ID: "team", Unit: DrawTokens, Limit: 100, Period: drawPeriod(base), Thresholds: []int{50, 100}}
	d1 := engineFixtureDemand(base, "a1", 60)
	d1.Budgets = []BudgetDraw{draw}
	if dec := engineAdmit(t, e, d1); !dec.Allowed {
		t.Fatal(dec)
	}
	d2 := engineFixtureDemand(base, "a2", 50)
	d2.Budgets = []BudgetDraw{draw}
	dec := engineAdmit(t, e, d2)
	if dec.Allowed || dec.Reason != "budget_exhausted" || dec.BudgetID != "team" {
		t.Fatalf("reservation must count held tokens and name the budget: %+v", dec)
	}
	bal, err := e.DrawBalance(context.Background(), "tenant", draw, base)
	if err != nil || bal.Held != 60 || bal.Consumed != 0 || bal.Available != 40 {
		t.Fatalf("%+v %v", bal, err)
	}
	// Settle with less than reserved: the hold is released, only usage counts.
	drawSettle(t, e, d1, Vector{"requests": 1, "tokens": 55}, "e1")
	bal, _ = e.DrawBalance(context.Background(), "tenant", draw, base)
	if bal.Held != 0 || bal.Consumed != 55 || bal.Available != 45 || len(bal.Crossed) != 1 || bal.Crossed[0].Percent != 50 {
		t.Fatalf("settlement/threshold: %+v", bal)
	}
}

func TestDrawRequestsAndAbortReleases(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var now atomic.Int64
	now.Store(base.UnixNano())
	e := drawEngine(t, &now, drawPolicy(base))
	draw := BudgetDraw{ID: "reqs", Unit: DrawRequests, Limit: 2, Period: drawPeriod(base)}
	admit := func(id string) Decision {
		d := engineFixtureDemand(base, id, 1)
		d.Budgets = []BudgetDraw{draw}
		return engineAdmit(t, e, d)
	}
	if !admit("r1").Allowed || !admit("r2").Allowed {
		t.Fatal("first two requests fit")
	}
	if dec := admit("r3"); dec.Allowed || dec.BudgetID != "reqs" {
		t.Fatal(dec)
	}
	// A known-undispatched attempt (hook denial, cache hit) draws nothing.
	if err := e.Abort(context.Background(), "tenant", "r2"); err != nil {
		t.Fatal(err)
	}
	if !admit("r4").Allowed {
		t.Fatal("aborted attempt must release its request")
	}
	bal, _ := e.DrawBalance(context.Background(), "tenant", draw, base)
	if bal.Consumed != 0 || bal.Held != 2 {
		t.Fatalf("%+v", bal)
	}
}

func costPolicy(now time.Time) Policy {
	p := drawPolicy(now)
	// tokens priced at 3/2 micro-dollars; tokens are Billable in the fixture.
	return p
}

func TestDrawCostUnknownPriceDenyAndFlag(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var now atomic.Int64
	now.Store(base.UnixNano())
	policy := costPolicy(base)
	// A second billable meter that the price book does not price.
	policy.Meters["extra"] = Meter{ID: "extra", Unit: "tokens", Billable: true}
	e := drawEngine(t, &now, policy)
	deny := BudgetDraw{ID: "cost-deny", Unit: DrawCost, Currency: "USD", Scale: 6, Limit: 1000, Period: drawPeriod(base)}
	flag := BudgetDraw{ID: "cost-flag", Unit: DrawCost, Currency: "USD", Scale: 6, Limit: 1000, Period: drawPeriod(base), OnUnknownCost: UnknownCostAllowAndFlag}

	// Priced request: tokens bound 100 -> 150 micro-USD held.
	ok := engineFixtureDemand(base, "priced", 100)
	ok.Budgets = []BudgetDraw{deny}
	if dec := engineAdmit(t, e, ok); !dec.Allowed {
		t.Fatal(dec)
	}
	bal, _ := e.DrawBalance(context.Background(), "tenant", deny, base)
	if bal.Held != 150 || bal.Available != 850 {
		t.Fatalf("%+v", bal)
	}

	// Unpriced request (billable meter without a price): denied by default.
	unpriced := engineFixtureDemand(base, "unpriced", 10)
	unpriced.Bounds["extra"] = 5
	unpriced.Budgets = []BudgetDraw{deny}
	dec := engineAdmit(t, e, unpriced)
	if dec.Allowed || dec.Reason != "unpriced_meter" || dec.BudgetID != "cost-deny" {
		t.Fatalf("unknown price must deny, not count as zero: %+v", dec)
	}
	// allow_and_flag admits, holds nothing, and counts the unknown.
	unpriced2 := engineFixtureDemand(base, "unpriced2", 10)
	unpriced2.Bounds["extra"] = 5
	unpriced2.Budgets = []BudgetDraw{flag}
	if dec = engineAdmit(t, e, unpriced2); !dec.Allowed {
		t.Fatal(dec)
	}
	drawSettle(t, e, unpriced2, Vector{"requests": 1, "tokens": 10, "extra": 5}, "ev-flag")
	fb, _ := e.DrawBalance(context.Background(), "tenant", flag, base)
	if fb.UnknownCost != 1 || fb.Consumed != 0 || fb.Held != 0 {
		t.Fatalf("flagged request must be counted unknown and not as zero spend: %+v", fb)
	}
	// Settling the priced request consumes its actual cost.
	drawSettle(t, e, ok, Vector{"requests": 1, "tokens": 80}, "ev-ok")
	bal, _ = e.DrawBalance(context.Background(), "tenant", deny, base)
	if bal.Consumed != 120 || bal.Held != 0 {
		t.Fatalf("%+v", bal)
	}
}

func TestDrawCostWithoutMatchingMoneyMeterIsRefused(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var now atomic.Int64
	now.Store(base.UnixNano())
	e := drawEngine(t, &now, drawPolicy(base))
	d := engineFixtureDemand(base, "eur", 1)
	d.Budgets = []BudgetDraw{{ID: "eur", Unit: DrawCost, Currency: "EUR", Scale: 6, Limit: 5, Period: drawPeriod(base)}}
	if dec := engineAdmit(t, e, d); dec.Allowed || dec.BudgetID != "eur" {
		t.Fatalf("a currency with no money meter cannot be enforced: %+v", dec)
	}
}

func TestDrawTreeParentAndChildAreBothChecked(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var now atomic.Int64
	now.Store(base.UnixNano())
	e := drawEngine(t, &now, drawPolicy(base))
	parent := BudgetDraw{ID: "org", Unit: DrawTokens, Limit: 100, Period: drawPeriod(base)}
	a := BudgetDraw{ID: "team-a", Unit: DrawTokens, Limit: 80, Period: drawPeriod(base)}
	b := BudgetDraw{ID: "team-b", Unit: DrawTokens, Limit: 80, Period: drawPeriod(base)}
	admit := func(id string, tokens int64, leaf BudgetDraw) Decision {
		d := engineFixtureDemand(base, id, tokens)
		d.Budgets = []BudgetDraw{leaf, parent}
		return engineAdmit(t, e, d)
	}
	if !admit("a1", 70, a).Allowed {
		t.Fatal("a1")
	}
	dec := admit("b1", 40, b)
	if dec.Allowed || dec.BudgetID != "org" {
		t.Fatalf("parent exhaustion must block the sibling and name the parent: %+v", dec)
	}
	// Denial commits nothing to the child.
	bb, _ := e.DrawBalance(context.Background(), "tenant", b, base)
	if bb.Held != 0 {
		t.Fatalf("denied request leaked a hold: %+v", bb)
	}
	if dec = admit("a2", 20, a); dec.Allowed || dec.BudgetID != "team-a" {
		t.Fatalf("child limit: %+v", dec)
	}
}

func TestDrawConcurrentAdmissionsNeverExceedLimit(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var now atomic.Int64
	now.Store(base.UnixNano())
	e := drawEngine(t, &now, drawPolicy(base))
	draw := BudgetDraw{ID: "race", Unit: DrawTokens, Limit: 500, Period: drawPeriod(base)}
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := engineFixtureDemand(base, "c"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+string(rune('A'+i/26)), 100)
			d.Budgets = []BudgetDraw{draw}
			dec, err := e.Admit(context.Background(), d)
			if err == nil && dec.Allowed {
				accepted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	bal, _ := e.DrawBalance(context.Background(), "tenant", draw, base)
	if accepted.Load() != 5 || bal.Held != 500 || bal.Held+bal.Consumed > draw.Limit {
		t.Fatalf("held tokens must never exceed the limit: accepted=%d %+v", accepted.Load(), bal)
	}
}

func TestDrawWindowsResetAtBoundary(t *testing.T) {
	base := time.Date(2026, 1, 31, 23, 0, 0, 0, time.UTC)
	var now atomic.Int64
	now.Store(base.UnixNano())
	policy := drawPolicy(base)
	e := drawEngine(t, &now, policy)
	anchor := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	draw := BudgetDraw{ID: "monthly", Unit: DrawRequests, Limit: 1, Period: Period{Kind: "calendar", Anchor: anchor, CalendarUnit: "month", TimeZone: "UTC"}}
	d := engineFixtureDemand(base, "jan", 1)
	d.Budgets = []BudgetDraw{draw}
	if !engineAdmit(t, e, d).Allowed {
		t.Fatal("jan")
	}
	d2 := engineFixtureDemand(base, "jan2", 1)
	d2.Budgets = []BudgetDraw{draw}
	if engineAdmit(t, e, d2).Allowed {
		t.Fatal("limit of one per month")
	}
	now.Store(base.Add(2 * time.Hour).UnixNano()) // 01:00 February
	d3 := engineFixtureDemand(base.Add(2*time.Hour), "feb", 1)
	d3.Budgets = []BudgetDraw{draw}
	if dec := engineAdmit(t, e, d3); !dec.Allowed {
		t.Fatalf("a new calendar window starts with a fresh counter: %+v", dec)
	}
}

func TestValidateDrawRejectsBadShapes(t *testing.T) {
	p := Period{Kind: "fixed", Start: time.Unix(0, 0).UTC().Add(time.Hour), End: time.Unix(0, 0).UTC().Add(2 * time.Hour)}
	for name, d := range map[string]BudgetDraw{
		"unit":      {ID: "x", Unit: "bytes", Period: p},
		"currency":  {ID: "x", Unit: DrawCost, Currency: "usd", Period: p},
		"threshold": {ID: "x", Unit: DrawTokens, Period: p, Thresholds: []int{0}},
		"cur-req":   {ID: "x", Unit: DrawRequests, Currency: "USD", Period: p},
		"negative":  {ID: "x", Unit: DrawTokens, Limit: -1, Period: p},
		"unknown":   {ID: "x", Unit: DrawCost, Currency: "USD", OnUnknownCost: "guess", Period: p},
		"rolling":   {ID: "x", Unit: DrawTokens, Period: Period{Kind: "rolling"}},
	} {
		if ValidateDraw(d) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

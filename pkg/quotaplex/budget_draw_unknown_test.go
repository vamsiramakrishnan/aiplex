package quotaplex

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// A cost budget under allow_and_flag sees priced and unpriced requests in the
// same window. Both must share one counter: priced requests hold and consume
// their cost, unpriced ones are counted as unknown and never as zero spend,
// in whichever order they arrive.

func unknownCostEngine(t *testing.T, base time.Time, store Store) *Engine {
	t.Helper()
	var now atomic.Int64
	now.Store(base.UnixNano())
	policy := costPolicy(base)
	// A billable meter the price book does not price: a request that bounds
	// it cannot be costed.
	policy.Meters["extra"] = Meter{ID: "extra", Unit: "tokens", Billable: true}
	clock := func() time.Time { return time.Unix(0, now.Load()).UTC() }
	e := engineFixture(t, policy, clock, store)
	engineActivate(t, e, "")
	return e
}

func flagDraw(base time.Time) BudgetDraw {
	return BudgetDraw{ID: "team-spend", Unit: DrawCost, Currency: "USD", Scale: 6, Limit: 1000, Period: drawPeriod(base), OnUnknownCost: UnknownCostAllowAndFlag}
}

func pricedDemand(base time.Time, id string, d BudgetDraw) Demand {
	x := engineFixtureDemand(base, id, 100) // 150 micro-USD
	x.Budgets = []BudgetDraw{d}
	return x
}

func unpricedDemand(base time.Time, id string, d BudgetDraw) Demand {
	x := engineFixtureDemand(base, id, 10)
	x.Bounds["extra"] = 5
	x.Budgets = []BudgetDraw{d}
	return x
}

func TestDrawCostFlagSharesOneCounterInEitherOrder(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, order := range []string{"priced-first", "unpriced-first"} {
		t.Run(order, func(t *testing.T) {
			e := unknownCostEngine(t, base, NewMemoryStore())
			d := flagDraw(base)
			steps := []Demand{pricedDemand(base, "p1", d), unpricedDemand(base, "u1", d), pricedDemand(base, "p2", d), unpricedDemand(base, "u2", d)}
			if order == "unpriced-first" {
				steps[0], steps[1], steps[2], steps[3] = steps[1], steps[0], steps[3], steps[2]
			}
			for _, s := range steps {
				if dec := engineAdmit(t, e, s); !dec.Allowed {
					t.Fatalf("%s: %+v", s.AttemptID, dec)
				}
			}
			bal, err := e.DrawBalance(context.Background(), "tenant", d, base)
			if err != nil || bal.Held != 300 || bal.Consumed != 0 || bal.UnknownCost != 2 {
				t.Fatalf("one counter holds both priced requests and counts both unpriced: %+v %v", bal, err)
			}
			for i, s := range steps {
				usage := Vector{"requests": 1, "tokens": 80}
				if _, unpriced := s.Bounds["extra"]; unpriced {
					usage = Vector{"requests": 1, "tokens": 10, "extra": 5}
				}
				drawSettle(t, e, s, usage, "ev-"+s.AttemptID+string(rune('a'+i)))
			}
			bal, _ = e.DrawBalance(context.Background(), "tenant", d, base)
			if bal.Held != 0 || bal.Consumed != 240 || bal.UnknownCost != 2 {
				t.Fatalf("settled: %+v", bal)
			}
		})
	}
}

// With deny, the unpriced request is refused with the cost-unknown reason and
// the priced one on the same budget is unaffected, before and after.
func TestDrawCostDenyRefusesOnlyTheUnpricedRequest(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e := unknownCostEngine(t, base, NewMemoryStore())
	d := flagDraw(base)
	d.OnUnknownCost = UnknownCostDeny
	if dec := engineAdmit(t, e, pricedDemand(base, "p1", d)); !dec.Allowed {
		t.Fatal(dec)
	}
	dec := engineAdmit(t, e, unpricedDemand(base, "u1", d))
	if dec.Allowed || dec.BudgetID != "team-spend" || (dec.Reason != "unpriced_meter" && dec.Reason != "budget_cost_unknown") {
		t.Fatalf("%+v", dec)
	}
	if dec = engineAdmit(t, e, pricedDemand(base, "p2", d)); !dec.Allowed {
		t.Fatalf("a refused unpriced request must not affect priced ones: %+v", dec)
	}
	bal, _ := e.DrawBalance(context.Background(), "tenant", d, base)
	if bal.Held != 300 || bal.UnknownCost != 0 {
		t.Fatalf("%+v", bal)
	}
}

// Counter records written before the fix carry MeterID "" (first written by
// an unpriced request) or the money meter id (first written by a priced
// request). Both must keep loading, keep their counts, and accept both kinds
// of request afterwards.
func TestDrawCostOldStoredCounterShapesStillLoad(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, legacy := range []string{"", "usd"} {
		t.Run("meter_id="+legacy, func(t *testing.T) {
			store := NewMemoryStore()
			e := unknownCostEngine(t, base, store)
			d := flagDraw(base)
			window, err := d.Period.Resolve(base)
			if err != nil {
				t.Fatal(err)
			}
			key := DrawKey(e.PolicyID(), d, window)
			old := budgetRecord{Key: key, RuleID: "budget:" + d.ID, MeterID: legacy, Window: window, Shape: DrawShape(d), Limit: d.Limit}
			if legacy == "" {
				old.UnknownCost = 3
			} else {
				old.Consumed = 400
			}
			if err = store.Update(context.Background(), "tenant", func(tx Tx) error { return tx.Put(budgetKind, key, old) }); err != nil {
				t.Fatal(err)
			}
			for _, s := range []Demand{pricedDemand(base, "p1", d), unpricedDemand(base, "u1", d)} {
				if dec := engineAdmit(t, e, s); !dec.Allowed {
					t.Fatalf("%s on a %q record: %+v", s.AttemptID, legacy, dec)
				}
			}
			bal, _ := e.DrawBalance(context.Background(), "tenant", d, base)
			wantUnknown, wantConsumed := int64(1), int64(0)
			if legacy == "" {
				wantUnknown = 4
			} else {
				wantConsumed = 400
			}
			if bal.Held != 150 || bal.Consumed != wantConsumed || bal.UnknownCost != wantUnknown {
				t.Fatalf("old counts lost: %+v", bal)
			}
			drawSettle(t, e, pricedDemand(base, "p1", d), Vector{"requests": 1, "tokens": 80}, "ev-p1")
			bal, _ = e.DrawBalance(context.Background(), "tenant", d, base)
			if bal.Held != 0 || bal.Consumed != wantConsumed+120 {
				t.Fatalf("%+v", bal)
			}
		})
	}
}

package quotaplex

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestDrawTokensSumEveryDisjointTokenMeter(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var now atomic.Int64
	now.Store(base.UnixNano())
	policy := drawPolicy(base)
	policy.Meters["input"] = Meter{ID: "input", Unit: "tokens"}
	policy.Meters["output"] = Meter{ID: "output", Unit: "tokens"}
	delete(policy.Meters, "tokens")
	policy.PriceBooks = nil
	e := drawEngine(t, &now, policy)
	draw := BudgetDraw{ID: "total", Unit: DrawTokens, Limit: 100, Period: drawPeriod(base)}
	d := engineFixtureDemand(base, "sum", 0)
	d.Bounds = Vector{"requests": 1, "input": 30, "output": 50}
	d.Budgets = []BudgetDraw{draw}
	if dec := engineAdmit(t, e, d); !dec.Allowed {
		t.Fatal(dec)
	}
	if bal, _ := e.DrawBalance(context.Background(), "tenant", draw, base); bal.Held != 80 {
		t.Fatalf("the reservation is the sum of the token meters' bounds: %+v", bal)
	}
	d2 := engineFixtureDemand(base, "sum2", 0)
	d2.Bounds = Vector{"requests": 1, "input": 15, "output": 10}
	d2.Budgets = []BudgetDraw{draw}
	if dec := engineAdmit(t, e, d2); dec.Allowed || dec.BudgetID != "total" {
		t.Fatalf("80 held + 25 exceeds 100: %+v", dec)
	}
	drawSettle(t, e, d, Vector{"requests": 1, "input": 20, "output": 5}, "sum-ev")
	if bal, _ := e.DrawBalance(context.Background(), "tenant", draw, base); bal.Held != 0 || bal.Consumed != 25 {
		t.Fatalf("settlement consumes the observed sum: %+v", bal)
	}
}

func TestDrawCostWithoutAMatchingPriceBookIsUnknown(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var now atomic.Int64
	now.Store(base.UnixNano())
	policy := drawPolicy(base)
	policy.PriceBooks[0].Match = Match{ResourceID: "some-other-model"}
	e := drawEngine(t, &now, policy)
	deny := BudgetDraw{ID: "deny", Unit: DrawCost, Currency: "USD", Scale: 6, Limit: 100, Period: drawPeriod(base)}
	flag := deny
	flag.ID, flag.OnUnknownCost = "flag", UnknownCostAllowAndFlag
	d := engineFixtureDemand(base, "nobook", 5)
	d.Budgets = []BudgetDraw{deny}
	if dec := engineAdmit(t, e, d); dec.Allowed || dec.Reason != "budget_cost_unknown" || dec.BudgetID != "deny" {
		t.Fatalf("no price book under a cost budget must deny: %+v", dec)
	}
	d2 := engineFixtureDemand(base, "nobook-flag", 5)
	d2.Budgets = []BudgetDraw{flag}
	if dec := engineAdmit(t, e, d2); !dec.Allowed {
		t.Fatal(dec)
	}
	if bal, _ := e.DrawBalance(context.Background(), "tenant", flag, base); bal.UnknownCost != 1 || bal.Held != 0 {
		t.Fatalf("%+v", bal)
	}
	// Both draws on one request: the deny budget still refuses it.
	d3 := engineFixtureDemand(base, "both", 5)
	d3.Budgets = []BudgetDraw{flag, deny}
	if dec := engineAdmit(t, e, d3); dec.Allowed || dec.BudgetID != "deny" {
		t.Fatalf("%+v", dec)
	}
	if bal, _ := e.DrawBalance(context.Background(), "tenant", flag, base); bal.UnknownCost != 1 {
		t.Fatalf("a refused request must not be counted by the budget that would have admitted it: %+v", bal)
	}
}

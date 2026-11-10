package usageledger

import (
	"context"
	"testing"
	"time"
)

// Back-estimation only ever writes over an unknown cost: a reported (harness)
// cost and a settled one stay as they are, and a reported record with usage
// but no cost is priced like any other. Summaries keep reported apart and add
// exact values once per basis.
func TestBackfillNeverTouchesReportedOrSettledCosts(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	add := func(id, source string, cost Cost) {
		r := priceRecord("m", &Usage{InputTokens: Int64(37)})
		r.ID, r.Source, r.Cost = id, source, cost
		r.StartedAt, r.FinishedAt = at, at
		if err := store.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	add("reported-cost", SourceReported, Cost{Basis: BasisReported, Currency: "USD", Amount: "5", Scale: 6})
	add("settled", SourceManaged, Cost{Basis: BasisSettled, Currency: "USD", Amount: "7", Scale: 6})
	add("reported-nocost", SourceReported, UnknownCost())
	p := Price{ID: "p", ModelID: "m", Currency: "USD", Scale: 6, Meters: map[string]Rational{MeterInput: {Numerator: 1, Denominator: 4}}}
	res, err := Backfill(ctx, store, "t", []Price{p}, BackfillQuery{}, at.Add(time.Hour))
	if err != nil || res.Updated != 1 || res.Scanned != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	for id, want := range map[string]string{"reported-cost": BasisReported, "settled": BasisSettled, "reported-nocost": BasisEstimated} {
		got, _ := store.Get(ctx, "t", id)
		if got.Cost.Basis != want {
			t.Errorf("%s: basis %s, want %s", id, got.Cost.Basis, want)
		}
	}
	if r, _ := store.Get(ctx, "t", "reported-cost"); r.Cost.Amount != "5" || r.Cost.BackfilledAt != nil {
		t.Fatalf("reported cost changed: %+v", r.Cost)
	}
	// 37 tokens at 1/4 is 9.25 -> 10 with exact 37/4; one reported group keeps its own total.
	s, err := Summarize(ctx, store, "t", SummaryQuery{Filter: Filter{Since: at.Add(-time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	totals := map[string]string{}
	for _, g := range s.Groups {
		for _, c := range g.Cost {
			totals[c.Basis] += c.Amount + ";"
		}
	}
	if totals[BasisReported] != "5;" || totals[BasisSettled] != "7;" || totals[BasisEstimated] != "10;" {
		t.Fatalf("totals %v", totals)
	}
}

package usageledger

import (
	"context"
	"testing"
	"time"
)

func TestSummaryByBudgetCountsManagedRecordsOnly(t *testing.T) {
	store := NewMemoryStore()
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	mk := func(id, source string, budgets []string) Record {
		return Record{ID: id, TenantID: "t", StartedAt: at, FinishedAt: at.Add(time.Second), Plane: PlaneLLM, Status: StatusOK, Source: source, Requests: 1, Cost: UnknownCost(), BudgetIDs: budgets}
	}
	for _, r := range []Record{mk("a", SourceManaged, []string{"org", "team"}), mk("b", SourceManaged, []string{"org"}), mk("c", SourceReported, nil)} {
		if err := store.Append(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Summarize(context.Background(), store, "t", SummaryQuery{GroupBy: []string{"budget"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, g := range s.Groups {
		got[g.Key["budget"]] = g.Requests
		if _, split := g.Key["source"]; split {
			t.Fatalf("a summary by budget is not source-split: %+v", g.Key)
		}
	}
	if len(got) != 2 || got["org"] != 2 || got["team"] != 1 {
		t.Fatalf("reported records must not appear: %v", got)
	}
}

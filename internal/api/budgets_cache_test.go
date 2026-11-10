package api

import (
	"context"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// A cache hit claims the lease and settles it as one request with zero
// provider consumption: request-count budgets draw one, cost budgets nothing.
func TestBudgetCacheHitDrawsRequestsButNotCost(t *testing.T) {
	requests := reqBudget("req", "", "Requests", "10", "engineering")
	cost := costBudget("cost", "Cost", "1", "", "engineering")
	f := budgetHostNew(t, requests, cost)
	identity := quotaHostTestIdentity(t, f.now, nil)
	r, in := quotaHostTestInvocation(identity, "hit", `{}`)
	lease, err := f.host.Prepare(r.Context(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	attempt := quotaHostTestAttempt(t, lease)
	if err = lease.BeforeDispatch(r.Context()); err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish(r.Context(), execution.Result{StatusCode: 200, ResponseComplete: true, Cached: true}); err != nil {
		t.Fatal(err)
	}
	if bal := f.balance(t, requests); bal.Consumed != 1 || bal.Held != 0 {
		t.Fatalf("a cache hit is one request: %+v", bal)
	}
	if bal := f.balance(t, cost); bal.Consumed != 0 || bal.Held != 0 {
		t.Fatalf("a cache hit costs nothing: %+v", bal)
	}
	if ids := f.host.budgetsDrawn(context.Background(), "tenant", attempt); strings.Join(ids, ",") != "cost,req" {
		t.Fatalf("the hit's record names the budgets it was admitted under: %v", ids)
	}
}

// A provably unsent attempt settles at zero: no budget is drawn.
func TestBudgetUnsentAttemptDrawsNothing(t *testing.T) {
	requests := reqBudget("req", "", "Requests", "10", "engineering")
	f := budgetHostNew(t, requests)
	identity := quotaHostTestIdentity(t, f.now, nil)
	r, in := quotaHostTestInvocation(identity, "unsent", `{}`)
	lease, err := f.host.Prepare(r.Context(), r, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.BeforeDispatch(r.Context()); err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish(r.Context(), execution.Result{NotSent: true}); err != nil {
		t.Fatal(err)
	}
	if bal := f.balance(t, requests); bal.Consumed != 0 || bal.Held != 0 {
		t.Fatalf("an unsent attempt draws nothing: %+v", bal)
	}
}

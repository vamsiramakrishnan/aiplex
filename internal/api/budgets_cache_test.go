package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/budgets"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
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

// pausedBudgetStore pauses a single read after taking its store snapshot.
type pausedBudgetStore struct {
	resources.Store
	pause    atomic.Bool
	snapshot chan struct{}
	resume   chan struct{}
	readErr  error
}

func (s *pausedBudgetStore) List(ctx context.Context, tenant, kind string) ([]resources.Record, error) {
	rows, err := s.Store.List(ctx, tenant, kind)
	if !s.pause.CompareAndSwap(true, false) {
		return rows, err
	}
	close(s.snapshot)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.resume:
		if s.readErr != nil {
			return nil, s.readErr
		}
		return rows, err
	}
}

func TestBudgetEditInvalidatesInFlightRefresh(t *testing.T) {
	for _, test := range []struct {
		name    string
		warm    bool
		readErr error
	}{
		{"first load", false, nil},
		{"expired entry", true, nil},
		{"stale fallback", true, errors.New("store temporarily unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			store := &pausedBudgetStore{Store: resources.NewMemoryStore(), snapshot: make(chan struct{}), resume: make(chan struct{}), readErr: test.readErr}
			now := time.Now()
			cache := &BudgetCache{Store: store, TTL: time.Second, Now: func() time.Time { return now }}
			if test.warm {
				if _, err := cache.Tree(ctx, "tenant"); err != nil {
					t.Fatal(err)
				}
				now = now.Add(2 * time.Second)
			}

			store.pause.Store(true)
			type result struct {
				tree *budgets.Tree
				err  error
			}
			done := make(chan result, 1)
			go func() {
				tree, err := cache.Tree(ctx, "tenant")
				done <- result{tree, err}
			}()
			select {
			case <-store.snapshot:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			budget := reqBudget("team", "", "Team", "5", "eng")
			spec, err := json.Marshal(budget)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Put(ctx, resources.Record{TenantID: "tenant", Kind: budgets.Kind, ID: budget.ID, Spec: spec}, 0); err != nil {
				t.Fatal(err)
			}
			cache.Invalidate("tenant")
			close(store.resume)

			read := <-done
			if read.err != nil {
				t.Fatal(read.err)
			}
			if _, ok := read.tree.Get(budget.ID); !ok {
				t.Fatal("refresh returned the pre-edit tree")
			}
			tree, err := cache.Tree(ctx, "tenant")
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := tree.Get(budget.ID); !ok {
				t.Fatal("refresh repopulated the cache with the pre-edit tree")
			}
		})
	}
}

// A definition written by another replica is discovered on refresh. Once a
// refresh finds that definition invalid, an outage cannot revive the old tree.
func TestInvalidBudgetTreeRetiresStaleFallback(t *testing.T) {
	ctx := context.Background()
	store := &pausedBudgetStore{Store: resources.NewMemoryStore(), snapshot: make(chan struct{}), resume: make(chan struct{}), readErr: errors.New("store offline")}
	now := time.Now()
	cache := &BudgetCache{Store: store, TTL: time.Second, Now: func() time.Time { return now }}
	if _, err := cache.Tree(ctx, "tenant"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if _, err := store.Put(ctx, resources.Record{TenantID: "tenant", Kind: budgets.Kind, ID: "broken", Spec: json.RawMessage(`{}`)}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Tree(ctx, "tenant"); !errors.Is(err, errBudgetInvalid) {
		t.Fatalf("expected an invalid budget definition, got %v", err)
	}
	close(store.resume)
	store.pause.Store(true)
	if tree, err := cache.Tree(ctx, "tenant"); !errors.Is(err, store.readErr) || tree != nil {
		t.Fatalf("unverified budget tree returned during outage: tree=%v err=%v", tree, err)
	}
}

package registry

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests exercise tenant budget draws (quotaplex.BudgetDraw) against the
// real Firestore adapter on the emulator, with two engines on two clients of
// one database. The names start with TestQuotaEmulator so the CI emulator job
// picks them up with its existing -run pattern. They prove the adapter's
// transaction semantics under emulator contention; they say nothing about
// managed Firestore.

// retryBudgetEmulator retries only aborted transactions (a fail-closed,
// retryable result) many more times than retryQuotaEmulator, because these
// tests put every goroutine on one counter document.
func retryBudgetEmulator(ctx context.Context, fn func() error) error {
	for i := 0; i < 60; i++ {
		err := fn()
		if status.Code(err) != codes.Aborted {
			return err
		}
		wait := time.Duration(i+1) * 10 * time.Millisecond
		if wait > 200*time.Millisecond {
			wait = 200 * time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	return fmt.Errorf("emulator contention retries exhausted")
}

// budgetWorkers bounds the goroutines that hit one counter document at once.
// On the emulator, a few dozen simultaneous transactions on one document
// exhaust the SDK's retries for minutes (see docs/budgets.md); 4 completes
// quickly and is still enough contention for a lost update to be likely.
const budgetWorkers = 4

// parallel runs fn(0..n-1) on at most workers goroutines, in index order.
func parallel(wg *sync.WaitGroup, workers, n int, fn func(i int)) {
	jobs := make(chan int, n)
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				fn(i)
			}
		}()
	}
}

// budgetEmulatorPolicy has no policy-level budgets or rates: only the draws
// under test constrain admission. "extra" is a billable meter the price book
// does not price, which makes a request's cost unknown.
func budgetEmulatorPolicy(now time.Time) quotaplex.Policy {
	p := quotaEmulatorPolicy(now)
	p.Budgets, p.Rates = nil, nil
	p.Concurrency = []quotaplex.ConcurrencyRule{{ID: "active", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 1000}}
	p.Meters["extra"] = quotaplex.Meter{ID: "extra", Unit: "tokens", Billable: true}
	return p
}

type budgetCase struct {
	name   string
	draw   func(period quotaplex.Period) quotaplex.BudgetDraw
	admits int // limit / per-request draw
}

// A request bounds 1 request and 10 tokens, which price to 15 micro-USD.
func budgetCases() []budgetCase {
	thresholds := []int{50, 100}
	return []budgetCase{
		{"requests", func(p quotaplex.Period) quotaplex.BudgetDraw {
			return quotaplex.BudgetDraw{ID: "b-requests", Unit: quotaplex.DrawRequests, Limit: 10, Period: p, Thresholds: thresholds}
		}, 10},
		{"tokens", func(p quotaplex.Period) quotaplex.BudgetDraw {
			return quotaplex.BudgetDraw{ID: "b-tokens", Unit: quotaplex.DrawTokens, Limit: 100, Period: p, Thresholds: thresholds}
		}, 10},
		{"cost", func(p quotaplex.Period) quotaplex.BudgetDraw {
			return quotaplex.BudgetDraw{ID: "b-cost", Unit: quotaplex.DrawCost, Currency: "USD", Scale: 6, Limit: 150, Period: p, Thresholds: thresholds}
		}, 10},
	}
}

type budgetFixture struct {
	t       *testing.T
	ctx     context.Context
	now     time.Time
	policy  quotaplex.Policy
	period  quotaplex.Period
	engines []*quotaplex.Engine
	project string
}

func newBudgetFixture(t *testing.T) *budgetFixture {
	t.Helper()
	first, second, project := quotaEmulatorStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	now := time.Now().UTC()
	policy := budgetEmulatorPolicy(now)
	f := &budgetFixture{t: t, ctx: ctx, now: now, policy: policy, project: project,
		period: quotaplex.Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}}
	f.engines = []*quotaplex.Engine{f.engine(first), f.engine(second)}
	if err := f.engines[0].Activate(ctx, ""); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *budgetFixture) engine(store *FirestoreStore) *quotaplex.Engine {
	f.t.Helper()
	compiled, err := quotaplex.Compile(f.policy)
	if err != nil {
		f.t.Fatal(err)
	}
	e, err := quotaplex.NewEngine(compiled, store.QuotaStore(), func() time.Time { return f.now })
	if err != nil {
		f.t.Fatal(err)
	}
	return e
}

func (f *budgetFixture) demand(id string, draws ...quotaplex.BudgetDraw) quotaplex.Demand {
	d := quotaEmulatorDemand(f.now, id)
	d.Budgets = draws
	return d
}

func (f *budgetFixture) admit(e *quotaplex.Engine, d quotaplex.Demand) (quotaplex.Decision, error) {
	var dec quotaplex.Decision
	err := retryBudgetEmulator(f.ctx, func() error {
		var err error
		dec, err = e.Admit(f.ctx, d)
		return err
	})
	return dec, err
}

func (f *budgetFixture) balance(e *quotaplex.Engine, draw quotaplex.BudgetDraw) quotaplex.DrawBalance {
	f.t.Helper()
	var bal quotaplex.DrawBalance
	err := retryBudgetEmulator(f.ctx, func() error {
		var err error
		bal, err = e.DrawBalance(f.ctx, "qualified-tenant", draw, f.now)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return bal
}

// claimAndSettle settles one admitted attempt on engine 0, then replays the
// same terminal event on engine 1 (a second replica retrying a lost response).
// The replay must be an idempotent acknowledgement, not a second charge.
func (f *budgetFixture) claimAndSettle(d quotaplex.Demand, usage quotaplex.Vector) error {
	var claim quotaplex.DispatchClaim
	err := retryBudgetEmulator(f.ctx, func() error {
		var err error
		claim, err = f.engines[0].ClaimDispatch(f.ctx, d.Context.TenantID, d.AttemptID)
		return err
	})
	if err != nil {
		return fmt.Errorf("claim %s: %w", d.AttemptID, err)
	}
	settlement := quotaplex.Settlement{TenantID: d.Context.TenantID, AttemptID: d.AttemptID, EventID: "evt-" + d.AttemptID, Claim: claim, Usage: usage, ModelVersion: "qualified-model-v1"}
	for _, e := range f.engines {
		if err := retryBudgetEmulator(f.ctx, func() error { _, err := e.Settle(f.ctx, settlement); return err }); err != nil {
			return fmt.Errorf("settle %s: %w", d.AttemptID, err)
		}
	}
	return nil
}

// Defect caught: a lost update or a non-transactional read-modify-write on the
// budget counter (the total admitted exceeds the limit), a double settlement
// (consumed is more than admitted*demand, or a hold is released twice and
// underflows), a hold that is never released (Held stays above zero), and a
// threshold recorded twice or overwritten (Crossed is not exactly 50 and 100).
// Two engines, 4 workers, 24 attempts against a budget that fits 10.
func TestQuotaEmulatorBudgetConcurrentAdmitSettleNeverExceedsLimit(t *testing.T) {
	for _, tc := range budgetCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newBudgetFixture(t)
			draw := tc.draw(f.period)
			const attempts, workers = 24, budgetWorkers
			var allowed atomic.Int64
			var mu sync.Mutex
			var denied []quotaplex.Decision
			jobs := make(chan int, attempts)
			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					for i := range jobs {
						d := f.demand(fmt.Sprintf("%s-%02d", tc.name, i), draw)
						dec, err := f.admit(f.engines[w%2], d)
						if err != nil {
							t.Error(err)
							continue
						}
						if !dec.Allowed {
							mu.Lock()
							denied = append(denied, dec)
							mu.Unlock()
							continue
						}
						allowed.Add(1)
						if err := f.claimAndSettle(d, quotaplex.Vector{"requests": 1, "tokens": 10}); err != nil {
							t.Error(err)
						}
					}
				}(w)
			}
			for i := 0; i < attempts; i++ {
				jobs <- i
			}
			close(jobs)
			wg.Wait()
			if t.Failed() {
				return
			}
			if allowed.Load() != int64(tc.admits) {
				t.Fatalf("admitted %d attempts, want exactly %d (limit %d)", allowed.Load(), tc.admits, draw.Limit)
			}
			for _, dec := range denied {
				if dec.Reason != "budget_exhausted" || dec.BudgetID != draw.ID {
					t.Fatalf("denial must name the exhausted budget: %+v", dec)
				}
			}
			// Settled at the full bound, so every admitted attempt moved its
			// hold into consumption exactly once: held 0, consumed == limit.
			for i, e := range f.engines {
				bal := f.balance(e, draw)
				if bal.Held != 0 || bal.Consumed != draw.Limit || bal.Available != 0 || bal.UnknownCost != 0 {
					t.Fatalf("engine %d balance after settlement: %+v", i, bal)
				}
				// Each threshold fired once, not once per engine or per settle.
				if len(bal.Crossed) != 2 || bal.Crossed[0].Percent != 50 || bal.Crossed[1].Percent != 100 {
					t.Fatalf("engine %d thresholds: %+v", i, bal.Crossed)
				}
				if bal.Crossed[0].Consumed < draw.Limit/2 || bal.Crossed[0].Consumed > draw.Limit || bal.Crossed[1].Consumed != draw.Limit {
					t.Fatalf("engine %d threshold consumption: %+v", i, bal.Crossed)
				}
			}
			// Same-attempt records agree: every admitted attempt is settled.
			settled := 0
			for i := 0; i < attempts; i++ {
				info, err := f.engines[1].GetAttempt(f.ctx, "qualified-tenant", fmt.Sprintf("%s-%02d", tc.name, i))
				if err == nil && info.Decision.State == "settled" {
					settled++
				}
			}
			if settled != tc.admits {
				t.Fatalf("%d settled attempts, want %d", settled, tc.admits)
			}
		})
	}
}

// Defect caught: two replicas settling the same terminal event at the same
// moment both applying it (consumed is twice the usage, held underflows), or
// both failing and leaving the hold stranded. The loser may be retried, but the
// charge lands exactly once and the loser's result is the same settled decision.
func TestQuotaEmulatorBudgetSimultaneousSettleChargesOnce(t *testing.T) {
	f := newBudgetFixture(t)
	draw := quotaplex.BudgetDraw{ID: "b-once", Unit: quotaplex.DrawTokens, Limit: 100, Period: f.period}
	for round := 0; round < 3; round++ {
		d := f.demand(fmt.Sprintf("once-%d", round), draw)
		if dec, err := f.admit(f.engines[0], d); err != nil || !dec.Allowed {
			t.Fatal(dec, err)
		}
		var claim quotaplex.DispatchClaim
		if err := retryBudgetEmulator(f.ctx, func() error {
			var err error
			claim, err = f.engines[0].ClaimDispatch(f.ctx, d.Context.TenantID, d.AttemptID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		// Settle with less than the bound: 7 consumed, 3 of the hold released.
		settlement := quotaplex.Settlement{TenantID: d.Context.TenantID, AttemptID: d.AttemptID, EventID: "evt-" + d.AttemptID, Claim: claim, Usage: quotaplex.Vector{"requests": 1, "tokens": 7}, ModelVersion: "qualified-model-v1"}
		var wg sync.WaitGroup
		decisions := make([]quotaplex.Decision, 2)
		for i, e := range f.engines {
			wg.Add(1)
			go func(i int, e *quotaplex.Engine) {
				defer wg.Done()
				err := retryBudgetEmulator(f.ctx, func() error {
					var err error
					decisions[i], err = e.Settle(f.ctx, settlement)
					return err
				})
				if err != nil {
					t.Error(err)
				}
			}(i, e)
		}
		wg.Wait()
		if t.Failed() {
			return
		}
		if decisions[0].State != "settled" || decisions[1].State != "settled" {
			t.Fatalf("both replicas must see the settled decision: %+v", decisions)
		}
		want := int64(7 * (round + 1))
		if bal := f.balance(f.engines[1], draw); bal.Held != 0 || bal.Consumed != want || bal.Available != 100-want {
			t.Fatalf("round %d: simultaneous settlement must charge once: %+v", round, bal)
		}
	}
}

// Defect caught: a release that is applied twice (replayed abort from the
// other engine returns hold twice, or underflows), or not applied (a hold is
// stranded and the budget stays closed). Every admitted hold is aborted by
// both engines at once; the budget must return to exactly its limit, and then
// admit exactly its limit again.
func TestQuotaEmulatorBudgetHoldReleasedExactlyOnce(t *testing.T) {
	for _, tc := range budgetCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newBudgetFixture(t)
			draw := tc.draw(f.period)
			var demands []quotaplex.Demand
			for i := 0; i < tc.admits; i++ {
				d := f.demand(fmt.Sprintf("hold-%s-%02d", tc.name, i), draw)
				dec, err := f.admit(f.engines[i%2], d)
				if err != nil || !dec.Allowed {
					t.Fatalf("hold %d: %+v %v", i, dec, err)
				}
				demands = append(demands, d)
			}
			if bal := f.balance(f.engines[1], draw); bal.Held != draw.Limit || bal.Available != 0 {
				t.Fatalf("holds before release: %+v", bal)
			}
			var wg sync.WaitGroup
			type release struct {
				d quotaplex.Demand
				e *quotaplex.Engine
			}
			var jobs []release
			for _, d := range demands {
				for _, e := range f.engines {
					jobs = append(jobs, release{d, e})
				}
			}
			parallel(&wg, budgetWorkers, len(jobs), func(i int) {
				j := jobs[i]
				if err := retryBudgetEmulator(f.ctx, func() error { return j.e.Abort(f.ctx, j.d.Context.TenantID, j.d.AttemptID) }); err != nil {
					t.Error(err)
				}
			})
			wg.Wait()
			if t.Failed() {
				return
			}
			bal := f.balance(f.engines[0], draw)
			if bal.Held != 0 || bal.Consumed != 0 || bal.Available != draw.Limit {
				t.Fatalf("aborted holds must return to the budget exactly once: %+v", bal)
			}
			// The released capacity is usable again, and only once.
			for i := 0; i < tc.admits+1; i++ {
				dec, err := f.admit(f.engines[i%2], f.demand(fmt.Sprintf("again-%s-%02d", tc.name, i), draw))
				if err != nil {
					t.Fatal(err)
				}
				if want := i < tc.admits; dec.Allowed != want {
					t.Fatalf("re-admission %d allowed=%v want %v: %+v", i, dec.Allowed, want, dec)
				}
			}
		})
	}
}

// Defect caught: an unknown cost recorded as zero spend (Consumed rises, or the
// request is silently dropped from the unknown count), a lost update on the
// unknown counter across engines, an unpriced request held against a deny-mode
// budget, or a deny-mode budget that admits what it cannot price.
func TestQuotaEmulatorBudgetUnknownCostIsCountedNotZero(t *testing.T) {
	f := newBudgetFixture(t)
	flag := quotaplex.BudgetDraw{ID: "cost-flag", Unit: quotaplex.DrawCost, Currency: "USD", Scale: 6, Limit: 150, Period: f.period, OnUnknownCost: quotaplex.UnknownCostAllowAndFlag}
	deny := quotaplex.BudgetDraw{ID: "cost-deny", Unit: quotaplex.DrawCost, Currency: "USD", Scale: 6, Limit: 150, Period: f.period}
	unpriced := func(id string, draw quotaplex.BudgetDraw) quotaplex.Demand {
		d := f.demand(id, draw)
		d.Bounds = quotaplex.Vector{"requests": 1, "tokens": 10, "extra": 5}
		return d
	}
	const n = 24
	var wg sync.WaitGroup
	parallel(&wg, budgetWorkers, n, func(i int) {
		d := unpriced(fmt.Sprintf("flag-%02d", i), flag)
		dec, err := f.admit(f.engines[i%2], d)
		if err != nil || !dec.Allowed {
			t.Errorf("allow_and_flag must admit an unpriced request: %+v %v", dec, err)
			return
		}
		if err := f.claimAndSettle(d, quotaplex.Vector{"requests": 1, "tokens": 10, "extra": 5}); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	if t.Failed() {
		return
	}
	for i, e := range f.engines {
		bal := f.balance(e, flag)
		if bal.UnknownCost != n || bal.Consumed != 0 || bal.Held != 0 || bal.Available != flag.Limit {
			t.Fatalf("engine %d: %d unknown-cost requests must be counted as unknown and add no spend: %+v", i, n, bal)
		}
	}
	// A deny-mode budget refuses what it cannot price, and holds nothing.
	dec, err := f.admit(f.engines[1], unpriced("deny-0", deny))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allowed || dec.Reason != "unpriced_meter" || dec.BudgetID != deny.ID {
		t.Fatalf("unknown price must deny, not count as zero: %+v", dec)
	}
	if bal := f.balance(f.engines[0], deny); bal.Held != 0 || bal.Consumed != 0 || bal.UnknownCost != 0 {
		t.Fatalf("denied request left state: %+v", bal)
	}
}

// Defect caught: balances held only in a process or client (a restarted engine
// over the same database sees different holds, consumption, unknown counts or
// threshold history), and a restart that loses the ability to settle or
// release holds taken before it, or that forgets what is already spent.
func TestQuotaEmulatorBudgetRestartSeesSameBalances(t *testing.T) {
	first, second, project := quotaEmulatorStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	now := time.Now().UTC()
	policy := budgetEmulatorPolicy(now)
	f := &budgetFixture{t: t, ctx: ctx, now: now, policy: policy, project: project,
		period: quotaplex.Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}}
	f.engines = []*quotaplex.Engine{f.engine(first), f.engine(second)}
	if err := f.engines[0].Activate(ctx, ""); err != nil {
		t.Fatal(err)
	}
	draws := map[string]quotaplex.BudgetDraw{}
	for _, tc := range budgetCases() {
		draws[tc.name] = tc.draw(f.period)
	}
	// One draw set per request: all three budgets are drawn together. Limits
	// are 10 requests, 100 tokens, 150 micro-USD, so 10 requests fit.
	all := []quotaplex.BudgetDraw{draws["requests"], draws["tokens"], draws["cost"]}
	var demands []quotaplex.Demand
	for i := 0; i < 10; i++ {
		d := f.demand(fmt.Sprintf("restart-%02d", i), all...)
		dec, err := f.admit(f.engines[i%2], d)
		if err != nil || !dec.Allowed {
			t.Fatalf("admit %d: %+v %v", i, dec, err)
		}
		demands = append(demands, d)
	}
	// Settle 6 at the full bound (crossing 50%), leave 4 holds open.
	for _, d := range demands[:6] {
		if err := f.claimAndSettle(d, quotaplex.Vector{"requests": 1, "tokens": 10}); err != nil {
			t.Fatal(err)
		}
	}
	before := map[string]quotaplex.DrawBalance{}
	for name, draw := range draws {
		before[name] = f.balance(f.engines[0], draw)
	}
	if b := before["tokens"]; b.Held != 40 || b.Consumed != 60 || len(b.Crossed) != 1 || b.Crossed[0].Percent != 50 {
		t.Fatalf("tokens before restart: %+v", b)
	}
	_ = first.Close()
	_ = second.Close()

	restarted, err := NewFirestoreStore(project, "")
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	f.engines = []*quotaplex.Engine{f.engine(restarted)}
	if err := f.engines[0].Activate(ctx, ""); err != nil {
		t.Fatal(err)
	}
	for name, draw := range draws {
		after := f.balance(f.engines[0], draw)
		if !reflect.DeepEqual(after, before[name]) {
			t.Fatalf("%s balance changed across restart:\nbefore %+v\nafter  %+v", name, before[name], after)
		}
	}
	// The restarted engine admits nothing beyond what remains (the four open
	// holds still count) ...
	dec, err := f.admit(f.engines[0], f.demand("after-restart", all...))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Allowed || dec.Reason != "budget_exhausted" {
		t.Fatalf("holds taken before the restart must still count: %+v", dec)
	}
	// ... and can settle and release holds taken before it.
	f.engines = []*quotaplex.Engine{f.engines[0], f.engines[0]}
	for _, d := range demands[6:8] {
		if err := f.claimAndSettle(d, quotaplex.Vector{"requests": 1, "tokens": 10}); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range demands[8:] {
		if err := retryBudgetEmulator(ctx, func() error { return f.engines[0].Abort(ctx, d.Context.TenantID, d.AttemptID) }); err != nil {
			t.Fatal(err)
		}
	}
	if b := f.balance(f.engines[0], draws["tokens"]); b.Held != 0 || b.Consumed != 80 || b.Available != 20 {
		t.Fatalf("tokens after finishing pre-restart holds: %+v", b)
	}
	if b := f.balance(f.engines[0], draws["cost"]); b.Held != 0 || b.Consumed != 120 || b.Available != 30 {
		t.Fatalf("cost after finishing pre-restart holds: %+v", b)
	}
	if b := f.balance(f.engines[0], draws["requests"]); b.Held != 0 || b.Consumed != 8 || b.Available != 2 {
		t.Fatalf("requests after finishing pre-restart holds: %+v", b)
	}
}

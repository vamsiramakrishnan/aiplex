package quotaplex

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func lifecycleClaim(t *testing.T, engine *Engine, demand Demand) DispatchClaim {
	t.Helper()
	if decision := engineAdmit(t, engine, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	claim, err := engine.ClaimDispatch(context.Background(), demand.Context.TenantID, demand.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	return claim
}

func TestLifecycleDispatchIsClaimedOnceAcrossReplicas(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	first := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, store)
	second := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, store)
	engineActivate(t, first, "")
	demand := engineFixtureDemand(now, "one-upstream-attempt", 10)
	if decision := engineAdmit(t, first, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	var claims atomic.Int64
	var workers sync.WaitGroup
	for i := 0; i < 40; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			engine := first
			if i%2 == 1 {
				engine = second
			}
			_, err := engine.ClaimDispatch(context.Background(), "tenant", demand.AttemptID)
			if err == nil {
				claims.Add(1)
			} else if !errors.Is(err, ErrDispatchClaimed) {
				t.Error(err)
			}
		}(i)
	}
	workers.Wait()
	if claims.Load() != 1 {
		t.Fatal("more than one upstream dispatch was permitted", claims.Load())
	}
	if result := engineAdmit(t, second, demand); !result.Allowed || result.State != "dispatching" {
		t.Fatal("replay lost current lifecycle", result)
	}
	if err := second.Abort(context.Background(), "tenant", demand.AttemptID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("possible provider work was refunded", err)
	}
}

func TestLifecycleUnknownWorkRetainsExposureBeyondWindowAndSettlesPinnedPrice(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	base := now
	store := NewMemoryStore()
	policy := engineFixturePolicy(now)
	engine := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "attempt", 10)
	key := engineBudgetKey(t, engine, demand, "token-budget")
	moneyKey := engineBudgetKey(t, engine, demand, "spend-budget")
	claim := lifecycleClaim(t, engine, demand)
	if err := engine.MarkUnknown(context.Background(), claim, "provider response lost"); err != nil {
		t.Fatal(err)
	}
	next := policyCopy(policy)
	next.Revision = 2
	next.PriceBooks[0].Revision = 2
	next.PriceBooks[0].Prices[0].Numerator = 100
	updated := engineFixture(t, next, func() time.Time { return now }, store)
	engineActivate(t, updated, engine.policy.Reference().Digest)
	now = base.Add(48 * time.Hour)
	if err := updated.Abort(context.Background(), "tenant", demand.AttemptID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("elapsed clock released unknown work", err)
	}
	balance, err := updated.BudgetBalance(context.Background(), "tenant", key)
	if err != nil || balance.Held != 10 {
		t.Fatal(balance, err)
	}
	decision, err := updated.Settle(context.Background(), Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "trusted-terminal", Claim: claim, Usage: Vector{"requests": 1, "tokens": 4}, ProviderResourceID: "provider-job"})
	if err != nil || decision.State != "settled" || decision.Overrun {
		t.Fatal(decision, err)
	}
	balance, err = updated.BudgetBalance(context.Background(), "tenant", key)
	if err != nil || balance.Held != 0 || balance.Consumed != 4 {
		t.Fatal(balance, err)
	}
	money, err := updated.BudgetBalance(context.Background(), "tenant", moneyKey)
	if err != nil || money.Consumed != 6 {
		t.Fatal("late settlement repriced historical consumption", money, err)
	}
	info, err := updated.GetAttempt(context.Background(), "tenant", demand.AttemptID)
	if err != nil || info.Plan.Policy.Revision != 1 || info.ProviderResource != "provider-job" {
		t.Fatal(info, err)
	}
	info.Actual["tokens"] = 999
	fresh, err := updated.GetAttempt(context.Background(), "tenant", demand.AttemptID)
	if err != nil || fresh.Actual["tokens"] != 4 {
		t.Fatal("recovery read mutated authoritative state", fresh, err)
	}
}

func TestLifecycleActualOverrunIsDebtRatherThanDiscardedUsage(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	policy := engineFixturePolicy(now)
	policy.Budgets[0].Limit = 10
	store := NewMemoryStore()
	engine := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "overrun", 10)
	key := engineBudgetKey(t, engine, demand, "token-budget")
	claim := lifecycleClaim(t, engine, demand)
	usage := Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "terminal", Claim: claim, Usage: Vector{"requests": 1, "tokens": 15}}
	decision, err := engine.Settle(context.Background(), usage)
	if err != nil || !decision.Overrun || decision.State != "settled" {
		t.Fatal("actual overrun was not acknowledged", decision, err)
	}
	balance, err := engine.BudgetBalance(context.Background(), "tenant", key)
	if err != nil || balance.Held != 0 || balance.Consumed != 15 || balance.Available != -5 {
		t.Fatal("overrun was refunded or capped", balance, err)
	}
	if denied := engineAdmit(t, engine, engineFixtureDemand(now, "after-debt", 1)); denied.Allowed || denied.Reason != "budget_exhausted" {
		t.Fatal("negative balance admitted new work", denied)
	}
	if duplicate, err := engine.Settle(context.Background(), usage); err != nil || duplicate != decision {
		t.Fatal("settlement replay was not idempotent", duplicate, err)
	}
	usage.EventID = "same-work-different-observation"
	if _, err := engine.Settle(context.Background(), usage); err != nil {
		t.Fatal("same terminal result was charged again", err)
	}
	usage.Usage["tokens"] = 16
	if _, err := engine.Settle(context.Background(), usage); !errors.Is(err, ErrConflict) {
		t.Fatal("terminal usage was rewritten", err)
	}
	balance, _ = engine.BudgetBalance(context.Background(), "tenant", key)
	if balance.Consumed != 15 {
		t.Fatal(balance)
	}
}

func TestLifecycleTerminalCoverageAndStaleFencesCannotReleaseHolds(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "attempt", 10)
	key := engineBudgetKey(t, engine, demand, "token-budget")
	claim := lifecycleClaim(t, engine, demand)
	for _, test := range []struct {
		name  string
		usage Vector
		fence uint64
	}{
		{"unknown usage", nil, 1},
		{"missing known meter", Vector{"requests": 1}, 1},
		{"caller money", Vector{"requests": 1, "tokens": 5, "usd": 1}, 1},
		{"unknown meter", Vector{"requests": 1, "tokens": 5, "fabricated": 2}, 1},
		{"stale fence", Vector{"requests": 1, "tokens": 5}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			badClaim := claim
			badClaim.Fence = test.fence
			if _, err := engine.Settle(context.Background(), Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: test.name, Claim: badClaim, Usage: test.usage}); err == nil {
				t.Fatal("unsafe terminal evidence accepted")
			}
			balance, err := engine.BudgetBalance(context.Background(), "tenant", key)
			if err != nil || balance.Held != 10 || balance.Consumed != 0 {
				t.Fatal("rejected terminal evidence changed exposure", balance, err)
			}
		})
	}
	if _, err := engine.Settle(context.Background(), Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "valid-zero", Claim: claim, Usage: Vector{"requests": 0, "tokens": 0}}); err != nil {
		t.Fatal("explicit known zero consumption was not supported", err)
	}
}

func TestLifecycleZeroBoundProjectionChargesUnexpectedActualWork(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	policy := engineFixturePolicy(now)
	policy.Meters["rows"] = Meter{ID: "rows", Unit: "rows"}
	policy.Budgets = append(policy.Budgets, BudgetRule{ID: "row-budget", Scope: Scope{Kind: "tenant"}, MeterID: "rows", Limit: 2, Period: policy.Budgets[0].Period})
	engine := engineFixture(t, policy, func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "attempt", 1)
	demand.Bounds["rows"] = 0
	key := engineBudgetKey(t, engine, demand, "row-budget")
	claim := lifecycleClaim(t, engine, demand)
	decision, err := engine.Settle(context.Background(), Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "terminal", Claim: claim, Usage: Vector{"requests": 1, "tokens": 1, "rows": 3}})
	if err != nil || !decision.Overrun {
		t.Fatal(decision, err)
	}
	balance, err := engine.BudgetBalance(context.Background(), "tenant", key)
	if err != nil || balance.Consumed != 3 || balance.Available != -1 {
		t.Fatal("zero admission hold suppressed actual usage", balance, err)
	}
}

func TestLifecycleClaimAbortRaceNeverReleasesPossiblyDispatchedWork(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for iteration := 0; iteration < 20; iteration++ {
		store := NewMemoryStore()
		engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, store)
		engineActivate(t, engine, "")
		demand := engineFixtureDemand(now, fmt.Sprintf("race-%d", iteration), 10)
		key := engineBudgetKey(t, engine, demand, "token-budget")
		if decision := engineAdmit(t, engine, demand); !decision.Allowed {
			t.Fatal(decision)
		}
		start := make(chan struct{})
		var claimErr, abortErr error
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			_, claimErr = engine.ClaimDispatch(context.Background(), "tenant", demand.AttemptID)
		}()
		go func() {
			defer workers.Done()
			<-start
			abortErr = engine.Abort(context.Background(), "tenant", demand.AttemptID)
		}()
		close(start)
		workers.Wait()
		if (claimErr == nil) == (abortErr == nil) {
			t.Fatal("claim and abort did not exclude one another", claimErr, abortErr)
		}
		balance, err := engine.BudgetBalance(context.Background(), "tenant", key)
		if err != nil || (claimErr == nil && balance.Held != 10) || (abortErr == nil && balance.Held != 0) {
			t.Fatal(balance, err)
		}
	}
}

func TestLifecycleConcurrentDuplicateUsageHasOneEffectAndEventCannotCrossAttempts(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "first", 10)
	key := engineBudgetKey(t, engine, demand, "token-budget")
	claim := lifecycleClaim(t, engine, demand)
	settlement := Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "global-event", Claim: claim, Usage: Vector{"requests": 1, "tokens": 5}}
	var workers sync.WaitGroup
	for i := 0; i < 24; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, err := engine.Settle(context.Background(), settlement); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	balance, err := engine.BudgetBalance(context.Background(), "tenant", key)
	if err != nil || balance.Consumed != 5 || balance.Held != 0 {
		t.Fatal("duplicate delivery consumed twice", balance, err)
	}
	second := engineFixtureDemand(now, "second", 10)
	secondClaim := lifecycleClaim(t, engine, second)
	settlement.AttemptID, settlement.Claim = second.AttemptID, secondClaim
	if _, err := engine.Settle(context.Background(), settlement); !errors.Is(err, ErrConflict) {
		t.Fatal("usage event was reassigned to another attempt", err)
	}
	secondInfo, err := engine.GetAttempt(context.Background(), "tenant", second.AttemptID)
	if err != nil || secondInfo.Decision.State != "dispatching" {
		t.Fatal("conflicting event released another attempt", secondInfo, err)
	}
}

func TestLifecycleProviderResourceSurvivesLostReplyAndEngineRestart(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := &engineLostReplyStore{Store: NewMemoryStore()}
	policy := engineFixturePolicy(now)
	first := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, first, "")
	demand := engineFixtureDemand(now, "async-attempt", 10)
	key := engineBudgetKey(t, first, demand, "token-budget")
	claim := lifecycleClaim(t, first, demand)
	store.lose = true
	if err := first.RecordProviderResource(context.Background(), claim, "projects/provider/jobs/native-job"); err == nil {
		t.Fatal("fixture failed to lose the commit reply")
	}
	restarted := engineFixture(t, policy, func() time.Time { return now }, store)
	info, err := restarted.GetAttempt(context.Background(), "tenant", demand.AttemptID)
	if err != nil || info.ProviderResource != "projects/provider/jobs/native-job" || info.Decision.State != "dispatching" {
		t.Fatal("restart lost nonterminal native correlation", info, err)
	}
	if err := restarted.RecordProviderResource(context.Background(), claim, info.ProviderResource); err != nil {
		t.Fatal("same-resource pin could not recover lost reply", err)
	}
	if err := restarted.RecordProviderResource(context.Background(), claim, "projects/provider/jobs/another-job"); !errors.Is(err, ErrConflict) {
		t.Fatal("native resource correlation was rewritten", err)
	}
	if _, err := restarted.ClaimDispatch(context.Background(), "tenant", demand.AttemptID); !errors.Is(err, ErrDispatchClaimed) {
		t.Fatal("pinning granted another dispatch", err)
	}
	if err := restarted.MarkUnknown(context.Background(), claim, "provider job still running"); err != nil {
		t.Fatal(err)
	}
	for _, resource := range []string{"", "projects/provider/jobs/another-job"} {
		if _, err := restarted.Settle(context.Background(), Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "wrong-" + resource, Claim: claim, Usage: Vector{"requests": 1, "tokens": 4}, ProviderResourceID: resource}); !errors.Is(err, ErrConflict) {
			t.Fatal("terminal observation did not bind pinned resource", resource, err)
		}
	}
	balance, err := restarted.BudgetBalance(context.Background(), "tenant", key)
	if err != nil || balance.Held != 10 || balance.Consumed != 0 {
		t.Fatal("nonterminal pin or rejected observation released exposure", balance, err)
	}
	if _, err := restarted.Settle(context.Background(), Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "terminal", Claim: claim, Usage: Vector{"requests": 1, "tokens": 4}, ProviderResourceID: info.ProviderResource}); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RecordProviderResource(context.Background(), claim, info.ProviderResource); err != nil {
		t.Fatal("same-resource pin replay failed after terminal settlement", err)
	}
}

func TestLifecycleProviderResourcePinAndSettlementCannotOverwriteEachOther(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for iteration := 0; iteration < 20; iteration++ {
		engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
		engineActivate(t, engine, "")
		demand := engineFixtureDemand(now, "attempt", 10)
		claim := lifecycleClaim(t, engine, demand)
		key := engineBudgetKey(t, engine, demand, "token-budget")
		start := make(chan struct{})
		var pinErr, settleErr error
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			pinErr = engine.RecordProviderResource(context.Background(), claim, "job-a")
		}()
		go func() {
			defer workers.Done()
			<-start
			_, settleErr = engine.Settle(context.Background(), Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "terminal", Claim: claim, Usage: Vector{"requests": 1, "tokens": 4}, ProviderResourceID: "job-b"})
		}()
		close(start)
		workers.Wait()
		if (pinErr == nil) == (settleErr == nil) {
			t.Fatal("conflicting native coordinates both succeeded or both failed", pinErr, settleErr)
		}
		info, err := engine.GetAttempt(context.Background(), "tenant", demand.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		balance, err := engine.BudgetBalance(context.Background(), "tenant", key)
		if err != nil {
			t.Fatal(err)
		}
		if pinErr == nil && (info.ProviderResource != "job-a" || info.Decision.State != "dispatching" || balance.Held != 10 || balance.Consumed != 0) {
			t.Fatal("resource pin lost correlation or exposure", info, balance)
		}
		if settleErr == nil && (info.ProviderResource != "job-b" || info.Decision.State != "settled" || balance.Held != 0 || balance.Consumed != 4) {
			t.Fatal("settlement resource was overwritten", info, balance)
		}
	}
}

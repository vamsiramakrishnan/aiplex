package quotaplex

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func atomicSchedulerFixture(t *testing.T, now *time.Time, store Store, modify func(*Policy, *SchedulerConfig)) (*Engine, *AtomicScheduler) {
	t.Helper()
	policy := engineFixturePolicy(*now)
	policy.Rates = nil
	policy.Concurrency = nil
	policy.Budgets[0].Limit = 1000
	policy.Budgets[1].Limit = 2000
	cfg := SchedulerConfig{ID: "managed", Queue: QueuePolicy{TenantID: "tenant", PoolID: "provider-capacity", Generation: 1, MeterID: "tokens", MaxEntries: 64, MaxReceipts: 256, MaxWait: time.Minute, MaxCost: 100, Quantum: 1, Weights: map[string]uint32{"a": 1, "b": 1}}, Capacity: CapacityAttestation{ID: "verified-capacity", Version: "1", SourceRef: "operator-reviewed-provider-contract", TenantID: "tenant", PoolID: "provider-capacity", MeterID: "tokens", Target: Match{Provider: "example", PoolID: "provider-capacity"}, Rate: 10, Burst: 10, Interval: time.Second, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}}
	if modify != nil {
		modify(&policy, &cfg)
	}
	engine := engineFixture(t, policy, func() time.Time { return *now }, store)
	engineActivate(t, engine, "")
	scheduler, err := NewAtomicScheduler(engine, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = scheduler.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return engine, scheduler
}
func atomicEnqueue(t *testing.T, s *AtomicScheduler, now time.Time, id, flow string, cost int64) ScheduledRequest {
	t.Helper()
	r := ScheduledRequest{Demand: engineFixtureDemand(now, id, cost), FlowID: flow, Deadline: now.Add(30 * time.Second)}
	status, err := s.Enqueue(context.Background(), r)
	if err != nil || status.State != "queued" {
		t.Fatal(status, err)
	}
	return r
}
func atomicNext(t *testing.T, s *AtomicScheduler, id string) *ScheduledAdmission {
	t.Helper()
	value, err := s.Next(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func TestAtomicSchedulerSelectionAdmissionAndClaimAreDistinctAndReplaySafe(t *testing.T) {
	now := queueTestNow
	store := NewMemoryStore()
	engine, s := atomicSchedulerFixture(t, &now, store, nil)
	request := atomicEnqueue(t, s, now, "attempt", "a", 4)
	if _, err := engine.GetAttempt(context.Background(), "tenant", "attempt"); !errors.Is(err, ErrNotFound) {
		t.Fatal("enqueue admitted quota", err)
	}
	selected := atomicNext(t, s, "selection")
	if selected == nil || selected.Status.State != "admitted" || selected.Status.Decision == nil || !selected.Status.Decision.Allowed {
		t.Fatal(selected)
	}
	info, err := engine.GetAttempt(context.Background(), "tenant", "attempt")
	if err != nil || info.Claim.Fence != 0 || info.Decision.State != "admitted" {
		t.Fatal(info, err)
	}
	balance, err := engine.BudgetBalance(context.Background(), "tenant", engineBudgetKey(t, engine, request.Demand, "token-budget"))
	if err != nil || balance.Held != 4 {
		t.Fatal(balance, err)
	}
	copy, err := NewAtomicScheduler(engine, s.Configuration())
	if err != nil {
		t.Fatal(err)
	}
	if err = copy.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if replay := atomicNext(t, copy, "selection"); !reflect.DeepEqual(replay, selected) {
		t.Fatal("selection replay changed", replay, selected)
	}
	if again := atomicNext(t, copy, "new-selection"); again != nil {
		t.Fatal("selected attempt requeued", again)
	}
	request.Demand.Context.RequestID = "new-correlation"
	status, err := copy.Enqueue(context.Background(), request)
	if err != nil || status.State != "admitted" {
		t.Fatal(status, err)
	}
	changed := request
	changed.Demand.PayloadDigest = strings.Repeat("d", 64)
	if _, err := s.Enqueue(context.Background(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("payload mutation replay accepted", err)
	}
	claim, err := engine.ClaimDispatch(context.Background(), "tenant", "attempt")
	if err != nil || claim.Fence != 1 {
		t.Fatal(claim, err)
	}
	if _, err := engine.ClaimDispatch(context.Background(), "tenant", "attempt"); !errors.Is(err, ErrDispatchClaimed) {
		t.Fatal("selection granted another dispatch", err)
	}
}

func TestAtomicSchedulerCompetingReplicasCannotOverspendOrPickTwice(t *testing.T) {
	now := queueTestNow
	store := NewMemoryStore()
	engine, first := atomicSchedulerFixture(t, &now, store, func(_ *Policy, c *SchedulerConfig) { c.Capacity.Rate = 5; c.Capacity.Burst = 5 })
	secondEngine, err := NewEngine(engine.policy, store, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewAtomicScheduler(secondEngine, first.Configuration())
	if err != nil {
		t.Fatal(err)
	}
	if err = second.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		atomicEnqueue(t, first, now, fmt.Sprintf("work-%02d", i), []string{"a", "b"}[i%2], 1)
	}
	var mu sync.Mutex
	ids := map[string]bool{}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := first
			if i%2 != 0 {
				s = second
			}
			value, err := s.Next(context.Background(), fmt.Sprintf("pick-%d", i))
			if err != nil {
				t.Error(err)
				return
			}
			if value == nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if ids[value.Status.AttemptID] {
				t.Error("duplicate selected attempt", value.Status.AttemptID)
			}
			ids[value.Status.AttemptID] = true
		}(i)
	}
	wg.Wait()
	if len(ids) != 5 {
		t.Fatal("pool capacity was not shared atomically", ids)
	}
	held := int64(0)
	for id := range ids {
		info, err := engine.GetAttempt(context.Background(), "tenant", id)
		if err != nil || !info.Decision.Allowed {
			t.Fatal(info, err)
		}
		held += info.Demand.Bounds["tokens"]
	}
	if held != 5 {
		t.Fatal(held)
	}
	now = now.Add(time.Second)
	if value := atomicNext(t, second, "refill"); value == nil {
		t.Fatal("verified capacity did not refill")
	}
}

type failSchedulerStore struct {
	Store
	fail bool
}

func (s *failSchedulerStore) Update(ctx context.Context, tenant string, fn func(Tx) error) error {
	return s.Store.Update(ctx, tenant, func(tx Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if s.fail {
			return errors.New("injected commit failure")
		}
		return nil
	})
}
func TestAtomicSchedulerFailedCommitRollsBackQueueQuotaAndCapacity(t *testing.T) {
	now := queueTestNow
	store := &failSchedulerStore{Store: NewMemoryStore()}
	engine, s := atomicSchedulerFixture(t, &now, store, nil)
	atomicEnqueue(t, s, now, "work", "a", 3)
	store.fail = true
	if value, err := s.Next(context.Background(), "pick"); err == nil || value != nil {
		t.Fatal("failed transaction exposed admission", value, err)
	}
	status, err := s.Get(context.Background(), "work")
	if err != nil || status.State != "queued" {
		t.Fatal(status, err)
	}
	if _, err = engine.GetAttempt(context.Background(), "tenant", "work"); !errors.Is(err, ErrNotFound) {
		t.Fatal("quota committed outside queue transaction", err)
	}
	store.fail = false
	value := atomicNext(t, s, "pick")
	if value == nil || !value.Status.Decision.Allowed {
		t.Fatal(value)
	}
	if replay := atomicNext(t, s, "pick"); !reflect.DeepEqual(replay, value) {
		t.Fatal(replay, value)
	}
}

func TestAtomicSchedulerQuotaDenialDoesNotSpendFairnessOrPoolCredits(t *testing.T) {
	now := queueTestNow
	engine, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), func(p *Policy, c *SchedulerConfig) {
		p.Concurrency = []ConcurrencyRule{{ID: "active", Scope: Scope{Kind: "tenant"}, Limit: 1}}
		c.Capacity.Rate = 1
		c.Capacity.Burst = 1
	})
	if decision := engineAdmit(t, engine, engineFixtureDemand(now, "already-active", 1)); !decision.Allowed {
		t.Fatal(decision)
	}
	atomicEnqueue(t, s, now, "denied", "a", 1)
	value := atomicNext(t, s, "denied-pick")
	if value == nil || value.Status.State != "denied" || value.Status.Decision.Reason != "concurrency_exhausted" {
		t.Fatal(value)
	}
	if err := s.engine.store.View(context.Background(), "tenant", func(tx Tx) error {
		var state QueueState
		if err := tx.Get(schedulerQueueKind, s.config.ID, &state); err != nil {
			return err
		}
		if state.Deficits["a"] != "0" {
			t.Error("denied request spent fairness", state)
		}
		var rate rateRecord
		if err := tx.Get(rateKind, s.limit("pool", ProtectedRate{}, 0).Key, &rate); !errors.Is(err, ErrNotFound) {
			t.Error("denied request spent pool", rate, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Abort(context.Background(), "tenant", "already-active"); err != nil {
		t.Fatal(err)
	}
	atomicEnqueue(t, s, now, "allowed", "b", 1)
	if value := atomicNext(t, s, "allowed-pick"); value == nil || value.Status.State != "admitted" {
		t.Fatal("denied selection consumed available capacity", value)
	}
}

func TestAtomicSchedulerProtectedPartitionsCannotBeBorrowedAndPriorityIsExplicit(t *testing.T) {
	now := queueTestNow
	_, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), func(_ *Policy, c *SchedulerConfig) {
		c.Capacity.Rate = 4
		c.Capacity.Burst = 4
		c.Protected = map[string]ProtectedRate{"a": {Rate: 2, Burst: 2}}
		c.Priorities = map[string]int{"a": -10, "b": 100}
	})
	for i := 0; i < 4; i++ {
		atomicEnqueue(t, s, now, fmt.Sprintf("bulk-%d", i), "b", 1)
	}
	if value := atomicNext(t, s, "bulk-first"); value == nil || value.Status.FlowID != "b" {
		t.Fatal(value)
	}
	if value := atomicNext(t, s, "bulk-second"); value == nil || value.Status.FlowID != "b" {
		t.Fatal(value)
	}
	if value := atomicNext(t, s, "bulk-cannot-borrow"); value != nil {
		t.Fatal("bulk consumed another flow's protected admission credits", value)
	}
	for i := 0; i < 3; i++ {
		atomicEnqueue(t, s, now, fmt.Sprintf("protected-%d", i), "a", 1)
	}
	for i := 0; i < 2; i++ {
		if value := atomicNext(t, s, fmt.Sprintf("protected-pick-%d", i)); value == nil || value.Status.FlowID != "a" {
			t.Fatal(value)
		}
	}
	if value := atomicNext(t, s, "all-exhausted"); value != nil {
		t.Fatal(value)
	}
	now = now.Add(time.Second)
	// Protected eligible work precedes shared work even though bulk has a higher
	// explicit priority. Once protected work is served, shared priority applies.
	if value := atomicNext(t, s, "refilled-protected"); value == nil || value.Status.FlowID != "a" {
		t.Fatal(value)
	}
	if value := atomicNext(t, s, "refilled-bulk"); value == nil || value.Status.FlowID != "b" {
		t.Fatal(value)
	}
}

func TestAtomicSchedulerCapacityBlockedFlowDoesNotBlockEligibleOtherFlow(t *testing.T) {
	now := queueTestNow
	_, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), func(_ *Policy, c *SchedulerConfig) { c.Capacity.Rate = 5; c.Capacity.Burst = 5 })
	atomicEnqueue(t, s, now, "spent", "a", 2)
	if value := atomicNext(t, s, "spent-pick"); value == nil {
		t.Fatal(value)
	}
	atomicEnqueue(t, s, now, "too-big-now", "a", 4)
	atomicEnqueue(t, s, now, "fits-now", "b", 1)
	value := atomicNext(t, s, "fits-pick")
	if value == nil || value.Status.AttemptID != "fits-now" {
		t.Fatal("blocked flow stalled eligible work", value)
	}
	status, err := s.Get(context.Background(), "too-big-now")
	if err != nil || status.State != "queued" {
		t.Fatal(status, err)
	}
}

func TestAtomicSchedulerDeadlineCancellationAndExpiredAuthorityHoldNoQuota(t *testing.T) {
	now := queueTestNow
	engine, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), nil)
	atomicEnqueue(t, s, now, "cancel", "a", 1)
	status, err := s.Cancel(context.Background(), "cancel")
	if err != nil || status.State != "cancelled" {
		t.Fatal(status, err)
	}
	if value := atomicNext(t, s, "after-cancel"); value != nil {
		t.Fatal(value)
	}
	request := atomicEnqueue(t, s, now, "expire", "a", 1)
	now = request.Deadline
	status, err = s.Get(context.Background(), "expire")
	if err != nil || status.State != "expired" {
		t.Fatal(status, err)
	}
	if value := atomicNext(t, s, "after-expiry"); value != nil {
		t.Fatal(value)
	}
	if _, err = engine.GetAttempt(context.Background(), "tenant", "expire"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired queued item acquired quota", err)
	}
	atomicEnqueue(t, s, now, "authority-expiry", "a", 1)
	now = s.config.Capacity.ExpiresAt
	if value, err := s.Next(context.Background(), "expired-authority"); !errors.Is(err, ErrDenied) || value != nil {
		t.Fatal("expired capacity authority admitted", value, err)
	}
	if _, err = engine.GetAttempt(context.Background(), "tenant", "authority-expiry"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestAtomicSchedulerRejectsCapacityForgeryCrossTenantAndImplicitReset(t *testing.T) {
	now := queueTestNow
	engine, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), nil)
	for _, modify := range []func(*SchedulerConfig){
		func(c *SchedulerConfig) { c.Capacity.Target.Provider = "" },
		func(c *SchedulerConfig) { c.Capacity.TenantID = "other" },
		func(c *SchedulerConfig) { c.Capacity.MeterID = "usd" },
		func(c *SchedulerConfig) { c.Protected = map[string]ProtectedRate{"a": {Rate: 11, Burst: 1}} },
		func(c *SchedulerConfig) { c.Protected = map[string]ProtectedRate{"unknown": {Rate: 1, Burst: 1}} },
		func(c *SchedulerConfig) { c.Protected = map[string]ProtectedRate{"a": {Rate: 1, Burst: 10}} },
	} {
		cfg := s.Configuration()
		modify(&cfg)
		if _, err := NewAtomicScheduler(engine, cfg); err == nil {
			t.Fatal("invalid pool authority or partition accepted", cfg)
		}
	}
	request := ScheduledRequest{Demand: engineFixtureDemand(now, "forged", 1), FlowID: "a", Deadline: now.Add(time.Second)}
	request.Demand.Bounds["tokens"] = 1
	request.Demand.Target.Provider = "other"
	if _, err := s.Enqueue(context.Background(), request); err == nil {
		t.Fatal("foreign provider used attested pool")
	}
	request.Demand.Target.Provider = "example"
	request.Demand.Context.TenantID = "other"
	if _, err := s.Enqueue(context.Background(), request); err == nil {
		t.Fatal("cross-tenant enqueue accepted")
	}
	cfg := s.Configuration()
	cfg.Queue.Generation++
	replacement, err := NewAtomicScheduler(engine, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = replacement.Activate(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatal("generation reset existing queue", err)
	}
	cfg = s.Configuration()
	cfg.ID = "duplicate-capacity"
	duplicate, err := NewAtomicScheduler(engine, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = duplicate.Activate(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatal("pool capacity duplicated across scheduler IDs", err)
	}
}

func TestAtomicSchedulerPausedVisitKeepsCreditWithoutAddingAnotherQuantum(t *testing.T) {
	now := queueTestNow
	_, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), func(_ *Policy, c *SchedulerConfig) {
		c.Queue.Quantum = 10
		c.Queue.MaxCost = 4
		c.Queue.Weights = map[string]uint32{"a": 3, "b": 1}
		c.Capacity.Rate = 4
		c.Capacity.Burst = 4
		c.Protected = map[string]ProtectedRate{"a": {Rate: 1, Burst: 1}}
		c.Priorities = map[string]int{"b": 10}
	})
	for i := 0; i < 3; i++ {
		atomicEnqueue(t, s, now, fmt.Sprintf("a-%d", i), "a", 1)
	}
	for i := 0; i < 2; i++ {
		atomicEnqueue(t, s, now, fmt.Sprintf("b-%d", i), "b", 1)
	}
	for _, step := range []struct{ id, flow string }{{"protected", "a"}, {"shared-b-1", "b"}, {"shared-b-2", "b"}, {"resume-a", "a"}} {
		value := atomicNext(t, s, step.id)
		if value == nil || value.Status.FlowID != step.flow {
			t.Fatal(step, value)
		}
	}
	if err := s.engine.store.View(context.Background(), "tenant", func(tx Tx) error {
		var state schedulerQueueState
		if err := tx.Get(schedulerQueueKind, s.config.ID, &state); err != nil {
			return err
		}
		if state.Deficits["a"] != "28" {
			t.Error("resumed visit gained duplicate service credit", state.Deficits)
		}
		return s.validateQueue(state, now)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAtomicSchedulerWeightedServiceMatchesQueueWithoutCapacityContention(t *testing.T) {
	now := queueTestNow
	_, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), func(_ *Policy, c *SchedulerConfig) {
		c.Queue.MaxEntries = 128
		c.Queue.Weights = map[string]uint32{"a": 3, "b": 1}
		c.Capacity.Rate = 100
		c.Capacity.Burst = 100
	})
	for i := 0; i < 40; i++ {
		atomicEnqueue(t, s, now, fmt.Sprintf("a-%d", i), "a", 1)
		atomicEnqueue(t, s, now, fmt.Sprintf("b-%d", i), "b", 1)
	}
	served := map[string]int{}
	for i := 0; i < 40; i++ {
		value := atomicNext(t, s, fmt.Sprintf("pick-%d", i))
		if value == nil {
			t.Fatal("eligible work vanished")
		}
		served[value.Status.FlowID]++
	}
	if served["a"] != 30 || served["b"] != 10 {
		t.Fatal("atomic composition changed weighted fairness", served)
	}
}

func TestAtomicSchedulerQueuedMembershipIsRevalidatedWithoutCreatingCapacity(t *testing.T) {
	now := queueTestNow
	engine, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), nil)
	request := ScheduledRequest{Demand: engineFixtureDemand(now, "stale", 1), FlowID: "a", Deadline: now.Add(30 * time.Second)}
	request.Demand.Bounds["tokens"] = 1
	request.Demand.Principal.Membership.ExpiresAt = now.Add(time.Second)
	if _, err := s.Enqueue(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	value := atomicNext(t, s, "stale-pick")
	if value == nil || value.Status.State != "denied" || value.Status.Decision.Reason != "membership_invalid" {
		t.Fatal(value)
	}
	info, err := engine.GetAttempt(context.Background(), "tenant", "stale")
	if err != nil || info.Plan.Bounds != nil || info.Decision.Allowed {
		t.Fatal(info, err)
	}
	atomicEnqueue(t, s, now, "fresh", "b", 1)
	if value := atomicNext(t, s, "fresh-pick"); value == nil || !value.Status.Decision.Allowed {
		t.Fatal(value)
	}
}

func TestAtomicSchedulerOwnershipCannotMoveAttemptToAnotherPool(t *testing.T) {
	now := queueTestNow
	engine, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), nil)
	request := atomicEnqueue(t, s, now, "owned", "a", 1)
	cfg := s.Configuration()
	cfg.ID = "other-pool"
	cfg.Queue.PoolID = "other-pool"
	cfg.Capacity.PoolID = "other-pool"
	cfg.Capacity.Target.PoolID = "other-pool"
	other, err := NewAtomicScheduler(engine, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = other.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	request.Demand.Target.PoolID = "other-pool"
	if _, err = other.Enqueue(context.Background(), request); !errors.Is(err, ErrConflict) {
		t.Fatal("same attempt moved between queue pools", err)
	}
	if value := atomicNext(t, s, "owner-pick"); value == nil || value.Status.AttemptID != "owned" {
		t.Fatal(value)
	}
	status, err := s.Cancel(context.Background(), "owned")
	if err != nil || status.State != "admitted" {
		t.Fatal("queued cancellation relabeled admission", status, err)
	}
	info, err := engine.GetAttempt(context.Background(), "tenant", "owned")
	if err != nil || info.Decision.State != "admitted" {
		t.Fatal(info, err)
	}
}

func TestAtomicSchedulerSnapshotsDoNotExposeDemandOrConfigurationAliases(t *testing.T) {
	now := queueTestNow
	_, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), nil)
	request := atomicEnqueue(t, s, now, "snapshot", "a", 2)
	request.Demand.Bounds["tokens"] = 100
	request.Demand.Principal.Membership.Groups[0].ID = "mutated-after-enqueue"
	configuration := s.Configuration()
	configuration.Queue.Weights["a"] = 999
	configuration.Capacity.Rate = 999
	value := atomicNext(t, s, "snapshot-pick")
	if value == nil || value.Demand.Bounds["tokens"] != 2 || value.Demand.Principal.Membership.Groups[0].ID == "mutated-after-enqueue" {
		t.Fatal("caller mutated queued native exposure", value)
	}
	value.Demand.Bounds["tokens"] = 200
	replay := atomicNext(t, s, "snapshot-pick")
	if replay.Demand.Bounds["tokens"] != 2 || s.Configuration().Capacity.Rate == 999 || s.Configuration().Queue.Weights["a"] == 999 {
		t.Fatal("returned snapshots mutated durable state", replay, s.Configuration())
	}
}

func TestAtomicSchedulerProofRenewalCASPreservesSpentCreditsAndQueueHistory(t *testing.T) {
	now := queueTestNow
	_, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), func(_ *Policy, c *SchedulerConfig) { c.Capacity.Rate = 1; c.Capacity.Burst = 1 })
	atomicEnqueue(t, s, now, "before", "a", 1)
	atomicEnqueue(t, s, now, "after", "b", 1)
	original := atomicNext(t, s, "before-pick")
	if original == nil {
		t.Fatal("first admission missing")
	}
	cfg := s.Configuration()
	cfg.Capacity.ID = "renewed-proof"
	cfg.Capacity.Version = "2"
	cfg.Capacity.IssuedAt = now
	cfg.Capacity.ExpiresAt = now.Add(2 * time.Hour)
	renewed, err := NewAtomicScheduler(s.engine, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Digest() == s.Digest() {
		t.Fatal("renewed proof did not change operator configuration digest")
	}
	if err = renewed.Activate(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatal("proof renewal accepted without expected digest", err)
	}
	if err = renewed.Activate(context.Background(), strings.Repeat("0", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("wrong digest renewed proof", err)
	}
	if err = renewed.Activate(context.Background(), s.Digest()); err != nil {
		t.Fatal(err)
	}
	if err = renewed.Activate(context.Background(), s.Digest()); err != nil {
		t.Fatal("idempotent renewal conflicted", err)
	}
	if err = s.Ready(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatal("stale proof generation stayed active", err)
	}
	if value := atomicNext(t, renewed, "after-before-refill"); value != nil {
		t.Fatal("proof renewal reset capacity credits", value)
	}
	if replay := atomicNext(t, renewed, "before-pick"); !reflect.DeepEqual(replay, original) {
		t.Fatal("proof renewal erased immutable selection", replay, original)
	}
	status, err := renewed.Get(context.Background(), "after")
	if err != nil || status.State != "queued" {
		t.Fatal("proof renewal lost queued work", status, err)
	}
	now = now.Add(time.Second)
	if value := atomicNext(t, renewed, "after-refill"); value == nil || value.Status.AttemptID != "after" {
		t.Fatal(value)
	}
	changed := renewed.Configuration()
	changed.Capacity.Rate = 2
	unsafe, err := NewAtomicScheduler(s.engine, changed)
	if err != nil {
		t.Fatal(err)
	}
	if err = unsafe.Activate(context.Background(), renewed.Digest()); !errors.Is(err, ErrConflict) {
		t.Fatal("proof renewal reinterpreted rate state", err)
	}
}

func TestAtomicSchedulerNewQuotaPolicyDoesNotResetQueueOrCapacity(t *testing.T) {
	now := queueTestNow
	engine, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), func(_ *Policy, c *SchedulerConfig) { c.Capacity.Rate = 1; c.Capacity.Burst = 1 })
	atomicEnqueue(t, s, now, "old-policy", "a", 1)
	atomicEnqueue(t, s, now, "new-policy", "b", 1)
	if value := atomicNext(t, s, "old-policy-pick"); value == nil {
		t.Fatal(value)
	}
	policy := engine.policy.Policy()
	policy.Revision++
	policy.Budgets[0].Limit = 1200
	updated := engineFixture(t, policy, engine.clock, engine.store)
	engineActivate(t, updated, engine.policy.Reference().Digest)
	next, err := NewAtomicScheduler(updated, s.Configuration())
	if err != nil {
		t.Fatal(err)
	}
	if next.Digest() != s.Digest() {
		t.Fatal("quota quantity revision reinterpreted scheduler config")
	}
	if err = next.Ready(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatal("scheduler followed new policy without activation", err)
	}
	if err = next.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s.Ready(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatal("old policy scheduler still ready", err)
	}
	if value := atomicNext(t, next, "new-policy-before-refill"); value != nil {
		t.Fatal("new quota policy reset pool rate", value)
	}
	now = now.Add(time.Second)
	value := atomicNext(t, next, "new-policy-after-refill")
	if value == nil || value.Status.Decision.Policy != updated.policy.Reference() {
		t.Fatal("queued work did not use newly active quota policy", value)
	}
	old, err := updated.GetAttempt(context.Background(), "tenant", "old-policy")
	if err != nil || old.Plan.Policy != engine.policy.Reference() {
		t.Fatal("new policy rewrote original hold", old, err)
	}
}

func TestAtomicSchedulerCompactsOnlyQueueIndexAndRetainsDurableAttemptReplay(t *testing.T) {
	now := queueTestNow
	_, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), func(_ *Policy, c *SchedulerConfig) {
		c.Queue.MaxEntries = 2
		c.Queue.MaxReceipts = 2
		c.Capacity.Rate = 100
		c.Capacity.Burst = 100
	})
	var first ScheduledRequest
	for i := 0; i < 30; i++ {
		request := atomicEnqueue(t, s, now, fmt.Sprintf("lifetime-%02d", i), "a", 1)
		if i == 0 {
			first = request
		}
		value := atomicNext(t, s, fmt.Sprintf("selection-%02d", i))
		if value == nil || !value.Status.Decision.Allowed {
			t.Fatal("bounded queue history stopped sustained execution", i, value)
		}
	}
	replay, err := s.Enqueue(context.Background(), first)
	if err != nil || replay.State != "admitted" {
		t.Fatal("compacted queue resurrected attempt", replay, err)
	}
	first.FlowID = "b"
	if _, err = s.Enqueue(context.Background(), first); !errors.Is(err, ErrConflict) {
		t.Fatal("compacted queue lost immutable flow identity", err)
	}
	if value := atomicNext(t, s, "selection-00"); value == nil || value.Status.AttemptID != "lifetime-00" {
		t.Fatal("compaction forgot selection replay", value)
	}
	if err := s.engine.store.View(context.Background(), "tenant", func(tx Tx) error {
		var state schedulerQueueState
		if err := tx.Get(schedulerQueueKind, s.config.ID, &state); err != nil {
			return err
		}
		if len(state.Receipts) != 0 || len(state.Items) != 0 {
			t.Error("terminal receipts accumulated in active queue", state)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAtomicSchedulerFullExpiredQueueCompactsBoundedHistoryBeforeNewEnqueue(t *testing.T) {
	now := queueTestNow
	_, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), func(_ *Policy, c *SchedulerConfig) { c.Queue.MaxEntries = 9; c.Queue.MaxReceipts = 9 })
	var oldest ScheduledRequest
	for i := 0; i < 9; i++ {
		request := atomicEnqueue(t, s, now, fmt.Sprintf("expire-%02d", i), "a", 1)
		if i == 0 {
			oldest = request
		}
	}
	now = now.Add(30 * time.Second)
	atomicEnqueue(t, s, now, "fresh-after-nine-expired", "b", 1)
	for i := 0; i < 9; i++ {
		status, err := s.Get(context.Background(), fmt.Sprintf("expire-%02d", i))
		if err != nil || status.State != "expired" {
			t.Fatal("bounded compaction lost expired proof", i, status, err)
		}
	}
	status, err := s.Enqueue(context.Background(), oldest)
	if err != nil || status.State != "expired" {
		t.Fatal("compaction resurrected expired work", status, err)
	}
	if value := atomicNext(t, s, "after-expired-compaction"); value == nil || value.Status.AttemptID != "fresh-after-nine-expired" {
		t.Fatal("full expired queue deadlocked admission", value)
	}
}

func TestAtomicSchedulerFundingAndFlowMeaningCannotChangeUnderQueuedWork(t *testing.T) {
	t.Run("funding priority", func(t *testing.T) {
		now := queueTestNow
		engine, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), nil)
		atomicEnqueue(t, s, now, "waiting", "a", 1)
		policy := engine.policy.Policy()
		policy.Revision++
		policy.Accounts[0].Priority++
		updated := engineFixture(t, policy, engine.clock, engine.store)
		engineActivate(t, updated, engine.policy.Reference().Digest)
		changed, err := NewAtomicScheduler(updated, s.Configuration())
		if err != nil {
			t.Fatal(err)
		}
		if err = changed.Activate(context.Background(), s.Digest()); !errors.Is(err, ErrConflict) {
			t.Fatal("funding semantics changed under existing queued flow", err)
		}
		if _, err = updated.GetAttempt(context.Background(), "tenant", "waiting"); !errors.Is(err, ErrNotFound) {
			t.Fatal("funding change admitted an old flow", err)
		}
	})
	t.Run("host payer to flow mapping", func(t *testing.T) {
		now := queueTestNow
		engine, s := atomicSchedulerFixture(t, &now, NewMemoryStore(), func(_ *Policy, c *SchedulerConfig) { c.FlowAuthorityDigest = strings.Repeat("a", 64) })
		atomicEnqueue(t, s, now, "waiting", "a", 1)
		config := s.Configuration()
		config.FlowAuthorityDigest = strings.Repeat("b", 64)
		changed, err := NewAtomicScheduler(engine, config)
		if err != nil {
			t.Fatal(err)
		}
		if err = changed.Activate(context.Background(), s.Digest()); !errors.Is(err, ErrConflict) {
			t.Fatal("changed flow authority reused old pending work", err)
		}
		config.FlowAuthorityDigest = "unverified"
		if _, err = NewAtomicScheduler(engine, config); err == nil {
			t.Fatal("malformed host authority digest accepted")
		}
	})
}

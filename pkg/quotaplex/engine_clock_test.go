package quotaplex

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type clockWaitKey struct{}
type clockWaitStore struct {
	Store
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *clockWaitStore) Update(ctx context.Context, tenant string, fn func(Tx) error) error {
	if wait, _ := ctx.Value(clockWaitKey{}).(bool); wait {
		s.once.Do(func() { close(s.started) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.release:
		}
	}
	return s.Store.Update(ctx, tenant, fn)
}
func waitForClockSnapshot(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("operation never reached store contention")
	}
}

func TestEngineAdmissionRefreshesRealClockAfterTransactionContention(t *testing.T) {
	base := time.Now().UTC()
	store := &clockWaitStore{Store: NewMemoryStore(), started: make(chan struct{}), release: make(chan struct{})}
	engine := engineFixture(t, engineFixturePolicy(base), time.Now, store)
	engineActivate(t, engine, "")
	ctx := context.WithValue(context.Background(), clockWaitKey{}, true)
	type outcome struct {
		decision Decision
		err      error
	}
	result := make(chan outcome, 1)
	go func() {
		decision, err := engine.Admit(ctx, engineFixtureDemand(base, "started-first", 1))
		result <- outcome{decision, err}
	}()
	waitForClockSnapshot(t, store.started)
	// The second invocation commits a newer rate timestamp before the original
	// invocation acquires its transaction snapshot. Neither clock actually rewinds.
	newer, err := engine.Admit(context.Background(), engineFixtureDemand(base, "committed-first", 1))
	if err != nil || !newer.Allowed {
		t.Fatal(newer, err)
	}
	close(store.release)
	select {
	case got := <-result:
		if got.err != nil || !got.decision.Allowed {
			t.Fatal("lock wait appeared to rewind a healthy real clock", got.decision, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("contended admission did not complete")
	}
}

func TestEngineDispatchClaimChecksFreshAuthorityAfterTransactionContention(t *testing.T) {
	base := queueTestNow
	var stamp atomic.Int64
	stamp.Store(base.UnixNano())
	clock := func() time.Time { return time.Unix(0, stamp.Load()).UTC() }
	store := &clockWaitStore{Store: NewMemoryStore(), started: make(chan struct{}), release: make(chan struct{})}
	engine := engineFixture(t, engineFixturePolicy(base), clock, store)
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(base, "claim-after-expiry", 1)
	if decision := engineAdmit(t, engine, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	ctx := context.WithValue(context.Background(), clockWaitKey{}, true)
	result := make(chan error, 1)
	go func() { _, err := engine.ClaimDispatch(ctx, "tenant", demand.AttemptID); result <- err }()
	waitForClockSnapshot(t, store.started)
	stamp.Store(demand.Principal.Membership.ExpiresAt.UnixNano())
	close(store.release)
	select {
	case err := <-result:
		if !errors.Is(err, ErrDenied) {
			t.Fatal("expired membership claimed using pre-contention time", err)
		}
	case <-time.After(time.Second):
		t.Fatal("contended claim did not complete")
	}
	info, err := engine.GetAttempt(context.Background(), "tenant", demand.AttemptID)
	if err != nil || info.Claim.Fence != 0 || info.Decision.State != "admitted" {
		t.Fatal("failed freshness check changed dispatch ownership", info, err)
	}
}

func TestAtomicSchedulerRefreshesRealClockUnderCompetingQueueMutations(t *testing.T) {
	base := time.Now().UTC()
	store := NewMemoryStore()
	policy := engineFixturePolicy(base)
	policy.Rates = nil
	policy.Concurrency = nil
	engine := engineFixture(t, policy, time.Now, store)
	engineActivate(t, engine, "")
	cfg := SchedulerConfig{ID: "real-clock", Queue: QueuePolicy{TenantID: "tenant", PoolID: "provider-capacity", Generation: 1, MeterID: "tokens", MaxEntries: 64, MaxReceipts: 128, MaxWait: time.Minute, MaxCost: 10, Quantum: 1, Weights: map[string]uint32{"a": 1, "b": 1}}, Capacity: CapacityAttestation{ID: "capacity", SourceRef: "operator-verified", Version: "1", TenantID: "tenant", PoolID: "provider-capacity", MeterID: "tokens", Target: Match{Provider: "example", PoolID: "provider-capacity"}, Rate: 100, Burst: 100, Interval: time.Second, IssuedAt: base.Add(-time.Minute), ExpiresAt: base.Add(time.Hour)}}
	scheduler, err := NewAtomicScheduler(engine, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = scheduler.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := base.Add(time.Duration(i)).Format(time.RFC3339Nano)
			request := ScheduledRequest{Demand: engineFixtureDemand(base, id, 1), FlowID: []string{"a", "b"}[i%2], Deadline: base.Add(30 * time.Second)}
			if _, err := scheduler.Enqueue(context.Background(), request); err != nil {
				t.Error("real queue clock failed during contention", err)
				return
			}
			if _, err := scheduler.Next(context.Background(), id); err != nil {
				t.Error("real admission clock failed during contention", err)
			}
		}(i)
	}
	wg.Wait()
	// Standalone FairQueue still rejects a genuine rewind; AtomicScheduler must
	// sample fresh time, rather than mask rewinds by clamping to persisted state.
	q, err := NewFairQueue(cfg.Queue)
	if err != nil {
		t.Fatal(err)
	}
	state, err := q.Enqueue(q.NewState(), QueueRequest{ID: "rewind", FlowID: "a", DemandDigest: strings.Repeat("a", 64), Cost: 1, Deadline: base.Add(time.Second)}, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = q.Pick(state, base.Add(-time.Nanosecond)); err == nil {
		t.Fatal("clock refresh disabled genuine rewind protection")
	}
}

func TestAtomicSchedulerStillRejectsAGenuinelyRewoundAccountingClock(t *testing.T) {
	now := queueTestNow
	_, scheduler := atomicSchedulerFixture(t, &now, NewMemoryStore(), nil)
	atomicEnqueue(t, scheduler, now, "rewind", "a", 1)
	now = now.Add(-time.Nanosecond)
	if value, err := scheduler.Next(context.Background(), "rewound-pick"); err == nil || value != nil {
		t.Fatal("fresh clock sampling masked an actual rewind", value, err)
	}
}

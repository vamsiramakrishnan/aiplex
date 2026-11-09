package quotaplex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
)

func engineFixturePolicy(now time.Time) Policy {
	return Policy{
		Version: Version, TenantID: "tenant", ID: "combined", Revision: 1, EffectiveAt: now.Add(-time.Hour),
		Meters: map[string]Meter{
			"requests": {ID: "requests", Unit: "requests"},
			"tokens":   {ID: "tokens", Unit: "tokens", Billable: true},
			"usd":      {ID: "usd", Unit: "money", Currency: "USD", Scale: 6},
		},
		PriceBooks: []PriceBook{{ID: "prices", Revision: 1, Prices: []UnitPrice{{MeterID: "tokens", MoneyMeterID: "usd", Numerator: 3, Denominator: 2}}}},
		Accounts:   []Account{{ID: "group-budget", TenantWide: true}},
		Budgets: []BudgetRule{
			{ID: "token-budget", Scope: Scope{Kind: "account"}, MeterID: "tokens", Limit: 100, Period: Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(23 * time.Hour)}},
			{ID: "spend-budget", Scope: Scope{Kind: "tenant"}, MeterID: "usd", Limit: 1000, Period: Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(23 * time.Hour)}},
		},
		Rates:       []RateRule{{ID: "arrival", Scope: Scope{Kind: "tenant"}, MeterID: "requests", Rate: 2, Interval: time.Second, Burst: 2, Basis: "admitted"}},
		Concurrency: []ConcurrencyRule{{ID: "active", Scope: Scope{Kind: "tenant"}, Limit: 2}},
	}
}

func engineFixtureDemand(now time.Time, attempt string, tokens int64) Demand {
	ref := contracts.SnapshotRef{ID: "principal-membership", Version: "1"}
	return Demand{
		Version:            Version,
		Context:            contracts.RequestContext{Version: contracts.Version, TenantID: "tenant", RequestID: "correlation-original", PrincipalID: "human", MembershipSnapshotRef: &ref, ResourceID: "model"},
		Principal:          Subject{ID: "human", Kind: "human", Membership: Membership{Ref: ref, TenantID: "tenant", SubjectID: "human", Groups: []GroupRef{{Source: "directory", ID: "engineering"}, {Source: "directory", ID: "research"}}, IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}},
		LogicalOperationID: "logical", AttemptID: attempt, PayloadDigest: strings.Repeat("a", 64),
		Target: Target{ConfigurationDigest: strings.Repeat("c", 64), Plane: "llmplex", Provider: "example", ResourceID: "model", Operation: "generate", PoolID: "provider-capacity", BillingAccount: "provider-project", Region: "global", ModelVersion: "resolved-v1"},
		Bounds: Vector{"requests": 1, "tokens": tokens},
	}
}

func engineFixture(t *testing.T, policy Policy, clock func() time.Time, store Store) *Engine {
	t.Helper()
	compiled, err := Compile(policy)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(compiled, store, clock)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func engineActivate(t *testing.T, engine *Engine, expected string) {
	t.Helper()
	if err := engine.Activate(context.Background(), expected); err != nil {
		t.Fatal(err)
	}
}

func engineAdmit(t *testing.T, engine *Engine, demand Demand) Decision {
	t.Helper()
	decision, err := engine.Admit(context.Background(), demand)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func engineBudgetKey(t *testing.T, engine *Engine, demand Demand, ruleID string) string {
	t.Helper()
	plan, err := engine.policy.Resolve(demand, engine.clock())
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range plan.Budgets {
		if budget.RuleID == ruleID {
			return budget.Key
		}
	}
	t.Fatal("missing budget", ruleID)
	return ""
}

func TestEngineAdmissionChecksAreAtomicAndRatesAreNotRefunded(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	policy := engineFixturePolicy(now)
	policy.Rates[0].Burst, policy.Rates[0].Rate = 1, 1
	policy.Concurrency[0].Limit = 1
	store := NewMemoryStore()
	engine := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "first", 80)
	if decision := engineAdmit(t, engine, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	denied := engineAdmit(t, engine, engineFixtureDemand(now, "denied", 1))
	if denied.Allowed || denied.Reason != "rate_exhausted" {
		t.Fatal(denied)
	}
	key := engineBudgetKey(t, engine, demand, "token-budget")
	balance, err := engine.BudgetBalance(context.Background(), "tenant", key)
	if err != nil || balance.Held != 80 || balance.Consumed != 0 {
		t.Fatalf("denial changed earlier control checks: %+v %v", balance, err)
	}
	if err := engine.Abort(context.Background(), "tenant", "first"); err != nil {
		t.Fatal(err)
	}
	if decision := engineAdmit(t, engine, engineFixtureDemand(now, "still-rate-limited", 1)); decision.Allowed || decision.Reason != "rate_exhausted" {
		t.Fatal("abort refunded accepted arrival rate", decision)
	}
	now = now.Add(time.Second)
	if decision := engineAdmit(t, engine, engineFixtureDemand(now, "after-refill", 100)); !decision.Allowed {
		t.Fatal(decision)
	}
	if old := engineAdmit(t, engine, engineFixtureDemand(now.Add(-time.Second), "denied", 1)); old != denied {
		t.Fatal("denied attempt was re-evaluated after availability changed", old, denied)
	}
}

func TestEngineConcurrentReplicasShareOneBudget(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	policy := engineFixturePolicy(now)
	policy.Rates, policy.Concurrency = nil, nil
	store := NewMemoryStore()
	first := engineFixture(t, policy, func() time.Time { return now }, store)
	second := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, first, "")
	var accepted atomic.Int64
	var workers sync.WaitGroup
	for i := 0; i < 80; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			engine := first
			if i%2 == 1 {
				engine = second
			}
			decision, err := engine.Admit(context.Background(), engineFixtureDemand(now, fmt.Sprintf("attempt-%d", i), 2))
			if err != nil {
				t.Error(err)
			} else if decision.Allowed {
				accepted.Add(1)
			}
		}(i)
	}
	workers.Wait()
	if accepted.Load() != 50 {
		t.Fatal("concurrent overspend or false loss", accepted.Load())
	}
	key := engineBudgetKey(t, first, engineFixtureDemand(now, "lookup", 1), "token-budget")
	balance, err := first.BudgetBalance(context.Background(), "tenant", key)
	if err != nil || balance.Held != 100 || balance.Available != 0 {
		t.Fatal(balance, err)
	}
}

func TestEngineReplayUsesAttemptIdentityAndPinnedDecision(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	policy := engineFixturePolicy(now)
	store := NewMemoryStore()
	engine := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "attempt", 10)
	original := engineAdmit(t, engine, demand)
	replay := policyCopy(demand)
	replay.Context.RequestID = "new-transport-correlation"
	replay.Principal.Membership.Groups[0], replay.Principal.Membership.Groups[1] = replay.Principal.Membership.Groups[1], replay.Principal.Membership.Groups[0]
	if got := engineAdmit(t, engine, replay); got != original {
		t.Fatal("correlation or group ordering changed replay", got)
	}
	changed := policyCopy(demand)
	changed.Bounds["tokens"] = 11
	if _, err := engine.Admit(context.Background(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed exposure replay accepted", err)
	}
	changed = policyCopy(demand)
	changed.PayloadDigest = strings.Repeat("b", 64)
	if _, err := engine.Admit(context.Background(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed provider payload replay accepted", err)
	}
	policy.Revision = 2
	policy.Budgets[0].Limit = 5
	updated := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, updated, engine.policy.Reference().Digest)
	now = now.Add(2 * time.Hour) // original membership is now expired
	if got := engineAdmit(t, updated, demand); got != original {
		t.Fatal("replay was evaluated against new policy or expired membership", got)
	}
	newDemand := engineFixtureDemand(now, "new-attempt", 1)
	if got := engineAdmit(t, engine, newDemand); got.Allowed || got.Reason != "policy_inactive" {
		t.Fatal("stale replica admitted using old policy", got)
	}
	newDemand.AttemptID = "new-attempt-active-policy"
	if got := engineAdmit(t, updated, newDemand); got.Allowed || got.Reason != "budget_exhausted" {
		t.Fatal("policy reduction reset outstanding holds", got)
	}
	key := originalBudgetKey(t, engine, demand, "token-budget")
	balance, err := updated.BudgetBalance(context.Background(), "tenant", key)
	if err != nil || balance.LimitPolicy != original.Policy {
		t.Fatal("historical balance limit lost its policy provenance", balance, err)
	}
}

func originalBudgetKey(t *testing.T, engine *Engine, demand Demand, rule string) string {
	t.Helper()
	info, err := engine.GetAttempt(context.Background(), demand.Context.TenantID, demand.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range info.Plan.Budgets {
		if budget.RuleID == rule {
			return budget.Key
		}
	}
	t.Fatal("missing pinned budget", rule)
	return ""
}

func TestEngineActivationRequiresCASAndPreservesLineagesAndPrices(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	policy := engineFixturePolicy(now)
	store := NewMemoryStore()
	engine := engineFixture(t, policy, func() time.Time { return now }, store)
	demand := engineFixtureDemand(now, "before-activation", 1)
	if got := engineAdmit(t, engine, demand); got.Allowed || got.Reason != "policy_inactive" {
		t.Fatal(got)
	}
	engineActivate(t, engine, "")
	if got := engineAdmit(t, engine, demand); got.Allowed {
		t.Fatal("old denied identity was reused after activation", got)
	}
	for _, change := range []struct {
		name string
		edit func(*Policy)
	}{
		{"scope reset", func(p *Policy) { p.Budgets[0].Scope.Kind = "principal" }},
		{"period reset", func(p *Policy) { p.Budgets[0].Period.End = p.Budgets[0].Period.End.Add(time.Hour) }},
		{"price revision reuse", func(p *Policy) { p.PriceBooks[0].Prices[0].Numerator = 100 }},
		{"policy identity replacement", func(p *Policy) { p.ID = "new-policy-id" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			next := policyCopy(policy)
			next.Revision = 2
			change.edit(&next)
			updated := engineFixture(t, next, func() time.Time { return now }, store)
			if err := updated.Activate(context.Background(), engine.policy.Reference().Digest); !errors.Is(err, ErrConflict) {
				t.Fatal("structural state reset accepted", err)
			}
			if err := engine.Ready(context.Background()); err != nil {
				t.Fatal("failed activation replaced the old head", err)
			}
		})
	}
	next := policyCopy(policy)
	next.Revision, next.Budgets[0].Limit = 2, 50
	updated := engineFixture(t, next, func() time.Time { return now }, store)
	if err := updated.Activate(context.Background(), "incorrect-head"); !errors.Is(err, ErrConflict) {
		t.Fatal("activation ignored CAS", err)
	}
	engineActivate(t, updated, engine.policy.Reference().Digest)
}

func TestEngineFractionalRateCreditHasNoRoundingLeak(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	base := now
	policy := engineFixturePolicy(now)
	policy.Budgets, policy.Concurrency = nil, nil
	policy.Rates[0].Rate, policy.Rates[0].Burst = 3, 1
	engine := engineFixture(t, policy, func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	if got := engineAdmit(t, engine, engineFixtureDemand(base, "first", 1)); !got.Allowed {
		t.Fatal(got)
	}
	now = base.Add(100 * time.Millisecond)
	if got := engineAdmit(t, engine, engineFixtureDemand(base, "too-early", 1)); got.Allowed || got.RetryAfter != 233333334*time.Nanosecond {
		t.Fatal(got)
	}
	now = base.Add(333333333 * time.Nanosecond)
	if got := engineAdmit(t, engine, engineFixtureDemand(base, "still-early", 1)); got.Allowed || got.RetryAfter != time.Nanosecond {
		t.Fatal(got)
	}
	now = base.Add(333333334 * time.Nanosecond)
	if got := engineAdmit(t, engine, engineFixtureDemand(base, "ready", 1)); !got.Allowed {
		t.Fatal(got)
	}
	now = base.Add(-time.Nanosecond)
	if _, err := engine.Admit(context.Background(), engineFixtureDemand(base, "clock-regression", 1)); !errors.Is(err, ErrConflict) {
		t.Fatal("backward time minted capacity", err)
	}
}

type engineLostReplyStore struct {
	Store
	lose bool
}

func (s *engineLostReplyStore) Update(ctx context.Context, tenant string, callback func(Tx) error) error {
	if err := s.Store.Update(ctx, tenant, callback); err != nil {
		return err
	}
	if s.lose {
		s.lose = false
		return errors.New("commit reply lost")
	}
	return nil
}

func TestEngineRecoversAnAmbiguousAdmissionCommitWithoutAnotherHold(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := &engineLostReplyStore{Store: NewMemoryStore()}
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "attempt", 10)
	store.lose = true
	if result, err := engine.Admit(context.Background(), demand); err == nil || result.Allowed {
		t.Fatal("ambiguous response reported success", result, err)
	}
	if got := engineAdmit(t, engine, demand); !got.Allowed {
		t.Fatal(got)
	}
	key := engineBudgetKey(t, engine, demand, "token-budget")
	balance, err := engine.BudgetBalance(context.Background(), "tenant", key)
	if err != nil || balance.Held != 10 {
		t.Fatal("recovery created another hold", balance, err)
	}
}

func TestEngineReadAndLifecycleStayBoundToCompiledTenant(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	first := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, store)
	engineActivate(t, first, "")
	otherPolicy := engineFixturePolicy(now)
	otherPolicy.TenantID = "other"
	other := engineFixture(t, otherPolicy, func() time.Time { return now }, store)
	engineActivate(t, other, "")
	demand := engineFixtureDemand(now, "shared-id", 10)
	demand.Context.TenantID, demand.Principal.Membership.TenantID = "other", "other"
	if decision := engineAdmit(t, other, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	if _, err := first.GetAttempt(context.Background(), "other", demand.AttemptID); err == nil {
		t.Fatal("read crossed engine tenant")
	}
	if _, err := first.BudgetBalance(context.Background(), "other", "any"); err == nil {
		t.Fatal("balance read crossed engine tenant")
	}
	if _, err := first.ClaimDispatch(context.Background(), "other", demand.AttemptID); err == nil {
		t.Fatal("claim crossed engine tenant")
	}
	if err := first.Abort(context.Background(), "other", demand.AttemptID); err == nil {
		t.Fatal("abort crossed engine tenant")
	}
	claim, err := other.ClaimDispatch(context.Background(), "other", demand.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.MarkUnknown(context.Background(), claim, "timeout"); err == nil {
		t.Fatal("unknown transition crossed engine tenant")
	}
	if err := first.RecordProviderResource(context.Background(), claim, "provider-job"); err == nil {
		t.Fatal("provider correlation crossed engine tenant")
	}
	if _, err := first.Settle(context.Background(), Settlement{TenantID: "other", AttemptID: demand.AttemptID, EventID: "event", Claim: claim, Usage: demand.Bounds}); err == nil {
		t.Fatal("settlement crossed engine tenant")
	}
}

type engineFailCreateTx struct {
	Tx
	kind string
}

func (t engineFailCreateTx) Create(kind, id string, value any) error {
	if kind == t.kind {
		return errors.New("fixture immutable journal write failed")
	}
	return t.Tx.Create(kind, id, value)
}

type engineFailCreateStore struct {
	Store
	failKind string
}

func (s *engineFailCreateStore) Update(ctx context.Context, tenant string, callback func(Tx) error) error {
	return s.Store.Update(ctx, tenant, func(tx Tx) error { return callback(engineFailCreateTx{Tx: tx, kind: s.failKind}) })
}

func TestEngineJournalFailureRollsBackEveryControlAndTerminalMutation(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := &engineFailCreateStore{Store: NewMemoryStore()}
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "attempt", 10)
	key := engineBudgetKey(t, engine, demand, "token-budget")
	store.failKind = ledgerKind
	if decision, err := engine.Admit(context.Background(), demand); err == nil || decision.Allowed {
		t.Fatal("failed journal write reported admission", decision, err)
	}
	if _, err := engine.GetAttempt(context.Background(), "tenant", demand.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed admission retained a replay result", err)
	}
	if _, err := engine.BudgetBalance(context.Background(), "tenant", key); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed admission retained budget state", err)
	}
	store.failKind = ""
	claim := lifecycleClaim(t, engine, demand)
	store.failKind = ledgerKind
	settlement := Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "terminal", Claim: claim, Usage: Vector{"requests": 1, "tokens": 5}}
	if decision, err := engine.Settle(context.Background(), settlement); err == nil || decision.State == "settled" {
		t.Fatal("failed terminal journal reported acknowledgement", decision, err)
	}
	info, err := engine.GetAttempt(context.Background(), "tenant", demand.AttemptID)
	if err != nil || info.Decision.State != "dispatching" {
		t.Fatal("failed terminal journal changed attempt", info, err)
	}
	balance, err := engine.BudgetBalance(context.Background(), "tenant", key)
	if err != nil || balance.Held != 10 || balance.Consumed != 0 {
		t.Fatal("failed terminal journal partially released exposure", balance, err)
	}
	store.failKind = ""
	if _, err := engine.Settle(context.Background(), settlement); err != nil {
		t.Fatal("rolled-back settlement could not be retried", err)
	}
}

type engineRetryStore struct {
	Store
	retry   bool
	between func() error
}

func (s *engineRetryStore) Update(ctx context.Context, tenant string, callback func(Tx) error) error {
	if !s.retry {
		return s.Store.Update(ctx, tenant, callback)
	}
	s.retry = false
	aborted := errors.New("fixture transaction retried")
	err := s.Store.Update(ctx, tenant, func(tx Tx) error {
		if err := callback(tx); err != nil {
			return err
		}
		return aborted
	})
	if !errors.Is(err, aborted) {
		return err
	}
	if err := s.between(); err != nil {
		return err
	}
	return s.Store.Update(ctx, tenant, callback)
}

func TestEngineTransactionRetryReturnsOnlyTheCommittedDecision(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	policy := engineFixturePolicy(now)
	policy.Rates, policy.Concurrency = nil, nil
	base := NewMemoryStore()
	store := &engineRetryStore{Store: base}
	engine := engineFixture(t, policy, func() time.Time { return now }, store)
	competitor := engineFixture(t, policy, func() time.Time { return now }, base)
	engineActivate(t, engine, "")
	store.between = func() error {
		decision, err := competitor.Admit(context.Background(), engineFixtureDemand(now, "winner", 100))
		if err == nil && !decision.Allowed {
			return errors.New("competing admission unexpectedly denied")
		}
		return err
	}
	store.retry = true
	decision := engineAdmit(t, engine, engineFixtureDemand(now, "retried", 1))
	if decision.Allowed || decision.Reason != "budget_exhausted" {
		t.Fatal("result escaped from aborted callback", decision)
	}
	info, err := engine.GetAttempt(context.Background(), "tenant", "retried")
	if err != nil || info.Decision != decision {
		t.Fatal("response disagrees with committed replay record", info, err)
	}
}

func TestEngineActivationAndReadinessRequireAnEffectivePolicyAndTrustedClock(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	policy := engineFixturePolicy(now)
	policy.EffectiveAt, policy.ExpiresAt = now.Add(time.Hour), now.Add(2*time.Hour)
	engine := engineFixture(t, policy, func() time.Time { return now }, NewMemoryStore())
	if err := engine.Activate(context.Background(), ""); !errors.Is(err, ErrDenied) {
		t.Fatal("future policy replaced active admission before its effective time", err)
	}
	now = now.Add(time.Hour)
	engineActivate(t, engine, "")
	if err := engine.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err := engine.Ready(context.Background()); !errors.Is(err, ErrDenied) {
		t.Fatal("expired policy advertised readiness", err)
	}
	now = time.Time{}
	if err := engine.Ready(context.Background()); err == nil {
		t.Fatal("invalid clock advertised readiness")
	}
}

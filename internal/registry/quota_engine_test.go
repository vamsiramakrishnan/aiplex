package registry

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// Exercise the real engine through the real Firestore SDK transaction transport,
// rather than reproducing its accounting by writing fixture records directly.
func TestFirestoreQuotaEngineRecoversAndSettlesPinnedUsageOnce(t *testing.T) {
	registry, wire := quotaWireFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	now := start
	clock := func() time.Time { return now }
	engineering := quotaplex.GroupRef{Source: "workforce", ID: "engineering"}
	research := quotaplex.GroupRef{Source: "workforce", ID: "research"}
	policy := quotaplex.Policy{
		Version: quotaplex.Version, TenantID: "tenant", ID: "shared-controls", Revision: 1, EffectiveAt: start.Add(-time.Hour),
		Meters: map[string]quotaplex.Meter{
			"requests": {ID: "requests", Unit: "requests"},
			"tokens":   {ID: "tokens", Unit: "tokens", Billable: true},
			"usd":      {ID: "usd", Unit: "money", Currency: "USD", Scale: 6},
		},
		PriceBooks: []quotaplex.PriceBook{{ID: "native-prices", Revision: 1, Prices: []quotaplex.UnitPrice{{MeterID: "tokens", MoneyMeterID: "usd", Numerator: 3, Denominator: 2}}}},
		Accounts: []quotaplex.Account{
			{ID: "engineering", Priority: 2, Eligible: []quotaplex.Eligibility{{SubjectRole: "principal", Group: &engineering}}},
			{ID: "research", Priority: 1, Eligible: []quotaplex.Eligibility{{SubjectRole: "principal", Group: &research}}},
		},
		Budgets: []quotaplex.BudgetRule{
			{ID: "account-tokens", Scope: quotaplex.Scope{Kind: "account"}, MeterID: "tokens", Limit: 100, Period: quotaplex.Period{Kind: "fixed", Start: start.Add(-time.Hour), End: start.Add(23 * time.Hour)}},
			{ID: "human-tokens", Scope: quotaplex.Scope{Kind: "principal"}, MeterID: "tokens", Limit: 40, Period: quotaplex.Period{Kind: "fixed", Start: start.Add(-24 * time.Hour), End: start.Add(6 * 24 * time.Hour)}},
			{ID: "agent-tokens", Scope: quotaplex.Scope{Kind: "actor"}, MeterID: "tokens", Limit: 60, Period: quotaplex.Period{Kind: "anchored", Anchor: start.Add(-time.Hour), Duration: 2 * time.Hour}},
			{ID: "tenant-money", Scope: quotaplex.Scope{Kind: "tenant"}, MeterID: "usd", Limit: 1000, Period: quotaplex.Period{Kind: "fixed", Start: start.Add(-time.Hour), End: start.Add(23 * time.Hour)}},
		},
		Rates:       []quotaplex.RateRule{{ID: "requests", Scope: quotaplex.Scope{Kind: "tenant"}, MeterID: "requests", Rate: 1, Interval: time.Second, Burst: 2, Basis: "admitted"}},
		Concurrency: []quotaplex.ConcurrencyRule{{ID: "active", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 1}},
	}
	compiled, err := quotaplex.Compile(policy)
	if err != nil {
		t.Fatal(err)
	}
	newEngine := func(compiled *quotaplex.CompiledPolicy) *quotaplex.Engine {
		t.Helper()
		engine, err := quotaplex.NewEngine(compiled, registry.QuotaStore(), clock)
		if err != nil {
			t.Fatal(err)
		}
		return engine
	}
	engine := newEngine(compiled)
	if err := engine.Activate(ctx, ""); err != nil {
		t.Fatal(err)
	}
	principalRef := contracts.SnapshotRef{ID: "human-membership", Version: "1"}
	demand := quotaplex.Demand{
		Version:            quotaplex.Version,
		Context:            contracts.RequestContext{Version: contracts.Version, TenantID: "tenant", RequestID: "correlation", PrincipalID: "human", ActorID: "agent", MembershipSnapshotRef: &principalRef, ResourceID: "native-model"},
		Principal:          quotaplex.Subject{ID: "human", Kind: "human", Membership: quotaplex.Membership{Ref: principalRef, TenantID: "tenant", SubjectID: "human", Groups: []quotaplex.GroupRef{engineering, research}, IssuedAt: start.Add(-time.Hour), ExpiresAt: start.Add(time.Hour)}},
		Actor:              &quotaplex.Subject{ID: "agent", Kind: "agent", Membership: quotaplex.Membership{Ref: contracts.SnapshotRef{ID: "agent-membership", Version: "1"}, TenantID: "tenant", SubjectID: "agent", IssuedAt: start.Add(-time.Hour), ExpiresAt: start.Add(time.Hour)}},
		LogicalOperationID: "operation", AttemptID: "attempt", PayloadDigest: strings.Repeat("a", 64),
		Target: quotaplex.Target{ConfigurationDigest: strings.Repeat("c", 64), Plane: "llmplex", Provider: "native", ResourceID: "native-model", Operation: "generate", PoolID: "native-capacity", BillingAccount: "provider-project", Region: "global", ModelVersion: "resolved-model-v1"},
		Bounds: quotaplex.Vector{"requests": 1, "tokens": 20},
	}
	decision, err := engine.Admit(ctx, demand)
	if err != nil || !decision.Allowed || decision.PayerID != "engineering" {
		t.Fatalf("admit=%+v error=%v", decision, err)
	}
	attempt, err := engine.GetAttempt(ctx, "tenant", "attempt")
	if err != nil || len(attempt.Plan.Budgets) != 4 || len(attempt.Plan.Rates) != 1 || len(attempt.Plan.Concurrency) != 1 {
		t.Fatalf("stored plan=%+v error=%v", attempt.Plan, err)
	}
	for _, budget := range attempt.Plan.Budgets {
		balance, err := engine.BudgetBalance(ctx, "tenant", budget.Key)
		want := int64(20)
		if budget.MeterID == "usd" {
			want = 30
		}
		if err != nil || balance.Held != want || balance.Consumed != 0 {
			t.Fatalf("held=%+v error=%v", balance, err)
		}
	}
	blocked := demand
	blocked.AttemptID = "blocked-by-active-work"
	if denied, err := engine.Admit(ctx, blocked); err != nil || denied.Allowed || denied.Reason != "concurrency_exhausted" {
		t.Fatalf("concurrency denial=%+v error=%v", denied, err)
	}
	if err := registry.QuotaStore().View(ctx, "tenant", func(tx quotaplex.Tx) error {
		var rate struct {
			Available string `json:"available"`
		}
		if err := tx.Get("rate", attempt.Plan.Rates[0].Key, &rate); err != nil {
			return err
		}
		if rate.Available != "1" {
			t.Fatalf("concurrency denial consumed prior rate capacity: %q", rate.Available)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	claim, err := engine.ClaimDispatch(ctx, "tenant", "attempt")
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RecordProviderResource(ctx, claim, "native-result"); err != nil {
		t.Fatal(err)
	}
	if err := engine.RecordProviderResource(ctx, claim, "native-result"); err != nil {
		t.Fatal("provider resource replay", err)
	}
	if err := engine.MarkUnknown(ctx, claim, "terminal response lost"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Abort(ctx, "tenant", "attempt"); !errors.Is(err, quotaplex.ErrInvalidTransition) {
		t.Fatal("unknown work was released", err)
	}

	// Activate a more expensive price revision and recreate both engine and
	// backend adapter. The old attempt must retain its original prices/windows.
	policy.Revision, policy.PriceBooks[0].Revision = 2, 2
	policy.PriceBooks[0].Prices[0].Numerator = 30
	next, err := quotaplex.Compile(policy)
	if err != nil {
		t.Fatal(err)
	}
	now = start.Add(2 * time.Hour) // old membership and actor window have expired
	recovered := newEngine(next)
	if err := recovered.Activate(ctx, compiled.Reference().Digest); err != nil {
		t.Fatal(err)
	}
	unknown, err := recovered.GetAttempt(ctx, "tenant", "attempt")
	if err != nil || unknown.Decision.State != "unknown" || unknown.ProviderResource != "native-result" || unknown.Claim != claim {
		t.Fatalf("recovered unknown work=%+v error=%v", unknown, err)
	}
	settlement := quotaplex.Settlement{TenantID: "tenant", AttemptID: "attempt", EventID: "terminal-event", Claim: claim, Usage: quotaplex.Vector{"requests": 1, "tokens": 7}, ProviderResourceID: "native-result"}
	settled, err := recovered.Settle(ctx, settlement)
	if err != nil || settled.State != "settled" || settled.Policy != compiled.Reference() {
		t.Fatalf("settle=%+v error=%v", settled, err)
	}
	if replay, err := recovered.Settle(ctx, settlement); err != nil || replay != settled {
		t.Fatalf("settlement replay=%+v error=%v", replay, err)
	}
	if replay, err := recovered.Admit(ctx, demand); err != nil || replay != settled {
		t.Fatalf("admission replay=%+v error=%v", replay, err)
	}
	if _, err := recovered.ClaimDispatch(ctx, "tenant", "attempt"); !errors.Is(err, quotaplex.ErrDispatchClaimed) {
		t.Fatal("replay granted a new dispatch", err)
	}
	settlement.Usage = quotaplex.Vector{"requests": 1, "tokens": 8}
	if _, err := recovered.Settle(ctx, settlement); !errors.Is(err, quotaplex.ErrConflict) {
		t.Fatal("changed settlement replay accepted", err)
	}
	for _, budget := range attempt.Plan.Budgets {
		balance, err := recovered.BudgetBalance(ctx, "tenant", budget.Key)
		want := int64(7)
		if budget.MeterID == "usd" {
			want = 11
		} // ceil(7*3/2), not the new price
		if err != nil || balance.Held != 0 || balance.Consumed != want {
			t.Fatalf("pinned settlement=%+v error=%v", balance, err)
		}
	}
	actual, err := recovered.GetAttempt(ctx, "tenant", "attempt")
	if err != nil || actual.Actual["usd"] != 11 || actual.Actual["tokens"] != 7 {
		t.Fatalf("stored actual=%+v error=%v", actual.Actual, err)
	}
	if err := registry.QuotaStore().View(ctx, "tenant", func(tx quotaplex.Tx) error {
		ledger, err := tx.List("ledger")
		if err != nil {
			return err
		}
		if len(ledger) != 5 {
			t.Fatalf("expected reserve/dispatch/resource/unknown/consume once; ledger=%d", len(ledger))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A fresh operation can now acquire the released concurrency permit. Its
	// subject snapshots are current, while the old attempt's history stays pinned.
	fresh := demand
	fresh.AttemptID = "after-reconciliation"
	fresh.Principal.Membership.Ref.Version = "2"
	fresh.Principal.Membership.IssuedAt, fresh.Principal.Membership.ExpiresAt = now, now.Add(time.Hour)
	freshRef := fresh.Principal.Membership.Ref
	fresh.Context.MembershipSnapshotRef = &freshRef
	actor := *demand.Actor
	actor.Membership.Ref.Version = "2"
	actor.Membership.IssuedAt, actor.Membership.ExpiresAt = now, now.Add(time.Hour)
	fresh.Actor = &actor
	if admitted, err := recovered.Admit(ctx, fresh); err != nil || !admitted.Allowed {
		t.Fatalf("reconciled permit not released: %+v %v", admitted, err)
	}
	if err := recovered.Abort(ctx, "tenant", fresh.AttemptID); err != nil {
		t.Fatal(err)
	}
	wire.mu.Lock()
	transactions, reads := wire.next, wire.reads
	wire.mu.Unlock()
	if transactions < 15 || reads < 25 {
		t.Fatalf("engine bypassed SDK transactional state: transactions=%d reads=%d", transactions, reads)
	}
}

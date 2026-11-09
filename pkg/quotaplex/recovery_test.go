package quotaplex

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecoveryPoisonBackoffDoesNotStarveLaterTerminalEvidence(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	policy := engineFixturePolicy(now)
	policy.Meters["rows"] = Meter{ID: "rows", Unit: "rows", Billable: true}
	engine := engineFixture(t, policy, func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	poison := terminalEvidenceFixture(t, engine, now, "poison", "a-poison", 4)
	poison.Settlement.Usage["rows"] = 1 // Known raw work, unknown price: preserve facts.
	if _, err := engine.EnqueueTerminal(ctx, poison); err != nil {
		t.Fatal(err)
	}
	good := terminalEvidenceFixture(t, engine, now, "good", "b-good", 5)
	if _, err := engine.EnqueueTerminal(ctx, good); err != nil {
		t.Fatal(err)
	}
	worker, err := NewRecovery(engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := worker.RunOnce(ctx, RecoveryPage{TenantID: "tenant", Limit: 16})
	if err != nil || report.Scanned != 2 || report.Settled != 1 || report.Failed != 1 || report.Pending != 1 || !report.Done {
		t.Fatal(report, err)
	}
	ack, err := engine.GetUsageAcknowledgement(ctx, "tenant", "a-poison")
	if err != nil || ack.Status != UsagePending || ack.LastErrorCode != "price_unavailable" {
		t.Fatal(ack, err)
	}
	if report, err = worker.RunOnce(ctx, RecoveryPage{TenantID: "tenant", Limit: 16}); err != nil || report.Scanned != 1 || report.Deferred != 1 || report.Failed != 0 {
		t.Fatal(report, err)
	}
	info, err := engine.GetAttempt(ctx, "tenant", "poison")
	if err != nil || info.Decision.State != "dispatching" {
		t.Fatal(info, err)
	}
	balance, err := engine.BudgetBalance(ctx, "tenant", terminalTokenKey(t, info))
	if err != nil || balance.Held != 10 || balance.Consumed != 5 {
		t.Fatal("poison lost exposure or healthy event double charged", balance, err)
	}
}

func TestRecoveryOnlyAbortsExpiredUndispatchedAdmission(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	if decision := engineAdmit(t, engine, engineFixtureDemand(now, "never-dispatched", 10)); !decision.Allowed {
		t.Fatal(decision)
	}
	evidence := terminalEvidenceFixture(t, engine, now, "possibly-dispatched", "event", 4)
	if err := engine.MarkUnknown(ctx, evidence.Settlement.Claim, "lost upstream response"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	worker, err := NewRecovery(engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := worker.RecoverAttempts(ctx, RecoveryPage{TenantID: "tenant", Limit: 16})
	if err != nil || report.Aborted != 1 || report.Pending != 1 || report.Failed != 0 {
		t.Fatal(report, err)
	}
	absent, err := engine.GetAttempt(ctx, "tenant", "never-dispatched")
	if err != nil || absent.Decision.State != "aborted" {
		t.Fatal(absent, err)
	}
	uncertain, err := engine.GetAttempt(ctx, "tenant", "possibly-dispatched")
	if err != nil || uncertain.Decision.State != "unknown" {
		t.Fatal("expired membership forgave provider uncertainty", uncertain, err)
	}
	balance, err := engine.BudgetBalance(ctx, "tenant", terminalTokenKey(t, uncertain))
	if err != nil || balance.Held != 10 || balance.Consumed != 0 {
		t.Fatal(balance, err)
	}
}

func TestTrustedRecoveryResolverRetriesReadsAndPinsTerminalFacts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	evidence := terminalEvidenceFixture(t, engine, now, "job-attempt", "job-terminal", 7)
	if err := engine.MarkUnknown(ctx, evidence.Settlement.Claim, "waiting for trusted completion"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	ready := false
	worker, err := NewRecovery(engine, TrustedRecoveryResolverFunc(func(_ context.Context, info AttemptInfo) (*TerminalEvidence, error) {
		calls++
		if info.ProviderResource != "job/job-attempt" || info.Claim != evidence.Settlement.Claim {
			t.Fatal("resolver received another attempt", info)
		}
		info.Demand.Bounds["tokens"] = 999 // Returned read facts are isolated.
		if !ready {
			return nil, nil
		}
		return &evidence, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	page := RecoveryPage{TenantID: "tenant", Limit: 16}
	if report, err := worker.RecoverAttempts(ctx, page); err != nil || report.Pending != 1 || calls != 1 {
		t.Fatal(report, err, calls)
	}
	if report, err := worker.RecoverAttempts(ctx, page); err != nil || report.Deferred != 1 || calls != 1 {
		t.Fatal(report, err, calls)
	}
	now = now.Add(time.Second)
	ready = true
	if report, err := worker.RecoverAttempts(ctx, page); err != nil || report.Settled != 1 || calls != 2 {
		t.Fatal(report, err, calls)
	}
	info, err := engine.GetAttempt(ctx, "tenant", "job-attempt")
	if err != nil || info.Actual["tokens"] != 7 || info.Actual["usd"] != 11 || info.Demand.Bounds["tokens"] != 10 {
		t.Fatal(info, err)
	}
}

func TestRecoveryResolverCannotRetargetAttemptOrNativeResource(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	evidence := terminalEvidenceFixture(t, engine, now, "job-attempt", "job-terminal", 7)
	evidence.Settlement.ProviderResourceID = "another-job"
	worker, err := NewRecovery(engine, TrustedRecoveryResolverFunc(func(context.Context, AttemptInfo) (*TerminalEvidence, error) { return &evidence, nil }))
	if err != nil {
		t.Fatal(err)
	}
	report, err := worker.RecoverAttempts(ctx, RecoveryPage{TenantID: "tenant", Limit: 16})
	if err != nil || report.Failed != 1 || report.Settled != 0 {
		t.Fatal(report, err)
	}
	if _, err := engine.GetUsageAcknowledgement(ctx, "tenant", "job-terminal"); !errors.Is(err, ErrNotFound) {
		t.Fatal("retargeted evidence was acknowledged", err)
	}
	info, err := engine.GetAttempt(ctx, "tenant", "job-attempt")
	if err != nil || info.Decision.State != "dispatching" {
		t.Fatal(info, err)
	}
}

func TestRecoveryResolverLeaseAllowsOneConcurrentRead(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	evidence := terminalEvidenceFixture(t, engine, now, "job-attempt", "job-terminal", 7)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	worker, err := NewRecovery(engine, TrustedRecoveryResolverFunc(func(context.Context, AttemptInfo) (*TerminalEvidence, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return &evidence, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			_, err := worker.RecoverAttempts(ctx, RecoveryPage{TenantID: "tenant", Limit: 16})
			results <- err
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("resolver did not begin")
	}
	for i := 0; i < 15; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("other workers did not defer")
		}
	}
	close(release)
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("concurrent workers repeated status lookup", calls.Load())
	}
}

func TestRecoveryLoanMaintenancePreservesHeldCreditAndHandlesLateAbort(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engine := engineFixture(t, loanFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	loan := loanGrant(t, engine, "loan", 50, now.Add(time.Minute))
	demand := loanFixtureDemand(now, "held", "borrower", 60)
	if decision := engineAdmit(t, engine, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	worker, err := NewRecovery(engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	page := RecoveryPage{TenantID: "tenant", Limit: 16}
	if report, err := worker.ReconcileLoans(ctx, page); err != nil || report.Returned != 0 || report.Pending != 1 {
		t.Fatal(report, err)
	}
	if balance := loanBalance(t, engine, loan.Borrower.Key); balance.Held != 60 || balance.Credited != 50 {
		t.Fatal("worker recalled held credit", balance)
	}
	if err := engine.Abort(ctx, "tenant", "held"); err != nil {
		t.Fatal(err)
	}
	if report, err := worker.ReconcileLoans(ctx, page); err != nil || report.Returned != 1 || report.Pending != 0 {
		t.Fatal(report, err)
	}
	if balance := loanBalance(t, engine, loan.Borrower.Key); balance.Held != 0 || balance.Credited != 0 || balance.Available != 10 {
		t.Fatal(balance)
	}
}

func TestRecoveryPagesAdvanceAcrossPoisonAndEnforceTenant(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	for _, id := range []string{"a", "b"} {
		evidence := terminalEvidenceFixture(t, engine, now, id, id, 1)
		if _, err := engine.EnqueueTerminal(ctx, evidence); err != nil {
			t.Fatal(err)
		}
	}
	worker, err := NewRecovery(engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := worker.RunOnce(ctx, RecoveryPage{TenantID: "tenant", Limit: 1})
	if err != nil || first.Settled != 1 || first.NextAfterID != "a" || first.Done {
		t.Fatal(first, err)
	}
	second, err := worker.RunOnce(ctx, RecoveryPage{TenantID: "tenant", AfterID: first.NextAfterID, Limit: 1})
	if err != nil || second.Settled != 1 || second.NextAfterID != "b" {
		t.Fatal(second, err)
	}
	last, err := worker.RunOnce(ctx, RecoveryPage{TenantID: "tenant", AfterID: second.NextAfterID, Limit: 1})
	if err != nil || !last.Done || last.Scanned != 0 {
		t.Fatal(last, err)
	}
	for _, fn := range []func(context.Context, RecoveryPage) (RecoveryReport, error){worker.RunOnce, worker.RecoverAttempts, worker.ReconcileLoans} {
		if _, err := fn(ctx, RecoveryPage{TenantID: "other", Limit: 16}); err == nil {
			t.Fatal("cross-tenant recovery accepted")
		}
		if _, err := fn(ctx, RecoveryPage{TenantID: "tenant", Limit: MaxTransactionRecords + 1}); err == nil {
			t.Fatal("unbounded page accepted")
		}
	}
}

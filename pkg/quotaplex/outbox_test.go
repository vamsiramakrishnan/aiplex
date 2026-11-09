package quotaplex

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type terminalFailStore struct {
	Store
	calls  atomic.Int64
	failAt atomic.Int64
	after  bool
}

func (s *terminalFailStore) Update(ctx context.Context, tenant string, fn func(Tx) error) error {
	call := s.calls.Add(1)
	if s.failAt.Load() == call && !s.after {
		return errors.New("test storage unavailable")
	}
	err := s.Store.Update(ctx, tenant, fn)
	if err == nil && s.failAt.Load() == call && s.after {
		return errors.New("test commit reply lost")
	}
	return err
}

func terminalEvidenceFixture(t *testing.T, engine *Engine, now time.Time, attempt, event string, tokens int64) TerminalEvidence {
	t.Helper()
	demand := engineFixtureDemand(now, attempt, 10)
	if decision := engineAdmit(t, engine, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	claim, err := engine.ClaimDispatch(context.Background(), "tenant", attempt)
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.RecordProviderResource(context.Background(), claim, "job/"+attempt); err != nil {
		t.Fatal(err)
	}
	return TerminalEvidence{Version: Version, ProducerID: "host/native-v1", ConfigurationDigest: demand.Target.ConfigurationDigest, Settlement: Settlement{TenantID: "tenant", AttemptID: attempt, EventID: event, Claim: claim, Usage: Vector{"requests": 1, "tokens": tokens}, ProviderResourceID: "job/" + attempt}}
}

func terminalTokenKey(t *testing.T, info AttemptInfo) string {
	t.Helper()
	for _, limit := range info.Plan.Budgets {
		if limit.MeterID == "tokens" {
			return limit.Key
		}
	}
	t.Fatal("missing token budget")
	return ""
}

func TestTerminalEvidenceAcknowledgementIsDurableBeforeSettlement(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	store := &terminalFailStore{Store: NewMemoryStore()}
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	evidence := terminalEvidenceFixture(t, engine, now, "attempt", "event", 7)
	ack, err := engine.EnqueueTerminal(ctx, evidence)
	if err != nil || ack.Status != UsagePending || ack.AcceptedAt != now {
		t.Fatal(ack, err)
	}
	// Caller mutation cannot change the acknowledged immutable facts.
	evidence.Settlement.Usage["tokens"] = 999
	store.failAt.Store(store.calls.Load() + 2) // lease succeeds, settlement fails
	ack, err = engine.ProcessTerminal(ctx, "tenant", "event")
	if err == nil || ack.Status != UsagePending || ack.LastErrorCode != "settlement_unavailable" || ack.Attempts != 1 {
		t.Fatal(ack, err)
	}
	info, err := engine.GetAttempt(ctx, "tenant", "attempt")
	if err != nil || info.Decision.State != "dispatching" {
		t.Fatal(info, err)
	}
	balance, err := engine.BudgetBalance(ctx, "tenant", terminalTokenKey(t, info))
	if err != nil || balance.Held != 10 || balance.Consumed != 0 {
		t.Fatal(balance, err)
	}
	// A new process can resume after backoff using the same durable store.
	now = now.Add(time.Second)
	restarted := engineFixture(t, engine.policy.Policy(), func() time.Time { return now }, store)
	worker, err := NewRecovery(restarted, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := worker.RunOnce(ctx, RecoveryPage{TenantID: "tenant", Limit: 16})
	if err != nil || report.Settled != 1 || report.Failed != 0 {
		t.Fatal(report, err)
	}
	ack, err = restarted.GetUsageAcknowledgement(ctx, "tenant", "event")
	if err != nil || ack.Status != UsageSettled || ack.Decision == nil {
		t.Fatal(ack, err)
	}
	info, err = restarted.GetAttempt(ctx, "tenant", "attempt")
	if err != nil || info.Actual["tokens"] != 7 || info.Actual["usd"] != 11 {
		t.Fatal(info, err)
	}
	if report, err = worker.RunOnce(ctx, RecoveryPage{TenantID: "tenant", Limit: 16}); err != nil || report.Scanned != 0 {
		t.Fatal("settled history stayed on active queue", report, err)
	}
}

func TestTerminalEvidenceRejectsChangedProvenanceAndCanonicalEvent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	evidence := terminalEvidenceFixture(t, engine, now, "attempt", "event", 4)
	first, err := engine.EnqueueTerminal(ctx, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := engine.EnqueueTerminal(ctx, evidence); err != nil || replay.Status != first.Status || replay.EvidenceDigest != first.EvidenceDigest {
		t.Fatal(replay, err)
	}
	for _, mutate := range []func(*TerminalEvidence){
		func(e *TerminalEvidence) { e.ProducerID = "another-producer" },
		func(e *TerminalEvidence) { e.ConfigurationDigest = "different-configuration" },
		func(e *TerminalEvidence) { e.Settlement.EventID = "different-event" },
		func(e *TerminalEvidence) { e.Settlement.Claim.Fence++ },
		func(e *TerminalEvidence) { e.Settlement.ProviderResourceID = "different-resource" },
		func(e *TerminalEvidence) { e.Settlement.Usage = Vector{"requests": 1, "tokens": 5} },
	} {
		changed := evidence
		mutate(&changed)
		if _, err := engine.EnqueueTerminal(ctx, changed); !errors.Is(err, ErrConflict) {
			t.Fatalf("changed terminal evidence accepted: %+v %v", changed, err)
		}
	}
	wrongTenant := evidence
	wrongTenant.Settlement.TenantID = "other"
	if _, err := engine.EnqueueTerminal(ctx, wrongTenant); err == nil {
		t.Fatal("cross-tenant evidence accepted")
	}
	incomplete := evidence
	incomplete.Settlement.EventID = "incomplete"
	incomplete.Settlement.Usage = Vector{"requests": 1}
	if _, err := engine.EnqueueTerminal(ctx, incomplete); err == nil {
		t.Fatal("missing usage became known zero")
	}
	if _, err := engine.ProcessTerminal(ctx, "tenant", "event"); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalEvidenceCommitAmbiguityAndCancelledClient(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	store := &terminalFailStore{Store: NewMemoryStore(), after: true}
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	evidence := terminalEvidenceFixture(t, engine, now, "attempt", "event", 4)
	store.failAt.Store(store.calls.Load() + 1)
	if ack, err := engine.EnqueueTerminal(ctx, evidence); err == nil || ack.Status != "" {
		t.Fatal("ack escaped uncertain commit", ack, err)
	}
	ack, err := engine.EnqueueTerminal(ctx, evidence)
	if err != nil || ack.Status != UsagePending {
		t.Fatal("same event could not recover committed acceptance", ack, err)
	}
	client, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = engine.EnqueueTerminal(client, evidence); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	durable, stop := context.WithTimeout(context.WithoutCancel(client), time.Second)
	defer stop()
	if _, err = engine.ProcessTerminal(durable, "tenant", "event"); err != nil {
		t.Fatal(err)
	}
	info, err := engine.GetAttempt(ctx, "tenant", "attempt")
	if err != nil || info.Actual["tokens"] != 4 {
		t.Fatal(info, err)
	}
}

func TestTerminalSettlementAfterWorkerAckFailureIsNotChargedTwice(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	store := &terminalFailStore{Store: NewMemoryStore()}
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	evidence := terminalEvidenceFixture(t, engine, now, "attempt", "event", 4)
	if _, err := engine.EnqueueTerminal(ctx, evidence); err != nil {
		t.Fatal(err)
	}
	store.failAt.Store(store.calls.Load() + 3) // acquire, settle, then lost worker ack
	if _, err := engine.ProcessTerminal(ctx, "tenant", "event"); err == nil {
		t.Fatal("expected worker acknowledgement failure")
	}
	info, err := engine.GetAttempt(ctx, "tenant", "attempt")
	if err != nil || info.Decision.State != "settled" {
		t.Fatal(info, err)
	}
	if _, err = engine.ProcessTerminal(ctx, "tenant", "event"); !errors.Is(err, ErrRecoveryDeferred) {
		t.Fatal("live worker lease ignored", err)
	}
	now = now.Add(terminalLease)
	if ack, err := engine.ProcessTerminal(ctx, "tenant", "event"); err != nil || ack.Status != UsageSettled {
		t.Fatal(ack, err)
	}
	info, err = engine.GetAttempt(ctx, "tenant", "attempt")
	if err != nil || info.Actual["tokens"] != 4 {
		t.Fatal(info, err)
	}
	var consumes int
	if err = store.View(ctx, "tenant", func(tx Tx) error {
		entries, err := tx.List(ledgerKind)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			var ledger ledgerEntry
			if err := DecodeStoreValue(entry.Value, &ledger); err != nil {
				return err
			}
			if ledger.Action == "consume" {
				consumes++
			}
		}
		return nil
	}); err != nil || consumes != 1 {
		t.Fatal(consumes, err)
	}
}

func TestConcurrentTerminalWorkersConsumeOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	evidence := terminalEvidenceFixture(t, engine, now, "attempt", "event", 6)
	if _, err := engine.EnqueueTerminal(ctx, evidence); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := engine.ProcessTerminal(ctx, "tenant", "event")
			if err != nil && !errors.Is(err, ErrRecoveryDeferred) {
				t.Errorf("worker: %v", err)
			}
		}()
	}
	wg.Wait()
	info, err := engine.GetAttempt(ctx, "tenant", "attempt")
	if err != nil || info.Actual["tokens"] != 6 {
		t.Fatal(info, err)
	}
	balance, err := engine.BudgetBalance(ctx, "tenant", terminalTokenKey(t, info))
	if err != nil || balance.Consumed != 6 || balance.Held != 0 {
		t.Fatal(balance, err)
	}
}

func TestTerminalObservedModelMismatchSurvivesRecoveryWithoutWrongSettlement(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	policy := engineFixturePolicy(now)
	engine := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	evidence := terminalEvidenceFixture(t, engine, now, "mismatch", "mismatch-event", 7)
	evidence.ObservedModelVersion = "resolved-v2"
	ack, err := engine.EnqueueTerminal(ctx, evidence)
	if err != nil || ack.Status != UsagePending {
		t.Fatal("trusted facts were discarded", ack, err)
	}
	ack, err = engine.ProcessTerminal(ctx, "tenant", "mismatch-event")
	if !errors.Is(err, ErrModelVersionMismatch) || ack.Status != UsagePending || ack.LastErrorCode != "model_version_mismatch" {
		t.Fatal(ack, err)
	}
	now = now.Add(time.Second)
	restarted := engineFixture(t, policy, func() time.Time { return now }, store)
	worker, err := NewRecovery(restarted, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := worker.RunOnce(ctx, RecoveryPage{TenantID: "tenant", Limit: 16})
	if err != nil || report.Failed != 1 || report.Pending != 1 || report.Settled != 0 {
		t.Fatal(report, err)
	}
	info, err := restarted.GetAttempt(ctx, "tenant", "mismatch")
	if err != nil || info.Decision.State != "dispatching" || len(info.Actual) != 0 {
		t.Fatal("wrong-version usage changed the ledger", info, err)
	}
	balance, err := restarted.BudgetBalance(ctx, "tenant", terminalTokenKey(t, info))
	if err != nil || balance.Held != 10 || balance.Consumed != 0 {
		t.Fatal("wrong-version exposure was released", balance, err)
	}
	if err = store.View(ctx, "tenant", func(tx Tx) error { var usage usageRecord; return tx.Get(usageKind, "mismatch-event", &usage) }); !errors.Is(err, ErrNotFound) {
		t.Fatal("mismatched usage was priced", err)
	}
	changed := evidence
	changed.ObservedModelVersion = "resolved-v1"
	if _, err = restarted.EnqueueTerminal(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("observed version rewritten on replay", err)
	}
	matching := terminalEvidenceFixture(t, restarted, now, "matching", "matching-event", 4)
	matching.ObservedModelVersion = "resolved-v1"
	if _, err = restarted.EnqueueTerminal(ctx, matching); err != nil {
		t.Fatal(err)
	}
	if ack, err = restarted.ProcessTerminal(ctx, "tenant", "matching-event"); err != nil || ack.Status != UsageSettled {
		t.Fatal("matching version failed normal settlement", ack, err)
	}
}

func TestTerminalRequestCountCanRecordUnpinnedObservedModel(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	policy := engineFixturePolicy(now)
	policy.Meters = map[string]Meter{"requests": {ID: "requests", Unit: "requests"}}
	policy.PriceBooks = nil
	policy.Budgets = []BudgetRule{{ID: "requests", Scope: Scope{Kind: "tenant"}, MeterID: "requests", Limit: 100, Period: Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}}}
	engine := engineFixture(t, policy, func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "request-only", 0)
	demand.Target.ModelVersion = ""
	demand.Bounds = Vector{"requests": 1}
	if decision := engineAdmit(t, engine, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	claim, err := engine.ClaimDispatch(ctx, "tenant", demand.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	evidence := TerminalEvidence{Version: Version, ProducerID: "host/request-count", ConfigurationDigest: demand.Target.ConfigurationDigest, ObservedModelVersion: "native-version-metadata", Settlement: Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "request-terminal", Claim: claim, Usage: Vector{"requests": 1}}}
	strict := evidence
	strict.RequireModelVersion = true
	if _, err = engine.EnqueueTerminal(ctx, strict); !errors.Is(err, ErrConflict) {
		t.Fatal("strict evidence without pinned model accepted", err)
	}
	if _, err = engine.EnqueueTerminal(ctx, evidence); err != nil {
		t.Fatal(err)
	}
	if ack, err := engine.ProcessTerminal(ctx, "tenant", evidence.Settlement.EventID); err != nil || ack.Status != UsageSettled {
		t.Fatal("request-count metadata unnecessarily blocked settlement", ack, err)
	}
}

func TestTerminalModelRequirementIsPinnedByAdmission(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	policy := engineFixturePolicy(now)
	store := NewMemoryStore()
	engine := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "strict-model", 10)
	demand.RequireModelVersion = true
	if decision := engineAdmit(t, engine, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	claim, err := engine.ClaimDispatch(ctx, "tenant", demand.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	evidence := TerminalEvidence{Version: Version, ProducerID: "host/strict-tokens", ConfigurationDigest: demand.Target.ConfigurationDigest, Settlement: Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "strict-terminal", Claim: claim, Usage: Vector{"requests": 1, "tokens": 4}}}
	// Producer omission cannot downgrade a model-proof requirement made at admission.
	ack, err := engine.EnqueueTerminal(ctx, evidence)
	if err != nil || ack.Status != UsagePending {
		t.Fatal("missing observed version facts were discarded", ack, err)
	}
	explicit := evidence
	explicit.RequireModelVersion = true
	if replay, err := engine.EnqueueTerminal(ctx, explicit); err != nil || replay.EvidenceDigest != ack.EvidenceDigest {
		t.Fatal("requirement normalization changed replay identity", replay, err)
	}
	ack, err = engine.ProcessTerminal(ctx, "tenant", evidence.Settlement.EventID)
	if !errors.Is(err, ErrModelVersionMissing) || ack.Status != UsagePending || ack.LastErrorCode != "model_version_missing" {
		t.Fatal(ack, err)
	}
	now = now.Add(time.Second)
	restarted := engineFixture(t, policy, func() time.Time { return now }, store)
	worker, err := NewRecovery(restarted, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report, err := worker.RunOnce(ctx, RecoveryPage{TenantID: "tenant", Limit: 16}); err != nil || report.Pending != 1 || report.Failed != 1 || report.Settled != 0 {
		t.Fatal(report, err)
	}
	info, err := restarted.GetAttempt(ctx, "tenant", demand.AttemptID)
	if err != nil || info.Decision.State != "dispatching" || len(info.Actual) != 0 {
		t.Fatal(info, err)
	}
	balance, err := restarted.BudgetBalance(ctx, "tenant", terminalTokenKey(t, info))
	if err != nil || balance.Held != 10 || balance.Consumed != 0 {
		t.Fatal("missing-version evidence released exposure", balance, err)
	}
	matching := engineFixtureDemand(now, "strict-match", 10)
	matching.RequireModelVersion = true
	if decision := engineAdmit(t, restarted, matching); !decision.Allowed {
		t.Fatal(decision)
	}
	matchingClaim, err := restarted.ClaimDispatch(ctx, "tenant", matching.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	good := TerminalEvidence{Version: Version, ProducerID: evidence.ProducerID, ConfigurationDigest: matching.Target.ConfigurationDigest, ObservedModelVersion: matching.Target.ModelVersion, Settlement: Settlement{TenantID: "tenant", AttemptID: matching.AttemptID, EventID: "strict-match-terminal", Claim: matchingClaim, Usage: Vector{"requests": 1, "tokens": 4}}}
	if _, err = restarted.EnqueueTerminal(ctx, good); err != nil {
		t.Fatal(err)
	}
	if ack, err = restarted.ProcessTerminal(ctx, "tenant", good.Settlement.EventID); err != nil || ack.Status != UsageSettled {
		t.Fatal("matching required version did not settle", ack, err)
	}
}

func TestTerminalModelProofNormalizationAndDirectSettlementReplay(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	evidence := terminalEvidenceFixture(t, engine, now, "normalized", "normalized-event", 4)
	evidence.Settlement.ModelVersion = "resolved-v1"
	first, err := engine.EnqueueTerminal(ctx, evidence)
	if err != nil {
		t.Fatal(err)
	}
	observedOnly := evidence
	observedOnly.Settlement.ModelVersion = ""
	observedOnly.ObservedModelVersion = "resolved-v1"
	if replay, err := engine.EnqueueTerminal(ctx, observedOnly); err != nil || replay.EvidenceDigest != first.EvidenceDigest {
		t.Fatal("equivalent typed model proof changed evidence identity", replay, err)
	}
	conflict := evidence
	conflict.ObservedModelVersion = "resolved-v2"
	if _, err := engine.EnqueueTerminal(ctx, conflict); !errors.Is(err, ErrConflict) {
		t.Fatal("internally conflicting model proofs accepted", err)
	}
	if ack, err := engine.ProcessTerminal(ctx, "tenant", evidence.Settlement.EventID); err != nil || ack.Status != UsageSettled {
		t.Fatal(ack, err)
	}
	direct := terminalEvidenceFixture(t, engine, now, "direct", "direct-event", 3)
	direct.Settlement.ModelVersion = "resolved-v1"
	if _, err := engine.Settle(ctx, direct.Settlement); err != nil {
		t.Fatal(err)
	}
	if ack, err := engine.EnqueueTerminal(ctx, direct); err != nil || ack.Status != UsageSettled {
		t.Fatal("typed direct-settlement replay used the legacy digest", ack, err)
	}
	worker, err := NewRecovery(engine, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report, err := worker.RunOnce(ctx, RecoveryPage{TenantID: "tenant", Limit: 16}); err != nil || report.Scanned != 0 {
		t.Fatal("already settled proof created pending work", report, err)
	}
}

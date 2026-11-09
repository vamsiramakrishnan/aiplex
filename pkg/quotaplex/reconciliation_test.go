package quotaplex

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func reconciliationFixture(t *testing.T, store Store) (*Engine, time.Time, TerminalEvidence) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	e := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, store)
	engineActivate(t, e, "")
	d := engineFixtureDemand(now, "attempt", 10)
	d.RequireModelVersion = true
	if decision := engineAdmit(t, e, d); !decision.Allowed {
		t.Fatal(decision)
	}
	claim, err := e.ClaimDispatch(ctx, "tenant", d.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.RecordProviderResource(ctx, claim, "provider/job"); err != nil {
		t.Fatal(err)
	}
	facts := TerminalEvidence{Version: Version, ProducerID: "trusted-native", ConfigurationDigest: d.Target.ConfigurationDigest, ObservedModelVersion: "unexpected-version", RequireModelVersion: true, Settlement: Settlement{TenantID: "tenant", AttemptID: d.AttemptID, EventID: "original-facts", Claim: claim, Usage: Vector{"requests": 1, "tokens": 6}, ProviderResourceID: "provider/job"}}
	if _, err = e.EnqueueTerminal(ctx, facts); err != nil {
		t.Fatal(err)
	}
	if err = e.MarkUnknown(ctx, claim, "version proof requires investigation"); err != nil {
		t.Fatal(err)
	}
	return e, now, facts
}
func reconciliationProposal(now time.Time) ReconciliationRequest {
	return ReconciliationRequest{TenantID: "tenant", ID: "correction", AttemptID: "attempt", Reason: "Provider correction confirms final usage and model identity", EvidenceRef: "case/evidence-verified-by-reviewer", Usage: Vector{"requests": 1, "tokens": 7}, ModelVersion: "resolved-v1", ProviderResourceID: "provider/job", ExpiresAt: now.Add(30 * time.Minute)}
}
func TestReconciliationPreservesOriginalFactsAndSettlesAtPinnedPrices(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	e, now, facts := reconciliationFixture(t, store)
	author := Administrator{PrincipalID: "investigator", RequestID: "plan"}
	approver := Administrator{PrincipalID: "reviewer", RequestID: "approve"}
	original, _, err := e.GetTerminalEvidence(ctx, "tenant", facts.Settlement.EventID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.PrepareReconciliation(ctx, reconciliationProposal(now), author, false)
	if err != nil || !p.IndependentApproval || p.OriginalEvidenceDigest == "" {
		t.Fatal(p, err)
	}
	if _, err = e.ApplyReconciliation(ctx, "tenant", p.ID, p.Digest, author); !errors.Is(err, ErrDenied) {
		t.Fatal("self approval accepted", err)
	}
	if _, err = e.ApplyReconciliation(ctx, "tenant", p.ID, "forged", approver); !errors.Is(err, ErrConflict) {
		t.Fatal("unreviewed digest accepted", err)
	}
	receipt, err := e.ApplyReconciliation(ctx, "tenant", p.ID, p.Digest, approver)
	if err != nil || receipt.Decision.State != "settled" {
		t.Fatal(receipt, err)
	}
	info, err := e.GetAttempt(ctx, "tenant", "attempt")
	if err != nil || info.Actual["tokens"] != 7 || info.Actual["usd"] != 11 {
		t.Fatal("correction did not use original rational prices", info, err)
	}
	balance, err := e.BudgetBalance(ctx, "tenant", terminalTokenKey(t, info))
	if err != nil || balance.Held != 0 || balance.Consumed != 7 {
		t.Fatal(balance, err)
	}
	after, ack, err := e.GetTerminalEvidence(ctx, "tenant", facts.Settlement.EventID)
	if err != nil || !reflect.DeepEqual(after, original) || ack.ResolutionID != p.ID || ack.Status != UsageSettled {
		t.Fatal("original facts mutated or resolution missing", after, ack, err)
	}
	if ack, err = e.ProcessTerminal(ctx, "tenant", facts.Settlement.EventID); err != nil || ack.ResolutionID != p.ID {
		t.Fatal("old worker retried superseded facts", ack, err)
	}
	replay, err := e.ApplyReconciliation(ctx, "tenant", p.ID, p.Digest, approver)
	if err != nil || replay != receipt {
		t.Fatal("replayed correction changed receipt", replay, err)
	}
	if _, err = e.ApplyReconciliation(ctx, "other", p.ID, p.Digest, approver); err == nil {
		t.Fatal("cross tenant apply")
	}
	if _, err = e.PrepareReconciliation(ctx, ReconciliationRequest{TenantID: "tenant", ID: "another", AttemptID: "attempt", Reason: "no", EvidenceRef: "case/x", Usage: Vector{"requests": 1, "tokens": 0}, ModelVersion: "resolved-v1", ProviderResourceID: "provider/job", ExpiresAt: now.Add(time.Minute)}, author, false); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("settled attempt repricing accepted", err)
	}
}
func TestReconciliationRejectsChangedFactsExpiredPlansAndMissingRawMeters(t *testing.T) {
	ctx := context.Background()
	author := Administrator{PrincipalID: "planner", RequestID: "plan"}
	approver := Administrator{PrincipalID: "reviewer", RequestID: "approve"}
	for _, test := range []struct {
		name  string
		alter func(*ReconciliationRequest)
	}{
		{"missing raw", func(r *ReconciliationRequest) { delete(r.Usage, "requests") }},
		{"computed money", func(r *ReconciliationRequest) { r.Usage["usd"] = 1 }},
		{"wrong model", func(r *ReconciliationRequest) { r.ModelVersion = "different-model" }},
		{"missing model", func(r *ReconciliationRequest) { r.ModelVersion = "" }},
		{"wrong resource", func(r *ReconciliationRequest) { r.ProviderResourceID = "other/job" }},
		{"expired", func(r *ReconciliationRequest) { r.ExpiresAt = r.ExpiresAt.Add(-time.Hour) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			e, now, _ := reconciliationFixture(t, NewMemoryStore())
			r := reconciliationProposal(now)
			test.alter(&r)
			if _, err := e.PrepareReconciliation(ctx, r, author, false); err == nil {
				t.Fatal("untrusted correction accepted")
			}
			info, _ := e.GetAttempt(ctx, "tenant", "attempt")
			if info.Decision.State != "unknown" || len(info.Actual) != 0 {
				t.Fatal("invalid plan changed accounting", info)
			}
		})
	}
	e, now, _ := reconciliationFixture(t, NewMemoryStore())
	p, err := e.PrepareReconciliation(ctx, reconciliationProposal(now), author, false)
	if err != nil {
		t.Fatal(err)
	}
	// A newly received native event or native job attachment must invalidate an old review.
	if err = e.store.Update(ctx, "tenant", func(tx Tx) error {
		var record attemptRecord
		if err := tx.Get(attemptKind, "attempt", &record); err != nil {
			return err
		}
		record.UnknownReason = "new provider evidence arrived"
		return tx.Put(attemptKind, "attempt", record)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ApplyReconciliation(ctx, "tenant", p.ID, p.Digest, approver); !errors.Is(err, ErrConflict) {
		t.Fatal("stale reviewed state accepted", err)
	}
	e, now, _ = reconciliationFixture(t, NewMemoryStore())
	p, err = e.PrepareReconciliation(ctx, reconciliationProposal(now), author, false)
	if err != nil {
		t.Fatal(err)
	}
	e.clock = func() time.Time { return p.ExpiresAt }
	if _, err = e.ApplyReconciliation(ctx, "tenant", p.ID, p.Digest, approver); !errors.Is(err, ErrDenied) {
		t.Fatal("expired approval accepted", err)
	}
}
func TestReconciliationCommitLossReplaysWithoutDoubleConsumption(t *testing.T) {
	ctx := context.Background()
	store := &terminalFailStore{Store: NewMemoryStore(), after: true}
	e, now, _ := reconciliationFixture(t, store)
	author := Administrator{PrincipalID: "planner", RequestID: "plan"}
	approver := Administrator{PrincipalID: "reviewer", RequestID: "approve"}
	p, err := e.PrepareReconciliation(ctx, reconciliationProposal(now), author, false)
	if err != nil {
		t.Fatal(err)
	}
	store.failAt.Store(store.calls.Load() + 1)
	if _, err = e.ApplyReconciliation(ctx, "tenant", p.ID, p.Digest, approver); err == nil {
		t.Fatal("lost commit reply not reported")
	}
	receipt, err := e.ApplyReconciliation(ctx, "tenant", p.ID, p.Digest, approver)
	if err != nil || receipt.Decision.State != "settled" {
		t.Fatal(receipt, err)
	}
	info, _ := e.GetAttempt(ctx, "tenant", "attempt")
	balance, err := e.BudgetBalance(ctx, "tenant", terminalTokenKey(t, info))
	if err != nil || balance.Consumed != 7 || balance.Held != 0 {
		t.Fatal(balance, err)
	}
}
func TestReconciliationConcurrentReviewPlansSettleOnlyOne(t *testing.T) {
	ctx := context.Background()
	e, now, _ := reconciliationFixture(t, NewMemoryStore())
	var plans []ReconciliationPlan
	for _, id := range []string{"a", "b"} {
		r := reconciliationProposal(now)
		r.ID = id
		p, err := e.PrepareReconciliation(ctx, r, Administrator{PrincipalID: "planner", RequestID: id}, false)
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, p)
	}
	var group sync.WaitGroup
	var successes atomic.Int32
	for _, p := range plans {
		group.Add(1)
		go func(p ReconciliationPlan) {
			defer group.Done()
			if _, err := e.ApplyReconciliation(ctx, "tenant", p.ID, p.Digest, Administrator{PrincipalID: "reviewer", RequestID: p.ID}); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		}(p)
	}
	group.Wait()
	if successes.Load() != 1 {
		t.Fatal("concurrent reviewed corrections both settled", successes.Load())
	}
}

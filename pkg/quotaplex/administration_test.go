package quotaplex

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestAdministrationPreviewUsesAdmissionWithoutAcquiringCapacity(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	policy := engineFixturePolicy(now)
	policy.Concurrency[0].Limit = 1
	engine := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	d := engineFixtureDemand(now, "preview", 10)
	before, err := engine.Inventory(ctx, "tenant", "attempts", "", 20)
	if err != nil || len(before.Records) != 0 {
		t.Fatal(before, err)
	}
	p, err := engine.Preview(ctx, d)
	if err != nil || !p.Decision.Allowed || p.Mutates || len(p.Plan.Budgets) != 2 {
		t.Fatal(p, err)
	}
	if _, err = engine.GetAttempt(ctx, "tenant", d.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatal("preview persisted an attempt", err)
	}
	if _, err = engine.BudgetBalance(ctx, "tenant", p.Plan.Budgets[0].Key); !errors.Is(err, ErrNotFound) {
		t.Fatal("preview persisted a balance", err)
	}
	if d := engineAdmit(t, engine, d); !d.Allowed {
		t.Fatal("preview consumed rate or concurrency", d)
	}
	denied, err := engine.Preview(ctx, engineFixtureDemand(now, "second", 10))
	if err != nil || denied.Decision.Allowed || denied.Decision.Reason != "concurrency_exhausted" {
		t.Fatal(denied, err)
	}
	after, err := engine.Inventory(ctx, "tenant", "attempts", "", 20)
	if err != nil || len(after.Records) != 1 {
		t.Fatal("denied preview wrote replay state", after, err)
	}
}
func TestAdministrationPolicyValidationExportsWithoutActivating(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	policy := engineFixturePolicy(now)
	engine := engineFixture(t, policy, func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	policy.Revision = 2
	policy.Budgets[0].Limit = 200
	artifact, err := engine.ValidatePolicy(ctx, policy, engine.policy.ref.Digest)
	if err != nil || artifact.State != "validated-export" || !artifact.DeploymentRequired || artifact.Reference.Revision != 2 {
		t.Fatal(artifact, err)
	}
	state, err := engine.InspectPolicy(ctx, "tenant")
	if err != nil || state.Active.Revision != 1 || state.Reference != state.Active {
		t.Fatal("validation published live policy", state, err)
	}
	if _, err = engine.ValidatePolicy(ctx, policy, "stale-digest"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale policy base accepted", err)
	}
	policy.Budgets[0].Period.End = policy.Budgets[0].Period.End.Add(time.Hour)
	if _, err = engine.ValidatePolicy(ctx, policy, engine.policy.ref.Digest); !errors.Is(err, ErrConflict) {
		t.Fatal("unsupported structural migration validated", err)
	}
	if err = engine.Ready(ctx); err != nil {
		t.Fatal("failed validation modified active head", err)
	}
}
func TestAdministrationLoansAuditAndReplayAtomically(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engine := engineFixture(t, loanFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	admin := Administrator{PrincipalID: "allocator", RequestID: "grant-1"}
	request := LoanRequest{TenantID: "tenant", ID: "loan", RuleID: "lend", Amount: 80, ExpiresAt: now.Add(time.Hour)}
	original, err := engine.GrantLoanAdmin(ctx, request, admin)
	if err != nil || original.Outstanding != 80 {
		t.Fatal(original, err)
	}
	if d := engineAdmit(t, engine, loanFixtureDemand(now, "work", "borrower", 80)); !d.Allowed {
		t.Fatal(d)
	}
	admin.RequestID = "return-1"
	partial, err := engine.ReturnUnusedLoanAdmin(ctx, "tenant", "loan", admin)
	if err != nil || partial.Returned != 10 {
		t.Fatal(partial, err)
	}
	if err = engine.Abort(ctx, "tenant", "work"); err != nil {
		t.Fatal(err)
	}
	replay, err := engine.ReturnUnusedLoanAdmin(ctx, "tenant", "loan", admin)
	if err != nil || !reflect.DeepEqual(replay, partial) {
		t.Fatal("same operation returned newly freed capacity", replay, partial, err)
	}
	admin.RequestID = "return-2"
	final, err := engine.ReturnUnusedLoanAdmin(ctx, "tenant", "loan", admin)
	if err != nil || final.Outstanding != 0 {
		t.Fatal(final, err)
	}
	audit, err := engine.Inventory(ctx, "tenant", "audit", "", 20)
	if err != nil || len(audit.Records) != 3 {
		t.Fatal("audit missing or duplicated", audit, err)
	}
	admin.RequestID = "grant-1"
	request.Amount = 1
	if _, err = engine.GrantLoanAdmin(ctx, request, admin); !errors.Is(err, ErrConflict) {
		t.Fatal("changed administration replay accepted", err)
	}
	if _, err = engine.Inventory(ctx, "other", "audit", "", 20); err == nil {
		t.Fatal("cross tenant inventory")
	}
	if _, err = engine.Inventory(ctx, "tenant", "policy-head", "", 20); err == nil {
		t.Fatal("unlisted internal inventory")
	}
}

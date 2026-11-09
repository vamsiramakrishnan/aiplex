package quotaplex

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/enforcement"
)

func TestRequiredModelVersionAdmissionMustPinTarget(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "required-version", 10)
	demand.RequireModelVersion = true
	demand.Target.ModelVersion = ""
	if _, err := engine.Admit(ctx, demand); err == nil {
		t.Fatal("strict admission accepted an unpinned model version")
	}
	if _, err := engine.GetAttempt(ctx, "tenant", demand.AttemptID); !errors.Is(err, ErrNotFound) {
		t.Fatal("invalid model-proof contract created accounting state", err)
	}
	demand.Target.ModelVersion = "resolved-v1"
	if decision := engineAdmit(t, engine, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	downgrade := demand
	downgrade.RequireModelVersion = false
	if _, err := engine.Admit(ctx, downgrade); !errors.Is(err, ErrConflict) {
		t.Fatal("replay downgraded the admission's proof requirement", err)
	}
}

func TestRequiredModelVersionDirectSettlementRetainsExposureUntilProof(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	policy := engineFixturePolicy(now)
	policy.Concurrency[0].Limit = 1
	engine := engineFixture(t, policy, func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "strict-direct", 10)
	demand.RequireModelVersion = true
	if decision := engineAdmit(t, engine, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	claim, err := engine.ClaimDispatch(ctx, "tenant", demand.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.RecordProviderResource(ctx, claim, "native-job"); err != nil {
		t.Fatal(err)
	}
	before, err := engine.GetAttempt(ctx, "tenant", demand.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	balances := map[string]BudgetBalance{}
	for _, limit := range before.Plan.Budgets {
		balance, err := engine.BudgetBalance(ctx, "tenant", limit.Key)
		if err != nil {
			t.Fatal(err)
		}
		balances[limit.Key] = balance
	}
	settlement := Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "strict-terminal", Claim: claim, Usage: Vector{"requests": 1, "tokens": 4}, ProviderResourceID: "native-job"}
	for _, version := range []string{"", "resolved-v2"} {
		invalid := settlement
		invalid.ModelVersion = version
		if _, err = engine.Settle(ctx, invalid); !errors.Is(err, ErrModelVersionProof) {
			t.Fatal("direct settlement bypassed model proof", version, err)
		}
		info, err := engine.GetAttempt(ctx, "tenant", demand.AttemptID)
		if err != nil || info.Decision.State != "dispatching" || len(info.Actual) != 0 {
			t.Fatal("invalid proof changed attempt consumption", info, err)
		}
		for key, expected := range balances {
			if got, err := engine.BudgetBalance(ctx, "tenant", key); err != nil || got != expected {
				t.Fatal("invalid proof released or consumed a hold", expected, got, err)
			}
		}
		blocked := engineFixtureDemand(now, "blocked-"+version, 1)
		if decision := engineAdmit(t, engine, blocked); decision.Allowed || decision.Reason != "concurrency_exhausted" {
			t.Fatal("invalid proof released active execution capacity", decision)
		}
	}
	settlement.ModelVersion = "resolved-v1"
	first, err := engine.Settle(ctx, settlement)
	if err != nil || first.State != "settled" {
		t.Fatal(first, err)
	}
	if replay, err := engine.Settle(ctx, settlement); err != nil || replay != first {
		t.Fatal("identical version proof was not idempotent", replay, err)
	}
	changed := settlement
	changed.ModelVersion = "resolved-v2"
	if _, err := engine.Settle(ctx, changed); err == nil {
		t.Fatal("settled proof was rewritten")
	}
	info, err := engine.GetAttempt(ctx, "tenant", demand.AttemptID)
	if err != nil || info.Actual["tokens"] != 4 || info.Actual["usd"] != 6 {
		t.Fatal(info, err)
	}
	for _, limit := range info.Plan.Budgets {
		balance, err := engine.BudgetBalance(ctx, "tenant", limit.Key)
		if err != nil || balance.Held != 0 || balance.Consumed != info.Actual[limit.MeterID] {
			t.Fatal("valid proof did not settle exactly once", balance, err)
		}
	}
}

func TestModelVersionProofChangesLegacySettlementReplayIdentity(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engine := engineFixture(t, engineFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	demand := engineFixtureDemand(now, "legacy", 10)
	if decision := engineAdmit(t, engine, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	claim, err := engine.ClaimDispatch(ctx, "tenant", demand.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	legacy := Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "legacy-terminal", Claim: claim, Usage: Vector{"requests": 1, "tokens": 4}}
	first, err := engine.Settle(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	changed := legacy
	changed.ModelVersion = "resolved-v1"
	if _, err := engine.Settle(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("a supplied proof retroactively changed a legacy settlement", err)
	}
	if replay, err := engine.Settle(ctx, legacy); err != nil || replay != first {
		t.Fatal("legacy empty-proof identity was not preserved", replay, err)
	}
}

func TestBoundProviderRequiresTypedModelVersionAndCompleteReusesIt(t *testing.T) {
	ctx := context.Background()
	engine, demand := adapterFixture(t, "llmplex")
	demand.Target.ModelVersion = "resolved-v1"
	demand.RequireModelVersion = true
	bound, err := engine.Bind(demand, func(context.Context, Demand) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := bound.Admit(ctx, demand.Context)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bound.ClaimDispatch(ctx); err != nil {
		t.Fatal(err)
	}
	usage := Vector{"work": 3}
	if _, err = bound.SettleTerminal(ctx, "bound-terminal", usage, ""); !errors.Is(err, ErrModelVersionProof) {
		t.Fatal("old terminal method bypassed the admission requirement", err)
	}
	if err = receipt.Complete(ctx, enforcement.Outcome{Successful: true, Usage: usage}); !errors.Is(err, ErrTerminalEvidenceRequired) {
		t.Fatal("failed version proof became terminal evidence", err)
	}
	if _, err = bound.SettleTerminalWithModelVersion(ctx, "bound-terminal", usage, "", "resolved-v2"); !errors.Is(err, ErrModelVersionProof) {
		t.Fatal("wrong bound-provider model proof accepted", err)
	}
	decision, err := bound.SettleTerminalWithModelVersion(ctx, "bound-terminal", usage, "", "resolved-v1")
	if err != nil || decision.State != "settled" {
		t.Fatal(decision, err)
	}
	for i := 0; i < 2; i++ {
		if err = receipt.Complete(ctx, enforcement.Outcome{Successful: false, Usage: usage}); err != nil {
			t.Fatal("completion dropped the typed model proof", err)
		}
	}
	info, err := engine.GetAttempt(ctx, "tenant", demand.AttemptID)
	if err != nil || info.Actual["work"] != 3 {
		t.Fatal(info, err)
	}
	if balance, err := engine.BudgetBalance(ctx, "tenant", info.Plan.Budgets[0].Key); err != nil || balance.Held != 0 || balance.Consumed != 3 {
		t.Fatal(balance, err)
	}
}

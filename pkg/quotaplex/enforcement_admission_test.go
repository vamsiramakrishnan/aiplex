package quotaplex

import (
	"context"
	"errors"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/enforcement"
)

func TestBoundAdmissionControllerUsesImmutableDemandAndDispatchReauthorization(t *testing.T) {
	engine, demand := adapterFixture(t, "llmplex")
	calls, rechecks := 0, 0
	bound, err := engine.BindWithAdmission(demand, func(_ context.Context, current Demand) error {
		rechecks++
		if current.Bounds["work"] != 4 {
			t.Error("controller mutated dispatch demand")
		}
		return nil
	}, func(ctx context.Context, current Demand) (Decision, error) {
		calls++
		decision, err := engine.Admit(ctx, current)
		current.Bounds["work"] = 999
		current.Principal.Membership.Ref.Version = "mutated"
		return decision, err
	})
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := enforcement.New([]string{"quota"}, map[string]enforcement.Provider{"quota": bound})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pipeline.Admit(context.Background(), demand.Context); err != nil {
		t.Fatal(err)
	}
	if _, err = bound.ClaimDispatch(context.Background()); err != nil || calls != 1 || rechecks != 1 {
		t.Fatal(calls, rechecks, err)
	}
	info, err := engine.GetAttempt(context.Background(), "tenant", "attempt")
	if err != nil || info.Demand.Bounds["work"] != 4 || info.Demand.Principal.Membership.Ref.Version != "1" {
		t.Fatal(info, err)
	}
}
func TestBoundAdmissionControllerCannotInventAClaimableReservation(t *testing.T) {
	engine, demand := adapterFixture(t, "llmplex")
	bound, err := engine.BindWithAdmission(demand, func(context.Context, Demand) error { return nil }, func(context.Context, Demand) (Decision, error) {
		return Decision{TenantID: "tenant", AttemptID: "attempt", Allowed: true, State: "admitted", Policy: engine.policy.Reference()}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bound.Admit(context.Background(), demand.Context); err != nil {
		t.Fatal(err)
	}
	if _, err = bound.ClaimDispatch(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatal("callback allow created unreserved dispatch right", err)
	}
}
func TestBoundAdmissionControllerRejectsCrossTenantDecision(t *testing.T) {
	engine, demand := adapterFixture(t, "llmplex")
	bound, err := engine.BindWithAdmission(demand, func(context.Context, Demand) error { return nil }, func(context.Context, Demand) (Decision, error) {
		return Decision{TenantID: "other", AttemptID: "attempt", Allowed: true, State: "admitted"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bound.Admit(context.Background(), demand.Context); !errors.Is(err, ErrConflict) {
		t.Fatal("cross-tenant admission callback accepted", err)
	}
}

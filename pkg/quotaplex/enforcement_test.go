package quotaplex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/enforcement"
	"github.com/vamsiramakrishnan/aiplex/pkg/modules"
)

func adapterFixture(t *testing.T, plane string) (*Engine, Demand) {
	t.Helper()
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	policy := Policy{Version: Version, TenantID: "tenant", ID: "quota", Revision: 1, EffectiveAt: now.Add(-time.Hour), Meters: map[string]Meter{"work": {ID: "work", Unit: "calls"}}, Accounts: []Account{{ID: "team", TenantWide: true}}, Budgets: []BudgetRule{{ID: "team-work", Scope: Scope{Kind: "account", ID: "team"}, MeterID: "work", Limit: 10, Period: Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(24 * time.Hour)}}}, Concurrency: []ConcurrencyRule{{ID: "running", Scope: Scope{Kind: "tenant"}, Limit: 1}}}
	compiled, err := Compile(policy)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(compiled, NewMemoryStore(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Activate(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	ref := contracts.SnapshotRef{ID: "membership", Version: "1"}
	digest := sha256.Sum256([]byte("native-request"))
	demand := Demand{Version: Version, Context: contracts.RequestContext{Version: contracts.Version, TenantID: "tenant", RequestID: "correlation", PrincipalID: "human", ResourceID: "surface", MembershipSnapshotRef: &ref}, Principal: Subject{ID: "human", Kind: "human", Membership: Membership{Ref: ref, TenantID: "tenant", SubjectID: "human", IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}}, LogicalOperationID: "logical", AttemptID: "attempt", PayloadDigest: hex.EncodeToString(digest[:]), Target: Target{ConfigurationDigest: hex.EncodeToString(digest[:]), Plane: plane, Provider: "provider", ResourceID: "surface", Operation: "execute"}, Bounds: Vector{"work": 4}}
	return engine, demand
}

func TestBoundQuotaNeedsTypedContextDispatchAndTerminalEvidence(t *testing.T) {
	for _, plane := range []string{"llmplex", "mcplex"} {
		t.Run(plane, func(t *testing.T) {
			engine, demand := adapterFixture(t, plane)
			ctx := context.Background()
			authorizations := 0
			bound, err := engine.Bind(demand, func(_ context.Context, d Demand) error { authorizations++; d.Bounds["work"] = 999; return nil })
			if err != nil {
				t.Fatal(err)
			}
			demand.Bounds["work"] = 999 // Binding isolated the authoritative input.
			wrong := demand.Context
			wrong.PrincipalID = "another"
			if _, err = bound.Admit(ctx, wrong); !errors.Is(err, ErrRequestContextMismatch) {
				t.Fatal(err)
			}
			pipeline, err := enforcement.New([]string{"quota"}, map[string]enforcement.Provider{"quota": bound})
			if err != nil {
				t.Fatal(err)
			}
			ticket, err := pipeline.Admit(ctx, demand.Context)
			if err != nil {
				t.Fatal(err)
			}
			if err = ticket.Complete(ctx, enforcement.Outcome{Successful: false}); !errors.Is(err, ErrTerminalEvidenceRequired) {
				t.Fatal("failure flag released unknown consumption", err)
			}
			claim, err := bound.ClaimDispatch(ctx)
			if err != nil || authorizations != 1 {
				t.Fatalf("dispatch=%+v err=%v auth=%d", claim, err, authorizations)
			}
			if _, err = bound.ClaimDispatch(ctx); err == nil {
				t.Fatal("duplicate dispatch granted")
			}
			if err = bound.RecordProviderResource(ctx, "native-job"); err != nil {
				t.Fatal(err)
			}
			if err = bound.MarkUnknown(ctx, "transport disconnected"); err != nil {
				t.Fatal(err)
			}
			if err = ticket.Abort(ctx); err == nil {
				t.Fatal("possibly dispatched work was refunded")
			}
			if _, err = bound.SettleTerminal(ctx, "final", nil, ""); !errors.Is(err, ErrTerminalEvidenceRequired) {
				t.Fatal(err)
			}
			if _, err = bound.SettleTerminal(ctx, "wrong-resource", Vector{"work": 3}, "other-job"); !errors.Is(err, ErrConflict) {
				t.Fatal("terminal report retargeted the resource", err)
			}
			decision, err := bound.SettleTerminal(ctx, "final", Vector{"work": 3}, "native-job")
			if err != nil || decision.State != "settled" {
				t.Fatalf("terminal %+v %v", decision, err)
			}
			if err = ticket.Complete(ctx, enforcement.Outcome{Successful: false}); err != nil {
				t.Fatal(err)
			}
			attempt, err := engine.GetAttempt(ctx, "tenant", "attempt")
			if err != nil || attempt.Actual["work"] != 3 {
				t.Fatalf("consumption lost: %+v %v", attempt, err)
			}
			balance, err := engine.BudgetBalance(ctx, "tenant", attempt.Plan.Budgets[0].Key)
			if err != nil || balance.Consumed != 3 || balance.Held != 0 || balance.Available != 7 {
				t.Fatalf("wrong settled balance %+v %v", balance, err)
			}
		})
	}
}

func TestBoundQuotaRechecksAuthorityAndRejectsCompletedReplay(t *testing.T) {
	engine, demand := adapterFixture(t, "mcplex")
	ctx := context.Background()
	denied := errors.New("execution revoked")
	bound, err := engine.Bind(demand, func(context.Context, Demand) error { return denied })
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := bound.Admit(ctx, demand.Context)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bound.ClaimDispatch(ctx); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	if err = receipt.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := engine.GetAttempt(ctx, "tenant", demand.AttemptID)
	if err != nil || info.Decision.State != "aborted" {
		t.Fatal(info, err)
	}
	replay, err := engine.Bind(demand, func(context.Context, Demand) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = replay.Admit(ctx, demand.Context); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("completed replay created another provider receipt", err)
	}
}

func TestQuotaModuleRequiresExplicitActivationAndPreservesStore(t *testing.T) {
	engine, demand := adapterFixture(t, "mcplex")
	unactivated, err := NewEngine(engine.policy, NewMemoryStore(), engine.clock)
	if err != nil {
		t.Fatal(err)
	}
	if err = NewModule(unactivated).Start(context.Background()); err == nil {
		t.Fatal("module silently activated its own allowance")
	}
	module := NewModule(engine)
	registry := modules.NewRegistry()
	if err := registry.Register(module); err != nil {
		t.Fatal(err)
	}
	runtime := modules.NewRuntime(registry)
	ctx := context.Background()
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Admit(ctx, demand); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Ready(ctx); err == nil {
		t.Fatal("stopped module ready")
	}
	if _, err := engine.GetAttempt(ctx, "tenant", demand.AttemptID); err != nil {
		t.Fatal("module stop discarded accounting", err)
	}
	if err := NewModule(nil).Start(ctx); err == nil {
		t.Fatal("missing engine accepted")
	}
	if _, err := engine.Bind(demand, nil); err == nil {
		t.Fatal("missing execution authorizer accepted")
	}
}

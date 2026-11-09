package quotaplex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPriceOnlyMeterIdentityCannotChangeOnPolicyRevision(t *testing.T) {
	policy, _, now := quotaPolicyFixture()
	// The output meter is priced and observed, but has no direct quantity cap.
	budgets := policy.Budgets[:0]
	for _, rule := range policy.Budgets {
		if rule.MeterID != "output" {
			budgets = append(budgets, rule)
		}
	}
	policy.Budgets = budgets
	compiled, err := Compile(policy)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	engine, err := NewEngine(compiled, store, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = engine.Activate(ctx, ""); err != nil {
		t.Fatal(err)
	}
	policy.Revision++
	meter := policy.Meters["output"]
	meter.Unit = "bytes"
	policy.Meters["output"] = meter
	replacement, err := Compile(policy)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := NewEngine(replacement, store, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err = changed.Activate(ctx, compiled.Reference().Digest); !errors.Is(err, ErrConflict) {
		t.Fatal("same price revision reinterpreted a raw unit", err)
	}
	if err = engine.Ready(ctx); err != nil {
		t.Fatal("failed replacement changed active policy", err)
	}
}

func TestPolicyRejectsUnsupportedActivationFanoutBeforeWrites(t *testing.T) {
	policy, _, _ := quotaPolicyFixture()
	for i := len(policy.Meters); i < 64; i++ {
		id := fmt.Sprintf("extra-%d", i)
		policy.Meters[id] = Meter{ID: id, Unit: "rows"}
	}
	book := policy.PriceBooks[0]
	policy.PriceBooks = nil
	for i := 0; i < 64; i++ {
		copy := book
		copy.ID = fmt.Sprintf("book-%d", i)
		copy.Match.Provider = fmt.Sprintf("provider-%d", i)
		policy.PriceBooks = append(policy.PriceBooks, copy)
	}
	if _, err := Compile(policy); !errors.Is(err, ErrTransactionLimit) {
		t.Fatal("configuration cannot fit one atomic activation", err)
	}
}

func TestConfigurationRevisionIsIndependentAccountingIdentity(t *testing.T) {
	engine, demand := adapterFixture(t, "mcplex")
	ctx := context.Background()
	missing := demand
	missing.Target.ConfigurationDigest = ""
	if err := ValidateDemand(missing); err == nil {
		t.Fatal("unversioned target accepted")
	}
	first, err := engine.Admit(ctx, demand)
	if err != nil || !first.Allowed {
		t.Fatal(first, err)
	}
	changed := demand
	changed.Target.ConfigurationDigest = strings.Repeat("d", 64)
	if _, err = engine.Admit(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("attempt retargeted after connection change", err)
	}
	changed = demand
	changed.Context.RequestID = "new-http-correlation"
	if replay, err := engine.Admit(ctx, changed); err != nil || replay != first {
		t.Fatal("correlation retry changed accounting", replay, err)
	}
}

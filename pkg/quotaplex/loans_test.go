package quotaplex

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func loanFixturePolicy(now time.Time) Policy {
	return Policy{Version: Version, TenantID: "tenant", ID: "loan-policy", Revision: 1, EffectiveAt: now.Add(-time.Hour),
		Meters:   map[string]Meter{"tokens": {ID: "tokens", Unit: "tokens"}, "requests": {ID: "requests", Unit: "requests"}},
		Accounts: []Account{{ID: "lender", TenantWide: true}, {ID: "borrower", TenantWide: true}},
		Budgets: []BudgetRule{
			{ID: "lender-budget", Scope: Scope{Kind: "account", ID: "lender"}, MeterID: "tokens", Limit: 100, Period: Period{Kind: "fixed", Start: now.Add(-time.Hour), End: now.Add(23 * time.Hour)}},
			{ID: "borrower-budget", Scope: Scope{Kind: "account", ID: "borrower"}, MeterID: "tokens", Limit: 10, Period: Period{Kind: "fixed", Start: now.Add(-2 * time.Hour), End: now.Add(46 * time.Hour)}},
		},
		Sharing: []LoanRule{{ID: "lend", LenderRuleID: "lender-budget", BorrowerRuleID: "borrower-budget", MaxAmount: 80, ProtectedFloor: 20, MaxDuration: time.Hour}},
	}
}

func loanFixtureDemand(now time.Time, attempt, account string, tokens int64) Demand {
	demand := engineFixtureDemand(now, attempt, tokens)
	demand.FundingAccountID = account
	return demand
}

func loanGrant(t *testing.T, engine *Engine, id string, amount int64, expiry time.Time) LoanInfo {
	t.Helper()
	info, err := engine.GrantLoan(context.Background(), LoanRequest{TenantID: "tenant", ID: id, RuleID: "lend", Amount: amount, ExpiresAt: expiry})
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func loanBalance(t *testing.T, engine *Engine, key string) BudgetBalance {
	t.Helper()
	balance, err := engine.BudgetBalance(context.Background(), "tenant", key)
	if err != nil {
		t.Fatal(err)
	}
	return balance
}

func TestLoanGrantConservesUnequalWindowsAndReplaysExactlyOnce(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engine := engineFixture(t, loanFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	request := LoanRequest{TenantID: "tenant", ID: "loan", RuleID: "lend", Amount: 80, ExpiresAt: now.Add(time.Hour)}
	info, err := engine.GrantLoan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if info.Lender.Window.End.Equal(info.Borrower.Window.End) || info.Outstanding != 80 {
		t.Fatal("different allocation periods were normalized", info)
	}
	beforeLender, beforeBorrower := loanBalance(t, engine, info.Lender.Key), loanBalance(t, engine, info.Borrower.Key)
	if beforeLender.Available != 20 || beforeBorrower.Available != 90 || beforeLender.Debited != beforeBorrower.Credited || beforeLender.Available+beforeBorrower.Available != 110 {
		t.Fatal("loan minted capacity or crossed protected floor", beforeLender, beforeBorrower)
	}
	if _, err := engine.GrantLoan(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if after := loanBalance(t, engine, info.Borrower.Key); after != beforeBorrower {
		t.Fatal("identical loan replay credited twice", beforeBorrower, after)
	}
	request.Amount--
	if _, err := engine.GrantLoan(context.Background(), request); !errors.Is(err, ErrConflict) {
		t.Fatal("changed replay accepted", err)
	}
	if _, err := engine.GrantLoan(context.Background(), LoanRequest{TenantID: "tenant", ID: "extra", RuleID: "lend", Amount: 1, ExpiresAt: now.Add(time.Minute)}); !errors.Is(err, ErrDenied) {
		t.Fatal("aggregate/floor bound bypassed with another loan ID", err)
	}
	if got := loanBalance(t, engine, info.Lender.Key); got != beforeLender {
		t.Fatal("failed grant changed lender", got)
	}
	closed, err := engine.ReturnUnusedLoan(context.Background(), "tenant", "loan")
	if err != nil || closed.Outstanding != 0 || closed.Returned != 80 || closed.State != "closed" {
		t.Fatal(closed, err)
	}
	if again, err := engine.ReturnUnusedLoan(context.Background(), "tenant", "loan"); err != nil || !reflect.DeepEqual(again, closed) {
		t.Fatal("closed return was not idempotent", again, err)
	}
	if lender, borrower := loanBalance(t, engine, info.Lender.Key), loanBalance(t, engine, info.Borrower.Key); lender.Available != 100 || borrower.Available != 10 || lender.Debited != 0 || borrower.Credited != 0 {
		t.Fatal("return broke conservation", lender, borrower)
	}
}

func TestLoanExpiryAndLateAbortCannotReviveExpiredCredit(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	base := now
	policy := loanFixturePolicy(now)
	policy.Budgets[1].Limit = 0
	engine := engineFixture(t, policy, func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	info := loanGrant(t, engine, "loan", 80, now.Add(10*time.Minute))
	demand := loanFixtureDemand(base, "held", "borrower", 80)
	if decision := engineAdmit(t, engine, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	now = base.Add(11 * time.Minute)
	remaining, err := engine.ReturnUnusedLoan(context.Background(), "tenant", "loan")
	if err != nil || remaining.Returned != 0 || remaining.Outstanding != 80 || remaining.State != "expired_committed" {
		t.Fatal("held work was recalled", remaining, err)
	}
	if err := engine.Abort(context.Background(), "tenant", demand.AttemptID); err != nil {
		t.Fatal(err)
	}
	decision := engineAdmit(t, engine, loanFixtureDemand(now, "after-late-abort", "borrower", 1))
	if decision.Allowed || decision.Reason != "loan_reconciliation_required" {
		t.Fatal("expired credit became spendable after abort", decision)
	}
	if balance := loanBalance(t, engine, info.Borrower.Key); balance.Held != 0 || balance.Credited != 80 {
		t.Fatal("denied admission mutated loan or created another hold", balance)
	}
	closed, err := engine.ReturnUnusedLoan(context.Background(), "tenant", "loan")
	if err != nil || closed.Returned != 80 || closed.Outstanding != 0 {
		t.Fatal(closed, err)
	}
	if decision := engineAdmit(t, engine, loanFixtureDemand(now, "after-return", "borrower", 1)); decision.Allowed || decision.Reason != "budget_exhausted" {
		t.Fatal("return left fresh borrowed capacity", decision)
	}
}

func TestLoanReturnRetainsConsumedExposureAndOriginalTerms(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	base := now
	store := NewMemoryStore()
	policy := loanFixturePolicy(now)
	engine := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	info := loanGrant(t, engine, "loan", 80, now.Add(10*time.Minute))
	demand := loanFixtureDemand(now, "used", "borrower", 70)
	claim := lifecycleClaim(t, engine, demand)
	if _, err := engine.Settle(context.Background(), Settlement{TenantID: "tenant", AttemptID: demand.AttemptID, EventID: "terminal", Claim: claim, Usage: Vector{"requests": 1, "tokens": 50}}); err != nil {
		t.Fatal(err)
	}
	// Removing the current sharing rule cannot erase a prior loan's obligations
	// or make its return depend on new policy terms.
	policy.Revision++
	policy.Sharing = nil
	updated := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, updated, engine.policy.Reference().Digest)
	now = base.Add(11 * time.Minute)
	remaining, err := updated.ReturnUnusedLoan(context.Background(), "tenant", "loan")
	if err != nil || remaining.Returned != 40 || remaining.Outstanding != 40 || remaining.State != "expired_committed" || remaining.Terms != info.Terms {
		t.Fatal("spent capacity was recalled or terms were rewritten", remaining, err)
	}
	if borrower := loanBalance(t, updated, info.Borrower.Key); borrower.Consumed != 50 || borrower.Credited != 40 || borrower.Available != 0 {
		t.Fatal("return destroyed actual usage", borrower)
	}
	if _, err := updated.GrantLoan(context.Background(), info.Request); err != nil {
		t.Fatal("historical replay was re-evaluated under new policy", err)
	}
	if _, err := updated.GrantLoan(context.Background(), LoanRequest{TenantID: "tenant", ID: "new", RuleID: "lend", Amount: 1, ExpiresAt: now.Add(time.Minute)}); !errors.Is(err, ErrDenied) {
		t.Fatal("removed sharing rule enabled a new loan", err)
	}
}

func TestLoanConcurrentGrantsRespectAggregateAndProtectedCapacity(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	policy := loanFixturePolicy(now)
	policy.Sharing[0].MaxAmount = 40
	store := NewMemoryStore()
	first := engineFixture(t, policy, func() time.Time { return now }, store)
	second := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, first, "")
	var total atomic.Int64
	var workers sync.WaitGroup
	for i := 0; i < 30; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			engine := first
			if i%2 == 1 {
				engine = second
			}
			_, err := engine.GrantLoan(context.Background(), LoanRequest{TenantID: "tenant", ID: fmt.Sprintf("loan-%02d", i), RuleID: "lend", Amount: 10, ExpiresAt: now.Add(time.Hour)})
			if err == nil {
				total.Add(10)
			} else if !errors.Is(err, ErrDenied) {
				t.Error(err)
			}
		}(i)
	}
	workers.Wait()
	if total.Load() != 40 {
		t.Fatal("aggregate outstanding limit raced", total.Load())
	}
	lender, _ := loanBudgetLimit(first.policy, policy.Budgets[0], now)
	borrower, _ := loanBudgetLimit(first.policy, policy.Budgets[1], now)
	if debit, credit := loanBalance(t, first, lender.Key), loanBalance(t, first, borrower.Key); debit.Debited != 40 || credit.Credited != 40 || debit.Available+credit.Available != 110 {
		t.Fatal("concurrent transfer broke conservation", debit, credit)
	}
}

func TestLoansCannotRelendBorrowedCreditOrExceedWindowExpiry(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	policy := loanFixturePolicy(now)
	policy.Budgets[1].Limit = 0
	policy.Accounts = append(policy.Accounts, Account{ID: "third", TenantWide: true})
	policy.Budgets = append(policy.Budgets, BudgetRule{ID: "third-budget", Scope: Scope{Kind: "account", ID: "third"}, MeterID: "tokens", Limit: 0, Period: policy.Budgets[1].Period})
	policy.Sharing = append(policy.Sharing, LoanRule{ID: "on-lend", LenderRuleID: "borrower-budget", BorrowerRuleID: "third-budget", MaxAmount: 80, MaxDuration: time.Hour})
	engine := engineFixture(t, policy, func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	loanGrant(t, engine, "first", 80, now.Add(time.Hour))
	if _, err := engine.GrantLoan(context.Background(), LoanRequest{TenantID: "tenant", ID: "recursive", RuleID: "on-lend", Amount: 1, ExpiresAt: now.Add(time.Minute)}); !errors.Is(err, ErrDenied) {
		t.Fatal("borrowed credit was recursively lent", err)
	}
	for _, request := range []LoanRequest{
		{TenantID: "tenant", ID: "expired", RuleID: "lend", Amount: 1, ExpiresAt: now},
		{TenantID: "tenant", ID: "too-long", RuleID: "lend", Amount: 1, ExpiresAt: now.Add(2 * time.Hour)},
		{TenantID: "other", ID: "cross-tenant", RuleID: "lend", Amount: 1, ExpiresAt: now.Add(time.Minute)},
	} {
		if _, err := engine.GrantLoan(context.Background(), request); err == nil {
			t.Fatal("unsafe loan request accepted", request)
		}
	}
	policy = loanFixturePolicy(now)
	policy.Budgets[0].Period.End = now.Add(10 * time.Minute)
	windowBound := engineFixture(t, policy, func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, windowBound, "")
	if _, err := windowBound.GrantLoan(context.Background(), LoanRequest{TenantID: "tenant", ID: "beyond-source", RuleID: "lend", Amount: 1, ExpiresAt: now.Add(11 * time.Minute)}); !errors.Is(err, ErrDenied) {
		t.Fatal("loan minted capacity outside source window", err)
	}
}

func TestLoanIndexIsBoundedAndClosedLoansDoNotAccumulateInHotIndex(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	engine := engineFixture(t, loanFixturePolicy(now), func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	for i := 0; i < maxBudgetLoans; i++ {
		loanGrant(t, engine, fmt.Sprintf("loan-%d", i), 1, now.Add(time.Hour))
	}
	if _, err := engine.GrantLoan(context.Background(), LoanRequest{TenantID: "tenant", ID: "overflow-index", RuleID: "lend", Amount: 1, ExpiresAt: now.Add(time.Hour)}); !errors.Is(err, ErrTransactionLimit) {
		t.Fatal("loan hot index was unbounded", err)
	}
	if _, err := engine.ReturnUnusedLoan(context.Background(), "tenant", "loan-0"); err != nil {
		t.Fatal(err)
	}
	loanGrant(t, engine, "new-after-close", 1, now.Add(time.Hour))
	if old, err := engine.GetLoan(context.Background(), "tenant", "loan-0"); err != nil || old.State != "closed" {
		t.Fatal("pruning hot index discarded replay history", old, err)
	}
}

func TestLoanReturnUsesCurrentReducedLimitWithoutRecallingHeldCredit(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	policy := loanFixturePolicy(now)
	policy.Budgets[1].Limit = 100
	store := NewMemoryStore()
	engine := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	info := loanGrant(t, engine, "loan", 50, now.Add(time.Hour))
	demand := loanFixtureDemand(now, "held-before-reduction", "borrower", 100)
	if decision := engineAdmit(t, engine, demand); !decision.Allowed {
		t.Fatal(decision)
	}
	policy.Revision++
	policy.Budgets[1].Limit = 0
	updated := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, updated, engine.policy.Reference().Digest)
	// The old engine instance must observe the new active policy too; the stored
	// projection still has the previous 100 limit because no new admission ran.
	remaining, err := engine.ReturnUnusedLoan(context.Background(), "tenant", "loan")
	if err != nil || remaining.Returned != 0 || remaining.Outstanding != 50 {
		t.Fatal("stale limit recalled credit backing an active hold", remaining, err)
	}
	if balance := loanBalance(t, engine, info.Borrower.Key); balance.Held != 100 || balance.Credited != 50 {
		t.Fatal("policy reduction return changed committed exposure", balance)
	}
	if err := updated.Abort(context.Background(), "tenant", demand.AttemptID); err != nil {
		t.Fatal(err)
	}
	closed, err := engine.ReturnUnusedLoan(context.Background(), "tenant", "loan")
	if err != nil || closed.Returned != 50 || closed.Outstanding != 0 {
		t.Fatal("known abort did not make unused credit returnable", closed, err)
	}
	if balance := loanBalance(t, engine, info.Borrower.Key); balance.Limit != 0 || balance.LimitPolicy != updated.policy.Reference() || balance.Available != 0 {
		t.Fatal("return wrote stale policy limit back into projection", balance)
	}
}

func TestLoanMaintenanceAfterWindowExpiryAndMissingRuleFailsClosed(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	base := now
	policy := loanFixturePolicy(now)
	store := NewMemoryStore()
	engine := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, engine, "")
	info := loanGrant(t, engine, "after-window", 50, now.Add(time.Hour))
	now = base.Add(7 * 24 * time.Hour)
	closed, err := engine.ReturnUnusedLoan(context.Background(), "tenant", "after-window")
	if err != nil || closed.Returned != 50 || closed.Outstanding != 0 || closed.Borrower.Window != info.Borrower.Window {
		t.Fatal("late maintenance resolved a new allocation window", closed, err)
	}
	now = base
	other := loanGrant(t, engine, "removed-rule", 50, now.Add(time.Hour))
	policy.Revision++
	policy.Sharing = nil
	policy.Budgets = policy.Budgets[:1]
	updated := engineFixture(t, policy, func() time.Time { return now }, store)
	engineActivate(t, updated, engine.policy.Reference().Digest)
	before := loanBalance(t, engine, other.Borrower.Key)
	if _, err := updated.ReturnUnusedLoan(context.Background(), "tenant", "removed-rule"); !errors.Is(err, ErrConflict) {
		t.Fatal("removed rule fabricated a new current limit", err)
	}
	if after := loanBalance(t, engine, other.Borrower.Key); after != before {
		t.Fatal("failed maintenance changed allocation", before, after)
	}
}

func TestLenderCannotLoanHeldCapacityOrBypassParentCaps(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	policy := loanFixturePolicy(now)
	policy.Budgets = append(policy.Budgets, BudgetRule{ID: "parent", Scope: Scope{Kind: "tenant"}, MeterID: "tokens", Limit: 40, Period: policy.Budgets[0].Period})
	engine := engineFixture(t, policy, func() time.Time { return now }, NewMemoryStore())
	engineActivate(t, engine, "")
	if decision := engineAdmit(t, engine, loanFixtureDemand(now, "lender-held", "lender", 30)); !decision.Allowed {
		t.Fatal(decision)
	}
	if _, err := engine.GrantLoan(context.Background(), LoanRequest{TenantID: "tenant", ID: "held-capacity", RuleID: "lend", Amount: 60, ExpiresAt: now.Add(time.Hour)}); !errors.Is(err, ErrDenied) {
		t.Fatal("lender held work was included in unused capacity", err)
	}
	loanGrant(t, engine, "unused-only", 50, now.Add(time.Hour))
	if decision := engineAdmit(t, engine, loanFixtureDemand(now, "over-parent", "borrower", 20)); decision.Allowed || decision.Reason != "budget_exhausted" {
		t.Fatal("borrowed child allowance enlarged the parent cap", decision)
	}
}

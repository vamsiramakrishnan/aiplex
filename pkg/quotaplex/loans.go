package quotaplex

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"
)

const (
	loanKind          = "loan"
	loanIndexKind     = "loan-index"
	loanRuleIndexKind = "loan-rule-index"
	loanLedgerKind    = "loan-ledger"
	maxBudgetLoans    = 8
)

// LoanRequest is a trusted, authorized administration operation. Validation
// does not authorize the caller to move another account's allocation.
type LoanRequest struct {
	TenantID  string    `json:"tenant_id"`
	ID        string    `json:"id"`
	RuleID    string    `json:"rule_id"`
	Amount    int64     `json:"amount"`
	ExpiresAt time.Time `json:"expires_at"`
}

// LoanInfo pins the original policy, terms and both accounting windows. A
// committed remainder may survive expiry until trusted work is settled.
type LoanInfo struct {
	Request     LoanRequest `json:"request"`
	Policy      PolicyRef   `json:"policy"`
	Terms       LoanRule    `json:"terms"`
	Lender      BudgetLimit `json:"lender"`
	Borrower    BudgetLimit `json:"borrower"`
	GrantedAt   time.Time   `json:"granted_at"`
	Returned    int64       `json:"returned"`
	Outstanding int64       `json:"outstanding"`
	State       string      `json:"state"` // open, expired_committed, or closed
}

type loanRecord struct {
	Info        LoanInfo `json:"info"`
	Fingerprint string   `json:"fingerprint"`
	RuleIndexID string   `json:"rule_index_id"`
}

type loanIndex struct {
	IDs []string `json:"ids"`
}

type loanRuleIndex struct {
	IDs         []string `json:"ids"`
	Outstanding int64    `json:"outstanding"`
}

type loanLedgerEntry struct {
	LoanID      string    `json:"loan_id"`
	Action      string    `json:"action"`
	At          time.Time `json:"at"`
	Policy      PolicyRef `json:"policy"`
	Amount      int64     `json:"amount"`
	LenderKey   string    `json:"lender_key"`
	BorrowerKey string    `json:"borrower_key"`
}

func validateLoanIndex(index loanIndex) error {
	if len(index.IDs) > maxBudgetLoans {
		return ErrTransactionLimit
	}
	seen := map[string]bool{}
	for _, id := range index.IDs {
		if policyText(id, false) != nil || seen[id] {
			return fmt.Errorf("invalid loan index: %w", ErrConflict)
		}
		seen[id] = true
	}
	return nil
}

func readLoanIndex(tx Tx, key string) (loanIndex, error) {
	var index loanIndex
	if err := tx.Get(loanIndexKind, key, &index); err != nil && !errors.Is(err, ErrNotFound) {
		return loanIndex{}, err
	}
	return index, validateLoanIndex(index)
}

func loanBudgetLimit(policy *CompiledPolicy, rule BudgetRule, now time.Time) (BudgetLimit, error) {
	if rule.Scope.Kind != "account" || rule.Scope.ID == "" {
		return BudgetLimit{}, fmt.Errorf("sharing requires explicit account budgets")
	}
	window, err := rule.Period.Resolve(now)
	if err != nil {
		return BudgetLimit{}, err
	}
	limit := rule.Limit
	if rule.Population != nil {
		if rule.Population.ObservedAt.After(now) {
			return BudgetLimit{}, fmt.Errorf("loan population snapshot is not yet valid")
		}
		limit = rule.Population.Count * rule.Population.PerMember // Compile checked overflow.
	}
	lineage := policyLineage(policy.policy.ID, "budget", rule.ID)
	key := policyKey(lineage, rule.Scope.ID, window.Start.UTC().Format(time.RFC3339Nano), window.End.UTC().Format(time.RFC3339Nano))
	return BudgetLimit{Key: key, LineageKey: lineage, Shape: policy.lineages[lineage], RuleID: rule.ID, MeterID: rule.MeterID, Window: window, Limit: limit}, nil
}

func validateLoanRecord(record loanRecord) error {
	info := record.Info
	if info.Request.Amount <= 0 || info.Returned < 0 || info.Returned > info.Request.Amount || info.Outstanding != info.Request.Amount-info.Returned || info.Request.ID == "" || info.Request.TenantID == "" || info.Terms.ID != info.Request.RuleID || info.Terms.LenderRuleID != info.Lender.RuleID || info.Terms.BorrowerRuleID != info.Borrower.RuleID || record.RuleIndexID != quotaHash("loan-rule", info.Policy.ID, info.Terms.ID, info.Borrower.Key) || record.Fingerprint != quotaHash(info.Request) || info.Lender.Key == "" || info.Borrower.Key == "" || info.Lender.Key == info.Borrower.Key || info.Lender.MeterID != info.Borrower.MeterID || !validAccountingTime(info.GrantedAt) || !info.Request.ExpiresAt.After(info.GrantedAt) || info.Request.ExpiresAt.After(info.Lender.Window.End) || info.Request.ExpiresAt.After(info.Borrower.Window.End) {
		return fmt.Errorf("invalid persisted loan: %w", ErrConflict)
	}
	if (info.Outstanding == 0 && info.State != "closed") || (info.Outstanding > 0 && info.State != "open" && info.State != "expired_committed") {
		return fmt.Errorf("invalid persisted loan state: %w", ErrConflict)
	}
	return nil
}

// GrantLoan lends only uncommitted original allocation. Borrowed credit is
// deliberately excluded from lendable capacity, preventing recursive lending.
// MaxAmount bounds outstanding loans for this rule and borrower window.
func (e *Engine) GrantLoan(ctx context.Context, request LoanRequest) (LoanInfo, error) {
	if policyText(request.TenantID, false) != nil || policyText(request.ID, false) != nil || policyText(request.RuleID, false) != nil || request.Amount <= 0 || !validAccountingTime(request.ExpiresAt) || request.TenantID != e.policy.policy.TenantID {
		return LoanInfo{}, fmt.Errorf("valid tenant-scoped loan identity, amount and expiry required")
	}
	request.ExpiresAt = request.ExpiresAt.UTC()
	fingerprint := quotaHash(request)
	now := e.clock().UTC()
	if !validAccountingTime(now) {
		return LoanInfo{}, fmt.Errorf("valid quota clock required")
	}
	var result LoanInfo
	err := e.store.Update(ctx, request.TenantID, func(tx Tx) error {
		result = LoanInfo{}
		var existing loanRecord
		if err := tx.Get(loanKind, request.ID, &existing); err == nil {
			if err := validateLoanRecord(existing); err != nil {
				return err
			}
			if existing.Fingerprint != fingerprint {
				return ErrConflict
			}
			result = existing.Info
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		var head PolicyRef
		if err := tx.Get(policyHeadKind, "active", &head); err != nil {
			return err
		}
		if head != e.policy.ref || now.Before(e.policy.policy.EffectiveAt) || (!e.policy.policy.ExpiresAt.IsZero() && !now.Before(e.policy.policy.ExpiresAt)) {
			return fmt.Errorf("loan policy is not active: %w", ErrConflict)
		}
		var terms LoanRule
		found := false
		for _, rule := range e.policy.policy.Sharing {
			if rule.ID == request.RuleID {
				terms, found = rule, true
				break
			}
		}
		if !found || request.Amount > terms.MaxAmount {
			return fmt.Errorf("loan is not permitted by sharing policy: %w", ErrDenied)
		}
		var lenderRule, borrowerRule BudgetRule
		for _, rule := range e.policy.policy.Budgets {
			if rule.ID == terms.LenderRuleID {
				lenderRule = rule
			}
			if rule.ID == terms.BorrowerRuleID {
				borrowerRule = rule
			}
		}
		lenderLimit, err := loanBudgetLimit(e.policy, lenderRule, now)
		if err != nil {
			return err
		}
		borrowerLimit, err := loanBudgetLimit(e.policy, borrowerRule, now)
		if err != nil {
			return err
		}
		if !request.ExpiresAt.After(now) || request.ExpiresAt.After(now.Add(terms.MaxDuration)) || request.ExpiresAt.After(lenderLimit.Window.End) || request.ExpiresAt.After(borrowerLimit.Window.End) {
			return fmt.Errorf("loan expiry exceeds its duration or accounting windows: %w", ErrDenied)
		}
		lender, err := loadBudget(tx, lenderLimit)
		if err != nil {
			return err
		}
		borrower, err := loadBudget(tx, borrowerLimit)
		if err != nil {
			return err
		}
		lendable := new(big.Int).SetInt64(lenderLimit.Limit)
		for _, value := range []int64{lender.Held, lender.Consumed, lender.Debited, terms.ProtectedFloor} {
			lendable.Sub(lendable, big.NewInt(value))
		}
		if lendable.Cmp(big.NewInt(request.Amount)) < 0 {
			return fmt.Errorf("lender has no uncommitted protected capacity: %w", ErrDenied)
		}
		index, err := readLoanIndex(tx, borrowerLimit.Key)
		if err != nil {
			return err
		}
		if len(index.IDs) == maxBudgetLoans {
			return fmt.Errorf("borrower loan index is full: %w", ErrTransactionLimit)
		}
		if loanIndexContains(index.IDs, request.ID) {
			return fmt.Errorf("loan index has no matching record: %w", ErrConflict)
		}
		ruleIndexID := quotaHash("loan-rule", e.policy.policy.ID, terms.ID, borrowerLimit.Key)
		var ruleIndex loanRuleIndex
		if err := tx.Get(loanRuleIndexKind, ruleIndexID, &ruleIndex); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if validateLoanIndex(loanIndex{IDs: ruleIndex.IDs}) != nil || loanIndexContains(ruleIndex.IDs, request.ID) || ruleIndex.Outstanding < 0 || ruleIndex.Outstanding > terms.MaxAmount || request.Amount > terms.MaxAmount-ruleIndex.Outstanding {
			return fmt.Errorf("sharing rule outstanding limit exceeded: %w", ErrDenied)
		}
		lender.Debited, err = checkedAdd(lender.Debited, request.Amount)
		if err != nil {
			return err
		}
		borrower.Credited, err = checkedAdd(borrower.Credited, request.Amount)
		if err != nil {
			return err
		}
		lender.Limit, borrower.Limit = lenderLimit.Limit, borrowerLimit.Limit
		lender.LimitPolicy, borrower.LimitPolicy = e.policy.ref, e.policy.ref
		index.IDs = append(index.IDs, request.ID)
		ruleIndex.IDs = append(ruleIndex.IDs, request.ID)
		sort.Strings(index.IDs)
		sort.Strings(ruleIndex.IDs)
		ruleIndex.Outstanding += request.Amount // Checked against MaxAmount above.
		info := LoanInfo{Request: request, Policy: e.policy.ref, Terms: terms, Lender: lenderLimit, Borrower: borrowerLimit, GrantedAt: now, Outstanding: request.Amount, State: "open"}
		record := loanRecord{Info: info, Fingerprint: fingerprint, RuleIndexID: ruleIndexID}
		for _, update := range []struct {
			kind, key string
			value     any
		}{{budgetKind, lenderLimit.Key, lender}, {budgetKind, borrowerLimit.Key, borrower}, {loanIndexKind, borrowerLimit.Key, index}, {loanRuleIndexKind, ruleIndexID, ruleIndex}} {
			if err := tx.Put(update.kind, update.key, update.value); err != nil {
				return err
			}
		}
		if err := tx.Create(loanKind, request.ID, record); err != nil {
			return err
		}
		if err := tx.Create(loanLedgerKind, quotaHash(request.ID, "grant"), loanLedgerEntry{LoanID: request.ID, Action: "grant", At: now, Policy: info.Policy, Amount: request.Amount, LenderKey: lenderLimit.Key, BorrowerKey: borrowerLimit.Key}); err != nil {
			return err
		}
		result = info
		return nil
	})
	if err != nil {
		return LoanInfo{}, err
	}
	return result, nil
}

// ReturnUnusedLoan atomically returns only currently available borrower capacity.
// Repeated calls are safe; a later call can return capacity freed by known abort
// or settlement. This maintenance remains available after policy changes/expiry.
func (e *Engine) ReturnUnusedLoan(ctx context.Context, tenant, id string) (LoanInfo, error) {
	if tenant != e.policy.policy.TenantID || policyText(tenant, false) != nil || policyText(id, false) != nil {
		return LoanInfo{}, fmt.Errorf("loan maintenance tenant does not match engine")
	}
	now := e.clock().UTC()
	if !validAccountingTime(now) {
		return LoanInfo{}, fmt.Errorf("valid quota clock required")
	}
	var result LoanInfo
	err := e.store.Update(ctx, tenant, func(tx Tx) error {
		result = LoanInfo{}
		var record loanRecord
		if err := tx.Get(loanKind, id, &record); err != nil {
			return err
		}
		if err := validateLoanRecord(record); err != nil {
			return err
		}
		info := record.Info
		if info.Request.TenantID != tenant {
			return fmt.Errorf("loan crosses tenant namespace: %w", ErrConflict)
		}
		if info.Outstanding == 0 {
			result = info
			return nil
		}
		lender, err := loadBudget(tx, info.Lender)
		if err != nil {
			return err
		}
		borrower, err := loadBudget(tx, info.Borrower)
		if err != nil {
			return err
		}
		currentLimit, currentPolicy, err := currentLoanBorrowerLimit(tx, record, now)
		if err != nil {
			return err
		}
		available, err := budgetAvailable(borrower, currentLimit)
		if err != nil {
			return err
		}
		amount := min(max(available, 0), info.Outstanding)
		if amount > borrower.Credited || amount > lender.Debited {
			return fmt.Errorf("loan return conservation mismatch: %w", ErrConflict)
		}
		info.Returned += amount // At most the original positive Amount.
		info.Outstanding -= amount
		if info.Outstanding == 0 {
			info.State = "closed"
		} else if !now.Before(info.Request.ExpiresAt) {
			info.State = "expired_committed"
		}
		borrower.Credited -= amount
		lender.Debited -= amount
		borrower.Limit, borrower.LimitPolicy = currentLimit, currentPolicy
		record.Info = info
		if amount > 0 {
			var ruleIndex loanRuleIndex
			if err := tx.Get(loanRuleIndexKind, record.RuleIndexID, &ruleIndex); err != nil {
				return err
			}
			if validateLoanIndex(loanIndex{IDs: ruleIndex.IDs}) != nil || ruleIndex.Outstanding < amount || !loanIndexContains(ruleIndex.IDs, id) {
				return fmt.Errorf("loan rule index mismatch: %w", ErrConflict)
			}
			ruleIndex.Outstanding -= amount
			if info.Outstanding == 0 {
				ruleIndex.IDs = loanIndexWithout(ruleIndex.IDs, id)
			}
			if err := tx.Put(loanRuleIndexKind, record.RuleIndexID, ruleIndex); err != nil {
				return err
			}
			for _, update := range []struct {
				key   string
				value budgetRecord
			}{{info.Lender.Key, lender}, {info.Borrower.Key, borrower}} {
				if err := tx.Put(budgetKind, update.key, update.value); err != nil {
					return err
				}
			}
			if err := tx.Create(loanLedgerKind, quotaHash(id, "return", info.Returned), loanLedgerEntry{LoanID: id, Action: "return", At: now, Policy: info.Policy, Amount: amount, LenderKey: info.Lender.Key, BorrowerKey: info.Borrower.Key}); err != nil {
				return err
			}
		}
		if info.Outstanding == 0 {
			index, err := readLoanIndex(tx, info.Borrower.Key)
			if err != nil {
				return err
			}
			if !loanIndexContains(index.IDs, id) {
				return fmt.Errorf("borrower loan index mismatch: %w", ErrConflict)
			}
			index.IDs = loanIndexWithout(index.IDs, id)
			if err := tx.Put(loanIndexKind, info.Borrower.Key, index); err != nil {
				return err
			}
		}
		if err := tx.Put(loanKind, id, record); err != nil {
			return err
		}
		result = info
		return nil
	})
	if err != nil {
		return LoanInfo{}, err
	}
	return result, nil
}

func (e *Engine) GetLoan(ctx context.Context, tenant, id string) (LoanInfo, error) {
	if tenant != e.policy.policy.TenantID || policyText(tenant, false) != nil || policyText(id, false) != nil {
		return LoanInfo{}, fmt.Errorf("loan lookup tenant does not match engine")
	}
	var record loanRecord
	if err := e.store.View(ctx, tenant, func(tx Tx) error { return tx.Get(loanKind, id, &record) }); err != nil {
		return LoanInfo{}, err
	}
	if err := validateLoanRecord(record); err != nil {
		return LoanInfo{}, err
	}
	if record.Info.Request.TenantID != tenant {
		return LoanInfo{}, fmt.Errorf("loan crosses tenant namespace: %w", ErrConflict)
	}
	return record.Info, nil
}

// A policy reduction may not yet have been written into the budget projection.
// Return must use the current configured limit for the original window, rather
// than recalling credit that now backs an existing hold. Never resolve a fresh
// window or replace the loan's original terms during this maintenance operation.
func currentLoanBorrowerLimit(tx Tx, record loanRecord, now time.Time) (int64, PolicyRef, error) {
	var head PolicyRef
	if err := tx.Get(policyHeadKind, "active", &head); err != nil {
		return 0, PolicyRef{}, err
	}
	var snapshot policyRecord
	if err := tx.Get(policyKind, quotaHash(head.ID, head.Revision), &snapshot); err != nil {
		return 0, PolicyRef{}, err
	}
	if snapshot.Reference != head || snapshot.Policy.TenantID != record.Info.Request.TenantID || head.ID != record.Info.Policy.ID || policyDigest(snapshot.Policy) != head.Digest {
		return 0, PolicyRef{}, fmt.Errorf("current loan policy identity mismatch: %w", ErrConflict)
	}
	for _, rule := range snapshot.Policy.Budgets {
		if rule.ID != record.Info.Borrower.RuleID {
			continue
		}
		meter, ok := snapshot.Policy.Meters[rule.MeterID]
		if !ok || budgetShape(rule, meter) != record.Info.Borrower.Shape {
			return 0, PolicyRef{}, fmt.Errorf("loan budget shape changed: %w", ErrConflict)
		}
		accounts := make(map[string]Account, len(snapshot.Policy.Accounts))
		for _, account := range snapshot.Policy.Accounts {
			accounts[account.ID] = account
		}
		limit, err := policyPopulation(rule, accounts)
		if err != nil || limit < 0 || (rule.Population != nil && rule.Population.ObservedAt.After(now)) {
			return 0, PolicyRef{}, fmt.Errorf("current loan allocation is invalid: %w", ErrConflict)
		}
		return limit, head, nil
	}
	return 0, PolicyRef{}, fmt.Errorf("loan borrower rule is no longer configured: %w", ErrConflict)
}

func loanIndexContains(ids []string, id string) bool {
	for _, value := range ids {
		if value == id {
			return true
		}
	}
	return false
}

func loanIndexWithout(ids []string, id string) []string {
	out := make([]string, 0, len(ids))
	for _, value := range ids {
		if value != id {
			out = append(out, value)
		}
	}
	return out
}

// checkBudgetLoanExpiry never mutates accounts inside admission. A recovery
// worker must call ReturnUnusedLoan before expired credit becomes spendable;
// every new admission rechecks, including after a late abort frees more credit.
func checkBudgetLoanExpiry(tx Tx, limit BudgetLimit, now time.Time) error {
	index, err := readLoanIndex(tx, limit.Key)
	if err != nil || len(index.IDs) == 0 {
		return err
	}
	state, err := loadBudget(tx, limit)
	if err != nil {
		return err
	}
	available, err := budgetAvailable(state, limit.Limit)
	if err != nil {
		return err
	}
	for _, id := range index.IDs {
		var record loanRecord
		if err := tx.Get(loanKind, id, &record); err != nil {
			return err
		}
		if err := validateLoanRecord(record); err != nil {
			return err
		}
		if record.Info.Borrower.Key != limit.Key || record.Info.Outstanding == 0 {
			return fmt.Errorf("borrower loan index mismatch: %w", ErrConflict)
		}
		if available > 0 && !now.Before(record.Info.Request.ExpiresAt) {
			return &ResolveError{Code: "loan_reconciliation_required", Message: "expired allocation requires unused-loan reconciliation before new admission"}
		}
	}
	return nil
}

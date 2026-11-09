package quotaplex

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

const attemptRecoveryKind = "attempt-recovery"

// TrustedRecoveryResolver may read the status/usage of one already submitted,
// pinned native resource. It must never replay submission or other provider
// writes. Nil evidence means terminal usage is not yet known. A host configures
// and authorizes this implementation; arbitrary API callers cannot choose it.
type TrustedRecoveryResolver interface {
	Resolve(context.Context, AttemptInfo) (*TerminalEvidence, error)
}

type TrustedRecoveryResolverFunc func(context.Context, AttemptInfo) (*TerminalEvidence, error)

func (f TrustedRecoveryResolverFunc) Resolve(ctx context.Context, attempt AttemptInfo) (*TerminalEvidence, error) {
	if f == nil {
		return nil, fmt.Errorf("trusted recovery resolver is unavailable")
	}
	return f(ctx, attempt)
}

// RecoveryPage is a bounded scan checkpoint. AfterID is the last logical record
// ID from the preceding page. Pages are different snapshots; reset the cursor
// after Done and periodically rescan for records inserted before the cursor.
type RecoveryPage struct {
	TenantID string `json:"tenant_id"`
	AfterID  string `json:"after_id,omitempty"`
	Limit    int    `json:"limit"`
}

type RecoveryFailure struct {
	ID   string `json:"id"`
	Code string `json:"code"`
}

// RecoveryReport contains bounded operational metadata, never provider payloads,
// credentials or raw transport errors. A failed item does not stop later IDs.
type RecoveryReport struct {
	Scanned     int               `json:"scanned"`
	Settled     int               `json:"settled"`
	Pending     int               `json:"pending"`
	Deferred    int               `json:"deferred"`
	Failed      int               `json:"failed"`
	Aborted     int               `json:"aborted"`
	Returned    int               `json:"returned"`
	NextAfterID string            `json:"next_after_id,omitempty"`
	Done        bool              `json:"done"`
	Failures    []RecoveryFailure `json:"failures,omitempty"`
}

type Recovery struct {
	engine   *Engine
	resolver TrustedRecoveryResolver
}

func NewRecovery(engine *Engine, resolver TrustedRecoveryResolver) (*Recovery, error) {
	if engine == nil {
		return nil, fmt.Errorf("quota engine is required")
	}
	if resolver != nil {
		value := reflect.ValueOf(resolver)
		if (value.Kind() == reflect.Pointer || value.Kind() == reflect.Func) && value.IsNil() {
			return nil, fmt.Errorf("recovery resolver is nil")
		}
	}
	return &Recovery{engine: engine, resolver: resolver}, nil
}

func (r *Recovery) scan(ctx context.Context, page RecoveryPage, kind string) ([]Record, RecoveryReport, error) {
	if err := r.engine.checkTenant(page.TenantID); err != nil {
		return nil, RecoveryReport{}, err
	}
	if page.Limit < 1 || page.Limit > MaxTransactionRecords || policyText(page.AfterID, true) != nil {
		return nil, RecoveryReport{}, fmt.Errorf("bounded recovery page is required")
	}
	var records []Record
	err := r.engine.store.View(ctx, page.TenantID, func(tx Tx) error {
		records = nil
		var err error
		records, err = tx.Scan(kind, page.AfterID, page.Limit)
		return err
	})
	if err != nil {
		return nil, RecoveryReport{}, err
	}
	return records, RecoveryReport{Done: len(records) < page.Limit, NextAfterID: page.AfterID}, nil
}

func recoveryFailure(report *RecoveryReport, id string, err error) {
	report.Failed++
	report.Pending++
	if len(report.Failures) < 16 {
		report.Failures = append(report.Failures, RecoveryFailure{ID: id, Code: settlementErrorCode(err)})
	}
}

// RunOnce drains one bounded terminal-evidence page. Settlement errors retain
// immutable evidence and exposure; per-event backoff and cursor advancement keep
// poison items from starving later work. Callers bound the overall work context.
func (r *Recovery) RunOnce(ctx context.Context, page RecoveryPage) (RecoveryReport, error) {
	records, report, err := r.scan(ctx, page, terminalPendingKind)
	if err != nil {
		return report, err
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Scanned++
		report.NextAfterID = record.ID
		ack, err := r.engine.ProcessTerminal(ctx, page.TenantID, record.ID)
		if errors.Is(err, ErrRecoveryDeferred) {
			report.Deferred++
			report.Pending++
			continue
		}
		if err != nil {
			recoveryFailure(&report, record.ID, err)
			continue
		}
		if ack.Status == UsageSettled {
			report.Settled++
		} else {
			report.Pending++
		}
	}
	return report, nil
}

type attemptRecoveryRecord struct {
	AttemptID     string    `json:"attempt_id"`
	Generation    uint64    `json:"generation"`
	Attempts      uint64    `json:"attempts"`
	LeaseOwner    string    `json:"lease_owner,omitempty"`
	LeaseUntil    time.Time `json:"lease_until,omitempty"`
	NextAttemptAt time.Time `json:"next_attempt_at,omitempty"`
	LastErrorCode string    `json:"last_error_code,omitempty"`
}

// RecoverAttempts reads already submitted jobs only through the trusted resolver.
// With no resolver it retains uncertain provider work. The only automatic abort
// is an undispatched admission whose pinned validity already makes claim
// acquisition impossible; provider uncertainty is never treated as zero usage.
func (r *Recovery) RecoverAttempts(ctx context.Context, page RecoveryPage) (RecoveryReport, error) {
	records, report, err := r.scan(ctx, page, attemptKind)
	if err != nil {
		return report, err
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Scanned++
		report.NextAfterID = record.ID
		var attempt attemptRecord
		if err := DecodeStoreValue(record.Value, &attempt); err != nil {
			recoveryFailure(&report, record.ID, err)
			continue
		}
		if attempt.Demand.AttemptID != record.ID || attempt.Decision.TenantID != page.TenantID {
			recoveryFailure(&report, record.ID, ErrConflict)
			continue
		}
		switch attempt.Decision.State {
		case "settled", "aborted", "denied":
			continue
		case "admitted":
			aborted, err := r.abortExpiredAdmission(ctx, page.TenantID, record.ID)
			if errors.Is(err, ErrInvalidTransition) || errors.Is(err, ErrDispatchClaimed) {
				report.Pending++
				continue
			}
			if err != nil {
				recoveryFailure(&report, record.ID, err)
				continue
			}
			if aborted {
				report.Aborted++
			} else {
				report.Pending++
			}
			continue
		case "dispatching", "unknown":
		default:
			recoveryFailure(&report, record.ID, ErrConflict)
			continue
		}
		// Prefer existing acknowledged evidence over another status lookup.
		var canonical terminalAttemptRecord
		err := r.engine.store.View(ctx, page.TenantID, func(tx Tx) error { return tx.Get(terminalAttemptKind, record.ID, &canonical) })
		if err == nil {
			ack, err := r.engine.ProcessTerminal(ctx, page.TenantID, canonical.EventID)
			if errors.Is(err, ErrRecoveryDeferred) {
				report.Pending++
				report.Deferred++
				continue
			}
			if err != nil {
				recoveryFailure(&report, record.ID, err)
				continue
			}
			if ack.Status == UsageSettled {
				report.Settled++
			} else {
				report.Pending++
			}
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			recoveryFailure(&report, record.ID, err)
			continue
		}
		if r.resolver == nil || attempt.ProviderResource == "" {
			report.Pending++
			continue
		}
		settled, err := r.resolveAttempt(ctx, page.TenantID, record.ID)
		if errors.Is(err, ErrRecoveryDeferred) {
			report.Pending++
			report.Deferred++
			continue
		}
		if err != nil {
			recoveryFailure(&report, record.ID, err)
			continue
		}
		if settled {
			report.Settled++
		} else {
			report.Pending++
		}
	}
	return report, nil
}

func (r *Recovery) abortExpiredAdmission(ctx context.Context, tenant, id string) (bool, error) {
	now := r.engine.clock().UTC()
	if !validAccountingTime(now) {
		return false, fmt.Errorf("valid quota clock required")
	}
	expired := false
	err := r.engine.store.View(ctx, tenant, func(tx Tx) error {
		expired = false
		var attempt attemptRecord
		if err := tx.Get(attemptKind, id, &attempt); err != nil {
			return err
		}
		if attempt.Decision.State != "admitted" || attempt.DispatchClaim.Fence != 0 {
			return ErrInvalidTransition
		}
		if attempt.Demand.AttemptID != id || attempt.Decision.TenantID != tenant {
			return ErrConflict
		}
		expired = !membershipValidAt(attempt.Demand.Principal.Membership, now) || (attempt.Demand.Actor != nil && !membershipValidAt(attempt.Demand.Actor.Membership, now))
		for _, budget := range attempt.Plan.Budgets {
			if !now.Before(budget.Window.End) {
				expired = true
			}
		}
		var pinned policyRecord
		if err := tx.Get(policyKind, quotaHash(attempt.Plan.Policy.ID, attempt.Plan.Policy.Revision), &pinned); err != nil {
			return err
		}
		if pinned.Reference != attempt.Plan.Policy {
			return ErrConflict
		}
		if !pinned.Policy.ExpiresAt.IsZero() && !now.Before(pinned.Policy.ExpiresAt) {
			expired = true
		}
		return nil
	})
	if err != nil || !expired {
		return false, err
	}
	if err := r.engine.Abort(ctx, tenant, id); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Recovery) resolveAttempt(ctx context.Context, tenant, id string) (bool, error) {
	now := r.engine.clock().UTC()
	if !validAccountingTime(now) {
		return false, fmt.Errorf("valid quota clock required")
	}
	// A random owner identifies this process invocation; generation fences retries.
	owner, err := randomRecoveryNonce()
	if err != nil {
		return false, err
	}
	var state attemptRecoveryRecord
	var info AttemptInfo
	err = r.engine.store.Update(ctx, tenant, func(tx Tx) error {
		state = attemptRecoveryRecord{}
		info = AttemptInfo{}
		var attempt attemptRecord
		if err := tx.Get(attemptKind, id, &attempt); err != nil {
			return err
		}
		if attempt.Decision.State != "dispatching" && attempt.Decision.State != "unknown" {
			return ErrInvalidTransition
		}
		if attempt.ProviderResource == "" || attempt.DispatchClaim.Fence == 0 || attempt.Demand.AttemptID != id || attempt.Decision.TenantID != tenant {
			return ErrConflict
		}
		if err := tx.Get(attemptRecoveryKind, id, &state); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if state.AttemptID != "" && state.AttemptID != id {
			return ErrConflict
		}
		if now.Before(state.NextAttemptAt) || (state.LeaseOwner != "" && now.Before(state.LeaseUntil)) {
			return ErrRecoveryDeferred
		}
		if state.Generation == ^uint64(0) || state.Attempts == ^uint64(0) {
			return ErrQuantityOverflow
		}
		state.AttemptID = id
		state.Generation++
		state.Attempts++
		state.LeaseOwner, state.LeaseUntil = owner, now.Add(terminalLease)
		if err := tx.Put(attemptRecoveryKind, id, state); err != nil {
			return err
		}
		info = AttemptInfo{Demand: attempt.Demand, Plan: attempt.Plan, Decision: attempt.Decision, AdmittedAt: attempt.AdmittedAt, Claim: attempt.DispatchClaim, DispatchAt: attempt.DispatchAt, UnknownReason: attempt.UnknownReason, Actual: attempt.Actual, ProviderResource: attempt.ProviderResource}
		return nil
	})
	if err != nil {
		return false, err
	}
	evidence, resolveErr := r.resolver.Resolve(ctx, info)
	settled := false
	if resolveErr == nil && evidence != nil {
		if evidence.Settlement.TenantID != tenant || evidence.Settlement.AttemptID != id || evidence.Settlement.Claim != info.Claim || evidence.Settlement.ProviderResourceID != info.ProviderResource || evidence.ConfigurationDigest != info.Demand.Target.ConfigurationDigest {
			resolveErr = ErrConflict
		} else if _, resolveErr = r.engine.EnqueueTerminal(ctx, *evidence); resolveErr == nil {
			var ack UsageAcknowledgement
			ack, resolveErr = r.engine.ProcessTerminal(ctx, tenant, evidence.Settlement.EventID)
			settled = resolveErr == nil && ack.Status == UsageSettled
		}
	}
	finished := r.engine.clock().UTC()
	if !validAccountingTime(finished) {
		return false, errors.Join(resolveErr, fmt.Errorf("valid quota clock required"))
	}
	writeErr := r.engine.store.Update(ctx, tenant, func(tx Tx) error {
		var current attemptRecoveryRecord
		if err := tx.Get(attemptRecoveryKind, id, &current); err != nil {
			return err
		}
		if current.LeaseOwner != owner || current.Generation != state.Generation {
			return ErrRecoveryLeaseLost
		}
		current.LeaseOwner, current.LeaseUntil = "", time.Time{}
		current.NextAttemptAt = finished.Add(terminalBackoff(current.Attempts))
		if resolveErr != nil {
			current.LastErrorCode = settlementErrorCode(resolveErr)
		} else {
			current.LastErrorCode = ""
		}
		return tx.Put(attemptRecoveryKind, id, current)
	})
	return settled, errors.Join(resolveErr, writeErr)
}

func randomRecoveryNonce() (string, error) {
	// Not an authority token: it only disambiguates local worker coordination.
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", id[:]), nil
}

// ReconcileLoans returns only currently unused loan credit. Committed exposure
// remains outstanding, including after expiry; late releases require another
// pass. No method here expires or forgives provider consumption.
func (r *Recovery) ReconcileLoans(ctx context.Context, page RecoveryPage) (RecoveryReport, error) {
	records, report, err := r.scan(ctx, page, loanKind)
	if err != nil {
		return report, err
	}
	now := r.engine.clock().UTC()
	if !validAccountingTime(now) {
		return report, fmt.Errorf("valid quota clock required")
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Scanned++
		report.NextAfterID = record.ID
		var loan loanRecord
		if err := json.Unmarshal(record.Value, &loan); err != nil {
			recoveryFailure(&report, record.ID, err)
			continue
		}
		if err := validateLoanRecord(loan); err != nil || loan.Info.Request.TenantID != page.TenantID || loan.Info.Request.ID != record.ID {
			recoveryFailure(&report, record.ID, ErrConflict)
			continue
		}
		if loan.Info.Outstanding == 0 {
			continue
		}
		if now.Before(loan.Info.Request.ExpiresAt) {
			report.Pending++
			continue
		}
		returned, err := r.engine.ReturnUnusedLoan(ctx, page.TenantID, record.ID)
		if err != nil {
			recoveryFailure(&report, record.ID, err)
			continue
		}
		if returned.Returned > loan.Info.Returned {
			report.Returned++
		}
		if returned.Outstanding > 0 {
			report.Pending++
		}
	}
	return report, nil
}

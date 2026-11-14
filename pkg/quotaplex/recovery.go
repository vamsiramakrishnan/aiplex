package quotaplex

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
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
	// Unknown summarizes the scanned attempts still in the unknown state
	// after this page. Only RecoverAttempts sets it; it is never nil there,
	// so "none" is distinguishable from "not computed".
	Unknown *UnknownExposure `json:"unknown,omitempty"`
}

// UnknownExposure summarizes attempts that were dispatched and whose outcome
// QuotaPlex never learned (state "unknown"). They keep holding budget until
// terminal evidence or a reviewed reconciliation settles them. Exposure is
// reported per meter and unit and is never folded into one cost. An amount
// that cannot be computed is counted in UncomputedAttempts and makes the
// meter's Quantity unknown (null), never 0.
type UnknownExposure struct {
	AsOf             time.Time  `json:"as_of"`
	Attempts         int        `json:"attempts"`
	OldestDispatchAt *time.Time `json:"oldest_dispatch_at,omitempty"`
	// UndatedAttempts are unknown attempts without a valid dispatch time; the
	// oldest age is then unknown.
	UndatedAttempts int `json:"undated_attempts,omitempty"`
	// Unreadable counts scanned attempt records that could not be decoded or
	// validated. Their state and exposure are unknown, so the summary is
	// incomplete while it is nonzero.
	Unreadable int            `json:"unreadable,omitempty"`
	Exposure   []HeldExposure `json:"exposure"`
}

// HeldExposure is the admitted bound (the most the attempts may have
// consumed) on one meter. Quantity is set only when every contributing amount
// is known and the sum fits; otherwise it is null and KnownPart is a lower
// bound. A MeterID or Unit of "" means the attempt's plan did not identify it.
type HeldExposure struct {
	MeterID            string `json:"meter_id"`
	Unit               string `json:"unit"`
	Currency           string `json:"currency,omitempty"`
	Scale              uint8  `json:"scale,omitempty"`
	Quantity           *int64 `json:"quantity"`
	KnownPart          int64  `json:"known_part"`
	UncomputedAttempts int    `json:"uncomputed_attempts"`
	Overflow           bool   `json:"overflow,omitempty"`
	Attempts           int    `json:"attempts"`
}

// String renders one exposure for operators. An unknown amount reads
// "unknown", with the known lower bound and the reason.
func (h HeldExposure) String() string {
	name, unit := h.MeterID, h.Unit
	if name == "" {
		name = "unknown meter"
	}
	switch unit {
	case "":
		unit = "(unknown unit)"
	case "money":
		currency := h.Currency
		if currency == "" {
			currency = "unknown currency"
		}
		unit = fmt.Sprintf("%s minor units (scale %d)", currency, h.Scale)
	}
	if h.Quantity != nil {
		return fmt.Sprintf("%s: %d %s", name, *h.Quantity, unit)
	}
	var reasons []string
	if h.UncomputedAttempts > 0 {
		reasons = append(reasons, fmt.Sprintf("%d attempt(s) not computable", h.UncomputedAttempts))
	}
	if h.Overflow {
		reasons = append(reasons, "sum overflows")
	}
	return fmt.Sprintf("%s: unknown (known part %d %s; %s)", name, h.KnownPart, unit, strings.Join(reasons, ", "))
}

func (h HeldExposure) key() [4]string {
	return [4]string{h.MeterID, h.Unit, h.Currency, fmt.Sprint(h.Scale)}
}

// Complete reports whether count, age and every exposure amount are known.
func (u *UnknownExposure) Complete() bool {
	if u == nil || u.Unreadable > 0 || u.UndatedAttempts > 0 {
		return false
	}
	for _, e := range u.Exposure {
		if e.Quantity == nil {
			return false
		}
	}
	return true
}

// OldestAge is AsOf minus the oldest dispatch time. It is unknown (false)
// when there are no unknown attempts, when one is undated, or when the as-of
// time is missing.
func (u *UnknownExposure) OldestAge() (time.Duration, bool) {
	if u == nil || u.Attempts == 0 || u.UndatedAttempts > 0 || u.OldestDispatchAt == nil || !validAccountingTime(u.AsOf) {
		return 0, false
	}
	age := u.AsOf.Sub(*u.OldestDispatchAt)
	if age < 0 {
		age = 0
	}
	return age, true
}

// Merge adds another summary (a later page, or another tenant). The later
// as-of time is kept. Sums that would overflow keep their lower bound and
// become unknown.
func (u *UnknownExposure) Merge(o UnknownExposure) {
	if o.AsOf.After(u.AsOf) {
		u.AsOf = o.AsOf
	}
	u.Attempts += o.Attempts
	u.UndatedAttempts += o.UndatedAttempts
	u.Unreadable += o.Unreadable
	if o.OldestDispatchAt != nil && (u.OldestDispatchAt == nil || o.OldestDispatchAt.Before(*u.OldestDispatchAt)) {
		at := *o.OldestDispatchAt
		u.OldestDispatchAt = &at
	}
	for _, e := range o.Exposure {
		u.add(e)
	}
	u.normalize()
}

func (u *UnknownExposure) add(e HeldExposure) {
	for i := range u.Exposure {
		current := &u.Exposure[i]
		if current.key() != e.key() {
			continue
		}
		sum, err := checkedAdd(current.KnownPart, e.KnownPart)
		if err != nil {
			current.Overflow = true
		} else {
			current.KnownPart = sum
		}
		current.Overflow = current.Overflow || e.Overflow
		current.UncomputedAttempts += e.UncomputedAttempts
		current.Attempts += e.Attempts
		return
	}
	e.Quantity = nil
	u.Exposure = append(u.Exposure, e)
}

// normalize sorts entries and derives Quantity from the known part.
func (u *UnknownExposure) normalize() {
	if u.Exposure == nil {
		u.Exposure = []HeldExposure{}
	}
	for i := range u.Exposure {
		e := &u.Exposure[i]
		e.Quantity = nil
		if e.UncomputedAttempts == 0 && !e.Overflow {
			q := e.KnownPart
			e.Quantity = &q
		}
	}
	sort.Slice(u.Exposure, func(i, j int) bool {
		a, b := u.Exposure[i].key(), u.Exposure[j].key()
		for k := range a {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return false
	})
}

// addAttempt counts one attempt in the unknown state.
func (u *UnknownExposure) addAttempt(attempt attemptRecord) {
	u.Attempts++
	if validAccountingTime(attempt.DispatchAt) {
		at := attempt.DispatchAt.UTC()
		if u.OldestDispatchAt == nil || at.Before(*u.OldestDispatchAt) {
			u.OldestDispatchAt = &at
		}
	} else {
		u.UndatedAttempts++
	}
	for _, e := range attemptExposure(attempt) {
		u.add(e)
	}
	u.normalize()
}

// attemptExposure lists one attempt's admitted bounds per meter. A bound on an
// undeclared meter, a negative bound, a plan without bounds and a cost that
// was admitted unpriced (on_unknown_cost allow_and_flag) are uncomputed.
func attemptExposure(attempt attemptRecord) []HeldExposure {
	out := map[[4]string]HeldExposure{}
	known := func(e HeldExposure, quantity int64) {
		e.Attempts, e.KnownPart = 1, quantity
		out[e.key()] = e
	}
	unknown := func(e HeldExposure) {
		e.Attempts, e.UncomputedAttempts, e.KnownPart = 1, 1, 0
		out[e.key()] = e
	}
	plan := attempt.Plan
	if len(plan.Bounds) == 0 {
		unknown(HeldExposure{})
	}
	for id, quantity := range plan.Bounds {
		meter, ok := plan.Meters[id]
		if !ok || meter.Unit == "" {
			unknown(HeldExposure{MeterID: id})
			continue
		}
		e := HeldExposure{MeterID: id, Unit: meter.Unit, Currency: meter.Currency, Scale: meter.Scale}
		if quantity < 0 {
			unknown(e)
			continue
		}
		known(e, quantity)
	}
	for _, limit := range plan.Budgets {
		if !limit.UnknownCost {
			continue
		}
		var draw *BudgetDraw
		for i := range attempt.Demand.Budgets {
			if attempt.Demand.Budgets[i].ID == limit.BudgetID {
				draw = &attempt.Demand.Budgets[i]
			}
		}
		if draw == nil {
			unknown(HeldExposure{Unit: "money"})
			continue
		}
		matched := false
		for id, meter := range plan.Meters {
			if meter.Unit != "money" || meter.Currency != draw.Currency || meter.Scale != draw.Scale {
				continue
			}
			matched = true
			e := HeldExposure{MeterID: id, Unit: "money", Currency: meter.Currency, Scale: meter.Scale}
			// Admission can flag the cost unknown while still holding a money
			// bound: PriceUsage skips billable meters bounded at 0, but
			// drawCostPriced requires a price for every billable bounded meter.
			// The bound is then only a known part, never an exact amount.
			if quantity, bounded := plan.Bounds[id]; bounded && quantity >= 0 {
				e.Attempts, e.UncomputedAttempts, e.KnownPart = 1, 1, quantity
				out[e.key()] = e
			} else {
				unknown(e)
			}
		}
		if !matched {
			unknown(HeldExposure{Unit: "money", Currency: draw.Currency, Scale: draw.Scale})
		}
	}
	entries := make([]HeldExposure, 0, len(out))
	for _, e := range out {
		entries = append(entries, e)
	}
	return entries
}

func newUnknownExposure(at time.Time) *UnknownExposure {
	return &UnknownExposure{AsOf: at, Exposure: []HeldExposure{}}
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
	now := r.engine.clock().UTC()
	if !validAccountingTime(now) {
		return report, fmt.Errorf("valid quota clock required")
	}
	report.Unknown = newUnknownExposure(now)
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Scanned++
		report.NextAfterID = record.ID
		var attempt attemptRecord
		if err := DecodeStoreValue(record.Value, &attempt); err != nil {
			report.Unknown.Unreadable++
			recoveryFailure(&report, record.ID, err)
			continue
		}
		if attempt.Demand.AttemptID != record.ID || attempt.Decision.TenantID != page.TenantID {
			report.Unknown.Unreadable++
			recoveryFailure(&report, record.ID, ErrConflict)
			continue
		}
		if !r.recoverAttempt(ctx, page.TenantID, record, attempt, &report) && attempt.Decision.State == "unknown" {
			report.Unknown.addAttempt(attempt)
		}
	}
	return report, nil
}

// recoverAttempt processes one decoded attempt and reports whether this pass
// settled it. An attempt that is not settled keeps its exposure.
func (r *Recovery) recoverAttempt(ctx context.Context, tenant string, record Record, attempt attemptRecord, report *RecoveryReport) bool {
	switch attempt.Decision.State {
	case "settled", "aborted", "denied":
		return false
	case "admitted":
		aborted, err := r.abortExpiredAdmission(ctx, tenant, record.ID)
		if errors.Is(err, ErrInvalidTransition) || errors.Is(err, ErrDispatchClaimed) {
			report.Pending++
			return false
		}
		if err != nil {
			recoveryFailure(report, record.ID, err)
			return false
		}
		if aborted {
			report.Aborted++
		} else {
			report.Pending++
		}
		return false
	case "dispatching", "unknown":
	default:
		report.Unknown.Unreadable++
		recoveryFailure(report, record.ID, ErrConflict)
		return false
	}
	settledBy := func(ack UsageAcknowledgement, err error) bool {
		if errors.Is(err, ErrRecoveryDeferred) {
			report.Pending++
			report.Deferred++
			return false
		}
		if err != nil {
			recoveryFailure(report, record.ID, err)
			return false
		}
		if ack.Status == UsageSettled {
			report.Settled++
			return true
		}
		report.Pending++
		return false
	}
	// Prefer existing acknowledged evidence over another status lookup.
	var canonical terminalAttemptRecord
	err := r.engine.store.View(ctx, tenant, func(tx Tx) error { return tx.Get(terminalAttemptKind, record.ID, &canonical) })
	if err == nil {
		return settledBy(r.engine.ProcessTerminal(ctx, tenant, canonical.EventID))
	}
	if !errors.Is(err, ErrNotFound) {
		recoveryFailure(report, record.ID, err)
		return false
	}
	if r.resolver == nil || attempt.ProviderResource == "" {
		report.Pending++
		return false
	}
	settled, err := r.resolveAttempt(ctx, tenant, record.ID)
	ack := UsageAcknowledgement{}
	if settled {
		ack.Status = UsageSettled
	}
	return settledBy(ack, err)
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

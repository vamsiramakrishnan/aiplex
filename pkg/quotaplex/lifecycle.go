package quotaplex

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ClaimDispatch is a single successful transition, not an idempotent permission
// to invoke upstream repeatedly. If its response is lost, inspect the stored
// attempt and reconcile possible work; a second claim never grants dispatch.
// The host must reauthorize execution immediately before calling this method.
func (e *Engine) ClaimDispatch(ctx context.Context, tenant, attempt string) (DispatchClaim, error) {
	if err := e.checkTenant(tenant); err != nil {
		return DispatchClaim{}, err
	}
	now := e.clock().UTC()
	if !validAccountingTime(now) {
		return DispatchClaim{}, fmt.Errorf("valid quota clock required")
	}
	var claim DispatchClaim
	err := e.store.Update(ctx, tenant, func(tx Tx) error {
		claim = DispatchClaim{}
		var record attemptRecord
		if err := tx.Get(attemptKind, attempt, &record); err != nil {
			return err
		}
		if !record.Decision.Allowed || record.Decision.State != "admitted" {
			return fmt.Errorf("attempt is not undispatched admission: %w", ErrDispatchClaimed)
		}
		if record.Decision.TenantID != tenant || record.Demand.AttemptID != attempt {
			return ErrConflict
		}
		if !membershipValidAt(record.Demand.Principal.Membership, now) || (record.Demand.Actor != nil && !membershipValidAt(record.Demand.Actor.Membership, now)) {
			return fmt.Errorf("dispatch membership expired: %w", ErrDenied)
		}
		for _, budget := range record.Plan.Budgets {
			if now.Before(budget.Window.Start) || !now.Before(budget.Window.End) {
				return fmt.Errorf("undispatched reservation window expired: %w", ErrDenied)
			}
		}
		var pinned policyRecord
		if err := tx.Get(policyKind, quotaHash(record.Plan.Policy.ID, record.Plan.Policy.Revision), &pinned); err != nil {
			return err
		}
		if pinned.Reference != record.Plan.Policy || now.Before(pinned.Policy.EffectiveAt) || (!pinned.Policy.ExpiresAt.IsZero() && !now.Before(pinned.Policy.ExpiresAt)) {
			return fmt.Errorf("dispatch policy is unavailable or expired: %w", ErrDenied)
		}
		record.DispatchClaim = DispatchClaim{TenantID: tenant, AttemptID: attempt, Fence: 1}
		record.DispatchAt = now
		record.Decision.State = "dispatching"
		if err := tx.Put(attemptKind, attempt, record); err != nil {
			return err
		}
		if err := tx.Create(ledgerKind, quotaHash(attempt, "dispatch"), ledgerEntry{AttemptID: attempt, Action: "dispatch", At: now, Policy: record.Plan.Policy}); err != nil {
			return err
		}
		claim = record.DispatchClaim
		return nil
	})
	if err != nil {
		return DispatchClaim{}, err
	}
	return claim, nil
}

func membershipValidAt(membership Membership, now time.Time) bool {
	return !now.IsZero() && !membership.IssuedAt.IsZero() && !membership.IssuedAt.After(now) && membership.ExpiresAt.After(now)
}

// RecordProviderResource durably correlates a submitted job/session before its
// terminal usage is known. The same claim may pin one immutable native resource;
// neither pinning nor replaying it releases exposure or permits another dispatch.
func (e *Engine) RecordProviderResource(ctx context.Context, claim DispatchClaim, resourceID string) error {
	if err := e.checkTenant(claim.TenantID); err != nil {
		return err
	}
	if policyText(resourceID, false) != nil || claim.Fence == 0 {
		return fmt.Errorf("bounded provider resource and dispatch claim required")
	}
	now := e.clock().UTC()
	if !validAccountingTime(now) {
		return fmt.Errorf("valid quota clock required")
	}
	return e.store.Update(ctx, claim.TenantID, func(tx Tx) error {
		var record attemptRecord
		if err := tx.Get(attemptKind, claim.AttemptID, &record); err != nil {
			return err
		}
		if record.DispatchClaim != claim {
			return ErrConflict
		}
		if record.ProviderResource != "" {
			if record.ProviderResource != resourceID {
				return ErrConflict
			}
			return nil
		}
		if record.Decision.State != "dispatching" && record.Decision.State != "unknown" {
			return ErrInvalidTransition
		}
		record.ProviderResource = resourceID
		if err := tx.Put(attemptKind, claim.AttemptID, record); err != nil {
			return err
		}
		return tx.Create(ledgerKind, quotaHash(claim.AttemptID, "provider-resource"), ledgerEntry{AttemptID: claim.AttemptID, Action: "provider-resource", At: now, Policy: record.Plan.Policy, ProviderResourceID: resourceID})
	})
}

// MarkUnknown retains every spending hold and concurrency permit. A timeout,
// disconnect or expired local lease cannot prove provider work has stopped.
func (e *Engine) MarkUnknown(ctx context.Context, claim DispatchClaim, reason string) error {
	if err := e.checkTenant(claim.TenantID); err != nil {
		return err
	}
	if reason == "" || len(reason) > 1024 {
		return fmt.Errorf("bounded unknown-work reason required")
	}
	now := e.clock().UTC()
	if !validAccountingTime(now) {
		return fmt.Errorf("valid quota clock required")
	}
	return e.store.Update(ctx, claim.TenantID, func(tx Tx) error {
		var record attemptRecord
		if err := tx.Get(attemptKind, claim.AttemptID, &record); err != nil {
			return err
		}
		if record.DispatchClaim != claim || claim.Fence == 0 {
			return ErrConflict
		}
		if record.Decision.State == "unknown" {
			return nil
		}
		if record.Decision.State != "dispatching" {
			return ErrInvalidTransition
		}
		record.Decision.State = "unknown"
		record.UnknownReason = reason
		if err := tx.Put(attemptKind, claim.AttemptID, record); err != nil {
			return err
		}
		return tx.Create(ledgerKind, quotaHash(claim.AttemptID, "unknown"), ledgerEntry{AttemptID: claim.AttemptID, Action: "unknown", At: now, Policy: record.Plan.Policy})
	})
}

// Abort releases only a known-undispatched attempt. Rate capacity was consumed
// by its accepted admission and is intentionally not refunded.
func (e *Engine) Abort(ctx context.Context, tenant, attempt string) error {
	if err := e.checkTenant(tenant); err != nil {
		return err
	}
	now := e.clock().UTC()
	if !validAccountingTime(now) {
		return fmt.Errorf("valid quota clock required")
	}
	return e.store.Update(ctx, tenant, func(tx Tx) error {
		var record attemptRecord
		if err := tx.Get(attemptKind, attempt, &record); err != nil {
			return err
		}
		if record.Decision.State == "aborted" {
			return nil
		}
		if record.Decision.State != "admitted" || !record.Decision.Allowed {
			return ErrInvalidTransition
		}
		changes, err := releaseAttempt(tx, record, nil)
		if err != nil {
			return err
		}
		record.Decision.State, record.Decision.Reason = "aborted", "aborted"
		if err := tx.Put(attemptKind, attempt, record); err != nil {
			return err
		}
		return tx.Create(ledgerKind, quotaHash(attempt, "abort"), ledgerEntry{AttemptID: attempt, Action: "release", At: now, Policy: record.Plan.Policy, Budgets: changes})
	})
}

type usageRecord struct {
	AttemptID string `json:"attempt_id"`
	Digest    string `json:"digest"`
}

// Settle acknowledges one terminal actual-usage result atomically with ledger,
// balance and permit updates. It prices against the original immutable plan.
// Actual overruns are retained as consumption/debt and block future admissions.
func (e *Engine) Settle(ctx context.Context, settlement Settlement) (Decision, error) {
	if err := e.checkTenant(settlement.TenantID); err != nil {
		return Decision{}, err
	}
	if settlement.TenantID == "" || settlement.AttemptID == "" || settlement.EventID == "" || len(settlement.EventID) > 1024 || settlement.Claim.TenantID != settlement.TenantID || settlement.Claim.AttemptID != settlement.AttemptID || settlement.Claim.Fence == 0 {
		return Decision{}, fmt.Errorf("terminal usage requires tenant, attempt, event and matching dispatch claim")
	}
	if policyText(settlement.ProviderResourceID, true) != nil {
		return Decision{}, fmt.Errorf("bounded provider resource required")
	}
	if settlement.Usage == nil {
		return Decision{}, fmt.Errorf("terminal usage must distinguish explicit zero from unknown")
	}
	if err := ValidateVector(settlement.Usage); err != nil {
		return Decision{}, err
	}
	now := e.clock().UTC()
	if !validAccountingTime(now) {
		return Decision{}, fmt.Errorf("valid quota clock required")
	}
	eventDigest, settlementDigest := settlementDigests(settlement)
	var result Decision
	err := e.store.Update(ctx, settlement.TenantID, func(tx Tx) error {
		result = Decision{}
		var record attemptRecord
		if err := tx.Get(attemptKind, settlement.AttemptID, &record); err != nil {
			return err
		}
		if record.DispatchClaim != settlement.Claim {
			return ErrConflict
		}
		if err := validateSettlementModel(record.Demand, settlement); err != nil {
			return err
		}
		if record.ProviderResource != "" && record.ProviderResource != settlement.ProviderResourceID {
			return fmt.Errorf("terminal usage does not identify the pinned provider resource: %w", ErrConflict)
		}
		var event usageRecord
		eventErr := tx.Get(usageKind, settlement.EventID, &event)
		if eventErr == nil {
			if event.AttemptID != settlement.AttemptID || event.Digest != eventDigest || record.Decision.State != "settled" || record.SettlementDigest != settlementDigest {
				return ErrConflict
			}
			result = record.Decision
			return nil
		} else if !errors.Is(eventErr, ErrNotFound) {
			return eventErr
		}
		if record.Decision.State == "settled" {
			if record.SettlementDigest != settlementDigest {
				return ErrConflict
			}
			if err := tx.Create(usageKind, settlement.EventID, usageRecord{AttemptID: settlement.AttemptID, Digest: eventDigest}); err != nil {
				return err
			}
			result = record.Decision
			return nil
		}
		if record.Decision.State != "dispatching" && record.Decision.State != "unknown" {
			return ErrInvalidTransition
		}
		for meter := range record.Demand.Bounds {
			if _, exists := settlement.Usage[meter]; !exists {
				return fmt.Errorf("terminal usage is incomplete for meter %s", meter)
			}
		}
		for meter := range settlement.Usage {
			definition, exists := record.Plan.Meters[meter]
			if !exists || definition.Unit == "money" {
				return fmt.Errorf("terminal usage contains an unknown or computed meter %s", meter)
			}
		}
		actual := CloneVector(settlement.Usage)
		if len(record.Plan.Prices) > 0 || planHasMoneyBudget(record.Plan) {
			money, err := PriceUsage(actual, record.Plan.Prices, record.Plan.Meters)
			if err != nil {
				return err
			}
			for _, budget := range record.Plan.Budgets {
				if record.Plan.Meters[budget.MeterID].Unit == "money" {
					if _, exists := money[budget.MeterID]; !exists {
						return ErrUnknownPrice
					}
				}
			}
			actual, err = AddVectors(actual, money)
			if err != nil {
				return err
			}
		}
		changes, err := releaseAttempt(tx, record, actual)
		if err != nil {
			return err
		}
		overrun := false
		for meter, quantity := range actual {
			if quantity > record.Plan.Bounds[meter] {
				overrun = true
			}
		}
		record.Decision.State, record.Decision.Reason, record.Decision.Overrun = "settled", "settled", overrun
		if overrun {
			record.Decision.Reason = "settled_overrun"
		}
		record.Actual, record.SettlementDigest, record.ProviderResource = actual, settlementDigest, settlement.ProviderResourceID
		if err := tx.Put(attemptKind, settlement.AttemptID, record); err != nil {
			return err
		}
		if err := tx.Create(usageKind, settlement.EventID, usageRecord{AttemptID: settlement.AttemptID, Digest: eventDigest}); err != nil {
			return err
		}
		if err := tx.Create(ledgerKind, quotaHash(settlement.AttemptID, "settle"), ledgerEntry{AttemptID: settlement.AttemptID, Action: "consume", At: now, Policy: record.Plan.Policy, Budgets: changes, Usage: actual, EventID: settlement.EventID, ProviderResourceID: settlement.ProviderResourceID}); err != nil {
			return err
		}
		result = record.Decision
		return nil
	})
	if err != nil {
		return Decision{}, err
	}
	return result, nil
}

func planHasMoneyBudget(plan Plan) bool {
	for _, budget := range plan.Budgets {
		if plan.Meters[budget.MeterID].Unit == "money" {
			return true
		}
	}
	return false
}

func releaseAttempt(tx Tx, record attemptRecord, actual Vector) ([]budgetMutation, error) {
	changes := make([]budgetMutation, 0, len(record.Plan.Budgets))
	for _, limit := range record.Plan.Budgets {
		state, err := loadBudget(tx, limit)
		if err != nil {
			return nil, err
		}
		if state.Held < limit.Demand {
			return nil, fmt.Errorf("reservation hold underflow: %w", ErrConflict)
		}
		state.Held -= limit.Demand
		consumed := actual[limit.MeterID]
		state.Consumed, err = checkedAdd(state.Consumed, consumed)
		if err != nil {
			return nil, err
		}
		if err := tx.Put(budgetKind, limit.Key, state); err != nil {
			return nil, err
		}
		changes = append(changes, budgetMutation{Key: limit.Key, MeterID: limit.MeterID, Held: -limit.Demand, Consumed: consumed})
	}
	for _, limit := range record.Plan.Concurrency {
		var state concurrencyRecord
		if err := tx.Get(concurrencyKind, limit.Key, &state); err != nil {
			return nil, err
		}
		if state.Shape != limit.Shape || state.Active < limit.Demand {
			return nil, fmt.Errorf("concurrency permit underflow: %w", ErrConflict)
		}
		state.Active -= limit.Demand
		if err := tx.Put(concurrencyKind, limit.Key, state); err != nil {
			return nil, err
		}
	}
	return changes, nil
}

package quotaplex

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	reconciliationPlanKind    = "reconciliation-plan"
	reconciliationReceiptKind = "reconciliation-receipt"
)

// ReconciliationRequest is an explicit operator assertion of terminal facts.
// The host must authorize planning and approval separately. EvidenceRef points to
// reviewed provider evidence; neither its syntax nor an approval proves a native
// lookup occurred. Values are raw usage: the original immutable plan sets prices.
type ReconciliationRequest struct {
	TenantID           string    `json:"tenant_id"`
	ID                 string    `json:"id"`
	AttemptID          string    `json:"attempt_id"`
	Reason             string    `json:"reason"`
	EvidenceRef        string    `json:"evidence_ref"`
	Usage              Vector    `json:"usage"`
	ModelVersion       string    `json:"model_version,omitempty"`
	ProviderResourceID string    `json:"provider_resource_id,omitempty"`
	ExpiresAt          time.Time `json:"expires_at"`
}
type ReconciliationPlan struct {
	Version                string        `json:"version"`
	ID                     string        `json:"id"`
	TenantID               string        `json:"tenant_id"`
	AttemptID              string        `json:"attempt_id"`
	Reason                 string        `json:"reason"`
	EvidenceRef            string        `json:"evidence_ref"`
	RequestedBy            Administrator `json:"requested_by"`
	IndependentApproval    bool          `json:"independent_approval"`
	ExpectedAttemptDigest  string        `json:"expected_attempt_digest"`
	OriginalEvidenceDigest string        `json:"original_evidence_digest,omitempty"`
	Settlement             Settlement    `json:"settlement"`
	CreatedAt              time.Time     `json:"created_at"`
	ExpiresAt              time.Time     `json:"expires_at"`
	Digest                 string        `json:"digest"`
}
type ReconciliationReceipt struct {
	Version    string        `json:"version"`
	PlanID     string        `json:"plan_id"`
	PlanDigest string        `json:"plan_digest"`
	TenantID   string        `json:"tenant_id"`
	AttemptID  string        `json:"attempt_id"`
	ApprovedBy Administrator `json:"approved_by"`
	AppliedAt  time.Time     `json:"applied_at"`
	Decision   Decision      `json:"decision"`
}
type ReconciliationInspection struct {
	Plan    ReconciliationPlan     `json:"plan"`
	Receipt *ReconciliationReceipt `json:"receipt,omitempty"`
}

func reconciliationDigest(p ReconciliationPlan) string { p.Digest = ""; return quotaHash(p) }
func reconciliationState(tx Tx, attempt string) (attemptRecord, string, error) {
	var record attemptRecord
	if err := tx.Get(attemptKind, attempt, &record); err != nil {
		return record, "", err
	}
	var evidence terminalAttemptRecord
	if err := tx.Get(terminalAttemptKind, attempt, &evidence); err != nil && !errors.Is(err, ErrNotFound) {
		return record, "", err
	}
	return record, evidence.Digest, nil
}
func validateReconciliationSettlement(record attemptRecord, s Settlement) error {
	if record.Decision.State != "unknown" && record.Decision.State != "dispatching" {
		return ErrInvalidTransition
	}
	if !record.Decision.Allowed || record.DispatchClaim.Fence == 0 || s.Claim != record.DispatchClaim {
		return ErrConflict
	}
	if err := validateSettlementModel(record.Demand, s); err != nil {
		return err
	}
	if record.ProviderResource != "" && record.ProviderResource != s.ProviderResourceID {
		return ErrConflict
	}
	evidence := TerminalEvidence{Version: Version, ProducerID: "authorized-reconciliation", ConfigurationDigest: record.Demand.Target.ConfigurationDigest, ObservedModelVersion: s.ModelVersion, RequireModelVersion: record.Demand.RequireModelVersion, Settlement: s}
	return validateEvidenceAttempt(evidence, record)
}

// PrepareReconciliation persists an immutable reviewed proposal. Self approval
// defaults to forbidden; only operator configuration may allow it. This method
// never changes holds, terminal producer evidence or the active binding.
func (e *Engine) PrepareReconciliation(ctx context.Context, r ReconciliationRequest, author Administrator, allowSelfApproval bool) (ReconciliationPlan, error) {
	if err := e.checkTenant(r.TenantID); err != nil {
		return ReconciliationPlan{}, err
	}
	if err := author.validate(); err != nil {
		return ReconciliationPlan{}, err
	}
	for _, v := range []string{r.ID, r.AttemptID, r.Reason, r.EvidenceRef} {
		if policyText(v, false) != nil {
			return ReconciliationPlan{}, fmt.Errorf("bounded reconciliation identity, reason and evidence reference required")
		}
	}
	now := e.clock().UTC()
	if !validAccountingTime(now) || !r.ExpiresAt.After(now) || r.ExpiresAt.After(now.Add(time.Hour)) || r.Usage == nil {
		return ReconciliationPlan{}, fmt.Errorf("complete usage and expiry within one hour required")
	}
	if err := ValidateVector(r.Usage); err != nil {
		return ReconciliationPlan{}, err
	}
	r.Usage = CloneVector(r.Usage)
	r.ExpiresAt = r.ExpiresAt.UTC()
	var result ReconciliationPlan
	err := e.store.Update(ctx, r.TenantID, func(tx Tx) error {
		result = ReconciliationPlan{}
		var previous ReconciliationPlan
		if err := tx.Get(reconciliationPlanKind, r.ID, &previous); err == nil {
			if previous.RequestedBy != author || previous.Reason != r.Reason || previous.EvidenceRef != r.EvidenceRef || previous.AttemptID != r.AttemptID || previous.ExpiresAt != r.ExpiresAt || previous.IndependentApproval == allowSelfApproval || quotaHash(previous.Settlement.Usage) != quotaHash(r.Usage) || previous.Settlement.ModelVersion != r.ModelVersion || previous.Settlement.ProviderResourceID != r.ProviderResourceID {
				return ErrConflict
			}
			if previous.Digest != reconciliationDigest(previous) {
				return ErrConflict
			}
			result = previous
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		record, originalDigest, err := reconciliationState(tx, r.AttemptID)
		if err != nil {
			return err
		}
		settlement := Settlement{TenantID: r.TenantID, AttemptID: r.AttemptID, EventID: quotaHash("reconciliation", r.ID), Claim: record.DispatchClaim, Usage: r.Usage, ModelVersion: r.ModelVersion, ProviderResourceID: r.ProviderResourceID}
		if err = validateReconciliationSettlement(record, settlement); err != nil {
			return err
		}
		plan := ReconciliationPlan{Version: Version, ID: r.ID, TenantID: r.TenantID, AttemptID: r.AttemptID, Reason: r.Reason, EvidenceRef: r.EvidenceRef, RequestedBy: author, IndependentApproval: !allowSelfApproval, ExpectedAttemptDigest: quotaHash(record), OriginalEvidenceDigest: originalDigest, Settlement: settlement, CreatedAt: now, ExpiresAt: r.ExpiresAt}
		plan.Digest = reconciliationDigest(plan)
		if err = tx.Create(reconciliationPlanKind, plan.ID, plan); err != nil {
			return err
		}
		raw, err := EncodeStoreValue(plan)
		if err != nil {
			return err
		}
		auditID := quotaHash("reconciliation-plan", plan.ID)
		if err = tx.Create(administrationKind, auditID, AdministrationRecord{ID: auditID, Action: "reconciliation-plan", ResourceID: r.AttemptID, Administrator: author, At: now, InputDigest: plan.Digest, Result: raw}); err != nil {
			return err
		}
		result = plan
		return nil
	})
	return result, err
}
func (e *Engine) GetReconciliation(ctx context.Context, tenant, id string) (ReconciliationInspection, error) {
	if err := e.checkTenant(tenant); err != nil {
		return ReconciliationInspection{}, err
	}
	var out ReconciliationInspection
	err := e.store.View(ctx, tenant, func(tx Tx) error {
		if err := tx.Get(reconciliationPlanKind, id, &out.Plan); err != nil {
			return err
		}
		if out.Plan.Digest != reconciliationDigest(out.Plan) || out.Plan.TenantID != tenant || out.Plan.ID != id {
			return ErrConflict
		}
		var receipt ReconciliationReceipt
		if err := tx.Get(reconciliationReceiptKind, id, &receipt); err == nil {
			out.Receipt = &receipt
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		return nil
	})
	return out, err
}

// ApplyReconciliation reuses Engine.Settle under a guarded transaction. The plan
// comparison, original-price settlement, immutable approval receipt, audit and
// outbox resolution commit atomically. It never reprices a different model,
// redispatches provider work or reopens a quarantined binding generation.
func (e *Engine) ApplyReconciliation(ctx context.Context, tenant, id, digest string, approver Administrator) (ReconciliationReceipt, error) {
	if err := e.checkTenant(tenant); err != nil {
		return ReconciliationReceipt{}, err
	}
	if err := approver.validate(); err != nil {
		return ReconciliationReceipt{}, err
	}
	view, err := e.GetReconciliation(ctx, tenant, id)
	if err != nil {
		return ReconciliationReceipt{}, err
	}
	plan := view.Plan
	if digest == "" || digest != plan.Digest {
		return ReconciliationReceipt{}, ErrConflict
	}
	now := e.clock().UTC()
	if !validAccountingTime(now) {
		return ReconciliationReceipt{}, fmt.Errorf("valid reconciliation clock required")
	}
	var receipt ReconciliationReceipt
	copy := *e
	copy.clock = func() time.Time { return now }
	copy.store = administrationStore{base: e.store, before: func(tx Tx) error {
		var stored ReconciliationPlan
		if err := tx.Get(reconciliationPlanKind, id, &stored); err != nil {
			return err
		}
		if stored.Digest != digest || stored.Digest != reconciliationDigest(stored) {
			return ErrConflict
		}
		if err := tx.Get(reconciliationReceiptKind, id, &receipt); err == nil {
			if receipt.PlanDigest != digest || receipt.ApprovedBy != approver {
				return ErrConflict
			}
			return errAdministrationReplay
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if !now.Before(stored.ExpiresAt) || now.Before(stored.CreatedAt) {
			return ErrDenied
		}
		if stored.IndependentApproval && stored.RequestedBy.PrincipalID == approver.PrincipalID {
			return ErrDenied
		}
		current, originalDigest, err := reconciliationState(tx, stored.AttemptID)
		if err != nil {
			return err
		}
		if quotaHash(current) != stored.ExpectedAttemptDigest || originalDigest != stored.OriginalEvidenceDigest {
			return ErrConflict
		}
		return validateReconciliationSettlement(current, stored.Settlement)
	}, after: func(tx Tx) error {
		var settled attemptRecord
		if err := tx.Get(attemptKind, plan.AttemptID, &settled); err != nil {
			return err
		}
		receipt = ReconciliationReceipt{Version: Version, PlanID: id, PlanDigest: digest, TenantID: tenant, AttemptID: plan.AttemptID, ApprovedBy: approver, AppliedAt: now, Decision: settled.Decision}
		if err := tx.Create(reconciliationReceiptKind, id, receipt); err != nil {
			return err
		}
		raw, err := EncodeStoreValue(receipt)
		if err != nil {
			return err
		}
		auditID := quotaHash("reconciliation-apply", id)
		if err = tx.Create(administrationKind, auditID, AdministrationRecord{ID: auditID, Action: "reconciliation-apply", ResourceID: plan.AttemptID, Administrator: approver, At: now, InputDigest: digest, Result: raw}); err != nil {
			return err
		}
		var canonical terminalAttemptRecord
		if err := tx.Get(terminalAttemptKind, plan.AttemptID, &canonical); errors.Is(err, ErrNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		var original terminalEvidenceRecord
		var work terminalWorkRecord
		if err = tx.Get(terminalEvidenceKind, canonical.EventID, &original); err != nil {
			return err
		}
		if err = tx.Get(terminalWorkKind, canonical.EventID, &work); err != nil {
			return err
		}
		if err = validateTerminalRecords(canonical.EventID, original, work); err != nil {
			return err
		}
		if original.Digest != plan.OriginalEvidenceDigest {
			return ErrConflict
		}
		work.Ack.Status = UsageSettled
		work.Ack.ResolutionID = id
		work.Ack.SettledAt = now
		work.Ack.NextAttemptAt = time.Time{}
		work.Ack.LastErrorCode = ""
		work.Ack.Decision = &settled.Decision
		work.LeaseOwner = ""
		work.LeaseUntil = time.Time{}
		if err = tx.Put(terminalWorkKind, canonical.EventID, work); err != nil {
			return err
		}
		return tx.Delete(terminalPendingKind, canonical.EventID)
	}}
	_, err = copy.Settle(ctx, plan.Settlement)
	if errors.Is(err, errAdministrationReplay) {
		return receipt, nil
	}
	if err != nil {
		return ReconciliationReceipt{}, err
	}
	return receipt, nil
}

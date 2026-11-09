package quotaplex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	terminalEvidenceKind = "terminal-evidence"
	terminalWorkKind     = "terminal-work"
	terminalAttemptKind  = "terminal-attempt"
	terminalPendingKind  = "terminal-pending"
	UsagePending         = "pending"
	UsageProcessing      = "processing"
	UsageSettled         = "settled"
	terminalLease        = 30 * time.Second
)

var ErrRecoveryDeferred = errors.New("quota recovery is deferred")
var ErrRecoveryLeaseLost = errors.New("quota recovery lease changed")
var ErrModelVersionMismatch = errors.New("observed model version does not match pinned quota target")
var ErrModelVersionMissing = errors.New("required observed model version is unavailable")

// TerminalEvidence is supplied only by an authenticated, authorized usage
// producer. Validation binds facts to an existing attempt; it does not establish
// that an arbitrary caller is a trusted producer or that a response is terminal.
// ObservedModelVersion preserves the provider's actual version separately from
// the pinned target; a mismatch is acknowledged but cannot settle at wrong prices.
type TerminalEvidence struct {
	Version              string     `json:"version"`
	ProducerID           string     `json:"producer_id"`
	ConfigurationDigest  string     `json:"configuration_digest"`
	ObservedModelVersion string     `json:"observed_model_version,omitempty"`
	RequireModelVersion  bool       `json:"require_model_version,omitempty"`
	Settlement           Settlement `json:"settlement"`
}

// UsageAcknowledgement acknowledges immutable terminal evidence after commit.
// Pending/processing means settlement acknowledgement is incomplete: the ledger
// may already have committed before the worker recorded its acknowledgement.
// Only ledger settlement releases holds; settled confirms it also completed.
type UsageAcknowledgement struct {
	TenantID       string    `json:"tenant_id"`
	AttemptID      string    `json:"attempt_id"`
	EventID        string    `json:"event_id"`
	EvidenceDigest string    `json:"evidence_digest"`
	Status         string    `json:"status"`
	AcceptedAt     time.Time `json:"accepted_at"`
	SettledAt      time.Time `json:"settled_at,omitempty"`
	NextAttemptAt  time.Time `json:"next_attempt_at,omitempty"`
	Attempts       uint64    `json:"attempts"`
	LastErrorCode  string    `json:"last_error_code,omitempty"`
	Decision       *Decision `json:"decision,omitempty"`
}

type terminalEvidenceRecord struct {
	Evidence TerminalEvidence `json:"evidence"`
	Digest   string           `json:"digest"`
}

type terminalWorkRecord struct {
	Ack        UsageAcknowledgement `json:"ack"`
	LeaseOwner string               `json:"lease_owner,omitempty"`
	LeaseUntil time.Time            `json:"lease_until,omitempty"`
	Generation uint64               `json:"generation"`
}

type terminalAttemptRecord struct {
	EventID string `json:"event_id"`
	Digest  string `json:"digest"`
}

func evidenceShape(evidence TerminalEvidence) error {
	s := evidence.Settlement
	if evidence.Version != Version || policyText(evidence.ProducerID, false) != nil || policyText(evidence.ObservedModelVersion, true) != nil || policyText(s.ModelVersion, true) != nil || (evidence.ObservedModelVersion != "" && s.ModelVersion != "" && evidence.ObservedModelVersion != s.ModelVersion) || policyText(s.TenantID, false) != nil || policyText(s.AttemptID, false) != nil || policyText(s.EventID, false) != nil || policyText(s.ProviderResourceID, true) != nil || s.Claim.TenantID != s.TenantID || s.Claim.AttemptID != s.AttemptID || s.Claim.Fence == 0 || s.Usage == nil {
		return fmt.Errorf("terminal evidence requires version, producer, attempt, event and matching dispatch claim")
	}
	if err := ValidateVector(s.Usage); err != nil {
		return err
	}
	if len(s.Usage) > 64 {
		return fmt.Errorf("terminal evidence has too many meters")
	}
	return nil
}

func normalizeTerminalModelProof(evidence TerminalEvidence) (TerminalEvidence, error) {
	observed, proof := evidence.ObservedModelVersion, evidence.Settlement.ModelVersion
	if observed != "" && proof != "" && observed != proof {
		return TerminalEvidence{}, fmt.Errorf("terminal model proofs disagree: %w", ErrConflict)
	}
	if observed == "" {
		evidence.ObservedModelVersion = proof
	}
	if proof == "" {
		evidence.Settlement.ModelVersion = observed
	}
	return evidence, nil
}

func validateEvidenceAttempt(evidence TerminalEvidence, attempt attemptRecord) error {
	s := evidence.Settlement
	if attempt.Decision.TenantID != s.TenantID || attempt.Demand.AttemptID != s.AttemptID || attempt.DispatchClaim != s.Claim || evidence.ConfigurationDigest != attempt.Demand.Target.ConfigurationDigest {
		return ErrConflict
	}
	if attempt.ProviderResource != "" && attempt.ProviderResource != s.ProviderResourceID {
		return ErrConflict
	}
	if attempt.Decision.State != "dispatching" && attempt.Decision.State != "unknown" && attempt.Decision.State != "settled" {
		return ErrInvalidTransition
	}
	if (evidence.RequireModelVersion || attempt.Demand.RequireModelVersion) && attempt.Demand.Target.ModelVersion == "" {
		return fmt.Errorf("required model version has no pinned target: %w", ErrConflict)
	}
	for meter := range attempt.Demand.Bounds {
		if _, present := s.Usage[meter]; !present {
			return fmt.Errorf("terminal evidence omits bounded meter %s", meter)
		}
	}
	for meter := range s.Usage {
		definition, known := attempt.Plan.Meters[meter]
		if !known || definition.Unit == "money" {
			return fmt.Errorf("terminal evidence contains an unknown or computed meter")
		}
	}
	return nil
}

func terminalModelError(evidence TerminalEvidence, attempt attemptRecord) error {
	expected, observed := attempt.Demand.Target.ModelVersion, evidence.ObservedModelVersion
	if (evidence.RequireModelVersion || attempt.Demand.RequireModelVersion) && observed == "" {
		return ErrModelVersionMissing
	}
	if expected != "" && observed != "" && expected != observed {
		return ErrModelVersionMismatch
	}
	return nil
}

func validateTerminalRecords(eventID string, evidence terminalEvidenceRecord, work terminalWorkRecord) error {
	if evidenceShape(evidence.Evidence) != nil || evidence.Digest != quotaHash(evidence.Evidence) || eventID != evidence.Evidence.Settlement.EventID || work.Ack.EventID != eventID || work.Ack.EvidenceDigest != evidence.Digest || work.Ack.TenantID != evidence.Evidence.Settlement.TenantID || work.Ack.AttemptID != evidence.Evidence.Settlement.AttemptID || !validAccountingTime(work.Ack.AcceptedAt) {
		return fmt.Errorf("terminal evidence/work identity mismatch: %w", ErrConflict)
	}
	switch work.Ack.Status {
	case UsagePending:
		if work.LeaseOwner != "" || !work.LeaseUntil.IsZero() || work.Ack.Decision != nil || !work.Ack.SettledAt.IsZero() {
			return ErrConflict
		}
	case UsageProcessing:
		if work.LeaseOwner == "" || !validAccountingTime(work.LeaseUntil) || work.Generation == 0 || work.Ack.Decision != nil || !work.Ack.SettledAt.IsZero() {
			return ErrConflict
		}
	case UsageSettled:
		if work.LeaseOwner != "" || !work.LeaseUntil.IsZero() || !validAccountingTime(work.Ack.SettledAt) || work.Ack.Decision == nil || work.Ack.Decision.State != "settled" || work.Ack.Decision.TenantID != work.Ack.TenantID || work.Ack.Decision.AttemptID != work.Ack.AttemptID {
			return ErrConflict
		}
	default:
		return ErrConflict
	}
	return nil
}

// EnqueueTerminal durably acknowledges known terminal usage without requiring
// settlement availability. The host must call with a bounded durability context
// that can outlive the client context. Unknown pricing or later settlement errors
// retain both the immutable evidence and outstanding exposure for reconciliation.
func (e *Engine) EnqueueTerminal(ctx context.Context, evidence TerminalEvidence) (UsageAcknowledgement, error) {
	if err := e.checkTenant(evidence.Settlement.TenantID); err != nil {
		return UsageAcknowledgement{}, err
	}
	var err error
	evidence, err = normalizeTerminalModelProof(evidence)
	if err != nil {
		return UsageAcknowledgement{}, err
	}
	if err := evidenceShape(evidence); err != nil {
		return UsageAcknowledgement{}, err
	}
	// Snapshot caller-owned maps before entering a retryable transaction.
	raw, err := EncodeStoreValue(evidence)
	if err != nil {
		return UsageAcknowledgement{}, err
	}
	if err = DecodeStoreValue(raw, &evidence); err != nil {
		return UsageAcknowledgement{}, err
	}
	now := e.clock().UTC()
	if !validAccountingTime(now) {
		return UsageAcknowledgement{}, fmt.Errorf("valid quota clock required")
	}
	s := evidence.Settlement
	var result UsageAcknowledgement
	err = e.store.Update(ctx, s.TenantID, func(tx Tx) error {
		result = UsageAcknowledgement{}
		if err := tx.Prefetch([]Key{{Kind: terminalAttemptKind, ID: s.AttemptID}, {Kind: terminalEvidenceKind, ID: s.EventID}, {Kind: terminalWorkKind, ID: s.EventID}, {Kind: terminalPendingKind, ID: s.EventID}, {Kind: attemptKind, ID: s.AttemptID}, {Kind: usageKind, ID: s.EventID}}); err != nil {
			return err
		}
		var attempt attemptRecord
		if err := tx.Get(attemptKind, s.AttemptID, &attempt); err != nil {
			return err
		}
		// Admission is authoritative: a terminal producer cannot accidentally
		// downgrade the original model-proof requirement.
		normalized := evidence
		normalized.RequireModelVersion = normalized.RequireModelVersion || attempt.Demand.RequireModelVersion
		if err := validateEvidenceAttempt(normalized, attempt); err != nil {
			return err
		}
		digest := quotaHash(normalized)
		var canonical terminalAttemptRecord
		indexErr := tx.Get(terminalAttemptKind, s.AttemptID, &canonical)
		if indexErr == nil && (canonical.EventID != s.EventID || canonical.Digest != digest) {
			return ErrConflict
		}
		if indexErr != nil && !errors.Is(indexErr, ErrNotFound) {
			return indexErr
		}
		var prior terminalEvidenceRecord
		if err := tx.Get(terminalEvidenceKind, s.EventID, &prior); err == nil {
			var work terminalWorkRecord
			if err := tx.Get(terminalWorkKind, s.EventID, &work); err != nil {
				return err
			}
			if err := validateTerminalRecords(s.EventID, prior, work); err != nil {
				return err
			}
			if prior.Digest != digest || indexErr != nil {
				return ErrConflict
			}
			result = work.Ack
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if indexErr == nil {
			return ErrConflict
		}
		ack := UsageAcknowledgement{TenantID: s.TenantID, AttemptID: s.AttemptID, EventID: s.EventID, EvidenceDigest: digest, Status: UsagePending, AcceptedAt: now, NextAttemptAt: now}
		// A direct prior settlement may already own this tenant-global event ID.
		var priorUsage usageRecord
		if err := tx.Get(usageKind, s.EventID, &priorUsage); err == nil {
			eventDigest, _ := settlementDigests(normalized.Settlement)
			if priorUsage.AttemptID != s.AttemptID || priorUsage.Digest != eventDigest || attempt.Decision.State != "settled" {
				return ErrConflict
			}
			if terminalModelError(normalized, attempt) == nil {
				decision := attempt.Decision
				ack.Status, ack.SettledAt, ack.NextAttemptAt, ack.Decision = UsageSettled, now, time.Time{}, &decision
			}
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := tx.Create(terminalEvidenceKind, s.EventID, terminalEvidenceRecord{Evidence: normalized, Digest: digest}); err != nil {
			return err
		}
		if err := tx.Create(terminalAttemptKind, s.AttemptID, terminalAttemptRecord{EventID: s.EventID, Digest: digest}); err != nil {
			return err
		}
		if err := tx.Create(terminalWorkKind, s.EventID, terminalWorkRecord{Ack: ack}); err != nil {
			return err
		}
		if ack.Status != UsageSettled {
			if err := tx.Create(terminalPendingKind, s.EventID, terminalAttemptRecord{EventID: s.EventID, Digest: digest}); err != nil {
				return err
			}
		}
		result = ack
		return nil
	})
	if err != nil {
		return UsageAcknowledgement{}, err
	}
	return result, nil
}

// GetUsageAcknowledgement reads durable acceptance/settlement state. It does not
// authenticate the caller or supply permission to execute the attempt again.
func (e *Engine) GetUsageAcknowledgement(ctx context.Context, tenant, eventID string) (UsageAcknowledgement, error) {
	if err := e.checkTenant(tenant); err != nil {
		return UsageAcknowledgement{}, err
	}
	var result UsageAcknowledgement
	err := e.store.View(ctx, tenant, func(tx Tx) error {
		if err := tx.Prefetch([]Key{{Kind: terminalEvidenceKind, ID: eventID}, {Kind: terminalWorkKind, ID: eventID}}); err != nil {
			return err
		}
		var evidence terminalEvidenceRecord
		var work terminalWorkRecord
		if err := tx.Get(terminalEvidenceKind, eventID, &evidence); err != nil {
			return err
		}
		if err := tx.Get(terminalWorkKind, eventID, &work); err != nil {
			return err
		}
		if err := validateTerminalRecords(eventID, evidence, work); err != nil {
			return err
		}
		result = work.Ack
		return nil
	})
	if err != nil {
		return UsageAcknowledgement{}, err
	}
	return result, nil
}

func settlementErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrModelVersionMismatch):
		return "model_version_mismatch"
	case errors.Is(err, ErrModelVersionMissing):
		return "model_version_missing"
	case errors.Is(err, ErrUnknownPrice):
		return "price_unavailable"
	case errors.Is(err, ErrQuantityOverflow):
		return "quantity_overflow"
	case errors.Is(err, ErrConflict), errors.Is(err, ErrInvalidTransition):
		return "settlement_conflict"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "settlement_interrupted"
	default:
		return "settlement_unavailable"
	}
}

func terminalBackoff(attempt uint64) time.Duration {
	if attempt == 0 {
		return time.Second
	}
	if attempt > 9 {
		return 5 * time.Minute
	}
	return time.Second * time.Duration(uint64(1)<<(attempt-1))
}

// ProcessTerminal retries only the pinned settlement transaction. Its lease is
// worker coordination, never an execution/spend lease: expiry cannot release
// exposure or authorize another provider dispatch. A crash after settlement and
// before work acknowledgement safely retries the same idempotent settlement.
func (e *Engine) ProcessTerminal(ctx context.Context, tenant, eventID string) (UsageAcknowledgement, error) {
	if err := e.checkTenant(tenant); err != nil {
		return UsageAcknowledgement{}, err
	}
	if policyText(eventID, false) != nil {
		return UsageAcknowledgement{}, fmt.Errorf("bounded event ID required")
	}
	now := e.clock().UTC()
	if !validAccountingTime(now) {
		return UsageAcknowledgement{}, fmt.Errorf("valid quota clock required")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return UsageAcknowledgement{}, err
	}
	owner := hex.EncodeToString(nonce[:])
	var payload TerminalEvidence
	var acquired terminalWorkRecord
	var modelErr error
	err := e.store.Update(ctx, tenant, func(tx Tx) error {
		payload = TerminalEvidence{}
		acquired = terminalWorkRecord{}
		modelErr = nil
		if err := tx.Prefetch([]Key{{Kind: terminalEvidenceKind, ID: eventID}, {Kind: terminalWorkKind, ID: eventID}, {Kind: terminalPendingKind, ID: eventID}}); err != nil {
			return err
		}
		var evidence terminalEvidenceRecord
		var work terminalWorkRecord
		if err := tx.Get(terminalEvidenceKind, eventID, &evidence); err != nil {
			return err
		}
		if err := tx.Get(terminalWorkKind, eventID, &work); err != nil {
			return err
		}
		if err := validateTerminalRecords(eventID, evidence, work); err != nil {
			return err
		}
		if work.Ack.Status == UsageSettled {
			acquired = work
			return nil
		}
		var pending terminalAttemptRecord
		if err := tx.Get(terminalPendingKind, eventID, &pending); err != nil {
			return err
		}
		if pending.EventID != eventID || pending.Digest != evidence.Digest {
			return ErrConflict
		}
		var attempt attemptRecord
		if err := tx.Get(attemptKind, evidence.Evidence.Settlement.AttemptID, &attempt); err != nil {
			return err
		}
		if err := validateEvidenceAttempt(evidence.Evidence, attempt); err != nil {
			return err
		}
		modelErr = terminalModelError(evidence.Evidence, attempt)
		if (work.Ack.Status == UsageProcessing && now.Before(work.LeaseUntil)) || now.Before(work.Ack.NextAttemptAt) {
			acquired = work
			return ErrRecoveryDeferred
		}
		if work.Generation == ^uint64(0) || work.Ack.Attempts == ^uint64(0) {
			return ErrQuantityOverflow
		}
		work.Generation++
		work.Ack.Attempts++
		work.Ack.Status = UsageProcessing
		work.LeaseOwner, work.LeaseUntil = owner, now.Add(terminalLease)
		if err := tx.Put(terminalWorkKind, eventID, work); err != nil {
			return err
		}
		payload, acquired = evidence.Evidence, work
		return nil
	})
	if err != nil {
		return UsageAcknowledgement{}, err
	}
	if acquired.Ack.Status == UsageSettled {
		return acquired.Ack, nil
	}
	var decision Decision
	var settleErr error
	if modelErr != nil {
		settleErr = modelErr
	} else {
		decision, settleErr = e.Settle(ctx, payload.Settlement)
	}
	finished := e.clock().UTC()
	if !validAccountingTime(finished) {
		return acquired.Ack, fmt.Errorf("valid quota clock required")
	}
	var result UsageAcknowledgement
	writeErr := e.store.Update(ctx, tenant, func(tx Tx) error {
		result = UsageAcknowledgement{}
		if err := tx.Prefetch([]Key{{Kind: terminalWorkKind, ID: eventID}, {Kind: terminalPendingKind, ID: eventID}}); err != nil {
			return err
		}
		var work terminalWorkRecord
		if err := tx.Get(terminalWorkKind, eventID, &work); err != nil {
			return err
		}
		if work.Ack.EvidenceDigest != acquired.Ack.EvidenceDigest {
			return ErrConflict
		}
		if work.Ack.Status == UsageSettled {
			result = work.Ack
			return nil
		}
		if work.LeaseOwner != owner || work.Generation != acquired.Generation {
			return ErrRecoveryLeaseLost
		}
		work.LeaseOwner, work.LeaseUntil = "", time.Time{}
		if settleErr == nil {
			work.Ack.Status, work.Ack.SettledAt, work.Ack.NextAttemptAt, work.Ack.LastErrorCode = UsageSettled, finished, time.Time{}, ""
			work.Ack.Decision = &decision
			if err := tx.Delete(terminalPendingKind, eventID); err != nil {
				return err
			}
		} else {
			work.Ack.Status = UsagePending
			work.Ack.LastErrorCode = settlementErrorCode(settleErr)
			work.Ack.NextAttemptAt = finished.Add(terminalBackoff(work.Ack.Attempts))
		}
		if err := tx.Put(terminalWorkKind, eventID, work); err != nil {
			return err
		}
		result = work.Ack
		return nil
	})
	if writeErr != nil {
		return acquired.Ack, errors.Join(settleErr, writeErr)
	}
	return result, settleErr
}

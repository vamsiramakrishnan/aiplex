package quotaplex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const administrationKind = "administration"

// Administrator is supplied by the authenticated host, never accepted from a
// request body. RequestID is an idempotent administration operation identifier.
type Administrator struct {
	PrincipalID string `json:"principal_id"`
	ActorID     string `json:"actor_id,omitempty"`
	RequestID   string `json:"request_id"`
}

func (a Administrator) validate() error {
	if policyText(a.PrincipalID, false) != nil || policyText(a.ActorID, true) != nil || policyText(a.RequestID, false) != nil {
		return fmt.Errorf("verified administrator and stable operation ID required")
	}
	return nil
}

type AdministrationRecord struct {
	ID            string          `json:"id"`
	Action        string          `json:"action"`
	ResourceID    string          `json:"resource_id"`
	Administrator Administrator   `json:"administrator"`
	At            time.Time       `json:"at"`
	InputDigest   string          `json:"input_digest"`
	Result        json.RawMessage `json:"result"`
}

type PolicyInspection struct {
	Policy    Policy    `json:"policy"`
	Reference PolicyRef `json:"reference"`
	Active    PolicyRef `json:"active"`
	AsOf      time.Time `json:"as_of"`
}

func (e *Engine) InspectPolicy(ctx context.Context, tenant string) (PolicyInspection, error) {
	if err := e.checkTenant(tenant); err != nil {
		return PolicyInspection{}, err
	}
	out := PolicyInspection{Policy: e.policy.Policy(), Reference: e.policy.Reference(), AsOf: e.clock().UTC()}
	err := e.store.View(ctx, tenant, func(tx Tx) error { return tx.Get(policyHeadKind, "active", &out.Active) })
	return out, err
}

// PolicyArtifact is a validated export, not a live publication. Deployment must
// coordinate the new policy with host bindings and all running replicas.
type PolicyArtifact struct {
	Version            string    `json:"version"`
	Policy             Policy    `json:"policy"`
	Reference          PolicyRef `json:"reference"`
	ExpectedDigest     string    `json:"expected_digest"`
	State              string    `json:"state"`
	DeploymentRequired bool      `json:"deployment_required"`
}

func (e *Engine) ValidatePolicy(ctx context.Context, policy Policy, expectedDigest string) (PolicyArtifact, error) {
	if err := e.checkTenant(policy.TenantID); err != nil {
		return PolicyArtifact{}, err
	}
	compiled, err := Compile(policy)
	if err != nil {
		return PolicyArtifact{}, err
	}
	copy := *e
	copy.policy = compiled
	copy.store = previewStore{base: e.store}
	if err = copy.Activate(ctx, expectedDigest); err != nil {
		return PolicyArtifact{}, err
	}
	return PolicyArtifact{Version: Version, Policy: compiled.Policy(), Reference: compiled.Reference(), ExpectedDigest: expectedDigest, State: "validated-export", DeploymentRequired: true}, nil
}

type AdmissionPreview struct {
	Plan     Plan      `json:"plan"`
	Decision Decision  `json:"decision"`
	AsOf     time.Time `json:"as_of"`
	Mutates  bool      `json:"mutates"`
}

// Preview runs the actual admission transaction against a snapshot overlay.
// The decision is explanatory only: it creates neither a reservation nor a right
// to dispatch, and availability can change immediately after the snapshot.
func (e *Engine) Preview(ctx context.Context, d Demand) (AdmissionPreview, error) {
	copy := *e
	var record attemptRecord
	copy.store = previewStore{base: e.store, capture: func(tx Tx) error { return tx.Get(attemptKind, d.AttemptID, &record) }}
	at := e.clock().UTC()
	copy.clock = func() time.Time { return at }
	decision, err := copy.Admit(ctx, d)
	if err != nil {
		return AdmissionPreview{}, err
	}
	return AdmissionPreview{Plan: record.Plan, Decision: decision, AsOf: at, Mutates: false}, nil
}

type AdministrationPage struct {
	Kind        string    `json:"kind"`
	Records     []Record  `json:"records"`
	NextAfterID string    `json:"next_after_id,omitempty"`
	Done        bool      `json:"done"`
	AsOf        time.Time `json:"as_of"`
	// OlderThan and Unknown are set only by InventoryUnknown. Unknown
	// summarizes the records of this page, not the whole tenant.
	OlderThan string           `json:"older_than,omitempty"`
	Unknown   *UnknownExposure `json:"unknown,omitempty"`
}

// Inventory exposes only explicitly selected administration kinds. Its caller
// must possess tenant-wide read authority; this is not an end-user listing API.
func (e *Engine) Inventory(ctx context.Context, tenant, kind, after string, limit int) (AdministrationPage, error) {
	if err := e.checkTenant(tenant); err != nil {
		return AdministrationPage{}, err
	}
	kinds := map[string]string{"budgets": budgetKind, "loans": loanKind, "attempts": attemptKind, "reconciliations": reconciliationPlanKind, "audit": administrationKind, "terminal-work": terminalWorkKind, "schedulers": schedulerHeadKind, "scheduled-jobs": schedulerJobKind}
	storageKind, ok := kinds[kind]
	if !ok || limit < 1 || limit > 64 {
		return AdministrationPage{}, fmt.Errorf("supported inventory and limit in [1,64] required")
	}
	out := AdministrationPage{Kind: kind, AsOf: e.clock().UTC(), Records: []Record{}}
	err := e.store.View(ctx, tenant, func(tx Tx) error {
		records, err := tx.Scan(storageKind, after, limit)
		if err != nil {
			return err
		}
		out.Records = records
		out.Done = len(records) < limit
		if len(records) > 0 {
			out.NextAfterID = records[len(records)-1].ID
		}
		return nil
	})
	return out, err
}

// InventoryUnknown pages through attempts and returns those in the unknown
// state that were dispatched at least olderThan before now (0 selects all of
// them), with a summary of their count, oldest dispatch time and exposure.
// An unknown attempt without a valid dispatch time is always included, since
// its age cannot be shown to be younger. A record that cannot be decoded is
// returned and counted as unreadable rather than skipped. Paging is over
// scanned records, so a page may hold no matches and still not be done.
func (e *Engine) InventoryUnknown(ctx context.Context, tenant string, olderThan time.Duration, after string, limit int) (AdministrationPage, error) {
	if err := e.checkTenant(tenant); err != nil {
		return AdministrationPage{}, err
	}
	if olderThan < 0 || limit < 1 || limit > 64 {
		return AdministrationPage{}, fmt.Errorf("nonnegative older_than and limit in [1,64] required")
	}
	now := e.clock().UTC()
	if !validAccountingTime(now) {
		return AdministrationPage{}, fmt.Errorf("valid quota clock required")
	}
	cutoff := now.Add(-olderThan)
	out := AdministrationPage{Kind: "attempts", AsOf: now, Records: []Record{}, OlderThan: olderThan.String()}
	err := e.store.View(ctx, tenant, func(tx Tx) error {
		out.Records, out.Unknown = []Record{}, newUnknownExposure(now)
		records, err := tx.Scan(attemptKind, after, limit)
		if err != nil {
			return err
		}
		out.Done = len(records) < limit
		out.NextAfterID = ""
		if len(records) > 0 {
			out.NextAfterID = records[len(records)-1].ID
		}
		for _, record := range records {
			var attempt attemptRecord
			if err := DecodeStoreValue(record.Value, &attempt); err != nil || attempt.Demand.AttemptID != record.ID || attempt.Decision.TenantID != tenant {
				out.Unknown.Unreadable++
				out.Records = append(out.Records, record)
				continue
			}
			if attempt.Decision.State != "unknown" {
				continue
			}
			if validAccountingTime(attempt.DispatchAt) && attempt.DispatchAt.After(cutoff) {
				continue
			}
			out.Unknown.addAttempt(attempt)
			out.Records = append(out.Records, record)
		}
		return nil
	})
	return out, err
}

var errAdministrationReplay = errors.New("administration operation replay")

type administrationStore struct {
	base   Store
	before func(Tx) error
	after  func(Tx) error
}

func (s administrationStore) View(ctx context.Context, tenant string, fn func(Tx) error) error {
	return s.base.View(ctx, tenant, fn)
}
func (s administrationStore) Update(ctx context.Context, tenant string, fn func(Tx) error) error {
	return s.base.Update(ctx, tenant, func(tx Tx) error {
		if s.before != nil {
			if err := s.before(tx); err != nil {
				return err
			}
		}
		if err := fn(tx); err != nil {
			return err
		}
		if s.after != nil {
			return s.after(tx)
		}
		return nil
	})
}

func (e *Engine) auditedLoan(ctx context.Context, tenant, action, id string, input any, admin Administrator) (LoanInfo, error) {
	if err := e.checkTenant(tenant); err != nil {
		return LoanInfo{}, err
	}
	if err := admin.validate(); err != nil {
		return LoanInfo{}, err
	}
	now := e.clock().UTC()
	if !validAccountingTime(now) {
		return LoanInfo{}, fmt.Errorf("valid administration clock required")
	}
	operationID := quotaHash(action, admin.PrincipalID, admin.ActorID, admin.RequestID)
	digest := quotaHash(input)
	var result LoanInfo
	copy := *e
	copy.clock = func() time.Time { return now }
	copy.store = administrationStore{base: e.store, before: func(tx Tx) error {
		var previous AdministrationRecord
		if err := tx.Get(administrationKind, operationID, &previous); err == nil {
			if previous.InputDigest != digest || previous.Action != action || previous.ResourceID != id {
				return ErrConflict
			}
			if err := json.Unmarshal(previous.Result, &result); err != nil {
				return err
			}
			return errAdministrationReplay
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		return nil
	}, after: func(tx Tx) error {
		var loan loanRecord
		if err := tx.Get(loanKind, id, &loan); err != nil {
			return err
		}
		result = loan.Info
		raw, err := EncodeStoreValue(result)
		if err != nil {
			return err
		}
		return tx.Create(administrationKind, operationID, AdministrationRecord{ID: operationID, Action: action, ResourceID: id, Administrator: admin, At: now, InputDigest: digest, Result: raw})
	}}
	var err error
	switch action {
	case "loan-grant":
		_, err = copy.GrantLoan(ctx, input.(LoanRequest))
	case "loan-return":
		_, err = copy.ReturnUnusedLoan(ctx, tenant, id)
	default:
		return LoanInfo{}, ErrDenied
	}
	if errors.Is(err, errAdministrationReplay) {
		return result, nil
	}
	if err != nil {
		return LoanInfo{}, err
	}
	return result, nil
}
func (e *Engine) GrantLoanAdmin(ctx context.Context, request LoanRequest, admin Administrator) (LoanInfo, error) {
	return e.auditedLoan(ctx, request.TenantID, "loan-grant", request.ID, request, admin)
}
func (e *Engine) ReturnUnusedLoanAdmin(ctx context.Context, tenant, id string, admin Administrator) (LoanInfo, error) {
	return e.auditedLoan(ctx, tenant, "loan-return", id, struct{ ID string }{id}, admin)
}

// previewStore uses the same read snapshot and transaction algorithm as a write,
// but stages every mutation privately. It cannot create a dispatch capability.
type previewStore struct {
	base    Store
	capture func(Tx) error
}

func (s previewStore) View(ctx context.Context, tenant string, fn func(Tx) error) error {
	return s.base.View(ctx, tenant, fn)
}
func (s previewStore) Update(ctx context.Context, tenant string, fn func(Tx) error) error {
	return s.base.View(ctx, tenant, func(tx Tx) error {
		overlay := &previewTx{Tx: tx, values: map[Key]json.RawMessage{}}
		if err := fn(overlay); err != nil {
			return err
		}
		if s.capture != nil {
			return s.capture(overlay)
		}
		return nil
	})
}

type previewTx struct {
	Tx
	values map[Key]json.RawMessage
	bytes  int
}

func (t *previewTx) Get(kind, id string, out any) error {
	if raw, ok := t.values[Key{kind, id}]; ok {
		if raw == nil {
			return ErrNotFound
		}
		return DecodeStoreValue(raw, out)
	}
	return t.Tx.Get(kind, id, out)
}
func (t *previewTx) Put(kind, id string, value any) error {
	if err := ValidateStoreCoordinate("preview", kind, id); err != nil {
		return err
	}
	raw, err := EncodeStoreValue(value)
	if err != nil {
		return err
	}
	key := Key{kind, id}
	t.bytes -= len(t.values[key])
	t.bytes += len(raw)
	if len(t.values) >= MaxTransactionRecords && t.values[key] == nil || t.bytes > MaxTransactionBytes {
		return ErrTransactionLimit
	}
	t.values[key] = raw
	return nil
}
func (t *previewTx) Create(kind, id string, value any) error {
	var old any
	err := t.Get(kind, id, &old)
	if err == nil {
		return ErrConflict
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	return t.Put(kind, id, value)
}
func (t *previewTx) Delete(kind, id string) error {
	if err := ValidateStoreCoordinate("preview", kind, id); err != nil {
		return err
	}
	t.values[Key{kind, id}] = nil
	return nil
}
func (t *previewTx) List(string) ([]Record, error)              { return nil, ErrReadOnly }
func (t *previewTx) Scan(string, string, int) ([]Record, error) { return nil, ErrReadOnly }

// GetTerminalEvidence returns the original immutable producer facts, including
// facts later resolved through an explicit reconciliation receipt.
func (e *Engine) GetTerminalEvidence(ctx context.Context, tenant, eventID string) (TerminalEvidence, UsageAcknowledgement, error) {
	if err := e.checkTenant(tenant); err != nil {
		return TerminalEvidence{}, UsageAcknowledgement{}, err
	}
	var evidence terminalEvidenceRecord
	var work terminalWorkRecord
	err := e.store.View(ctx, tenant, func(tx Tx) error {
		if err := tx.Get(terminalEvidenceKind, eventID, &evidence); err != nil {
			return err
		}
		if err := tx.Get(terminalWorkKind, eventID, &work); err != nil {
			return err
		}
		return validateTerminalRecords(eventID, evidence, work)
	})
	return evidence.Evidence, work.Ack, err
}

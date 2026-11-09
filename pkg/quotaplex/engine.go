package quotaplex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"time"
)

var (
	ErrDenied            = errors.New("quota admission denied")
	ErrInvalidTransition = errors.New("invalid quota lifecycle transition")
	ErrDispatchClaimed   = errors.New("quota dispatch was already claimed")
)

const (
	attemptKind     = "attempt"
	budgetKind      = "budget"
	rateKind        = "rate"
	concurrencyKind = "concurrency"
	policyKind      = "policy"
	priceBookKind   = "price-book"
	policyHeadKind  = "policy-head"
	lineageKind     = "lineage"
	ledgerKind      = "ledger"
	usageKind       = "usage"
)

// Engine coordinates the quota-owned transaction boundary. Its public methods
// accept trusted host inputs, not authenticated end-user wire payloads. One
// compiled combined policy is active per tenant; provider I/O stays outside it.
type Engine struct {
	policy *CompiledPolicy
	store  Store
	clock  func() time.Time
	tenant string
}

func NewEngine(policy *CompiledPolicy, store Store, clock func() time.Time) (*Engine, error) {
	if policy == nil || store == nil || (reflect.ValueOf(store).Kind() == reflect.Pointer && reflect.ValueOf(store).IsNil()) {
		return nil, fmt.Errorf("compiled policy and transactional store are required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &Engine{policy: policy, store: store, clock: clock, tenant: policy.Policy().TenantID}, nil
}

type policyRecord struct {
	Reference PolicyRef `json:"reference"`
	Policy    Policy    `json:"policy"`
}

type lineageRecord struct {
	Shape string `json:"shape"`
}

type attemptRecord struct {
	Fingerprint      string        `json:"fingerprint"`
	Demand           Demand        `json:"demand"`
	Plan             Plan          `json:"plan"`
	Decision         Decision      `json:"decision"`
	AdmittedAt       time.Time     `json:"admitted_at"`
	DispatchClaim    DispatchClaim `json:"dispatch_claim"`
	DispatchAt       time.Time     `json:"dispatch_at"`
	UnknownReason    string        `json:"unknown_reason,omitempty"`
	SettlementDigest string        `json:"settlement_digest,omitempty"`
	Actual           Vector        `json:"actual,omitempty"`
	ProviderResource string        `json:"provider_resource,omitempty"`
}

// These records deliberately retain quantities separately: an actual overrun
// can make available capacity negative without erasing consumed work.
type budgetRecord struct {
	Key         string    `json:"key"`
	RuleID      string    `json:"rule_id"`
	MeterID     string    `json:"meter_id"`
	Window      Window    `json:"window"`
	Shape       string    `json:"shape"`
	Limit       int64     `json:"limit"`
	LimitPolicy PolicyRef `json:"limit_policy"`
	Held        int64     `json:"held"`
	Consumed    int64     `json:"consumed"`
	Credited    int64     `json:"credited"`
	Debited     int64     `json:"debited"`
}

type rateRecord struct {
	Shape     string    `json:"shape"`
	Available string    `json:"available"` // exact rational quantity, never a float
	At        time.Time `json:"at"`
}

type concurrencyRecord struct {
	Shape  string `json:"shape"`
	Active int64  `json:"active"`
}

type budgetMutation struct {
	Key      string `json:"key"`
	MeterID  string `json:"meter_id"`
	Held     int64  `json:"held"`
	Consumed int64  `json:"consumed"`
}

type ledgerEntry struct {
	AttemptID          string           `json:"attempt_id"`
	Action             string           `json:"action"`
	At                 time.Time        `json:"at"`
	Policy             PolicyRef        `json:"policy"`
	Budgets            []budgetMutation `json:"budgets,omitempty"`
	Usage              Vector           `json:"usage,omitempty"`
	EventID            string           `json:"event_id,omitempty"`
	ProviderResourceID string           `json:"provider_resource_id,omitempty"`
}

// AttemptInfo is a stored snapshot for authorized recovery workers. Reading an
// admitted result never creates another dispatch claim or refunds exposure.
type AttemptInfo struct {
	Demand           Demand        `json:"demand"`
	Plan             Plan          `json:"plan"`
	Decision         Decision      `json:"decision"`
	AdmittedAt       time.Time     `json:"admitted_at"`
	Claim            DispatchClaim `json:"claim"`
	DispatchAt       time.Time     `json:"dispatch_at"`
	UnknownReason    string        `json:"unknown_reason,omitempty"`
	Actual           Vector        `json:"actual,omitempty"`
	ProviderResource string        `json:"provider_resource,omitempty"`
}

// BudgetBalance includes the last committed limit snapshot. A newly activated
// policy may change that limit before the next successful admission.
type BudgetBalance struct {
	Key         string    `json:"key"`
	RuleID      string    `json:"rule_id"`
	MeterID     string    `json:"meter_id"`
	Window      Window    `json:"window"`
	Limit       int64     `json:"limit"`
	LimitPolicy PolicyRef `json:"limit_policy"`
	Held        int64     `json:"held"`
	Consumed    int64     `json:"consumed"`
	Credited    int64     `json:"credited"`
	Debited     int64     `json:"debited"`
	Available   int64     `json:"available"`
}

func quotaHash(parts ...any) string {
	raw, _ := json.Marshal(parts)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// Activate atomically publishes an immutable policy and changes the tenant's
// active head only from expectedDigest. Structural lineage edits require an
// explicit future migration; changing quantities cannot reset old balances.
func (e *Engine) Activate(ctx context.Context, expectedDigest string) error {
	policy, reference := e.policy.Policy(), e.policy.Reference()
	if err := policyUsableAt(policy, e.clock().UTC()); err != nil {
		return err
	}
	lineages := e.policy.Lineages()
	return e.store.Update(ctx, policy.TenantID, func(tx Tx) error {
		var head PolicyRef
		err := tx.Get(policyHeadKind, "active", &head)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil && head == reference {
			return nil
		}
		if head.Digest != expectedDigest {
			return ErrConflict
		}
		if head.Digest != "" && (head.ID != reference.ID || reference.Revision <= head.Revision) {
			return fmt.Errorf("policy identity replacement or non-increasing revision: %w", ErrConflict)
		}
		for _, key := range sortedEngineKeys(lineages) {
			var existing lineageRecord
			err := tx.Get(lineageKind, key, &existing)
			if err == nil {
				if existing.Shape != lineages[key] {
					return fmt.Errorf("policy lineage changed: %w", ErrConflict)
				}
			} else if errors.Is(err, ErrNotFound) {
				if err := tx.Create(lineageKind, key, lineageRecord{Shape: lineages[key]}); err != nil {
					return err
				}
			} else {
				return err
			}
		}
		for _, priceBook := range policy.PriceBooks {
			key := quotaHash(priceBook.ID, priceBook.Revision)
			var existing PriceBook
			if err := tx.Get(priceBookKind, key, &existing); err == nil {
				if quotaHash(existing) != quotaHash(priceBook) {
					return fmt.Errorf("price-book revision changed: %w", ErrConflict)
				}
			} else if errors.Is(err, ErrNotFound) {
				if err := tx.Create(priceBookKind, key, priceBook); err != nil {
					return err
				}
			} else {
				return err
			}
		}
		key := quotaHash(reference.ID, reference.Revision)
		var existing policyRecord
		if err := tx.Get(policyKind, key, &existing); err == nil {
			if existing.Reference != reference {
				return ErrConflict
			}
		} else if errors.Is(err, ErrNotFound) {
			if err := tx.Create(policyKind, key, policyRecord{Reference: reference, Policy: policy}); err != nil {
				return err
			}
		} else {
			return err
		}
		return tx.Put(policyHeadKind, "active", reference)
	})
}

func (e *Engine) Ready(ctx context.Context) error {
	if err := policyUsableAt(e.policy.policy, e.clock().UTC()); err != nil {
		return err
	}
	ref := e.policy.Reference()
	return e.store.View(ctx, e.tenant, func(tx Tx) error {
		var head PolicyRef
		if err := tx.Get(policyHeadKind, "active", &head); err != nil {
			return err
		}
		if head != ref {
			return fmt.Errorf("compiled policy is not active: %w", ErrConflict)
		}
		return nil
	})
}

func policyUsableAt(policy Policy, now time.Time) error {
	if !validAccountingTime(now) {
		return fmt.Errorf("valid quota clock required")
	}
	if now.Before(policy.EffectiveAt) || (!policy.ExpiresAt.IsZero() && !now.Before(policy.ExpiresAt)) {
		return fmt.Errorf("quota policy is not currently effective: %w", ErrDenied)
	}
	return nil
}

func sortedEngineKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Admit applies every matched control in one transaction. Denials persist their
// replay result but commit no proposed budget, rate or concurrency mutations.
func (e *Engine) Admit(ctx context.Context, demand Demand) (Decision, error) {
	if err := ValidateDemand(demand); err != nil {
		return Decision{}, err
	}
	if demand.Context.TenantID != e.tenant {
		return Decision{}, fmt.Errorf("quota demand tenant does not match compiled policy")
	}
	now := e.clock().UTC()
	if !validAccountingTime(now) {
		return Decision{}, fmt.Errorf("quota clock returned invalid accounting time")
	}
	fingerprint, err := demandFingerprint(demand)
	if err != nil {
		return Decision{}, err
	}
	var result Decision
	err = e.store.Update(ctx, demand.Context.TenantID, func(tx Tx) error {
		result = Decision{}
		var existing attemptRecord
		if err := tx.Get(attemptKind, demand.AttemptID, &existing); err == nil {
			if existing.Fingerprint != fingerprint {
				return ErrConflict
			}
			result = existing.Decision
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		decision := Decision{TenantID: demand.Context.TenantID, AttemptID: demand.AttemptID, Policy: e.policy.Reference(), State: "denied"}
		persistDenial := func(reason string, retry time.Duration) error {
			decision.Reason, decision.RetryAfter = reason, retry
			result = decision
			return tx.Create(attemptKind, demand.AttemptID, attemptRecord{Fingerprint: fingerprint, Demand: demand, Decision: decision})
		}
		var head PolicyRef
		if err := tx.Get(policyHeadKind, "active", &head); errors.Is(err, ErrNotFound) {
			return persistDenial("policy_inactive", 0)
		} else if err != nil {
			return err
		} else if head != e.policy.Reference() {
			return persistDenial("policy_inactive", 0)
		}
		// A request may wait behind another transaction after its ingress clock
		// sample. Observe accounting time after the consistent read so contention
		// cannot make a healthy clock appear to precede a committed rate debit.
		now := e.clock().UTC()
		if !validAccountingTime(now) {
			return fmt.Errorf("quota clock returned invalid accounting time")
		}
		plan, err := e.policy.Resolve(demand, now)
		if err != nil {
			var denied *ResolveError
			if errors.As(err, &denied) {
				return persistDenial(denied.Code, 0)
			}
			return err
		}
		decision.PayerID = plan.PayerID
		// Known control coordinates are loaded together by durable backends.
		// Native transactions still recheck all of them before committing.
		keys := make([]Key, 0, len(plan.Budgets)+len(plan.Rates)+len(plan.Concurrency)+1)
		keys = append(keys, Key{Kind: ledgerKind, ID: quotaHash(demand.AttemptID, "admit")})
		for _, limit := range plan.Budgets {
			keys = append(keys, Key{Kind: budgetKind, ID: limit.Key})
		}
		for _, limit := range plan.Rates {
			keys = append(keys, Key{Kind: rateKind, ID: limit.Key})
		}
		for _, limit := range plan.Concurrency {
			keys = append(keys, Key{Kind: concurrencyKind, ID: limit.Key})
		}
		if err := tx.Prefetch(keys); err != nil {
			return err
		}
		budgets := make(map[string]budgetRecord, len(plan.Budgets))
		rates := make(map[string]rateRecord, len(plan.Rates))
		concurrency := make(map[string]concurrencyRecord, len(plan.Concurrency))
		changes := make([]budgetMutation, 0, len(plan.Budgets))
		for _, limit := range plan.Budgets {
			state, err := loadBudget(tx, limit)
			if err != nil {
				return err
			}
			if state.Credited > 0 {
				if err := checkBudgetLoanExpiry(tx, limit, now); err != nil {
					var denied *ResolveError
					if errors.As(err, &denied) {
						return persistDenial(denied.Code, 0)
					}
					return err
				}
			}
			available, err := budgetAvailable(state, limit.Limit)
			if err != nil {
				return err
			}
			if available < limit.Demand {
				return persistDenial("budget_exhausted", positiveDuration(limit.Window.End.Sub(now)))
			}
			state.Held, err = checkedAdd(state.Held, limit.Demand)
			if err != nil {
				return err
			}
			state.Limit = limit.Limit
			state.LimitPolicy = plan.Policy
			budgets[limit.Key] = state
			changes = append(changes, budgetMutation{Key: limit.Key, MeterID: limit.MeterID, Held: limit.Demand})
		}
		for _, limit := range plan.Rates {
			state, retry, allowed, err := nextRate(tx, limit, now)
			if err != nil {
				return err
			}
			if !allowed {
				return persistDenial("rate_exhausted", retry)
			}
			rates[limit.Key] = state
		}
		for _, limit := range plan.Concurrency {
			var state concurrencyRecord
			if err := tx.Get(concurrencyKind, limit.Key, &state); errors.Is(err, ErrNotFound) {
				state.Shape = limit.Shape
			} else if err != nil {
				return err
			}
			if state.Shape != limit.Shape || state.Active < 0 {
				return fmt.Errorf("concurrency state shape mismatch: %w", ErrConflict)
			}
			if state.Active > limit.Limit || limit.Demand > limit.Limit-state.Active {
				return persistDenial("concurrency_exhausted", 0)
			}
			var err error
			state.Active, err = checkedAdd(state.Active, limit.Demand)
			if err != nil {
				return err
			}
			concurrency[limit.Key] = state
		}
		for _, key := range sortedEngineKeys(budgets) {
			if err := tx.Put(budgetKind, key, budgets[key]); err != nil {
				return err
			}
		}
		for _, key := range sortedEngineKeys(rates) {
			if err := tx.Put(rateKind, key, rates[key]); err != nil {
				return err
			}
		}
		for _, key := range sortedEngineKeys(concurrency) {
			if err := tx.Put(concurrencyKind, key, concurrency[key]); err != nil {
				return err
			}
		}
		decision.Allowed, decision.Reason, decision.State = true, "allowed", "admitted"
		if err := tx.Create(ledgerKind, quotaHash(demand.AttemptID, "admit"), ledgerEntry{AttemptID: demand.AttemptID, Action: "hold", At: now, Policy: plan.Policy, Budgets: changes}); err != nil {
			return err
		}
		if err := tx.Create(attemptKind, demand.AttemptID, attemptRecord{Fingerprint: fingerprint, Demand: demand, Plan: plan, Decision: decision, AdmittedAt: now}); err != nil {
			return err
		}
		result = decision
		return nil
	})
	if err != nil {
		return Decision{}, err
	}
	return result, nil
}

func positiveDuration(duration time.Duration) time.Duration {
	if duration < 0 {
		return 0
	}
	return duration
}

func loadBudget(tx Tx, limit BudgetLimit) (budgetRecord, error) {
	var state budgetRecord
	if err := tx.Get(budgetKind, limit.Key, &state); errors.Is(err, ErrNotFound) {
		return budgetRecord{Key: limit.Key, RuleID: limit.RuleID, MeterID: limit.MeterID, Window: limit.Window, Shape: limit.Shape, Limit: limit.Limit}, nil
	} else if err != nil {
		return budgetRecord{}, err
	}
	if state.Key != limit.Key || state.Shape != limit.Shape || state.MeterID != limit.MeterID || !state.Window.Start.Equal(limit.Window.Start) || !state.Window.End.Equal(limit.Window.End) || state.Held < 0 || state.Consumed < 0 || state.Credited < 0 || state.Debited < 0 {
		return budgetRecord{}, fmt.Errorf("budget state shape mismatch: %w", ErrConflict)
	}
	return state, nil
}

func demandFingerprint(demand Demand) (string, error) {
	// RequestID is correlation and may change across transport retries. Group
	// membership is a set; ordering cannot create another accounting identity.
	demand.Context.RequestID = ""
	demand.Principal.Membership.Groups = append([]GroupRef(nil), demand.Principal.Membership.Groups...)
	sort.Slice(demand.Principal.Membership.Groups, func(i, j int) bool {
		a, b := demand.Principal.Membership.Groups[i], demand.Principal.Membership.Groups[j]
		return a.Source < b.Source || (a.Source == b.Source && a.ID < b.ID)
	})
	if demand.Actor != nil {
		actor := *demand.Actor
		actor.Membership.Groups = append([]GroupRef(nil), actor.Membership.Groups...)
		sort.Slice(actor.Membership.Groups, func(i, j int) bool {
			a, b := actor.Membership.Groups[i], actor.Membership.Groups[j]
			return a.Source < b.Source || (a.Source == b.Source && a.ID < b.ID)
		})
		demand.Actor = &actor
	}
	raw, err := json.Marshal(demand)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func budgetAvailable(state budgetRecord, limit int64) (int64, error) {
	available := new(big.Int).SetInt64(limit)
	available.Add(available, big.NewInt(state.Credited))
	available.Sub(available, big.NewInt(state.Debited))
	available.Sub(available, big.NewInt(state.Held))
	available.Sub(available, big.NewInt(state.Consumed))
	if !available.IsInt64() {
		return 0, ErrQuantityOverflow
	}
	return available.Int64(), nil
}

// nextRate uses exact rational credit. A denied check returns no staged write;
// time-based refill is recomputed from the last committed acceptance.
func nextRate(tx Tx, limit RateLimit, now time.Time) (rateRecord, time.Duration, bool, error) {
	var state rateRecord
	credit := new(big.Rat).SetInt64(limit.Burst)
	if err := tx.Get(rateKind, limit.Key, &state); errors.Is(err, ErrNotFound) {
		state = rateRecord{Shape: limit.Shape, At: now}
	} else if err != nil {
		return rateRecord{}, 0, false, err
	} else {
		var valid bool
		credit, valid = new(big.Rat).SetString(state.Available)
		if !valid || credit.Sign() < 0 || credit.Cmp(new(big.Rat).SetInt64(limit.Burst)) > 0 || state.Shape != limit.Shape || state.At.IsZero() || now.Before(state.At) {
			return rateRecord{}, 0, false, fmt.Errorf("rate state or clock mismatch: %w", ErrConflict)
		}
		elapsed := now.Sub(state.At)
		refill := new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(int64(elapsed)), big.NewInt(limit.Rate)), big.NewInt(int64(limit.Interval)))
		credit.Add(credit, refill)
		if credit.Cmp(new(big.Rat).SetInt64(limit.Burst)) > 0 {
			credit.SetInt64(limit.Burst)
		}
	}
	needed := new(big.Rat).SetInt64(limit.Demand)
	if credit.Cmp(needed) < 0 {
		if limit.Rate == 0 || limit.Demand > limit.Burst {
			return rateRecord{}, 0, false, nil
		}
		wait := new(big.Rat).Sub(needed, credit)
		wait.Mul(wait, new(big.Rat).SetFrac(big.NewInt(int64(limit.Interval)), big.NewInt(limit.Rate)))
		quotient, remainder := new(big.Int), new(big.Int)
		quotient.QuoRem(wait.Num(), wait.Denom(), remainder)
		if remainder.Sign() > 0 {
			quotient.Add(quotient, big.NewInt(1))
		}
		if !quotient.IsInt64() {
			return rateRecord{}, time.Duration(1<<63 - 1), false, nil
		}
		return rateRecord{}, time.Duration(quotient.Int64()), false, nil
	}
	credit.Sub(credit, needed)
	return rateRecord{Shape: limit.Shape, Available: credit.RatString(), At: now}, 0, true, nil
}

func (e *Engine) GetAttempt(ctx context.Context, tenant, attempt string) (AttemptInfo, error) {
	if err := e.checkTenant(tenant); err != nil {
		return AttemptInfo{}, err
	}
	var record attemptRecord
	err := e.store.View(ctx, tenant, func(tx Tx) error { return tx.Get(attemptKind, attempt, &record) })
	if err != nil {
		return AttemptInfo{}, err
	}
	return AttemptInfo{Demand: record.Demand, Plan: record.Plan, Decision: record.Decision, AdmittedAt: record.AdmittedAt, Claim: record.DispatchClaim, DispatchAt: record.DispatchAt, UnknownReason: record.UnknownReason, Actual: record.Actual, ProviderResource: record.ProviderResource}, nil
}

func (e *Engine) BudgetBalance(ctx context.Context, tenant, key string) (BudgetBalance, error) {
	if err := e.checkTenant(tenant); err != nil {
		return BudgetBalance{}, err
	}
	var record budgetRecord
	err := e.store.View(ctx, tenant, func(tx Tx) error { return tx.Get(budgetKind, key, &record) })
	if err != nil {
		return BudgetBalance{}, err
	}
	available, err := budgetAvailable(record, record.Limit)
	if err != nil {
		return BudgetBalance{}, err
	}
	return BudgetBalance{Key: record.Key, RuleID: record.RuleID, MeterID: record.MeterID, Window: record.Window, Limit: record.Limit, LimitPolicy: record.LimitPolicy, Held: record.Held, Consumed: record.Consumed, Credited: record.Credited, Debited: record.Debited, Available: available}, nil
}

func (e *Engine) checkTenant(tenant string) error {
	if tenant != e.tenant {
		return fmt.Errorf("quota tenant does not match compiled policy")
	}
	return nil
}

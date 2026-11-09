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
	schedulerHeadKind      = "scheduler-head"
	schedulerPoolKind      = "scheduler-pool"
	schedulerJobKind       = "scheduler-job"
	schedulerOwnerKind     = "scheduler-attempt-owner"
	schedulerSelectionKind = "scheduler-selection"
	schedulerQueueKind     = "scheduler-queue"
)

// CapacityAttestation is operator-verified admission capacity, not evidence that
// an upstream provider will decode tokens, serve requests, or meet a latency SLA.
// Target confines the authority to an exact provider/pool and optional additional
// coordinates. A deployment must verify SourceRef before supplying this value.
type CapacityAttestation struct {
	ID        string        `json:"id"`
	SourceRef string        `json:"source_ref"`
	Version   string        `json:"version"`
	TenantID  string        `json:"tenant_id"`
	PoolID    string        `json:"pool_id"`
	MeterID   string        `json:"meter_id"`
	Target    Match         `json:"target"`
	Rate      int64         `json:"rate"`
	Burst     int64         `json:"burst"`
	Interval  time.Duration `json:"interval"`
	IssuedAt  time.Time     `json:"issued_at"`
	ExpiresAt time.Time     `json:"expires_at"`
}

// ProtectedRate partitions admission credits. Other flows cannot consume them.
// Protected and shared rates use Capacity.Interval. This is not stream pacing,
// preemption, a native reservation, or an end-to-end minimum-throughput promise.
type ProtectedRate struct {
	Rate  int64 `json:"rate"`
	Burst int64 `json:"burst"`
}

type SchedulerConfig struct {
	ID string `json:"id"`
	// FlowAuthorityDigest pins the host's rules that map trusted funding or
	// identity facts to flows. A changed mapping requires explicit migration.
	FlowAuthorityDigest string                   `json:"flow_authority_digest,omitempty"`
	Queue               QueuePolicy              `json:"queue"`
	Capacity            CapacityAttestation      `json:"capacity"`
	Protected           map[string]ProtectedRate `json:"protected,omitempty"`
	Priorities          map[string]int           `json:"priorities,omitempty"`
}

type ScheduledRequest struct {
	Demand   Demand    `json:"demand"`
	FlowID   string    `json:"flow_id"`
	Deadline time.Time `json:"deadline"`
}

type ScheduledStatus struct {
	AttemptID    string    `json:"attempt_id"`
	FlowID       string    `json:"flow_id"`
	State        string    `json:"state"` // queued, admitted, denied, cancelled or expired
	Deadline     time.Time `json:"deadline"`
	EnqueuedAt   time.Time `json:"enqueued_at"`
	DemandDigest string    `json:"demand_digest"`
	Decision     *Decision `json:"decision,omitempty"`
}

// ScheduledAdmission creates quota holds only. The trusted host must still
// perform fresh dispatch authorization and obtain Engine.ClaimDispatch once.
type ScheduledAdmission struct {
	Status ScheduledStatus `json:"status"`
	Demand Demand          `json:"demand"`
}

type schedulerHead struct {
	Digest string    `json:"digest"`
	Shape  string    `json:"shape"`
	Policy PolicyRef `json:"policy"`
}

type schedulerOwner struct {
	SchedulerID string `json:"scheduler_id"`
	Digest      string `json:"digest"`
}

type schedulerQueueState struct {
	QueueState
	Paused map[string]bool `json:"paused_visits,omitempty"`
}

func (s *AtomicScheduler) clearPausedIdle(state *schedulerQueueState) {
	if state.Paused == nil {
		state.Paused = map[string]bool{}
	}
	for flow := range state.Paused {
		if queueHead(state.Items, flow) < 0 {
			delete(state.Paused, flow)
		}
	}
}

func (s *AtomicScheduler) validateQueue(state schedulerQueueState, now time.Time) error {
	if err := s.queue.validate(state.QueueState, now); err != nil {
		return err
	}
	if len(state.Paused) > len(s.queue.flows) {
		return ErrConflict
	}
	for flow, paused := range state.Paused {
		if !paused || s.config.Queue.Weights[flow] == 0 || queueHead(state.Items, flow) < 0 || (state.Credited && s.queue.flows[state.Cursor] == flow) {
			return fmt.Errorf("invalid paused scheduler visit: %w", ErrConflict)
		}
	}
	return nil
}

type schedulerJob struct {
	Status ScheduledStatus `json:"status"`
	Demand Demand          `json:"demand"`
}

// AtomicScheduler persists fairness, capacity debits and quota admission inside
// the engine's one tenant transaction. It never performs provider or directory
// I/O, and never returns transaction results before their commit succeeds.
type AtomicScheduler struct {
	engine *Engine
	config SchedulerConfig
	queue  *FairQueue
	digest string
	shape  string
	shared ProtectedRate
}

func NewAtomicScheduler(engine *Engine, config SchedulerConfig) (*AtomicScheduler, error) {
	if engine == nil || !queueID(config.ID) || (config.FlowAuthorityDigest != "" && !queueDigest(config.FlowAuthorityDigest)) {
		return nil, fmt.Errorf("engine and scheduler ID required")
	}
	q, err := NewFairQueue(config.Queue)
	if err != nil {
		return nil, err
	}
	cap := config.Capacity
	meter, exists := engine.policy.policy.Meters[cap.MeterID]
	if len(config.Queue.Weights) > 32 || config.Queue.TenantID != engine.tenant || cap.TenantID != engine.tenant || cap.PoolID != config.Queue.PoolID || cap.MeterID != config.Queue.MeterID || !exists || meter.Unit == "money" || !queueID(cap.ID) || policyText(cap.SourceRef, false) != nil || !queueID(cap.Version) || cap.Rate <= 0 || cap.Burst <= 0 || cap.Interval <= 0 || !queueTime(cap.IssuedAt) || !queueTime(cap.ExpiresAt) || !cap.ExpiresAt.After(cap.IssuedAt) || policyMatchValid(cap.Target) != nil || cap.Target.Provider == "" || cap.Target.PoolID != cap.PoolID {
		return nil, fmt.Errorf("exact versioned pool capacity authority and non-money meter required")
	}
	config = policyCopy(config)
	if config.Protected == nil {
		config.Protected = map[string]ProtectedRate{}
	}
	if config.Priorities == nil {
		config.Priorities = map[string]int{}
	}
	shared := ProtectedRate{Rate: cap.Rate, Burst: cap.Burst}
	for flow, p := range config.Protected {
		if config.Queue.Weights[flow] == 0 || p.Rate <= 0 || p.Burst <= 0 || p.Rate > shared.Rate || p.Burst > shared.Burst {
			return nil, fmt.Errorf("protected admission partitions exceed verified capacity")
		}
		shared.Rate -= p.Rate
		shared.Burst -= p.Burst
	}
	if shared.Rate > 0 && shared.Burst == 0 {
		return nil, fmt.Errorf("remaining shared rate has no usable burst capacity")
	}
	for flow, priority := range config.Priorities {
		if config.Queue.Weights[flow] == 0 || priority < -1000000 || priority > 1000000 {
			return nil, fmt.Errorf("priority requires a configured flow and bounded value")
		}
	}
	accounts := engine.policy.Policy().Accounts
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	for i := range accounts {
		sort.Slice(accounts[i].Eligible, func(a, b int) bool {
			return quotaHash(accounts[i].Eligible[a]) < quotaHash(accounts[i].Eligible[b])
		})
	}
	shape := quotaHash(config.ID, config.Queue, config.Protected, config.Priorities, config.FlowAuthorityDigest, accounts,
		cap.TenantID, cap.PoolID, cap.MeterID, cap.Target, cap.Rate, cap.Burst, cap.Interval)
	return &AtomicScheduler{engine: engine, config: config, queue: q, digest: quotaHash(config), shape: shape, shared: shared}, nil
}

func (s *AtomicScheduler) Configuration() SchedulerConfig { return policyCopy(s.config) }
func (s *AtomicScheduler) Digest() string                 { return s.digest }
func (s *AtomicScheduler) current(now time.Time) error {
	c := s.config.Capacity
	if !queueTime(now) || now.Before(c.IssuedAt) || !now.Before(c.ExpiresAt) {
		return fmt.Errorf("scheduler capacity authority is not current: %w", ErrDenied)
	}
	return nil
}
func (s *AtomicScheduler) active(tx Tx) error {
	var head schedulerHead
	if err := tx.Get(schedulerHeadKind, s.config.ID, &head); err != nil {
		return err
	}
	if head.Digest != s.digest || head.Shape != s.shape || head.Policy != s.engine.policy.Reference() {
		return ErrConflict
	}
	var policy PolicyRef
	if err := tx.Get(policyHeadKind, "active", &policy); err != nil {
		return err
	}
	if policy != head.Policy {
		return fmt.Errorf("scheduler compiled policy is inactive: %w", ErrConflict)
	}
	return nil
}

// Activate initializes a pool, resumes its exact configuration, or follows a
// newer already-active quota policy without resetting queue or capacity state.
// expectedDigest is the previous scheduler configuration digest (not policy
// digest), required only to renew capacity evidence. Renewal must retain the
// exact queue, target, rates, bursts, priorities and partitions. Structural edits
// need an explicit future migration, never an implicit reset.
func (s *AtomicScheduler) Activate(ctx context.Context, expectedDigest ...string) error {
	if len(expectedDigest) > 1 || (len(expectedDigest) == 1 && expectedDigest[0] != "" && !queueDigest(expectedDigest[0])) {
		return fmt.Errorf("one previous scheduler digest is required")
	}
	expected := ""
	if len(expectedDigest) == 1 {
		expected = expectedDigest[0]
	}
	now := s.engine.clock().UTC()
	if err := s.current(now); err != nil {
		return err
	}
	return s.engine.store.Update(ctx, s.engine.tenant, func(tx Tx) error {
		var policy PolicyRef
		if err := tx.Get(policyHeadKind, "active", &policy); err != nil {
			return err
		}
		if policy != s.engine.policy.Reference() {
			return ErrConflict
		}
		if err := s.current(s.engine.clock().UTC()); err != nil {
			return err
		}
		wanted := schedulerHead{Digest: s.digest, Shape: s.shape, Policy: policy}
		var head schedulerHead
		if err := tx.Get(schedulerHeadKind, s.config.ID, &head); err == nil {
			if head == wanted {
				return s.active(tx)
			}
			if head.Shape != s.shape || head.Policy.ID != policy.ID || policy.Revision < head.Policy.Revision || (policy.Revision == head.Policy.Revision && head.Policy != policy) || (head.Digest != s.digest && head.Digest != expected) {
				return ErrConflict
			}
			var owner schedulerOwner
			if err := tx.Get(schedulerPoolKind, s.config.Queue.PoolID, &owner); err != nil {
				return err
			}
			if owner.SchedulerID != s.config.ID || owner.Digest != head.Digest {
				return ErrConflict
			}
			if err := tx.Put(schedulerPoolKind, s.config.Queue.PoolID, schedulerOwner{SchedulerID: s.config.ID, Digest: s.digest}); err != nil {
				return err
			}
			return tx.Put(schedulerHeadKind, s.config.ID, wanted)
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if expected != "" {
			return ErrConflict
		}
		var owner schedulerOwner
		if err := tx.Get(schedulerPoolKind, s.config.Queue.PoolID, &owner); err == nil {
			return fmt.Errorf("pool already belongs to a scheduler: %w", ErrConflict)
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := tx.Create(schedulerPoolKind, s.config.Queue.PoolID, schedulerOwner{SchedulerID: s.config.ID, Digest: s.digest}); err != nil {
			return err
		}
		if err := tx.Create(schedulerHeadKind, s.config.ID, wanted); err != nil {
			return err
		}
		return tx.Create(schedulerQueueKind, s.config.ID, schedulerQueueState{QueueState: s.queue.NewState(), Paused: map[string]bool{}})
	})
}
func (s *AtomicScheduler) Ready(ctx context.Context) error {
	return s.engine.store.View(ctx, s.engine.tenant, func(tx Tx) error {
		if err := s.active(tx); err != nil {
			return err
		}
		return s.current(s.engine.clock().UTC())
	})
}
func (s *AtomicScheduler) jobKey(id string) string { return quotaHash(s.config.ID, id) }
func (s *AtomicScheduler) loadJob(tx Tx, id string) (schedulerJob, error) {
	var job schedulerJob
	err := tx.Get(schedulerJobKind, s.jobKey(id), &job)
	return job, err
}

func (s *AtomicScheduler) validateJobOwner(tx Tx, job schedulerJob, id string) error {
	if job.Status.AttemptID != id || job.Demand.AttemptID != id {
		return ErrConflict
	}
	fingerprint, err := demandFingerprint(job.Demand)
	if err != nil || fingerprint != job.Status.DemandDigest {
		return ErrConflict
	}
	var owner schedulerOwner
	if err := tx.Get(schedulerOwnerKind, id, &owner); err != nil {
		return err
	}
	if owner.SchedulerID != s.config.ID || owner.Digest != fingerprint {
		return ErrConflict
	}
	return nil
}

// The atomic scheduler's durable jobs/ownership and selection records retain
// replay history independently of the bounded FairQueue active index. Compact
// only after verifying that durable proof, never by forgetting an attempt ID.
func (s *AtomicScheduler) compactQueueHistory(tx Tx, state *schedulerQueueState, limit int) error {
	compacted := 0
	for _, id := range sortedEngineKeys(state.Receipts) {
		receipt := state.Receipts[id]
		if receipt.State == "queued" {
			continue
		}
		job, err := s.loadJob(tx, id)
		if err != nil {
			return err
		}
		if err := s.validateJobOwner(tx, job, id); err != nil {
			return err
		}
		switch receipt.State {
		case "expired":
			if job.Status.State != "queued" && job.Status.State != "expired" {
				return ErrConflict
			}
			job.Status.State = "expired"
			if err := tx.Put(schedulerJobKind, s.jobKey(id), job); err != nil {
				return err
			}
		case "selected", "cancelled":
			if job.Status.State != "admitted" && job.Status.State != "denied" && job.Status.State != "cancelled" {
				return ErrConflict
			}
		default:
			return ErrConflict
		}
		delete(state.Receipts, id)
		compacted++
		if compacted >= limit {
			break
		}
	}
	return nil
}
func (s *AtomicScheduler) status(tx Tx, job schedulerJob, now time.Time) (ScheduledStatus, error) {
	status := job.Status
	if status.State == "queued" {
		var queue schedulerQueueState
		if err := tx.Get(schedulerQueueKind, s.config.ID, &queue); err != nil {
			return ScheduledStatus{}, err
		}
		now = s.engine.clock().UTC()
		if err := s.validateQueue(queue, now); err != nil {
			return ScheduledStatus{}, err
		}
		receipt, ok := queue.Receipts[status.AttemptID]
		if !ok {
			return ScheduledStatus{}, ErrConflict
		}
		if receipt.State == "selected" {
			return ScheduledStatus{}, ErrConflict
		}
		if receipt.State != "queued" {
			status.State = receipt.State
		} else if !status.Deadline.After(now) {
			status.State = "expired"
		}
	}
	return status, nil
}
func (s *AtomicScheduler) Get(ctx context.Context, id string) (ScheduledStatus, error) {
	if !queueID(id) {
		return ScheduledStatus{}, fmt.Errorf("attempt ID required")
	}
	now := s.engine.clock().UTC()
	var result ScheduledStatus
	err := s.engine.store.View(ctx, s.engine.tenant, func(tx Tx) error {
		job, err := s.loadJob(tx, id)
		if err != nil {
			return err
		}
		if err := s.validateJobOwner(tx, job, id); err != nil {
			return err
		}
		result, err = s.status(tx, job, now)
		return err
	})
	if err != nil {
		return ScheduledStatus{}, err
	}
	return policyCopy(result), nil
}

// Enqueue accepts only host-bound demand/flow authority. A caller-selected flow
// is not evidence of membership, priority or protected-allocation eligibility.
func (s *AtomicScheduler) Enqueue(ctx context.Context, request ScheduledRequest) (ScheduledStatus, error) {
	if err := ValidateDemand(request.Demand); err != nil {
		return ScheduledStatus{}, err
	}
	d := policyCopy(request.Demand)
	now := s.engine.clock().UTC()
	if d.Context.TenantID != s.engine.tenant || d.Target.PoolID != s.config.Queue.PoolID || !policyMatches(s.config.Capacity.Target, d.Target) {
		return ScheduledStatus{}, fmt.Errorf("demand is outside scheduler capacity authority")
	}
	digest, err := demandFingerprint(d)
	if err != nil {
		return ScheduledStatus{}, err
	}
	cost := d.Bounds[s.config.Queue.MeterID]
	qRequest := QueueRequest{ID: d.AttemptID, FlowID: request.FlowID, DemandDigest: digest, Cost: cost, Deadline: request.Deadline.UTC()}
	if err := s.queue.validateRequest(qRequest); err != nil {
		return ScheduledStatus{}, err
	}
	maximum := s.shared.Burst
	if p := s.config.Protected[request.FlowID]; p.Burst > maximum {
		maximum = p.Burst
	}
	if cost > s.config.Capacity.Burst || cost > maximum {
		return ScheduledStatus{}, fmt.Errorf("attempt cannot fit any configured admission partition")
	}
	var result ScheduledStatus
	err = s.engine.store.Update(ctx, s.engine.tenant, func(tx Tx) error {
		if job, err := s.loadJob(tx, d.AttemptID); err == nil {
			if err := s.validateJobOwner(tx, job, d.AttemptID); err != nil {
				return err
			}
			if job.Status.DemandDigest != digest || job.Status.FlowID != request.FlowID || !job.Status.Deadline.Equal(qRequest.Deadline) {
				return ErrConflict
			}
			result, err = s.status(tx, job, now)
			return err
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := s.current(now); err != nil {
			return err
		}
		if err := s.active(tx); err != nil {
			return err
		}
		var existing attemptRecord
		if err := tx.Get(attemptKind, d.AttemptID, &existing); err == nil {
			return ErrConflict
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		var owner schedulerOwner
		if err := tx.Get(schedulerOwnerKind, d.AttemptID, &owner); err == nil {
			return ErrConflict
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		var state schedulerQueueState
		if err := tx.Get(schedulerQueueKind, s.config.ID, &state); err != nil {
			return err
		}
		now := s.engine.clock().UTC()
		if err := s.current(now); err != nil {
			return err
		}
		if err := s.validateQueue(state, now); err != nil {
			return err
		}
		s.queue.expire(&state.QueueState, now)
		s.clearPausedIdle(&state)
		if err := s.compactQueueHistory(tx, &state, 4); err != nil {
			return err
		}
		next, err := s.queue.Enqueue(state.QueueState, qRequest, now)
		if err != nil {
			return err
		}
		result = ScheduledStatus{AttemptID: d.AttemptID, FlowID: request.FlowID, State: "queued", Deadline: qRequest.Deadline, EnqueuedAt: now, DemandDigest: digest}
		if err := tx.Create(schedulerOwnerKind, d.AttemptID, schedulerOwner{SchedulerID: s.config.ID, Digest: digest}); err != nil {
			return err
		}
		if err := tx.Create(schedulerJobKind, s.jobKey(d.AttemptID), schedulerJob{Status: result, Demand: d}); err != nil {
			return err
		}
		state.QueueState = next
		s.clearPausedIdle(&state)
		return tx.Put(schedulerQueueKind, s.config.ID, state)
	})
	if err != nil {
		return ScheduledStatus{}, err
	}
	return policyCopy(result), nil
}

// Cancel only removes queued work. It cannot cancel an admitted/claimed attempt;
// only a verified undispatched Engine.Abort can release that attempt's holds.
func (s *AtomicScheduler) Cancel(ctx context.Context, id string) (ScheduledStatus, error) {
	if !queueID(id) {
		return ScheduledStatus{}, fmt.Errorf("attempt ID required")
	}
	var result ScheduledStatus
	err := s.engine.store.Update(ctx, s.engine.tenant, func(tx Tx) error {
		job, err := s.loadJob(tx, id)
		if err != nil {
			return err
		}
		if err := s.validateJobOwner(tx, job, id); err != nil {
			return err
		}
		if job.Status.State != "queued" {
			result = job.Status
			return nil
		}
		var state schedulerQueueState
		if err := tx.Get(schedulerQueueKind, s.config.ID, &state); err != nil {
			return err
		}
		now := s.engine.clock().UTC()
		if err := s.validateQueue(state, now); err != nil {
			return err
		}
		next, _, err := s.queue.Cancel(state.QueueState, id, now)
		if err != nil {
			return err
		}
		receipt := next.Receipts[id]
		job.Status.State = receipt.State
		result = job.Status
		if err := tx.Put(schedulerJobKind, s.jobKey(id), job); err != nil {
			return err
		}
		state.QueueState = next
		delete(state.Receipts, id)
		s.clearPausedIdle(&state)
		return tx.Put(schedulerQueueKind, s.config.ID, state)
	})
	if err != nil {
		return ScheduledStatus{}, err
	}
	return policyCopy(result), nil
}

func (s *AtomicScheduler) limit(part string, p ProtectedRate, cost int64) RateLimit {
	return RateLimit{Key: quotaHash("scheduler-capacity", s.config.ID, part), Shape: quotaHash(s.shape, part), MeterID: s.config.Queue.MeterID, Rate: p.Rate, Burst: p.Burst, Interval: s.config.Capacity.Interval, Demand: cost}
}

type schedulerCapacity struct {
	flow      string
	part      string
	partition rateRecord
	pool      rateRecord
	priority  int
	protected bool
}

func (s *AtomicScheduler) eligible(tx Tx, state QueueState, now time.Time) (map[string]schedulerCapacity, error) {
	candidates := map[string]schedulerCapacity{}
	anyProtected := false
	highest := 0
	havePriority := false
	for _, flow := range s.queue.flows {
		index := queueHead(state.Items, flow)
		if index < 0 || !state.Items[index].Request.Deadline.After(now) {
			continue
		}
		cost := state.Items[index].Request.Cost
		pool, _, allowed, err := nextRate(tx, s.limit("pool", ProtectedRate{Rate: s.config.Capacity.Rate, Burst: s.config.Capacity.Burst}, cost), now)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		c := schedulerCapacity{flow: flow, pool: pool, priority: s.config.Priorities[flow]}
		if p, exists := s.config.Protected[flow]; exists {
			part, _, ok, err := nextRate(tx, s.limit("protected:"+flow, p, cost), now)
			if err != nil {
				return nil, err
			}
			if ok {
				c.part = "protected:" + flow
				c.partition = part
				c.protected = true
				anyProtected = true
			}
		}
		if c.part == "" && s.shared.Burst > 0 {
			part, _, ok, err := nextRate(tx, s.limit("shared", s.shared, cost), now)
			if err != nil {
				return nil, err
			}
			if ok {
				c.part = "shared"
				c.partition = part
			}
		}
		if c.part != "" {
			candidates[flow] = c
		}
	}
	// Protected eligible work precedes shared work. Priority applies inside that
	// class; weighted DRR allocates service among equal-priority eligible flows.
	for flow, c := range candidates {
		if anyProtected && !c.protected {
			delete(candidates, flow)
			continue
		}
		if !havePriority || c.priority > highest {
			highest = c.priority
			havePriority = true
		}
	}
	for flow, c := range candidates {
		if c.priority != highest {
			delete(candidates, flow)
		}
	}
	return candidates, nil
}

// Next atomically selects and admits at most one attempt. A successful nonempty
// selection is replayed by selectionID even if the original acknowledgement was
// lost. Empty/capacity-blocked polls commit no selection receipt and may be retried.
// Quota-denied attempts leave the queue without spending fairness or capacity.
func (s *AtomicScheduler) Next(ctx context.Context, selectionID string) (*ScheduledAdmission, error) {
	if !queueID(selectionID) {
		return nil, fmt.Errorf("selection ID required")
	}
	now := s.engine.clock().UTC()
	var result *ScheduledAdmission
	err := s.engine.store.Update(ctx, s.engine.tenant, func(tx Tx) error {
		result = nil
		var previous ScheduledAdmission
		if err := tx.Get(schedulerSelectionKind, s.jobKey(selectionID), &previous); err == nil {
			copy := policyCopy(previous)
			result = &copy
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := s.current(now); err != nil {
			return err
		}
		if err := s.active(tx); err != nil {
			return err
		}
		var state schedulerQueueState
		if err := tx.Get(schedulerQueueKind, s.config.ID, &state); err != nil {
			return err
		}
		now := s.engine.clock().UTC()
		if err := s.current(now); err != nil {
			return err
		}
		if err := s.validateQueue(state, now); err != nil {
			return err
		}
		s.queue.expire(&state.QueueState, now)
		s.clearPausedIdle(&state)
		eligible, err := s.eligible(tx, state.QueueState, now)
		if err != nil {
			return err
		}
		next, picked, err := s.pickEligible(state, eligible, now)
		if err != nil {
			return err
		}
		if picked == nil {
			return tx.Put(schedulerQueueKind, s.config.ID, next)
		}
		job, err := s.loadJob(tx, picked.ID)
		if err != nil {
			return err
		}
		if err := s.validateJobOwner(tx, job, picked.ID); err != nil {
			return err
		}
		if job.Status.State != "queued" || job.Status.DemandDigest != picked.DemandDigest {
			return ErrConflict
		}
		var existing attemptRecord
		if err := tx.Get(attemptKind, picked.ID, &existing); err == nil {
			return fmt.Errorf("queued attempt was admitted outside scheduler: %w", ErrConflict)
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		engine := *s.engine
		engine.store = transactionStore{tenant: s.engine.tenant, tx: tx}
		engine.clock = func() time.Time { return now }
		decision, err := engine.Admit(ctx, job.Demand)
		if err != nil {
			return err
		}
		job.Status.Decision = &decision
		if decision.Allowed {
			c := eligible[picked.FlowID]
			if err := tx.Put(rateKind, s.limit("pool", ProtectedRate{}, 0).Key, c.pool); err != nil {
				return err
			}
			if err := tx.Put(rateKind, s.limit(c.part, ProtectedRate{}, 0).Key, c.partition); err != nil {
				return err
			}
			job.Status.State = "admitted"
		} else {
			// Cancel from the original state: rejected demand earns/spends no service
			// credit and does not consume the pool's or a partition's rate capacity.
			cancelled, _, cancelErr := s.queue.Cancel(state.QueueState, picked.ID, now)
			err = cancelErr
			next = state
			next.QueueState = cancelled
			s.clearPausedIdle(&next)
			if err != nil {
				return err
			}
			job.Status.State = "denied"
		}
		delete(next.Receipts, picked.ID)
		if err := s.validateQueue(next, now); err != nil {
			return err
		}
		if err := tx.Put(schedulerQueueKind, s.config.ID, next); err != nil {
			return err
		}
		if err := tx.Put(schedulerJobKind, s.jobKey(picked.ID), job); err != nil {
			return err
		}
		value := ScheduledAdmission{Status: job.Status, Demand: job.Demand}
		if err := tx.Create(schedulerSelectionKind, s.jobKey(selectionID), value); err != nil {
			return err
		}
		copy := policyCopy(value)
		result = &copy
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// transactionStore only delegates to the already-open tenant transaction. It
// neither nests store calls nor commits data independently of its owner.
type transactionStore struct {
	tenant string
	tx     Tx
}

func (s transactionStore) Update(ctx context.Context, tenant string, fn func(Tx) error) error {
	if ctx == nil || ctx.Err() != nil {
		if ctx == nil {
			return fmt.Errorf("context required")
		}
		return ctx.Err()
	}
	if tenant != s.tenant || fn == nil {
		return ErrConflict
	}
	return fn(s.tx)
}
func (s transactionStore) View(ctx context.Context, tenant string, fn func(Tx) error) error {
	if fn == nil {
		return fmt.Errorf("transaction callback required")
	}
	return s.Update(ctx, tenant, func(tx Tx) error { return fn(schedulerReadOnlyTx{Tx: tx}) })
}

type schedulerReadOnlyTx struct{ Tx }

func (schedulerReadOnlyTx) Put(string, string, any) error    { return ErrReadOnly }
func (schedulerReadOnlyTx) Create(string, string, any) error { return ErrReadOnly }
func (schedulerReadOnlyTx) Delete(string, string) error      { return ErrReadOnly }

// pickEligible is DRR over the complete persisted state. Temporarily blocked
// flows retain their existing credit but earn no new credit while ineligible.
func (s *AtomicScheduler) pickEligible(state schedulerQueueState, eligible map[string]schedulerCapacity, now time.Time) (schedulerQueueState, *QueueRequest, error) {
	next := schedulerQueueState{QueueState: cloneQueueState(state.QueueState), Paused: map[string]bool{}}
	for flow, paused := range state.Paused {
		next.Paused[flow] = paused
	}
	s.queue.expire(&next.QueueState, now)
	s.clearPausedIdle(&next)
	if len(eligible) == 0 {
		finished, err := s.queue.finish(next.QueueState, now)
		next.QueueState = finished
		return next, nil, err
	}
	for {
		for visits := 0; visits < len(s.queue.flows); visits++ {
			flow := s.queue.flows[next.Cursor]
			index := queueHead(next.Items, flow)
			if index < 0 {
				next.Deficits[flow] = "0"
				s.queue.advance(&next.QueueState)
				continue
			}
			if _, ok := eligible[flow]; !ok {
				if next.Credited {
					next.Paused[flow] = true
				}
				s.queue.advance(&next.QueueState)
				continue
			}
			deficit, _ := new(big.Int).SetString(next.Deficits[flow], 10)
			if next.Paused[flow] {
				next.Credited = true
				delete(next.Paused, flow)
			}
			if !next.Credited {
				deficit.Add(deficit, s.queue.quanta[flow])
				next.Credited = true
			}
			cost := big.NewInt(next.Items[index].Request.Cost)
			if deficit.Cmp(cost) >= 0 {
				request := next.Items[index].Request
				next.Items = append(next.Items[:index], next.Items[index+1:]...)
				deficit.Sub(deficit, cost)
				next.Deficits[flow] = deficit.String()
				receipt := next.Receipts[request.ID]
				receipt.State = "selected"
				next.Receipts[request.ID] = receipt
				if queueHead(next.Items, flow) < 0 {
					next.Deficits[flow] = "0"
					s.queue.advance(&next.QueueState)
				}
				finished, err := s.queue.finish(next.QueueState, now)
				if err != nil {
					return schedulerQueueState{}, nil, err
				}
				next.QueueState = finished
				s.clearPausedIdle(&next)
				return next, &request, nil
			}
			next.Deficits[flow] = deficit.String()
			s.queue.advance(&next.QueueState)
		}
		var rounds *big.Int
		for _, flow := range s.queue.flows {
			if _, ok := eligible[flow]; !ok {
				continue
			}
			index := queueHead(next.Items, flow)
			if index < 0 {
				continue
			}
			deficit, _ := new(big.Int).SetString(next.Deficits[flow], 10)
			need := new(big.Int).Sub(big.NewInt(next.Items[index].Request.Cost), deficit)
			candidate := new(big.Int).Sub(s.queue.quanta[flow], big.NewInt(1))
			candidate.Add(candidate, need).Quo(candidate, s.queue.quanta[flow])
			if rounds == nil || candidate.Cmp(rounds) < 0 {
				rounds = candidate
			}
		}
		if rounds == nil || rounds.Sign() <= 0 {
			return schedulerQueueState{}, nil, fmt.Errorf("invalid eligible scheduling state")
		}
		rounds.Sub(rounds, big.NewInt(1))
		if rounds.Sign() > 0 {
			for _, flow := range s.queue.flows {
				if _, ok := eligible[flow]; !ok {
					continue
				}
				if queueHead(next.Items, flow) < 0 {
					continue
				}
				deficit, _ := new(big.Int).SetString(next.Deficits[flow], 10)
				deficit.Add(deficit, new(big.Int).Mul(rounds, s.queue.quanta[flow]))
				next.Deficits[flow] = deficit.String()
			}
		}
	}
}

var _ Store = transactionStore{}

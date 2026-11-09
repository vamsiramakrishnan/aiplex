package quotaplex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrQueueFull        = errors.New("quota queue full")
	ErrQueueHistoryFull = errors.New("quota queue replay history full")
	ErrQueueExpired     = errors.New("quota queue deadline expired")
)

// QueuePolicy defines one tenant/pool generation and one cost dimension. The
// scheduler allocates relative service; it does not establish provider capacity,
// admission rights, concurrency permits or minimum-throughput guarantees.
type QueuePolicy struct {
	TenantID    string            `json:"tenant_id"`
	PoolID      string            `json:"pool_id"`
	Generation  uint64            `json:"generation"`
	MeterID     string            `json:"meter_id"`
	MaxEntries  int               `json:"max_entries"`
	MaxReceipts int               `json:"max_receipts"`
	MaxWait     time.Duration     `json:"max_wait"`
	MaxCost     int64             `json:"max_cost"`
	Quantum     int64             `json:"quantum"`
	Weights     map[string]uint32 `json:"weights"`
}

// QueueRequest is an opaque attempt reference, not a provider payload. ID must
// retain its attempt identity across retries; DemandDigest binds the immutable
// typed demand retained by the host. Deadline is an absolute host-owned time.
type QueueRequest struct {
	ID           string    `json:"id"`
	FlowID       string    `json:"flow_id"`
	DemandDigest string    `json:"demand_digest"`
	Cost         int64     `json:"cost"`
	Deadline     time.Time `json:"deadline"`
}

type QueueItem struct {
	Request    QueueRequest `json:"request"`
	EnqueuedAt time.Time    `json:"enqueued_at"`
}

type QueueReceipt struct {
	Fingerprint string `json:"fingerprint"`
	State       string `json:"state"` // queued, selected, cancelled or expired
}

// QueueState is serializable transaction state. Every operation returns a
// separate snapshot. Persist it inside Store.Update to serialize competing
// schedulers. Selected requests must still pass atomic quota admission and an
// explicit dispatch claim; selecting is not a dispatch operation.
//
// Receipts are never silently evicted. When the bounded replay history fills,
// new IDs fail closed. Replacing a queue generation requires an explicit host
// migration retaining durable attempt deduplication; NewState is not a reset API.
type QueueState struct {
	Version      string                  `json:"version"`
	PolicyDigest string                  `json:"policy_digest"`
	Items        []QueueItem             `json:"items"`
	Receipts     map[string]QueueReceipt `json:"receipts"`
	Deficits     map[string]string       `json:"deficits"`
	Cursor       int                     `json:"cursor"`
	Credited     bool                    `json:"credited"`
	AsOf         time.Time               `json:"as_of"`
}

// FairQueue uses deficit round robin with exact integer credit. A visit adds
// Quantum*Weight; idle flows retain no credit. Large costs skip empty virtual
// rounds arithmetically, so work is bounded by flow count rather than token cost.
type FairQueue struct {
	policy QueuePolicy
	digest string
	flows  []string
	quanta map[string]*big.Int
}

func NewFairQueue(policy QueuePolicy) (*FairQueue, error) {
	if !queueID(policy.TenantID) || !queueID(policy.PoolID) || !queueID(policy.MeterID) || policy.Generation == 0 || policy.MaxEntries < 1 || policy.MaxEntries > 4096 || policy.MaxReceipts < policy.MaxEntries || policy.MaxReceipts > 16384 || policy.MaxWait <= 0 || policy.MaxCost <= 0 || policy.Quantum <= 0 || len(policy.Weights) == 0 || len(policy.Weights) > 256 {
		return nil, fmt.Errorf("invalid bounded queue policy")
	}
	policy.Weights = cloneQueueWeights(policy.Weights)
	q := &FairQueue{policy: policy, quanta: make(map[string]*big.Int)}
	for id, weight := range policy.Weights {
		if !queueID(id) || weight == 0 || weight > 1_000_000 {
			return nil, fmt.Errorf("queue flows require bounded IDs and positive weights")
		}
		q.flows = append(q.flows, id)
		q.quanta[id] = new(big.Int).Mul(big.NewInt(policy.Quantum), new(big.Int).SetUint64(uint64(weight)))
	}
	sort.Strings(q.flows)
	raw, _ := json.Marshal(policy)
	digest := sha256.Sum256(raw)
	q.digest = hex.EncodeToString(digest[:])
	return q, nil
}

func (q *FairQueue) NewState() QueueState {
	state := QueueState{Version: Version, PolicyDigest: q.digest, Items: []QueueItem{}, Receipts: map[string]QueueReceipt{}, Deficits: map[string]string{}}
	for _, flow := range q.flows {
		state.Deficits[flow] = "0"
	}
	return state
}

// Enqueue is idempotent even after selection, cancellation or expiry. Changed
// content under the same ID conflicts. Such replays do not gain a new position.
func (q *FairQueue) Enqueue(state QueueState, request QueueRequest, now time.Time) (QueueState, error) {
	if err := q.validate(state, now); err != nil {
		return QueueState{}, err
	}
	request.Deadline = request.Deadline.UTC()
	if err := q.validateRequest(request); err != nil {
		return QueueState{}, err
	}
	fingerprint := queueFingerprint(request)
	if receipt, ok := state.Receipts[request.ID]; ok {
		if receipt.Fingerprint != fingerprint {
			return QueueState{}, ErrConflict
		}
		next := cloneQueueState(state)
		q.expire(&next, now)
		return q.finish(next, now)
	}
	if !request.Deadline.After(now) {
		return QueueState{}, ErrQueueExpired
	}
	if request.Deadline.After(now.Add(q.policy.MaxWait)) {
		return QueueState{}, fmt.Errorf("queue deadline exceeds maximum wait")
	}
	next := cloneQueueState(state)
	q.expire(&next, now)
	if len(next.Items) >= q.policy.MaxEntries {
		return QueueState{}, ErrQueueFull
	}
	if len(next.Receipts) >= q.policy.MaxReceipts {
		return QueueState{}, ErrQueueHistoryFull
	}
	next.Items = append(next.Items, QueueItem{Request: request, EnqueuedAt: now.UTC()})
	next.Receipts[request.ID] = QueueReceipt{Fingerprint: fingerprint, State: "queued"}
	return q.finish(next, now)
}

// Pick selects one request and records that selection exactly once in this
// state. It makes no reservation or upstream call. An empty/expired queue returns
// a nil request. The caller must commit returned state before using the result.
func (q *FairQueue) Pick(state QueueState, now time.Time) (QueueState, *QueueRequest, error) {
	if err := q.validate(state, now); err != nil {
		return QueueState{}, nil, err
	}
	next := cloneQueueState(state)
	q.expire(&next, now)
	if len(next.Items) == 0 {
		finished, err := q.finish(next, now)
		return finished, nil, err
	}
	for {
		for visits := 0; visits < len(q.flows); visits++ {
			flow := q.flows[next.Cursor]
			index := queueHead(next.Items, flow)
			if index < 0 {
				next.Deficits[flow] = "0"
				q.advance(&next)
				continue
			}
			deficit, _ := new(big.Int).SetString(next.Deficits[flow], 10)
			if !next.Credited {
				deficit.Add(deficit, q.quanta[flow])
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
					q.advance(&next)
				}
				finished, err := q.finish(next, now)
				if err != nil {
					return QueueState{}, nil, err
				}
				return finished, &request, nil
			}
			next.Deficits[flow] = deficit.String()
			q.advance(&next)
		}
		// Every flow was visited without a selection. All active head costs
		// exceed their current deficits. Skip all complete rounds that cannot
		// select any head, then perform the next round in deterministic order.
		var rounds *big.Int
		for _, flow := range q.flows {
			index := queueHead(next.Items, flow)
			if index < 0 {
				continue
			}
			deficit, _ := new(big.Int).SetString(next.Deficits[flow], 10)
			need := new(big.Int).Sub(big.NewInt(next.Items[index].Request.Cost), deficit)
			candidate := new(big.Int).Sub(q.quanta[flow], big.NewInt(1))
			candidate.Add(candidate, need).Quo(candidate, q.quanta[flow])
			if rounds == nil || candidate.Cmp(rounds) < 0 {
				rounds = candidate
			}
		}
		if rounds == nil || rounds.Sign() <= 0 {
			return QueueState{}, nil, fmt.Errorf("invalid queue scheduling state")
		}
		rounds.Sub(rounds, big.NewInt(1))
		if rounds.Sign() > 0 {
			for _, flow := range q.flows {
				if queueHead(next.Items, flow) < 0 {
					continue
				}
				deficit, _ := new(big.Int).SetString(next.Deficits[flow], 10)
				deficit.Add(deficit, new(big.Int).Mul(rounds, q.quanta[flow]))
				next.Deficits[flow] = deficit.String()
			}
		}
	}
}

func (q *FairQueue) Cancel(state QueueState, id string, now time.Time) (QueueState, bool, error) {
	if err := q.validate(state, now); err != nil {
		return QueueState{}, false, err
	}
	if !queueID(id) {
		return QueueState{}, false, fmt.Errorf("queue attempt ID required")
	}
	next := cloneQueueState(state)
	q.expire(&next, now)
	cancelled := false
	for i, item := range next.Items {
		if item.Request.ID != id {
			continue
		}
		next.Items = append(next.Items[:i], next.Items[i+1:]...)
		receipt := next.Receipts[id]
		receipt.State = "cancelled"
		next.Receipts[id] = receipt
		cancelled = true
		break
	}
	q.clearIdle(&next)
	finished, err := q.finish(next, now)
	return finished, cancelled, err
}

func (q *FairQueue) validateRequest(request QueueRequest) error {
	if !queueID(request.ID) || !queueID(request.FlowID) || q.policy.Weights[request.FlowID] == 0 || request.Cost <= 0 || request.Cost > q.policy.MaxCost || !queueDigest(request.DemandDigest) || !queueTime(request.Deadline) {
		return fmt.Errorf("invalid typed queue request")
	}
	return nil
}

func (q *FairQueue) validate(state QueueState, now time.Time) error {
	if q == nil || !queueTime(now) || state.Version != Version || state.PolicyDigest != q.digest || state.Receipts == nil || state.Deficits == nil || len(state.Items) > q.policy.MaxEntries || len(state.Receipts) > q.policy.MaxReceipts || len(state.Deficits) != len(q.flows) || state.Cursor < 0 || state.Cursor >= len(q.flows) || (!state.AsOf.IsZero() && (!queueTime(state.AsOf) || now.Before(state.AsOf))) {
		return fmt.Errorf("invalid queue snapshot, policy or clock")
	}
	if state.Credited && queueHead(state.Items, q.flows[state.Cursor]) < 0 {
		return fmt.Errorf("idle flow cannot retain a credited visit")
	}
	for _, flow := range q.flows {
		text, exists := state.Deficits[flow]
		if !exists || len(text) > 64 || (len(text) > 1 && text[0] == '0') || text == "" || strings.Trim(text, "0123456789") != "" {
			return fmt.Errorf("invalid queue deficit")
		}
		deficit, _ := new(big.Int).SetString(text, 10)
		bound := new(big.Int).Add(big.NewInt(q.policy.MaxCost), q.quanta[flow])
		if deficit.Cmp(bound) >= 0 || (queueHead(state.Items, flow) < 0 && deficit.Sign() != 0) {
			return fmt.Errorf("queue deficit exceeds bounded service credit")
		}
	}
	for id, receipt := range state.Receipts {
		if !queueID(id) || !queueDigest(receipt.Fingerprint) || (receipt.State != "queued" && receipt.State != "selected" && receipt.State != "cancelled" && receipt.State != "expired") {
			return fmt.Errorf("invalid queue replay receipt")
		}
	}
	seen := map[string]bool{}
	for _, item := range state.Items {
		receipt, exists := state.Receipts[item.Request.ID]
		if q.validateRequest(item.Request) != nil || !queueTime(item.EnqueuedAt) || item.EnqueuedAt.After(now) || state.AsOf.IsZero() || item.EnqueuedAt.After(state.AsOf) || !item.Request.Deadline.After(item.EnqueuedAt) || item.Request.Deadline.After(item.EnqueuedAt.Add(q.policy.MaxWait)) || seen[item.Request.ID] || !exists || receipt.State != "queued" || receipt.Fingerprint != queueFingerprint(item.Request) {
			return fmt.Errorf("invalid queued attempt or replay binding")
		}
		seen[item.Request.ID] = true
	}
	for id, receipt := range state.Receipts {
		if receipt.State == "queued" && !seen[id] {
			return fmt.Errorf("queued replay receipt has no request")
		}
	}
	if _, err := EncodeStoreValue(state); err != nil {
		return err
	}
	return nil
}

func (q *FairQueue) finish(state QueueState, now time.Time) (QueueState, error) {
	state.AsOf = now.UTC()
	if err := q.validate(state, now); err != nil {
		return QueueState{}, err
	}
	return state, nil
}

func (q *FairQueue) expire(state *QueueState, now time.Time) {
	kept := state.Items[:0]
	for _, item := range state.Items {
		if !item.Request.Deadline.After(now) {
			receipt := state.Receipts[item.Request.ID]
			receipt.State = "expired"
			state.Receipts[item.Request.ID] = receipt
			continue
		}
		kept = append(kept, item)
	}
	state.Items = kept
	q.clearIdle(state)
}

func (q *FairQueue) clearIdle(state *QueueState) {
	for _, flow := range q.flows {
		if queueHead(state.Items, flow) < 0 {
			state.Deficits[flow] = "0"
			if q.flows[state.Cursor] == flow {
				state.Credited = false
			}
		}
	}
}

func (q *FairQueue) advance(state *QueueState) {
	state.Cursor = (state.Cursor + 1) % len(q.flows)
	state.Credited = false
}

func cloneQueueState(state QueueState) QueueState {
	copy := state
	copy.Items = append([]QueueItem{}, state.Items...)
	copy.Receipts = make(map[string]QueueReceipt, len(state.Receipts))
	for id, receipt := range state.Receipts {
		copy.Receipts[id] = receipt
	}
	copy.Deficits = make(map[string]string, len(state.Deficits))
	for flow, deficit := range state.Deficits {
		copy.Deficits[flow] = deficit
	}
	return copy
}

func cloneQueueWeights(weights map[string]uint32) map[string]uint32 {
	copy := make(map[string]uint32, len(weights))
	for flow, weight := range weights {
		copy[flow] = weight
	}
	return copy
}

func queueHead(items []QueueItem, flow string) int {
	for i, item := range items {
		if item.Request.FlowID == flow {
			return i
		}
	}
	return -1
}

func queueFingerprint(request QueueRequest) string {
	request.Deadline = request.Deadline.UTC()
	raw, _ := json.Marshal(request)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func queueDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func queueID(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, c := range value {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func queueTime(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 1 && value.Year() <= 9999
}

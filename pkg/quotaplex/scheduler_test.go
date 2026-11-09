package quotaplex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var queueTestNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func queueFixture(t *testing.T, weights map[string]uint32) *FairQueue {
	t.Helper()
	q, err := NewFairQueue(QueuePolicy{TenantID: "tenant", PoolID: "capacity", Generation: 1, MeterID: "tool.rows", MaxEntries: 300, MaxReceipts: 600, MaxWait: time.Hour, MaxCost: math.MaxInt64, Quantum: 1, Weights: weights})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func queueRequest(id, flow string, cost int64) QueueRequest {
	return QueueRequest{ID: id, FlowID: flow, Cost: cost, DemandDigest: strings.Repeat("a", 64), Deadline: queueTestNow.Add(time.Minute)}
}

func queueEnqueue(t *testing.T, q *FairQueue, state QueueState, request QueueRequest) QueueState {
	t.Helper()
	next, err := q.Enqueue(state, request, queueTestNow)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func queuePick(t *testing.T, q *FairQueue, state QueueState) (QueueState, *QueueRequest) {
	t.Helper()
	next, request, err := q.Pick(state, queueTestNow)
	if err != nil {
		t.Fatal(err)
	}
	return next, request
}

func TestFairQueueUsesWeightsForServiceRatherThanRequestCount(t *testing.T) {
	t.Run("equal work", func(t *testing.T) {
		q := queueFixture(t, map[string]uint32{"a": 3, "b": 1})
		state := q.NewState()
		for i := 0; i < 80; i++ {
			state = queueEnqueue(t, q, state, queueRequest(fmt.Sprintf("a-%d", i), "a", 1))
			state = queueEnqueue(t, q, state, queueRequest(fmt.Sprintf("b-%d", i), "b", 1))
		}
		counts := map[string]int{}
		for i := 0; i < 80; i++ {
			var selected *QueueRequest
			state, selected = queuePick(t, q, state)
			counts[selected.FlowID]++
		}
		if counts["a"] != 60 || counts["b"] != 20 {
			t.Fatalf("3:1 flow service became %v", counts)
		}
	})
	t.Run("different work per request", func(t *testing.T) {
		q := queueFixture(t, map[string]uint32{"a": 3, "b": 1})
		state := q.NewState()
		for i := 0; i < 30; i++ {
			state = queueEnqueue(t, q, state, queueRequest(fmt.Sprintf("a-%d", i), "a", 3))
			state = queueEnqueue(t, q, state, queueRequest(fmt.Sprintf("b-%d", i), "b", 1))
		}
		served := map[string]int64{}
		for i := 0; i < 40; i++ {
			var selected *QueueRequest
			state, selected = queuePick(t, q, state)
			served[selected.FlowID] += selected.Cost
		}
		if served["a"] != 60 || served["b"] != 20 {
			t.Fatalf("weighted row service became %v", served)
		}
	})
}

func TestFairQueueSkipsHugeEmptyRoundsAndUsesExactCredits(t *testing.T) {
	q := queueFixture(t, map[string]uint32{"a": 1})
	state := queueEnqueue(t, q, q.NewState(), queueRequest("huge", "a", math.MaxInt64))
	start := time.Now()
	next, selected := queuePick(t, q, state)
	if selected == nil || selected.ID != "huge" || len(next.Items) != 0 || next.Deficits["a"] != "0" || time.Since(start) > time.Second {
		t.Fatalf("huge cost was not bounded work: selected=%v deficit=%s", selected, next.Deficits["a"])
	}
	policy := q.policy
	policy.Quantum = math.MaxInt64
	policy.Weights = map[string]uint32{"a": 1_000_000}
	q, err := NewFairQueue(policy)
	if err != nil {
		t.Fatal(err)
	}
	state = queueEnqueue(t, q, q.NewState(), queueRequest("first", "a", 1))
	state = queueEnqueue(t, q, state, queueRequest("second", "a", 1))
	next, selected = queuePick(t, q, state)
	deficit, ok := new(big.Int).SetString(next.Deficits["a"], 10)
	if selected.ID != "first" || !ok || deficit.IsInt64() {
		t.Fatalf("large exact credit was truncated: %s", next.Deficits["a"])
	}
}

func TestFairQueueFastRoundsMatchPacketRoundRobin(t *testing.T) {
	q := queueFixture(t, map[string]uint32{"a": 7, "b": 3, "c": 1})
	state := q.NewState()
	remaining := map[string][]QueueRequest{"a": {}, "b": {}, "c": {}}
	random := rand.New(rand.NewSource(18))
	for i := 0; i < 90; i++ {
		flow := q.flows[i%len(q.flows)]
		request := queueRequest(fmt.Sprintf("attempt-%d", i), flow, int64(1+random.Intn(200)))
		remaining[flow] = append(remaining[flow], request)
		state = queueEnqueue(t, q, state, request)
	}
	credits := map[string]int64{}
	cursor := 0
	credited := false
	for i := 0; i < 90; i++ {
		var expected QueueRequest
		for {
			flow := q.flows[cursor]
			if len(remaining[flow]) == 0 {
				credits[flow] = 0
				cursor = (cursor + 1) % len(q.flows)
				credited = false
				continue
			}
			if !credited {
				credits[flow] += int64(q.policy.Weights[flow])
				credited = true
			}
			if credits[flow] >= remaining[flow][0].Cost {
				expected = remaining[flow][0]
				remaining[flow] = remaining[flow][1:]
				credits[flow] -= expected.Cost
				if len(remaining[flow]) == 0 {
					credits[flow] = 0
					cursor = (cursor + 1) % len(q.flows)
					credited = false
				}
				break
			}
			cursor = (cursor + 1) % len(q.flows)
			credited = false
		}
		var selected *QueueRequest
		state, selected = queuePick(t, q, state)
		if selected.ID != expected.ID {
			t.Fatalf("round skipping changed selection %d: got %s want %s", i, selected.ID, expected.ID)
		}
	}
}

func TestFairQueueDeadlineCancellationAndDurableReplayBounds(t *testing.T) {
	q := queueFixture(t, map[string]uint32{"a": 1})
	policy := q.policy
	policy.MaxEntries, policy.MaxReceipts = 1, 2
	q, _ = NewFairQueue(policy)
	first := queueRequest("one", "a", 1)
	state := queueEnqueue(t, q, q.NewState(), first)
	if _, err := q.Enqueue(state, queueRequest("other", "a", 1), queueTestNow); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("queue exceeded its bound: %v", err)
	}
	var selected *QueueRequest
	var err error
	state, selected, err = q.Pick(state, first.Deadline)
	if err != nil || selected != nil || state.Receipts[first.ID].State != "expired" {
		t.Fatalf("deadline boundary was not terminal: %+v %v", selected, err)
	}
	state, err = q.Enqueue(state, first, first.Deadline)
	if err != nil || len(state.Items) != 0 {
		t.Fatalf("expired replay created work: %v", err)
	}
	changed := first
	changed.Cost++
	if _, err := q.Enqueue(state, changed, first.Deadline); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed expired replay was accepted: %v", err)
	}
	second := queueRequest("two", "a", 1)
	second.Deadline = first.Deadline.Add(time.Minute)
	state, err = q.Enqueue(state, second, first.Deadline)
	if err != nil {
		t.Fatal(err)
	}
	var cancelled bool
	state, cancelled, err = q.Cancel(state, second.ID, first.Deadline)
	if err != nil || !cancelled || state.Receipts[second.ID].State != "cancelled" {
		t.Fatalf("cancel failed: %v", err)
	}
	state, cancelled, err = q.Cancel(state, second.ID, first.Deadline)
	if err != nil || cancelled {
		t.Fatal("cancel was not idempotent", err)
	}
	third := second
	third.ID = "three"
	if _, err := q.Enqueue(state, third, first.Deadline); !errors.Is(err, ErrQueueHistoryFull) {
		t.Fatalf("full history silently evicted replay protection: %v", err)
	}
	state, err = q.Enqueue(state, second, first.Deadline)
	if err != nil || len(state.Items) != 0 {
		t.Fatalf("cancelled replay created work: %v", err)
	}
}

func TestFairQueueSelectionReplaySnapshotsAndNoIdleCredit(t *testing.T) {
	weights := map[string]uint32{"a": 100, "b": 1}
	q := queueFixture(t, weights)
	weights["a"] = 0
	request := queueRequest("one", "a", 1)
	initial := q.NewState()
	state := queueEnqueue(t, q, initial, request)
	if len(initial.Receipts) != 0 || len(initial.Items) != 0 {
		t.Fatal("enqueue mutated prior snapshot")
	}
	state = queueEnqueue(t, q, state, queueRequest("other", "b", 1))
	before, _ := json.Marshal(state)
	next, selected := queuePick(t, q, state)
	if selected.ID != request.ID || next.Deficits["a"] != "0" {
		t.Fatalf("idle flow retained borrowed credit: %+v", next)
	}
	after, _ := json.Marshal(state)
	if string(before) != string(after) {
		t.Fatal("selection mutated prior snapshot")
	}
	selected.ID = "caller mutation"
	next.Items[0].Request.ID = "caller mutation"
	next.Receipts[request.ID] = QueueReceipt{}
	if state.Items[0].Request.ID != request.ID || state.Receipts[request.ID].State != "queued" {
		t.Fatal("returned data aliases prior snapshot")
	}
	clean, _ := queuePick(t, q, state)
	clean, err := q.Enqueue(clean, request, queueTestNow)
	if err != nil || len(clean.Items) != 1 || clean.Receipts[request.ID].State != "selected" {
		t.Fatalf("selected replay gained a position: %v", err)
	}
	raw, _ := json.Marshal(clean)
	var restored QueueState
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	one, first := queuePick(t, q, clean)
	two, second := queuePick(t, q, restored)
	if !reflect.DeepEqual(one, two) || !reflect.DeepEqual(first, second) {
		t.Fatal("serialized state changed service order")
	}
}

func TestFairQueueRejectsForeignPolicyCorruptStateAndClockRewind(t *testing.T) {
	q := queueFixture(t, map[string]uint32{"a": 1})
	state := queueEnqueue(t, q, q.NewState(), queueRequest("one", "a", 1))
	policy := q.policy
	policy.Generation++
	other, _ := NewFairQueue(policy)
	if _, _, err := other.Pick(state, queueTestNow); err == nil {
		t.Fatal("foreign generation accepted existing queue")
	}
	if _, _, err := q.Pick(state, queueTestNow.Add(-time.Nanosecond)); err == nil {
		t.Fatal("rewound clock was accepted")
	}
	corrupt := cloneQueueState(state)
	corrupt.Deficits["a"] = strings.Repeat("9", 1000)
	if _, _, err := q.Pick(corrupt, queueTestNow); err == nil {
		t.Fatal("unbounded serialized deficit accepted")
	}
	corrupt = cloneQueueState(state)
	delete(corrupt.Receipts, "one")
	if _, _, err := q.Pick(corrupt, queueTestNow); err == nil {
		t.Fatal("request without replay binding accepted")
	}
	invalid := queueRequest("zero", "a", 0)
	if _, err := q.Enqueue(state, invalid, queueTestNow); err == nil {
		t.Fatal("zero-cost work accepted")
	}
}

func TestFairQueueStateSerializesCompetingSchedulers(t *testing.T) {
	q := queueFixture(t, map[string]uint32{"a": 3, "b": 1})
	state := q.NewState()
	for i := 0; i < 32; i++ {
		state = queueEnqueue(t, q, state, queueRequest(fmt.Sprint(i), q.flows[i%2], 1))
	}
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.Update(ctx, "tenant", func(tx Tx) error { return tx.Create("queue", "capacity", state) }); err != nil {
		t.Fatal(err)
	}
	results := make(chan string, 32)
	failures := make(chan error, 32)
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			var selected *QueueRequest
			err := store.Update(ctx, "tenant", func(tx Tx) error {
				var current QueueState
				if err := tx.Get("queue", "capacity", &current); err != nil {
					return err
				}
				var next QueueState
				var err error
				next, selected, err = q.Pick(current, queueTestNow)
				if err != nil {
					return err
				}
				return tx.Put("queue", "capacity", next)
			})
			if err != nil || selected == nil {
				failures <- fmt.Errorf("selection=%v error=%v", selected, err)
				return
			}
			results <- selected.ID
		}()
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	seen := map[string]bool{}
	for id := range results {
		if seen[id] {
			t.Fatalf("attempt %s selected twice", id)
		}
		seen[id] = true
	}
	if len(seen) != 32 {
		t.Fatalf("lost selections: %d", len(seen))
	}
}

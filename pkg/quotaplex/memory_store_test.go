package quotaplex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryTransactionsConserveCapacityAcrossConcurrentEngines(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.Update(ctx, "tenant", func(tx Tx) error { return tx.Create("balances", "shared", 20) }); err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			granted := false
			err := store.Update(ctx, "tenant", func(tx Tx) error {
				var available int
				if err := tx.Get("balances", "shared", &available); err != nil {
					return err
				}
				if available == 0 {
					return nil
				}
				if err := tx.Put("balances", "shared", available-1); err != nil {
					return err
				}
				if err := tx.Create("ledger", fmt.Sprint(i), 1); err != nil {
					return err
				}
				granted = true
				return nil
			})
			if err != nil {
				t.Error(err)
			} else if granted {
				accepted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 20 {
		t.Fatalf("accepted=%d", accepted.Load())
	}
	if err := store.View(ctx, "tenant", func(tx Tx) error {
		var available int
		if err := tx.Get("balances", "shared", &available); err != nil {
			return err
		}
		ledger, err := tx.List("ledger")
		if err != nil {
			return err
		}
		if available != 0 || len(ledger) != 20 {
			t.Fatalf("available=%d entries=%d", available, len(ledger))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryTransactionRollbackReadOnlyAndMutationErrors(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.Update(ctx, "t", func(tx Tx) error { return tx.Create("balances", "one", 10) }); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("later constraint failed")
	if err := store.Update(ctx, "t", func(tx Tx) error {
		if err := tx.Put("balances", "one", 0); err != nil {
			return err
		}
		return failure
	}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := store.View(ctx, "t", func(tx Tx) error { _ = tx.Put("balances", "one", 0); return nil }); !errors.Is(err, ErrReadOnly) {
		t.Fatal("discarded read-only error committed", err)
	}
	if err := store.Update(ctx, "t", func(tx Tx) error {
		_ = tx.Put("balances", "one", 0)
		_ = tx.Create("balances", "one", 20)
		return nil
	}); !errors.Is(err, ErrConflict) {
		t.Fatal("discarded collision committed earlier writes", err)
	}
	if err := store.View(ctx, "t", func(tx Tx) error {
		var value int
		if err := tx.Get("balances", "one", &value); err != nil {
			return err
		}
		if value != 10 {
			t.Fatalf("rollback value=%d", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryTransactionsSnapshotValuesAndCloseHandles(t *testing.T) {
	var store MemoryStore // zero value is usable
	ctx := context.Background()
	input := map[string][]int{"values": {1}}
	var escaped Tx
	if err := store.Update(ctx, "tenant/slash", func(tx Tx) error {
		escaped = tx
		if err := tx.Create("kind/slash", "record/slash", input); err != nil {
			return err
		}
		input["values"][0] = 99
		var inside map[string][]int
		if err := tx.Get("kind/slash", "record/slash", &inside); err != nil {
			return err
		}
		if inside["values"][0] != 1 {
			t.Fatal("write retained caller aliases")
		}
		inside["values"][0] = 88
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := escaped.Put("kind/slash", "record/slash", 2); !errors.Is(err, ErrTransactionClosed) {
		t.Fatal("escaped transaction accepted", err)
	}
	if err := store.View(ctx, "tenant/slash", func(tx Tx) error {
		result := map[string][]int{"stale": {5}}
		if err := tx.Get("kind/slash", "record/slash", &result); err != nil {
			return err
		}
		if result["values"][0] != 1 || result["stale"] != nil {
			t.Fatal("read is not a replacing snapshot", result)
		}
		records, err := tx.List("kind/slash")
		if err != nil {
			return err
		}
		for i := range records[0].Value {
			records[0].Value[i] = 'x'
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.View(ctx, "tenant/slash", func(tx Tx) error {
		var result map[string][]int
		return tx.Get("kind/slash", "record/slash", &result)
	}); err != nil {
		t.Fatal("List leaked persisted bytes", err)
	}
	if err := store.View(ctx, "tenant", func(tx Tx) error { var value any; return tx.Get("slash/kind", "slash/record/slash", &value) }); !errors.Is(err, ErrNotFound) {
		t.Fatal("tenant namespace alias", err)
	}
}

func TestMemoryTransactionCancellationRollsBackAndCancelsLockWait(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	err := store.Update(ctx, "t", func(tx Tx) error {
		if err := tx.Put("balances", "one", 1); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- store.Update(context.Background(), "t", func(Tx) error { close(entered); <-release; return nil })
	}()
	<-entered
	waitCtx, waitCancel := context.WithCancel(context.Background())
	waiting := make(chan error, 1)
	go func() {
		waiting <- store.View(waitCtx, "t", func(Tx) error { t.Error("canceled lock waiter entered callback"); return nil })
	}()
	waitCancel()
	select {
	case err := <-waiting:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Error("lock wait ignored cancellation")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := store.View(context.Background(), "t", func(tx Tx) error { var value int; return tx.Get("balances", "one", &value) }); !errors.Is(err, ErrNotFound) {
		t.Fatal("canceled transaction committed", err)
	}
}

func TestMemoryTransactionBoundsAreAtomicAndListNeverTruncates(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.Update(ctx, "t", func(tx Tx) error {
		for i := 0; i <= MaxTransactionRecords; i++ {
			if err := tx.Put("many", fmt.Sprintf("%03d", i), i); err != nil {
				return err
			}
		}
		return nil
	}); !errors.Is(err, ErrTransactionLimit) {
		t.Fatal(err)
	}
	if err := store.View(ctx, "t", func(tx Tx) error {
		values, err := tx.List("many")
		if len(values) != 0 {
			t.Fatal("bounded rollback committed partial values")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= MaxTransactionRecords; i++ {
		if err := store.Update(ctx, "t", func(tx Tx) error { return tx.Put("many", fmt.Sprintf("%03d", i), i) }); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.View(ctx, "t", func(tx Tx) error { _, err := tx.List("many"); return err }); !errors.Is(err, ErrTransactionLimit) {
		t.Fatal("List silently truncated", err)
	}
	var count int
	after := ""
	for {
		var records []Record
		if err := store.View(ctx, "t", func(tx Tx) error { var err error; records, err = tx.Scan("many", after, 64); return err }); err != nil {
			t.Fatal(err)
		}
		if len(records) == 0 {
			break
		}
		for _, record := range records {
			if record.ID <= after {
				t.Fatal("duplicate/unordered scan")
			}
			after = record.ID
			count++
		}
	}
	if count != MaxTransactionRecords+1 {
		t.Fatalf("paged recovery lost records: %d", count)
	}
	if err := store.Update(ctx, "t", func(tx Tx) error { return tx.Put("large", "one", strings.Repeat("a", MaxRecordBytes)) }); !errors.Is(err, ErrTransactionLimit) {
		t.Fatal("oversize value accepted", err)
	}
	bad := json.RawMessage(`{"broken":`)
	if err := store.Update(ctx, "t", func(tx Tx) error { return tx.Put("large", "bad", bad) }); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestMemoryTransactionByteBoundIncludesOriginalReadsAndFinalWrites(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	value := strings.Repeat("a", MaxRecordBytes-2)
	for i := 0; i < 17; i++ {
		if err := store.Update(ctx, "t", func(tx Tx) error { return tx.Create("large", fmt.Sprint(i), value) }); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.View(ctx, "t", func(tx Tx) error { _, err := tx.List("large"); return err }); !errors.Is(err, ErrTransactionLimit) {
		t.Fatal("read-only bytes not bounded", err)
	}
	if err := store.View(ctx, "t", func(tx Tx) error {
		keys := make([]Key, 17)
		for i := range keys {
			keys[i] = Key{Kind: "large", ID: fmt.Sprint(i)}
		}
		return tx.Prefetch(keys)
	}); !errors.Is(err, ErrTransactionLimit) {
		t.Fatal("prefetch original read bytes not bounded", err)
	}
	if err := store.Update(ctx, "t", func(tx Tx) error {
		for i := 0; i < 9; i++ {
			if err := tx.Put("large", fmt.Sprint(i), value); err != nil {
				return err
			}
		}
		return nil
	}); !errors.Is(err, ErrTransactionLimit) {
		t.Fatal("old reads plus new writes not bounded", err)
	}
}

package registry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

func TestDelegationSnapshotsAndUpdatedList(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	d := models.Delegation{ID: "delegation", Status: "pending", StartedAt: time.Now()}
	if e := s.AppendDelegation(ctx, &d); e != nil {
		t.Fatal(e)
	}
	stored, e := s.GetDelegation(ctx, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	stored.Status = "completed"
	at := time.Now()
	stored.CompletedAt = &at
	if e = s.UpdateDelegation(ctx, stored); e != nil {
		t.Fatal(e)
	}
	list, e := s.ListDelegations(ctx, "", 10)
	if e != nil || len(list) != 1 || list[0].Status != "completed" || list[0].CompletedAt == nil {
		t.Fatalf("updated map and list disagree: %+v %v", list, e)
	}
	stored.Error = "mutated outside store"
	if current, _ := s.GetDelegation(ctx, d.ID); current.Error != "" {
		t.Fatal("caller mutated persisted delegation through returned pointer")
	}
	*list[0].CompletedAt = list[0].CompletedAt.Add(time.Hour)
	if current, _ := s.GetDelegation(ctx, d.ID); !current.CompletedAt.Equal(at) {
		t.Fatal("list completion pointer aliases persisted state")
	}
	if e = s.AppendDelegation(ctx, &d); e == nil {
		t.Fatal("duplicate append overwrote recorded delegation")
	}
	if e = s.UpdateDelegation(ctx, &models.Delegation{ID: "absent"}); !errors.Is(e, ErrNotFound) {
		t.Fatal("update created missing delegation")
	}
}

func TestConcurrentDelegationCompletionIsSingleTransition(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	d := models.Delegation{ID: "concurrent", Status: "pending", StartedAt: time.Now()}
	_ = s.AppendDelegation(ctx, &d)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var completed atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := d
			at := time.Now()
			local.Status = "completed"
			local.CompletedAt = &at
			<-start
			if e := s.UpdateDelegation(ctx, &local); e == nil {
				completed.Add(1)
			} else if !errors.Is(e, ErrConflict) {
				t.Errorf("unexpected transition error: %v", e)
			}
		}()
	}
	close(start)
	wg.Wait()
	if completed.Load() != 1 {
		t.Fatalf("completion applied %d times", completed.Load())
	}
}

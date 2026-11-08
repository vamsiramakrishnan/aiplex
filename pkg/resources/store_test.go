package resources

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestTenantCASAndCopies(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	r := Record{TenantID: "a", Kind: "connection", ID: "same", Spec: json.RawMessage(`{}`)}
	got, e := s.Put(ctx, r, 0)
	if e != nil {
		t.Fatal(e)
	}
	got.Spec[0] = 'x'
	if _, e = s.Get(ctx, "b", "connection", "same"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.Put(ctx, r, 1)
			if e == nil {
				wins.Add(1)
			} else if !errors.Is(e, ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal(wins.Load())
	}
	got, e = s.Get(ctx, "a", "connection", "same")
	if e != nil || string(got.Spec) != "{}" {
		t.Fatal(e, string(got.Spec))
	}
}

func TestDeleteRecreateNeverReusesRevision(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	r := Record{TenantID: "tenant", Kind: "deployment-intent", ID: "intent", Spec: json.RawMessage(`{"inputs":{"version":"old"}}`)}
	first, e := s.Put(ctx, r, 0)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Delete(ctx, r.TenantID, r.Kind, r.ID, first.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, r.TenantID, r.Kind, r.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("deleted resource remained visible", e)
	}
	if listed, e := s.List(ctx, r.TenantID, r.Kind); e != nil || len(listed) != 0 {
		t.Fatal("tombstone was listed", listed, e)
	}
	if _, e = s.Put(ctx, r, first.Revision); !errors.Is(e, ErrConflict) {
		t.Fatal("stale writer resurrected deleted resource", e)
	}
	r.Spec = json.RawMessage(`{"inputs":{"version":"new"}}`)
	recreated, e := s.Put(ctx, r, 0)
	if e != nil || recreated.Revision <= first.Revision {
		t.Fatal("recreation reused old revision", recreated, e)
	}
	if _, e = s.Put(ctx, r, first.Revision); !errors.Is(e, ErrConflict) {
		t.Fatal("old ETag overwrote recreated resource", e)
	}
	if e = s.Delete(ctx, r.TenantID, r.Kind, r.ID, first.Revision); !errors.Is(e, ErrConflict) {
		t.Fatal("old ETag deleted recreated resource", e)
	}
	if e = s.Delete(ctx, r.TenantID, r.Kind, r.ID, recreated.Revision); e != nil {
		t.Fatal(e)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.Put(ctx, r, 0); e == nil {
				wins.Add(1)
			} else if !errors.Is(e, ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("concurrent recreation had %d successful creates", wins.Load())
	}
	final, e := s.Get(ctx, r.TenantID, r.Kind, r.ID)
	if e != nil || final.Revision <= recreated.Revision {
		t.Fatal("later incarnation reused revision", final, e)
	}
}

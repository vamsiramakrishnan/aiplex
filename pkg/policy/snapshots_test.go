package policy

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSnapshotsImmutableAndTenantIsolated(t *testing.T) {
	s := NewMemoryStore()
	doc := json.RawMessage(`{"allow":true}`)
	v, e := s.Publish("a", "p", 0, doc)
	if e != nil {
		t.Fatal(e)
	}
	doc[0] = 'x'
	v.Document[0] = 'x'
	got, e := s.Get("a", "p", 1)
	if e != nil || string(got.Document) != `{"allow":true}` {
		t.Fatalf("mutated snapshot: %+v %v", got, e)
	}
	if _, e = s.Get("b", "p", 1); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.Publish("a", "p", 0, json.RawMessage(`{}`)); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
func TestConcurrentPublishCAS(t *testing.T) {
	s := NewMemoryStore()
	var n atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.Publish("t", "p", 0, json.RawMessage(`{}`))
			if e == nil {
				n.Add(1)
			} else if !errors.Is(e, ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Fatal(n.Load())
	}
}

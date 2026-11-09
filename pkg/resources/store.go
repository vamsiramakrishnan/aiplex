// Package resources defines tenant-scoped, revisioned module-owned resources.
package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

var ErrNotFound = errors.New("resource not found")
var ErrConflict = errors.New("resource revision conflict")

type Record struct {
	TenantID string          `json:"tenant_id"`
	Kind     string          `json:"kind"`
	ID       string          `json:"id"`
	Revision uint64          `json:"revision"`
	Spec     json.RawMessage `json:"spec"`
}

func (r Record) Validate() error {
	if r.TenantID == "" || r.Kind == "" || r.ID == "" || len(r.ID) > 512 || len(r.Kind) > 128 || len(r.TenantID) > 512 || len(r.Spec) > 1<<20 || !json.Valid(r.Spec) {
		return fmt.Errorf("invalid resource identity or JSON specification")
	}
	return nil
}

type Store interface {
	Get(context.Context, string, string, string) (Record, error)
	List(context.Context, string, string) ([]Record, error)
	// Put uses expected=0 for creation, including recreation after deletion.
	// Revisions increase across recreations; a deleted ETag is never reused.
	Put(context.Context, Record, uint64) (Record, error)
	Delete(context.Context, string, string, string, uint64) error
}
type key struct{ tenant, kind, id string }
type MemoryStore struct {
	mu      sync.RWMutex
	records map[key]Record
	deleted map[key]bool
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: map[key]Record{}, deleted: map[key]bool{}}
}
func clone(r Record) Record { r.Spec = append(json.RawMessage(nil), r.Spec...); return r }
func (s *MemoryStore) Get(ctx context.Context, t, k, id string) (Record, error) {
	if e := ctx.Err(); e != nil {
		return Record{}, e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.records[key{t, k, id}]
	if !ok || s.deleted[key{t, k, id}] {
		return Record{}, ErrNotFound
	}
	return clone(r), nil
}
func (s *MemoryStore) List(ctx context.Context, t, k string) ([]Record, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Record{}
	for key, r := range s.records {
		if r.TenantID == t && r.Kind == k && !s.deleted[key] {
			out = append(out, clone(r))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (s *MemoryStore) Put(ctx context.Context, r Record, expected uint64) (Record, error) {
	if e := ctx.Err(); e != nil {
		return Record{}, e
	}
	if e := r.Validate(); e != nil {
		return Record{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{r.TenantID, r.Kind, r.ID}
	current := s.records[k]
	// A deleted identity can be created again with expected=0, but its next
	// revision must not reuse an ETag from a previous incarnation. Any stale
	// update/delete against the former resource remains a conflict.
	if (s.deleted[k] && expected != 0) || (!s.deleted[k] && current.Revision != expected) {
		return Record{}, ErrConflict
	}
	if current.Revision >= math.MaxInt64 {
		return Record{}, ErrConflict
	}
	r.Revision = current.Revision + 1
	s.records[k] = clone(r)
	delete(s.deleted, k)
	return clone(r), nil
}
func (s *MemoryStore) Delete(ctx context.Context, t, k, id string, expected uint64) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	resourceKey := key{t, k, id}
	r, ok := s.records[resourceKey]
	if !ok || s.deleted[resourceKey] {
		return ErrNotFound
	}
	if r.Revision != expected {
		return ErrConflict
	}
	// Retain only the resource identity/revision; deleted payload data is not
	// retained by the tombstone.
	r.Spec = nil
	s.records[resourceKey] = r
	if s.deleted == nil {
		s.deleted = make(map[key]bool)
	}
	s.deleted[resourceKey] = true
	return nil
}

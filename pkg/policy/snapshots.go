// Package policy publishes immutable policy snapshots. It contains no provider
// adapters or allocation logic. Persistence implementations must preserve CAS.
package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

var ErrConflict = errors.New("policy revision conflict")
var ErrNotFound = errors.New("policy snapshot not found")

type Snapshot struct {
	TenantID string          `json:"tenant_id"`
	ID       string          `json:"id"`
	Version  uint64          `json:"version"`
	Digest   string          `json:"digest"`
	Document json.RawMessage `json:"document"`
}

// Store is deliberately independent of any module's resource database.
type Store interface {
	Publish(tenant, id string, expected uint64, document json.RawMessage) (Snapshot, error)
	Get(tenant, id string, version uint64) (Snapshot, error)
}

type key struct{ tenant, id string }
type MemoryStore struct {
	mu      sync.RWMutex
	history map[key][]Snapshot
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{history: make(map[key][]Snapshot)} }
func copySnapshot(s Snapshot) Snapshot {
	s.Document = append(json.RawMessage(nil), s.Document...)
	return s
}

// Publish appends a revision only if expected is the current revision. Inputs
// and outputs are copied so callers cannot mutate already-published snapshots.
func (s *MemoryStore) Publish(tenant, id string, expected uint64, doc json.RawMessage) (Snapshot, error) {
	if tenant == "" || id == "" {
		return Snapshot{}, fmt.Errorf("tenant and policy id required")
	}
	if !json.Valid(doc) || len(doc) > 1<<20 {
		return Snapshot{}, fmt.Errorf("policy must be valid JSON of at most 1MiB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.history == nil {
		s.history = make(map[key][]Snapshot)
	}
	k := key{tenant, id}
	h := s.history[k]
	if uint64(len(h)) != expected {
		return Snapshot{}, ErrConflict
	}
	// Snapshot identity includes tenant/id/version; digest covers exact published
	// bytes, not a claim of semantic equivalence between different JSON encodings.
	digest := sha256.Sum256(doc)
	next := Snapshot{tenant, id, expected + 1, hex.EncodeToString(digest[:]), append(json.RawMessage(nil), doc...)}
	s.history[k] = append(h, next)
	return copySnapshot(next), nil
}
func (s *MemoryStore) Get(tenant, id string, version uint64) (Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.history[key{tenant, id}]
	if version == 0 || version > uint64(len(h)) {
		return Snapshot{}, ErrNotFound
	}
	return copySnapshot(h[version-1]), nil
}

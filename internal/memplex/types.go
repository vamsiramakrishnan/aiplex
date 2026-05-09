// Package memplex implements the memory plane — the broker workload that
// serves cap://memory/* capabilities. See design/20-memplex-memory-plane.md.
package memplex

import (
	"context"
	"errors"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// Namespace identifies a single memory namespace served by the broker.
// Namespaces are addressed by capability URI; the broker resolves the URI
// against its registered NamespaceConfigs to find the backend.
type Namespace struct {
	URI       capability.URI    // canonical
	Tenant    string            // resolved from URI sub-path (may be empty for global ns)
	User      string            // resolved sub-user (may be empty)
	Backend   string            // local | firestore | alloydb | vertex | letta
	DataClass string            // public | internal | pii | regulated
	Retention time.Duration     // 0 = no automatic expiry
	Schema    map[string]any    // optional JSON Schema for value validation
	PII       *PIIPolicy        // nil = no PII detection
	Labels    map[string]string // free-form
}

// Value is the stored payload for a memory entry.
type Value struct {
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
	UpdatedAt time.Time      `json:"updated_at,omitempty"`
	TTL       time.Duration  `json:"ttl,omitempty"`
	Embedding []float32      `json:"embedding,omitempty"` // optional, for search backends
}

// WriteOpts captures per-write knobs.
type WriteOpts struct {
	TTL       time.Duration
	Embedding []float32
	IfNoneMatch bool // CAS: only insert if key absent
}

// Hit is a single search result.
type Hit struct {
	Key   string  `json:"key"`
	Value Value   `json:"value"`
	Score float32 `json:"score,omitempty"` // similarity score (1.0 = exact)
}

// Listing pages keys by prefix.
type Listing struct {
	Keys      []string `json:"keys"`
	NextPage  string   `json:"next_page,omitempty"`
}

// Page is the cursor for pagination.
type Page struct {
	Cursor string
	Limit  int
}

// Query selects entries for search.
type Query struct {
	Embedding []float32 // for vector search
	Text      string    // for full-text search (optional, backend-dependent)
	TopK      int
	Filter    map[string]any // backend-specific filters
}

// Change is one mutation event for subscribers.
type Change struct {
	Op    string    `json:"op"` // put | delete
	Key   string    `json:"key"`
	Value *Value    `json:"value,omitempty"` // nil for delete
	At    time.Time `json:"at"`
}

// PIIPolicy declares how to handle PII in writes.
type PIIPolicy struct {
	Enabled bool       `json:"enabled"`
	Rules   []PIIRule  `json:"rules"`
}

// PIIRule fires on a JSON path within the value.
type PIIRule struct {
	Field  string `json:"field"`  // dotted JSON path: "user.ssn"
	Action string `json:"action"` // reject | hash | redact
}

// MemoryBackend is the pluggable storage interface. Backends only see
// Namespace metadata; tenant scoping is enforced upstream by the broker.
type MemoryBackend interface {
	Read(ctx context.Context, ns Namespace, key string) (*Value, error)
	Write(ctx context.Context, ns Namespace, key string, val Value, opts WriteOpts) error
	Delete(ctx context.Context, ns Namespace, key string) error
	Search(ctx context.Context, ns Namespace, q Query) ([]Hit, error)
	List(ctx context.Context, ns Namespace, prefix string, page Page) (Listing, error)
	Subscribe(ctx context.Context, ns Namespace, prefix string) (<-chan Change, error)

	// Name returns the backend identifier ("local", "firestore", …).
	Name() string
}

// ErrNotFound signals a missing key. Backends must return this exact error
// (use errors.Is) so the broker can map it to HTTP 404.
var ErrNotFound = errors.New("memplex: key not found")

// ErrConflict signals a CAS failure on IfNoneMatch writes.
var ErrConflict = errors.New("memplex: key already exists")

// ErrPIIRejected signals that a PII rule rejected the write.
var ErrPIIRejected = errors.New("memplex: write rejected by PII policy")

// ErrSchemaViolation signals that the value did not match the namespace schema.
var ErrSchemaViolation = errors.New("memplex: value violates namespace schema")

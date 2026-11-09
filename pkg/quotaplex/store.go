package quotaplex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"unicode/utf8"
)

var (
	ErrNotFound          = errors.New("quota record not found")
	ErrConflict          = errors.New("quota record conflict")
	ErrReadOnly          = errors.New("quota transaction is read-only")
	ErrTransactionLimit  = errors.New("quota transaction limit exceeded")
	ErrTransactionClosed = errors.New("quota transaction is closed")
)

const (
	MaxTransactionRecords = 128
	MaxRecordBytes        = 256 << 10
	// MaxTransactionBytes bounds the sum of original record payloads touched
	// plus final staged write payloads, excluding fixed coordinate metadata.
	MaxTransactionBytes = 4 << 20
)

// Record is a snapshot of one module-owned record. Kind and ID are coordinates
// within the transaction's authenticated tenant; Value is valid JSON.
type Record struct {
	Kind  string          `json:"kind"`
	ID    string          `json:"id"`
	Value json.RawMessage `json:"value"`
}

// Key identifies one record within the transaction's tenant.
type Key struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Store is QuotaPlex's multi-record atomic boundary. Update commits every staged
// change together or none. View supplies one consistent read-only snapshot.
// Callbacks may be retried: keep them deterministic and free of network calls,
// clock reads and mutations outside the callback. Returned data is usable only
// after the method succeeds. Store implementations do not authenticate callers.
type Store interface {
	Update(context.Context, string, func(Tx) error) error
	View(context.Context, string, func(Tx) error) error
}

// Tx exists only during its callback and must not be used concurrently. Writes
// are read-your-own-writes and Create refuses replacement of an existing record.
// List is bounded and ordered by ID; it fails instead of returning a truncated
// recovery inventory. Any operation error other than ErrNotFound aborts the
// transaction even if the callback discards that error.
type Tx interface {
	// Prefetch warms at most MaxTransactionRecords requested keys together.
	// Duplicate/cached/staged keys issue no additional backend reads. Missing
	// records are normal; a subsequent Get still returns ErrNotFound. It changes
	// no persisted state and uses the same coordinate/read-byte/fanout bounds.
	Prefetch([]Key) error
	Get(kind, id string, out any) error
	Put(kind, id string, value any) error
	Create(kind, id string, value any) error
	Delete(kind, id string) error
	List(kind string) ([]Record, error)
	// Scan returns at most limit records ordered by logical ID, strictly after
	// afterID. An empty afterID starts the scan. Separate calls have separate
	// snapshots: recovery must be idempotent and periodically rescan for inserts
	// before its saved cursor. limit must be in [1, MaxTransactionRecords].
	Scan(kind, afterID string, limit int) ([]Record, error)
}

// ValidateStoreCoordinate is shared by backend adapters. Slashes are safe in
// logical IDs because a backend must encode the complete coordinate tuple.
func ValidateStoreCoordinate(tenant, kind, id string) error {
	for _, v := range []string{tenant, kind, id} {
		if v == "" || len(v) > 1024 || !utf8.ValidString(v) {
			return fmt.Errorf("bounded quota tenant, kind and id are required")
		}
		for _, c := range v {
			if c < 0x20 || c == 0x7f {
				return fmt.Errorf("quota coordinates contain control characters")
			}
		}
	}
	return nil
}

// EncodeStoreValue snapshots a value and rejects records that cannot fit the
// atomic backend contract. A nil JSON value is valid; invalid encodings are not.
func EncodeStoreValue(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode quota record: %w", err)
	}
	if len(raw) > MaxRecordBytes {
		return nil, ErrTransactionLimit
	}
	return raw, nil
}

// DecodeStoreValue never exposes the backend's bytes to custom JSON decoders.
func DecodeStoreValue(raw json.RawMessage, out any) error {
	if out == nil || reflect.ValueOf(out).Kind() != reflect.Pointer || reflect.ValueOf(out).IsNil() {
		return fmt.Errorf("quota record destination must be a nonnil pointer")
	}
	if len(raw) > MaxRecordBytes || !json.Valid(raw) {
		return fmt.Errorf("invalid persisted quota record")
	}
	destination := reflect.ValueOf(out)
	fresh := reflect.New(destination.Elem().Type())
	if err := json.Unmarshal(append(json.RawMessage(nil), raw...), fresh.Interface()); err != nil {
		return fmt.Errorf("decode quota record: %w", err)
	}
	destination.Elem().Set(fresh.Elem())
	return nil
}

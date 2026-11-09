package quotaplex

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

type recordKey struct{ kind, id string }

// MemoryStore is an executable transaction reference, not durable storage.
// Independent engines sharing this instance share one atomic tenant boundary.
type MemoryStore struct {
	once    sync.Once
	gate    chan struct{}
	tenants map[string]map[recordKey]json.RawMessage
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tenants: map[string]map[recordKey]json.RawMessage{}}
}

func (s *MemoryStore) Update(ctx context.Context, tenant string, fn func(Tx) error) error {
	return s.run(ctx, tenant, false, fn)
}
func (s *MemoryStore) View(ctx context.Context, tenant string, fn func(Tx) error) error {
	return s.run(ctx, tenant, true, fn)
}
func (s *MemoryStore) run(ctx context.Context, tenant string, readOnly bool, fn func(Tx) error) error {
	if ctx == nil || fn == nil {
		return fmt.Errorf("quota transaction needs context and callback")
	}
	if err := ValidateStoreCoordinate(tenant, "transaction", "transaction"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.once.Do(func() { s.gate = make(chan struct{}, 1); s.gate <- struct{}{} })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.gate:
	}
	defer func() { s.gate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.tenants == nil {
		s.tenants = map[string]map[recordKey]json.RawMessage{}
	}
	tx := &memoryTx{ctx: ctx, tenant: tenant, base: s.tenants[tenant], staged: map[recordKey]json.RawMessage{}, touched: map[recordKey]bool{}, readOnly: readOnly, active: true}
	defer func() { tx.active = false }()
	if err := fn(tx); err != nil {
		return err
	}
	if tx.err != nil {
		return tx.err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if readOnly {
		return nil
	}
	if s.tenants[tenant] == nil {
		s.tenants[tenant] = map[recordKey]json.RawMessage{}
	}
	for k, raw := range tx.staged {
		if raw == nil {
			delete(s.tenants[tenant], k)
		} else {
			s.tenants[tenant][k] = append(json.RawMessage(nil), raw...)
		}
	}
	return nil
}

type memoryTx struct {
	ctx       context.Context
	tenant    string
	base      map[recordKey]json.RawMessage
	staged    map[recordKey]json.RawMessage // nil is a deletion; JSON null has bytes
	touched   map[recordKey]bool
	bytes     int
	readBytes int
	readOnly  bool
	active    bool
	err       error
}

func (t *memoryTx) fail(err error) error {
	if err != nil && err != ErrNotFound && t.err == nil {
		t.err = err
	}
	return err
}
func (t *memoryTx) touch(kind, id string, write bool) (recordKey, error) {
	k := recordKey{kind, id}
	if !t.active {
		return k, ErrTransactionClosed
	}
	if t.err != nil {
		return k, t.err
	}
	if err := t.ctx.Err(); err != nil {
		return k, t.fail(err)
	}
	if err := ValidateStoreCoordinate(t.tenant, kind, id); err != nil {
		return k, t.fail(err)
	}
	if write && t.readOnly {
		return k, t.fail(ErrReadOnly)
	}
	if !t.touched[k] {
		if len(t.touched) >= MaxTransactionRecords {
			return k, t.fail(ErrTransactionLimit)
		}
		t.touched[k] = true
		t.readBytes += len(t.base[k])
		if t.readBytes+t.bytes > MaxTransactionBytes {
			return k, t.fail(ErrTransactionLimit)
		}
	}
	return k, nil
}
func (t *memoryTx) raw(k recordKey) (json.RawMessage, bool) {
	if value, ok := t.staged[k]; ok {
		return value, value != nil
	}
	value, ok := t.base[k]
	return value, ok
}
func (t *memoryTx) Prefetch(keys []Key) error {
	if !t.active {
		return ErrTransactionClosed
	}
	if t.err != nil {
		return t.err
	}
	if err := t.ctx.Err(); err != nil {
		return t.fail(err)
	}
	if len(keys) > MaxTransactionRecords {
		return t.fail(ErrTransactionLimit)
	}
	for _, key := range keys {
		if _, err := t.touch(key.Kind, key.ID, false); err != nil {
			return err
		}
	}
	return nil
}
func (t *memoryTx) Get(kind, id string, out any) error {
	k, err := t.touch(kind, id, false)
	if err != nil {
		return err
	}
	raw, ok := t.raw(k)
	if !ok {
		return ErrNotFound
	}
	return t.fail(DecodeStoreValue(raw, out))
}
func (t *memoryTx) write(kind, id string, value any, create bool) error {
	k, err := t.touch(kind, id, true)
	if err != nil {
		return err
	}
	if _, exists := t.raw(k); create && exists {
		return t.fail(ErrConflict)
	}
	raw, err := EncodeStoreValue(value)
	if err != nil {
		return t.fail(err)
	}
	previous := len(t.staged[k])
	if t.readBytes+t.bytes-previous+len(raw) > MaxTransactionBytes {
		return t.fail(ErrTransactionLimit)
	}
	t.bytes = t.bytes - previous + len(raw)
	t.staged[k] = raw
	return nil
}
func (t *memoryTx) Put(kind, id string, value any) error    { return t.write(kind, id, value, false) }
func (t *memoryTx) Create(kind, id string, value any) error { return t.write(kind, id, value, true) }
func (t *memoryTx) Delete(kind, id string) error {
	k, err := t.touch(kind, id, true)
	if err != nil {
		return err
	}
	if _, ok := t.raw(k); !ok {
		return ErrNotFound
	}
	t.bytes -= len(t.staged[k])
	t.staged[k] = nil
	return nil
}
func (t *memoryTx) List(kind string) ([]Record, error) {
	return t.scan(kind, "", MaxTransactionRecords+1)
}
func (t *memoryTx) Scan(kind, afterID string, limit int) ([]Record, error) {
	if limit < 1 || limit > MaxTransactionRecords {
		return nil, t.fail(ErrTransactionLimit)
	}
	return t.scan(kind, afterID, limit)
}
func (t *memoryTx) scan(kind, afterID string, limit int) ([]Record, error) {
	if !t.active {
		return nil, ErrTransactionClosed
	}
	if t.err != nil {
		return nil, t.err
	}
	if err := t.ctx.Err(); err != nil {
		return nil, t.fail(err)
	}
	if err := ValidateStoreCoordinate(t.tenant, kind, "list"); err != nil {
		return nil, t.fail(err)
	}
	if afterID != "" {
		if err := ValidateStoreCoordinate(t.tenant, kind, afterID); err != nil {
			return nil, t.fail(err)
		}
	}
	ids := map[string]bool{}
	for k := range t.base {
		if k.kind == kind {
			ids[k.id] = true
		}
	}
	for k := range t.staged {
		if k.kind == kind {
			ids[k.id] = true
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		if id > afterID {
			ordered = append(ordered, id)
		}
	}
	sort.Strings(ordered)
	out := make([]Record, 0)
	for _, id := range ordered {
		k, err := t.touch(kind, id, false)
		if err != nil {
			return nil, err
		}
		if raw, ok := t.raw(k); ok {
			out = append(out, Record{Kind: kind, ID: id, Value: append(json.RawMessage(nil), raw...)})
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

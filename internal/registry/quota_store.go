package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"cloud.google.com/go/firestore"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// QuotaStore uses native multi-document transactions, separate from the legacy
// registry and single-resource CAS interface. It shares the configured database.
// No quota policy or admission is enabled by constructing this adapter.
func (f *FirestoreStore) QuotaStore() quotaplex.Store {
	if f == nil {
		return &firestoreQuotaStore{}
	}
	return &firestoreQuotaStore{client: f.client}
}

type firestoreQuotaStore struct{ client *firestore.Client }
type quotaRecordKey struct{ kind, id string }
type quotaRecordDoc struct {
	Tenant string `firestore:"tenant"`
	Kind   string `firestore:"kind"`
	ID     string `firestore:"id"`
	Value  string `firestore:"value"`
}
type quotaWrite struct {
	raw     json.RawMessage
	create  bool
	deleted bool
}

func quotaCoordinateDigest(v string) string {
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (s *firestoreQuotaStore) collection(tenant, kind string) *firestore.CollectionRef {
	return s.client.Collection("quota_tenants").Doc(quotaCoordinateDigest(tenant)).Collection("kinds").Doc(quotaCoordinateDigest(kind)).Collection("records")
}
func (s *firestoreQuotaStore) ref(tenant string, key quotaRecordKey) *firestore.DocumentRef {
	return s.collection(tenant, key.kind).Doc(quotaCoordinateDigest(key.id))
}
func (s *firestoreQuotaStore) Update(ctx context.Context, tenant string, fn func(quotaplex.Tx) error) error {
	return s.run(ctx, tenant, false, fn)
}
func (s *firestoreQuotaStore) View(ctx context.Context, tenant string, fn func(quotaplex.Tx) error) error {
	return s.run(ctx, tenant, true, fn)
}
func (s *firestoreQuotaStore) run(ctx context.Context, tenant string, readOnly bool, fn func(quotaplex.Tx) error) error {
	if s == nil || s.client == nil || ctx == nil || fn == nil {
		return fmt.Errorf("quota transaction needs a configured Firestore client, context and callback")
	}
	if err := quotaplex.ValidateStoreCoordinate(tenant, "transaction", "transaction"); err != nil {
		return err
	}
	opts := []firestore.TransactionOption{firestore.MaxAttempts(5)}
	if readOnly {
		opts = append(opts, firestore.ReadOnly)
	}
	return s.client.RunTransaction(ctx, func(transactionCtx context.Context, native *firestore.Transaction) error {
		// Every callback retry starts from clean staging/cache/limit state.
		tx := &firestoreQuotaTx{ctx: transactionCtx, store: s, native: native, tenant: tenant, readOnly: readOnly, active: true, cache: map[quotaRecordKey]json.RawMessage{}, loaded: map[quotaRecordKey]bool{}, staged: map[quotaRecordKey]quotaWrite{}}
		defer func() { tx.active = false }()
		if err := fn(tx); err != nil {
			return err
		}
		if tx.err != nil {
			return tx.err
		}
		if err := transactionCtx.Err(); err != nil {
			return err
		}
		// No native writes are issued until all callback reads are complete.
		// This preserves Firestore's read-before-write requirement even when
		// the generic callback reads another account after staging a write.
		keys := make([]quotaRecordKey, 0, len(tx.staged))
		for key := range tx.staged {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].kind != keys[j].kind {
				return keys[i].kind < keys[j].kind
			}
			return keys[i].id < keys[j].id
		})
		for _, key := range keys {
			write := tx.staged[key]
			ref := s.ref(tenant, key)
			var err error
			if write.deleted {
				err = native.Delete(ref)
			} else {
				doc := quotaRecordDoc{Tenant: tenant, Kind: key.kind, ID: key.id, Value: string(write.raw)}
				if write.create {
					err = native.Create(ref, doc)
				} else {
					err = native.Set(ref, doc)
				}
			}
			if err != nil {
				return err
			}
		}
		return nil
	}, opts...)
}

type firestoreQuotaTx struct {
	ctx       context.Context
	store     *firestoreQuotaStore
	native    *firestore.Transaction
	tenant    string
	readOnly  bool
	active    bool
	cache     map[quotaRecordKey]json.RawMessage
	loaded    map[quotaRecordKey]bool
	staged    map[quotaRecordKey]quotaWrite
	bytes     int
	readBytes int
	err       error
}

func (t *firestoreQuotaTx) fail(err error) error {
	if err != nil && err != quotaplex.ErrNotFound && t.err == nil {
		t.err = err
	}
	return err
}
func (t *firestoreQuotaTx) touch(kind, id string, write bool) (quotaRecordKey, error) {
	k := quotaRecordKey{kind, id}
	if !t.active {
		return k, quotaplex.ErrTransactionClosed
	}
	if t.err != nil {
		return k, t.err
	}
	if err := t.ctx.Err(); err != nil {
		return k, t.fail(err)
	}
	if err := quotaplex.ValidateStoreCoordinate(t.tenant, kind, id); err != nil {
		return k, t.fail(err)
	}
	if write && t.readOnly {
		return k, t.fail(quotaplex.ErrReadOnly)
	}
	if _, known := t.loaded[k]; !known {
		if len(t.loaded) >= quotaplex.MaxTransactionRecords {
			return k, t.fail(quotaplex.ErrTransactionLimit)
		}
		// A touched-but-unloaded coordinate counts toward the fanout bound.
		t.loaded[k] = false
	}
	return k, nil
}
func (t *firestoreQuotaTx) decode(doc *firestore.DocumentSnapshot, key quotaRecordKey) (json.RawMessage, error) {
	var record quotaRecordDoc
	if err := doc.DataTo(&record); err != nil {
		return nil, err
	}
	if record.Tenant != t.tenant || record.Kind != key.kind || record.ID != key.id || doc.Ref.Path != t.store.ref(t.tenant, key).Path || !json.Valid([]byte(record.Value)) || len(record.Value) > quotaplex.MaxRecordBytes {
		return nil, fmt.Errorf("invalid persisted quota namespace or value")
	}
	return json.RawMessage(record.Value), nil
}
func (t *firestoreQuotaTx) raw(key quotaRecordKey) (json.RawMessage, error) {
	if write, ok := t.staged[key]; ok {
		if write.deleted {
			return nil, quotaplex.ErrNotFound
		}
		return write.raw, nil
	}
	if t.loaded[key] {
		if raw := t.cache[key]; raw != nil {
			return raw, nil
		}
		return nil, quotaplex.ErrNotFound
	}
	doc, err := t.native.Get(t.store.ref(t.tenant, key))
	if status.Code(err) == codes.NotFound {
		t.loaded[key] = true
		return nil, quotaplex.ErrNotFound
	}
	if err != nil {
		return nil, t.fail(err)
	}
	raw, err := t.decode(doc, key)
	if err != nil {
		return nil, t.fail(err)
	}
	if err := t.cacheRecord(key, raw); err != nil {
		return nil, err
	}
	return raw, nil
}
func (t *firestoreQuotaTx) cacheRecord(key quotaRecordKey, raw json.RawMessage) error {
	if !t.loaded[key] {
		t.readBytes += len(raw)
		if t.readBytes+t.bytes > quotaplex.MaxTransactionBytes {
			return t.fail(quotaplex.ErrTransactionLimit)
		}
	}
	t.loaded[key], t.cache[key] = true, raw
	return nil
}
func (t *firestoreQuotaTx) Prefetch(keys []quotaplex.Key) error {
	if !t.active {
		return quotaplex.ErrTransactionClosed
	}
	if t.err != nil {
		return t.err
	}
	if err := t.ctx.Err(); err != nil {
		return t.fail(err)
	}
	if len(keys) > quotaplex.MaxTransactionRecords {
		return t.fail(quotaplex.ErrTransactionLimit)
	}
	pending := make([]quotaRecordKey, 0, len(keys))
	refs := make([]*firestore.DocumentRef, 0, len(keys))
	seen := map[quotaRecordKey]bool{}
	// Validate the whole request before issuing network reads. Staged writes
	// are not flushed here, preserving the SDK's native read-before-write rule.
	for _, coordinate := range keys {
		key, err := t.touch(coordinate.Kind, coordinate.ID, false)
		if err != nil {
			return err
		}
		if t.loaded[key] || seen[key] {
			continue
		}
		if _, staged := t.staged[key]; staged {
			continue
		}
		seen[key] = true
		pending, refs = append(pending, key), append(refs, t.store.ref(t.tenant, key))
	}
	if len(refs) == 0 {
		return nil
	}
	docs, err := t.native.GetAll(refs)
	if err != nil {
		return t.fail(err)
	}
	if len(docs) != len(refs) {
		return t.fail(fmt.Errorf("incomplete quota prefetch result"))
	}
	for i, doc := range docs {
		key := pending[i]
		if doc == nil || doc.Ref == nil || doc.Ref.Path != refs[i].Path {
			return t.fail(fmt.Errorf("quota prefetch namespace mismatch"))
		}
		if !doc.Exists() {
			t.loaded[key] = true
			continue
		}
		raw, err := t.decode(doc, key)
		if err != nil {
			return t.fail(err)
		}
		if err = t.cacheRecord(key, raw); err != nil {
			return err
		}
	}
	return nil
}
func (t *firestoreQuotaTx) Get(kind, id string, out any) error {
	k, err := t.touch(kind, id, false)
	if err != nil {
		return err
	}
	raw, err := t.raw(k)
	if err != nil {
		return err
	}
	return t.fail(quotaplex.DecodeStoreValue(raw, out))
}
func (t *firestoreQuotaTx) write(kind, id string, value any, create bool) error {
	k, err := t.touch(kind, id, true)
	if err != nil {
		return err
	}
	// Read the old value even for Put: the transaction must lock/observe every
	// affected account and validate persisted tenant coordinates before replacing.
	_, err = t.raw(k)
	if err != nil && err != quotaplex.ErrNotFound {
		return err
	}
	if create && err == nil {
		return t.fail(quotaplex.ErrConflict)
	}
	raw, err := quotaplex.EncodeStoreValue(value)
	if err != nil {
		return t.fail(err)
	}
	previous := len(t.staged[k].raw)
	if t.readBytes+t.bytes-previous+len(raw) > quotaplex.MaxTransactionBytes {
		return t.fail(quotaplex.ErrTransactionLimit)
	}
	t.bytes = t.bytes - previous + len(raw)
	// If a physically existing record was deleted then recreated in this same
	// transaction, Set expresses replacement; Create would incorrectly conflict.
	physicalCreate := create && t.cache[k] == nil
	if prior, ok := t.staged[k]; ok && prior.create {
		physicalCreate = true
	}
	t.staged[k] = quotaWrite{raw: raw, create: physicalCreate}
	return nil
}
func (t *firestoreQuotaTx) Put(kind, id string, value any) error {
	return t.write(kind, id, value, false)
}
func (t *firestoreQuotaTx) Create(kind, id string, value any) error {
	return t.write(kind, id, value, true)
}
func (t *firestoreQuotaTx) Delete(kind, id string) error {
	k, err := t.touch(kind, id, true)
	if err != nil {
		return err
	}
	if _, err = t.raw(k); err != nil {
		return err
	}
	t.bytes -= len(t.staged[k].raw)
	t.staged[k] = quotaWrite{deleted: true}
	return nil
}
func (t *firestoreQuotaTx) List(kind string) ([]quotaplex.Record, error) {
	return t.scan(kind, "", quotaplex.MaxTransactionRecords+1)
}
func (t *firestoreQuotaTx) Scan(kind, afterID string, limit int) ([]quotaplex.Record, error) {
	if limit < 1 || limit > quotaplex.MaxTransactionRecords {
		return nil, t.fail(quotaplex.ErrTransactionLimit)
	}
	return t.scan(kind, afterID, limit)
}
func (t *firestoreQuotaTx) scan(kind, afterID string, limit int) ([]quotaplex.Record, error) {
	if !t.active {
		return nil, quotaplex.ErrTransactionClosed
	}
	if t.err != nil {
		return nil, t.err
	}
	if err := t.ctx.Err(); err != nil {
		return nil, t.fail(err)
	}
	if err := quotaplex.ValidateStoreCoordinate(t.tenant, kind, "list"); err != nil {
		return nil, t.fail(err)
	}
	if afterID != "" {
		if err := quotaplex.ValidateStoreCoordinate(t.tenant, kind, afterID); err != nil {
			return nil, t.fail(err)
		}
	}
	extra := 0
	for key := range t.staged {
		if key.kind == kind && key.id > afterID {
			extra++
		}
	}
	query := t.store.collection(t.tenant, kind).OrderBy("id", firestore.Asc).Limit(limit + extra)
	if afterID != "" {
		query = query.StartAfter(afterID)
	}
	it := t.native.Documents(query)
	defer it.Stop()
	keys := map[quotaRecordKey]bool{}
	for {
		doc, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, t.fail(err)
		}
		var coordinate quotaRecordDoc
		if err = doc.DataTo(&coordinate); err != nil {
			return nil, t.fail(err)
		}
		key, err := t.touch(kind, coordinate.ID, false)
		if err != nil {
			return nil, err
		}
		raw, err := t.decode(doc, key)
		if err != nil {
			return nil, t.fail(err)
		}
		if err = t.cacheRecord(key, raw); err != nil {
			return nil, err
		}
		keys[key] = true
	}
	for key := range t.staged {
		if key.kind == kind && key.id > afterID {
			keys[key] = true
		}
	}
	out := make([]quotaplex.Record, 0, len(keys))
	for key := range keys {
		raw, err := t.raw(key)
		if err == quotaplex.ErrNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, quotaplex.Record{Kind: key.kind, ID: key.id, Value: append(json.RawMessage(nil), raw...)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

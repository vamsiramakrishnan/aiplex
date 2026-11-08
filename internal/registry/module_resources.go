package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"cloud.google.com/go/firestore"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ModuleResources shares the configured database while keeping new module-owned
// objects out of the historical broad registry.Store interface.
func (f *FirestoreStore) ModuleResources() resources.Store { return &firestoreResources{f.client} }

type firestoreResources struct{ client *firestore.Client }
type resourceDoc struct {
	TenantID string `firestore:"tenant_id"`
	Kind     string `firestore:"kind"`
	ID       string `firestore:"id"`
	Revision int64  `firestore:"revision"`
	Spec     string `firestore:"spec"`
	Deleted  bool   `firestore:"deleted,omitempty"`
}

func resourceKey(t, k, id string) string {
	b, _ := json.Marshal([]string{t, k, id})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (s *firestoreResources) ref(t, k, id string) *firestore.DocumentRef {
	return s.client.Collection("module_resources").Doc(resourceKey(t, k, id))
}
func decodeResourceState(doc *firestore.DocumentSnapshot) (resources.Record, bool, error) {
	var r resourceDoc
	if e := doc.DataTo(&r); e != nil {
		return resources.Record{}, false, e
	}
	record, e := recordFromResourceDoc(r)
	return record, r.Deleted, e
}

// Firestore stores signed 64-bit integers. Keeping the wire DTO signed is
// necessary: the SDK rejects uint64 values even when their numerical value fits.
func resourceDocument(r resources.Record) (resourceDoc, error) {
	if err := r.Validate(); err != nil {
		return resourceDoc{}, err
	}
	if r.Revision == 0 || r.Revision > math.MaxInt64 {
		return resourceDoc{}, resources.ErrConflict
	}
	return resourceDoc{TenantID: r.TenantID, Kind: r.Kind, ID: r.ID, Revision: int64(r.Revision), Spec: string(r.Spec)}, nil
}
func recordFromResourceDoc(d resourceDoc) (resources.Record, error) {
	if d.Revision <= 0 {
		return resources.Record{}, fmt.Errorf("invalid persisted resource revision")
	}
	r := resources.Record{TenantID: d.TenantID, Kind: d.Kind, ID: d.ID, Revision: uint64(d.Revision), Spec: json.RawMessage(d.Spec)}
	if err := r.Validate(); err != nil {
		return resources.Record{}, err
	}
	return r, nil
}
func sameNamespace(r resources.Record, t, k, id string) bool {
	return r.TenantID == t && r.Kind == k && r.ID == id
}

func (s *firestoreResources) Get(ctx context.Context, t, k, id string) (resources.Record, error) {
	d, e := s.ref(t, k, id).Get(ctx)
	if status.Code(e) == codes.NotFound {
		return resources.Record{}, resources.ErrNotFound
	}
	if e != nil {
		return resources.Record{}, e
	}
	r, deleted, e := decodeResourceState(d)
	if e == nil && !sameNamespace(r, t, k, id) {
		return resources.Record{}, fmt.Errorf("resource namespace mismatch")
	}
	if e == nil && deleted {
		return resources.Record{}, resources.ErrNotFound
	}
	return r, e
}
func (s *firestoreResources) List(ctx context.Context, t, k string) ([]resources.Record, error) {
	it := s.client.Collection("module_resources").Where("tenant_id", "==", t).Where("kind", "==", k).Documents(ctx)
	defer it.Stop()
	out := []resources.Record{}
	for {
		d, e := it.Next()
		if e == iterator.Done {
			sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
			return out, nil
		}
		if e != nil {
			return nil, e
		}
		r, deleted, e := decodeResourceState(d)
		if e != nil {
			return nil, e
		}
		if r.TenantID != t || r.Kind != k {
			return nil, fmt.Errorf("resource namespace mismatch")
		}
		if deleted {
			continue
		}
		out = append(out, r)
	}
}
func (s *firestoreResources) Put(ctx context.Context, r resources.Record, expected uint64) (resources.Record, error) {
	if e := r.Validate(); e != nil {
		return resources.Record{}, e
	}
	if expected >= math.MaxInt64 {
		return resources.Record{}, resources.ErrConflict
	}
	ref := s.ref(r.TenantID, r.Kind, r.ID)
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		d, e := tx.Get(ref)
		current := uint64(0)
		deleted := false
		if e == nil {
			old, tombstone, e := decodeResourceState(d)
			if e != nil {
				return e
			}
			if !sameNamespace(old, r.TenantID, r.Kind, r.ID) {
				return fmt.Errorf("resource namespace mismatch")
			}
			current = old.Revision
			deleted = tombstone
		} else if status.Code(e) != codes.NotFound {
			return e
		}
		if (deleted && expected != 0) || (!deleted && current != expected) || current >= math.MaxInt64 {
			return resources.ErrConflict
		}
		r.Revision = current + 1
		document, e := resourceDocument(r)
		if e != nil {
			return e
		}
		return tx.Set(ref, document)
	})
	if err != nil {
		return resources.Record{}, err
	}
	return r, nil
}
func (s *firestoreResources) Delete(ctx context.Context, t, k, id string, expected uint64) error {
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		d, e := tx.Get(s.ref(t, k, id))
		if status.Code(e) == codes.NotFound {
			return resources.ErrNotFound
		}
		if e != nil {
			return e
		}
		r, deleted, e := decodeResourceState(d)
		if e != nil {
			return e
		}
		if !sameNamespace(r, t, k, id) {
			return fmt.Errorf("resource namespace mismatch")
		}
		if deleted {
			return resources.ErrNotFound
		}
		if r.Revision != expected {
			return resources.ErrConflict
		}
		// Preserve the last revision while removing the original payload. A
		// create after deletion increments this revision instead of resetting it.
		tombstone := resourceDoc{TenantID: t, Kind: k, ID: id, Revision: int64(r.Revision), Spec: "null", Deleted: true}
		return tx.Set(d.Ref, tombstone)
	})
}

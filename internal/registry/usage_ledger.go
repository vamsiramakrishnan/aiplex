package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Usage ledger layout. Each tenant owns a subcollection keyed by a digest of
// the tenant ID, so a query can never span tenants and needs only the
// automatic single-field index on started_at:
//
//	usage_ledger_tenants/{sha256(tenant)}/usage_ledger_records/{record id}
//
// expires_at supports a Firestore TTL policy; reads also exclude expired
// records because TTL deletion is asynchronous.
const (
	usageLedgerTenants = "usage_ledger_tenants"
	usageLedgerRecords = "usage_ledger_records"
	// usageLedgerScanBound limits documents read by one List call. A page may
	// therefore hold fewer than limit records while still returning a cursor.
	usageLedgerScanBound = 5000
	usageLedgerBatch     = 500
)

type usageLedgerDoc struct {
	TenantID  string    `firestore:"tenant_id"`
	ID        string    `firestore:"id"`
	StartedAt time.Time `firestore:"started_at"`
	ExpiresAt time.Time `firestore:"expires_at"`
	Record    string    `firestore:"record"`
}

// UsageLedger returns the Firestore usage ledger sharing this database.
func (f *FirestoreStore) UsageLedger(retention time.Duration) usageledger.Store {
	if retention <= 0 {
		retention = usageledger.DefaultRetention
	}
	return &firestoreUsageLedger{client: f.client, retention: retention, now: time.Now}
}

type firestoreUsageLedger struct {
	client    *firestore.Client
	retention time.Duration
	now       func() time.Time
}

func (s *firestoreUsageLedger) collection(tenant string) *firestore.CollectionRef {
	return s.client.Collection(usageLedgerTenants).Doc(quotaCoordinateDigest(tenant)).Collection(usageLedgerRecords)
}

func usageLedgerDocument(r usageledger.Record, retention time.Duration) (usageLedgerDoc, error) {
	if err := r.Validate(); err != nil {
		return usageLedgerDoc{}, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return usageLedgerDoc{}, err
	}
	return usageLedgerDoc{TenantID: r.TenantID, ID: r.ID, StartedAt: r.StartedAt.UTC(), ExpiresAt: r.StartedAt.UTC().Add(retention), Record: string(raw)}, nil
}

func usageLedgerFromDoc(d usageLedgerDoc, tenant string) (usageledger.Record, error) {
	var r usageledger.Record
	if err := json.Unmarshal([]byte(d.Record), &r); err != nil {
		return usageledger.Record{}, fmt.Errorf("malformed persisted usage record")
	}
	if r.TenantID != tenant || d.TenantID != tenant || r.ID != d.ID {
		return usageledger.Record{}, fmt.Errorf("usage record namespace mismatch")
	}
	if err := r.Validate(); err != nil {
		return usageledger.Record{}, err
	}
	return r, nil
}

func (s *firestoreUsageLedger) Append(ctx context.Context, r usageledger.Record) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("usage ledger needs a configured Firestore client")
	}
	doc, err := usageLedgerDocument(r, s.retention)
	if err != nil {
		return err
	}
	_, err = s.collection(r.TenantID).Doc(r.ID).Create(ctx, doc)
	if status.Code(err) == codes.AlreadyExists {
		return usageledger.ErrConflict
	}
	return err
}

func (s *firestoreUsageLedger) cutoff() time.Time { return s.now().Add(-s.retention) }

func (s *firestoreUsageLedger) Get(ctx context.Context, tenant, id string) (usageledger.Record, error) {
	if s == nil || s.client == nil {
		return usageledger.Record{}, fmt.Errorf("usage ledger needs a configured Firestore client")
	}
	if tenant == "" || id == "" || len(id) > usageledger.MaxTextLength {
		return usageledger.Record{}, usageledger.ErrNotFound
	}
	snapshot, err := s.collection(tenant).Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return usageledger.Record{}, usageledger.ErrNotFound
	}
	if err != nil {
		return usageledger.Record{}, err
	}
	var d usageLedgerDoc
	if err := snapshot.DataTo(&d); err != nil {
		return usageledger.Record{}, err
	}
	r, err := usageLedgerFromDoc(d, tenant)
	if err != nil {
		return usageledger.Record{}, err
	}
	if r.StartedAt.Before(s.cutoff()) {
		return usageledger.Record{}, usageledger.ErrNotFound
	}
	return r, nil
}

func (s *firestoreUsageLedger) List(ctx context.Context, tenant string, q usageledger.Query) (usageledger.Page, error) {
	if s == nil || s.client == nil {
		return usageledger.Page{}, fmt.Errorf("usage ledger needs a configured Firestore client")
	}
	limit, err := usageledger.NormalizeLimit(q.Limit)
	if err != nil {
		return usageledger.Page{}, err
	}
	var position *usageledger.Position
	if q.Cursor != "" {
		p, err := usageledger.DecodeCursor(q.Cursor)
		if err != nil {
			return usageledger.Page{}, err
		}
		position = &p
	}
	since := q.Filter.Since
	if cutoff := s.cutoff(); since.Before(cutoff) {
		since = cutoff
	}
	base := s.collection(tenant).Where("started_at", ">=", since.UTC())
	if !q.Filter.Until.IsZero() {
		base = base.Where("started_at", "<", q.Filter.Until.UTC())
	}
	base = base.OrderBy("started_at", firestore.Desc).OrderBy(firestore.DocumentID, firestore.Desc)
	page := usageledger.Page{Records: []usageledger.Record{}}
	scanned := 0
	var last *usageledger.Record
	for {
		query := base.Limit(usageLedgerBatch)
		switch {
		case last != nil:
			query = query.StartAfter(last.StartedAt.UTC(), last.ID)
		case position != nil:
			query = query.StartAfter(position.StartedAt.UTC(), position.ID)
		}
		docs := query.Documents(ctx)
		fetched := 0
		for {
			snapshot, err := docs.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				docs.Stop()
				return usageledger.Page{}, err
			}
			fetched++
			scanned++
			var d usageLedgerDoc
			if err := snapshot.DataTo(&d); err != nil {
				docs.Stop()
				return usageledger.Page{}, err
			}
			r, err := usageLedgerFromDoc(d, tenant)
			if err != nil {
				docs.Stop()
				return usageledger.Page{}, err
			}
			if q.Filter.Matches(r) {
				if len(page.Records) == limit {
					docs.Stop()
					page.NextCursor = usageledger.EncodeCursor(page.Records[limit-1])
					return page, nil
				}
				page.Records = append(page.Records, r)
			}
			current := r
			last = &current
			if scanned >= usageLedgerScanBound {
				docs.Stop()
				page.NextCursor = usageledger.EncodeCursor(current)
				return page, nil
			}
		}
		docs.Stop()
		if fetched < usageLedgerBatch {
			return page, nil
		}
	}
}

var _ usageledger.Store = (*firestoreUsageLedger)(nil)

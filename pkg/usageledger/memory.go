package usageledger

import (
	"context"
	"sort"
	"sync"
	"time"
)

// DefaultMemoryRecordsPerTenant bounds the in-memory store per tenant.
const DefaultMemoryRecordsPerTenant = 100_000

// MemoryStore keeps records in process, newest first per tenant, bounded by
// age and count. It is intended for development and tests.
type MemoryStore struct {
	mu        sync.RWMutex
	tenants   map[string][]Record
	ids       map[string]map[string]bool
	retention time.Duration
	maxPer    int
	now       func() time.Time
}

type MemoryOption func(*MemoryStore)

// WithRetention bounds record age; zero keeps DefaultRetention.
func WithRetention(d time.Duration) MemoryOption {
	return func(s *MemoryStore) {
		if d > 0 {
			s.retention = d
		}
	}
}

// WithMaxRecordsPerTenant bounds the number of records kept per tenant.
func WithMaxRecordsPerTenant(n int) MemoryOption {
	return func(s *MemoryStore) {
		if n > 0 {
			s.maxPer = n
		}
	}
}

// WithClock replaces the retention clock (tests).
func WithClock(now func() time.Time) MemoryOption {
	return func(s *MemoryStore) {
		if now != nil {
			s.now = now
		}
	}
}

func NewMemoryStore(opts ...MemoryOption) *MemoryStore {
	s := &MemoryStore{tenants: map[string][]Record{}, ids: map[string]map[string]bool{}, retention: DefaultRetention, maxPer: DefaultMemoryRecordsPerTenant, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *MemoryStore) Append(ctx context.Context, r Record) error {
	if err := r.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids[r.TenantID] == nil {
		s.ids[r.TenantID] = map[string]bool{}
	}
	if s.ids[r.TenantID][r.ID] {
		return ErrConflict
	}
	records := s.tenants[r.TenantID]
	i := sort.Search(len(records), func(i int) bool { return !Less(records[i], r) })
	records = append(records, Record{})
	copy(records[i+1:], records[i:])
	records[i] = r.Clone()
	s.ids[r.TenantID][r.ID] = true
	s.tenants[r.TenantID] = s.prune(r.TenantID, records)
	return nil
}

// prune drops records older than the retention window and beyond the count
// bound; both remove the oldest records first. Callers hold the write lock.
func (s *MemoryStore) prune(tenant string, records []Record) []Record {
	cutoff := s.now().Add(-s.retention)
	keep := len(records)
	for keep > 0 && records[keep-1].StartedAt.Before(cutoff) {
		keep--
	}
	if keep > s.maxPer {
		keep = s.maxPer
	}
	for _, dropped := range records[keep:] {
		delete(s.ids[tenant], dropped.ID)
	}
	return records[:keep]
}

func (s *MemoryStore) expired(r Record) bool {
	return r.StartedAt.Before(s.now().Add(-s.retention))
}

func (s *MemoryStore) Get(ctx context.Context, tenant, id string) (Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.tenants[tenant] {
		if r.ID == id && r.TenantID == tenant {
			if s.expired(r) {
				return Record{}, ErrNotFound
			}
			return r.Clone(), nil
		}
	}
	return Record{}, ErrNotFound
}

func (s *MemoryStore) List(ctx context.Context, tenant string, q Query) (Page, error) {
	limit, err := NormalizeLimit(q.Limit)
	if err != nil {
		return Page{}, err
	}
	var position *Position
	if q.Cursor != "" {
		p, err := DecodeCursor(q.Cursor)
		if err != nil {
			return Page{}, err
		}
		position = &p
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	page := Page{Records: []Record{}}
	for _, r := range s.tenants[tenant] {
		if position != nil && !position.After(r) {
			continue
		}
		if r.TenantID != tenant || s.expired(r) || !q.Filter.Matches(r) {
			continue
		}
		if len(page.Records) == limit {
			page.NextCursor = EncodeCursor(page.Records[limit-1])
			break
		}
		page.Records = append(page.Records, r.Clone())
	}
	return page, nil
}

// BackfillCost sets the cost of a record whose cost is still unknown (see
// CostBackfiller). It never replaces a settled or estimated cost.
func (s *MemoryStore) BackfillCost(ctx context.Context, tenant, id string, cost Cost) (bool, error) {
	if err := cost.Validate(); err != nil || cost.Basis == BasisUnknown {
		return false, invalid("backfilled cost must be a valid known cost")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.tenants[tenant] {
		if r.ID == id && r.TenantID == tenant && !s.expired(r) {
			if r.Cost.Basis != BasisUnknown {
				return false, nil
			}
			updated := r.Clone()
			updated.Cost = cost
			s.tenants[tenant][i] = updated
			return true, nil
		}
	}
	return false, ErrNotFound
}

var (
	_ Store          = (*MemoryStore)(nil)
	_ CostBackfiller = (*MemoryStore)(nil)
)

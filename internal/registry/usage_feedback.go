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

// Feedback layout, beside the usage ledger and keyed the same way:
//
//	usage_ledger_tenants/{sha256(tenant)}/usage_feedback_latest/{record id}
//	usage_ledger_tenants/{sha256(tenant)}/usage_feedback_latest/{record id}/events/{feedback id}
//
// The latest document is the latest-wins view; events are the append-only
// history. expires_at supports a Firestore TTL policy on both.
const (
	usageFeedbackLatest = "usage_feedback_latest"
	usageFeedbackEvents = "events"
	usageFeedbackBatch  = 100
)

type usageFeedbackDoc struct {
	TenantID  string    `firestore:"tenant_id"`
	RecordID  string    `firestore:"record_id"`
	CreatedAt time.Time `firestore:"created_at"`
	ExpiresAt time.Time `firestore:"expires_at"`
	Feedback  string    `firestore:"feedback"`
}

// UsageFeedback returns the Firestore feedback store sharing this database.
func (f *FirestoreStore) UsageFeedback(retention time.Duration) usageledger.FeedbackStore {
	if retention <= 0 {
		retention = usageledger.DefaultRetention
	}
	return &firestoreUsageFeedback{client: f.client, retention: retention}
}

type firestoreUsageFeedback struct {
	client    *firestore.Client
	retention time.Duration
}

func (s *firestoreUsageFeedback) latest(tenant string) *firestore.CollectionRef {
	return s.client.Collection(usageLedgerTenants).Doc(quotaCoordinateDigest(tenant)).Collection(usageFeedbackLatest)
}

func feedbackDoc(f usageledger.Feedback, retention time.Duration) (usageFeedbackDoc, error) {
	raw, err := json.Marshal(f)
	if err != nil {
		return usageFeedbackDoc{}, err
	}
	return usageFeedbackDoc{TenantID: f.TenantID, RecordID: f.RecordID, CreatedAt: f.CreatedAt.UTC(), ExpiresAt: f.CreatedAt.UTC().Add(retention), Feedback: string(raw)}, nil
}

func feedbackFromDoc(d usageFeedbackDoc, tenant string) (usageledger.Feedback, error) {
	var f usageledger.Feedback
	if err := json.Unmarshal([]byte(d.Feedback), &f); err != nil {
		return usageledger.Feedback{}, fmt.Errorf("malformed persisted feedback")
	}
	if f.TenantID != tenant || d.TenantID != tenant {
		return usageledger.Feedback{}, fmt.Errorf("feedback namespace mismatch")
	}
	return f, nil
}

func (s *firestoreUsageFeedback) Append(ctx context.Context, f usageledger.Feedback) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("feedback needs a configured Firestore client")
	}
	if err := f.Validate(true); err != nil {
		return err
	}
	doc, err := feedbackDoc(f, s.retention)
	if err != nil {
		return err
	}
	latest := s.latest(f.TenantID).Doc(f.RecordID)
	event := latest.Collection(usageFeedbackEvents).Doc(f.ID)
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		current, err := tx.Get(latest)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		if err := tx.Create(event, doc); err != nil {
			return err
		}
		if current != nil && current.Exists() {
			var existing usageFeedbackDoc
			if err := current.DataTo(&existing); err == nil && existing.CreatedAt.After(doc.CreatedAt) {
				// A newer judgement is already the latest; keep it.
				return nil
			}
		}
		return tx.Set(latest, doc)
	})
}

func (s *firestoreUsageFeedback) Latest(ctx context.Context, tenant string, recordIDs []string) (map[string]usageledger.Feedback, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feedback needs a configured Firestore client")
	}
	out := map[string]usageledger.Feedback{}
	for start := 0; start < len(recordIDs); start += usageFeedbackBatch {
		end := start + usageFeedbackBatch
		if end > len(recordIDs) {
			end = len(recordIDs)
		}
		refs := make([]*firestore.DocumentRef, 0, end-start)
		for _, id := range recordIDs[start:end] {
			refs = append(refs, s.latest(tenant).Doc(id))
		}
		snaps, err := s.client.GetAll(ctx, refs)
		if err != nil {
			return nil, err
		}
		for _, snap := range snaps {
			if !snap.Exists() {
				continue
			}
			var d usageFeedbackDoc
			if err := snap.DataTo(&d); err != nil {
				return nil, err
			}
			if d.ExpiresAt.Before(time.Now()) {
				continue
			}
			f, err := feedbackFromDoc(d, tenant)
			if err != nil {
				return nil, err
			}
			out[f.RecordID] = f
		}
	}
	return out, nil
}

func (s *firestoreUsageFeedback) History(ctx context.Context, tenant, recordID string, limit int) ([]usageledger.Feedback, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feedback needs a configured Firestore client")
	}
	if limit < 1 || limit > usageledger.MaxFeedbackHistory {
		limit = usageledger.MaxFeedbackHistory
	}
	docs := s.latest(tenant).Doc(recordID).Collection(usageFeedbackEvents).OrderBy("created_at", firestore.Desc).Limit(limit).Documents(ctx)
	defer docs.Stop()
	out := []usageledger.Feedback{}
	for {
		snap, err := docs.Next()
		if err == iterator.Done {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		var d usageFeedbackDoc
		if err := snap.DataTo(&d); err != nil {
			return nil, err
		}
		f, err := feedbackFromDoc(d, tenant)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
}

var _ usageledger.FeedbackStore = (*firestoreUsageFeedback)(nil)

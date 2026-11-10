package usageledger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"math"
	"regexp"
	"sync"
	"time"
	"unicode/utf8"
)

// Feedback is one human or automated judgement of a usage record's exchange.
// Feedback is append-only: a later judgement of the same record supersedes an
// earlier one in the latest-wins view, and the earlier one stays in the
// history. It is stored beside the record, never in it, so records stay
// immutable.
type Feedback struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	RecordID string `json:"record_id"`
	// By is the verified principal that submitted it. It is set by the server.
	By        string    `json:"by"`
	CreatedAt time.Time `json:"created_at"`
	// Score is in [-1, 1]: -1 worst, 0 neutral, 1 best.
	Score float64 `json:"score"`
	// Label is an optional short classification such as "helpful".
	Label string `json:"label,omitempty"`
	// Comment is optional free text, stored only when the operator enabled
	// comments; it never appears in summaries.
	Comment string `json:"comment,omitempty"`
}

const (
	// MaxFeedbackComment bounds a comment, in characters.
	MaxFeedbackComment = 500
	// MaxFeedbackHistory bounds the history returned for one record.
	MaxFeedbackHistory = 100
	// MaxFeedbackPerRecord bounds the entries kept for one record by the
	// bundled stores; older entries are dropped from history, not from the
	// latest-wins view.
	MaxFeedbackPerRecord = 100
)

var feedbackLabel = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,31}$`)

// NewFeedbackID returns a random feedback identifier.
func NewFeedbackID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "fb_" + hex.EncodeToString(b[:])
}

// Validate checks a feedback entry before it is stored. allowComment reflects
// the operator's setting: a comment while comments are disabled is invalid,
// not silently dropped.
func (f Feedback) Validate(allowComment bool) error {
	if !validText(f.ID, true) || !validText(f.TenantID, true) || !validText(f.RecordID, true) || !validText(f.By, true) {
		return invalid("feedback id, tenant, record and author required")
	}
	if math.IsNaN(f.Score) || math.IsInf(f.Score, 0) || f.Score < -1 || f.Score > 1 {
		return invalid("score must be a number between -1 and 1")
	}
	if f.Label != "" && !feedbackLabel.MatchString(f.Label) {
		return invalid("label must match [a-z][a-z0-9_.-]{0,31}")
	}
	if f.Comment != "" {
		if !allowComment {
			return invalid("comments are not enabled")
		}
		if utf8.RuneCountInString(f.Comment) > MaxFeedbackComment || !utf8.ValidString(f.Comment) {
			return invalid("comment is bounded to %d characters", MaxFeedbackComment)
		}
		for _, c := range f.Comment {
			if (c < 0x20 && c != '\n' && c != '\t') || c == 0x7f {
				return invalid("comment cannot contain control characters")
			}
		}
	}
	if f.CreatedAt.IsZero() {
		return invalid("created_at required")
	}
	return nil
}

// FeedbackStore keeps feedback. Implementations scope every operation to the
// tenant argument.
type FeedbackStore interface {
	Append(ctx context.Context, f Feedback) error
	// Latest returns the newest feedback per record id; records without
	// feedback are absent from the result.
	Latest(ctx context.Context, tenant string, recordIDs []string) (map[string]Feedback, error)
	// History returns one record's feedback, newest first.
	History(ctx context.Context, tenant, recordID string, limit int) ([]Feedback, error)
}

// MemoryFeedbackStore keeps feedback in process, for development and tests.
type MemoryFeedbackStore struct {
	mu      sync.RWMutex
	entries map[string]map[string][]Feedback // tenant -> record -> oldest first
	max     int
}

// NewMemoryFeedbackStore bounds the records with feedback per tenant to
// maxRecords (zero: 100000).
func NewMemoryFeedbackStore(maxRecords int) *MemoryFeedbackStore {
	if maxRecords <= 0 {
		maxRecords = 100_000
	}
	return &MemoryFeedbackStore{entries: map[string]map[string][]Feedback{}, max: maxRecords}
}

func (s *MemoryFeedbackStore) Append(_ context.Context, f Feedback) error {
	// Comments are validated by the caller with the operator setting; the
	// store re-checks the rest.
	if err := f.Validate(true); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records := s.entries[f.TenantID]
	if records == nil {
		records = map[string][]Feedback{}
		s.entries[f.TenantID] = records
	}
	if _, ok := records[f.RecordID]; !ok && len(records) >= s.max {
		return invalid("feedback store is full for this tenant")
	}
	list := append(records[f.RecordID], f)
	if len(list) > MaxFeedbackPerRecord {
		list = list[len(list)-MaxFeedbackPerRecord:]
	}
	records[f.RecordID] = list
	return nil
}

func (s *MemoryFeedbackStore) Latest(_ context.Context, tenant string, recordIDs []string) (map[string]Feedback, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]Feedback{}
	for _, id := range recordIDs {
		if list := s.entries[tenant][id]; len(list) > 0 {
			out[id] = list[len(list)-1]
		}
	}
	return out, nil
}

func (s *MemoryFeedbackStore) History(_ context.Context, tenant, recordID string, limit int) ([]Feedback, error) {
	if limit < 1 || limit > MaxFeedbackHistory {
		limit = MaxFeedbackHistory
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.entries[tenant][recordID]
	out := make([]Feedback, 0, len(list))
	for i := len(list) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, list[i])
	}
	return out, nil
}

var _ FeedbackStore = (*MemoryFeedbackStore)(nil)

// FeedbackTotals summarizes the latest feedback of the records in a group.
// Records without feedback are counted in Without and never in the average:
// absence of feedback is not a neutral score.
type FeedbackTotals struct {
	// Records is the number of records with feedback.
	Records  int64 `json:"records"`
	Without  int64 `json:"without_feedback"`
	Positive int64 `json:"positive"`
	Negative int64 `json:"negative"`
	Neutral  int64 `json:"neutral"`
	// AverageScore is null when no record has feedback.
	AverageScore *float64 `json:"average_score"`
}

type feedbackAccumulator struct {
	totals FeedbackTotals
	sum    float64
}

func (a *feedbackAccumulator) add(f *Feedback) {
	if f == nil {
		a.totals.Without++
		return
	}
	a.totals.Records++
	a.sum += f.Score
	switch {
	case f.Score > 0:
		a.totals.Positive++
	case f.Score < 0:
		a.totals.Negative++
	default:
		a.totals.Neutral++
	}
}

func (a *feedbackAccumulator) result() *FeedbackTotals {
	t := a.totals
	if t.Records > 0 {
		avg := math.Round(a.sum/float64(t.Records)*1000) / 1000
		t.AverageScore = &avg
	}
	return &t
}

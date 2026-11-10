package usageledger

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Claude Code's log events carry the interaction's trace id but not the span
// of the LLM request, so a trace can be the only shared key. A trace can hold
// several requests: each managed record absorbs at most one reported record.
func TestTraceLevelCorrelationPairsOneReportedRecordPerManagedRecord(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := NewMemoryStore()
	trace := strings.Repeat("d", 32)
	seed(t, s,
		rec("m1", SourceManaged, "alice", now, func(r *Record) { r.TraceID, r.CallerSpanID = trace, strings.Repeat("1", 16) }),
		rec("m2", SourceManaged, "alice", now, func(r *Record) { r.TraceID, r.CallerSpanID = trace, strings.Repeat("2", 16) }),
		rec("r1", SourceReported, "alice", now, func(r *Record) { r.Harness, r.TraceID, r.SpanID = "claude-code", trace, strings.Repeat("9", 16) }),
		rec("r2", SourceReported, "alice", now, func(r *Record) { r.Harness, r.TraceID, r.SpanID = "claude-code", trace, strings.Repeat("8", 16) }),
		rec("r3", SourceReported, "alice", now, func(r *Record) { r.Harness, r.TraceID, r.SpanID = "claude-code", trace, strings.Repeat("7", 16) }),
		// A managed record whose trace AIPlex generated itself (no caller span) offers no key.
		rec("m-own", SourceManaged, "alice", now, func(r *Record) { r.TraceID = strings.Repeat("e", 32) }),
		rec("r-own", SourceReported, "alice", now, func(r *Record) { r.Harness, r.TraceID = "claude-code", strings.Repeat("e", 32) }),
	)
	sum, err := Summarize(context.Background(), s, "t", SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, g := range sum.Groups {
		counts[g.Key["source"]] = g.Records
	}
	if sum.ExcludedCorrelatedReported != 2 || counts[SourceReported] != 2 || counts[SourceManaged] != 3 {
		t.Fatalf("two managed records in the trace absorb two of three reported records: %v excluded %d", counts, sum.ExcludedCorrelatedReported)
	}
}

package usageledger

import (
	"context"
	"strings"
	"testing"
	"time"
)

func seed(t *testing.T, s Store, recs ...Record) {
	t.Helper()
	for _, r := range recs {
		if err := s.Append(context.Background(), r); err != nil {
			t.Fatalf("%s: %v", r.ID, err)
		}
	}
}

func rec(id, source, principal string, at time.Time, mutate func(*Record)) Record {
	r := Record{ID: id, TenantID: "t", StartedAt: at, FinishedAt: at, Plane: PlaneLLM, ModelID: "m", PrincipalID: principal, Status: StatusOK, Source: source, Requests: 1,
		Usage: &Usage{InputTokens: Int64(10), OutputTokens: Int64(5)}, Cost: UnknownCost()}
	if mutate != nil {
		mutate(&r)
	}
	return r
}

func TestSummarySeparatesSourcesAndDropsCorrelatedReported(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := NewMemoryStore()
	trace, span := strings.Repeat("a", 32), strings.Repeat("b", 16)
	seed(t, s,
		// A managed record that continued the harness's trace.
		rec("m-traced", SourceManaged, "alice", now, func(r *Record) { r.TraceID, r.CallerSpanID = trace, span }),
		// Its reported twin, matched by trace and span.
		rec("r-traced", SourceReported, "alice", now, func(r *Record) { r.Harness, r.TraceID, r.SpanID = "claude-code", trace, span }),
		// Matched by the harness's per-request id.
		rec("m-client", SourceManaged, "alice", now, func(r *Record) { r.ClientRequestID = "req-uuid-1" }),
		rec("r-client", SourceReported, "alice", now, func(r *Record) { r.Harness, r.ClientRequestID = "claude-code", "req-uuid-1" }),
		// Same keys, other principal: never correlated.
		rec("r-other", SourceReported, "bob", now, func(r *Record) { r.Harness, r.ClientRequestID = "claude-code", "req-uuid-1" }),
		// Same trace but a different span: not the same request.
		rec("r-otherspan", SourceReported, "alice", now, func(r *Record) { r.Harness, r.TraceID, r.SpanID = "claude-code", trace, strings.Repeat("c", 16) }),
		// A server-generated request id is not a key.
		rec("m-gen", SourceManaged, "alice", now, func(r *Record) { r.RequestID = "req-7" }),
		rec("r-gen", SourceReported, "alice", now, func(r *Record) { r.Harness, r.RequestID = "claude-code", "req-7" }),
		// A caller-chosen request id is.
		rec("m-own", SourceManaged, "alice", now, func(r *Record) { r.RequestID = "my-correlation-id" }),
		rec("r-own", SourceReported, "alice", now, func(r *Record) { r.Harness, r.RequestID = "claude-code", "my-correlation-id" }),
		// No key at all: kept apart, not guessed together by model and time.
		rec("r-none", SourceReported, "alice", now, func(r *Record) { r.Harness = "codex" }),
	)
	sum, err := Summarize(context.Background(), s, "t", SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if !sum.SourceSplit || len(sum.GroupBy) != 1 || sum.GroupBy[0] != "source" {
		t.Fatalf("sources must be separate groups: %+v", sum)
	}
	counts := map[string]int64{}
	for _, g := range sum.Groups {
		counts[g.Key["source"]] = g.Records
	}
	if counts[SourceManaged] != 4 || counts[SourceReported] != 4 || sum.ExcludedCorrelatedReported != 3 {
		t.Fatalf("counts %v excluded %d", counts, sum.ExcludedCorrelatedReported)
	}
	if len(sum.Notes) == 0 || !strings.Contains(sum.Notes[0], "not added together") {
		t.Fatalf("notes: %v", sum.Notes)
	}

	// Asking for reported records returns all of them, with a warning note.
	rep, _ := Summarize(context.Background(), s, "t", SummaryQuery{Filter: Filter{Source: SourceReported}})
	if rep.SourceSplit || rep.Groups[0].Records != 7 || rep.ExcludedCorrelatedReported != 0 || len(rep.Notes) != 1 || !strings.Contains(rep.Notes[0], "do not add") {
		t.Fatalf("explicit reported: %+v", rep)
	}

	// Merging is explicit, still leaves out correlated records, and says what it cannot know.
	merged, _ := Summarize(context.Background(), s, "t", SummaryQuery{MergeSources: true})
	if merged.SourceSplit || len(merged.Groups) != 1 || merged.Groups[0].Records != 8 || !strings.Contains(strings.Join(merged.Notes, " "), "may describe requests that are also counted") {
		t.Fatalf("merged: %+v", merged)
	}

	// A principal-scoped view only correlates its own records.
	bob, _ := Summarize(context.Background(), s, "t", SummaryQuery{Filter: Filter{Visibility: []string{"bob"}}})
	if bob.ExcludedCorrelatedReported != 0 || bob.Groups[0].Records != 1 {
		t.Fatalf("bob: %+v", bob)
	}
}

func TestSummaryWithoutReportedRecordsHasNoNote(t *testing.T) {
	now := time.Now().UTC()
	s := NewMemoryStore()
	seed(t, s, rec("m1", SourceManaged, "alice", now, nil))
	sum, _ := Summarize(context.Background(), s, "t", SummaryQuery{GroupBy: []string{"model_id"}})
	if len(sum.Notes) != 0 || sum.ExcludedCorrelatedReported != 0 || len(sum.Groups) != 1 || sum.Groups[0].Key["source"] != SourceManaged {
		t.Fatalf("%+v", sum)
	}
}

func TestReportedCostBasisIsASeparateTotal(t *testing.T) {
	now := time.Now().UTC()
	s := NewMemoryStore()
	seed(t, s,
		rec("r1", SourceReported, "alice", now, func(r *Record) { r.Cost = Cost{Basis: BasisReported, Currency: "USD", Amount: "12346", Scale: 6} }),
		rec("r2", SourceReported, "alice", now, func(r *Record) { r.Cost = Cost{Basis: BasisEstimated, Currency: "USD", Amount: "10", Scale: 6} }),
		rec("r3", SourceReported, "alice", now, nil),
	)
	sum, _ := Summarize(context.Background(), s, "t", SummaryQuery{Filter: Filter{Source: SourceReported}})
	g := sum.Groups[0]
	if len(g.Cost) != 2 || g.Cost[0].Basis != BasisEstimated || g.Cost[1].Basis != BasisReported || g.Cost[1].Amount != "12346" || g.UnknownCostRecords != 1 {
		t.Fatalf("%+v", g)
	}
}

func TestTagsFilterGroupAndValidation(t *testing.T) {
	now := time.Now().UTC()
	s := NewMemoryStore()
	seed(t, s,
		rec("a", SourceManaged, "alice", now, func(r *Record) { r.Tags = map[string]string{"experiment": "arm-a", "team": "x"} }),
		rec("b", SourceManaged, "alice", now, func(r *Record) { r.Tags = map[string]string{"experiment": "arm-b"} }),
		rec("c", SourceManaged, "alice", now, nil),
	)
	sum, err := Summarize(context.Background(), s, "t", SummaryQuery{GroupBy: []string{"tag:experiment"}})
	if err != nil || len(sum.Groups) != 3 {
		t.Fatalf("%v %+v", err, sum)
	}
	vals := map[string]int64{}
	for _, g := range sum.Groups {
		vals[g.Key["tag:experiment"]] = g.Records
	}
	if vals["arm-a"] != 1 || vals["arm-b"] != 1 || vals[""] != 1 {
		t.Fatalf("a record without the tag is its own group, not a value: %v", vals)
	}
	page, _ := s.List(context.Background(), "t", Query{Filter: Filter{Tags: map[string]string{"experiment": "arm-a", "team": "x"}}})
	if len(page.Records) != 1 || page.Records[0].ID != "a" {
		t.Fatalf("%+v", page.Records)
	}
	if _, err := ParseGroupBy("tag:Bad Key"); err == nil {
		t.Fatal("invalid tag key accepted")
	}
	bad := rec("d", SourceManaged, "alice", now, func(r *Record) { r.Tags = map[string]string{"k": "bad\nvalue"} })
	if err := s.Append(context.Background(), bad); err == nil {
		t.Fatal("tag value with control characters accepted")
	}
	tooMany := rec("e", SourceManaged, "alice", now, func(r *Record) {
		r.Tags = map[string]string{}
		for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
			r.Tags[k] = "v"
		}
	})
	if err := s.Append(context.Background(), tooMany); err == nil {
		t.Fatal("more than MaxTags accepted")
	}
}

func TestFeedbackLatestWinsAppendOnlyAndSummaryCountsRecordsWithout(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := NewMemoryStore()
	fs := NewMemoryFeedbackStore(0)
	seed(t, s,
		rec("a", SourceManaged, "alice", now, nil),
		rec("b", SourceManaged, "alice", now, nil),
		rec("c", SourceManaged, "alice", now, nil),
	)
	add := func(id, record string, score float64, at time.Time) {
		t.Helper()
		f := Feedback{ID: id, TenantID: "t", RecordID: record, By: "alice", CreatedAt: at, Score: score}
		if err := f.Validate(false); err != nil {
			t.Fatal(err)
		}
		if err := fs.Append(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	add("f1", "a", -1, now)
	add("f2", "a", 1, now.Add(time.Second))
	add("f3", "b", 0, now)
	latest, _ := fs.Latest(ctx, "t", []string{"a", "b", "c"})
	if len(latest) != 2 || latest["a"].Score != 1 || latest["b"].Score != 0 {
		t.Fatalf("latest wins: %+v", latest)
	}
	if h, _ := fs.History(ctx, "t", "a", 0); len(h) != 2 || h[0].ID != "f2" || h[1].ID != "f1" {
		t.Fatalf("history is append-only, newest first: %+v", h)
	}
	if other, _ := fs.Latest(ctx, "other-tenant", []string{"a"}); len(other) != 0 {
		t.Fatal("feedback crossed tenants")
	}
	sum, err := Summarize(ctx, s, "t", SummaryQuery{Feedback: fs})
	if err != nil {
		t.Fatal(err)
	}
	fb := sum.Groups[0].Feedback
	if fb == nil || fb.Records != 2 || fb.Without != 1 || fb.Positive != 1 || fb.Neutral != 1 || fb.Negative != 0 || fb.AverageScore == nil || *fb.AverageScore != 0.5 {
		t.Fatalf("feedback totals: %+v", fb)
	}
	none, _ := Summarize(ctx, s, "t", SummaryQuery{})
	if none.Groups[0].Feedback != nil {
		t.Fatal("feedback totals must be opt-in")
	}
	empty := NewMemoryFeedbackStore(0)
	zero, _ := Summarize(ctx, s, "t", SummaryQuery{Feedback: empty})
	if z := zero.Groups[0].Feedback; z.Records != 0 || z.Without != 3 || z.AverageScore != nil {
		t.Fatalf("no feedback is not a neutral score: %+v", z)
	}
}

func TestFeedbackValidation(t *testing.T) {
	base := Feedback{ID: "f", TenantID: "t", RecordID: "r", By: "alice", CreatedAt: time.Now(), Score: 0.5}
	for name, mutate := range map[string]func(*Feedback){
		"score too high": func(f *Feedback) { f.Score = 1.5 },
		"score too low":  func(f *Feedback) { f.Score = -1.01 },
		"bad label":      func(f *Feedback) { f.Label = "Not Allowed" },
		"comment":        func(f *Feedback) { f.Comment = "free text" },
		"control chars":  func(f *Feedback) { f.Comment = "x\x00y" },
		"no author":      func(f *Feedback) { f.By = "" },
		"overlong":       func(f *Feedback) { f.Comment = strings.Repeat("a", MaxFeedbackComment+1) },
	} {
		f := base
		mutate(&f)
		if err := f.Validate(name != "comment"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := base
	ok.Label, ok.Comment = "helpful", "fine"
	if err := ok.Validate(true); err != nil {
		t.Fatal(err)
	}
	nan := base
	nan.Score = nan.Score - nan.Score
	nan.Score = nan.Score / nan.Score
	if err := nan.Validate(false); err == nil {
		t.Fatal("NaN accepted")
	}
}

func TestReportedFieldBounds(t *testing.T) {
	now := time.Now().UTC()
	s := NewMemoryStore()
	good := rec("g", SourceReported, "alice", now, func(r *Record) {
		r.Harness, r.HarnessVersion, r.SessionID = "claude-code", "2.1.0", "s1"
		r.ReportedAttributes = map[string]string{"request_id": "req_1"}
		r.Cost = Cost{Basis: BasisReported, Currency: "USD", Amount: "5", Scale: 6}
	})
	if err := s.Append(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Record){
		"attribute key": func(r *Record) { r.ReportedAttributes = map[string]string{"User Email": "x"} },
		"attribute value": func(r *Record) {
			r.ReportedAttributes = map[string]string{"k": strings.Repeat("a", MaxLabelValueLength+1)}
		},
		"too many": func(r *Record) {
			r.ReportedAttributes = map[string]string{}
			for i := 0; i <= MaxReportedAttributes; i++ {
				r.ReportedAttributes["k"+string(rune('a'+i))] = "v"
			}
		},
		"unknown basis": func(r *Record) { r.Cost.Basis = "guess" },
	} {
		r := good
		r.ID = name
		mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

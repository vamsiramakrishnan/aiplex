package usageledger

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

var base = time.Date(2026, 11, 9, 10, 30, 0, 0, time.UTC)

func record(tenant, id string, at time.Time) Record {
	return Record{ID: id, TenantID: tenant, StartedAt: at, FinishedAt: at.Add(time.Second), LatencyMS: 1000, Plane: PlaneLLM, Provider: "google", ModelID: "gemini-2.5-flash", PrincipalID: "user-1", ActorID: "agent-1", Status: StatusOK, Source: SourceManaged, Requests: 1, Cost: UnknownCost()}
}

func TestRecordValidation(t *testing.T) {
	good := record("t", "a", base)
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Record){
		"no tenant":       func(r *Record) { r.TenantID = "" },
		"bad plane":       func(r *Record) { r.Plane = "other" },
		"bad status":      func(r *Record) { r.Status = "fine" },
		"bad source":      func(r *Record) { r.Source = "guessed" },
		"time order":      func(r *Record) { r.FinishedAt = r.StartedAt.Add(-time.Second) },
		"negative usage":  func(r *Record) { r.Usage = &Usage{InputTokens: Int64(-1)} },
		"control text":    func(r *Record) { r.ModelID = "a\nb" },
		"long text":       func(r *Record) { r.ToolName = strings.Repeat("x", MaxTextLength+1) },
		"unknown amount":  func(r *Record) { r.Cost = Cost{Basis: BasisUnknown, Amount: "1"} },
		"bad currency":    func(r *Record) { r.Cost = Cost{Basis: BasisSettled, Currency: "usd", Amount: "1"} },
		"negative amount": func(r *Record) { r.Cost = Cost{Basis: BasisEstimated, Currency: "USD", Amount: "-1"} },
		"non canonical":   func(r *Record) { r.Cost = Cost{Basis: BasisEstimated, Currency: "USD", Amount: "01"} },
		"large scale":     func(r *Record) { r.Cost = Cost{Basis: BasisEstimated, Currency: "USD", Amount: "1", Scale: 19} },
		"bad basis":       func(r *Record) { r.Cost = Cost{Basis: "guess"} },
	}
	for name, mutate := range cases {
		r := record("t", "a", base)
		mutate(&r)
		if err := r.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: expected invalid, got %v", name, err)
		}
	}
}

func TestUnknownStaysUnknownOnTheWire(t *testing.T) {
	r := record("t", "a", base)
	r.Usage = &Usage{OutputTokens: Int64(0)}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	_ = json.Unmarshal(raw, &wire)
	usage := wire["usage"].(map[string]any)
	if usage["input_tokens"] != nil || usage["output_tokens"] != float64(0) {
		t.Fatalf("unknown input must be null and reported zero must be 0: %s", raw)
	}
	if _, present := usage["input_tokens"]; !present {
		t.Fatal("unknown counter must be explicit null")
	}
	cost := wire["cost"].(map[string]any)
	if len(cost) != 1 || cost["basis"] != BasisUnknown {
		t.Fatalf("unknown cost must carry no amount or scale: %s", raw)
	}
	r.Usage = nil
	raw, _ = json.Marshal(r)
	if !strings.Contains(string(raw), `"usage":null`) {
		t.Fatalf("missing usage must be null: %s", raw)
	}
	var back Record
	if err := json.Unmarshal(raw, &back); err != nil || back.Cost.Basis != BasisUnknown || back.Usage != nil {
		t.Fatalf("roundtrip: %+v %v", back, err)
	}
}

func TestFromCountersNeverInventsZero(t *testing.T) {
	if FromCounters(map[string]int64{"requests": 1, "tool_calls": 1}) != nil {
		t.Fatal("non-token counters must not produce usage")
	}
	u := FromCounters(map[string]int64{"input_tokens": 10, "thought_tokens": 3})
	if *u.InputTokens != 10 || *u.ReasoningTokens != 3 || u.OutputTokens != nil || u.CacheReadTokens != nil || u.TotalTokens != nil {
		t.Fatalf("unexpected usage %+v", u)
	}
	u = FromCounters(map[string]int64{"uncached_input_tokens": 7, "cached_tokens": 5})
	if u.InputTokens == nil || *u.InputTokens != 12 || *u.CacheReadTokens != 5 {
		t.Fatalf("normalized input must include cached input: %+v", u)
	}
	if u = FromCounters(map[string]int64{"uncached_input_tokens": 7}); u != nil {
		t.Fatalf("input cannot be derived without cached input: %+v", u)
	}
}

func TestAccumulator(t *testing.T) {
	var a Accumulator
	a.Observe(map[string]int64{"input_tokens": 10, "output_tokens": 1}, true)
	a.Observe(map[string]int64{"output_tokens": 30}, true)
	a.Observe(map[string]int64{"output_tokens": 20}, true) // never decreases
	a.Observe(map[string]int64{"tool_use_tokens": 2}, false)
	a.Observe(map[string]int64{"tool_use_tokens": 3}, false)
	a.Observe(map[string]int64{"input_tokens": -1, "output_tokens": 99}, true) // rejected whole
	got := a.Snapshot()
	if got["input_tokens"] != 10 || got["output_tokens"] != 30 || got["tool_use_tokens"] != 5 || len(got) != 3 {
		t.Fatalf("accumulated %v", got)
	}
	a.Observe(map[string]int64{"tool_use_tokens": 1<<63 - 1}, false)
	if _, known := a.Snapshot()["tool_use_tokens"]; known {
		t.Fatal("overflowed counter must become unknown")
	}
}

func TestMemoryStoreTenantIsolationAndPaging(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(WithClock(func() time.Time { return base.Add(time.Hour) }))
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		at := base.Add(time.Duration(i/2) * time.Minute) // pairs share timestamps
		if err := s.Append(ctx, record("t1", id, at)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Append(ctx, record("t2", "z", base)); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, record("t1", "a", base)); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate id must conflict, got %v", err)
	}
	if _, err := s.Get(ctx, "t2", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("record visible across tenants")
	}
	var ids []string
	cursor := ""
	for {
		page, err := s.List(ctx, "t1", Query{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Records {
			if r.TenantID != "t1" {
				t.Fatal("foreign tenant listed")
			}
			ids = append(ids, r.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if strings.Join(ids, ",") != "e,d,c,b,a" {
		t.Fatalf("newest-first stable order, got %v", ids)
	}
	if _, err := s.List(ctx, "t1", Query{Cursor: "%%%"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("malformed cursor accepted")
	}
	if _, err := s.List(ctx, "t1", Query{Limit: MaxLimit + 1}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded limit accepted")
	}
}

func TestFilterAndVisibility(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(WithClock(func() time.Time { return base }))
	mine := record("t", "mine", base)
	asActor := record("t", "actor", base.Add(time.Second))
	asActor.PrincipalID, asActor.ActorID = "someone", "user-1"
	other := record("t", "other", base.Add(2*time.Second))
	other.PrincipalID, other.ActorID, other.Plane, other.ToolName, other.ModelID = "someone", "agent-2", PlaneMCP, "search", ""
	for _, r := range []Record{mine, asActor, other} {
		if err := s.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	page, _ := s.List(ctx, "t", Query{Filter: Filter{Visibility: []string{"user-1"}}})
	if len(page.Records) != 2 {
		t.Fatalf("visibility must allow principal or actor matches only: %+v", page.Records)
	}
	page, _ = s.List(ctx, "t", Query{Filter: Filter{Plane: PlaneMCP, ToolName: "search"}})
	if len(page.Records) != 1 || page.Records[0].ID != "other" {
		t.Fatalf("plane/tool filter: %+v", page.Records)
	}
	page, _ = s.List(ctx, "t", Query{Filter: Filter{Since: base.Add(time.Second), Until: base.Add(2 * time.Second)}})
	if len(page.Records) != 1 || page.Records[0].ID != "actor" {
		t.Fatalf("since inclusive, until exclusive: %+v", page.Records)
	}
}

func TestMemoryRetentionBounds(t *testing.T) {
	ctx := context.Background()
	now := base
	s := NewMemoryStore(WithRetention(time.Hour), WithMaxRecordsPerTenant(2), WithClock(func() time.Time { return now }))
	_ = s.Append(ctx, record("t", "old", base.Add(-2*time.Hour)))
	for _, id := range []string{"a", "b", "c"} {
		_ = s.Append(ctx, record("t", id, base.Add(time.Duration(id[0])*time.Millisecond)))
	}
	page, _ := s.List(ctx, "t", Query{})
	if len(page.Records) != 2 || page.Records[0].ID != "c" || page.Records[1].ID != "b" {
		t.Fatalf("retention must keep the newest bounded set: %+v", page.Records)
	}
	now = base.Add(3 * time.Hour)
	if _, err := s.Get(ctx, "t", "c"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired record readable")
	}
}

func TestSummaryKeepsUnknownDistinctAndSumsExactly(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(WithClock(func() time.Time { return base.Add(24 * time.Hour) }))
	known := record("t", "known", base)
	known.Usage = &Usage{InputTokens: Int64(100), OutputTokens: Int64(0)}
	known.Cost = Cost{Basis: BasisSettled, Currency: "USD", Amount: "15", Scale: 2} // 0.15
	unknown := record("t", "unknown", base.Add(time.Minute))
	unknown.Usage = nil
	partial := record("t", "partial", base.Add(2*time.Hour))
	partial.Usage = &Usage{InputTokens: Int64(5)}
	partial.Cost = Cost{Basis: BasisSettled, Currency: "USD", Amount: "1", Scale: 9} // 0.000000001
	partial.Status = StatusError
	eur := record("t", "eur", base.Add(3*time.Hour))
	eur.Cost = Cost{Basis: BasisEstimated, Currency: "EUR", Amount: "7", Scale: 0}
	eur.Status = StatusDenied
	other := record("t2", "foreign", base)
	other.Usage = &Usage{InputTokens: Int64(1 << 40)}
	for _, r := range []Record{known, unknown, partial, eur, other} {
		if err := s.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := Summarize(ctx, s, "t", SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Groups) != 1 || sum.RecordsScanned != 4 {
		t.Fatalf("one tenant-scoped group expected: %+v", sum)
	}
	g := sum.Groups[0]
	if g.Records != 4 || g.Requests != 4 || g.Errors != 1 || g.Denied != 1 {
		t.Fatalf("counts %+v", g)
	}
	if g.Tokens[CounterInput] != 105 || g.Tokens[CounterOutput] != 0 || g.TokensUnknown[CounterOutput] != 3 || g.TokensUnknown[CounterInput] != 2 {
		t.Fatalf("token sums must skip, and count, unknown counters: %+v %+v", g.Tokens, g.TokensUnknown)
	}
	if g.UnknownUsageRecords != 2 {
		t.Fatalf("records without usage counted: %d", g.UnknownUsageRecords)
	}
	if g.UnknownCostRecords != 1 || len(g.Cost) != 2 {
		t.Fatalf("cost groups %+v", g.Cost)
	}
	if c := g.Cost[1]; c.Currency != "USD" || c.Basis != BasisSettled || c.Amount != "150000001" || c.Scale != 9 || c.Records != 2 {
		t.Fatalf("exact USD sum after rescale: %+v", c)
	}
	if c := g.Cost[0]; c.Currency != "EUR" || c.Amount != "7" {
		t.Fatalf("currencies never mix: %+v", c)
	}
	byStatus, err := Summarize(ctx, s, "t", SummaryQuery{GroupBy: []string{"status"}, Bucket: BucketHour})
	if err != nil || len(byStatus.Groups) != 3 {
		t.Fatalf("status x hour groups: %+v %v", byStatus, err)
	}
	first := byStatus.Groups[0]
	if !first.BucketStart.Equal(base.Truncate(time.Hour)) || first.Key["status"] != StatusOK || first.Records != 2 {
		t.Fatalf("first bucket %+v", first)
	}
	if _, err := ParseGroupBy("plane,secret"); err == nil {
		t.Fatal("unknown group_by accepted")
	}
	if _, err := ParseGroupBy("plane,plane"); err == nil {
		t.Fatal("repeated group_by accepted")
	}
	if ValidateBucket("week") == nil {
		t.Fatal("unknown bucket accepted")
	}
	day, _ := Summarize(ctx, s, "t", SummaryQuery{Bucket: BucketDay})
	if len(day.Groups) != 1 || !day.Groups[0].BucketStart.Equal(time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("day bucket %+v", day.Groups)
	}
}

func price(id string, meters map[string]Rational) Price {
	return Price{ID: id, ModelID: "gemini-2.5-flash", Currency: "USD", Scale: 9, Meters: meters}
}

func TestPriceValidation(t *testing.T) {
	if _, err := DecodePrice("p", []byte(`{"id":"p","model_id":"m","currency":"USD","scale":9,"meters":{"input_tokens":{"numerator":1,"denominator":1}}}`)); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"id mismatch":   `{"id":"q","model_id":"m","currency":"USD","meters":{"requests":{"numerator":1,"denominator":1}}}`,
		"unknown field": `{"id":"p","model_id":"m","currency":"USD","meters":{"requests":{"numerator":1,"denominator":1}},"usd_per_token":0.1}`,
		"unknown meter": `{"id":"p","model_id":"m","currency":"USD","meters":{"vibes":{"numerator":1,"denominator":1}}}`,
		"zero denom":    `{"id":"p","model_id":"m","currency":"USD","meters":{"requests":{"numerator":1,"denominator":0}}}`,
		"negative":      `{"id":"p","model_id":"m","currency":"USD","meters":{"requests":{"numerator":-1,"denominator":1}}}`,
		"no meters":     `{"id":"p","model_id":"m","currency":"USD","meters":{}}`,
		"currency":      `{"id":"p","model_id":"m","currency":"US","meters":{"requests":{"numerator":1,"denominator":1}}}`,
		"no model":      `{"id":"p","currency":"USD","meters":{"requests":{"numerator":1,"denominator":1}}}`,
		"window":        `{"id":"p","model_id":"m","currency":"USD","meters":{"requests":{"numerator":1,"denominator":1}},"effective_from":"2026-01-02T00:00:00Z","effective_until":"2026-01-01T00:00:00Z"}`,
		"trailing":      `{"id":"p","model_id":"m","currency":"USD","meters":{"requests":{"numerator":1,"denominator":1}}} {}`,
	} {
		if _, err := DecodePrice("p", []byte(raw)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestEstimateIsExactAndNeverInvented(t *testing.T) {
	r := record("t", "a", base)
	r.Usage = &Usage{InputTokens: Int64(1), OutputTokens: Int64(1), ReasoningTokens: Int64(0)}
	// Thirds would round to 3 units if each term were rounded; the exact sum is 1.
	thirds := price("thirds", map[string]Rational{MeterInput: {1, 3}, MeterOutput: {1, 3}, MeterRequests: {1, 3}})
	if c := Estimate([]Price{thirds}, r); c.Basis != BasisEstimated || c.Amount != "1" || c.Currency != "USD" || c.Scale != 9 {
		t.Fatalf("exact rational sum: %+v", c)
	}
	// $0.30 / 1M input and $2.50 / 1M output at nano-dollar scale.
	list := price("list", map[string]Rational{MeterInput: {300_000_000, 1_000_000}, MeterOutput: {2_500_000_000, 1_000_000}})
	r.Usage = &Usage{InputTokens: Int64(1_234_567), OutputTokens: Int64(89)}
	// 1234567*300 = 370370100 ; 89*2500 = 222500 ; total 370592600 nano-USD.
	if c := Estimate([]Price{list}, r); c.Amount != "370592600" {
		t.Fatalf("list price: %+v", c)
	}
	// Ceil once for a fractional total.
	r.Usage = &Usage{InputTokens: Int64(1), OutputTokens: Int64(0)}
	if c := Estimate([]Price{price("x", map[string]Rational{MeterInput: {1, 2}, MeterOutput: {1, 1}})}, r); c.Amount != "1" {
		t.Fatalf("half rounds up once: %+v", c)
	}
	// Unknown quantity for a priced meter: unknown.
	r.Usage = &Usage{InputTokens: Int64(10)}
	if c := Estimate([]Price{list}, r); c.Basis != BasisUnknown {
		t.Fatalf("unknown output priced as zero: %+v", c)
	}
	// A declared zero price tolerates an unknown quantity.
	free := price("free", map[string]Rational{MeterInput: {1, 1}, MeterOutput: {0, 1}})
	if c := Estimate([]Price{free}, r); c.Amount != "10" {
		t.Fatalf("zero-priced unknown meter: %+v", c)
	}
	// Known non-zero quantity without a price: unknown.
	r.Usage = &Usage{InputTokens: Int64(10), OutputTokens: Int64(1), CacheWriteTokens: Int64(4)}
	if c := Estimate([]Price{list}, r); c.Basis != BasisUnknown {
		t.Fatalf("unpriced cache writes ignored: %+v", c)
	}
	// Cached input priced separately charges input only for the remainder.
	cache := price("cache", map[string]Rational{MeterInput: {10, 1}, MeterCacheRead: {1, 1}, MeterOutput: {0, 1}})
	r.Usage = &Usage{InputTokens: Int64(10), CacheReadTokens: Int64(4), OutputTokens: Int64(0)}
	if c := Estimate([]Price{cache}, r); c.Amount != "64" {
		t.Fatalf("6*10 + 4*1: %+v", c)
	}
	// No usage at all: only a request-only price can estimate.
	r.Usage = nil
	if c := Estimate([]Price{list}, r); c.Basis != BasisUnknown {
		t.Fatalf("missing usage estimated: %+v", c)
	}
	if c := Estimate([]Price{price("req", map[string]Rational{MeterRequests: {5, 1}})}, r); c.Amount != "5" {
		t.Fatalf("request price: %+v", c)
	}
}

func TestPriceMatching(t *testing.T) {
	r := record("t", "a", base)
	r.Usage = &Usage{InputTokens: Int64(1)}
	generic := price("generic", map[string]Rational{MeterInput: {1, 1}})
	specific := price("specific", map[string]Rational{MeterInput: {2, 1}})
	specific.Provider = "google"
	if c := Estimate([]Price{generic, specific}, r); c.Amount != "2" {
		t.Fatalf("provider-specific price must win: %+v", c)
	}
	twin := specific
	twin.ID = "twin"
	if c := Estimate([]Price{specific, twin}, r); c.Basis != BasisUnknown {
		t.Fatalf("ambiguous prices must not be guessed: %+v", c)
	}
	future := generic
	from := base.Add(time.Hour)
	future.EffectiveFrom = &from
	if c := Estimate([]Price{future}, r); c.Basis != BasisUnknown {
		t.Fatalf("price outside its window applied: %+v", c)
	}
	until := base
	expired := generic
	expired.EffectiveUntil = &until
	if c := Estimate([]Price{expired}, r); c.Basis != BasisUnknown {
		t.Fatalf("effective_until is exclusive: %+v", c)
	}
	other := generic
	other.ModelID = "other"
	if c := Estimate([]Price{other}, r); c.Basis != BasisUnknown {
		t.Fatalf("other model price applied: %+v", c)
	}
}

type failingStore struct{ Store }

func (failingStore) Append(context.Context, Record) error { return errors.New("down") }

func TestRecorderReportsFailuresWithoutPropagating(t *testing.T) {
	var mu sync.Mutex
	var failures []error
	rec := NewRecorder(failingStore{}, WithErrorHandler(func(_ Record, err error) { mu.Lock(); failures = append(failures, err); mu.Unlock() }), WithConcurrency(1))
	for i := 0; i < 5; i++ {
		rec.Submit(context.Background(), func(context.Context) (Record, error) { return record("t", NewID(), base), nil })
	}
	rec.Submit(context.Background(), func(context.Context) (Record, error) { panic("bug") })
	rec.Flush()
	if len(failures) != 6 {
		t.Fatalf("every failure reported once: %v", failures)
	}
	ok := NewMemoryStore(WithClock(func() time.Time { return base }))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	recorder := NewRecorder(ok)
	recorder.Submit(cancelled, func(ctx context.Context) (Record, error) { return record("t", "kept", base), ctx.Err() })
	recorder.Flush()
	if _, err := ok.Get(context.Background(), "t", "kept"); err != nil {
		t.Fatal("a cancelled request context must not drop its record", err)
	}
}

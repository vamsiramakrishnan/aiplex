package usageledger

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Every per-TTL cache-write counter the observers emit is recorded as a subset
// of cache_write_tokens, kept out of the token totals, and round-trips in JSON.
func TestCacheWriteTTLSubsetsAreRecorded(t *testing.T) {
	anthropic := FromCounters(map[string]int64{"input_tokens": 100, "output_tokens": 3, "cached_tokens": 40, "cache_write_tokens": 30, "cache_write_5m_tokens": 10, "cache_write_1h_tokens": 20})
	if anthropic == nil || *anthropic.CacheWrite5mTokens != 10 || *anthropic.CacheWrite1hTokens != 20 || anthropic.CacheWrite30mTokens != nil || anthropic.CacheWrite12hTokens != nil {
		t.Fatalf("anthropic subsets: %+v", anthropic)
	}
	openai := FromCounters(map[string]int64{"input_tokens": 100, "output_tokens": 3, "cached_tokens": 40, "cache_write_tokens": 30, "cache_write_30m_tokens": 12, "cache_write_12h_tokens": 18})
	if openai == nil || *openai.CacheWrite30mTokens != 12 || *openai.CacheWrite12hTokens != 18 || openai.CacheWrite5mTokens != nil || openai.CacheWrite1hTokens != nil {
		t.Fatalf("openai subsets: %+v", openai)
	}
	for _, name := range []string{CounterCacheWrite5m, CounterCacheWrite1h, CounterCacheWrite30m, CounterCacheWrite12h} {
		if !HeldUnit(name) {
			t.Errorf("%s is not held", name)
		}
	}
	raw, err := json.Marshal(openai)
	if err != nil || !strings.Contains(string(raw), `"cache_write_30m_tokens":12`) || !strings.Contains(string(raw), `"cache_write_12h_tokens":18`) || strings.Contains(string(raw), "cache_write_5m_tokens") {
		t.Fatalf("json %s %v", raw, err)
	}
	var back Usage
	if err := json.Unmarshal(raw, &back); err != nil || *back.CacheWrite12hTokens != 18 || *back.CacheWrite30mTokens != 12 {
		t.Fatalf("round trip %+v %v", back, err)
	}
	// A negative subset is invalid like every other held counter.
	r := priceRecord("m", &Usage{CacheWriteTokens: Int64(1)})
	r.Usage.CacheWrite12hTokens = Int64(-1)
	if r.Validate() == nil {
		t.Fatal("negative 12h subset accepted")
	}
}

// A record stored before the per-TTL fields existed has none of them: it reads
// back unchanged, its subsets are unknown, and its cost is what it was.
func TestRecordWrittenBeforeTTLFieldsStillReads(t *testing.T) {
	const stored = `{"id":"old","tenant_id":"t","started_at":"2026-01-02T03:04:05Z","finished_at":"2026-01-02T03:04:05Z","plane":"llmplex","provider":"anthropic","model_id":"m","status":"ok","source":"managed","requests":1,"usage":{"input_tokens":100,"output_tokens":0,"cache_read_tokens":null,"cache_write_tokens":30,"reasoning_tokens":null,"total_tokens":null,"cache_write_1h_tokens":10},"cost":{"basis":"unknown"}}`
	var r Record
	if err := json.Unmarshal([]byte(stored), &r); err != nil {
		t.Fatal(err)
	}
	if err := r.Validate(); err != nil {
		t.Fatal("old record no longer valid:", err)
	}
	if r.Usage.CacheWrite5mTokens != nil || r.Usage.CacheWrite30mTokens != nil || r.Usage.CacheWrite12hTokens != nil || *r.Usage.CacheWrite1hTokens != 10 {
		t.Fatalf("old record subsets: %+v", r.Usage)
	}
	raw, _ := json.Marshal(r.Usage)
	for _, name := range []string{"cache_write_5m_tokens", "cache_write_30m_tokens", "cache_write_12h_tokens"} {
		if strings.Contains(string(raw), name) {
			t.Fatalf("unknown %s serialized: %s", name, raw)
		}
	}
	// The old record is priced exactly as before: 70 input + 20 base writes*2 + 10 one-hour*3.
	p := ttlPrice(map[string]int64{MeterCacheWrite1h: 3})
	if got := EstimateWith(p, r); got.Amount != "140" {
		t.Fatalf("old record cost %+v", got)
	}
}

func ttlRate(n int64) Rational { return Rational{Numerator: n, Denominator: 1} }

// ttlPrice rates input 1, output 1 and the cache-write base 2, plus the given
// per-TTL rates.
func ttlPrice(subsets map[string]int64) Price {
	p := Price{ID: "p", ModelID: "m", Currency: "USD", Scale: 6, Meters: map[string]Rational{MeterInput: ttlRate(1), MeterOutput: ttlRate(1), MeterCacheWrite: ttlRate(2)}}
	for name, n := range subsets {
		p.Meters[name] = ttlRate(n)
	}
	return p
}

func ttlUsage(write int64, subsets map[string]int64) *Usage {
	u := &Usage{InputTokens: Int64(100), OutputTokens: Int64(0), CacheWriteTokens: Int64(write)}
	for name, n := range subsets {
		*u.UnitUsage.field(name) = Int64(n)
	}
	return u
}

func TestCacheWriteTTLRates(t *testing.T) {
	for _, test := range []struct {
		name   string
		rates  map[string]int64
		usage  *Usage
		amount string // "" is unknown
	}{
		// Anthropic: 5m is what the base rate covers; a 5m rate replaces it.
		{"5m rate", map[string]int64{MeterCacheWrite5m: 5}, ttlUsage(30, map[string]int64{CounterCacheWrite5m: 30, CounterCacheWrite1h: 0}), "220"},
		{"5m and 1h mix", map[string]int64{MeterCacheWrite5m: 5, MeterCacheWrite1h: 3}, ttlUsage(30, map[string]int64{CounterCacheWrite5m: 10, CounterCacheWrite1h: 20}), "180"},
		{"5m unrated stays at base", map[string]int64{MeterCacheWrite1h: 3}, ttlUsage(30, map[string]int64{CounterCacheWrite5m: 10, CounterCacheWrite1h: 20}), "150"},
		// OpenAI Responses: 30m is the base, 12h its own rate.
		{"12h rate", map[string]int64{MeterCacheWrite12h: 7}, ttlUsage(30, map[string]int64{CounterCacheWrite30m: 12, CounterCacheWrite12h: 18}), "220"},
		{"30m and 12h mix", map[string]int64{MeterCacheWrite30m: 4, MeterCacheWrite12h: 7}, ttlUsage(30, map[string]int64{CounterCacheWrite30m: 12, CounterCacheWrite12h: 18}), "244"},
		{"30m rate alone", map[string]int64{MeterCacheWrite30m: 4}, ttlUsage(30, map[string]int64{CounterCacheWrite30m: 30, CounterCacheWrite12h: 0}), "190"},
		// (b) a long TTL with a count and no rate is unknown, never the base rate.
		{"12h unrated", nil, ttlUsage(30, map[string]int64{CounterCacheWrite30m: 12, CounterCacheWrite12h: 18}), ""},
		{"12h unrated beside a 30m rate", map[string]int64{MeterCacheWrite30m: 4}, ttlUsage(30, map[string]int64{CounterCacheWrite30m: 12, CounterCacheWrite12h: 18}), ""},
		{"1h unrated beside a 5m rate", map[string]int64{MeterCacheWrite5m: 5}, ttlUsage(30, map[string]int64{CounterCacheWrite5m: 10, CounterCacheWrite1h: 20}), ""},
		{"12h zero needs no rate", nil, ttlUsage(30, map[string]int64{CounterCacheWrite30m: 30, CounterCacheWrite12h: 0}), "130"},
		// (c) a rated subset whose count is unknown is unknown.
		{"12h rated, breakdown unknown", map[string]int64{MeterCacheWrite12h: 7}, ttlUsage(30, nil), ""},
		{"5m rated, breakdown unknown", map[string]int64{MeterCacheWrite5m: 5}, ttlUsage(30, nil), ""},
		{"30m rated, 30m unknown", map[string]int64{MeterCacheWrite30m: 4, MeterCacheWrite12h: 7}, ttlUsage(30, map[string]int64{CounterCacheWrite12h: 18}), ""},
		// A reported zero total means every TTL bucket is zero.
		{"zero total", map[string]int64{MeterCacheWrite5m: 5, MeterCacheWrite1h: 3, MeterCacheWrite30m: 4, MeterCacheWrite12h: 7}, ttlUsage(0, nil), "100"},
		// Subsets that exceed their parent are contradictory: unknown.
		{"subset above parent", map[string]int64{MeterCacheWrite12h: 7}, ttlUsage(30, map[string]int64{CounterCacheWrite12h: 31}), ""},
		{"subsets together above parent", map[string]int64{MeterCacheWrite30m: 4, MeterCacheWrite12h: 7}, ttlUsage(30, map[string]int64{CounterCacheWrite30m: 20, CounterCacheWrite12h: 20}), ""},
		{"unrated subsets above parent", nil, ttlUsage(30, map[string]int64{CounterCacheWrite5m: 20, CounterCacheWrite30m: 20}), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := ttlPrice(test.rates)
			if err := p.Validate(); err != nil {
				t.Fatal(err)
			}
			got := Estimate([]Price{p}, priceRecord("m", test.usage))
			if test.amount == "" {
				if got.Basis != BasisUnknown {
					t.Fatalf("want unknown, got %+v", got)
				}
				return
			}
			if got.Basis != BasisEstimated || got.Amount != test.amount {
				t.Fatalf("want %s, got %+v", test.amount, got)
			}
		})
	}
}

// A positive subset with an unknown cache_write_tokens total means the total
// is at least the subset: it cannot be priced, so the cost is unknown, never
// estimated without those writes. Subsets that are zero or unknown leave such
// a record as it was.
func TestCacheWriteTTLWithUnknownTotal(t *testing.T) {
	flat := Price{ID: "p", ModelID: "m", Currency: "USD", Scale: 6, Meters: map[string]Rational{MeterInput: ttlRate(1), MeterOutput: ttlRate(1)}}
	freeWrites := ttlPrice(map[string]int64{MeterCacheWrite1h: 3})
	freeWrites.Meters[MeterCacheWrite] = ttlRate(0)
	nilTotal := func(subsets map[string]int64) *Usage {
		u := ttlUsage(0, subsets)
		u.CacheWriteTokens = nil
		return u
	}
	for _, test := range []struct {
		name   string
		price  Price
		usage  *Usage
		amount string // "" is unknown
	}{
		{"positive 5m, flat price", flat, nilTotal(map[string]int64{CounterCacheWrite5m: 50}), ""},
		{"positive 30m, flat price", flat, nilTotal(map[string]int64{CounterCacheWrite30m: 50}), ""},
		{"positive 1h, unpriced", flat, nilTotal(map[string]int64{CounterCacheWrite1h: 50}), ""},
		// Already unknown before the unknown-total rule: the priced
		// cache_write_tokens left input_tokens without a known remainder.
		{"positive 1h, priced", freeWrites, nilTotal(map[string]int64{CounterCacheWrite1h: 50}), ""},
		{"zero subsets", flat, nilTotal(map[string]int64{CounterCacheWrite5m: 0, CounterCacheWrite1h: 0}), "100"},
		{"no subsets", flat, nilTotal(nil), "100"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := EstimateWith(test.price, priceRecord("m", test.usage))
			if test.amount == "" {
				if got.Basis != BasisUnknown {
					t.Fatalf("want unknown, got %+v", got)
				}
				return
			}
			if got.Basis != BasisEstimated || got.Amount != test.amount {
				t.Fatalf("want %s, got %+v", test.amount, got)
			}
		})
	}
}

// (a) A per-TTL rate needs the cache-write base rate, as the 1h rate does.
func TestCacheWriteTTLRateNeedsParent(t *testing.T) {
	for _, meter := range []string{MeterCacheWrite5m, MeterCacheWrite1h, MeterCacheWrite30m, MeterCacheWrite12h} {
		p := Price{ID: "p", ModelID: "m", Currency: "USD", Scale: 6, Meters: map[string]Rational{MeterInput: ttlRate(1), meter: ttlRate(3)}}
		if err := p.Validate(); err == nil || !strings.Contains(err.Error(), meter) {
			t.Errorf("%s without cache_write_tokens: %v", meter, err)
		}
		p.Meters[MeterCacheWrite] = ttlRate(2)
		if err := p.Validate(); err != nil {
			t.Errorf("%s with cache_write_tokens: %v", meter, err)
		}
	}
}

// A context tier repeats every per-TTL meter of the base price.
func TestCacheWriteTTLRatesInTiers(t *testing.T) {
	p := ttlPrice(map[string]int64{MeterCacheWrite12h: 7})
	p.Tiers = []PriceTier{{AboveInputTokens: 50, Meters: map[string]Rational{MeterInput: ttlRate(2), MeterOutput: ttlRate(2), MeterCacheWrite: ttlRate(4), MeterCacheWrite12h: ttlRate(14)}}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	// 70 uncached*2 + 12 base*4 + 18 twelve-hour*14 = 440.
	if got := EstimateWith(p, priceRecord("m", ttlUsage(30, map[string]int64{CounterCacheWrite30m: 12, CounterCacheWrite12h: 18}))); got.Amount != "440" {
		t.Fatalf("%+v", got)
	}
	p.Tiers[0].Meters = map[string]Rational{MeterInput: ttlRate(2), MeterOutput: ttlRate(2), MeterCacheWrite: ttlRate(4)}
	if p.Validate() == nil {
		t.Fatal("tier without the 12h meter accepted")
	}
}

// Summaries sum each subset beside the token totals, never into them.
func TestSummaryKeepsTTLSubsetsOutOfTotals(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i, u := range []*Usage{
		ttlUsage(30, map[string]int64{CounterCacheWrite5m: 10, CounterCacheWrite1h: 20}),
		ttlUsage(30, map[string]int64{CounterCacheWrite30m: 12, CounterCacheWrite12h: 18}),
		ttlUsage(5, nil),
	} {
		r := priceRecord("m", u)
		r.ID = string(rune('a' + i))
		if err := store.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Summarize(ctx, store, "t", SummaryQuery{Filter: Filter{Since: at.Add(-time.Hour)}})
	if err != nil || len(s.Groups) != 1 {
		t.Fatalf("%+v %v", s, err)
	}
	g := s.Groups[0]
	if g.Tokens[CounterCacheWrite] != 65 || g.Tokens[CounterInput] != 300 {
		t.Fatalf("token totals changed by subsets: %v", g.Tokens)
	}
	want := map[string]int64{CounterCacheWrite5m: 10, CounterCacheWrite1h: 20, CounterCacheWrite30m: 12, CounterCacheWrite12h: 18}
	for name, n := range want {
		if g.ModalityTokens[name] != n {
			t.Errorf("%s: %d, want %d (%v)", name, g.ModalityTokens[name], n, g.ModalityTokens)
		}
	}
}

// A record written before its price had a 12h rate was unknown; backfill
// prices it once the rate exists.
func TestBackfillPricesTTLRecordAfterRateExists(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	r := priceRecord("m", ttlUsage(30, map[string]int64{CounterCacheWrite30m: 12, CounterCacheWrite12h: 18}))
	before := ttlPrice(nil)
	if r.Cost = Estimate([]Price{before}, r); r.Cost.Basis != BasisUnknown {
		t.Fatalf("12h writes priced without a 12h rate: %+v", r.Cost)
	}
	if err := store.Append(ctx, r); err != nil {
		t.Fatal(err)
	}
	if res, err := Backfill(ctx, store, "t", []Price{before}, BackfillQuery{}, at.Add(time.Hour)); err != nil || res.Updated != 0 || res.Skipped.NoPrice != 1 {
		t.Fatalf("backfill without the rate: %+v %v", res, err)
	}
	res, err := Backfill(ctx, store, "t", []Price{ttlPrice(map[string]int64{MeterCacheWrite12h: 7})}, BackfillQuery{}, at.Add(time.Hour))
	if err != nil || res.Updated != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	got, _ := store.Get(ctx, "t", "r")
	if got.Cost.Basis != BasisEstimated || got.Cost.Amount != "220" || got.Cost.BackfilledAt == nil {
		t.Fatalf("backfilled cost %+v", got.Cost)
	}
}

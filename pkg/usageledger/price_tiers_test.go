package usageledger

import (
	"context"
	"testing"
	"time"
)

func priceRecord(model string, usage *Usage) Record {
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	return Record{ID: "r", TenantID: "t", StartedAt: at, FinishedAt: at, Plane: PlaneLLM, Provider: "anthropic", ModelID: model, Status: StatusOK, Source: SourceManaged, Requests: 1, Usage: usage, Cost: UnknownCost()}
}

func tieredPrice() Price {
	// $1 per Mtok input and $5 output; above 100k input tokens $2 and $7.5.
	rate := func(n int64) Rational { return Rational{Numerator: n, Denominator: 1_000_000} }
	return Price{ID: "p", ModelID: "m", Currency: "USD", Scale: 6,
		Meters: map[string]Rational{MeterInput: rate(1_000_000), MeterOutput: rate(5_000_000)},
		Tiers:  []PriceTier{{AboveInputTokens: 100_000, Meters: map[string]Rational{MeterInput: rate(2_000_000), MeterOutput: rate(7_500_000)}}}}
}

func TestEstimateTierAppliesToWholeRequest(t *testing.T) {
	p := tieredPrice()
	// 100000 input is not above the threshold: 100000*1 + 1000*5 = 105000 micro-USD.
	got := Estimate([]Price{p}, priceRecord("m", &Usage{InputTokens: Int64(100_000), OutputTokens: Int64(1000)}))
	if got.Amount != "105000" || got.Basis != BasisEstimated || got.PriceID != "p" {
		t.Fatalf("at threshold: %+v", got)
	}
	// 100001 input: whole request at the higher rate: 100001*2 + 1000*7.5 = 207502.
	got = Estimate([]Price{p}, priceRecord("m", &Usage{InputTokens: Int64(100_001), OutputTokens: Int64(1000)}))
	if got.Amount != "207502" {
		t.Fatalf("above threshold: %+v", got)
	}
	// Unknown input cannot select a tier.
	got = Estimate([]Price{p}, priceRecord("m", &Usage{OutputTokens: Int64(1000)}))
	if got.Basis != BasisUnknown {
		t.Fatalf("unknown input must give unknown cost: %+v", got)
	}
}

func TestTierValidation(t *testing.T) {
	rate := Rational{Numerator: 1, Denominator: 1}
	base := func() Price {
		return Price{ID: "p", ModelID: "m", Currency: "USD", Scale: 6, Meters: map[string]Rational{MeterInput: rate, MeterOutput: rate}}
	}
	for name, tiers := range map[string][]PriceTier{
		"missing meter":    {{AboveInputTokens: 10, Meters: map[string]Rational{MeterInput: rate}}},
		"extra meter":      {{AboveInputTokens: 10, Meters: map[string]Rational{MeterInput: rate, MeterOutput: rate, MeterCacheRead: rate}}},
		"zero threshold":   {{AboveInputTokens: 0, Meters: map[string]Rational{MeterInput: rate, MeterOutput: rate}}},
		"not increasing":   {{AboveInputTokens: 10, Meters: map[string]Rational{MeterInput: rate, MeterOutput: rate}}, {AboveInputTokens: 10, Meters: map[string]Rational{MeterInput: rate, MeterOutput: rate}}},
		"negative numer.":  {{AboveInputTokens: 10, Meters: map[string]Rational{MeterInput: {Numerator: -1, Denominator: 1}, MeterOutput: rate}}},
		"unknown meter":    {{AboveInputTokens: 10, Meters: map[string]Rational{MeterInput: rate, "bogus": rate}}},
		"zero denominator": {{AboveInputTokens: 10, Meters: map[string]Rational{MeterInput: rate, MeterOutput: {Numerator: 1}}}},
	} {
		p := base()
		p.Tiers = tiers
		if p.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := base()
	ok.Tiers = []PriceTier{{AboveInputTokens: 10, Meters: map[string]Rational{MeterInput: rate, MeterOutput: rate}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestModalityAwarePriceRefusesUnpricedModality(t *testing.T) {
	rate := func(n int64) Rational { return Rational{Numerator: n, Denominator: 1} }
	p := Price{ID: "p", ModelID: "m", Currency: "USD", Scale: 6, Meters: map[string]Rational{MeterInput: rate(1), MeterOutput: rate(2), CounterInputAudioTokens: rate(10)}}
	usage := func(audio, image int64) *Usage {
		u := &Usage{InputTokens: Int64(100 + audio + image), OutputTokens: Int64(5)}
		u.InputAudioTokens, u.InputImageTokens = Int64(audio), Int64(image)
		return u
	}
	// 100 text*1 + 7 audio*10 + 5 output*2 = 180.
	if got := Estimate([]Price{p}, priceRecord("m", usage(7, 0))); got.Amount != "180" {
		t.Fatalf("audio priced: %+v", got)
	}
	// Image tokens reported with no image rate: the price separates
	// modalities, so the cost is unknown rather than partially costed.
	if got := Estimate([]Price{p}, priceRecord("m", usage(7, 3))); got.Basis != BasisUnknown {
		t.Fatalf("unpriced image: %+v", got)
	}
	// A flat price covers every modality at its parent rate.
	flat := Price{ID: "f", ModelID: "m", Currency: "USD", Scale: 6, Meters: map[string]Rational{MeterInput: rate(1), MeterOutput: rate(2)}}
	if got := Estimate([]Price{flat}, priceRecord("m", usage(7, 3))); got.Amount != "120" {
		t.Fatalf("flat price: %+v", got)
	}
}

func TestOneHourCacheWriteRate(t *testing.T) {
	rate := func(n int64) Rational { return Rational{Numerator: n, Denominator: 1} }
	p := Price{ID: "p", ModelID: "m", Currency: "USD", Scale: 6, Meters: map[string]Rational{MeterInput: rate(1), MeterOutput: rate(1), MeterCacheWrite: rate(2), MeterCacheWrite1h: rate(3)}}
	u := &Usage{InputTokens: Int64(100), OutputTokens: Int64(0), CacheWriteTokens: Int64(30)}
	u.CacheWrite1hTokens = Int64(10)
	// uncached 70*1 + 20 five-minute writes*2 + 10 one-hour writes*3 = 140.
	if got := Estimate([]Price{p}, priceRecord("m", u)); got.Amount != "140" {
		t.Fatalf("%+v", got)
	}
	// One-hour writes with no one-hour rate: unknown, not priced at the 5m rate.
	noHour := p
	noHour.Meters = map[string]Rational{MeterInput: rate(1), MeterOutput: rate(1), MeterCacheWrite: rate(2)}
	if got := Estimate([]Price{noHour}, priceRecord("m", u)); got.Basis != BasisUnknown {
		t.Fatalf("%+v", got)
	}
	// A positive total without a TTL breakdown cannot be priced per TTL.
	u2 := &Usage{InputTokens: Int64(100), OutputTokens: Int64(0), CacheWriteTokens: Int64(30)}
	if got := Estimate([]Price{p}, priceRecord("m", u2)); got.Basis != BasisUnknown {
		t.Fatalf("%+v", got)
	}
	// A reported zero total implies zero one-hour writes.
	u3 := &Usage{InputTokens: Int64(100), OutputTokens: Int64(0), CacheWriteTokens: Int64(0)}
	if got := Estimate([]Price{p}, priceRecord("m", u3)); got.Amount != "100" {
		t.Fatalf("%+v", got)
	}
	// The one-hour meter needs the cache-write meter.
	bad := p
	bad.Meters = map[string]Rational{MeterInput: rate(1), MeterCacheWrite1h: rate(3)}
	if bad.Validate() == nil {
		t.Fatal("one-hour meter without cache_write_tokens accepted")
	}
}

func TestServiceTierSelector(t *testing.T) {
	rate := func(n int64) Rational { return Rational{Numerator: n, Denominator: 1} }
	std := Price{ID: "std", ModelID: "m", Currency: "USD", Scale: 6, Meters: map[string]Rational{MeterInput: rate(1)}}
	prio := Price{ID: "prio", ModelID: "m", Currency: "USD", Scale: 6, ServiceTier: "priority", Meters: map[string]Rational{MeterInput: rate(2)}}
	prices := []Price{std, prio}
	usage := &Usage{InputTokens: Int64(10)}
	for tier, want := range map[string]string{"": "10", "default": "10", "priority": "20"} {
		r := priceRecord("m", usage)
		r.ServiceTier = tier
		if got := Estimate(prices, r); got.Amount != want {
			t.Errorf("tier %q: %+v want %s", tier, got, want)
		}
	}
	// A tier with no matching price is unknown, never the standard rate.
	r := priceRecord("m", usage)
	r.ServiceTier = "flex"
	if got := Estimate(prices, r); got.Basis != BasisUnknown {
		t.Fatalf("flex: %+v", got)
	}
}

func TestServerToolMeters(t *testing.T) {
	rate := func(n, d int64) Rational { return Rational{Numerator: n, Denominator: d} }
	// $10 per 1000 searches at scale 6 is 10,000,000 micro-USD per 1000 calls.
	p := Price{ID: "p", ModelID: "m", Currency: "USD", Scale: 6, Meters: map[string]Rational{MeterInput: rate(1, 1), ServerToolMeter("web_search"): rate(10_000_000, 1000)}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	r := priceRecord("m", &Usage{InputTokens: Int64(100)})
	r.ServerTools = map[string]int64{"web_search": 3}
	if got := Estimate([]Price{p}, r); got.Amount != "30100" {
		t.Fatalf("%+v", got)
	}
	// A tool kind without a meter is unknown, not free.
	r.ServerTools = map[string]int64{"web_search": 3, "web_fetch": 1}
	if got := Estimate([]Price{p}, r); got.Basis != BasisUnknown {
		t.Fatalf("%+v", got)
	}
	// No tool reported: the priced meter contributes nothing.
	r.ServerTools = nil
	if got := Estimate([]Price{p}, r); got.Amount != "100" {
		t.Fatalf("%+v", got)
	}
	// A price without any tool meter cannot cost a record that used a tool.
	plain := Price{ID: "q", ModelID: "m", Currency: "USD", Scale: 6, Meters: map[string]Rational{MeterInput: rate(1, 1)}}
	r.ServerTools = map[string]int64{"web_search": 1}
	if got := Estimate([]Price{plain}, r); got.Basis != BasisUnknown {
		t.Fatalf("%+v", got)
	}
	// A zero-count tool needs no rate.
	r.ServerTools = map[string]int64{"web_search": 0}
	if got := Estimate([]Price{plain}, r); got.Amount != "100" {
		t.Fatalf("%+v", got)
	}
	bad := p
	bad.Meters = map[string]Rational{MeterInput: rate(1, 1), "server_tool.Web Search": rate(1, 1)}
	if bad.Validate() == nil {
		t.Fatal("invalid kind accepted")
	}
}

func TestRoundingUpAtScaleAndExactSummaries(t *testing.T) {
	// 37 tokens at 1/4 micro-USD per token is exactly 9.25 micro-USD: the
	// stored amount rounds up to 10 and Exact keeps 37/4.
	p := Price{ID: "p", ModelID: "m", Currency: "USD", Scale: 6, Meters: map[string]Rational{MeterInput: {Numerator: 1, Denominator: 4}}}
	r := priceRecord("m", &Usage{InputTokens: Int64(37)})
	cost := Estimate([]Price{p}, r)
	if cost.Amount != "10" || cost.Exact != "37/4" || cost.Validate() != nil {
		t.Fatalf("%+v", cost)
	}
	// A whole amount carries no Exact.
	if c := Estimate([]Price{p}, priceRecord("m", &Usage{InputTokens: Int64(36)})); c.Amount != "9" || c.Exact != "" {
		t.Fatalf("%+v", c)
	}
	// Four such records sum to 37, not 40, because exact values are added
	// before the one rounding (4 * 9.25 = 37 exactly).
	store := NewMemoryStore()
	for i, id := range []string{"a", "b", "c", "d"} {
		rec := priceRecord("m", &Usage{InputTokens: Int64(37)})
		rec.ID, rec.Cost = id, cost
		rec.StartedAt = rec.StartedAt.Add(time.Duration(i) * time.Second)
		rec.FinishedAt = rec.StartedAt
		if err := store.Append(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := Summarize(context.Background(), store, "t", SummaryQuery{Filter: Filter{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Groups) != 1 || len(summary.Groups[0].Cost) != 1 || summary.Groups[0].Cost[0].Amount != "37" {
		t.Fatalf("%+v", summary.Groups)
	}
	// Five records: 46.25 rounds up once to 47 (five rounded records would give 50).
	rec := priceRecord("m", &Usage{InputTokens: Int64(37)})
	rec.ID, rec.Cost = "e", cost
	rec.StartedAt = rec.StartedAt.Add(10 * time.Second)
	rec.FinishedAt = rec.StartedAt
	_ = store.Append(context.Background(), rec)
	summary, _ = Summarize(context.Background(), store, "t", SummaryQuery{Filter: Filter{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}})
	if got := summary.Groups[0].Cost[0].Amount; got != "47" {
		t.Fatalf("got %s", got)
	}
}

func TestCostExactValidation(t *testing.T) {
	for _, c := range []Cost{
		{Basis: BasisEstimated, Currency: "USD", Amount: "10", Scale: 6, Exact: "10"},
		{Basis: BasisEstimated, Currency: "USD", Amount: "10", Scale: 6, Exact: "41/4"},
		{Basis: BasisEstimated, Currency: "USD", Amount: "10", Scale: 6, Exact: "19/2"},
		{Basis: BasisEstimated, Currency: "USD", Amount: "10", Scale: 6, Exact: "x"},
	} {
		if c.Exact == "19/2" {
			if c.Validate() != nil {
				t.Errorf("9.5 rounds up to 10: %+v", c)
			}
			continue
		}
		if c.Validate() == nil {
			t.Errorf("accepted %+v", c)
		}
	}
}

package pricecatalog

import (
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

func longEntry() Entry {
	return Entry{Provider: "anthropic", Model: "haiku-long", Mode: "chat",
		Rates: Rates{Input: "0.1", Output: "0.5", CacheRead: "0.01", CacheWrite: "0.125", CacheWrite1h: "0.2",
			Tiers: []Tier{{Above: 100_000, Rates: Rates{Input: "0.5", Output: "2.5", CacheRead: "0.05", CacheWrite: "0.625", CacheWrite1h: "1"}}}},
		ServerTools: map[string]string{"web_search": "0.01"}}
}

func i64(v int64) *int64 { return &v }

func TestTieredEntryEstimatesByHand(t *testing.T) {
	c := &Catalog{Snapshot: Snapshot{Meta: Meta{Catalog: "litellm"}}}
	plan, err := c.Convert(longEntry(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Prices) != 1 || len(plan.Prices[0].Tiers) != 1 || len(plan.Tiers[0]) != 1 || plan.Tiers[0][0].Above != 100_000 {
		t.Fatalf("plan: %+v", plan)
	}
	rec := func(u *usageledger.Usage, tools map[string]int64) usageledger.Record {
		return usageledger.Record{Provider: "anthropic", ModelID: "haiku-long", Requests: 1, Usage: u, ServerTools: tools}
	}
	// 50,000 input (short): 50000*0.1 + 1000*0.5 = 5000 + 500 micro-USD.
	short := rec(&usageledger.Usage{InputTokens: i64(50_000), OutputTokens: i64(1000), CacheReadTokens: i64(0), CacheWriteTokens: i64(0)}, nil)
	if got := usageledger.Estimate(plan.Prices, short); got.Amount != "5500" {
		t.Fatalf("short: %+v", got)
	}
	// 150,000 input is over 100,000: the WHOLE request is priced at the tier.
	// uncached 30000*0.5 = 15000; cache read 100000*0.05 = 5000;
	// 5m writes 15000*0.625 = 9375; 1h writes 5000*1 = 5000; output 2000*2.5 = 5000.
	u := &usageledger.Usage{InputTokens: i64(150_000), OutputTokens: i64(2000), CacheReadTokens: i64(100_000), CacheWriteTokens: i64(20_000)}
	u.CacheWrite1hTokens = i64(5000)
	long := rec(u, nil)
	if got := usageledger.Estimate(plan.Prices, long); got.Amount != "39375" || got.PriceID != PriceID(longEntry()) {
		t.Fatalf("long: %+v", got)
	}
	// Two web searches at $0.01 each add 20000 micro-USD, on the same price.
	withSearch := rec(u, map[string]int64{"web_search": 2})
	if got := usageledger.Estimate(plan.Prices, withSearch); got.Amount != "59375" {
		t.Fatalf("with search: %+v", got)
	}
	// Another server tool has no per-call price: unknown, never free.
	other := rec(u, map[string]int64{"web_fetch": 1})
	if got := usageledger.Estimate(plan.Prices, other); got.Basis != usageledger.BasisUnknown {
		t.Fatalf("web_fetch: %+v", got)
	}
	// One-hour writes reported without a TTL breakdown cannot be priced.
	u2 := &usageledger.Usage{InputTokens: i64(50_000), OutputTokens: i64(1), CacheReadTokens: i64(0), CacheWriteTokens: i64(10)}
	if got := usageledger.Estimate(plan.Prices, rec(u2, nil)); got.Basis != usageledger.BasisUnknown {
		t.Fatalf("no breakdown: %+v", got)
	}
}

func TestServiceTierAndModalityConversion(t *testing.T) {
	c := &Catalog{Snapshot: Snapshot{Meta: Meta{Catalog: "litellm"}}}
	e := Entry{Provider: "openai", Model: "gpt-audio-x", Mode: "chat",
		Rates:    Rates{Input: "2", Output: "8", InputAudio: "32", OutputAudio: "64"},
		Priority: &Rates{Input: "3", Output: "12", InputAudio: "48", OutputAudio: "96"}}
	plan, err := c.Convert(e, Options{})
	if err != nil || len(plan.Prices) != 2 || plan.Prices[1].ServiceTier != "priority" || plan.Prices[1].ID != "openai-gpt-audio-x-priority" {
		t.Fatalf("plan %+v %v", plan.Prices, err)
	}
	u := &usageledger.Usage{InputTokens: i64(1000), OutputTokens: i64(100)}
	u.InputAudioTokens, u.OutputAudioTokens = i64(200), i64(40)
	rec := usageledger.Record{Provider: "openai", ModelID: "gpt-audio-x", Requests: 1, Usage: u}
	// text in 800*2 + audio in 200*32 + text out 60*8 + audio out 40*64 = 1600+6400+480+2560 = 11040.
	if got := usageledger.Estimate(plan.Prices, rec); got.Amount != "11040" {
		t.Fatalf("standard: %+v", got)
	}
	// Priority: 800*3 + 200*48 + 60*12 + 40*96 = 2400+9600+720+3840 = 16560.
	rec.ServiceTier = "priority"
	if got := usageledger.Estimate(plan.Prices, rec); got.Amount != "16560" || got.PriceID != "openai-gpt-audio-x-priority" {
		t.Fatalf("priority: %+v", got)
	}
	// Image input tokens with no image rate: the price separates modalities,
	// so the record is unknown rather than partially costed.
	rec.ServiceTier = ""
	u.InputImageTokens = i64(10)
	if got := usageledger.Estimate(plan.Prices, rec); got.Basis != usageledger.BasisUnknown {
		t.Fatalf("image: %+v", got)
	}
	// A tier nobody priced is unknown, not charged at the standard rate.
	rec.ServiceTier = "flex"
	u.InputImageTokens = nil
	if got := usageledger.Estimate(plan.Prices, rec); got.Basis != usageledger.BasisUnknown {
		t.Fatalf("flex: %+v", got)
	}
}

func TestEmbeddedSnapshotCarriesTiersAndStaysConsistent(t *testing.T) {
	c, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := c.Lookup("anthropic", "claude-haiku-5-5")
	if !ok || len(e.Rates.Tiers) != 1 || e.Rates.Tiers[0].Above != 100_000 || len(e.Blocking) != 0 {
		t.Fatalf("claude-haiku-5-5: %+v", e)
	}
	// Every tiered entry in the snapshot converts to valid prices, and every
	// entry's price validates.
	for _, e := range c.Entries {
		plan, err := c.Convert(e, Options{})
		if err != nil {
			t.Fatalf("%s: %v", e.ID(), err)
		}
		for _, p := range plan.Prices {
			if err := p.Validate(); err != nil {
				t.Fatalf("%s: %v", p.ID, err)
			}
		}
	}
}

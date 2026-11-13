package pricecatalog

import (
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// The pinned snapshots state a per-TTL cache-write rate only for one-hour
// writes. The 12-hour (litellm) and 30-minute (models.dev) rates are named as
// left out, with no value, so no import writes a 5m, 30m or 12h meter, and
// none is derived from another rate.
func TestPinnedSnapshotsImportNoOtherTTLRate(t *testing.T) {
	other := []string{usageledger.MeterCacheWrite5m, usageledger.MeterCacheWrite30m, usageledger.MeterCacheWrite12h}
	for _, source := range SourceNames {
		c, err := EmbeddedSource(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range c.Entries {
			plan, err := c.Convert(e, Options{})
			if err != nil {
				continue
			}
			for _, p := range plan.Prices {
				for _, meter := range other {
					if _, ok := p.Meters[meter]; ok {
						t.Errorf("%s %s imports %s", source, e.ID(), meter)
					}
				}
			}
		}
	}
}

// An imported OpenAI price covers 30-minute writes at its cache-write rate and
// leaves 12-hour writes unknown, because the 12-hour rate is not imported.
func TestImportedPriceLeavesTwelveHourWritesUnknown(t *testing.T) {
	c, err := EmbeddedSource(SourceLiteLLM)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := c.Get("openai/gpt-5.6")
	if !ok {
		t.Fatal("openai/gpt-5.6 not in the snapshot")
	}
	leftOut := false
	for _, name := range e.LeftOut {
		leftOut = leftOut || name == "cache_creation_input_token_cost_above_12hr"
	}
	if !leftOut {
		t.Fatalf("12-hour rate not listed as left out: %v", e.LeftOut)
	}
	plan, err := c.Convert(e, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := func(ttl30m, ttl12h int64) usageledger.Record {
		u := &usageledger.Usage{InputTokens: i64(1000), OutputTokens: i64(0), CacheReadTokens: i64(0), CacheWriteTokens: i64(ttl30m + ttl12h)}
		u.CacheWrite30mTokens, u.CacheWrite12hTokens = i64(ttl30m), i64(ttl12h)
		return usageledger.Record{Provider: "openai", ModelID: "gpt-5.6", Requests: 1, Usage: u}
	}
	// 900 fresh input at $4/Mtok + 100 30-minute writes at $5/Mtok = 3600 + 500 micro-USD.
	if got := usageledger.Estimate(plan.Prices, rec(100, 0)); got.Basis != usageledger.BasisEstimated || got.Amount != "4100" {
		t.Fatalf("30-minute writes: %+v", got)
	}
	if got := usageledger.Estimate(plan.Prices, rec(50, 50)); got.Basis != usageledger.BasisUnknown {
		t.Fatalf("12-hour writes priced without a 12-hour rate: %+v", got)
	}
}

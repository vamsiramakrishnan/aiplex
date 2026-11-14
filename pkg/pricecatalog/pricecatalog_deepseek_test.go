package pricecatalog

import (
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// The DeepSeek entries state their context-cache hit rate as the cache-read
// rate (litellm input_cost_per_token_cache_hit equals
// cache_read_input_token_cost in every pinned DeepSeek entry), so they import
// without --accept-partial. A record from a surface that declares DeepSeek
// usage semantics (cache hits as cache_read_tokens, cache writes 0) is priced
// exactly: hits at the cache-hit rate, misses at the input rate.
func TestDeepSeekEntriesImportTheCacheHitRate(t *testing.T) {
	c, err := EmbeddedSource(SourceLiteLLM)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range c.Entries {
		if e.Provider != "deepseek" {
			continue
		}
		n++
		plan, err := c.Convert(e, Options{})
		if err != nil || len(plan.Blocking) != 0 {
			t.Errorf("%s: blocking %v err %v", e.ID(), plan.Blocking, err)
		}
		if _, ok := plan.Prices[0].Meters[usageledger.MeterCacheRead]; !ok {
			t.Errorf("%s imports no cache-hit rate", e.ID())
		}
	}
	if n == 0 {
		t.Fatal("no DeepSeek entries in the snapshot")
	}
	e, ok := c.Get("deepseek/deepseek-v4-pro")
	if !ok {
		t.Fatal("deepseek/deepseek-v4-pro not in the snapshot")
	}
	plan, err := c.Convert(e, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := func(u *usageledger.Usage) usageledger.Record {
		return usageledger.Record{Provider: "openai-compatible", ModelID: "deepseek-v4-pro", Requests: 1, Usage: u}
	}
	// 176 misses at $1.32/Mtok + 1024 hits at $0.044/Mtok + 37 output at $3.96/Mtok
	// = 232.32 + 45.056 + 146.52 = 423.896 micro-USD, rounded up once.
	observed := &usageledger.Usage{InputTokens: i64(1200), OutputTokens: i64(37), CacheReadTokens: i64(1024), CacheWriteTokens: i64(0)}
	if got := usageledger.Estimate(plan.Prices, rec(observed)); got.Basis != usageledger.BasisEstimated || got.Amount != "424" || got.Exact != "52987/125" {
		t.Fatalf("deepseek cost %+v", got)
	}
	// An unknown cache split (inconsistent or missing hit/miss fields) is unknown, not priced as input.
	unknown := &usageledger.Usage{InputTokens: i64(1200), OutputTokens: i64(37)}
	if got := usageledger.Estimate(plan.Prices, rec(unknown)); got.Basis != usageledger.BasisUnknown {
		t.Fatalf("unknown split priced: %+v", got)
	}
}

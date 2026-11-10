package usageledger

import "testing"

// Total input derived from disjoint subsets includes cache writes; a counter
// set that already reports total input is taken as is.
func TestFromCountersDerivesInputWithCacheWrites(t *testing.T) {
	u := FromCounters(map[string]int64{"uncached_input_tokens": 20, "cached_tokens": 50, "cache_write_tokens": 30, "output_tokens": 3})
	if u == nil || *u.InputTokens != 100 || *u.CacheWriteTokens != 30 {
		t.Fatalf("derived input %+v", u)
	}
	u = FromCounters(map[string]int64{"input_tokens": 100, "uncached_input_tokens": 20, "cached_tokens": 50, "cache_write_tokens": 30, "output_tokens": 3})
	if *u.InputTokens != 100 {
		t.Fatal("reported total input changed", *u.InputTokens)
	}
}

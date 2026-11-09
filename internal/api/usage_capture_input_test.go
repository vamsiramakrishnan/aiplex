package api

import (
	"reflect"
	"testing"
)

// Messages-format counters exclude cache reads and writes from input_tokens;
// the ledger's input_tokens is total input. Unknown cache counters add
// nothing and already-normalized counters are left alone.
func TestLedgerNormalizesExclusiveInputIncludingCacheWrites(t *testing.T) {
	for _, test := range []struct{ in, want map[string]int64 }{
		{map[string]int64{"input_tokens": 1203, "cached_tokens": 4096, "cache_write_tokens": 2048, "output_tokens": 777}, map[string]int64{"input_tokens": 7347, "uncached_input_tokens": 1203, "cached_tokens": 4096, "cache_write_tokens": 2048, "output_tokens": 777}},
		{map[string]int64{"input_tokens": 105, "output_tokens": 239}, map[string]int64{"input_tokens": 105, "uncached_input_tokens": 105, "output_tokens": 239}},
		{map[string]int64{"input_tokens": 9, "uncached_input_tokens": 7, "cached_tokens": 2}, map[string]int64{"input_tokens": 9, "uncached_input_tokens": 7, "cached_tokens": 2}},
		{map[string]int64{"cached_tokens": 6144}, map[string]int64{"cached_tokens": 6144}},
	} {
		got := map[string]int64{}
		for k, v := range test.in {
			got[k] = v
		}
		normalizeExclusiveInput(got)
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%v -> %v, want %v", test.in, got, test.want)
		}
	}
}

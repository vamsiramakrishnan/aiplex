package api

import "testing"

// The ledger's token counters overlap, so a spender's rank never adds them:
// it is the known total, or input plus output where no total was recorded.
func TestSpenderTokenRankDoesNotAddOverlappingCounters(t *testing.T) {
	for name, tc := range map[string]struct {
		tokens map[string]int64
		want   int64
	}{
		"total wins over its parts":       {map[string]int64{"input_tokens": 100, "output_tokens": 20, "total_tokens": 120}, 120},
		"cache and reasoning are subsets": {map[string]int64{"input_tokens": 100, "output_tokens": 20, "cache_read_tokens": 80, "reasoning_tokens": 15, "total_tokens": 120}, 120},
		"no total recorded":               {map[string]int64{"input_tokens": 7, "output_tokens": 5, "cache_read_tokens": 4}, 12},
		"nothing recorded":                {nil, 0},
	} {
		if got := spenderTokenRank(tc.tokens); got != tc.want {
			t.Errorf("%s: rank %d, want %d", name, got, tc.want)
		}
	}
}

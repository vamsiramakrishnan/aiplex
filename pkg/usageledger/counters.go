package usageledger

import (
	"math"
	"sync"
)

// Accumulator merges provider-normalized usage observations for one exchange.
// Cumulative observations replace earlier values (never decreasing them);
// delta observations add. Counters that were never observed stay unknown.
// An invalid observation (negative value, empty key) is ignored as a whole,
// and a counter whose sum overflows becomes unknown rather than wrong.
type Accumulator struct {
	mu       sync.Mutex
	counters map[string]int64
	broken   map[string]bool
}

const maxAccumulatedCounters = 64

func (a *Accumulator) Observe(counters map[string]int64, cumulative bool) {
	if a == nil || len(counters) == 0 {
		return
	}
	for key, value := range counters {
		if key == "" || value < 0 {
			return
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.counters == nil {
		a.counters = map[string]int64{}
		a.broken = map[string]bool{}
	}
	for key, value := range counters {
		if a.broken[key] {
			continue
		}
		prior, known := a.counters[key]
		if !known && len(a.counters)+len(a.broken) >= maxAccumulatedCounters {
			continue
		}
		switch {
		case !known:
			a.counters[key] = value
		case cumulative:
			if value > prior {
				a.counters[key] = value
			}
		case prior > math.MaxInt64-value:
			delete(a.counters, key)
			a.broken[key] = true
		default:
			a.counters[key] = prior + value
		}
	}
}

// Snapshot returns a private copy of the known counters.
func (a *Accumulator) Snapshot() map[string]int64 {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]int64, len(a.counters))
	for k, v := range a.counters {
		out[k] = v
	}
	return out
}

// FromCounters maps the normalized execution counter names produced by the
// native adapters (input_tokens, output_tokens, cached_tokens,
// cache_write_tokens, thought_tokens, total_tokens, uncached_input_tokens) to
// ledger usage. input_tokens is total input including cached input. It returns
// nil when no token counter is known; it never fills a missing counter with zero.
func FromCounters(counters map[string]int64) *Usage {
	get := func(name string) *int64 {
		if v, ok := counters[name]; ok && v >= 0 {
			return Int64(v)
		}
		return nil
	}
	u := &Usage{
		InputTokens:      get("input_tokens"),
		OutputTokens:     get("output_tokens"),
		CacheReadTokens:  get("cached_tokens"),
		CacheWriteTokens: get("cache_write_tokens"),
		ReasoningTokens:  get("thought_tokens"),
		TotalTokens:      get("total_tokens"),
	}
	if u.InputTokens == nil {
		uncached, cached := get("uncached_input_tokens"), u.CacheReadTokens
		if uncached != nil && cached != nil && *uncached <= math.MaxInt64-*cached {
			u.InputTokens = Int64(*uncached + *cached)
		}
	}
	if !u.Known() {
		return nil
	}
	return u
}

package execution

import (
	"errors"
	"fmt"
	"maps"
)

var ErrUsageRegression = errors.New("cumulative usage regressed")

// CumulativeUsage validates one trusted producer's cumulative counters for one
// attempt. Missing counters remain unknown; repeated values are not added.
// Native adapters still own normalization, delta conversion and finality. This
// accumulator neither establishes terminal evidence nor releases exposure.
// The zero value is usable. Callers must serialize access.
type CumulativeUsage struct {
	counters map[string]int64
}

// Observe commits the entire update only if every counter is valid. The bound
// applies across all updates, so a stream cannot grow state with fresh keys.
func (u *CumulativeUsage) Observe(counters map[string]int64) error {
	count := len(u.counters)
	for key, value := range counters {
		if key == "" || value < 0 {
			return fmt.Errorf("invalid cumulative usage counter")
		}
		if prior, known := u.counters[key]; known {
			if value < prior {
				return ErrUsageRegression
			}
		} else {
			count++
		}
	}
	if count > 64 {
		return fmt.Errorf("cumulative usage counter bound exceeded")
	}
	if u.counters == nil {
		u.counters = make(map[string]int64, len(counters))
	}
	maps.Copy(u.counters, counters)
	return nil
}

// Snapshot returns private known counters without inventing missing zeros.
func (u *CumulativeUsage) Snapshot() map[string]int64 { return maps.Clone(u.counters) }

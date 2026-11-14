package toolexec

import (
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// Usage of one managed tool call, as the gateway observes it.
//
//   - tool_response_bytes: the bytes of the upstream response body as the
//     gateway read it (after HTTP transfer decoding), for a complete response
//     only. It is never the bytes read before a failure.
//   - tool_duration_milliseconds: the gateway's monotonic clock from the first
//     dispatch of the request to the end of the response body, in whole
//     milliseconds rounded up (at least 1 for a call that ran), never more
//     than the call's duration bound.
//
// Both are reported only with a complete response that establishes the
// call's terminal outcome; every other ending leaves them unknown.
const (
	CounterToolResponseBytes        = "tool_response_bytes"
	CounterToolDurationMilliseconds = "tool_duration_milliseconds"
)

// MeteringProfileToolUsage is the lease profile of a tool binding that meters
// response size or duration. Only such a lease receives never-started results
// (NotSent, Rejected); a request-count lease keeps its contract unchanged.
const MeteringProfileToolUsage = "tool-usage"

// Bounds a connection may set for its calls.
const (
	MaxResponseBytesLimit   = 64 << 20
	MaxDurationSecondsLimit = 30 * 60
)

// durationMilliseconds rounds a measured duration up to whole milliseconds,
// at least 1. A call whose body was read completely ended before the call's
// deadline (bound), so a measurement above it is the delay between the last
// byte and the clock read, and is clamped to the bound.
func durationMilliseconds(elapsed, bound time.Duration) int64 {
	if elapsed > bound {
		elapsed = bound
	}
	ms := int64(elapsed / time.Millisecond)
	if elapsed%time.Millisecond != 0 || ms == 0 {
		ms++
	}
	return ms
}

func usageProfile(lease execution.Lease) bool {
	contract, ok := lease.(interface{ MeteringProfile() string })
	return ok && contract.MeteringProfile() == MeteringProfileToolUsage
}

// MaxResponseBytes is the response bound the gateway applies to this
// connection's calls: the connection's own, else the default.
func (c Connection) EffectiveMaxResponseBytes(fallback int64) int64 {
	if c.MaxResponseBytes > 0 {
		return c.MaxResponseBytes
	}
	return fallback
}

// EffectiveMaxDuration is the duration bound the gateway applies to this
// connection's calls: the connection's own, else the default.
func (c Connection) EffectiveMaxDuration(fallback time.Duration) time.Duration {
	if c.MaxDurationSeconds > 0 {
		return time.Duration(c.MaxDurationSeconds) * time.Second
	}
	return fallback
}

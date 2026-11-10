package routing

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Backoff is the wait before retry number n (n >= 1 is the second attempt
// on a backend): exponential from base, capped at max, with equal jitter
// (half fixed, half uniformly random). random returns a value in [0, 1).
func Backoff(n int, base, max time.Duration, random func() float64) time.Duration {
	if n < 1 || base <= 0 {
		return 0
	}
	d := float64(base) * math.Pow(2, float64(n-1))
	if d > float64(max) {
		d = float64(max)
	}
	r := 0.0
	if random != nil {
		r = random()
		if r < 0 || r >= 1 {
			r = 0
		}
	}
	return time.Duration(d/2 + r*d/2)
}

// RetryAfter reads a provider's backoff hint: Retry-After-Ms (milliseconds,
// sent by some OpenAI-compatible providers) or Retry-After (delay seconds or
// an HTTP date). ok is false when there is no usable hint. A hint is never
// negative.
func RetryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	if v := strings.TrimSpace(h.Get("Retry-After-Ms")); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil && ms >= 0 && ms < float64(24*time.Hour/time.Millisecond) {
			return time.Duration(ms * float64(time.Millisecond)), true
		}
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0, false
	}
	if s, err := strconv.ParseFloat(v, 64); err == nil {
		if s < 0 || s > 86400 {
			return 0, false
		}
		return time.Duration(s * float64(time.Second)), true
	}
	if t, err := http.ParseTime(v); err == nil {
		d := t.Sub(now)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

// Wait decides the wait before retrying on the same backend. It honours the
// provider's hint: a hint above maxRetryAfter is not shortened, it means
// "do not retry here" (ok false), as does a wait that would not leave the
// retry inside the remaining budget.
func Wait(backoff, hint time.Duration, hinted bool, maxRetryAfter, remaining time.Duration) (time.Duration, bool) {
	wait := backoff
	if hinted {
		if hint > maxRetryAfter {
			return 0, false
		}
		if hint > wait {
			wait = hint
		}
	}
	if wait >= remaining {
		return 0, false
	}
	return wait, true
}

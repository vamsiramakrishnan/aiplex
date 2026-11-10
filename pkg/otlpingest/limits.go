package otlpingest

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter is a per-principal rate limit on requests and on records, in
// process. Each replica has its own view.
type Limiter struct {
	mu       sync.Mutex
	entries  map[string]*limiterEntry
	requests rate.Limit
	reqBurst int
	records  rate.Limit
	recBurst int
	max      int
	now      func() time.Time
}

type limiterEntry struct {
	requests, records *rate.Limiter
	seen              time.Time
}

// LimiterConfig sets rates per principal. Zero values use the defaults.
type LimiterConfig struct {
	RequestsPerSecond float64
	RequestBurst      int
	RecordsPerSecond  float64
	RecordBurst       int
}

// DefaultLimiterConfig allows a harness exporting every few seconds with
// generous headroom: 20 requests per second (burst 40) and 2000 records per
// second (burst 5000) per principal.
func DefaultLimiterConfig() LimiterConfig {
	return LimiterConfig{RequestsPerSecond: 20, RequestBurst: 40, RecordsPerSecond: 2000, RecordBurst: 5000}
}

// NewLimiter returns a limiter. maxPrincipals bounds its memory (zero:
// 50000); the least recently seen principals are dropped first.
func NewLimiter(c LimiterConfig, maxPrincipals int) *Limiter {
	d := DefaultLimiterConfig()
	if c.RequestsPerSecond <= 0 {
		c.RequestsPerSecond = d.RequestsPerSecond
	}
	if c.RequestBurst <= 0 {
		c.RequestBurst = d.RequestBurst
	}
	if c.RecordsPerSecond <= 0 {
		c.RecordsPerSecond = d.RecordsPerSecond
	}
	if c.RecordBurst <= 0 {
		c.RecordBurst = d.RecordBurst
	}
	if maxPrincipals <= 0 {
		maxPrincipals = 50_000
	}
	return &Limiter{entries: map[string]*limiterEntry{}, requests: rate.Limit(c.RequestsPerSecond), reqBurst: c.RequestBurst, records: rate.Limit(c.RecordsPerSecond), recBurst: c.RecordBurst, max: maxPrincipals, now: time.Now}
}

func (l *Limiter) entry(principal string, now time.Time) *limiterEntry {
	e := l.entries[principal]
	if e == nil {
		if len(l.entries) >= l.max {
			var oldest string
			var at time.Time
			for k, v := range l.entries {
				if oldest == "" || v.seen.Before(at) {
					oldest, at = k, v.seen
				}
			}
			delete(l.entries, oldest)
		}
		e = &limiterEntry{requests: rate.NewLimiter(l.requests, l.reqBurst), records: rate.NewLimiter(l.records, l.recBurst)}
		l.entries[principal] = e
	}
	e.seen = now
	return e
}

// AllowRequest takes one request token. When refused, retryAfter says when to
// retry.
func (l *Limiter) AllowRequest(principal string) (ok bool, retryAfter time.Duration) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e := l.entry(principal, now)
	r := e.requests.ReserveN(now, 1)
	if !r.OK() {
		return false, time.Second
	}
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return false, d
	}
	return true, 0
}

// AllowRecords takes n record tokens. A request larger than the burst can
// never pass; callers bound n by the per-request maximum first.
func (l *Limiter) AllowRecords(principal string, n int) (ok bool, retryAfter time.Duration) {
	if l == nil || n <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e := l.entry(principal, now)
	r := e.records.ReserveN(now, n)
	if !r.OK() {
		return false, time.Second
	}
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return false, d
	}
	return true, 0
}

package usageledger

import "fmt"

// BasisCached is the cost basis of an answer served from the AIPlex response
// cache: no provider ran, so the exchange cost nothing at the provider. Its
// amount is always "0" and it carries no currency. Summaries count these
// records separately; they are not unknown cost.
const BasisCached = "cached"

// Cache statuses on a record.
const (
	CacheHit    = "hit"
	CacheMiss   = "miss"
	CacheBypass = "bypass"
)

// MaxAttempts bounds the attempts one record carries.
const MaxAttempts = 32

// Attempt is one try of a routed exchange: which backend, how it ended and
// how long it took. It never holds request or response content.
type Attempt struct {
	SurfaceRef  string `json:"surface_ref,omitempty"`
	NativeModel string `json:"native_model,omitempty"`
	// Outcome is the attempt's class (ok, connect_error, rate_limited,
	// overloaded, unavailable, server_error, bad_gateway, gateway_timeout,
	// request_timeout, upstream_error, transport_error, capacity_refused,
	// quota_denied, not_granted, breaker_open, refused, body_mismatch,
	// deadline_exceeded, backend_unavailable, cache_hit).
	Outcome        string `json:"outcome"`
	UpstreamStatus int    `json:"upstream_status,omitempty"`
	RetryAfterMS   *int64 `json:"retry_after_ms,omitempty"`
	DurationMS     int64  `json:"duration_ms"`
	// BreakerState is the backend's breaker state when the attempt was
	// admitted (closed, open, half_open).
	BreakerState string `json:"breaker_state,omitempty"`
	// Reason is why the backend was in the attempt order (priority,
	// weighted_pick, weighted_fallback, lowest_cost, price_unknown, ...).
	Reason string `json:"reason,omitempty"`
}

// BackendRef names the backend that served a routed exchange.
type BackendRef struct {
	SurfaceRef  string `json:"surface_ref"`
	NativeModel string `json:"native_model"`
}

// CacheInfo is the response-cache outcome of an exchange through an
// access-point model with a cache.
type CacheInfo struct {
	Status string `json:"status"`
	// Reason says why a request bypassed the cache (streaming, stateful,
	// no_store, tools, user_identifier, ...) or why a miss was not stored.
	Reason string `json:"reason,omitempty"`
	Scope  string `json:"scope,omitempty"`
}

func (a Attempt) validate() error {
	for name, v := range map[string]string{"surface_ref": a.SurfaceRef, "native_model": a.NativeModel, "outcome": a.Outcome, "breaker_state": a.BreakerState, "reason": a.Reason} {
		if !validText(v, name == "outcome") {
			return invalid("attempts %s is not bounded printable text", name)
		}
	}
	if a.DurationMS < 0 || a.UpstreamStatus < 0 || a.UpstreamStatus > 999 || (a.RetryAfterMS != nil && *a.RetryAfterMS < 0) {
		return invalid("attempts durations and statuses must be nonnegative")
	}
	return nil
}

func (r Record) validateRouting() error {
	if len(r.Attempts) > MaxAttempts {
		return invalid("at most %d attempts", MaxAttempts)
	}
	for _, a := range r.Attempts {
		if err := a.validate(); err != nil {
			return err
		}
	}
	if r.Backend != nil && (!validText(r.Backend.SurfaceRef, true) || !validText(r.Backend.NativeModel, true)) {
		return invalid("backend needs surface_ref and native_model")
	}
	if !validText(r.RoutingStrategy, false) {
		return invalid("routing_strategy is not bounded printable text")
	}
	if c := r.Cache; c != nil {
		switch c.Status {
		case CacheHit, CacheMiss, CacheBypass:
		default:
			return invalid("cache.status must be hit, miss or bypass")
		}
		if !validText(c.Reason, false) || !validText(c.Scope, false) {
			return invalid("cache fields are not bounded printable text")
		}
	}
	if r.CachedResponseUsage != nil {
		for _, name := range TokenCounters {
			if v := r.CachedResponseUsage.Counter(name); v != nil && *v < 0 {
				return invalid("cached_response_usage counter %s is negative", name)
			}
		}
	}
	return nil
}

func validateCachedCost(c Cost) error {
	if c.Currency != "" || c.Amount != "0" || c.Scale != 0 {
		return invalid("a cached cost is exactly amount 0 with no currency")
	}
	return nil
}

// CachedCost is the cost of a cache hit.
func CachedCost() Cost { return Cost{Basis: BasisCached, Amount: "0"} }

func cloneRouting(r *Record) {
	if r.Attempts != nil {
		attempts := make([]Attempt, len(r.Attempts))
		for i, a := range r.Attempts {
			if a.RetryAfterMS != nil {
				v := *a.RetryAfterMS
				a.RetryAfterMS = &v
			}
			attempts[i] = a
		}
		r.Attempts = attempts
	}
	if r.Backend != nil {
		b := *r.Backend
		r.Backend = &b
	}
	if r.Cache != nil {
		c := *r.Cache
		r.Cache = &c
	}
	if r.CachedResponseUsage != nil {
		u := Record{Usage: r.CachedResponseUsage}.Clone().Usage
		r.CachedResponseUsage = u
	}
}

// RedactRouting removes backend identities from a record for a reader who
// is not an operator: attempts keep their order, outcomes and timings, and
// backends are numbered by position ("backend-1", ...) instead of named.
func (r *Record) RedactRouting() {
	if r == nil || (len(r.Attempts) == 0 && r.Backend == nil) {
		return
	}
	names := map[string]string{}
	label := func(surface, native string) string {
		key := surface + "\x00" + native
		if names[key] == "" {
			names[key] = fmt.Sprintf("backend-%d", len(names)+1)
		}
		return names[key]
	}
	for i := range r.Attempts {
		a := &r.Attempts[i]
		a.SurfaceRef, a.NativeModel = label(a.SurfaceRef, a.NativeModel), ""
	}
	if r.Backend != nil {
		r.Backend = &BackendRef{SurfaceRef: label(r.Backend.SurfaceRef, r.Backend.NativeModel), NativeModel: "-"}
	}
}

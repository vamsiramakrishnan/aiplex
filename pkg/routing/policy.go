// Package routing holds the request-path logic of access-point model routing:
// the routing, retry, fallback, circuit-breaker and response-cache
// configuration of one access-point model, outcome classification, backoff,
// Retry-After handling, backend ordering, the per-process circuit breakers and
// the per-process exact-match response cache. It does no I/O of its own; the
// API wires it between the request hooks and the response hooks.
package routing

import (
	"fmt"
	"strings"
	"time"
)

// Strategies order the backends of one access-point model.
const (
	StrategyPriority   = "priority"
	StrategyWeighted   = "weighted"
	StrategyLowestCost = "lowest_cost"
)

// Fallback behaviour after a quota denial.
const (
	QuotaDeniedFail        = "fail"
	QuotaDeniedNextBackend = "next_backend"
)

// Cache scopes.
const (
	CacheScopePrincipal   = "principal"
	CacheScopeAccessPoint = "access_point"
)

// Limits on the configuration.
const (
	MaxBackends        = 8
	MaxAttemptsPerHop  = 5
	MaxDeadline        = 10 * time.Minute
	MaxBackoff         = time.Minute
	MaxRetryAfterCap   = 5 * time.Minute
	MaxBreakerOpen     = time.Hour
	MaxBreakerFailures = 1000
	MaxCacheTTL        = 24 * time.Hour
	MaxCacheEntryBytes = 4 << 20
	MaxWeight          = 1000
)

// Defaults applied when a field is omitted.
const (
	DefaultMaxAttempts      = 2
	DefaultBaseBackoff      = 200 * time.Millisecond
	DefaultMaxBackoff       = 2 * time.Second
	DefaultMaxRetryAfter    = 5 * time.Second
	DefaultDeadline         = 30 * time.Second
	DefaultBreakerFailures  = 5
	DefaultBreakerOpen      = 30 * time.Second
	DefaultCacheEntryBytes  = 1 << 20
	DefaultInputCostWeight  = 3
	DefaultOutputCostWeight = 1
)

// Backend is one way to serve an access-point model: a saved model surface
// and the native model name it is asked for there.
type Backend struct {
	SurfaceRef  string `json:"surface_ref"`
	NativeModel string `json:"native_model"`
	// Weight is the relative share of first attempts under the weighted
	// strategy (default 1; 0 means only ever a fallback).
	Weight *int `json:"weight,omitempty"`
	// Priority orders backends under the priority strategy and breaks ties
	// under lowest_cost; lower first, list order within a priority.
	Priority int `json:"priority,omitempty"`
}

// EffectiveWeight is the weight with its default.
func (b Backend) EffectiveWeight() int {
	if b.Weight == nil {
		return 1
	}
	return *b.Weight
}

// Routing is the routing block of an access-point model.
type Routing struct {
	Strategy   string      `json:"strategy,omitempty"`
	Retry      *Retry      `json:"retry,omitempty"`
	Fallback   *Fallback   `json:"fallback,omitempty"`
	Breaker    *Breaker    `json:"breaker,omitempty"`
	LowestCost *LowestCost `json:"lowest_cost,omitempty"`
	// DeadlineMS bounds, from the start of the exchange, when a retry or a
	// fallback may still start (default 30000). It never cuts an attempt that
	// is already running, and it is further bounded by the caller's context.
	DeadlineMS int64 `json:"deadline_ms,omitempty"`
}

// Retry configures attempts on the same backend.
type Retry struct {
	// MaxAttempts is the number of attempts on one backend, the first
	// included (default 2; 1 disables retries).
	MaxAttempts int `json:"max_attempts,omitempty"`
	// On lists the outcome classes that are retried (default
	// connect_error, rate_limited, overloaded, unavailable).
	On              []string `json:"on,omitempty"`
	BaseBackoffMS   int64    `json:"base_backoff_ms,omitempty"`
	MaxBackoffMS    int64    `json:"max_backoff_ms,omitempty"`
	MaxRetryAfterMS int64    `json:"max_retry_after_ms,omitempty"`
}

// Fallback configures moving to the next backend.
type Fallback struct {
	// Disabled turns fallback off: only the first chosen backend is tried.
	Disabled bool `json:"disabled,omitempty"`
	// On lists the outcome classes that move to the next backend (default
	// connect_error, rate_limited, overloaded, unavailable, capacity_refused).
	On []string `json:"on,omitempty"`
	// OnQuotaDenied is fail (default: a quota denial is final) or
	// next_backend.
	OnQuotaDenied string `json:"on_quota_denied,omitempty"`
}

// Breaker configures the per-backend circuit breaker.
type Breaker struct {
	Disabled         bool  `json:"disabled,omitempty"`
	FailureThreshold int   `json:"failure_threshold,omitempty"`
	OpenMS           int64 `json:"open_ms,omitempty"`
}

// LowestCost configures the lowest_cost strategy.
type LowestCost struct {
	// InputWeight and OutputWeight weigh the input and output token rates
	// (default 3:1).
	InputWeight  int `json:"input_weight,omitempty"`
	OutputWeight int `json:"output_weight,omitempty"`
	// RequirePrice excludes backends whose price is unknown instead of
	// ordering them last.
	RequirePrice bool `json:"require_price,omitempty"`
}

// Cache configures the exact-match response cache of one access-point model.
// It is off unless the block is present.
type Cache struct {
	// TTL is a Go duration ("10m"), between 1s and 24h.
	TTL string `json:"ttl"`
	// Scope is principal (default: an answer is served only to the
	// principal that caused it to be stored) or access_point (shared by every
	// caller of the access point).
	Scope string `json:"scope,omitempty"`
	// MaxEntryBytes bounds one stored response (default 1 MiB, at most 4 MiB).
	MaxEntryBytes int64 `json:"max_entry_bytes,omitempty"`
}

// Outcome classes of one attempt. They are bounded metric labels.
const (
	OutcomeOK             = "ok"
	OutcomeConnectError   = "connect_error"   // the request provably never left
	OutcomeTransportError = "transport_error" // sent; no response was read
	OutcomeRateLimited    = "rate_limited"    // upstream 429
	OutcomeOverloaded     = "overloaded"      // upstream 529
	OutcomeUnavailable    = "unavailable"     // upstream 503
	OutcomeServerError    = "server_error"    // upstream 500
	OutcomeBadGateway     = "bad_gateway"     // upstream 502
	OutcomeGatewayTimeout = "gateway_timeout" // upstream 504
	OutcomeRequestTimeout = "request_timeout" // upstream 408
	OutcomeUpstreamError  = "upstream_error"  // any other upstream status
	OutcomeCapacity       = "capacity_refused"
	OutcomeQuotaDenied    = "quota_denied"
	OutcomeNotGranted     = "not_granted"
	OutcomeBreakerOpen    = "breaker_open"
	OutcomeRefused        = "refused" // an AIPlex refusal: validation, hooks, ownership
	OutcomeBodyMismatch   = "body_mismatch"
	OutcomeDeadline       = "deadline_exceeded"
	OutcomeBackendMissing = "backend_unavailable"
	OutcomeCacheHit       = "cache_hit"
)

// Outcomes lists every class (the metric label set).
var Outcomes = []string{OutcomeOK, OutcomeConnectError, OutcomeTransportError, OutcomeRateLimited, OutcomeOverloaded, OutcomeUnavailable, OutcomeServerError, OutcomeBadGateway, OutcomeGatewayTimeout, OutcomeRequestTimeout, OutcomeUpstreamError, OutcomeCapacity, OutcomeQuotaDenied, OutcomeNotGranted, OutcomeBreakerOpen, OutcomeRefused, OutcomeBodyMismatch, OutcomeDeadline, OutcomeBackendMissing, OutcomeCacheHit}

// retryable lists the classes that may be configured in retry.on.
var retryable = []string{OutcomeConnectError, OutcomeRateLimited, OutcomeOverloaded, OutcomeUnavailable, OutcomeServerError, OutcomeBadGateway, OutcomeGatewayTimeout, OutcomeRequestTimeout}

// fallbackable lists the classes that may be configured in fallback.on.
var fallbackable = append(append([]string(nil), retryable...), OutcomeCapacity)

// DefaultRetryOn and DefaultFallbackOn are the "not executed" signals.
var (
	DefaultRetryOn    = []string{OutcomeConnectError, OutcomeRateLimited, OutcomeOverloaded, OutcomeUnavailable}
	DefaultFallbackOn = []string{OutcomeConnectError, OutcomeRateLimited, OutcomeOverloaded, OutcomeUnavailable, OutcomeCapacity}
)

// breakerFailure lists the classes that count against a backend's breaker.
var breakerFailure = []string{OutcomeConnectError, OutcomeTransportError, OutcomeRateLimited, OutcomeOverloaded, OutcomeUnavailable, OutcomeServerError, OutcomeBadGateway, OutcomeGatewayTimeout, OutcomeRequestTimeout}

func member(v string, set []string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// UpstreamStatusClass classifies an upstream HTTP status.
func UpstreamStatusClass(status int) string {
	switch {
	case status >= 200 && status < 300:
		return OutcomeOK
	case status == 408:
		return OutcomeRequestTimeout
	case status == 429:
		return OutcomeRateLimited
	case status == 500:
		return OutcomeServerError
	case status == 502:
		return OutcomeBadGateway
	case status == 503:
		return OutcomeUnavailable
	case status == 504:
		return OutcomeGatewayTimeout
	case status == 529:
		return OutcomeOverloaded
	}
	return OutcomeUpstreamError
}

// BreakerFailure reports whether an outcome counts against a breaker.
func BreakerFailure(outcome string) bool { return member(outcome, breakerFailure) }

// Effective is a routing block with every default applied.
type Effective struct {
	Strategy        string
	MaxAttempts     int
	RetryOn         []string
	BaseBackoff     time.Duration
	MaxBackoff      time.Duration
	MaxRetryAfter   time.Duration
	Fallback        bool
	FallbackOn      []string
	QuotaFallback   bool
	Breaker         bool
	BreakerFailures int
	BreakerOpen     time.Duration
	InputWeight     int
	OutputWeight    int
	RequirePrice    bool
	Deadline        time.Duration
}

// Effective returns the configuration with defaults. A nil block is the
// default routing of a model with several backends.
func (r *Routing) Effective() Effective {
	e := Effective{Strategy: StrategyPriority, MaxAttempts: DefaultMaxAttempts, RetryOn: DefaultRetryOn, BaseBackoff: DefaultBaseBackoff, MaxBackoff: DefaultMaxBackoff, MaxRetryAfter: DefaultMaxRetryAfter, Fallback: true, FallbackOn: DefaultFallbackOn, Breaker: true, BreakerFailures: DefaultBreakerFailures, BreakerOpen: DefaultBreakerOpen, InputWeight: DefaultInputCostWeight, OutputWeight: DefaultOutputCostWeight, Deadline: DefaultDeadline}
	if r == nil || r.Retry == nil {
		// Retrying on the same backend is opt-in: a routing.retry block
		// (even an empty one) turns it on with the defaults.
		e.MaxAttempts = 1
	}
	if r == nil {
		return e
	}
	if r.Strategy != "" {
		e.Strategy = r.Strategy
	}
	if r.DeadlineMS > 0 {
		e.Deadline = time.Duration(r.DeadlineMS) * time.Millisecond
	}
	if t := r.Retry; t != nil {
		if t.MaxAttempts > 0 {
			e.MaxAttempts = t.MaxAttempts
		}
		if t.On != nil {
			e.RetryOn = t.On
		}
		if t.BaseBackoffMS > 0 {
			e.BaseBackoff = time.Duration(t.BaseBackoffMS) * time.Millisecond
		}
		if t.MaxBackoffMS > 0 {
			e.MaxBackoff = time.Duration(t.MaxBackoffMS) * time.Millisecond
		}
		if t.MaxRetryAfterMS > 0 {
			e.MaxRetryAfter = time.Duration(t.MaxRetryAfterMS) * time.Millisecond
		}
	}
	if f := r.Fallback; f != nil {
		e.Fallback = !f.Disabled
		if f.On != nil {
			e.FallbackOn = f.On
		}
		e.QuotaFallback = f.OnQuotaDenied == QuotaDeniedNextBackend
	}
	if b := r.Breaker; b != nil {
		e.Breaker = !b.Disabled
		if b.FailureThreshold > 0 {
			e.BreakerFailures = b.FailureThreshold
		}
		if b.OpenMS > 0 {
			e.BreakerOpen = time.Duration(b.OpenMS) * time.Millisecond
		}
	}
	if c := r.LowestCost; c != nil {
		if c.InputWeight > 0 || c.OutputWeight > 0 {
			e.InputWeight, e.OutputWeight = c.InputWeight, c.OutputWeight
		}
		e.RequirePrice = c.RequirePrice
	}
	return e
}

// Retries reports whether an outcome is retried on the same backend.
func (e Effective) Retries(outcome string) bool { return member(outcome, e.RetryOn) }

// FallsOver reports whether an outcome moves to the next backend.
func (e Effective) FallsOver(outcome string) bool {
	switch outcome {
	case OutcomeNotGranted, OutcomeBreakerOpen, OutcomeBodyMismatch, OutcomeBackendMissing:
		// Skips: the backend was never asked.
		return true
	case OutcomeQuotaDenied:
		return e.Fallback && e.QuotaFallback
	}
	return e.Fallback && member(outcome, e.FallbackOn)
}

func validClasses(field string, values, allowed []string) error {
	if len(values) > len(allowed) {
		return fmt.Errorf("%s lists too many classes", field)
	}
	seen := map[string]bool{}
	for _, v := range values {
		if !member(v, allowed) {
			return fmt.Errorf("%s: %q is not one of %s", field, v, strings.Join(allowed, ", "))
		}
		if seen[v] {
			return fmt.Errorf("%s: %q is listed twice", field, v)
		}
		seen[v] = true
	}
	return nil
}

// FieldError names the field of a validation failure (a JSON pointer suffix
// relative to the model entry, e.g. "routing/retry/on").
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

func fieldErr(field, format string, args ...any) error {
	return &FieldError{Field: field, Err: fmt.Errorf(format, args...)}
}

// Validate checks a routing block for n backends.
func (r *Routing) Validate(n int) error {
	if r == nil {
		return nil
	}
	switch r.Strategy {
	case "", StrategyPriority, StrategyWeighted, StrategyLowestCost:
	default:
		return fieldErr("routing/strategy", "routing.strategy must be priority, weighted or lowest_cost")
	}
	if r.DeadlineMS < 0 || time.Duration(r.DeadlineMS)*time.Millisecond > MaxDeadline {
		return fieldErr("routing/deadline_ms", "routing.deadline_ms must be between 1 and %d", MaxDeadline.Milliseconds())
	}
	if t := r.Retry; t != nil {
		if t.MaxAttempts < 0 || t.MaxAttempts > MaxAttemptsPerHop {
			return fieldErr("routing/retry/max_attempts", "routing.retry.max_attempts must be between 1 and %d", MaxAttemptsPerHop)
		}
		if err := validClasses("routing.retry.on", t.On, retryable); err != nil {
			return fieldErr("routing/retry/on", "%v", err)
		}
		for name, v := range map[string]int64{"base_backoff_ms": t.BaseBackoffMS, "max_backoff_ms": t.MaxBackoffMS} {
			if v < 0 || time.Duration(v)*time.Millisecond > MaxBackoff {
				return fieldErr("routing/retry/"+name, "routing.retry.%s must be between 1 and %d", name, MaxBackoff.Milliseconds())
			}
		}
		if t.BaseBackoffMS > 0 && t.MaxBackoffMS > 0 && t.BaseBackoffMS > t.MaxBackoffMS {
			return fieldErr("routing/retry/base_backoff_ms", "routing.retry.base_backoff_ms exceeds max_backoff_ms")
		}
		if t.MaxRetryAfterMS < 0 || time.Duration(t.MaxRetryAfterMS)*time.Millisecond > MaxRetryAfterCap {
			return fieldErr("routing/retry/max_retry_after_ms", "routing.retry.max_retry_after_ms must be between 1 and %d", MaxRetryAfterCap.Milliseconds())
		}
	}
	if f := r.Fallback; f != nil {
		if err := validClasses("routing.fallback.on", f.On, fallbackable); err != nil {
			return fieldErr("routing/fallback/on", "%v", err)
		}
		switch f.OnQuotaDenied {
		case "", QuotaDeniedFail, QuotaDeniedNextBackend:
		default:
			return fieldErr("routing/fallback/on_quota_denied", "routing.fallback.on_quota_denied must be fail or next_backend")
		}
	}
	if b := r.Breaker; b != nil {
		if b.FailureThreshold < 0 || b.FailureThreshold > MaxBreakerFailures {
			return fieldErr("routing/breaker/failure_threshold", "routing.breaker.failure_threshold must be between 1 and %d", MaxBreakerFailures)
		}
		if b.OpenMS < 0 || time.Duration(b.OpenMS)*time.Millisecond > MaxBreakerOpen {
			return fieldErr("routing/breaker/open_ms", "routing.breaker.open_ms must be between 1 and %d", MaxBreakerOpen.Milliseconds())
		}
	}
	if c := r.LowestCost; c != nil {
		if r.Strategy != StrategyLowestCost {
			return fieldErr("routing/lowest_cost", "routing.lowest_cost applies only with strategy lowest_cost")
		}
		// Both 0 (omitted) means the default 3:1.
		if c.InputWeight < 0 || c.OutputWeight < 0 || c.InputWeight > 1000 || c.OutputWeight > 1000 {
			return fieldErr("routing/lowest_cost", "routing.lowest_cost weights must be between 0 and 1000")
		}
	}
	return nil
}

// Validate checks a cache block.
func (c *Cache) Validate() error {
	if c == nil {
		return nil
	}
	ttl, err := time.ParseDuration(c.TTL)
	if err != nil || ttl < time.Second || ttl > MaxCacheTTL {
		return fieldErr("cache/ttl", "cache.ttl must be a duration between 1s and 24h (for example \"10m\")")
	}
	switch c.Scope {
	case "", CacheScopePrincipal, CacheScopeAccessPoint:
	default:
		return fieldErr("cache/scope", "cache.scope must be principal or access_point")
	}
	if c.MaxEntryBytes < 0 || c.MaxEntryBytes > MaxCacheEntryBytes {
		return fieldErr("cache/max_entry_bytes", "cache.max_entry_bytes must be between 1 and %d", MaxCacheEntryBytes)
	}
	return nil
}

// TTLDuration is the parsed TTL (valid blocks only).
func (c *Cache) TTLDuration() time.Duration {
	ttl, _ := time.ParseDuration(c.TTL)
	return ttl
}

// EffectiveScope is the scope with its default.
func (c *Cache) EffectiveScope() string {
	if c.Scope == "" {
		return CacheScopePrincipal
	}
	return c.Scope
}

// EntryLimit is the per-entry byte bound with its default.
func (c *Cache) EntryLimit() int64 {
	if c.MaxEntryBytes > 0 {
		return c.MaxEntryBytes
	}
	return DefaultCacheEntryBytes
}

// ValidateBackends checks the backend list's own shape (references are
// resolved by the caller).
func ValidateBackends(backends []Backend) error {
	if len(backends) == 0 {
		return fieldErr("backends", "backends lists at least one backend")
	}
	if len(backends) > MaxBackends {
		return fieldErr("backends", "backends is bounded to %d entries", MaxBackends)
	}
	seen := map[string]bool{}
	positive := false
	for i, b := range backends {
		key := b.SurfaceRef + "\x00" + b.NativeModel
		if seen[key] {
			return fieldErr(fmt.Sprintf("backends/%d", i), "backend %s/%s is listed twice", b.SurfaceRef, b.NativeModel)
		}
		seen[key] = true
		if b.Weight != nil && (*b.Weight < 0 || *b.Weight > MaxWeight) {
			return fieldErr(fmt.Sprintf("backends/%d/weight", i), "weight must be between 0 and %d", MaxWeight)
		}
		if b.Priority < -1000 || b.Priority > 1000 {
			return fieldErr(fmt.Sprintf("backends/%d/priority", i), "priority must be between -1000 and 1000")
		}
		positive = positive || b.EffectiveWeight() > 0
	}
	if !positive {
		return fieldErr("backends", "at least one backend needs a weight above 0")
	}
	return nil
}

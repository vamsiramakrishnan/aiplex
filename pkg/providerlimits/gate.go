package providerlimits

import (
	"fmt"
	"hash/fnv"
	"math"
	"sync"
	"time"
)

// Capacity policy defaults and bounds. A policy is off unless it is present on
// the surface spec and enables at least one control.
const (
	DefaultMaxCooldown   = 60 * time.Second
	MaxAllowedCooldown   = time.Hour
	DefaultSignalMaxAge  = 60 * time.Second
	MaxAllowedSignalAge  = time.Hour
	DefaultWeightValue   = 1
	MaxPolicyWeight      = 1000 // matches the access-point QoS weight bound
	DefaultBackoff       = 2 * time.Second
	probeRetryHint       = time.Second
	defaultProbeTimeout  = 30 * time.Second
	defaultMaxGateScopes = 4096
)

// Rejection reasons. They double as the stable error codes the API layer
// lowercases into the usage ledger's error_class.
const (
	ReasonCooldown = "CAPACITY_COOLDOWN"
	ReasonShed     = "CAPACITY_SHED"
)

// Policy is the per-surface capacity_policy block. Every control is advisory:
// it acts on replica-local, possibly stale provider signals and never replaces
// QuotaPlex admission.
type Policy struct {
	// CooldownEnabled rejects new requests while the last observed response
	// was a throttling or overload response with a retry or reset time.
	CooldownEnabled bool `json:"cooldown_enabled,omitempty"`
	// MaxCooldownSeconds bounds any cooldown (default 60, at most 3600).
	MaxCooldownSeconds int `json:"max_cooldown_seconds,omitempty"`
	// HeadroomFraction in (0, 1] reserves capacity: when remaining requests
	// or tokens fall below this fraction of the limit, callers whose weight
	// is below MinWeight are shed. Zero disables headroom reservation.
	HeadroomFraction float64 `json:"headroom_fraction,omitempty"`
	// MinWeight is the lowest QoS weight still admitted under low headroom.
	MinWeight uint32 `json:"min_weight,omitempty"`
	// DefaultWeight is the weight of requests without access-point QoS
	// (default 1).
	DefaultWeight uint32 `json:"default_weight,omitempty"`
	// SignalMaxAgeSeconds bounds how old an observation may be and still
	// be used (default 60, at most 3600).
	SignalMaxAgeSeconds int `json:"signal_max_age_seconds,omitempty"`
}

// Enabled reports whether any control is on.
func (p Policy) Enabled() bool { return p.CooldownEnabled || p.HeadroomFraction > 0 }

// Validate is the strict save-time contract. Settings that would have no
// effect are rejected rather than silently ignored.
func (p Policy) Validate() error {
	if math.IsNaN(p.HeadroomFraction) || p.HeadroomFraction < 0 || p.HeadroomFraction > 1 {
		return fmt.Errorf("capacity_policy.headroom_fraction must be between 0 and 1")
	}
	if p.MaxCooldownSeconds < 0 || p.MaxCooldownSeconds > int(MaxAllowedCooldown/time.Second) {
		return fmt.Errorf("capacity_policy.max_cooldown_seconds must be between 1 and %d", int(MaxAllowedCooldown/time.Second))
	}
	if p.SignalMaxAgeSeconds < 0 || p.SignalMaxAgeSeconds > int(MaxAllowedSignalAge/time.Second) {
		return fmt.Errorf("capacity_policy.signal_max_age_seconds must be between 1 and %d", int(MaxAllowedSignalAge/time.Second))
	}
	if p.MinWeight > MaxPolicyWeight || p.DefaultWeight > MaxPolicyWeight {
		return fmt.Errorf("capacity_policy weights must be at most %d", MaxPolicyWeight)
	}
	if p.MaxCooldownSeconds != 0 && !p.CooldownEnabled {
		return fmt.Errorf("capacity_policy.max_cooldown_seconds requires cooldown_enabled")
	}
	if p.HeadroomFraction > 0 && p.MinWeight == 0 {
		return fmt.Errorf("capacity_policy.headroom_fraction requires min_weight of at least 1")
	}
	if p.HeadroomFraction == 0 && (p.MinWeight != 0 || p.DefaultWeight != 0) {
		return fmt.Errorf("capacity_policy.min_weight and default_weight require headroom_fraction")
	}
	if p.SignalMaxAgeSeconds != 0 && !p.Enabled() {
		return fmt.Errorf("capacity_policy.signal_max_age_seconds requires cooldown_enabled or headroom_fraction")
	}
	return nil
}

// WithDefaults returns the policy with every unset bound made explicit.
func (p Policy) WithDefaults() Policy {
	if p.MaxCooldownSeconds == 0 {
		p.MaxCooldownSeconds = int(DefaultMaxCooldown / time.Second)
	}
	if p.SignalMaxAgeSeconds == 0 {
		p.SignalMaxAgeSeconds = int(DefaultSignalMaxAge / time.Second)
	}
	if p.DefaultWeight == 0 {
		p.DefaultWeight = DefaultWeightValue
	}
	return p
}

func (p Policy) maxCooldown() time.Duration {
	return time.Duration(p.WithDefaults().MaxCooldownSeconds) * time.Second
}
func (p Policy) maxAge() time.Duration {
	return time.Duration(p.WithDefaults().SignalMaxAgeSeconds) * time.Second
}

// Decision is the gate's verdict for one request.
type Decision struct {
	Allow      bool
	Reason     string        // ReasonCooldown or ReasonShed when !Allow
	RetryAfter time.Duration // hint for the caller when !Allow
	// Probe is true when this request is the single half-open probe.
	Probe bool

	gate *Gate
	key  string
	gen  uint64
}

// Cooldown phases reported by State.
const (
	PhaseNone    = "none"
	PhaseCooling = "cooling"
	PhaseHalf    = "half_open" // cooldown elapsed, next request is the probe
	PhaseProbing = "probing"   // a probe is in flight
)

// CooldownState is the read-only view of a scope's cooldown.
type CooldownState struct {
	Phase        string     `json:"phase"`
	Until        *time.Time `json:"until,omitempty"`
	RetryAfterMS int64      `json:"retry_after_ms,omitempty"`
}

type scopeState struct {
	obsAt   time.Time     // observation the state was derived from
	length  time.Duration // unbounded; the policy maximum applies in until()
	cleared bool          // a probe response showed the provider is no longer limiting
	probing bool
	probeAt time.Time
	gen     uint64
}

// Gate turns CapacitySignal observations into admission decisions. State is
// process-local; it never calls the provider, acquires credentials or touches
// quota.
type Gate struct {
	signal       CapacitySignal
	clock        func() time.Time
	probeTimeout time.Duration
	maxScopes    int

	mu     sync.Mutex
	scopes map[string]*scopeState
	next   uint64
}

func NewGate(signal CapacitySignal) *Gate {
	return &Gate{signal: signal, clock: time.Now, probeTimeout: defaultProbeTimeout, maxScopes: defaultMaxGateScopes, scopes: map[string]*scopeState{}}
}

// limited reports whether an observation is a throttling response whose
// retry may succeed soon. Quota exhaustion is excluded: waiting out a short
// cooldown would not change the outcome.
func limited(l Limits) bool {
	return l.Classification == RateLimited || l.Classification == Overloaded
}

func windows(l Limits) []*Window {
	return []*Window{l.Requests, l.Tokens, l.InputTokens, l.OutputTokens}
}

// jitter01 is a stable fraction in [0,1) per scope and observation, so every
// view of one observation agrees and retry hints do not flap.
func jitter01(key string, at time.Time) float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	_, _ = fmt.Fprintf(h, "|%d", at.UnixNano())
	return float64(h.Sum64()>>11) / float64(1<<53)
}

// cooldownLength derives the wait from the observation: the provider's
// retry-after, else the reset of an exhausted window, else the earliest
// window reset (rate limits only), else a short default with jitter. The
// result is unbounded here; the policy maximum applies where it is used, so a
// policy change takes effect on the current observation. ok is false when the provider said to retry now.
func cooldownLength(key string, l Limits) (time.Duration, bool) {
	var d time.Duration
	switch {
	case l.RetryAfterMS != nil:
		d = time.Duration(*l.RetryAfterMS) * time.Millisecond
		if d <= 0 {
			return 0, false
		}
	default:
		found := false
		if l.Classification == RateLimited {
			var exhausted, earliest time.Duration
			haveExhausted, haveEarliest := false, false
			for _, w := range windows(l) {
				if w == nil || w.ResetAt == nil {
					continue
				}
				wait := w.ResetAt.Sub(l.ObservedAt)
				if wait <= 0 {
					continue
				}
				if w.Remaining != nil && *w.Remaining == 0 && (!haveExhausted || wait > exhausted) {
					exhausted, haveExhausted = wait, true
				}
				if !haveEarliest || wait < earliest {
					earliest, haveEarliest = wait, true
				}
			}
			switch {
			case haveExhausted:
				d, found = exhausted, true
			case haveEarliest:
				d, found = earliest, true
			}
		}
		if !found {
			d = DefaultBackoff + time.Duration(jitter01(key, l.ObservedAt)*float64(DefaultBackoff/2))
		}
	}
	return d, true
}

func scopeKey(tenant, surface string) string { return storeKey(tenant, surface) }

// Admit decides one request for a tenant's surface. weight is the caller's
// QoS weight; zero means "no QoS" and takes the policy's default weight. A
// disabled policy, an absent observation or a stale one always admits.
func (g *Gate) Admit(tenant, surface string, p Policy, weight uint32) Decision {
	if g == nil || g.signal == nil || tenant == "" || surface == "" || !p.Enabled() {
		return Decision{Allow: true}
	}
	p = p.WithDefaults()
	if weight == 0 {
		weight = p.DefaultWeight
	}
	lim, ok := g.signal.LastObserved(tenant, surface)
	key := scopeKey(tenant, surface)
	now := g.clock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if !ok {
		delete(g.scopes, key)
		return Decision{Allow: true}
	}
	age := now.Sub(lim.ObservedAt)
	if age < -time.Second {
		// A future-dated observation cannot be trusted as fresh.
		return Decision{Allow: true}
	}
	var st *scopeState
	if p.CooldownEnabled && limited(lim) {
		st = g.stateFor(key, lim, p)
		if st != nil && !st.cleared && now.Before(st.until(p)) {
			return Decision{Reason: ReasonCooldown, RetryAfter: st.until(p).Sub(now)}
		}
	} else {
		delete(g.scopes, key)
	}
	if p.HeadroomFraction > 0 && weight < p.MinWeight && age <= p.maxAge() {
		if wait, shed := shedWait(key, lim, p, now); shed {
			return Decision{Reason: ReasonShed, RetryAfter: wait}
		}
	}
	if st == nil || st.cleared || now.Sub(st.until(p)) > p.maxAge() {
		// No cooldown, a recovered one, or a limiting observation so old that
		// it no longer describes the provider.
		return Decision{Allow: true}
	}
	// Half-open: exactly one probe until it responds or times out.
	if st.probing && now.Sub(st.probeAt) < g.probeTimeout {
		return Decision{Reason: ReasonCooldown, RetryAfter: probeRetryHint}
	}
	st.probing, st.probeAt = true, now
	g.next++
	st.gen = g.next
	return Decision{Allow: true, Probe: true, gate: g, key: key, gen: st.gen}
}

// stateFor returns the scope state for a limiting observation, deriving it
// when the observation is new. Callers hold g.mu.
func (g *Gate) stateFor(key string, lim Limits, p Policy) *scopeState {
	st := g.scopes[key]
	if st != nil && st.obsAt.Equal(lim.ObservedAt) {
		return st
	}
	length, ok := cooldownLength(key, lim)
	if !ok {
		delete(g.scopes, key)
		return nil
	}
	if st == nil && len(g.scopes) >= g.maxScopes {
		for k := range g.scopes {
			delete(g.scopes, k)
			break
		}
	}
	st = &scopeState{obsAt: lim.ObservedAt, length: length}
	g.scopes[key] = st
	return st
}

func (s *scopeState) until(p Policy) time.Time {
	length := s.length
	if max := p.maxCooldown(); length > max {
		length = max
	}
	return s.obsAt.Add(length)
}

// shedWait reports whether any requests or token window is below the
// reserved fraction, and how long until the reset of those windows. Unknown
// members are never treated as zero; a window whose reset has passed has
// replenished and is ignored.
func shedWait(key string, lim Limits, p Policy, now time.Time) (time.Duration, bool) {
	var wait time.Duration
	shed, known := false, false
	for _, w := range windows(lim) {
		if w == nil || w.Limit == nil || *w.Limit <= 0 || w.Remaining == nil {
			continue
		}
		if w.ResetAt != nil && !w.ResetAt.After(now) {
			continue
		}
		if float64(*w.Remaining) >= p.HeadroomFraction*float64(*w.Limit) {
			continue
		}
		shed = true
		if w.ResetAt != nil {
			known = true
			if d := w.ResetAt.Sub(now); d > wait {
				wait = d
			}
		}
	}
	if !shed {
		return 0, false
	}
	if !known {
		wait = DefaultBackoff + time.Duration(jitter01(key, lim.ObservedAt)*float64(DefaultBackoff/2))
	}
	if max := p.maxCooldown(); wait > max {
		wait = max
	}
	return wait, true
}

// Responded tells the gate the probe reached the provider. A response that is
// not itself limiting closes the cooldown; a limiting one starts a new
// cooldown from its own observation on the next Admit. Safe on any Decision.
func (d Decision) Responded(response Limits) {
	if d.gate == nil || !d.Probe {
		return
	}
	g := d.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.scopes[d.key]
	if st == nil || st.gen != d.gen {
		return
	}
	st.probing = false
	if !limited(response) {
		st.cleared = true
	}
}

// Done releases an unanswered probe (the request ended before the provider
// responded, for example a quota denial or a cancelled client) so the next
// request can probe. Idempotent and safe on any Decision.
func (d Decision) Done() {
	if d.gate == nil || !d.Probe {
		return
	}
	g := d.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if st := g.scopes[d.key]; st != nil && st.gen == d.gen {
		st.probing = false
	}
}

// State reports the scope's cooldown without changing it.
func (g *Gate) State(tenant, surface string, p Policy) CooldownState {
	none := CooldownState{Phase: PhaseNone}
	if g == nil || g.signal == nil || !p.CooldownEnabled {
		return none
	}
	p = p.WithDefaults()
	lim, ok := g.signal.LastObserved(tenant, surface)
	now := g.clock()
	if !ok || !limited(lim) || now.Sub(lim.ObservedAt) < -time.Second {
		return none
	}
	key := scopeKey(tenant, surface)
	g.mu.Lock()
	defer g.mu.Unlock()
	var st scopeState
	if cur := g.scopes[key]; cur != nil && cur.obsAt.Equal(lim.ObservedAt) {
		st = *cur
	} else {
		length, ok := cooldownLength(key, lim)
		if !ok {
			return none
		}
		st = scopeState{obsAt: lim.ObservedAt, length: length}
	}
	until := st.until(p)
	switch {
	case st.cleared || now.Sub(until) > p.maxAge():
		return none
	case now.Before(until):
		return CooldownState{Phase: PhaseCooling, Until: &until, RetryAfterMS: until.Sub(now).Milliseconds()}
	case st.probing && now.Sub(st.probeAt) < g.probeTimeout:
		return CooldownState{Phase: PhaseProbing, Until: &until}
	}
	return CooldownState{Phase: PhaseHalf, Until: &until}
}

// NewGateWithClock is NewGate with an injected clock, for tests that must move
// time without sleeping.
func NewGateWithClock(signal CapacitySignal, clock func() time.Time) *Gate {
	g := NewGate(signal)
	if clock != nil {
		g.clock = clock
	}
	return g
}

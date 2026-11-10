package routing

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Breaker states.
const (
	StateClosed   = "closed"
	StateOpen     = "open"
	StateHalfOpen = "half_open"
)

// maxBreakers bounds the number of tracked backends per process.
const maxBreakers = 10000

// maxRecent is how many recent outcomes a breaker keeps for operators.
const maxRecent = 8

// RecentOutcome is one remembered attempt outcome (no request content).
type RecentOutcome struct {
	At             time.Time `json:"at"`
	Outcome        string    `json:"outcome"`
	UpstreamStatus int       `json:"upstream_status,omitempty"`
}

// BreakerState is the operator view of one backend's breaker.
type BreakerState struct {
	Key                 string          `json:"-"`
	State               string          `json:"state"`
	ConsecutiveFailures int             `json:"consecutive_failures"`
	OpenedAt            *time.Time      `json:"opened_at,omitempty"`
	RetryAt             *time.Time      `json:"half_open_at,omitempty"`
	Recent              []RecentOutcome `json:"recent,omitempty"`
}

type breaker struct {
	state    string
	failures int
	opened   time.Time
	openFor  time.Duration
	probing  bool
	recent   []RecentOutcome
	touched  time.Time
}

// Breakers holds the circuit breakers of one process. State is in memory
// only: it is not shared between replicas and is lost on restart.
type Breakers struct {
	mu  sync.Mutex
	m   map[string]*breaker
	now func() time.Time
	// OnTransition, when set, is called (outside the lock) with the new
	// state each time a breaker changes state.
	OnTransition func(state string)
}

// NewBreakers returns an empty set. now may be nil (time.Now).
func NewBreakers(now func() time.Time) *Breakers {
	if now == nil {
		now = time.Now
	}
	return &Breakers{m: map[string]*breaker{}, now: now}
}

// Admission is the result of Allow.
type Admission struct {
	Allowed bool
	// Probe is true for the single half-open trial request; its result
	// closes or reopens the breaker.
	Probe bool
	State string
}

func (s *Breakers) get(key string) *breaker {
	b := s.m[key]
	if b == nil {
		if len(s.m) >= maxBreakers {
			s.evict()
		}
		b = &breaker{state: StateClosed}
		s.m[key] = b
	}
	b.touched = s.now()
	return b
}

// evict drops the least recently used closed breakers (never an open one).
func (s *Breakers) evict() {
	type kv struct {
		k string
		t time.Time
	}
	var closed []kv
	for k, b := range s.m {
		if b.state == StateClosed {
			closed = append(closed, kv{k, b.touched})
		}
	}
	sort.Slice(closed, func(i, j int) bool { return closed[i].t.Before(closed[j].t) })
	for i := 0; i < len(closed) && i < maxBreakers/10+1; i++ {
		delete(s.m, closed[i].k)
	}
}

// Allow asks whether an attempt may go to the backend. An open breaker whose
// interval has passed admits exactly one probe at a time.
func (s *Breakers) Allow(key string, e Effective) Admission {
	if !e.Breaker {
		return Admission{Allowed: true, State: StateClosed}
	}
	s.mu.Lock()
	b := s.get(key)
	now := s.now()
	var transition string
	defer func() {
		s.mu.Unlock()
		if transition != "" && s.OnTransition != nil {
			s.OnTransition(transition)
		}
	}()
	switch b.state {
	case StateOpen:
		if now.Sub(b.opened) < b.openFor {
			return Admission{State: StateOpen}
		}
		b.state, b.probing = StateHalfOpen, true
		transition = StateHalfOpen
		return Admission{Allowed: true, Probe: true, State: StateHalfOpen}
	case StateHalfOpen:
		if b.probing {
			return Admission{State: StateHalfOpen}
		}
		b.probing = true
		return Admission{Allowed: true, Probe: true, State: StateHalfOpen}
	}
	return Admission{Allowed: true, State: StateClosed}
}

// Record reports an attempt's outcome. A breaker failure counts towards the
// threshold; a success closes the breaker; any other outcome (a refusal by
// AIPlex, a client error, a skip, a cache hit, which says nothing about the
// backend) leaves the counts alone but ends a probe.
func (s *Breakers) Record(key string, e Effective, a Admission, outcome string, upstreamStatus int) {
	if !e.Breaker {
		return
	}
	s.mu.Lock()
	b := s.get(key)
	now := s.now()
	b.recent = append(b.recent, RecentOutcome{At: now.UTC(), Outcome: outcome, UpstreamStatus: upstreamStatus})
	if len(b.recent) > maxRecent {
		b.recent = b.recent[len(b.recent)-maxRecent:]
	}
	transition := ""
	if a.Probe {
		b.probing = false
	}
	switch {
	case outcome == OutcomeOK:
		if b.state != StateClosed {
			transition = StateClosed
		}
		b.state, b.failures = StateClosed, 0
	case BreakerFailure(outcome):
		b.failures++
		if b.state == StateHalfOpen || (b.state == StateClosed && b.failures >= e.BreakerFailures) {
			b.state, b.opened, b.openFor = StateOpen, now, e.BreakerOpen
			transition = StateOpen
		}
	}
	s.mu.Unlock()
	if transition != "" && s.OnTransition != nil {
		s.OnTransition(transition)
	}
}

// Release ends a probe whose attempt never reached the backend.
func (s *Breakers) Release(key string, a Admission) {
	if !a.Probe {
		return
	}
	s.mu.Lock()
	if b := s.m[key]; b != nil {
		b.probing = false
	}
	s.mu.Unlock()
}

// State returns one breaker's state (closed when unknown).
func (s *Breakers) State(key string) BreakerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.m[key]
	out := BreakerState{Key: key, State: StateClosed}
	if b == nil {
		return out
	}
	out.State, out.ConsecutiveFailures = b.state, b.failures
	if b.state == StateOpen && s.now().Sub(b.opened) >= b.openFor {
		// The next request will probe.
		out.State = StateHalfOpen
	}
	if b.state != StateClosed {
		opened, retry := b.opened.UTC(), b.opened.Add(b.openFor).UTC()
		out.OpenedAt, out.RetryAt = &opened, &retry
	}
	out.Recent = append([]RecentOutcome(nil), b.recent...)
	return out
}

// Counts returns how many tracked breakers are in each state.
func (s *Breakers) Counts() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int64{StateClosed: 0, StateOpen: 0, StateHalfOpen: 0}
	now := s.now()
	for _, b := range s.m {
		state := b.state
		if state == StateOpen && now.Sub(b.opened) >= b.openFor {
			state = StateHalfOpen
		}
		out[state]++
	}
	return out
}

// Reset forgets every breaker whose key starts with prefix.
func (s *Breakers) Reset(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.m {
		if strings.HasPrefix(k, prefix) {
			delete(s.m, k)
		}
	}
}

// BreakerKey names one backend of one access-point model of one tenant.
func BreakerKey(tenant, accessPoint, model string, b Backend) string {
	return strings.Join([]string{tenant, accessPoint, model, b.SurfaceRef, b.NativeModel}, "\x00")
}

package routing

import (
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"
)

func intp(v int) *int { return &v }

func TestDefaultsRetryOnlyNotExecutedSignals(t *testing.T) {
	e := (*Routing)(nil).Effective()
	for _, c := range []string{OutcomeConnectError, OutcomeRateLimited, OutcomeOverloaded, OutcomeUnavailable} {
		if !e.Retries(c) || !e.FallsOver(c) {
			t.Errorf("%s should be retried and fall over by default", c)
		}
	}
	for _, c := range []string{OutcomeServerError, OutcomeBadGateway, OutcomeGatewayTimeout, OutcomeRequestTimeout, OutcomeTransportError, OutcomeUpstreamError, OutcomeRefused} {
		if e.Retries(c) || e.FallsOver(c) {
			t.Errorf("%s must not be retried or fall over by default", c)
		}
	}
	if e.FallsOver(OutcomeQuotaDenied) {
		t.Error("a quota denial must be final by default")
	}
	if !e.FallsOver(OutcomeCapacity) || e.Retries(OutcomeCapacity) {
		t.Error("a capacity refusal falls over and is not retried on the same backend")
	}
	for _, c := range []string{OutcomeNotGranted, OutcomeBreakerOpen} {
		if !e.FallsOver(c) {
			t.Errorf("%s is a skip", c)
		}
	}
	q := (&Routing{Fallback: &Fallback{OnQuotaDenied: QuotaDeniedNextBackend}}).Effective()
	if !q.FallsOver(OutcomeQuotaDenied) {
		t.Error("on_quota_denied next_backend")
	}
	off := (&Routing{Fallback: &Fallback{Disabled: true}}).Effective()
	if off.FallsOver(OutcomeRateLimited) || !off.FallsOver(OutcomeNotGranted) {
		t.Error("disabled fallback still skips ungranted backends but does not fall over")
	}
}

func TestValidate(t *testing.T) {
	bad := []*Routing{
		{Strategy: "random"},
		{Retry: &Retry{On: []string{"transport_error"}}},
		{Retry: &Retry{On: []string{"rate_limited", "rate_limited"}}},
		{Retry: &Retry{MaxAttempts: 9}},
		{Retry: &Retry{BaseBackoffMS: 5000, MaxBackoffMS: 100}},
		{Fallback: &Fallback{On: []string{"refused"}}},
		{Fallback: &Fallback{OnQuotaDenied: "maybe"}},
		{LowestCost: &LowestCost{InputWeight: 1}},
		{DeadlineMS: int64(time.Hour / time.Millisecond)},
	}
	for i, r := range bad {
		if r.Validate(2) == nil {
			t.Errorf("case %d validated", i)
		}
	}
	good := &Routing{Strategy: StrategyLowestCost, LowestCost: &LowestCost{InputWeight: 1, OutputWeight: 1, RequirePrice: true}, Retry: &Retry{On: []string{"server_error"}}}
	if err := good.Validate(2); err != nil {
		t.Fatal(err)
	}
	if ValidateBackends([]Backend{{SurfaceRef: "a", NativeModel: "m", Weight: intp(0)}}) == nil {
		t.Error("all zero weights")
	}
	if ValidateBackends([]Backend{{SurfaceRef: "a", NativeModel: "m"}, {SurfaceRef: "a", NativeModel: "m"}}) == nil {
		t.Error("duplicate backend")
	}
	if (&Cache{TTL: "0s"}).Validate() == nil || (&Cache{TTL: "10m", Scope: "everyone"}).Validate() == nil {
		t.Error("cache validation")
	}
	if (&Cache{TTL: "10m"}).EffectiveScope() != CacheScopePrincipal {
		t.Error("cache scope defaults to principal")
	}
}

func TestBackoffAndRetryAfter(t *testing.T) {
	for n, want := range map[int]time.Duration{1: 100 * time.Millisecond, 2: 200 * time.Millisecond, 3: 400 * time.Millisecond, 10: time.Second} {
		if got := Backoff(n, 200*time.Millisecond, 2*time.Second, func() float64 { return 0 }); got != want {
			t.Errorf("backoff %d = %v, want %v", n, got, want)
		}
	}
	if got := Backoff(1, 200*time.Millisecond, 2*time.Second, func() float64 { return 0.999 }); got >= 200*time.Millisecond || got < 199*time.Millisecond {
		t.Errorf("jitter bound: %v", got)
	}
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	h := http.Header{}
	h.Set("Retry-After", "3")
	if d, ok := RetryAfter(h, now); !ok || d != 3*time.Second {
		t.Fatal(d, ok)
	}
	h.Set("Retry-After-Ms", "250")
	if d, ok := RetryAfter(h, now); !ok || d != 250*time.Millisecond {
		t.Fatal(d, ok)
	}
	h = http.Header{}
	h.Set("Retry-After", now.Add(2*time.Second).Format(http.TimeFormat))
	if d, ok := RetryAfter(h, now); !ok || d != 2*time.Second {
		t.Fatal(d, ok)
	}
	h.Set("Retry-After", "soon")
	if _, ok := RetryAfter(h, now); ok {
		t.Fatal("garbage hint")
	}
	// A hint above the cap means "not here"; within it, it is honoured.
	if _, ok := Wait(100*time.Millisecond, 7*time.Second, true, 5*time.Second, time.Minute); ok {
		t.Error("hint above cap must not be shortened")
	}
	if d, ok := Wait(100*time.Millisecond, time.Second, true, 5*time.Second, time.Minute); !ok || d != time.Second {
		t.Error("hint honoured", d, ok)
	}
	if _, ok := Wait(100*time.Millisecond, time.Second, true, 5*time.Second, 500*time.Millisecond); ok {
		t.Error("wait beyond remaining budget")
	}
}

func TestBreakerOpensAndHalfOpens(t *testing.T) {
	now := time.Unix(1000, 0)
	b := NewBreakers(func() time.Time { return now })
	var transitions []string
	b.OnTransition = func(s string) { transitions = append(transitions, s) }
	e := (&Routing{Breaker: &Breaker{FailureThreshold: 2, OpenMS: 1000}}).Effective()
	key := "k"
	for i := 0; i < 2; i++ {
		a := b.Allow(key, e)
		if !a.Allowed {
			t.Fatal("closed breaker refused")
		}
		b.Record(key, e, a, OutcomeUnavailable, 503)
	}
	if a := b.Allow(key, e); a.Allowed || a.State != StateOpen {
		t.Fatalf("breaker should be open: %+v", a)
	}
	now = now.Add(1100 * time.Millisecond)
	probe := b.Allow(key, e)
	if !probe.Allowed || !probe.Probe || probe.State != StateHalfOpen {
		t.Fatalf("half-open probe: %+v", probe)
	}
	if a := b.Allow(key, e); a.Allowed {
		t.Fatal("only one probe at a time")
	}
	b.Record(key, e, probe, OutcomeUnavailable, 503)
	if a := b.Allow(key, e); a.Allowed {
		t.Fatal("failed probe reopens")
	}
	now = now.Add(1100 * time.Millisecond)
	probe = b.Allow(key, e)
	b.Record(key, e, probe, OutcomeOK, 200)
	if s := b.State(key); s.State != StateClosed || s.ConsecutiveFailures != 0 || len(s.Recent) != 4 {
		t.Fatalf("closed after a good probe: %+v", s)
	}
	if strings.Join(transitions, ",") != "open,half_open,open,half_open,closed" {
		t.Fatalf("transitions %v", transitions)
	}
	// A refusal by AIPlex does not count against the backend.
	for i := 0; i < 5; i++ {
		a := b.Allow(key, e)
		b.Record(key, e, a, OutcomeRefused, 0)
	}
	if b.State(key).State != StateClosed {
		t.Fatal("refusals opened the breaker")
	}
}

func TestOrderStrategies(t *testing.T) {
	backends := []Backend{{SurfaceRef: "a", Priority: 1}, {SurfaceRef: "b"}, {SurfaceRef: "c", Weight: intp(0)}}
	order, _ := Order((&Routing{}).Effective(), backends, nil, nil)
	if order[0].Index != 1 || order[1].Index != 2 || order[2].Index != 0 {
		t.Fatalf("priority order %v", order)
	}
	w := (&Routing{Strategy: StrategyWeighted}).Effective()
	seen := map[int]int{}
	for r := 0; r < 2; r++ {
		order, _ = Order(w, backends, nil, func(int) int { return r })
		seen[order[0].Index]++
		if len(order) != 3 || order[0].Reason != ReasonWeightedPick {
			t.Fatalf("weighted %v", order)
		}
	}
	if seen[2] != 0 || seen[0] != 1 || seen[1] != 1 {
		t.Fatalf("weight 0 is never picked first: %v", seen)
	}
	rat := func(s string) *big.Rat { r, _ := new(big.Rat).SetString(s); return r }
	prices := []Price{{Known: true, Currency: "USD", Amount: rat("5")}, {Known: false}, {Known: true, Currency: "USD", Amount: rat("2")}}
	lc := (&Routing{Strategy: StrategyLowestCost}).Effective()
	order, excluded := Order(lc, backends, prices, nil)
	if order[0].Index != 2 || order[1].Index != 0 || order[2].Index != 1 || order[2].Reason != ReasonPriceUnknown || len(excluded) != 0 {
		t.Fatalf("lowest cost: unknown sorts last: %v", order)
	}
	lc.RequirePrice = true
	order, excluded = Order(lc, backends, prices, nil)
	if len(order) != 2 || len(excluded) != 1 || excluded[0].Index != 1 {
		t.Fatalf("require_price excludes unknown: %v %v", order, excluded)
	}
	// Ties keep priority order.
	tie := []Price{{Known: true, Currency: "USD", Amount: rat("1")}, {Known: true, Currency: "USD", Amount: rat("1")}, {Known: true, Currency: "USD", Amount: rat("1")}}
	order, _ = Order((&Routing{Strategy: StrategyLowestCost}).Effective(), backends, tie, nil)
	if order[0].Index != 1 || order[1].Index != 2 || order[2].Index != 0 {
		t.Fatalf("tie -> priority %v", order)
	}
}

func TestMemoryCacheLRUAndExpiry(t *testing.T) {
	c := NewMemoryCache(2000)
	now := time.Unix(0, 0)
	put := func(k string, n int) {
		c.Put(k, CacheEntry{Status: 200, Body: make([]byte, n), TenantID: "t", AccessPoint: "ap", Model: "m", Expires: now.Add(time.Minute)})
	}
	put("a", 600)
	put("b", 600)
	if _, ok := c.Get("a", now); !ok {
		t.Fatal("a")
	}
	put("c", 600) // evicts b, the least recently used
	if _, ok := c.Get("b", now); ok {
		t.Fatal("b should have been evicted")
	}
	if _, ok := c.Get("a", now.Add(2*time.Minute)); ok {
		t.Fatal("expired entry served")
	}
	put("big", 5000)
	if _, ok := c.Get("big", now); ok {
		t.Fatal("entry above the store bound kept")
	}
	if n := c.Purge("t", "ap", ""); n != 1 {
		t.Fatalf("purge removed %d", n)
	}
	if c.Stats().Entries != 0 {
		t.Fatal(c.Stats())
	}
}

func TestCacheKeyAndCanonicalJSON(t *testing.T) {
	a, err := CanonicalJSON([]byte(`{"model":"native-a", "messages":[{"role":"user","content":"hi"}],"temperature":0.10}`), "m")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := CanonicalJSON([]byte(`{"temperature":0.10,"messages":[{"content":"hi","role":"user"}],"model":"native-b"}`), "m")
	if string(a) != string(b) || !strings.Contains(string(a), "0.10") {
		t.Fatalf("canonical forms differ: %s vs %s", a, b)
	}
	base := CacheKeyParts{TenantID: "t", AccessPoint: "ap", Model: "m", Wire: "w", Scope: CacheScopePrincipal, Principal: "alice", Body: a}
	other := base
	other.Principal = "bob"
	if CacheKey(base) == CacheKey(other) {
		t.Fatal("principal scope must separate callers")
	}
	x, y := base, base
	x.TenantID, x.AccessPoint = "ab", "c"
	y.TenantID, y.AccessPoint = "a", "bc"
	if CacheKey(x) == CacheKey(y) {
		t.Fatal("length prefixes must keep field boundaries")
	}
}

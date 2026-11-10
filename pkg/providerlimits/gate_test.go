package providerlimits

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var gateNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

type gateFixture struct {
	t     *testing.T
	store *Store
	gate  *Gate
	now   time.Time
}

func newGateFixture(t *testing.T) *gateFixture {
	f := &gateFixture{t: t, store: NewStore(0), now: gateNow}
	f.gate = NewGate(f.store)
	f.gate.clock = func() time.Time { return f.now }
	return f
}

// observe records a provider response the way the gateway does, at the
// fixture's current time.
func (f *gateFixture) observe(tenant, surface, provider string, status int, headers map[string]string) Limits {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	l := Parse(provider, status, h, f.now)
	f.store.Record(tenant, surface, l)
	return l
}

var cooldownPolicy = Policy{CooldownEnabled: true}

func TestCooldownFromEachProviderSignalShape(t *testing.T) {
	future := func(d time.Duration) string { return gateNow.Add(d).Format(time.RFC3339) }
	tests := []struct {
		name     string
		provider string
		status   int
		headers  map[string]string
		want     time.Duration // 0 with defaulted true means the default backoff band
		defaults bool
	}{
		{"openai duration reset of exhausted window", OpenAI, 429, map[string]string{"X-Ratelimit-Remaining-Requests": "0", "X-Ratelimit-Limit-Requests": "60", "X-Ratelimit-Reset-Requests": "20s"}, 20 * time.Second, false},
		{"openai exhausted window beats a shorter one", OpenAI, 429, map[string]string{"X-Ratelimit-Remaining-Requests": "0", "X-Ratelimit-Reset-Requests": "20s", "X-Ratelimit-Remaining-Tokens": "500", "X-Ratelimit-Reset-Tokens": "2s"}, 20 * time.Second, false},
		{"openai retry-after wins over resets", OpenAI, 429, map[string]string{"Retry-After": "7", "X-Ratelimit-Remaining-Requests": "0", "X-Ratelimit-Reset-Requests": "20s"}, 7 * time.Second, false},
		{"openai minute-second duration", OpenAI, 429, map[string]string{"X-Ratelimit-Remaining-Tokens": "0", "X-Ratelimit-Reset-Tokens": "6m0s", "Retry-After": "40"}, 40 * time.Second, false},
		{"anthropic rfc3339 reset", Anthropic, 429, map[string]string{"Anthropic-Ratelimit-Requests-Remaining": "0", "Anthropic-Ratelimit-Requests-Reset": future(30 * time.Second)}, 30 * time.Second, false},
		{"anthropic input tokens window", Anthropic, 429, map[string]string{"Anthropic-Ratelimit-Input-Tokens-Remaining": "0", "Anthropic-Ratelimit-Input-Tokens-Reset": future(12 * time.Second)}, 12 * time.Second, false},
		{"anthropic overloaded with retry-after", Anthropic, 529, map[string]string{"Retry-After": "9"}, 9 * time.Second, false},
		{"anthropic overloaded ignores unrelated resets", Anthropic, 529, map[string]string{"Anthropic-Ratelimit-Requests-Reset": future(50 * time.Second)}, 0, true},
		{"retry-after seconds", OpenAICompatible, 429, map[string]string{"Retry-After": "3"}, 3 * time.Second, false},
		{"retry-after http date", OpenAICompatible, 429, map[string]string{"Retry-After": gateNow.Add(15 * time.Second).UTC().Format(http.TimeFormat)}, 15 * time.Second, false},
		{"azure retry-after-ms", AzureOpenAI, 429, map[string]string{"Retry-After-Ms": "1500"}, 1500 * time.Millisecond, false},
		{"azure retry-after-ms beats retry-after", AzureOpenAI, 429, map[string]string{"Retry-After-Ms": "250", "Retry-After": "30"}, 250 * time.Millisecond, false},
		{"bedrock throttling with retry-after", Bedrock, 400, map[string]string{"X-Amzn-Errortype": "ThrottlingException:http://x", "Retry-After": "4"}, 4 * time.Second, false},
		{"openai 503 overloaded", OpenAI, 503, map[string]string{"Retry-After": "5"}, 5 * time.Second, false},
		{"missing retry value uses jittered default", OpenAI, 429, nil, 0, true},
		{"unparseable retry value uses jittered default", OpenAI, 429, map[string]string{"Retry-After": "soon", "Retry-After-Ms": "NaN", "X-Ratelimit-Reset-Requests": "garbage"}, 0, true},
		{"negative retry value uses default", OpenAI, 429, map[string]string{"Retry-After": "-5"}, 0, true},
		{"absurd retry value uses default not blackhole", OpenAI, 429, map[string]string{"Retry-After": "99999999"}, 0, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newGateFixture(t)
			f.observe("t", "s", test.provider, test.status, test.headers)
			d := f.gate.Admit("t", "s", cooldownPolicy, 0)
			if d.Allow || d.Reason != ReasonCooldown {
				t.Fatalf("not rejected: %+v", d)
			}
			if test.defaults {
				if d.RetryAfter < DefaultBackoff || d.RetryAfter >= DefaultBackoff+DefaultBackoff/2 {
					t.Fatalf("default backoff outside the jitter band: %v", d.RetryAfter)
				}
			} else if d.RetryAfter != test.want {
				t.Fatalf("retry after %v, want %v", d.RetryAfter, test.want)
			}
			// The hint shrinks as time passes and the cooldown ends at the bound.
			f.now = f.now.Add(d.RetryAfter - time.Millisecond)
			if again := f.gate.Admit("t", "s", cooldownPolicy, 0); again.Allow || again.RetryAfter != time.Millisecond {
				t.Fatalf("cooldown ended early: %+v", again)
			}
		})
	}
}

func TestCooldownIsBoundedAndConfigurable(t *testing.T) {
	f := newGateFixture(t)
	f.observe("t", "s", OpenAI, 429, map[string]string{"Retry-After": "3600"})
	if d := f.gate.Admit("t", "s", cooldownPolicy, 0); d.Allow || d.RetryAfter != DefaultMaxCooldown {
		t.Fatalf("default bound not applied: %+v", d)
	}
	if d := f.gate.Admit("t", "s", Policy{CooldownEnabled: true, MaxCooldownSeconds: 5}, 0); d.Allow || d.RetryAfter != 5*time.Second {
		t.Fatalf("configured bound not applied: %+v", d)
	}
	// A reset time is bounded the same way.
	g := newGateFixture(t)
	g.observe("t", "s", Anthropic, 429, map[string]string{"Anthropic-Ratelimit-Requests-Remaining": "0", "Anthropic-Ratelimit-Requests-Reset": gateNow.Add(6 * time.Hour).Format(time.RFC3339)})
	if d := g.gate.Admit("t", "s", cooldownPolicy, 0); d.Allow || d.RetryAfter != DefaultMaxCooldown {
		t.Fatalf("reset not bounded: %+v", d)
	}
}

func TestCooldownDisabledOrNotApplicableAdmits(t *testing.T) {
	cases := []struct {
		name    string
		policy  Policy
		status  int
		headers map[string]string
	}{
		{"cooldown disabled", Policy{}, 429, map[string]string{"Retry-After": "30"}},
		{"success response", cooldownPolicy, 200, map[string]string{"Retry-After": "30", "X-Ratelimit-Remaining-Requests": "10"}},
		{"retry now", cooldownPolicy, 429, map[string]string{"Retry-After": "0"}},
		{"quota exceeded is not a short cooldown", cooldownPolicy, 400, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newGateFixture(t)
			provider := OpenAI
			if c.name == "quota exceeded is not a short cooldown" {
				provider = Bedrock
				c.headers = map[string]string{"X-Amzn-Errortype": "ServiceQuotaExceededException", "Retry-After": "30"}
			}
			f.observe("t", "s", provider, c.status, c.headers)
			if d := f.gate.Admit("t", "s", c.policy, 0); !d.Allow || d.Probe {
				t.Fatalf("should admit: %+v", d)
			}
		})
	}
	// No observation at all is never zero capacity.
	f := newGateFixture(t)
	if d := f.gate.Admit("t", "s", cooldownPolicy, 0); !d.Allow {
		t.Fatal("absent signal rejected")
	}
	if d := (*Gate)(nil).Admit("t", "s", cooldownPolicy, 0); !d.Allow {
		t.Fatal("nil gate rejected")
	}
}

func TestCooldownIsTenantAndSurfaceIsolated(t *testing.T) {
	f := newGateFixture(t)
	f.observe("tenant-a", "s", OpenAI, 429, map[string]string{"Retry-After": "30"})
	if d := f.gate.Admit("tenant-a", "s", cooldownPolicy, 0); d.Allow {
		t.Fatal("tenant a not in cooldown")
	}
	if d := f.gate.Admit("tenant-b", "s", cooldownPolicy, 0); !d.Allow {
		t.Fatal("tenant a's cooldown leaked to tenant b")
	}
	if d := f.gate.Admit("tenant-a", "other", cooldownPolicy, 0); !d.Allow {
		t.Fatal("cooldown leaked to another surface")
	}
	if st := f.gate.State("tenant-b", "s", cooldownPolicy); st.Phase != PhaseNone {
		t.Fatalf("state leaked: %+v", st)
	}
}

func TestHalfOpenAdmitsExactlyOneProbeUnderConcurrency(t *testing.T) {
	f := newGateFixture(t)
	f.observe("t", "s", OpenAI, 429, map[string]string{"Retry-After": "10"})
	f.now = f.now.Add(10 * time.Second) // cooldown ends
	if st := f.gate.State("t", "s", cooldownPolicy); st.Phase != PhaseHalf {
		t.Fatalf("state: %+v", st)
	}
	var allowed, probes, rejected atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	decisions := make(chan Decision, 200)
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			d := f.gate.Admit("t", "s", cooldownPolicy, 0)
			if d.Allow {
				allowed.Add(1)
				if d.Probe {
					probes.Add(1)
				}
				decisions <- d
			} else {
				rejected.Add(1)
				if d.Reason != ReasonCooldown || d.RetryAfter <= 0 {
					t.Errorf("bad probe-in-flight rejection %+v", d)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if allowed.Load() != 1 || probes.Load() != 1 || rejected.Load() != 199 {
		t.Fatalf("allowed=%d probes=%d rejected=%d", allowed.Load(), probes.Load(), rejected.Load())
	}
	if st := f.gate.State("t", "s", cooldownPolicy); st.Phase != PhaseProbing {
		t.Fatalf("state during probe: %+v", st)
	}
	probe := <-decisions
	// The probe succeeds with no limit headers: the cooldown closes for good.
	probe.Responded(Parse(OpenAI, 200, http.Header{}, f.now))
	for i := 0; i < 5; i++ {
		if d := f.gate.Admit("t", "s", cooldownPolicy, 0); !d.Allow || d.Probe {
			t.Fatalf("not reopened after a good probe: %+v", d)
		}
	}
	if st := f.gate.State("t", "s", cooldownPolicy); st.Phase != PhaseNone {
		t.Fatalf("state after recovery: %+v", st)
	}
}

func TestProbeThatIsLimitedAgainStartsANewCooldown(t *testing.T) {
	f := newGateFixture(t)
	f.observe("t", "s", OpenAI, 429, map[string]string{"Retry-After": "10"})
	f.now = f.now.Add(10 * time.Second)
	probe := f.gate.Admit("t", "s", cooldownPolicy, 0)
	if !probe.Allow || !probe.Probe {
		t.Fatalf("no probe: %+v", probe)
	}
	f.now = f.now.Add(time.Second)
	limited := f.observe("t", "s", OpenAI, 429, map[string]string{"Retry-After": "20"})
	probe.Responded(limited)
	d := f.gate.Admit("t", "s", cooldownPolicy, 0)
	if d.Allow || d.RetryAfter != 20*time.Second {
		t.Fatalf("second cooldown not started from the new observation: %+v", d)
	}
}

func TestUnansweredProbeIsReleasedOrExpires(t *testing.T) {
	f := newGateFixture(t)
	f.observe("t", "s", OpenAI, 429, map[string]string{"Retry-After": "10"})
	f.now = f.now.Add(10 * time.Second)
	first := f.gate.Admit("t", "s", cooldownPolicy, 0)
	if !first.Probe {
		t.Fatal("no probe")
	}
	// The probe died before reaching the provider (for example a quota denial):
	// the cooldown stays and the next request probes.
	first.Done()
	first.Done() // idempotent
	second := f.gate.Admit("t", "s", cooldownPolicy, 0)
	if !second.Allow || !second.Probe {
		t.Fatalf("slot not released: %+v", second)
	}
	// A late Done from the superseded probe must not free the new probe's slot.
	first.Done()
	if d := f.gate.Admit("t", "s", cooldownPolicy, 0); d.Allow {
		t.Fatal("stale release freed a live probe")
	}
	// A probe that never reports is replaced after the probe timeout.
	f.now = f.now.Add(f.gate.probeTimeout)
	if d := f.gate.Admit("t", "s", cooldownPolicy, 0); !d.Allow || !d.Probe {
		t.Fatalf("stuck probe never replaced: %+v", d)
	}
}

func TestStaleCooldownObservationIsIgnoredAfterItsWindow(t *testing.T) {
	f := newGateFixture(t)
	policy := Policy{CooldownEnabled: true, SignalMaxAgeSeconds: 30}
	f.observe("t", "s", OpenAI, 429, map[string]string{"Retry-After": "10"})
	f.now = f.now.Add(10*time.Second + 31*time.Second)
	if d := f.gate.Admit("t", "s", policy, 0); !d.Allow || d.Probe {
		t.Fatalf("a long-expired observation gated traffic: %+v", d)
	}
	if st := f.gate.State("t", "s", policy); st.Phase != PhaseNone {
		t.Fatalf("stale state reported: %+v", st)
	}
}

func headroomObservation(f *gateFixture, remaining, limit string) {
	f.observe("t", "s", OpenAI, 200, map[string]string{"X-Ratelimit-Remaining-Requests": remaining, "X-Ratelimit-Limit-Requests": limit, "X-Ratelimit-Reset-Requests": "30s"})
}

var headroomPolicy = Policy{HeadroomFraction: 0.2, MinWeight: 10, DefaultWeight: 1}

func TestHeadroomShedsByWeight(t *testing.T) {
	f := newGateFixture(t)
	headroomObservation(f, "10", "100") // 10% remaining, below the 20% reserve
	for _, c := range []struct {
		weight uint32
		allow  bool
	}{{0, false}, {1, false}, {9, false}, {10, true}, {500, true}} {
		d := f.gate.Admit("t", "s", headroomPolicy, c.weight)
		if d.Allow != c.allow {
			t.Fatalf("weight %d: %+v", c.weight, d)
		}
		if !c.allow && (d.Reason != ReasonShed || d.RetryAfter != 30*time.Second) {
			t.Fatalf("shed without a reset-derived hint: %+v", d)
		}
	}
	// At or above the reserve everyone is admitted.
	f2 := newGateFixture(t)
	headroomObservation(f2, "20", "100")
	if d := f2.gate.Admit("t", "s", headroomPolicy, 1); !d.Allow {
		t.Fatalf("shed at exactly the reserve: %+v", d)
	}
}

func TestHeadroomDefaultWeightApplies(t *testing.T) {
	f := newGateFixture(t)
	headroomObservation(f, "1", "100")
	p := Policy{HeadroomFraction: 0.5, MinWeight: 5, DefaultWeight: 5}
	if d := f.gate.Admit("t", "s", p, 0); !d.Allow {
		t.Fatalf("unclassified caller with default weight 5 shed: %+v", d)
	}
	p.DefaultWeight = 0 // built-in default is 1
	if d := f.gate.Admit("t", "s", p, 0); d.Allow {
		t.Fatalf("unclassified caller not shed at the built-in default weight: %+v", d)
	}
}

func TestHeadroomUsesAnyTokenOrRequestWindow(t *testing.T) {
	f := newGateFixture(t)
	f.observe("t", "s", Anthropic, 200, map[string]string{
		"Anthropic-Ratelimit-Requests-Limit": "100", "Anthropic-Ratelimit-Requests-Remaining": "90",
		"Anthropic-Ratelimit-Output-Tokens-Limit": "1000", "Anthropic-Ratelimit-Output-Tokens-Remaining": "50",
		"Anthropic-Ratelimit-Output-Tokens-Reset": gateNow.Add(8 * time.Second).Format(time.RFC3339),
	})
	d := f.gate.Admit("t", "s", headroomPolicy, 1)
	if d.Allow || d.RetryAfter != 8*time.Second {
		t.Fatalf("low output-token window not honoured: %+v", d)
	}
}

func TestHeadroomIgnoresStaleAbsentAndUnknownSignals(t *testing.T) {
	t.Run("stale", func(t *testing.T) {
		f := newGateFixture(t)
		headroomObservation(f, "0", "100")
		f.now = f.now.Add(61 * time.Second)
		if d := f.gate.Admit("t", "s", headroomPolicy, 1); !d.Allow {
			t.Fatalf("stale signal shed: %+v", d)
		}
		f.now = gateNow.Add(59 * time.Second)
		f.observe("t", "s", OpenAI, 200, map[string]string{"X-Ratelimit-Remaining-Requests": "0", "X-Ratelimit-Limit-Requests": "100"})
		if d := f.gate.Admit("t", "s", Policy{HeadroomFraction: 0.2, MinWeight: 10, SignalMaxAgeSeconds: 5}, 1); d.Allow {
			t.Fatalf("fresh signal under a configured age not used: %+v", d)
		}
		f.now = f.now.Add(6 * time.Second)
		if d := f.gate.Admit("t", "s", Policy{HeadroomFraction: 0.2, MinWeight: 10, SignalMaxAgeSeconds: 5}, 1); !d.Allow {
			t.Fatalf("configured max age ignored: %+v", d)
		}
	})
	t.Run("absent observation", func(t *testing.T) {
		f := newGateFixture(t)
		if d := f.gate.Admit("t", "s", headroomPolicy, 1); !d.Allow {
			t.Fatal("absent signal treated as zero capacity")
		}
	})
	t.Run("unknown members are not zero", func(t *testing.T) {
		f := newGateFixture(t)
		// Remaining only, no limit; limit only, no remaining; reset only.
		f.observe("t", "s", OpenAI, 200, map[string]string{"X-Ratelimit-Remaining-Requests": "0", "X-Ratelimit-Limit-Tokens": "100", "X-Ratelimit-Reset-Tokens": "5s"})
		if d := f.gate.Admit("t", "s", headroomPolicy, 1); !d.Allow {
			t.Fatalf("incomplete window shed: %+v", d)
		}
		g := newGateFixture(t)
		g.observe("t", "s", OpenAI, 200, map[string]string{"X-Ratelimit-Remaining-Requests": "garbage", "X-Ratelimit-Limit-Requests": "100"})
		if d := g.gate.Admit("t", "s", headroomPolicy, 1); !d.Allow {
			t.Fatalf("unparseable remaining treated as zero: %+v", d)
		}
	})
	t.Run("window already reset", func(t *testing.T) {
		f := newGateFixture(t)
		headroomObservation(f, "0", "100") // resets in 30s
		f.now = f.now.Add(31 * time.Second)
		p := headroomPolicy
		p.SignalMaxAgeSeconds = 120
		if d := f.gate.Admit("t", "s", p, 1); !d.Allow {
			t.Fatalf("a replenished window still shed: %+v", d)
		}
	})
	t.Run("zero limit", func(t *testing.T) {
		f := newGateFixture(t)
		f.observe("t", "s", OpenAI, 200, map[string]string{"X-Ratelimit-Remaining-Requests": "0", "X-Ratelimit-Limit-Requests": "0"})
		if d := f.gate.Admit("t", "s", headroomPolicy, 1); !d.Allow {
			t.Fatalf("zero limit shed: %+v", d)
		}
	})
	t.Run("disabled", func(t *testing.T) {
		f := newGateFixture(t)
		headroomObservation(f, "0", "100")
		if d := f.gate.Admit("t", "s", Policy{}, 1); !d.Allow {
			t.Fatal("disabled policy shed")
		}
	})
	t.Run("no reset uses the jittered default", func(t *testing.T) {
		f := newGateFixture(t)
		f.observe("t", "s", OpenAI, 200, map[string]string{"X-Ratelimit-Remaining-Requests": "0", "X-Ratelimit-Limit-Requests": "100"})
		d := f.gate.Admit("t", "s", headroomPolicy, 1)
		if d.Allow || d.RetryAfter < DefaultBackoff || d.RetryAfter >= DefaultBackoff+DefaultBackoff/2 {
			t.Fatalf("hint without a reset: %+v", d)
		}
	})
}

func TestHeadroomShedDoesNotConsumeTheProbe(t *testing.T) {
	f := newGateFixture(t)
	f.observe("t", "s", OpenAI, 429, map[string]string{"Retry-After": "10", "X-Ratelimit-Remaining-Requests": "0", "X-Ratelimit-Limit-Requests": "100", "X-Ratelimit-Reset-Requests": "1m"})
	f.now = f.now.Add(10 * time.Second)
	p := Policy{CooldownEnabled: true, HeadroomFraction: 0.2, MinWeight: 10}
	if d := f.gate.Admit("t", "s", p, 1); d.Allow || d.Reason != ReasonShed {
		t.Fatalf("low weight not shed: %+v", d)
	}
	if d := f.gate.Admit("t", "s", p, 50); !d.Allow || !d.Probe {
		t.Fatalf("high weight did not get the probe: %+v", d)
	}
}

func TestPolicyValidation(t *testing.T) {
	good := []Policy{
		{},
		{CooldownEnabled: true},
		{CooldownEnabled: true, MaxCooldownSeconds: 3600, SignalMaxAgeSeconds: 3600},
		{HeadroomFraction: 1, MinWeight: 1},
		{HeadroomFraction: 0.1, MinWeight: 10, DefaultWeight: 1, SignalMaxAgeSeconds: 30},
		{CooldownEnabled: true, HeadroomFraction: 0.25, MinWeight: 1000, DefaultWeight: 1000},
	}
	for i, p := range good {
		if err := p.Validate(); err != nil {
			t.Fatalf("good[%d] %+v: %v", i, p, err)
		}
	}
	bad := map[string]Policy{
		"negative fraction":               {HeadroomFraction: -0.1, MinWeight: 1},
		"fraction above one":              {HeadroomFraction: 1.5, MinWeight: 1},
		"negative max cooldown":           {CooldownEnabled: true, MaxCooldownSeconds: -1},
		"max cooldown too large":          {CooldownEnabled: true, MaxCooldownSeconds: 3601},
		"max cooldown without cooldown":   {HeadroomFraction: 0.1, MinWeight: 1, MaxCooldownSeconds: 10},
		"negative age":                    {CooldownEnabled: true, SignalMaxAgeSeconds: -1},
		"age too large":                   {CooldownEnabled: true, SignalMaxAgeSeconds: 3601},
		"age with nothing enabled":        {SignalMaxAgeSeconds: 10},
		"headroom without min weight":     {HeadroomFraction: 0.1},
		"min weight without headroom":     {CooldownEnabled: true, MinWeight: 5},
		"default weight without headroom": {CooldownEnabled: true, DefaultWeight: 5},
		"weight above bound":              {HeadroomFraction: 0.1, MinWeight: 1001},
	}
	for name, p := range bad {
		if err := p.Validate(); err == nil {
			t.Fatalf("%s accepted: %+v", name, p)
		}
	}
	if (Policy{}).Enabled() || !(Policy{CooldownEnabled: true}).Enabled() || !(Policy{HeadroomFraction: 0.1, MinWeight: 1}).Enabled() {
		t.Fatal("Enabled wrong")
	}
	d := Policy{CooldownEnabled: true}.WithDefaults()
	if d.MaxCooldownSeconds != 60 || d.SignalMaxAgeSeconds != 60 || d.DefaultWeight != 1 {
		t.Fatalf("defaults: %+v", d)
	}
}

func TestStateReportsPhases(t *testing.T) {
	f := newGateFixture(t)
	if st := f.gate.State("t", "s", cooldownPolicy); st.Phase != PhaseNone || st.Until != nil {
		t.Fatalf("%+v", st)
	}
	f.observe("t", "s", OpenAI, 429, map[string]string{"Retry-After": "10"})
	st := f.gate.State("t", "s", cooldownPolicy)
	if st.Phase != PhaseCooling || st.RetryAfterMS != 10000 || st.Until == nil || !st.Until.Equal(gateNow.Add(10*time.Second)) {
		t.Fatalf("%+v", st)
	}
	// Reading state must not change it: the probe is still available.
	f.now = f.now.Add(10 * time.Second)
	_ = f.gate.State("t", "s", cooldownPolicy)
	if d := f.gate.Admit("t", "s", cooldownPolicy, 0); !d.Probe {
		t.Fatal("State consumed the probe")
	}
	if st := f.gate.State("t", "s", cooldownPolicy); st.Phase != PhaseProbing {
		t.Fatalf("%+v", st)
	}
	if st := f.gate.State("t", "s", Policy{}); st.Phase != PhaseNone {
		t.Fatal("state reported with cooldown disabled")
	}
}

func TestGateScopeTableIsBounded(t *testing.T) {
	f := newGateFixture(t)
	f.gate.maxScopes = 8
	for i := 0; i < 50; i++ {
		surface := string(rune('a'+i%26)) + string(rune('a'+i/26))
		f.observe("t", surface, OpenAI, 429, map[string]string{"Retry-After": "10"})
		f.gate.Admit("t", surface, cooldownPolicy, 0)
	}
	f.gate.mu.Lock()
	n := len(f.gate.scopes)
	f.gate.mu.Unlock()
	if n > 8 {
		t.Fatalf("gate state grew to %d", n)
	}
}

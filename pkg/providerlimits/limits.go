// Package providerlimits normalizes the rate-limit metadata a model provider
// returns with each native response (remaining/limit/reset windows,
// retry-after and throttling classifications). Observations describe the
// shared upstream credential's capacity at one instant; they are advisory
// signals, never accounting facts and never automatic enforcement.
package providerlimits

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Classifications of a limiting response.
const (
	RateLimited   = "rate_limited"   // HTTP 429 or a documented throttling error
	Overloaded    = "overloaded"     // provider capacity, not caller quota (Anthropic 529, OpenAI 503)
	QuotaExceeded = "quota_exceeded" // account quota or spend cap, retry will not succeed soon
)

// Window is one provider limit dimension. Unknown members stay nil.
type Window struct {
	Limit     *int64     `json:"limit,omitempty"`
	Remaining *int64     `json:"remaining,omitempty"`
	ResetAt   *time.Time `json:"reset_at,omitempty"`
}

func (w *Window) empty() bool {
	return w == nil || (w.Limit == nil && w.Remaining == nil && w.ResetAt == nil)
}

// Limits is one normalized provider limit observation.
type Limits struct {
	Provider       string    `json:"provider"`
	ObservedAt     time.Time `json:"observed_at"`
	HTTPStatus     int       `json:"http_status,omitempty"`
	Classification string    `json:"classification,omitempty"`
	RetryAfterMS   *int64    `json:"retry_after_ms,omitempty"`
	Requests       *Window   `json:"requests,omitempty"`
	Tokens         *Window   `json:"tokens,omitempty"`
	InputTokens    *Window   `json:"input_tokens,omitempty"`
	OutputTokens   *Window   `json:"output_tokens,omitempty"`
}

// Empty reports whether the observation carries no limit signal at all.
func (l Limits) Empty() bool {
	return l.Classification == "" && l.RetryAfterMS == nil && l.Requests.empty() && l.Tokens.empty() && l.InputTokens.empty() && l.OutputTokens.empty()
}

// Clone returns a deep copy so stores and records never share pointers.
func (l Limits) Clone() Limits {
	copyWindow := func(w *Window) *Window {
		if w == nil {
			return nil
		}
		out := &Window{}
		if w.Limit != nil {
			v := *w.Limit
			out.Limit = &v
		}
		if w.Remaining != nil {
			v := *w.Remaining
			out.Remaining = &v
		}
		if w.ResetAt != nil {
			v := *w.ResetAt
			out.ResetAt = &v
		}
		return out
	}
	if l.RetryAfterMS != nil {
		v := *l.RetryAfterMS
		l.RetryAfterMS = &v
	}
	l.Requests, l.Tokens, l.InputTokens, l.OutputTokens = copyWindow(l.Requests), copyWindow(l.Tokens), copyWindow(l.InputTokens), copyWindow(l.OutputTokens)
	return l
}

// Provider identifiers understood by Parse. They equal modelapi provider IDs.
const (
	OpenAI           = "openai"
	Anthropic        = "anthropic"
	AzureOpenAI      = "azure-openai"
	Bedrock          = "bedrock"
	OpenAICompatible = "openai-compatible"
)

// Parse reads the provider's documented limit headers and status. It never
// fails: malformed or absent values remain unknown (nil), never zero.
func Parse(provider string, status int, h http.Header, now time.Time) Limits {
	out := Limits{Provider: provider, ObservedAt: now.UTC(), HTTPStatus: status}
	if ms, ok := retryAfter(h, now); ok {
		out.RetryAfterMS = &ms
	}
	switch provider {
	case OpenAI, AzureOpenAI, OpenAICompatible:
		// OpenAI: x-ratelimit-{limit,remaining,reset}-{requests,tokens};
		// reset values are Go-style durations ("1s", "6m0s"). Azure OpenAI
		// documents the remaining-* headers; others stay unknown.
		out.Requests = window(h, "x-ratelimit-limit-requests", "x-ratelimit-remaining-requests", "x-ratelimit-reset-requests", durationReset(now))
		out.Tokens = window(h, "x-ratelimit-limit-tokens", "x-ratelimit-remaining-tokens", "x-ratelimit-reset-tokens", durationReset(now))
	case Anthropic:
		reset := rfc3339Reset
		out.Requests = window(h, "anthropic-ratelimit-requests-limit", "anthropic-ratelimit-requests-remaining", "anthropic-ratelimit-requests-reset", reset)
		out.Tokens = window(h, "anthropic-ratelimit-tokens-limit", "anthropic-ratelimit-tokens-remaining", "anthropic-ratelimit-tokens-reset", reset)
		out.InputTokens = window(h, "anthropic-ratelimit-input-tokens-limit", "anthropic-ratelimit-input-tokens-remaining", "anthropic-ratelimit-input-tokens-reset", reset)
		out.OutputTokens = window(h, "anthropic-ratelimit-output-tokens-limit", "anthropic-ratelimit-output-tokens-remaining", "anthropic-ratelimit-output-tokens-reset", reset)
	}
	out.Classification = classify(provider, status, h)
	return out
}

func classify(provider string, status int, h http.Header) string {
	switch {
	case provider == Anthropic && status == 529:
		return Overloaded
	case provider == Bedrock:
		// Bedrock returns the exception shape name in x-amzn-ErrorType.
		kind := h.Get("X-Amzn-Errortype")
		if i := strings.IndexByte(kind, ':'); i >= 0 {
			kind = kind[:i]
		}
		switch kind {
		case "ThrottlingException":
			return RateLimited
		case "ServiceQuotaExceededException":
			return QuotaExceeded
		case "ServiceUnavailableException", "ModelNotReadyException":
			return Overloaded
		}
		if status == 429 {
			return RateLimited
		}
		if status == 503 {
			return Overloaded
		}
	case status == 429:
		return RateLimited
	case status == 503 && (provider == OpenAI || provider == AzureOpenAI):
		return Overloaded
	}
	return ""
}

func header(h http.Header, name string) string { return strings.TrimSpace(h.Get(name)) }

func window(h http.Header, limit, remaining, reset string, parseReset func(string) *time.Time) *Window {
	w := &Window{Limit: count(header(h, limit)), Remaining: count(header(h, remaining))}
	if raw := header(h, reset); raw != "" {
		w.ResetAt = parseReset(raw)
	}
	if w.empty() {
		return nil
	}
	return w
}

func count(raw string) *int64 {
	if raw == "" || len(raw) > 20 {
		return nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return nil
	}
	return &n
}

func durationReset(now time.Time) func(string) *time.Time {
	return func(raw string) *time.Time {
		if len(raw) > 32 {
			return nil
		}
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 || d > 31*24*time.Hour {
			return nil
		}
		at := now.UTC().Add(d)
		return &at
	}
}

func rfc3339Reset(raw string) *time.Time {
	if len(raw) > 64 {
		return nil
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil
	}
	at = at.UTC()
	return &at
}

// retryAfter accepts retry-after-ms (milliseconds), Retry-After seconds or an
// HTTP date. Values are bounded to one day.
func retryAfter(h http.Header, now time.Time) (int64, bool) {
	if raw := header(h, "Retry-After-Ms"); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil && f >= 0 && f <= 86400000 && !math.IsNaN(f) {
			return int64(math.Ceil(f)), true
		}
	}
	raw := header(h, "Retry-After")
	if raw == "" {
		return 0, false
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		if f < 0 || f > 86400 || math.IsNaN(f) {
			return 0, false
		}
		return int64(math.Ceil(f * 1000)), true
	}
	if at, err := http.ParseTime(raw); err == nil {
		d := at.Sub(now)
		if d < 0 {
			d = 0
		}
		if d > 24*time.Hour {
			return 0, false
		}
		return d.Milliseconds(), true
	}
	return 0, false
}

// Headers lists every response header Parse may read, for operators enabling
// passthrough. Names are canonical.
func Headers(provider string) []string {
	names := []string{"Retry-After", "Retry-After-Ms"}
	switch provider {
	case OpenAI, AzureOpenAI, OpenAICompatible:
		for _, kind := range []string{"limit", "remaining", "reset"} {
			for _, dimension := range []string{"requests", "tokens", "project-tokens"} {
				names = append(names, http.CanonicalHeaderKey("x-ratelimit-"+kind+"-"+dimension))
			}
		}
	case Anthropic:
		for _, dimension := range []string{"requests", "tokens", "input-tokens", "output-tokens"} {
			for _, kind := range []string{"limit", "remaining", "reset"} {
				names = append(names, http.CanonicalHeaderKey("anthropic-ratelimit-"+dimension+"-"+kind))
			}
		}
	}
	return names
}

// CapacitySignal is the narrow read contract a quota scheduler may consume
// later. It reports the last provider limit observation for a tenant's
// surface. It is advisory: it never admits, rejects or settles work.
type CapacitySignal interface {
	LastObserved(tenant, surface string) (Limits, bool)
}

// Store is an in-memory, per tenant+surface last-observed capacity store. It
// is process-local and bounded; restarts and other replicas do not share it.
type Store struct {
	mu      sync.Mutex
	max     int
	entries map[string]Limits
}

func NewStore(maxEntries int) *Store {
	if maxEntries <= 0 {
		maxEntries = 4096
	}
	return &Store{max: maxEntries, entries: map[string]Limits{}}
}

func storeKey(tenant, surface string) string { return tenant + "\x00" + surface }

// Record keeps the newest observation that carries a limit signal.
func (s *Store) Record(tenant, surface string, value Limits) {
	if s == nil || tenant == "" || surface == "" || value.Empty() {
		return
	}
	key := storeKey(tenant, surface)
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, ok := s.entries[key]; ok && prior.ObservedAt.After(value.ObservedAt) {
		return
	}
	if _, ok := s.entries[key]; !ok && len(s.entries) >= s.max {
		oldest := ""
		for k, v := range s.entries {
			if oldest == "" || v.ObservedAt.Before(s.entries[oldest].ObservedAt) {
				oldest = k
			}
		}
		delete(s.entries, oldest)
	}
	s.entries[key] = value.Clone()
}

func (s *Store) LastObserved(tenant, surface string) (Limits, bool) {
	if s == nil {
		return Limits{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.entries[storeKey(tenant, surface)]
	if !ok {
		return Limits{}, false
	}
	return value.Clone(), true
}

var _ CapacitySignal = (*Store)(nil)

package providerlimits

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func headers(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func TestOpenAILimitHeaders(t *testing.T) {
	l := Parse(OpenAI, 200, headers("x-ratelimit-limit-requests", "60", "x-ratelimit-remaining-requests", "59", "x-ratelimit-reset-requests", "1s", "x-ratelimit-limit-tokens", "150000", "x-ratelimit-remaining-tokens", "149984", "x-ratelimit-reset-tokens", "6m0s"), now)
	if *l.Requests.Limit != 60 || *l.Requests.Remaining != 59 || !l.Requests.ResetAt.Equal(now.Add(time.Second)) {
		t.Fatalf("requests window %+v", l.Requests)
	}
	if *l.Tokens.Limit != 150000 || *l.Tokens.Remaining != 149984 || !l.Tokens.ResetAt.Equal(now.Add(6*time.Minute)) || l.Classification != "" || l.RetryAfterMS != nil {
		t.Fatalf("tokens window %+v", l)
	}
}

func TestAnthropicLimitHeadersAndOverload(t *testing.T) {
	l := Parse(Anthropic, 529, headers("anthropic-ratelimit-input-tokens-remaining", "1000", "anthropic-ratelimit-input-tokens-reset", "2026-11-09T12:00:30Z", "anthropic-ratelimit-output-tokens-limit", "400000", "retry-after", "3"), now)
	if l.Classification != Overloaded || *l.RetryAfterMS != 3000 || *l.InputTokens.Remaining != 1000 || !l.InputTokens.ResetAt.Equal(now.Add(30*time.Second)) || *l.OutputTokens.Limit != 400000 || l.Requests != nil {
		t.Fatalf("%+v", l)
	}
	if Parse(Anthropic, 429, nil, now).Classification != RateLimited {
		t.Fatal("429 not classified")
	}
}

func TestMalformedValuesStayUnknown(t *testing.T) {
	l := Parse(OpenAI, 200, headers("x-ratelimit-remaining-requests", "-1", "x-ratelimit-limit-tokens", "lots", "x-ratelimit-reset-tokens", "soon", "retry-after", "never"), now)
	if l.Requests != nil || l.Tokens != nil || l.RetryAfterMS != nil || !l.Empty() {
		t.Fatalf("malformed values became known: %+v", l)
	}
	raw, _ := json.Marshal(Parse(OpenAI, 200, http.Header{}, now))
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	if _, ok := doc["requests"]; ok {
		t.Fatal("unknown window serialized as zero")
	}
}

func TestRetryAfterFormsAndBedrockErrors(t *testing.T) {
	if l := Parse(AzureOpenAI, 429, headers("retry-after-ms", "1500", "retry-after", "9"), now); *l.RetryAfterMS != 1500 || l.Classification != RateLimited {
		t.Fatalf("%+v", l)
	}
	if l := Parse(OpenAI, 503, headers("retry-after", now.Add(10*time.Second).Format(http.TimeFormat)), now); *l.RetryAfterMS != 10000 || l.Classification != Overloaded {
		t.Fatalf("%+v", l)
	}
	for kind, want := range map[string]string{"ThrottlingException:http://internal.amazon.com/coral/": RateLimited, "ServiceQuotaExceededException": QuotaExceeded, "ServiceUnavailableException": Overloaded, "ValidationException": ""} {
		if got := Parse(Bedrock, 400, headers("x-amzn-ErrorType", kind), now).Classification; got != want {
			t.Fatalf("%s classified %q", kind, got)
		}
	}
}

func TestStoreKeepsNewestPerTenantSurface(t *testing.T) {
	s := NewStore(2)
	remaining := int64(5)
	s.Record("t1", "s", Limits{Provider: OpenAI, ObservedAt: now, Requests: &Window{Remaining: &remaining}})
	s.Record("t1", "s", Limits{Provider: OpenAI, ObservedAt: now.Add(-time.Second), Classification: RateLimited})
	s.Record("t1", "s", Limits{Provider: OpenAI, ObservedAt: now.Add(time.Second)}) // empty: ignored
	got, ok := s.LastObserved("t1", "s")
	if !ok || got.Classification != "" || *got.Requests.Remaining != 5 {
		t.Fatalf("%+v", got)
	}
	*got.Requests.Remaining = 0
	if again, _ := s.LastObserved("t1", "s"); *again.Requests.Remaining != 5 {
		t.Fatal("store shares mutable state")
	}
	if _, ok := s.LastObserved("t2", "s"); ok {
		t.Fatal("tenant isolation")
	}
	s.Record("t2", "s", Limits{ObservedAt: now.Add(30 * time.Second), Classification: RateLimited})
	s.Record("t3", "s", Limits{ObservedAt: now.Add(time.Minute), Classification: RateLimited})
	if _, ok := s.LastObserved("t1", "s"); ok {
		t.Fatal("bound not enforced")
	}
}

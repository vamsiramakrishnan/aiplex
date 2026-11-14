package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// A compatible surface that declares DeepSeek usage semantics settles the
// openai-text profile with the cache hit as cached input and the miss as
// uncached input. An inconsistent hit/miss split leaves the cache split
// unknown, so the profile refuses the exchange instead of settling it.
func TestDeepSeekSemanticsSettleCachedInputSeparately(t *testing.T) {
	settings := modelapi.Provider{ID: modelapi.ProviderOpenAICompatible, UsageSemantics: modelapi.UsageSemanticsDeepSeek}
	usage := `{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":2},"prompt_cache_hit_tokens":2,"prompt_cache_miss_tokens":5}`
	chat := `{"id":"c1","model":"model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":` + usage + `}`
	test := tokenProfileCase{name: "deepseek", profile: quotaProfileOpenAI, operation: "compat.chat-completions", provider: modelapi.ProviderOpenAICompatible, path: "/v1/chat/completions", body: `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":3}`, response: chat, settings: settings, protocol: modelapi.ProtocolOpenAIChat, auth: bearerRef, meters: map[string]int64{"uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 3}, want: map[string]int64{"uncached_input_tokens": 5, "cached_tokens": 2, "output_tokens": 3, "usd": 10 + 2 + 9}}
	runTokenProfileCase(t, test)

	inconsistent := test
	inconsistent.response = strings.Replace(chat, `"prompt_cache_miss_tokens":5`, `"prompt_cache_miss_tokens":4`, 1)
	f := tokenProfileFixture(t, inconsistent)
	r := f.request("generateContent", "deepseek-inconsistent", inconsistent.body, false)
	r.URL.Path, r.URL.RawQuery = "/api/v1/llm/native/surface"+inconsistent.path, ""
	w := httptest.NewRecorder()
	f.handler.NativeModel(w, r)
	if w.Code == 200 {
		t.Fatal("an inconsistent cache split settled under a token profile")
	}
	if info := quotaHostModelAttempt(t, f, w.Header().Get("X-Aiplex-Attempt-ID")); info.Decision.State == "settled" {
		t.Fatalf("inconsistent split settled: %s %v", info.Decision.State, info.Actual)
	}
}

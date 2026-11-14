package modelapi

import (
	"strings"
	"testing"
)

func deepseekSurface(semantics string) Surface {
	return providerSurfaceFor(Provider{ID: ProviderOpenAICompatible, UsageSemantics: semantics}, ProtocolOpenAIChat, []string{"deepseek-model"}, "compat.chat-completions")("https://api.deepseek.test")
}

// usage_semantics "deepseek" is a declaration of the generic compatible
// provider only; any other value is refused.
func TestDeepSeekUsageSemanticsValidation(t *testing.T) {
	for _, semantics := range []string{UsageSemanticsOpenAI, UsageSemanticsDeepSeek} {
		if err := deepseekSurface(semantics).Validate(); err != nil {
			t.Errorf("%s: %v", semantics, err)
		}
	}
	if err := deepseekSurface("deepseek-v2").Validate(); err == nil || !strings.Contains(err.Error(), "usage_semantics") {
		t.Errorf("unknown semantics: %v", err)
	}
	s := providerSurfaceFor(Provider{ID: ProviderOpenAI, UsageSemantics: UsageSemanticsDeepSeek}, ProtocolOpenAIChat, []string{"m"}, "openai.v1.chat-completions")("https://api.openai.test")
	if err := s.Validate(); err == nil {
		t.Error("deepseek semantics accepted on the openai provider")
	}
}

const deepseekBody = `{"id":"x","object":"chat.completion","model":"deepseek-model","choices":[{"index":0,"message":{"role":"assistant","content":"a"},"finish_reason":"stop"}],"usage":%s}`

func deepseekUsage(t *testing.T, semantics, usage string) map[string]int64 {
	t.Helper()
	value := inspectFor(t, deepseekSurface(semantics), "compat.chat-completions", strings.Replace(deepseekBody, "%s", usage, 1))
	if value.usage == nil {
		t.Fatalf("no usage observed for %s", usage)
	}
	return value.usage.Counters
}

// Declared DeepSeek semantics: the cache hit is cached input, the miss the
// rest, and there are no separately counted cache writes (prompt_tokens is
// hit + miss). Anything inconsistent leaves the cache split unknown.
func TestDeepSeekCacheHitCounters(t *testing.T) {
	c := deepseekUsage(t, UsageSemanticsDeepSeek, `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_tokens_details":{"cached_tokens":1024},"prompt_cache_hit_tokens":1024,"prompt_cache_miss_tokens":176}`)
	if c["input_tokens"] != 1200 || c["cached_tokens"] != 1024 || c["output_tokens"] != 37 || c["total_tokens"] != 1237 {
		t.Fatalf("counters %v", c)
	}
	if w, ok := c["cache_write_tokens"]; !ok || w != 0 {
		t.Fatalf("cache writes: %v", c)
	}
	// prompt_tokens_details is documented as required, but the hit and miss
	// fields alone state the split.
	c = deepseekUsage(t, UsageSemanticsDeepSeek, `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_cache_hit_tokens":1024,"prompt_cache_miss_tokens":176}`)
	if c["cached_tokens"] != 1024 || c["cache_write_tokens"] != 0 {
		t.Fatalf("hit and miss without details: %v", c)
	}
	for name, usage := range map[string]string{
		"hit plus miss is not prompt": `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_tokens_details":{"cached_tokens":1024},"prompt_cache_hit_tokens":1024,"prompt_cache_miss_tokens":170}`,
		"cached is not hit":           `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_tokens_details":{"cached_tokens":1000},"prompt_cache_hit_tokens":1024,"prompt_cache_miss_tokens":176}`,
		"miss missing":                `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_tokens_details":{"cached_tokens":1024},"prompt_cache_hit_tokens":1024}`,
		"hit missing":                 `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_tokens_details":{"cached_tokens":1024},"prompt_cache_miss_tokens":176}`,
		"both missing":                `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_tokens_details":{"cached_tokens":1024}}`,
		"hit null":                    `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_cache_hit_tokens":null,"prompt_cache_miss_tokens":176}`,
		"hit negative":                `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_cache_hit_tokens":-1,"prompt_cache_miss_tokens":1201}`,
		"hit not a number":            `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_cache_hit_tokens":"1024","prompt_cache_miss_tokens":176}`,
		"hit fractional":              `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_cache_hit_tokens":1024.5,"prompt_cache_miss_tokens":175.5}`,
		"cache write reported":        `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_tokens_details":{"cached_tokens":1024,"cache_write_tokens":5},"prompt_cache_hit_tokens":1024,"prompt_cache_miss_tokens":176}`,
		"prompt missing":              `{"completion_tokens":37,"total_tokens":1237,"prompt_cache_hit_tokens":1024,"prompt_cache_miss_tokens":176}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := deepseekUsage(t, UsageSemanticsDeepSeek, usage)
			for _, unknown := range []string{"cached_tokens", "cache_write_tokens"} {
				if v, ok := c[unknown]; ok {
					t.Fatalf("%s = %d, want unknown: %v", unknown, v, c)
				}
			}
			if c["output_tokens"] != 37 {
				t.Fatalf("output lost: %v", c)
			}
		})
	}
	// Without the declaration the private fields are not read: the
	// observation is exactly what it was, inconsistent fields included.
	c = deepseekUsage(t, UsageSemanticsOpenAI, `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_tokens_details":{"cached_tokens":1000},"prompt_cache_hit_tokens":1024,"prompt_cache_miss_tokens":170}`)
	if _, write := c["cache_write_tokens"]; c["cached_tokens"] != 1000 || write {
		t.Fatalf("openai semantics changed: %v", c)
	}
	c = deepseekUsage(t, "", `{"prompt_tokens":1200,"completion_tokens":37,"total_tokens":1237,"prompt_cache_hit_tokens":1024,"prompt_cache_miss_tokens":176}`)
	if _, cached := c["cached_tokens"]; cached {
		t.Fatalf("undeclared surface read the private fields: %v", c)
	}
}

// Stream chunks before the last carry "usage": null and report nothing; the
// last content chunk carries the whole request's usage.
func TestDeepSeekStreamChunks(t *testing.T) {
	s := deepseekSurface(UsageSemanticsDeepSeek)
	value := inspectFor(t, s, "compat.chat-completions", `{"id":"x","object":"chat.completion.chunk","model":"deepseek-model","choices":[{"index":0,"delta":{"content":"a"},"finish_reason":null}],"usage":null}`)
	if value.usage != nil {
		t.Fatalf("a null usage chunk reported %v", value.usage.Counters)
	}
	value = inspectFor(t, s, "compat.chat-completions", `{"id":"x","object":"chat.completion.chunk","model":"deepseek-model","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":11,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":0},"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":7}}`)
	if c := value.usage.Counters; c["input_tokens"] != 7 || c["output_tokens"] != 11 {
		t.Fatalf("final chunk %v", c)
	}
	// A recorded zero is a value, not a missing key.
	knownCacheSplit(t, value.usage.Counters, 0)
	// A non-zero hit with no prompt_tokens_details: only the declared
	// semantics can settle this split.
	value = inspectFor(t, s, "compat.chat-completions", `{"id":"x","object":"chat.completion.chunk","model":"deepseek-model","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":11,"total_tokens":18,"prompt_cache_hit_tokens":4,"prompt_cache_miss_tokens":3}}`)
	knownCacheSplit(t, value.usage.Counters, 4)
	// An inconsistent final chunk leaves the split unknown, as a JSON body does.
	value = inspectFor(t, s, "compat.chat-completions", `{"id":"x","object":"chat.completion.chunk","model":"deepseek-model","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":11,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":2},"prompt_cache_hit_tokens":4,"prompt_cache_miss_tokens":3}}`)
	for _, unknown := range []string{"cached_tokens", "cache_write_tokens"} {
		if v, ok := value.usage.Counters[unknown]; ok {
			t.Fatalf("%s = %d in an inconsistent chunk, want unknown: %v", unknown, v, value.usage.Counters)
		}
	}
}

// knownCacheSplit requires both cache counters to be present: indexing a
// missing key reads 0, which would let "unknown" pass as "zero".
func knownCacheSplit(t *testing.T, c map[string]int64, cached int64) {
	t.Helper()
	if v, ok := c["cached_tokens"]; !ok || v != cached {
		t.Fatalf("cached tokens: want %d recorded, got %v", cached, c)
	}
	if v, ok := c["cache_write_tokens"]; !ok || v != 0 {
		t.Fatalf("cache writes: want 0 recorded, got %v", c)
	}
}

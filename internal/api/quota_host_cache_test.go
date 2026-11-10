package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
)

// tokenProfileCase drives one managed strict-profile exchange end to end:
// admission with trusted bounds, native dispatch, exact settlement of every
// disjoint meter and money, then a replay that must not dispatch or charge.
type tokenProfileCase struct {
	name, profile, operation, provider, path, body, response, media string
	// google selects the Google surface (with operation) instead of a
	// provider surface built from settings/protocol/auth.
	google   bool
	settings modelapi.Provider
	protocol string
	auth     *toolcredential.Auth
	// meters: native counter -> price per token (usd). "requests" implicit.
	meters map[string]int64
	// want: expected settled meters (including usd); nil means the attempt
	// must stay unknown with every hold retained.
	want map[string]int64
	code int
	// upstream replaces the canned success response.
	upstream func(http.ResponseWriter)
}

func tokenProfileFixture(t *testing.T, test tokenProfileCase) *quotaHostModelFixture {
	t.Helper()
	f := quotaHostModelNew(t, true, func(w http.ResponseWriter, r *http.Request) {
		if test.upstream != nil {
			test.upstream(w)
			return
		}
		media := test.media
		if media == "" {
			media = "application/json"
		}
		w.Header().Set("Content-Type", media)
		_, _ = io.WriteString(w, test.response)
	}, func(cfg *QuotaHostConfig) {
		cfg.Bindings = cfg.Bindings[:1]
		binding := &cfg.Bindings[0]
		binding.OperationID, binding.Profile, binding.Provider = test.operation, test.profile, test.provider
		if !test.google {
			binding.BillingAccount, binding.Region = "", ""
		}
		policy := &cfg.Tenants[0].Policy
		for _, native := range []string{"uncached_input_tokens", "cached_tokens", "output_tokens", "thought_tokens"} {
			delete(binding.Meters, native)
			delete(policy.Meters, native)
		}
		budgets := policy.Budgets[:0]
		for _, budget := range policy.Budgets {
			if budget.MeterID == "usd" {
				budget.Limit = 1_000_000
			}
			if _, priced := test.meters[budget.MeterID]; priced || budget.MeterID == "usd" || budget.MeterID == "requests" {
				budgets = append(budgets, budget)
			}
		}
		policy.Budgets = budgets
		book := &policy.PriceBooks[0]
		book.Prices = nil
		for native, price := range test.meters {
			policy.Meters[native] = quotaplex.Meter{ID: native, Unit: "tokens", Billable: true}
			binding.Meters[native] = native
			book.Prices = append(book.Prices, quotaplex.UnitPrice{MeterID: native, MoneyMeterID: "usd", Numerator: price, Denominator: 1})
		}
		book.Match.Provider = test.provider
		if !test.google {
			book.Match.BillingAccount, book.Match.Region = "", ""
		}
	})
	if test.google {
		record, err := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
		if err != nil {
			t.Fatal(err)
		}
		var connection ModelConnection
		if err = json.Unmarshal(record.Spec, &connection); err != nil {
			t.Fatal(err)
		}
		ops, err := modelapi.SelectOperations(test.operation)
		if err != nil {
			t.Fatal(err)
		}
		connection.Surface.Operations = ops
		connection.Surface.Endpoint = "openapi"
		if test.operation == "openai.chat-completions" {
			connection.Surface.Protocol = "openai-chat"
		}
		putProviderSurface(t, f.resources, connection)
		return f
	}
	installProviderCredentials(t, f.handler)
	record, _ := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
	var google ModelConnection
	_ = json.Unmarshal(record.Spec, &google)
	connection := providerConnection(t, google.Surface.Origin, test.settings, test.protocol, test.auth, test.operation)
	if err := f.handler.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, connection)); err != nil {
		t.Fatal("provider surface rejected:", err)
	}
	putProviderSurface(t, f.resources, connection)
	return f
}

func runTokenProfileCase(t *testing.T, test tokenProfileCase) {
	f := tokenProfileFixture(t, test)
	r := f.request("generateContent", "token-profile-"+test.name, test.body, false)
	r.URL.Path, r.URL.RawQuery = "/api/v1/llm/native/surface"+test.path, ""
	if i := strings.Index(test.path, "?"); i >= 0 {
		r.URL.Path, r.URL.RawQuery = "/api/v1/llm/native/surface"+test.path[:i], test.path[i+1:]
	}
	w := httptest.NewRecorder()
	f.handler.NativeModel(w, r)
	want := test.code
	if want == 0 {
		want = 200
	}
	if w.Code != want || f.calls.Load() != 1 {
		t.Fatalf("managed exchange %d (want %d) calls=%d: %s", w.Code, want, f.calls.Load(), w.Body.String())
	}
	info := quotaHostModelAttempt(t, f, w.Header().Get("X-Aiplex-Attempt-ID"))
	engine := f.host.Engines()["tenant"]
	if test.want == nil {
		if info.Decision.State != "unknown" || info.Claim.Fence == 0 {
			t.Fatal("unmetered or incomplete exchange did not retain uncertainty", info.Decision.State, info.Actual)
		}
		for _, budget := range info.Plan.Budgets {
			balance, err := engine.BudgetBalance(context.Background(), "tenant", budget.Key)
			if err != nil || balance.Held <= 0 || balance.Consumed != 0 {
				t.Fatal("uncertain exposure released", budget.MeterID, balance, err)
			}
		}
		return
	}
	if w.Body.String() != test.response {
		t.Fatal("native bytes changed", w.Body.String())
	}
	if info.Decision.State != "settled" {
		t.Fatal("exchange did not settle", info.Decision.State, info.Actual)
	}
	for meter, value := range test.want {
		if info.Actual[meter] != value {
			t.Fatalf("%s settled %d want %d (all %v)", meter, info.Actual[meter], value, info.Actual)
		}
	}
	consumed := map[string]int64{}
	for _, budget := range info.Plan.Budgets {
		balance, err := engine.BudgetBalance(context.Background(), "tenant", budget.Key)
		if err != nil || balance.Held != 0 || balance.Consumed != test.want[budget.MeterID]+map[bool]int64{true: 1}[budget.MeterID == "requests"] {
			t.Fatal("budget did not settle exactly", budget.MeterID, balance, err)
		}
		consumed[budget.Key] = balance.Consumed
	}
	// Replay: same key never redispatches and never charges twice.
	retry := httptest.NewRecorder()
	retryRequest := r.Clone(r.Context())
	retryRequest.Body = io.NopCloser(strings.NewReader(test.body))
	f.handler.NativeModel(retry, retryRequest)
	if retry.Code != 409 || f.calls.Load() != 1 {
		t.Fatal("same attempt redispatched", retry.Code, f.calls.Load())
	}
	for _, budget := range info.Plan.Budgets {
		balance, _ := engine.BudgetBalance(context.Background(), "tenant", budget.Key)
		if balance.Consumed != consumed[budget.Key] || balance.Held != 0 {
			t.Fatal("replay changed settled budget", budget.MeterID, balance)
		}
	}
}

const (
	cacheAnthropicBody = `{"model":"model-v1","system":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]}],"max_tokens":3}`
	cacheAnthropicJSON = `{"id":"msg_1","type":"message","model":"model-v1","stop_reason":"end_turn","usage":{"input_tokens":20,"cache_read_input_tokens":50,"cache_creation_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":20},"output_tokens":3}}`
)

func TestManagedTokenProfilesSettleCacheWritesExactly(t *testing.T) {
	anthropic := modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}
	bedrock := modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}
	ttlMeters := map[string]int64{"uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 3, "cache_write_5m_tokens": 3, "cache_write_1h_tokens": 4}
	sseResponse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"model-v1\",\"usage\":{\"input_tokens\":20,\"cache_read_input_tokens\":50,\"cache_creation_input_tokens\":30,\"cache_creation\":{\"ephemeral_5m_input_tokens\":10,\"ephemeral_1h_input_tokens\":20},\"output_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	converseFrames := func(final bool) string {
		var stream []byte
		frames := [][2]string{{"messageStart", `{"role":"assistant"}`}, {"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"hi"}}`}}
		if final {
			frames = append(frames, [2]string{"messageStop", `{"stopReason":"end_turn"}`}, [2]string{"metadata", `{"usage":{"inputTokens":20,"outputTokens":3,"totalTokens":103,"cacheReadInputTokens":50,"cacheWriteInputTokens":30,"cacheDetails":[{"inputTokens":20,"ttl":"1h"},{"inputTokens":10,"ttl":"5m"}]},"metrics":{"latencyMs":10}}`})
		}
		for _, frame := range frames {
			stream = append(stream, bedrockTestFrame(frame[0], frame[1])...)
		}
		return string(stream)
	}
	ttlWant := map[string]int64{"uncached_input_tokens": 20, "cached_tokens": 50, "cache_write_5m_tokens": 10, "cache_write_1h_tokens": 20, "output_tokens": 3, "usd": 20*2 + 50*1 + 10*3 + 20*4 + 3*3}
	converseBody := `{"messages":[{"role":"user","content":[{"text":"stable"},{"cachePoint":{"type":"default","ttl":"1h"}},{"text":"hello"}]}],"inferenceConfig":{"maxTokens":3}}`
	tests := []tokenProfileCase{
		{name: "anthropic-json-ttl", profile: quotaProfileAnthropic, operation: "anthropic.messages", provider: modelapi.ProviderAnthropic, path: "/v1/messages", body: cacheAnthropicBody, response: cacheAnthropicJSON, settings: anthropic, protocol: "anthropic-messages", auth: anthropicRef, meters: ttlMeters, want: ttlWant},
		{name: "anthropic-sse-ttl", profile: quotaProfileAnthropic, operation: "anthropic.messages", provider: modelapi.ProviderAnthropic, path: "/v1/messages", body: strings.Replace(cacheAnthropicBody, `"max_tokens":3`, `"max_tokens":3,"stream":true`, 1), response: sseResponse, media: "text/event-stream", settings: anthropic, protocol: "anthropic-messages", auth: anthropicRef, meters: ttlMeters, want: ttlWant},
		{name: "anthropic-automatic-total", profile: quotaProfileAnthropic, operation: "anthropic.messages", provider: modelapi.ProviderAnthropic, path: "/v1/messages", body: `{"model":"model-v1","cache_control":{"type":"ephemeral"},"messages":[{"role":"user","content":"hello"}],"max_tokens":3}`, response: cacheAnthropicJSON, settings: anthropic, protocol: "anthropic-messages", auth: anthropicRef, meters: map[string]int64{"uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 3, "cache_write_tokens": 3}, want: map[string]int64{"uncached_input_tokens": 20, "cached_tokens": 50, "cache_write_tokens": 30, "output_tokens": 3, "usd": 40 + 50 + 90 + 9}},
		// Positive cache writes with no cache-write meter fail closed.
		{name: "anthropic-unmetered-write", profile: quotaProfileAnthropic, operation: "anthropic.messages", provider: modelapi.ProviderAnthropic, path: "/v1/messages", body: `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_tokens":3}`, response: cacheAnthropicJSON, settings: anthropic, protocol: "anthropic-messages", auth: anthropicRef, meters: map[string]int64{"uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 3}, code: 502},
		// A stream cut before message_stop retains the whole hold.
		{name: "anthropic-sse-interrupted", profile: quotaProfileAnthropic, operation: "anthropic.messages", provider: modelapi.ProviderAnthropic, path: "/v1/messages", body: strings.Replace(cacheAnthropicBody, `"max_tokens":3`, `"max_tokens":3,"stream":true`, 1), response: sseResponse[:strings.Index(sseResponse, "event: message_delta")], media: "text/event-stream", settings: anthropic, protocol: "anthropic-messages", auth: anthropicRef, meters: ttlMeters},
		{name: "openai-responses-total", profile: quotaProfileOpenAI, operation: "openai.v1.responses", provider: modelapi.ProviderOpenAI, path: "/v1/responses", body: `{"model":"model-v1","input":"hello","max_output_tokens":3,"store":false,"prompt_cache_key":"tenant-app"}`, response: `{"id":"resp_1","model":"model-v1","status":"completed","usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":40,"cache_write_tokens":50,"cache_write_tokens_details":{"ttl_30m_tokens":50,"ttl_12h_tokens":0}},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":103}}`, settings: modelapi.Provider{ID: modelapi.ProviderOpenAI}, protocol: "openai-responses", auth: bearerRef, meters: map[string]int64{"uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 3, "cache_write_30m_tokens": 3, "cache_write_12h_tokens": 5}, want: map[string]int64{"uncached_input_tokens": 10, "cached_tokens": 40, "cache_write_30m_tokens": 50, "cache_write_12h_tokens": 0, "output_tokens": 3, "usd": 20 + 40 + 150 + 9}},
		{name: "bedrock-converse-json", profile: quotaProfileBedrockConverse, operation: "bedrock.converse", provider: modelapi.ProviderBedrock, path: "/model/model-v1/converse", body: converseBody, response: `{"output":{"message":{"role":"assistant","content":[{"text":"hi"}]}},"stopReason":"end_turn","usage":{"inputTokens":20,"outputTokens":3,"totalTokens":103,"cacheReadInputTokens":50,"cacheWriteInputTokens":30,"cacheDetails":[{"inputTokens":20,"ttl":"1h"},{"inputTokens":10,"ttl":"5m"}]},"metrics":{"latencyMs":10}}`, settings: bedrock, protocol: "bedrock-converse", auth: bearerRef, meters: ttlMeters, want: ttlWant},
		{name: "bedrock-converse-stream", profile: quotaProfileBedrockConverse, operation: "bedrock.converse-stream", provider: modelapi.ProviderBedrock, path: "/model/model-v1/converse-stream", body: converseBody, response: converseFrames(true), media: "application/vnd.amazon.eventstream", settings: bedrock, protocol: "bedrock-converse", auth: bearerRef, meters: ttlMeters, want: ttlWant},
		{name: "bedrock-converse-stream-interrupted", profile: quotaProfileBedrockConverse, operation: "bedrock.converse-stream", provider: modelapi.ProviderBedrock, path: "/model/model-v1/converse-stream", body: converseBody, response: converseFrames(false), media: "application/vnd.amazon.eventstream", settings: bedrock, protocol: "bedrock-converse", auth: bearerRef, meters: ttlMeters},
		{name: "bedrock-invoke-anthropic", profile: quotaProfileAnthropic, operation: "bedrock.invoke", provider: modelapi.ProviderBedrock, path: "/model/model-v1/invoke", body: `{"anthropic_version":"bedrock-2023-05-31","max_tokens":3,"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`, response: cacheAnthropicJSON, settings: bedrock, protocol: "bedrock-invoke", auth: bearerRef, meters: ttlMeters, want: ttlWant},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { runTokenProfileCase(t, test) })
	}
}

// The Vertex OpenAI-compatible endpoint (no cached-input breakdown) settles
// total input and billed output when its own total proves the composition,
// and Gemini streamGenerateContent without alt=sse (one JSON array) is
// admitted, observed whole and settled before delivery.
func TestManagedGoogleCompositionProfilesSettle(t *testing.T) {
	array := `[{"responseId":"native-response","modelVersion":"model-v1","candidates":[{"index":0,"content":{"parts":[{"text":"native"}]}}]},{"responseId":"native-response","modelVersion":"model-v1","candidates":[{"index":0,"content":{"parts":[{"text":" answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"cachedContentTokenCount":1,"candidatesTokenCount":2,"thoughtsTokenCount":1,"totalTokenCount":6}}]`
	tests := []tokenProfileCase{
		{name: "vertex-openai-exclusive-reasoning", google: true, profile: quotaProfileVertexOpenAI, operation: "openai.chat-completions", provider: "google", path: "/v1/projects/project/locations/us-central1/endpoints/openapi/chat/completions", body: `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":3}`, response: `{"id":"chatcmpl-1","model":"model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":1,"completion_tokens_details":{"reasoning_tokens":2},"total_tokens":10}}`, meters: map[string]int64{"input_tokens": 2, "output_tokens": 3}, want: map[string]int64{"input_tokens": 7, "output_tokens": 3, "usd": 14 + 9}},
		// Reasoning present but no total: composition unproven, fail closed.
		{name: "vertex-openai-unproven", google: true, profile: quotaProfileVertexOpenAI, operation: "openai.chat-completions", provider: "google", path: "/v1/projects/project/locations/us-central1/endpoints/openapi/chat/completions", body: `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":3}`, response: `{"id":"chatcmpl-1","model":"model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":1,"completion_tokens_details":{"reasoning_tokens":2}}}`, meters: map[string]int64{"input_tokens": 2, "output_tokens": 3}, code: 502},
		{name: "gemini-json-array-stream", google: true, profile: quotaProfileGemini, operation: "models.streamGenerateContent", provider: "google", path: "/v1/projects/project/locations/us-central1/publishers/google/models/model-v1:streamGenerateContent", body: quotaHostModelBody, response: array, meters: map[string]int64{"uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 3, "thought_tokens": 3}, want: map[string]int64{"uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 2, "thought_tokens": 1, "usd": 14}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { runTokenProfileCase(t, test) })
	}
}

func TestCacheWriteBindingsMustMapOneCompleteOption(t *testing.T) {
	for name, meters := range map[string][]string{
		"total and per-TTL": {"cache_write_tokens", "cache_write_5m_tokens", "cache_write_1h_tokens"},
		"partial per-TTL":   {"cache_write_5m_tokens"},
		"foreign family":    {"cache_write_30m_tokens", "cache_write_12h_tokens"},
	} {
		t.Run(name, func(t *testing.T) {
			now := quotaHostModelFixedNow
			cfg := quotaHostModelConfig(now, true)
			cfg.Bindings = cfg.Bindings[:1]
			binding := &cfg.Bindings[0]
			binding.OperationID, binding.Profile, binding.Provider = "anthropic.messages", quotaProfileAnthropic, "anthropic"
			delete(binding.Meters, "thought_tokens")
			for _, meter := range meters {
				cfg.Tenants[0].Policy.Meters[meter] = quotaplex.Meter{ID: meter, Unit: "tokens"}
				binding.Meters[meter] = meter
			}
			if _, err := NewQuotaHost(context.Background(), cfg, quotaplex.NewMemoryStore(), func() time.Time { return now }, nil, &quotaHostTestBounds{input: 100, expiry: now.Add(time.Hour)}); err == nil {
				t.Fatal("ambiguous cache-write mapping accepted")
			}
		})
	}
}

func TestStrictProfilesAcceptCacheControlsOnlyWhenWritesAreMetered(t *testing.T) {
	metered := QuotaBinding{Profile: quotaProfileAnthropic, OperationID: "anthropic.messages", Model: "model-v1", Meters: map[string]string{"cache_write_tokens": "w"}}
	plain := QuotaBinding{Profile: quotaProfileAnthropic, OperationID: "anthropic.messages", Model: "model-v1"}
	for _, body := range []string{cacheAnthropicBody, `{"model":"model-v1","cache_control":{"type":"ephemeral","ttl":"1h"},"messages":[{"role":"user","content":"hello"}],"max_tokens":3}`} {
		if got, err := quotaProviderTextOutput(metered, []byte(body)); err != nil || got != 3 {
			t.Fatal("metered cache control refused", body, err)
		}
		if _, err := quotaProviderTextOutput(plain, []byte(body)); err == nil {
			t.Fatal("cache control accepted without a cache-write meter", body)
		}
	}
	for _, body := range []string{
		`{"model":"model-v1","cache_control":{"type":"persistent"},"messages":[{"role":"user","content":"hello"}],"max_tokens":3}`,
		`{"model":"model-v1","messages":[{"role":"user","content":[{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"2h"}}]}],"max_tokens":3}`,
		`{"model":"model-v1","messages":[{"role":"user","content":[{"type":"image","source":{}}]}],"max_tokens":3}`,
	} {
		if _, err := quotaProviderTextOutput(metered, []byte(body)); err == nil {
			t.Fatal("unbounded cache feature accepted", body)
		}
	}
	converse := QuotaBinding{Profile: quotaProfileBedrockConverse, OperationID: "bedrock.converse", Model: "model-v1"}
	if _, err := quotaProviderTextOutput(converse, []byte(`{"messages":[{"role":"user","content":[{"text":"x"},{"cachePoint":{"type":"default"}}]}],"inferenceConfig":{"maxTokens":3}}`)); err == nil {
		t.Fatal("Converse cache point accepted without a cache-write meter")
	}
	if got, err := quotaProviderTextOutput(converse, []byte(`{"messages":[{"role":"user","content":[{"text":"x"}]}],"inferenceConfig":{"maxTokens":3}}`)); err != nil || got != 3 {
		t.Fatal("plain Converse text refused", err)
	}
	for _, body := range []string{`{"messages":[{"role":"user","content":[{"text":"x"}]}]}`, `{"messages":[{"role":"user","content":[{"text":"x"}]}],"inferenceConfig":{"maxTokens":3},"toolConfig":{}}`} {
		if _, err := quotaProviderTextOutput(converse, []byte(body)); err == nil {
			t.Fatal("unbounded Converse request accepted", body)
		}
	}
}

var _ = fmt.Sprint

var quotaHostModelFixedNow = time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)

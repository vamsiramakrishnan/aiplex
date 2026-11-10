package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// A provider rejection that its documentation defines as "not processed"
// (rate limit, overload, authentication/permission, request validation)
// reaches the client unchanged and releases the hold; an ambiguous failure
// (5xx after the body was accepted) keeps exposure retained.
func TestStrictProfileProviderRejectionReleasesHold(t *testing.T) {
	anthropic := modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}
	openai := modelapi.Provider{ID: modelapi.ProviderOpenAI}
	bedrock := modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}
	textMeters := map[string]int64{"uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 3}
	anthropicBody := `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_tokens":3}`
	tests := []struct {
		name, provider, profile, operation, path, body, protocol string
		settings                                                 modelapi.Provider
		google                                                   bool
		status                                                   int
		errorBody                                                string
		released                                                 bool
	}{
		{"anthropic-429", modelapi.ProviderAnthropic, quotaProfileAnthropic, "anthropic.messages", "/v1/messages", anthropicBody, "anthropic-messages", anthropic, false, 429, `{"type":"error","error":{"type":"rate_limit_error","message":"slow"},"request_id":"req_1"}`, true},
		{"anthropic-529", modelapi.ProviderAnthropic, quotaProfileAnthropic, "anthropic.messages", "/v1/messages", anthropicBody, "anthropic-messages", anthropic, false, 529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, true},
		{"anthropic-400", modelapi.ProviderAnthropic, quotaProfileAnthropic, "anthropic.messages", "/v1/messages", anthropicBody, "anthropic-messages", anthropic, false, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`, true},
		{"anthropic-500-retained", modelapi.ProviderAnthropic, quotaProfileAnthropic, "anthropic.messages", "/v1/messages", anthropicBody, "anthropic-messages", anthropic, false, 500, `{"type":"error","error":{"type":"api_error","message":"internal"}}`, false},
		{"openai-429", modelapi.ProviderOpenAI, quotaProfileOpenAI, "openai.v1.responses", "/v1/responses", `{"model":"model-v1","input":"hello","max_output_tokens":3,"store":false}`, "openai-responses", openai, false, 429, `{"error":{"message":"Rate limit reached","type":"rate_limit_error","param":null,"code":"rate_limit_exceeded"}}`, true},
		{"openai-503-retained", modelapi.ProviderOpenAI, quotaProfileOpenAI, "openai.v1.responses", "/v1/responses", `{"model":"model-v1","input":"hello","max_output_tokens":3,"store":false}`, "openai-responses", openai, false, 503, `{"error":{"message":"overloaded","type":"server_error"}}`, false},
		{"bedrock-throttling", modelapi.ProviderBedrock, quotaProfileBedrockConverse, "bedrock.converse", "/model/model-v1/converse", `{"messages":[{"role":"user","content":[{"text":"x"}]}],"inferenceConfig":{"maxTokens":3}}`, "bedrock-converse", bedrock, false, 429, `{"message":"Too many requests, please wait before trying again."}`, true},
		{"google-429", "google", quotaProfileGemini, "models.generateContent", "/v1/projects/project/locations/us-central1/publishers/google/models/model-v1:generateContent", quotaHostModelBody, "", modelapi.Provider{}, true, 429, `{"error":{"code":429,"message":"Resource exhausted, please try again later.","status":"RESOURCE_EXHAUSTED"}}`, true},
		{"google-503-retained", "google", quotaProfileGemini, "models.generateContent", "/v1/projects/project/locations/us-central1/publishers/google/models/model-v1:generateContent", quotaHostModelBody, "", modelapi.Provider{}, true, 503, `{"error":{"code":503,"message":"unavailable","status":"UNAVAILABLE"}}`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			meters := textMeters
			if test.profile == quotaProfileGemini {
				meters = map[string]int64{"uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 3, "thought_tokens": 3}
			}
			auth := bearerRef
			if test.provider == modelapi.ProviderAnthropic {
				auth = anthropicRef
			}
			f := tokenProfileFixture(t, tokenProfileCase{name: test.name, profile: test.profile, operation: test.operation, provider: test.provider, google: test.google, settings: test.settings, protocol: test.protocol, auth: auth, meters: meters, upstream: func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.errorBody)
			}})
			send := func(key string) *httptest.ResponseRecorder {
				r := f.request("generateContent", key, test.body, false)
				r.URL.Path, r.URL.RawQuery = "/api/v1/llm/native/surface"+test.path, ""
				w := httptest.NewRecorder()
				f.handler.NativeModel(w, r)
				return w
			}
			w := send("rejected-attempt")
			info := quotaHostModelAttempt(t, f, w.Header().Get("X-Aiplex-Attempt-ID"))
			engine := f.host.Engines()["tenant"]
			if !test.released {
				if info.Decision.State != "unknown" {
					t.Fatal("ambiguous failure released exposure", w.Code, info.Decision.State)
				}
				return
			}
			if w.Code != test.status || w.Body.String() != test.errorBody || w.Header().Get("Retry-After") != "7" {
				t.Fatalf("provider rejection not delivered unchanged: %d %q retry-after=%q", w.Code, w.Body.String(), w.Header().Get("Retry-After"))
			}
			if info.Decision.State != "settled" || info.Actual["requests"] != 1 || info.Actual["usd"] != 0 {
				t.Fatal("rejection did not release the hold", info.Decision.State, info.Actual)
			}
			for meter, value := range info.Actual {
				if meter != "requests" && value != 0 {
					t.Fatal("rejection charged tokens", meter, value)
				}
			}
			for _, budget := range info.Plan.Budgets {
				balance, err := engine.BudgetBalance(context.Background(), "tenant", budget.Key)
				if err != nil || balance.Held != 0 {
					t.Fatal("rejection retained a hold", budget.MeterID, balance, err)
				}
			}
			// The concurrency slot (limit 1) is free: a fresh attempt is admitted.
			if next := send("after-rejection"); next.Code != test.status || f.calls.Load() != 2 {
				t.Fatal("concurrency slot retained after rejection", next.Code, strings.TrimSpace(next.Body.String()))
			}
		})
	}
}

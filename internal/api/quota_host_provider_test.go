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

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

func TestManagedProviderTextProfilesSettleNativeDisjointUsage(t *testing.T) {
	type providerTextCase struct {
		name, profile, operation, provider, path, body, response string
		location                                                 string
		wantMoney                                                int64
	}
	tests := []providerTextCase{
		{"openai", quotaProfileOpenAI, "response.create", "openai", "/v1/projects/project/locations/us-central1/endpoints/openapi/responses", `{"model":"model-v1","input":"hello","max_output_tokens":3,"store":false}`, `{"id":"resp-1","model":"model-v1","status":"completed","usage":{"input_tokens":7,"input_tokens_details":{"cached_tokens":2},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":10}}`, "us-central1", 21},
		{"anthropic", quotaProfileAnthropic, "anthropic.messages", "anthropic", "/v1/messages", `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_tokens":3}`, `{"id":"msg-1","model":"model-v1","type":"message","stop_reason":"end_turn","usage":{"input_tokens":5,"cache_read_input_tokens":2,"cache_creation_input_tokens":0,"output_tokens":3}}`, "us-central1", 21},
		{"interaction", quotaProfileInteraction, "interaction.create", "google", "/v1/projects/project/locations/global/interactions", `{"model":"model-v1","input":"hello","generation_config":{"max_output_tokens":3},"store":false}`, `{"id":"interaction-1","model":"model-v1","status":"completed","usage":{"total_input_tokens":7,"total_cached_tokens":2,"total_output_tokens":2,"total_thought_tokens":1,"total_tokens":10}}`, "global", 21},
	}
	tests = append(tests,
		providerTextCase{"openai-chat-sse", quotaProfileOpenAI, "openai.chat-completions", "openai", "/v1/projects/project/locations/us-central1/endpoints/openapi/chat/completions", `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":3,"stream":true,"stream_options":{"include_usage":true}}`, "data: {\"model\":\"model-v1\",\"choices\":[{\"finish_reason\":\"stop\"}]}\n\ndata: {\"model\":\"model-v1\",\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"prompt_tokens_details\":{\"cached_tokens\":2},\"completion_tokens\":3,\"completion_tokens_details\":{\"reasoning_tokens\":1},\"total_tokens\":10}}\n\ndata: [DONE]\n\n", "us-central1", 21},
		providerTextCase{"anthropic-sse", quotaProfileAnthropic, "anthropic.messages", "anthropic", "/v1/messages", `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_tokens":3,"stream":true}`, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\",\"model\":\"model-v1\",\"usage\":{\"input_tokens\":5,\"cache_read_input_tokens\":2,\"cache_creation_input_tokens\":0,\"output_tokens\":1}}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\ndata: {\"type\":\"message_stop\"}\n\n", "us-central1", 21},
	)
	tests = append(tests,
		providerTextCase{"openai-facade", quotaProfileOpenAI, "response.create", "openai", "/v1/responses", `{"model":"model-v1","input":"hello","max_output_tokens":3,"store":false}`, `{"id":"resp-1","model":"model-v1","status":"completed","usage":{"input_tokens":7,"input_tokens_details":{"cached_tokens":2},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":10}}`, "us-central1", 21},
		providerTextCase{"anthropic-facade", quotaProfileAnthropic, "models.rawPredict", "anthropic", "/v1/messages", `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_tokens":3}`, `{"id":"msg-1","model":"model-v1","type":"message","stop_reason":"end_turn","usage":{"input_tokens":5,"cache_read_input_tokens":2,"cache_creation_input_tokens":0,"output_tokens":3}}`, "us-central1", 21},
	)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := quotaHostModelNew(t, true, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(test.name, "sse") {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				fmt.Fprint(w, test.response)
			}, func(cfg *QuotaHostConfig) {
				cfg.Bindings = cfg.Bindings[:1]
				binding := &cfg.Bindings[0]
				binding.OperationID = test.operation
				binding.Profile = test.profile
				binding.Provider = test.provider
				binding.Region = test.location
				if test.profile == quotaProfileOpenAI || test.profile == quotaProfileAnthropic {
					delete(binding.Meters, "thought_tokens")
					delete(cfg.Tenants[0].Policy.Meters, "thought_tokens")
					prices := cfg.Tenants[0].Policy.PriceBooks[0].Prices[:0]
					for _, price := range cfg.Tenants[0].Policy.PriceBooks[0].Prices {
						if price.MeterID != "thought_tokens" {
							prices = append(prices, price)
						}
					}
					cfg.Tenants[0].Policy.PriceBooks[0].Prices = prices
				}
				cfg.Tenants[0].Policy.PriceBooks[0].Match.Provider = test.provider
				cfg.Tenants[0].Policy.PriceBooks[0].Match.Region = test.location
			})
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
			connection.Surface.Publisher = test.provider
			connection.Surface.Location = test.location
			connection.Surface.Endpoint = "openapi"
			if test.operation == "response.create" {
				connection.Surface.Protocol = "openai-responses"
			}
			if test.operation == "openai.chat-completions" {
				connection.Surface.Protocol = "openai-chat"
			}
			if test.name == "anthropic-facade" {
				connection.Surface.Protocol = "anthropic-messages"
			}
			record.Spec, _ = json.Marshal(connection)
			if _, err = f.resources.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "model-surface", ID: "surface", Spec: record.Spec}, record.Revision); err != nil {
				t.Fatal(err)
			}
			r := f.request("generateContent", "provider-attempt", test.body, false)
			r.URL.Path = "/api/v1/llm/native/surface" + test.path
			r.URL.RawQuery = ""
			serve := f.handler.NativeModel
			if strings.Contains(test.name, "facade") {
				r.URL.Path = "/api/v1/llm/gateway/surface" + test.path
				serve = f.handler.ClientGateway
			}
			w := httptest.NewRecorder()
			serve(w, r)
			if w.Code != 200 || w.Body.String() != test.response || f.calls.Load() != 1 {
				t.Fatalf("native managed call failed %d %s calls=%d", w.Code, w.Body.String(), f.calls.Load())
			}
			info := quotaHostModelAttempt(t, f, w.Header().Get("X-Aiplex-Attempt-ID"))
			if info.Decision.State != "settled" || info.Actual["usd"] != test.wantMoney || info.Actual["uncached_input_tokens"] != 5 || info.Actual["cached_tokens"] != 2 {
				t.Fatal("native usage/pricing changed", info)
			}
			retry := httptest.NewRecorder()
			retryRequest := r.Clone(r.Context())
			retryRequest.Body = io.NopCloser(strings.NewReader(test.body))
			serve(retry, retryRequest)
			if retry.Code != 409 || f.calls.Load() != 1 {
				t.Fatal("same attempt redispatched", retry.Code, f.calls.Load())
			}
		})
	}
}
func TestStrictProviderProfilesRejectUnboundedFeaturesBeforeExposure(t *testing.T) {
	tests := []struct{ profile, op, body string }{
		{quotaProfileOpenAI, "response.create", `{"model":"model-v1","input":"hello","max_output_tokens":3,"store":false,"tools":[{"type":"web_search"}]}`},
		{quotaProfileOpenAI, "response.create", `{"model":"model-v1","input":"hello","max_output_tokens":3,"store":false,"background":true}`},
		{quotaProfileOpenAI, "openai.chat-completions", `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":3,"stream":true}`},
		{quotaProfileAnthropic, "anthropic.messages", `{"model":"model-v1","messages":[{"role":"user","content":[{"type":"image","source":{}}]}],"max_tokens":3}`},
		{quotaProfileAnthropic, "anthropic.messages", `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_tokens":3,"cache_control":{"type":"ephemeral"}}`},
		{quotaProfileInteraction, "interaction.create", `{"model":"model-v1","input":"hello","generation_config":{"max_output_tokens":3},"store":false,"previous_interaction_id":"secret"}`},
		{quotaProfileInteraction, "interaction.create", `{"model":"model-v1","input":"hello","generation_config":{"max_output_tokens":3},"store":false,"agent":"opaque"}`},
	}
	for _, test := range tests {
		if _, err := quotaProviderTextOutput(QuotaBinding{Profile: test.profile, OperationID: test.op, Model: "model-v1"}, []byte(test.body)); err == nil {
			t.Fatal("unsupported exposure accepted", test.profile, test.body)
		}
	}
	for _, test := range []struct{ profile, op, body string }{
		{quotaProfileOpenAI, "openai.chat-completions", `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":3,"stream":true,"stream_options":{"include_usage":true}}`},
		{quotaProfileAnthropic, "anthropic.messages", `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_tokens":3,"thinking":{"type":"enabled","budget_tokens":2}}`},
	} {
		if got, err := quotaProviderTextOutput(QuotaBinding{Profile: test.profile, OperationID: test.op, Model: "model-v1"}, []byte(test.body)); err != nil || got != 3 {
			t.Fatal(got, err)
		}
	}
}

func TestCountOnlySpecializedOperationStillRequiresBinding(t *testing.T) {
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"totalTokens":7}`)
	}, func(cfg *QuotaHostConfig) {
		cfg.Bindings = cfg.Bindings[:1]
		cfg.Bindings[0].OperationID = "models.countTokens"
	})
	w := httptest.NewRecorder()
	f.handler.NativeModel(w, f.request("countTokens", "count-tokens", `{"contents":[]}`, false))
	if w.Code != 200 || f.calls.Load() != 1 || !strings.Contains(w.Body.String(), "totalTokens") {
		t.Fatal(w.Code, w.Body.String(), f.calls.Load())
	}
	info := quotaHostModelAttempt(t, f, w.Header().Get("X-Aiplex-Attempt-ID"))
	if info.Decision.State != "settled" || info.Actual["requests"] != 1 {
		t.Fatal(info)
	}
}

func TestManagedStrictProfilesRejectNativeProtocolOverrides(t *testing.T) {
	for _, key := range []string{"Anthropic-Beta", "Last-Event-ID"} {
		t.Run(key, func(t *testing.T) {
			f := quotaHostModelNew(t, true, func(http.ResponseWriter, *http.Request) { t.Error("unsupported protocol reached provider") }, nil)
			r := f.request("generateContent", "protocol-override", quotaHostModelBody, false)
			r.Header.Set(key, "unreviewed-feature")
			w := httptest.NewRecorder()
			f.handler.NativeModel(w, r)
			if w.Code != 400 || f.calls.Load() != 0 || len(quotaHostTestAttempts(t, f.store)) != 0 {
				t.Fatal("protocol override reached dispatch", w.Code, w.Body.String(), f.calls.Load())
			}
		})
	}
}
func TestManagedProtocolHeadersArePinnedToAttempt(t *testing.T) {
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, quotaHostModelTerminal)
	}, nil)
	for i, version := range []string{"2023-06-01", "2026-10-21"} {
		r := f.request("generateContent", "header-replay", quotaHostModelBody, false)
		r.Header.Set("Anthropic-Version", version)
		w := httptest.NewRecorder()
		f.handler.NativeModel(w, r)
		want := 200
		if i > 0 {
			want = 409
		}
		if w.Code != want {
			t.Fatalf("version header replay got%d want%d: %s", w.Code, want, w.Body.String())
		}
	}
	if f.calls.Load() != 1 {
		t.Fatal("protocol change redispatched", f.calls.Load())
	}
}

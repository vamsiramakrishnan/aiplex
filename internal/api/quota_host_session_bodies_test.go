package api

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

func TestSessionExchangeBodiesAreBounded(t *testing.T) {
	responses := QuotaBinding{Profile: quotaProfileOpenAI, OperationID: "openai.v1.responses-websocket", Model: "model-v1"}
	if got, err := quotaProviderTextOutput(responses, []byte(`{"type":"response.create","model":"model-v1","input":"hi","max_output_tokens":5,"store":false}`)); err != nil || got != 5 {
		t.Fatal("bounded response.create refused", got, err)
	}
	for _, body := range []string{
		`{"type":"response.create","model":"model-v1","input":"hi","store":false}`,
		`{"type":"response.create","model":"model-v1","input":"hi","max_output_tokens":5,"store":false,"stream_id":"lane"}`,
		`{"type":"session.update","model":"model-v1","input":"hi","max_output_tokens":5,"store":false}`,
	} {
		if _, err := quotaProviderTextOutput(responses, []byte(body)); err == nil {
			t.Fatal("unbounded WebSocket response accepted", body)
		}
	}
	realtime := QuotaBinding{Profile: quotaProfileRealtime, OperationID: "openai.v1.realtime", Model: "model-v1"}
	if got, err := quotaProviderTextOutput(realtime, []byte(`{"type":"response.create","event_id":"e1","response":{"max_output_tokens":7,"output_modalities":["audio"]}}`)); err != nil || got != 7 {
		t.Fatal("bounded Realtime response refused", got, err)
	}
	for _, body := range []string{
		`{"type":"response.create","response":{"max_output_tokens":"inf"}}`,
		`{"type":"response.create","response":{"max_output_tokens":7,"tools":[{"type":"mcp"}]}}`,
		`{"type":"response.create","response":{"max_output_tokens":7,"conversation":"none","input":[]}}`,
	} {
		if _, err := quotaProviderTextOutput(realtime, []byte(body)); err == nil {
			t.Fatal("unbounded Realtime response accepted", body)
		}
	}
}

func TestCompatibleServerTokenProfileRequiresDeclaredSemantics(t *testing.T) {
	chat := `{"id":"c1","model":"model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":2}}}`
	for _, declared := range []bool{false, true} {
		t.Run(fmt.Sprint("declared=", declared), func(t *testing.T) {
			settings := modelapi.Provider{ID: modelapi.ProviderOpenAICompatible}
			if declared {
				settings.UsageSemantics = modelapi.UsageSemanticsOpenAI
			}
			test := tokenProfileCase{name: "compat", profile: quotaProfileOpenAI, operation: "compat.chat-completions", provider: modelapi.ProviderOpenAICompatible, path: "/v1/chat/completions", body: `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":3}`, response: chat, settings: settings, protocol: modelapi.ProtocolOpenAIChat, auth: bearerRef, meters: map[string]int64{"uncached_input_tokens": 2, "cached_tokens": 1, "output_tokens": 3}, want: map[string]int64{"uncached_input_tokens": 5, "cached_tokens": 2, "output_tokens": 3, "usd": 10 + 2 + 9}}
			if !declared {
				f := tokenProfileFixture(t, test)
				r := f.request("generateContent", "compat-undeclared", test.body, false)
				r.URL.Path, r.URL.RawQuery = "/api/v1/llm/native/surface"+test.path, ""
				w := httptest.NewRecorder()
				f.handler.NativeModel(w, r)
				if w.Code != 503 || !strings.Contains(w.Body.String(), "QUOTA_PROFILE_UNSUPPORTED") || f.calls.Load() != 0 {
					t.Fatal("undeclared compatible server admitted under a token profile", w.Code, w.Body.String())
				}
				return
			}
			runTokenProfileCase(t, test)
		})
	}
}

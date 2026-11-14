package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// The provider fixtures that report per-TTL cache writes reach the ledger
// record through the real gateway observers, and a price with per-TTL rates
// costs them: OpenAI Responses (30m/12h) and Anthropic Messages (5m/1h).
func TestLedgerRecordsCacheWriteTTLsFromProviderFixtures(t *testing.T) {
	anthropic := modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}
	for _, test := range []struct {
		name, fixture, protocol, operation, path, body, price string
		provider                                              modelapi.Provider
		check                                                 func(*usageledger.Usage) bool
		amount                                                string
	}{
		{
			name: "openai-responses", fixture: "openai/responses.json", protocol: "openai-responses", operation: "openai.v1.responses", path: "/v1/responses",
			body: `{"model":"model-v1","input":"hello","max_output_tokens":800,"store":false}`, provider: modelapi.Provider{ID: modelapi.ProviderOpenAI},
			check: func(u *usageledger.Usage) bool {
				return *u.CacheWriteTokens == 512 && *u.CacheWrite30mTokens == 512 && *u.CacheWrite12hTokens == 0 && u.CacheWrite5mTokens == nil && u.CacheWrite1hTokens == nil
			},
			// input 2048 total: 512 fresh*1 + 1024 read*0 + 512 30m-writes*3; 731 output*2; the zero 12h needs its rate only when positive.
			price:  `{"id":"p","model_id":"model-v1","currency":"USD","scale":6,"meters":{"input_tokens":{"numerator":1,"denominator":1},"cache_read_tokens":{"numerator":0,"denominator":1},"cache_write_tokens":{"numerator":9,"denominator":1},"cache_write_30m_tokens":{"numerator":3,"denominator":1},"output_tokens":{"numerator":2,"denominator":1},"reasoning_tokens":{"numerator":0,"denominator":1}}}`,
			amount: "3510",
		},
		{
			name: "anthropic-messages", fixture: "anthropic/messages.json", protocol: "anthropic-messages", operation: "anthropic.messages", path: "/v1/messages",
			body: `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_tokens":800}`, provider: anthropic,
			check: func(u *usageledger.Usage) bool {
				return *u.CacheWriteTokens == 2048 && *u.CacheWrite5mTokens == 1536 && *u.CacheWrite1hTokens == 512 && u.CacheWrite30mTokens == nil && u.CacheWrite12hTokens == nil
			},
			// 1203 fresh*1 + 4096 read*0 + 1536 base writes*2 + 512 one-hour*5 + 777 output*1 + 2 searches*10 + 1 fetch*0.
			price:  `{"id":"p","model_id":"model-v1","currency":"USD","scale":6,"meters":{"input_tokens":{"numerator":1,"denominator":1},"cache_read_tokens":{"numerator":0,"denominator":1},"cache_write_tokens":{"numerator":2,"denominator":1},"cache_write_1h_tokens":{"numerator":5,"denominator":1},"output_tokens":{"numerator":1,"denominator":1},"reasoning_tokens":{"numerator":0,"denominator":1},"server_tool.web_search":{"numerator":10,"denominator":1},"server_tool.web_fetch":{"numerator":0,"denominator":1}}}`,
			amount: "7632",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := os.ReadFile("../../pkg/modelapi/testdata/usage-formats/" + test.fixture)
			if err != nil {
				t.Fatal(err)
			}
			var native map[string]any
			if err := json.Unmarshal(raw, &native); err != nil {
				t.Fatal(err)
			}
			native["model"] = "model-v1"
			response, _ := json.Marshal(native)
			f := quotaHostModelNew(t, false, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(response)
			}, nil)
			f.handler.Quota = nil
			installProviderCredentials(t, f.handler)
			record, _ := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
			var google ModelConnection
			_ = json.Unmarshal(record.Spec, &google)
			auth := bearerRef
			if test.provider.ID == modelapi.ProviderAnthropic {
				auth = anthropicRef
			}
			connection := providerConnection(t, google.Surface.Origin, test.provider, test.protocol, auth, test.operation)
			putProviderSurface(t, f.resources, connection)
			putModelPrice(t, f.resources, test.price)
			records := attachLedger(t, f.handler)
			r := f.request("generateContent", "", test.body, false)
			r.URL.Path, r.URL.RawQuery = "/api/v1/llm/native/surface"+test.path, ""
			r.Header.Set("Anthropic-Version", "2023-06-01")
			w := httptest.NewRecorder()
			f.handler.NativeModel(w, r)
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), response) {
				t.Fatalf("native response changed: %d %s", w.Code, w.Body)
			}
			rec := onlyRecord(t, records())
			if rec.Usage == nil || !test.check(rec.Usage) {
				t.Fatalf("per-TTL cache writes not recorded: %+v", rec.Usage)
			}
			if rec.Cost.Basis != usageledger.BasisEstimated || rec.Cost.Amount != test.amount {
				t.Fatalf("cost %+v usage=%+v tools=%v", rec.Cost, rec.Usage, rec.ServerTools)
			}
		})
	}
}

package modelapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

func TestOutputInclusiveUsageDoesNotDoubleCountReasoningOrCache(t *testing.T) {
	cases := []struct {
		name            string
		anthropic       bool
		usage           map[string]int64
		uncached, total int64
	}{
		{"openai", false, map[string]int64{"input_tokens": 7, "cached_tokens": 2, "output_tokens": 4, "thought_tokens": 3, "total_tokens": 11}, 5, 7},
		{"anthropic", true, map[string]int64{"input_tokens": 5, "cached_tokens": 2, "output_tokens": 4}, 5, 7},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := &UsageObservation{Counters: test.usage}
			if err := normalizeOutputInclusive(value, test.anthropic); err != nil {
				t.Fatal(err)
			}
			if value.Counters["uncached_input_tokens"] != test.uncached || value.Counters["input_tokens"] != test.total || value.Counters["output_tokens"] != 4 {
				t.Fatal(value.Counters)
			}
		})
	}
	value := &UsageObservation{Counters: map[string]int64{"input_tokens": 5, "cached_tokens": 0, "output_tokens": 2, "server_tool_calls": 1}}
	if err := normalizeOutputInclusive(value, false); err == nil {
		t.Fatal("server-tool quantities accepted by a token profile")
	}
}

func TestOutputInclusiveCacheWritesAreDisjointMeters(t *testing.T) {
	// OpenAI: input includes reads and writes; uncached = input - read - write.
	value := &UsageObservation{Counters: map[string]int64{"input_tokens": 12000, "cached_tokens": 4000, "cache_write_tokens": 7000, "cache_write_30m_tokens": 7000, "cache_write_12h_tokens": 0, "output_tokens": 10, "total_tokens": 12010}}
	if err := normalizeOutputInclusive(value, false); err != nil {
		t.Fatal(err)
	}
	if c := value.Counters; c["uncached_input_tokens"] != 1000 || c["cached_tokens"] != 4000 || c["cache_write_tokens"] != 7000 || c["input_tokens"] != 12000 {
		t.Fatal("OpenAI cache composition double counted", c)
	}
	// Anthropic: input excludes reads and writes; total input is their sum.
	value = &UsageObservation{Counters: map[string]int64{"input_tokens": 50, "cached_tokens": 100, "cache_write_tokens": 30, "cache_write_5m_tokens": 10, "cache_write_1h_tokens": 20, "output_tokens": 4}}
	if err := normalizeOutputInclusive(value, true); err != nil {
		t.Fatal(err)
	}
	if c := value.Counters; c["uncached_input_tokens"] != 50 || c["input_tokens"] != 180 || c["cache_write_1h_tokens"] != 20 {
		t.Fatal("Anthropic cache composition wrong", c)
	}
	// Re-normalizing a normalized observation is idempotent.
	if err := normalizeOutputInclusive(value, true); err != nil || value.Counters["input_tokens"] != 180 {
		t.Fatal("normalization not idempotent", err, value.Counters)
	}
	// Zero writes imply zero in every TTL bucket.
	value = &UsageObservation{Counters: map[string]int64{"input_tokens": 5, "cached_tokens": 0, "output_tokens": 2}}
	if err := normalizeOutputInclusive(value, true); err != nil || value.Counters["cache_write_5m_tokens"] != 0 || value.Counters["cache_write_1h_tokens"] != 0 {
		t.Fatal("zero cache writes left TTL buckets unknown", err, value.Counters)
	}
	// Positive writes without a breakdown leave the buckets unknown.
	value = &UsageObservation{Counters: map[string]int64{"input_tokens": 5, "cached_tokens": 0, "cache_write_tokens": 3, "output_tokens": 2}}
	if err := normalizeOutputInclusive(value, true); err != nil {
		t.Fatal(err)
	}
	if _, known := value.Counters["cache_write_5m_tokens"]; known {
		t.Fatal("unreported TTL breakdown invented", value.Counters)
	}
	for name, counters := range map[string]map[string]int64{
		"breakdown conflicts with total": {"input_tokens": 5, "cached_tokens": 0, "cache_write_tokens": 3, "cache_write_5m_tokens": 1, "cache_write_1h_tokens": 1, "output_tokens": 2},
		"partial breakdown":              {"input_tokens": 5, "cached_tokens": 0, "cache_write_tokens": 3, "cache_write_5m_tokens": 3, "output_tokens": 2},
	} {
		if err := normalizeOutputInclusive(&UsageObservation{Counters: counters}, true); err == nil {
			t.Fatal(name, "accepted")
		}
	}
	// OpenAI reads + writes cannot exceed input.
	if err := normalizeOutputInclusive(&UsageObservation{Counters: map[string]int64{"input_tokens": 5, "cached_tokens": 3, "cache_write_tokens": 3, "output_tokens": 2}}, false); err == nil {
		t.Fatal("cache subsets larger than input accepted")
	}
}

func TestVertexOpenAIChatSettlesOnlyProvenComposition(t *testing.T) {
	value := &UsageObservation{Counters: map[string]int64{"input_tokens": 27, "output_tokens": 549, "thought_tokens": 504, "total_tokens": 576}}
	if err := normalizeVertexOpenAIChat(value); err != nil {
		t.Fatal(err)
	}
	if _, cached := value.Counters["uncached_input_tokens"]; cached {
		t.Fatal("unreported cache split invented", value.Counters)
	}
	for _, counters := range []map[string]int64{
		{"input_tokens": 27, "thought_tokens": 504, "total_tokens": 576},
		{"input_tokens": 27, "output_tokens": 45, "total_tokens": 576},
	} {
		if err := normalizeVertexOpenAIChat(&UsageObservation{Counters: counters}); err == nil {
			t.Fatal("unproven composition settled", counters)
		}
	}
}

func TestControlledProviderNativeTerminalUsage(t *testing.T) {
	tests := []struct {
		name, op, path, body, response string
		stream                         bool
		input, cached, output          int64
	}{
		{"openai-response", "response.create", "/v1/projects/test-project/locations/global/endpoints/openapi/responses", `{"model":"model-a","input":"hello"}`, `{"id":"resp-1","model":"model-v1","status":"completed","usage":{"input_tokens":7,"input_tokens_details":{"cached_tokens":2},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":10}}`, false, 5, 2, 3},
		{"anthropic-json", "anthropic.messages", "/v1/messages", `{"model":"model-a","messages":[{"role":"user","content":"hello"}],"max_tokens":3}`, `{"id":"msg-1","type":"message","model":"model-v1","stop_reason":"end_turn","usage":{"input_tokens":5,"cache_read_input_tokens":2,"cache_creation_input_tokens":0,"output_tokens":3}}`, false, 5, 2, 3},
		{"anthropic-sse", "anthropic.messages", "/v1/messages", `{"model":"model-a","messages":[{"role":"user","content":"hello"}],"max_tokens":3,"stream":true}`, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\",\"model\":\"model-v1\",\"usage\":{\"input_tokens\":5,\"cache_read_input_tokens\":2,\"cache_creation_input_tokens\":0,\"output_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", true, 5, 2, 3},
		{"interaction", "interaction.create", "/v1/projects/test-project/locations/global/interactions", `{"model":"model-a","input":"hello"}`, `{"id":"interaction-1","model":"model-v1","status":"completed","usage":{"total_input_tokens":7,"total_cached_tokens":2,"total_output_tokens":2,"total_thought_tokens":1,"total_tokens":10}}`, false, 5, 2, 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lease := &modelLease{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				fmt.Fprint(w, test.response)
			}))
			defer upstream.Close()
			opts := options(t, upstream.URL, newOwnership())
			opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
			g, err := NewGateway(surface(t, upstream.URL, test.op), opts)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", test.path, strings.NewReader(test.body))
			w := httptest.NewRecorder()
			g.ServeHTTP(w, r)
			if w.Code != 200 || w.Body.String() != test.response {
				t.Fatalf("native response changed: %d %s", w.Code, w.Body.String())
			}
			terminal := lease.terminalObservations()
			if len(terminal) != 1 {
				t.Fatal("missing one terminal observation", lease.observations)
			}
			value := terminal[0]
			if value.ModelVersion != "model-v1" || value.Counters["uncached_input_tokens"] != test.input || value.Counters["cached_tokens"] != test.cached || value.Counters["output_tokens"] != test.output {
				t.Fatal(value)
			}
		})
	}
}

func TestAnthropicStreamWithoutFinalUsageOrStopNeverSettles(t *testing.T) {
	for _, tail := range []string{"", "data: {\"type\":\"message_stop\"}\n\n", "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n"} {
		lease := &modelLease{}
		stream := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\",\"model\":\"model-v1\",\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\n" + tail
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, stream)
		}))
		opts := options(t, upstream.URL, nil)
		opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
		g, err := NewGateway(surface(t, upstream.URL, "anthropic.messages"), opts)
		if err != nil {
			t.Fatal(err)
		}
		w := serve(t, g, "POST", "/v1/messages", `{"model":"model-a"}`, "")
		if w.Code != 200 || len(lease.terminalObservations()) != 0 || lease.isSettled() {
			t.Fatal("incomplete stream settled", w.Code, lease.results)
		}
		upstream.Close()
	}
}

type requestCountModelLease struct{ modelLease }

func (*requestCountModelLease) MeteringProfile() string { return "request-count" }
func TestRequestCountDoesNotClaimTokenBillingCoverage(t *testing.T) {
	lease := &requestCountModelLease{}
	response := `{"id":"resp-1","status":"completed","model":"model-v1","usage":{"input_tokens":7,"input_tokens_details":{"cached_tokens":2,"cache_write_tokens":1},"output_tokens":3,"total_tokens":10}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	}))
	defer upstream.Close()
	opts := options(t, upstream.URL, newOwnership())
	opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
	g, err := NewGateway(surface(t, upstream.URL, "response.create"), opts)
	if err != nil {
		t.Fatal(err)
	}
	w := serve(t, g, "POST", "/v1/projects/test-project/locations/global/endpoints/openapi/responses", `{"model":"model-a","input":"hello"}`, "")
	if w.Code != 200 || w.Body.String() != response || len(lease.terminalObservations()) != 1 {
		t.Fatal(w.Code, w.Body.String(), lease.observations)
	}
}

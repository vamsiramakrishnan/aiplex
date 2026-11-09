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
	for _, quantity := range []string{"cache_write_tokens", "server_tool_calls"} {
		value := &UsageObservation{Counters: map[string]int64{"input_tokens": 5, "cached_tokens": 0, "output_tokens": 2, quantity: 1}}
		if err := normalizeOutputInclusive(value, false); err == nil {
			t.Fatal("unsupported native billing dimension accepted", quantity)
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

package modelapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

func TestControlledNativeCumulativeTailCannotRegress(t *testing.T) {
	for _, test := range []struct{ name, op, path, first, lower, end string }{
		{"Gemini", "models.streamGenerateContent", controlledStreamPath, controlledResponse, strings.ReplaceAll(strings.ReplaceAll(controlledResponse, `"candidatesTokenCount":3`, `"candidatesTokenCount":2`), `"totalTokenCount":12`, `"totalTokenCount":11`), ""},
		{"OpenAI Chat", "openai.chat-completions", "/v1/projects/test-project/locations/global/endpoints/openapi/chat/completions", `{"model":"model-v1","choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens":3,"total_tokens":10}}`, `{"model":"model-v1","choices":[],"usage":{"prompt_tokens":7,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens":2,"total_tokens":9}}`, "data: [DONE]\n\n"},
		{"Responses", "response.create", "/v1/projects/test-project/locations/global/endpoints/openapi/responses", `{"type":"response.completed","response":{"id":"resp-1","model":"model-v1","status":"completed","usage":{"input_tokens":7,"input_tokens_details":{"cached_tokens":2},"output_tokens":3,"total_tokens":10}}}`, `{"type":"response.completed","response":{"id":"resp-1","model":"model-v1","status":"completed","usage":{"input_tokens":7,"input_tokens_details":{"cached_tokens":2},"output_tokens":2,"total_tokens":9}}}`, ""},
		{"Interactions", "interaction.create", "/v1/projects/test-project/locations/global/interactions", `{"type":"interaction.completed","interaction":{"id":"interaction-1","model":"model-v1","status":"completed","usage":{"total_input_tokens":7,"total_cached_tokens":2,"total_output_tokens":3,"total_thought_tokens":1,"total_tokens":11}}}`, `{"type":"interaction.completed","interaction":{"id":"interaction-1","model":"model-v1","status":"completed","usage":{"total_input_tokens":7,"total_cached_tokens":2,"total_output_tokens":2,"total_thought_tokens":1,"total_tokens":10}}}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			lease := &modelLease{}
			stream := "data: " + test.first + "\n\ndata: " + test.lower + "\n\n" + test.end
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, stream)
			}))
			defer upstream.Close()
			opts := options(t, upstream.URL, newOwnership())
			opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
			g, err := NewGateway(surface(t, upstream.URL, test.op), opts)
			if err != nil {
				t.Fatal(err)
			}
			w := serve(t, g, "POST", test.path, `{"model":"model-a"}`, "")
			if len(lease.terminalObservations()) != 0 || lease.aborts != 0 || w.Body.Len() != 0 || len(lease.results) != 1 || lease.results[0].Err == nil {
				t.Fatal("contradictory native final tail settled or escaped", lease.results, w.Body.String())
			}
		})
	}
}

func TestRequestCountKeepsTokenTelemetryAdvisory(t *testing.T) {
	lease := &requestCountModelLease{}
	stream := "data: " + controlledResponse + "\n\ndata: " + strings.ReplaceAll(strings.ReplaceAll(controlledResponse, `"candidatesTokenCount":3`, `"candidatesTokenCount":2`), `"totalTokenCount":12`, `"totalTokenCount":11`) + "\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, stream)
	}))
	defer upstream.Close()
	opts := options(t, upstream.URL, nil)
	opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
	g, err := NewGateway(surface(t, upstream.URL, "models.streamGenerateContent"), opts)
	if err != nil {
		t.Fatal(err)
	}
	w := serve(t, g, "POST", controlledStreamPath, controlledBody, "")
	if w.Code != 200 || w.Body.String() != stream || len(lease.terminalObservations()) != 1 {
		t.Fatal("request-count profile implicitly acquired token enforcement", w.Code, w.Body.String(), lease.results)
	}
}

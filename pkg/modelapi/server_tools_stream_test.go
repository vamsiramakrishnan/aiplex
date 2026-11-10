package modelapi

import "testing"

// The final streaming events carry the same server-tool evidence as the JSON
// responses: Anthropic's cumulative message_delta and the Responses
// response.completed event.
func TestServerToolsInFinalStreamingEvents(t *testing.T) {
	a := callAnthropic.surface("http://127.0.0.1:1")
	delta := inspectFor(t, a, "anthropic.messages", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9,"server_tool_use":{"web_search_requests":2}}}`)
	if delta.usage == nil || delta.usage.Counters["server_tool_web_search_requests"] != 2 || delta.usage.Counters["server_tool_calls"] != 1 {
		t.Fatalf("anthropic message_delta: %+v", delta.usage)
	}
	r := callResponses.surface("http://127.0.0.1:1")
	done := inspectFor(t, r, "openai.v1.responses", `{"type":"response.completed","response":{"id":"resp_1","status":"completed","service_tier":"priority","output":[{"type":"web_search_call","id":"ws_1","status":"completed"},{"type":"code_interpreter_call","id":"ci_1"},{"type":"message","id":"m1","role":"assistant","content":[]}],"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":0},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":15}}}`)
	c := done.usage.Counters
	if c["server_tool_web_search_requests"] != 1 || c["server_tool_code_interpreter_requests"] != 1 || len(done.usage.Dimensions) != 1 || done.usage.Dimensions["service_tier"] != "priority" {
		t.Fatalf("responses event: %+v dims=%v", c, done.usage.Dimensions)
	}
	// An in-progress event has no usage yet and so reports nothing.
	progress := inspectFor(t, r, "openai.v1.responses", `{"type":"response.in_progress","response":{"id":"resp_1","status":"in_progress","output":[{"type":"web_search_call","id":"ws_1","status":"in_progress"}]}}`)
	if progress.usage != nil {
		t.Fatalf("in-progress event must not report server tools: %+v", progress.usage)
	}
}

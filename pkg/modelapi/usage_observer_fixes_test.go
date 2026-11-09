package modelapi

import (
	"context"
	"testing"
)

func inspectFor(t *testing.T, s Surface, opID, body string) jsonObservation {
	t.Helper()
	g, err := NewGateway(s, func() Options {
		o := options(t, s.Origin, newOwnership())
		if s.Provider != nil {
			o.LiveDialer, o.TokenSource, o.Credential = nil, nil, NoCredential()
		}
		return o
	}())
	if err != nil {
		t.Fatal(err)
	}
	var op Operation
	for _, candidate := range s.Operations {
		if candidate.ID == opID {
			op = candidate
		}
	}
	value, err := g.inspectJSON(context.Background(), Owner{"tenant", "alice", "native"}, op, []byte(body), false, true)
	if err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return value
}

// Anthropic documents message_delta input/cache counters as nullable: null is
// unknown (absent), never an invalid stream and never zero.
func TestAnthropicNullableDeltaCountersStayUnknown(t *testing.T) {
	s := callAnthropic.surface("http://127.0.0.1:1")
	value := inspectFor(t, s, "anthropic.messages", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":null,"cache_creation_input_tokens":null,"cache_read_input_tokens":null,"output_tokens":15,"output_tokens_details":null,"server_tool_use":null}}`)
	if value.usage == nil || value.usage.Counters["output_tokens"] != 15 || len(value.usage.Counters) != 1 {
		t.Fatalf("nullable delta counters: %+v", value.usage)
	}
	// Cache writes reported only as the per-TTL breakdown are summed.
	value = inspectFor(t, s, "anthropic.messages", `{"type":"message","id":"m","stop_reason":"end_turn","usage":{"input_tokens":5,"cache_creation":{"ephemeral_5m_input_tokens":7,"ephemeral_1h_input_tokens":11},"output_tokens":3,"output_tokens_details":{"thinking_tokens":2}}}`)
	if value.usage.Counters["cache_write_tokens"] != 18 || value.usage.Counters["thought_tokens"] != 2 {
		t.Fatalf("per-TTL cache writes / thinking: %+v", value.usage.Counters)
	}
	// The message_start output count is a placeholder, never observed.
	value = inspectFor(t, s, "anthropic.messages", `{"type":"message_start","message":{"id":"m","usage":{"input_tokens":5,"output_tokens":1}}}`)
	if _, placeholder := value.usage.Counters["output_tokens"]; placeholder || value.usage.Counters["input_tokens"] != 5 {
		t.Fatalf("placeholder output observed: %+v", value.usage.Counters)
	}
}

// The Vertex OpenAI-compatible endpoint serves Gemini (completion excludes
// reasoning) and open models (convention unconfirmed); only a native total
// that proves one composition is accepted.
func TestVertexOpenAICompatibleCompositionIsProvenByTotal(t *testing.T) {
	s := callVertexChat.surface("http://127.0.0.1:1")
	for _, test := range []struct {
		body         string
		output       int64
		outputKnown  bool
		totalKnownAs int64
	}{
		{`{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"completion_tokens_details":{"reasoning_tokens":7},"total_tokens":22}}`, 12, true, 22},
		{`{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":7},"total_tokens":19}}`, 9, true, 19},
		{`{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":7},"total_tokens":40}}`, 0, false, -1},
		{`{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":9,"completion_tokens_details":{"reasoning_tokens":7}}}`, 0, false, -1},
	} {
		value := inspectFor(t, s, "openai.chat-completions", test.body)
		output, known := value.usage.Counters["output_tokens"]
		total, totalKnown := value.usage.Counters["total_tokens"]
		if known != test.outputKnown || output != test.output || (test.totalKnownAs >= 0) != totalKnown || (totalKnown && total != test.totalKnownAs) {
			t.Fatalf("%s: %+v", test.body, value.usage.Counters)
		}
	}
}

// streamGenerateContent without alt=sse is one JSON array of cumulative
// snapshots: the last element is authoritative and a regression is refused.
func TestGeminiJSONArrayStreamUsesLastCumulativeSnapshot(t *testing.T) {
	s := callVertexStream.surface("http://127.0.0.1:1")
	first := `{"candidates":[{"index":0}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":4,"totalTokenCount":13},"modelVersion":"v1"}`
	last := `{"candidates":[{"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":6,"totalTokenCount":15},"modelVersion":"v1"}`
	value := inspectFor(t, s, "models.streamGenerateContent", "["+first+","+last+"]")
	if !value.nativeTerminal || value.usage.Counters["output_tokens"] != 6 || value.usage.Counters["input_tokens"] != 9 {
		t.Fatalf("array stream: %+v", value.usage)
	}
	g, _ := NewGateway(s, options(t, s.Origin, newOwnership()))
	if _, err := g.inspectJSON(context.Background(), Owner{}, s.Operations[0], []byte("["+last+","+first+"]"), false, true); err == nil {
		t.Fatal("regressing or post-final array accepted")
	}
}

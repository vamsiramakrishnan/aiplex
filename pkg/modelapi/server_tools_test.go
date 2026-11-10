package modelapi

import (
	"os"
	"path/filepath"
	"testing"
)

func fixtureBody(t *testing.T, parts ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"testdata", "usage-formats"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestResponsesBuiltInToolCallsAndServiceTier(t *testing.T) {
	s := callResponses.surface("http://127.0.0.1:1")
	value := inspectFor(t, s, "openai.v1.responses", fixtureBody(t, "openai", "responses-web-search.json"))
	c := value.usage.Counters
	if c["server_tool_web_search_requests"] != 2 || c["server_tool_file_search_requests"] != 1 || c["server_tool_calls"] != 1 {
		t.Fatalf("server tool counters: %+v", c)
	}
	// function_call items run in the caller and are not server tools.
	for name := range c {
		if name == "server_tool_function_call_requests" || name == "server_tool_function_requests" {
			t.Fatalf("function call counted: %s", name)
		}
	}
	if value.usage.Dimensions[DimensionServiceTier] != "flex" {
		t.Fatalf("dimensions: %+v", value.usage.Dimensions)
	}
	// No built-in tool items: nothing is recorded, not even zeros.
	plain := inspectFor(t, s, "openai.v1.responses", fixtureBody(t, "openai", "responses.json"))
	for name := range plain.usage.Counters {
		if len(name) > 11 && name[:11] == "server_tool" {
			t.Fatalf("unexpected %s", name)
		}
	}
}

func TestAnthropicServerToolKindsAreKeptByName(t *testing.T) {
	s := callAnthropic.surface("http://127.0.0.1:1")
	body := `{"type":"message","id":"m","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3,"service_tier":"priority","server_tool_use":{"web_search_requests":2,"web_fetch_requests":1,"tool_search_requests":4,"Bad Key":9}}}`
	value := inspectFor(t, s, "anthropic.messages", body)
	c := value.usage.Counters
	if c["server_tool_web_search_requests"] != 2 || c["server_tool_web_fetch_requests"] != 1 || c["server_tool_tool_search_requests"] != 4 || c["server_tool_calls"] != 1 {
		t.Fatalf("%+v", c)
	}
	if value.usage.Dimensions[DimensionServiceTier] != "priority" {
		t.Fatalf("%+v", value.usage.Dimensions)
	}
}

func TestGeminiGroundingQueriesAndTier(t *testing.T) {
	s := geminiDASurface("gemini.models.generateContent")("http://127.0.0.1:1")
	value := inspectFor(t, s, "gemini.models.generateContent", fixtureBody(t, "google", "gemini-generate-grounded.json"))
	c := value.usage.Counters
	if c["server_tool_google_search_requests"] != 3 || c["tool_use_tokens"] != 300 {
		t.Fatalf("%+v", c)
	}
	if value.usage.Dimensions[DimensionServiceTier] != "priority" {
		t.Fatalf("%+v", value.usage.Dimensions)
	}
	// An unspecified tier is unknown.
	plain := inspectFor(t, s, "gemini.models.generateContent", `{"candidates":[{"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2,"serviceTier":"unspecified"}}`)
	if plain.usage.Dimensions[DimensionServiceTier] != "" {
		t.Fatalf("%+v", plain.usage.Dimensions)
	}
}

package hooks

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Request bodies per wire format. Each keeps unusual formatting (spacing,
// escapes, number spellings, unknown fields) that a re-serializer would
// change, so the byte-for-byte checks mean something.
var requestFixtures = []struct {
	name, format, body string
	texts              []string
	uninspected        []string
}{
	{"openai chat", FormatOpenAIChat, `{ "model":"gpt-x", "temperature": 1.50e0,
  "messages":[{"role":"system","content":"be brief"},
   {"role":"user","content":[{"type":"text","text":"mail a@b.example"},{"type":"image_url","image_url":{"url":"https://x/y.png"}}]},
   {"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{\"q\":\"x\"}"}}]},
   {"role":"tool","tool_call_id":"c1","content":"tool says hi"}],
  "x_unknown": {"nested": [1, 2.0, "keep"]}}`,
		[]string{"be brief", "mail a@b.example", `{"q":"x"}`, "tool says hi"}, []string{"image_url"}},
	{"openai responses", FormatOpenAIResponses, `{"model":"gpt-x","instructions":"sys","input":[{"role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_file","file_id":"f"}]},{"type":"function_call","call_id":"1","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"1","output":"result"},{"type":"reasoning","id":"r","encrypted_content":"zz"}]}`,
		[]string{"sys", "hello", "{}", "result"}, []string{"input_file", "reasoning"}},
	{"openai responses string input", FormatOpenAIResponses, `{"model":"m","input":"just text"}`, []string{"just text"}, nil},
	{"anthropic", FormatAnthropicMessages, `{"model":"c","max_tokens":10,"system":[{"type":"text","text":"sys"}],"messages":[{"role":"user","content":"plain"},{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"s"},{"type":"tool_use","id":"t1","name":"n","input":{"q":"look up","n":3,"deep":{"a":["x"]}}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"found"},{"type":"image","source":{}}]},{"type":"document","source":{}}]}]}`,
		[]string{"sys", "plain", "look up", "x", "found"}, []string{"thinking", "image", "document"}},
	{"gemini", FormatGemini, `{"system_instruction":{"parts":[{"text":"sys"}]},"contents":[{"role":"user","parts":[{"text":"hi"},{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]},{"role":"model","parts":[{"functionCall":{"name":"f","args":{"city":"Paris"}}}]},{"role":"user","parts":[{"functionResponse":{"name":"f","response":{"temp":"20C"}}}]}],"generationConfig":{"temperature":0.10}}`,
		[]string{"sys", "hi", "Paris", "20C"}, []string{"inline_data"}},
	{"bedrock converse", FormatBedrockConverse, `{"system":[{"text":"sys"}],"messages":[{"role":"user","content":[{"text":"hi"},{"image":{"format":"png","source":{"bytes":"AA=="}}}]},{"role":"assistant","content":[{"toolUse":{"toolUseId":"1","name":"n","input":{"q":"v"}}}]},{"role":"user","content":[{"toolResult":{"toolUseId":"1","content":[{"text":"r"},{"json":{"k":"j"}}]}}]}],"inferenceConfig":{"maxTokens":5}}`,
		[]string{"sys", "hi", "v", "r", "j"}, []string{"image"}},
	{"mcp tools/call", FormatMCPToolCall, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"search","arguments":{"query":"find x","limit":5,"tags":["a"]}}}`,
		[]string{"find x", "a"}, nil},
	{"embeddings", FormatOpenAIEmbeddings, `{"model":"e","input":["one",[1,2],"two"]}`, []string{"one", "two"}, []string{"tokens"}},
}

func TestRequestExtractorsRoundTrip(t *testing.T) {
	for _, f := range requestFixtures {
		t.Run(f.name, func(t *testing.T) {
			body := []byte(f.body)
			content, err := ExtractRequest(f.format, body)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if got := content.Texts(); strings.Join(got, "|") != strings.Join(f.texts, "|") {
				t.Fatalf("texts = %q, want %q", got, f.texts)
			}
			var types []string
			for _, u := range content.Uninspected {
				types = append(types, u.Type)
			}
			if strings.Join(types, ",") != strings.Join(f.uninspected, ",") {
				t.Fatalf("uninspected = %v, want %v", types, f.uninspected)
			}
			// No transform: byte-for-byte passthrough.
			if out := content.Apply(nil); !bytes.Equal(out, body) {
				t.Fatalf("untouched body changed:\n%s", out)
			}
			if out := content.Apply(map[int]string{0: content.Parts[0].Text}); !bytes.Equal(out, body) {
				t.Fatalf("identity transform changed the body")
			}
			// Transform one part: only that string literal changes.
			last := len(content.Parts) - 1
			out := content.Apply(map[int]string{last: "[MASKED]"})
			again, err := ExtractRequest(f.format, out)
			if err != nil {
				t.Fatalf("transformed body no longer parses: %v\n%s", err, out)
			}
			want := append([]string(nil), f.texts...)
			want[last] = "[MASKED]"
			if strings.Join(again.Texts(), "|") != strings.Join(want, "|") {
				t.Fatalf("after transform texts = %q", again.Texts())
			}
			start, end := content.Parts[last].spans[0].start, content.Parts[last].spans[0].end
			if !bytes.Equal(out[:start], body[:start]) || !bytes.Equal(out[len(out)-(len(body)-end):], body[end:]) {
				t.Fatalf("bytes outside the transformed literal changed")
			}
		})
	}
}

func TestRequestExtractorsRefuseAmbiguity(t *testing.T) {
	for name, c := range map[string]struct{ format, body string }{
		"duplicate key":         {FormatOpenAIChat, `{"messages":[],"messages":[{"role":"user","content":"hidden"}]}`},
		"case variant":          {FormatOpenAIChat, `{"messages":[],"Messages":[{"role":"user","content":"hidden"}]}`},
		"nested case variant":   {FormatAnthropicMessages, `{"messages":[{"role":"user","content":"a","Content":"hidden"}]}`},
		"gemini both spellings": {FormatGemini, `{"systemInstruction":{"parts":[]},"system_instruction":{"parts":[{"text":"hidden"}]}}`},
		"not json":              {FormatOpenAIChat, `{"messages":[`},
		"trailing data":         {FormatOpenAIChat, `{"messages":[]} {}`},
		"invalid utf8":          {FormatOpenAIChat, "{\"messages\":[{\"role\":\"user\",\"content\":\"\xff\"}]}"},
		"no extractor":          {"bedrock-invoke", `{"prompt":"x"}`},
		"messages not an array": {FormatOpenAIChat, `{"messages":"x"}`},
	} {
		if _, err := ExtractRequest(c.format, []byte(c.body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func fixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "modelapi", "testdata", "usage-formats", path))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestResponseExtractorsOnProviderFixtures(t *testing.T) {
	for _, c := range []struct {
		format, path, media, first string
	}{
		{FormatOpenAIChat, "openai/chat.json", "application/json", ""},
		{FormatOpenAIResponses, "azure-openai/responses.json", "application/json", "Synthetic answer."},
		{FormatAnthropicMessages, "anthropic/messages.json", "application/json", ""},
		{FormatAnthropicMessages, "anthropic/vertex-raw.json", "application/json", ""},
		{FormatGemini, "google/gemini-generate.json", "application/json", ""},
		{FormatGemini, "google/gemini-generate.stream.json", "application/json", ""},
		{FormatBedrockConverse, "anthropic/bedrock-converse.json", "application/json", "Synthetic answer."},
		{FormatOpenAIChat, "openai/chat.sse", "text/event-stream", ""},
		{FormatOpenAIChat, "azure-openai/chat.sse", "text/event-stream", "Hi"},
		{FormatOpenAIChat, "google/vertex-openai-chat.sse", "text/event-stream", ""},
		{FormatAnthropicMessages, "anthropic/messages.sse", "text/event-stream", ""},
		{FormatAnthropicMessages, "google/vertex-claude-streamrawpredict.sse", "text/event-stream", ""},
	} {
		t.Run(c.path, func(t *testing.T) {
			body := fixture(t, c.path)
			content, err := ExtractResponse(c.format, c.media, body)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if len(content.Parts) == 0 {
				t.Fatalf("no text parts found")
			}
			if c.first != "" && !strings.HasPrefix(content.Parts[0].Text, c.first) {
				t.Fatalf("first part %q", content.Parts[0].Text)
			}
			if out := content.Apply(nil); !bytes.Equal(out, body) {
				t.Fatalf("untouched response changed")
			}
			replacement := map[int]string{0: "REPLACED <&> text"}
			out := content.Apply(replacement)
			again, err := ExtractResponse(c.format, c.media, out)
			if err != nil {
				t.Fatalf("transformed response does not parse: %v", err)
			}
			if again.Parts[0].Text != "REPLACED <&> text" || len(again.Parts) != len(content.Parts) {
				t.Fatalf("transformed text %q (%d parts, want %d)", again.Parts[0].Text, len(again.Parts), len(content.Parts))
			}
			for i := 1; i < len(content.Parts); i++ {
				if again.Parts[i].Text != content.Parts[i].Text {
					t.Fatalf("part %d changed", i)
				}
			}
			if c.media == "text/event-stream" {
				// Every event without text is re-emitted byte for byte, and
				// the stream keeps its event count.
				before, after := parseSSE(body), parseSSE(out)
				if len(before.events) != len(after.events) {
					t.Fatalf("event count %d -> %d", len(before.events), len(after.events))
				}
				for i := range before.events {
					if !bytes.Contains(before.events[i].data(), []byte(`"content"`)) && !bytes.Contains(before.events[i].data(), []byte(`"text"`)) && !bytes.Equal(before.events[i].raw, after.events[i].raw) {
						t.Fatalf("non-text event %d changed", i)
					}
				}
			}
		})
	}
}

func TestStreamsWithoutBufferSupportAreUninspectable(t *testing.T) {
	if _, err := ExtractResponse(FormatGemini, "text/event-stream", fixture(t, "google/gemini-generate.sse")); err == nil {
		t.Fatal("gemini SSE accepted for buffering")
	}
	if _, err := ExtractResponse(FormatOpenAIChat, "image/png", []byte("x")); err == nil {
		t.Fatal("binary accepted")
	}
}

func TestMCPResultExtraction(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"secret result"},{"type":"image","data":"AA","mimeType":"image/png"}],"structuredContent":{"value":"s"},"isError":false}}`)
	content, err := ExtractResponse(FormatMCPToolCall, "application/json", body)
	if err != nil || len(content.Parts) != 2 || content.Parts[0].Text != "secret result" || len(content.Uninspected) != 1 {
		t.Fatalf("%v %+v", err, content)
	}
	sse := []byte("event: message\ndata: " + string(body) + "\n\n")
	streamed, err := ExtractResponse(FormatMCPToolCall, "text/event-stream", sse)
	if err != nil || len(streamed.Parts) != 2 {
		t.Fatalf("streamed: %v %+v", err, streamed)
	}
	out := streamed.Apply(map[int]string{0: "[X]"})
	if !bytes.Contains(out, []byte(`"text":"[X]"`)) || !bytes.HasPrefix(out, []byte("event: message\n")) {
		t.Fatalf("streamed transform: %s", out)
	}
}

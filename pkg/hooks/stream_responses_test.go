package hooks

import (
	"strings"
	"testing"
)

// rev builds one Responses event: the type is added to the fields.
func rev(typ, fields string) string {
	if fields != "" {
		fields = "," + fields
	}
	return "event: " + typ + "\ndata: {\"type\":\"" + typ + "\"" + fields + "}\n\n"
}

const respUsage = `"usage":{"input_tokens":311,"input_tokens_details":{"cached_tokens":256},"output_tokens":88,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":399}`

func respCompleted(output string) string {
	return rev("response.completed", `"response":{"id":"resp_1","object":"response","status":"completed","model":"m","output":`+output+`,`+respUsage+`}`)
}

// textStream is a Responses text stream: the secret is split over three
// deltas and repeated in every event that repeats the text.
func textStream(final string) string {
	full := "Your key is " + leakKey + " ok"
	msg := `{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"` + full + `","annotations":[],"logprobs":[]}]}`
	return rev("response.created", `"response":{"id":"resp_1","object":"response","status":"in_progress","output":[],"usage":null}`) +
		rev("response.output_item.added", `"output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]}`) +
		rev("response.content_part.added", `"item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[],"logprobs":[]}`) +
		rev("response.output_text.delta", `"item_id":"msg_1","output_index":0,"content_index":0,"delta":"Your key is sk-LEAK","logprobs":[],"obfuscation":"x1"`) +
		rev("response.output_text.delta", `"item_id":"msg_1","output_index":0,"content_index":0,"delta":"ABLE12","logprobs":[]`) +
		rev("response.output_text.delta", `"item_id":"msg_1","output_index":0,"content_index":0,"delta":"345678 ok","logprobs":[]`) +
		rev("response.output_text.done", `"item_id":"msg_1","output_index":0,"content_index":0,"text":"`+full+`","logprobs":[]`) +
		rev("response.content_part.done", `"item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"`+full+`","annotations":[],"logprobs":[]}`) +
		rev("response.output_item.done", `"output_index":0,"item":`+msg) +
		final
}

func TestResponsesStreamRedactionLeavesNoCopy(t *testing.T) {
	full := "Your key is " + leakKey + " ok"
	msg := `[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"` + full + `","annotations":[],"logprobs":[]}]}]`
	body := textStream(respCompleted(msg))
	content, err := ExtractResponse(FormatOpenAIResponses, "text/event-stream", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	// One part: the deltas and every repeated copy are one text.
	if len(content.Parts) != 1 || content.Parts[0].Text != full || len(content.Parts[0].spans) != 3 || len(content.Parts[0].repeats) != 4 {
		t.Fatalf("parts %+v", content.Parts)
	}
	out := redacted(t, FormatOpenAIResponses, body)
	assertNoLeak(t, out, leakKey)
	// The same events, in the same order, with the same terminal usage.
	sameJSON(t, eventTypes(t, []byte(body)), eventTypes(t, out), "event types")
	before := eventField(t, []byte(body), "response.completed", "response")
	after := eventField(t, out, "response.completed", "response")
	sameJSON(t, before.(map[string]any)["usage"], after.(map[string]any)["usage"], "usage")
	// The client reassembles the redacted text from the deltas and finds the
	// same text in every repeated copy.
	again, err := ExtractResponse(FormatOpenAIResponses, "text/event-stream", out)
	if err != nil {
		t.Fatalf("re-emitted stream does not parse: %v", err)
	}
	if len(again.Parts) != 1 || again.Parts[0].Text != "Your key is [KEY] ok" || len(again.Parts[0].repeats) != 4 {
		t.Fatalf("re-emitted parts %+v", again.Parts)
	}
	// Events that carry no text are byte for byte what the provider sent.
	pb, pa := parseSSE([]byte(body)).events, parseSSE(out).events
	if len(pb) != len(pa) {
		t.Fatalf("event count %d -> %d", len(pb), len(pa))
	}
	for i := range pb {
		if strings.Contains(string(pb[i].raw), "response.created") || strings.Contains(string(pb[i].raw), "output_item.added") {
			if string(pb[i].raw) != string(pa[i].raw) {
				t.Fatalf("event %d changed:\n%s\n%s", i, pb[i].raw, pa[i].raw)
			}
		}
	}
}

func TestResponsesStreamAllowedIsByteForByte(t *testing.T) {
	body := strings.ReplaceAll(textStream(respCompleted(`[]`)), leakKey, "harmless123456789")
	body = strings.Replace(body, "sk-LEAK", "harmles", 1)
	body = strings.Replace(body, "ABLE12", "s12345", 1)
	out, got, err := streamOutcome(t, FormatOpenAIResponses, body, keyMasker())
	if err != nil || out.Action != Allow || string(got) != body {
		t.Fatalf("unchanged stream: %v %+v", err, out)
	}
}

func TestResponsesStreamCopyThatDiffersIsInspectedOnItsOwn(t *testing.T) {
	// The completed response carries text the deltas never did. It is a part
	// of its own, so the hooks see it and redact it.
	other := `[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"A different answer with ` + leakKey + `","annotations":[]}]}]`
	body := textStream(respCompleted(other))
	content, err := ExtractResponse(FormatOpenAIResponses, "text/event-stream", []byte(body))
	if err != nil || len(content.Parts) != 2 {
		t.Fatalf("%v %+v", err, content)
	}
	out := redacted(t, FormatOpenAIResponses, body)
	assertNoLeak(t, out, leakKey)
}

func TestResponsesStreamFunctionCallArguments(t *testing.T) {
	args := `{\"token\":\"` + leakKey + `\"}`
	item := `{"id":"fc_1","type":"function_call","call_id":"call_1","name":"send","arguments":"` + args + `","status":"completed"}`
	body := rev("response.created", `"response":{"id":"r","status":"in_progress","output":[]}`) +
		rev("response.output_item.added", `"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"send","arguments":"","status":"in_progress"}`) +
		rev("response.function_call_arguments.delta", `"item_id":"fc_1","output_index":0,"delta":"{\"token\":\"sk-LEAK"`) +
		rev("response.function_call_arguments.delta", `"item_id":"fc_1","output_index":0,"delta":"ABLE12345678\"}"`) +
		rev("response.function_call_arguments.done", `"item_id":"fc_1","output_index":0,"name":"send","arguments":"`+args+`"`) +
		rev("response.output_item.done", `"output_index":0,"item":`+item) +
		respCompleted(`[`+item+`]`)
	content, err := ExtractResponse(FormatOpenAIResponses, "text/event-stream", []byte(body))
	if err != nil || len(content.Parts) != 1 || content.Parts[0].Kind != KindToolArguments {
		t.Fatalf("%v %+v", err, content)
	}
	out := redacted(t, FormatOpenAIResponses, body)
	assertNoLeak(t, out, leakKey)
	// Only tool calls: no text part, nothing to refuse.
	if _, got, err := streamOutcome(t, FormatOpenAIResponses, strings.ReplaceAll(strings.ReplaceAll(body, leakKey, "x"), "sk-LEAK", "x"), keyMasker()); err != nil || len(got) == 0 {
		t.Fatalf("tool-call-only stream: %v", err)
	}
}

func TestResponsesStreamRefusalAndCustomToolCall(t *testing.T) {
	body := rev("response.output_item.added", `"output_index":0,"item":{"id":"m","type":"message","role":"assistant","content":[]}`) +
		rev("response.refusal.delta", `"item_id":"m","output_index":0,"content_index":0,"delta":"I will not say sk-LEAK"`) +
		rev("response.refusal.delta", `"item_id":"m","output_index":0,"content_index":0,"delta":"ABLE12345678."`) +
		rev("response.refusal.done", `"item_id":"m","output_index":0,"content_index":0,"refusal":"I will not say `+leakKey+`."`) +
		rev("response.content_part.done", `"item_id":"m","output_index":0,"content_index":0,"part":{"type":"refusal","refusal":"I will not say `+leakKey+`."}`) +
		rev("response.custom_tool_call_input.delta", `"item_id":"c","output_index":1,"delta":"run `+leakKey+`"`) +
		rev("response.custom_tool_call_input.done", `"item_id":"c","output_index":1,"input":"run `+leakKey+`"`) +
		respCompleted(`[{"id":"m","type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I will not say `+leakKey+`."}]},{"id":"c","type":"custom_tool_call","call_id":"x","name":"n","input":"run `+leakKey+`"}]`)
	out := redacted(t, FormatOpenAIResponses, body)
	assertNoLeak(t, out, leakKey)
}

func TestResponsesStreamSplitSurrogatePairAcrossDeltas(t *testing.T) {
	// A character written as a pair of escapes may be split between deltas.
	// Decoding each delta alone would corrupt it; the stream is assembled
	// from the escaped literals instead.
	body := rev("response.output_text.delta", `"item_id":"m","output_index":0,"content_index":0,"delta":"smile \ud83d"`) +
		rev("response.output_text.delta", `"item_id":"m","output_index":0,"content_index":0,"delta":"\ude00 key `+leakKey+`"`) +
		rev("response.output_text.done", `"item_id":"m","output_index":0,"content_index":0,"text":"smile 😀 key `+leakKey+`"`)
	content, err := ExtractResponse(FormatOpenAIResponses, "text/event-stream", []byte(body))
	if err != nil || len(content.Parts) != 1 || content.Parts[0].Text != "smile \U0001F600 key "+leakKey || len(content.Parts[0].repeats) != 1 {
		t.Fatalf("%v %+v", err, content)
	}
	out := redacted(t, FormatOpenAIResponses, body)
	assertNoLeak(t, out, leakKey)
	again, err := ExtractResponse(FormatOpenAIResponses, "text/event-stream", out)
	if err != nil || again.Parts[0].Text != "smile \U0001F600 key [KEY]" {
		t.Fatalf("%v %+v", err, again)
	}
}

func TestResponsesStreamErrorEventIsInspected(t *testing.T) {
	body := rev("response.output_text.delta", `"item_id":"m","output_index":0,"content_index":0,"delta":"fine"`) +
		rev("error", `"code":"ERR_X","message":"upstream said `+leakKey+`","param":null,"sequence_number":3`)
	out := redacted(t, FormatOpenAIResponses, body)
	assertNoLeak(t, out, leakKey)
	if !strings.Contains(string(out), "ERR_X") {
		t.Fatalf("error code lost: %s", out)
	}
	// The failed-response form of the same.
	failed := rev("response.failed", `"response":{"id":"r","status":"failed","error":{"code":"server_error","message":"saw `+leakKey+`"},"output":[],"usage":null}`)
	assertNoLeak(t, redacted(t, FormatOpenAIResponses, failed), leakKey)
}

func TestResponsesStreamFixtures(t *testing.T) {
	for _, name := range []string{"openai/responses.failed.sse", "openai/responses.incomplete.sse", "openai/responses.error.sse"} {
		body := fixture(t, name)
		content, err := ExtractResponse(FormatOpenAIResponses, "text/event-stream", body)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out := content.Apply(nil); string(out) != string(body) {
			t.Fatalf("%s: untouched stream changed", name)
		}
	}
	// A stream with no events at all, and one with only a blank line.
	for _, body := range []string{"", "\n", ": hi\n\n"} {
		content, err := ExtractResponse(FormatOpenAIResponses, "text/event-stream", []byte(body))
		if err != nil || len(content.Parts) != 0 {
			t.Fatalf("%q: %v", body, err)
		}
	}
}

func TestResponsesStreamReportsUninspectedPayloads(t *testing.T) {
	body := rev("response.output_item.added", `"output_index":0,"item":{"id":"r","type":"reasoning","summary":[]}`) +
		rev("response.reasoning_summary_text.delta", `"item_id":"r","output_index":0,"summary_index":0,"delta":"thinking"`) +
		rev("response.web_search_call.searching", `"item_id":"w","output_index":1`)
	content, err := ExtractResponse(FormatOpenAIResponses, "text/event-stream", []byte(body))
	if err != nil || len(content.Parts) != 0 || len(content.Uninspected) != 2 {
		t.Fatalf("%v %+v", err, content)
	}
}

func TestResponsesStreamAmbiguityIsRefused(t *testing.T) {
	delta := rev("response.output_text.delta", `"item_id":"m","output_index":0,"content_index":0,"delta":"hi"`)
	cases := map[string]string{
		"unknown event type":             delta + rev("response.something.new", `"delta":"`+leakKey+`"`),
		"annotation event":               delta + rev("response.output_text.annotation.added", `"item_id":"m","output_index":0,"content_index":0,"annotation_index":0,"annotation":{"type":"url_citation","title":"`+leakKey+`"}`),
		"logprobs on a delta":            rev("response.output_text.delta", `"item_id":"m","output_index":0,"content_index":0,"delta":"hi","logprobs":[{"token":"hi","logprob":-1,"top_logprobs":[]}]`),
		"logprobs on a done":             rev("response.output_text.done", `"item_id":"m","output_index":0,"content_index":0,"text":"hi","logprobs":[{"token":"hi","logprob":-1}]`),
		"annotations on a part":          rev("response.content_part.done", `"item_id":"m","output_index":0,"content_index":0,"part":{"type":"output_text","text":"hi","annotations":[{"type":"url_citation"}],"logprobs":[]}`),
		"message from another role":      rev("response.output_item.done", `"output_index":0,"item":{"type":"message","role":"`+leakKey+`","content":[]}`),
		"obfuscation is not a string":    rev("response.output_text.delta", `"item_id":"m","output_index":0,"content_index":0,"delta":"hi","obfuscation":{"v":"`+leakKey+`"}`),
		"obfuscation on a done event":    rev("response.output_text.done", `"item_id":"m","output_index":0,"content_index":0,"text":"hi","obfuscation":"`+leakKey+`"`),
		"unknown field on a delta":       rev("response.output_text.delta", `"item_id":"m","output_index":0,"content_index":0,"delta":"hi","text":"`+leakKey+`"`),
		"unknown field on a status":      rev("response.web_search_call.searching", `"item_id":"w","output_index":1,"query":"`+leakKey+`"`),
		"unknown field on a part":        rev("response.content_part.done", `"item_id":"m","output_index":0,"content_index":0,"part":{"type":"output_text","text":"hi","note":"`+leakKey+`"}`),
		"unknown field on an item":       rev("response.output_item.done", `"output_index":0,"item":{"type":"message","role":"assistant","content":[],"summary":"`+leakKey+`"}`),
		"unknown field on a call":        rev("response.output_item.done", `"output_index":0,"item":{"type":"function_call","name":"n","arguments":"{}","extra":"`+leakKey+`"}`),
		"unknown item type":              rev("response.output_item.done", `"output_index":0,"item":{"type":"message2","text":"`+leakKey+`"}`),
		"unknown part type":              rev("response.content_part.done", `"item_id":"m","output_index":0,"content_index":0,"part":{"type":"output_text2","text":"`+leakKey+`"}`),
		"response is not an object":      rev("response.completed", `"response":"`+leakKey+`"`),
		"output is not an array":         rev("response.completed", `"response":{"id":"r","output":"`+leakKey+`"}`),
		"text is not a string":           rev("response.output_text.done", `"item_id":"m","output_index":0,"content_index":0,"text":{"v":"`+leakKey+`"}`),
		"missing output index":           rev("response.output_text.delta", `"item_id":"m","content_index":0,"delta":"hi"`),
		"negative content index":         rev("response.output_text.delta", `"item_id":"m","output_index":0,"content_index":-1,"delta":"hi"`),
		"event name differs from type":   "event: response.created\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"hi\"}\n\n",
		"two event names":                "event: response.output_text.delta\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"hi\"}\n\n",
		"event name without data":        "event: response.output_text.delta\n\n",
		"id field":                       "id: " + leakKey + "\ndata: {\"type\":\"response.created\",\"response\":{\"output\":[]}}\n\n",
		"retry field":                    "retry: 5\ndata: {\"type\":\"response.created\",\"response\":{\"output\":[]}}\n\n",
		"bare field name":                "data\ndata: {\"type\":\"response.created\",\"response\":{\"output\":[]}}\n\n",
		"unterminated final event":       delta + "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"" + leakKey + "\"}",
		"unterminated with single break": delta + "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"" + leakKey + "\"}\n",
		"carriage return separators":     "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"" + leakKey + "\"}\r\rdata: {}\r\r",
		"not JSON":                       delta + "data: not json " + leakKey + "\n\n",
		"JSON array":                     delta + "data: [\"" + leakKey + "\"]\n\n",
		"duplicate key":                  "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"a\",\"delta\":\"" + leakKey + "\"}\n\n",
		"type with another case":         "data: {\"type\":\"response.output_text.delta\",\"Type\":\"x\",\"output_index\":0,\"content_index\":0,\"delta\":\"hi\"}\n\n",
		"no type":                        "data: {\"delta\":\"" + leakKey + "\"}\n\n",
		"invalid UTF-8":                  "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"\xff" + leakKey + "\"}\n\n",
		"byte order mark":                "\xef\xbb\xbf" + delta,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) { refused(t, FormatOpenAIResponses, body) })
	}
}

func TestResponsesStreamCommentsAreDroppedWhenTransformed(t *testing.T) {
	body := ": " + leakKey + "\n" + textStream(respCompleted(`[]`)) + ": trailing " + leakKey + "\n\n"
	out := redacted(t, FormatOpenAIResponses, body)
	assertNoLeak(t, out, leakKey)
	// Untouched, the stream is delivered as the provider sent it.
	quiet := strings.NewReplacer(leakKey, "harmless123456789", "sk-LEAK", "harmles", "ABLE12", "s12345").Replace(body)
	if _, got, err := streamOutcome(t, FormatOpenAIResponses, quiet, keyMasker()); err != nil || string(got) != quiet {
		t.Fatalf("untouched stream with comments changed: %v", err)
	}
}

func TestResponsesStreamCRLF(t *testing.T) {
	body := strings.ReplaceAll(textStream(respCompleted(`[]`)), "\n", "\r\n")
	out := redacted(t, FormatOpenAIResponses, body)
	assertNoLeak(t, out, leakKey)
	if strings.Contains(string(out), "\n") && !strings.Contains(string(out), "\r\n") {
		t.Fatalf("line endings not kept")
	}
}

func TestResponsesStreamPaddingIsEmptiedWhenTransformed(t *testing.T) {
	// A delta's obfuscation padding is not text, but it is a string a copy
	// of the original could ride in. It is emptied when the stream is
	// rewritten, and left alone when it is not.
	body := rev("response.output_text.delta", `"item_id":"m","output_index":0,"content_index":0,"delta":"key `+leakKey+`","logprobs":[],"obfuscation":"`+leakKey+`"`)
	out := redacted(t, FormatOpenAIResponses, body)
	assertNoLeak(t, out, leakKey)
	if !strings.Contains(string(out), `"obfuscation":""`) {
		t.Fatalf("padding kept: %s", out)
	}
	quiet := rev("response.output_text.delta", `"item_id":"m","output_index":0,"content_index":0,"delta":"key","obfuscation":"AbC123"`)
	if _, got, err := streamOutcome(t, FormatOpenAIResponses, quiet, keyMasker()); err != nil || string(got) != quiet {
		t.Fatalf("untouched stream changed: %v", err)
	}
}

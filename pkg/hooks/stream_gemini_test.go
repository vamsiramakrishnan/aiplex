package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

// gev builds one Gemini SSE event from a chunk's JSON.
func gev(chunk string) string { return "data: " + chunk + "\n\n" }

func gtext(texts ...string) string {
	parts := make([]string, len(texts))
	for i, s := range texts {
		parts[i] = `{"text":"` + s + `"}`
	}
	return `{"candidates":[{"content":{"role":"model","parts":[` + strings.Join(parts, ",") + `]},"index":0}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":3,"totalTokenCount":12},"modelVersion":"v1","responseId":"r1"}`
}

func TestGeminiStreamSecretSplitAcrossChunks(t *testing.T) {
	body := gev(gtext("Your key is sk-LEAK")) + gev(gtext("ABLE12")) + gev(gtext("345678 ok")) +
		gev(`{"candidates":[{"content":{"role":"model","parts":[{"text":" bye"}]},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":9,"totalTokenCount":18}}`)
	content, err := ExtractResponse(FormatGemini, "text/event-stream", []byte(body))
	if err != nil || len(content.Parts) != 1 || content.Parts[0].Text != "Your key is "+leakKey+" ok bye" {
		t.Fatalf("%v %+v", err, content)
	}
	out := redacted(t, FormatGemini, body)
	assertNoLeak(t, out, leakKey)
	// Usage and the finish reason are untouched.
	events := parseSSE(out).events
	if len(events) != 4 {
		t.Fatalf("events %d", len(events))
	}
	var last struct {
		Candidates []struct{ FinishReason string }
		Usage      map[string]int `json:"usageMetadata"`
	}
	if err := json.Unmarshal(events[3].data(), &last); err != nil || last.Candidates[0].FinishReason != "STOP" || last.Usage["totalTokenCount"] != 18 {
		t.Fatalf("last chunk %s", events[3].data())
	}
	again, err := ExtractResponse(FormatGemini, "text/event-stream", out)
	if err != nil || len(again.Parts) != 1 || again.Parts[0].Text != "Your key is [KEY] ok bye" {
		t.Fatalf("%v %+v", err, again)
	}
}

func TestGeminiStreamFunctionCallsAndCodeAreInspected(t *testing.T) {
	chunk := `{"candidates":[{"content":{"role":"model","parts":[` +
		`{"functionCall":{"name":"send","args":{"to":"` + leakKey + `","nested":[{"v":"` + leakKey + `"}]}}},` +
		`{"executableCode":{"language":"PYTHON","code":"print('` + leakKey + `')"}},` +
		`{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":"` + leakKey + `"}}]},"index":0}]}`
	body := gev(chunk)
	content, err := ExtractResponse(FormatGemini, "text/event-stream", []byte(body))
	if err != nil || len(content.Parts) != 4 {
		t.Fatalf("%v %+v", err, content)
	}
	assertNoLeak(t, redacted(t, FormatGemini, body), leakKey)
}

func TestGeminiStreamTextAndFunctionCallInOnePart(t *testing.T) {
	// A part that carries text and a call must not hide the call.
	body := gev(`{"candidates":[{"content":{"parts":[{"text":"hi","functionCall":{"name":"f","args":{"k":"` + leakKey + `"}}}]},"index":0}]}`)
	assertNoLeak(t, redacted(t, FormatGemini, body), leakKey)
}

func TestGeminiStreamRunsAndCandidates(t *testing.T) {
	// Answer and thought text are separate texts; a function call between text parts does not split one;
	// two candidates assemble apart.
	body := gev(`{"candidates":[`+
		`{"content":{"parts":[{"text":"thinking ","thought":true}]},"index":0},`+
		`{"content":{"parts":[{"text":"other "}]},"index":1}]}`) +
		gev(`{"candidates":[`+
			`{"content":{"parts":[{"text":"more","thought":true},{"text":"answer ","thoughtSignature":"abc"}]},"index":0},`+
			`{"content":{"parts":[{"text":"cand"}]},"index":1}]}`) +
		gev(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","args":{}}},{"text":" after"}]},"index":0}]}`)
	content, err := ExtractResponse(FormatGemini, "text/event-stream", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"thinking more", "answer  after", "other cand"}
	got := map[string]bool{}
	for _, p := range content.Parts {
		got[p.Text] = true
	}
	if len(content.Parts) != len(want) {
		t.Fatalf("parts %+v", content.Parts)
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("missing %q in %+v", w, content.Parts)
		}
	}
	// Replacing one run leaves the others and the thought flags alone.
	out := content.Apply(map[int]string{0: "[T]"})
	for _, w := range []string{`"text":"[T]","thought":true`, `"text":"","thought":true`, `"text":"answer "`, `"text":" after"`, `"text":"other "`} {
		if !strings.Contains(string(out), w) {
			t.Fatalf("missing %s in\n%s", w, out)
		}
	}
}

func TestGeminiStreamMissingCandidateIndexUsesPosition(t *testing.T) {
	body := gev(`{"candidates":[{"content":{"parts":[{"text":"a"}]}},{"content":{"parts":[{"text":"b"}]}}]}`) +
		gev(`{"candidates":[{"content":{"parts":[{"text":"c"}]}},{"content":{"parts":[{"text":"d"}]}}]}`)
	content, err := ExtractResponse(FormatGemini, "text/event-stream", []byte(body))
	if err != nil || len(content.Parts) != 2 || content.Parts[0].Text != "ac" || content.Parts[1].Text != "bd" {
		t.Fatalf("%v %+v", err, content)
	}
}

func TestGeminiStreamSplitSurrogatePairAcrossChunks(t *testing.T) {
	body := gev(gtext(`ok \ud83d`)) + gev(gtext(`\ude00 `+leakKey))
	content, err := ExtractResponse(FormatGemini, "text/event-stream", []byte(body))
	if err != nil || content.Parts[0].Text != "ok \U0001F600 "+leakKey {
		t.Fatalf("%v %+v", err, content)
	}
	out := redacted(t, FormatGemini, body)
	assertNoLeak(t, out, leakKey)
	again, _ := ExtractResponse(FormatGemini, "text/event-stream", out)
	if again.Parts[0].Text != "ok \U0001F600 [KEY]" {
		t.Fatalf("%+v", again.Parts)
	}
}

func TestGeminiStreamErrorChunkIsInspected(t *testing.T) {
	body := gev(gtext("fine")) + gev(`{"error":{"code":429,"message":"quota for `+leakKey+`","status":"RESOURCE_EXHAUSTED"}}`)
	out := redacted(t, FormatGemini, body)
	assertNoLeak(t, out, leakKey)
	if !strings.Contains(string(out), "RESOURCE_EXHAUSTED") {
		t.Fatalf("error status lost: %s", out)
	}
	refused(t, FormatGemini, gev(`{"error":{"code":429,"message":"m","details":[{"x":"`+leakKey+`"}]}}`))
	refused(t, FormatGemini, gev(`{"error":{"code":429,"message":"m"},"candidates":[]}`))
}

func TestGeminiStreamFixtures(t *testing.T) {
	for _, name := range []string{"google/gemini-generate.sse", "google/vertex-generate.sse", "google/vertex-generate-cumulative.sse", "google/gemini-generate-interrupted.sse"} {
		body := fixture(t, name)
		content, err := ExtractResponse(FormatGemini, "text/event-stream", body)
		if err != nil || len(content.Parts) != 1 {
			t.Fatalf("%s: %v %+v", name, err, content)
		}
		if string(content.Apply(nil)) != string(body) {
			t.Fatalf("%s: untouched stream changed", name)
		}
		out := content.Apply(map[int]string{0: "[X]"})
		again, err := ExtractResponse(FormatGemini, "text/event-stream", out)
		if err != nil || again.Parts[0].Text != "[X]" {
			t.Fatalf("%s: %v", name, err)
		}
		// Usage metadata is on every chunk and unchanged.
		if strings.Count(string(out), "usageMetadata") != strings.Count(string(body), "usageMetadata") {
			t.Fatalf("%s: usage events changed", name)
		}
	}
	// An empty stream, and a stream of only function calls.
	if content, err := ExtractResponse(FormatGemini, "text/event-stream", nil); err != nil || len(content.Parts) != 0 {
		t.Fatalf("empty: %v", err)
	}
	only := gev(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","args":{"a":1,"b":true}}}]},"index":0,"finishReason":"STOP"}]}`)
	if content, err := ExtractResponse(FormatGemini, "text/event-stream", []byte(only)); err != nil || len(content.Parts) != 0 {
		t.Fatalf("tool calls without strings: %v %+v", err, content)
	}
}

func TestGeminiStreamReportsUninspectedParts(t *testing.T) {
	body := gev(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"AAAA"}},{"text":"x"}]},"index":0}]}`)
	content, err := ExtractResponse(FormatGemini, "text/event-stream", []byte(body))
	if err != nil || len(content.Uninspected) != 1 || content.Uninspected[0].Type != "inline_data" {
		t.Fatalf("%v %+v", err, content)
	}
}

func TestGeminiStreamJSONArrayFormIsAssembledAndChecked(t *testing.T) {
	body := `[` + gtext("key sk-LEAK") + `,` + gtext("ABLE12345678 ok") + `]`
	content, err := ExtractResponse(FormatGemini, "application/json", []byte(body))
	if err != nil || len(content.Parts) != 1 || content.Parts[0].Text != "key "+leakKey+" ok" {
		t.Fatalf("%v %+v", err, content)
	}
	out := content.Apply(map[int]string{0: "key [KEY] ok"})
	assertNoLeak(t, out, leakKey)
	if !json.Valid(out) {
		t.Fatalf("not JSON: %s", out)
	}
	grounded := `[{"candidates":[{"content":{"parts":[{"text":"` + leakKey + `"}]},"groundingMetadata":{"groundingSupports":[{"segment":{"text":"` + leakKey + `"}}]}}]}]`
	if _, err := ExtractResponse(FormatGemini, "application/json", []byte(grounded)); err == nil {
		t.Fatal("grounded array accepted")
	}
}

func TestGeminiStreamAmbiguityIsRefused(t *testing.T) {
	okPart := `{"text":"hi"}`
	cand := func(extra string) string {
		return gev(`{"candidates":[{"content":{"parts":[` + okPart + `]},"index":0` + extra + `}]}`)
	}
	cases := map[string]string{
		"grounding metadata repeats the text": cand(`,"groundingMetadata":{"groundingSupports":[{"segment":{"text":"` + leakKey + `"}}]}`),
		"log probabilities repeat the text":   cand(`,"logprobsResult":{"chosenCandidates":[{"token":"` + leakKey + `"}]}`),
		"unknown candidate field":             cand(`,"summary":"` + leakKey + `"`),
		"unknown chunk field":                 gev(`{"candidates":[],"echo":"` + leakKey + `"}`),
		"content from another role":           gev(`{"candidates":[{"content":{"role":"` + leakKey + `","parts":[]}}]}`),
		"unknown content field":               gev(`{"candidates":[{"content":{"parts":[],"note":"` + leakKey + `"}}]}`),
		"unknown part field":                  gev(`{"candidates":[{"content":{"parts":[{"text":"hi","caption":"` + leakKey + `"}]}}]}`),
		"function response in an answer":      gev(`{"candidates":[{"content":{"parts":[{"functionResponse":{"name":"f","response":{"v":"` + leakKey + `"}}}]}}]}`),
		"streamed call arguments":             gev(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","partialArgs":[{"jsonPath":"$.a","stringValue":"` + leakKey + `"}],"willContinue":true}}]}}]}`),
		"unknown call field":                  gev(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","args":{},"note":"` + leakKey + `"}}]}}]}`),
		"unknown code field":                  gev(`{"candidates":[{"content":{"parts":[{"executableCode":{"language":"PYTHON","code":"x","note":"` + leakKey + `"}}]}}]}`),
		"text is not a string":                gev(`{"candidates":[{"content":{"parts":[{"text":["` + leakKey + `"]}]}}]}`),
		"thought is not a boolean":            gev(`{"candidates":[{"content":{"parts":[{"text":"x","thought":"` + leakKey + `"}]}}]}`),
		"candidates is not an array":          gev(`{"candidates":{"content":"` + leakKey + `"}}`),
		"candidate is not an object":          gev(`{"candidates":["` + leakKey + `"]}`),
		"parts is not an array":               gev(`{"candidates":[{"content":{"parts":"` + leakKey + `"}}]}`),
		"part is not an object":               gev(`{"candidates":[{"content":{"parts":["` + leakKey + `"]}}]}`),
		"index is not a number":               gev(`{"candidates":[{"index":"0","content":{"parts":[]}}]}`),
		"both spellings of a field":           gev(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"f"},"function_call":{"name":"g","args":{"v":"` + leakKey + `"}}}]}}]}`),
		"field spelled in another case":       gev(`{"candidates":[{"content":{"parts":[{"text":"x","Text":"` + leakKey + `"}]}}]}`),
		"event field":                         "event: message\n" + cand(""),
		"id field":                            "id: " + leakKey + "\n" + cand(""),
		"unterminated final event":            cand("") + "data: " + gtext(leakKey),
		"unterminated with single break":      cand("") + "data: " + gtext(leakKey) + "\n",
		"not JSON":                            cand("") + "data: " + leakKey + "\n\n",
		"JSON array event":                    cand("") + `data: ["` + leakKey + `"]` + "\n\n",
		"duplicate key":                       gev(`{"candidates":[],"candidates":[{"content":{"parts":[{"text":"` + leakKey + `"}]}}]}`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) { refused(t, FormatGemini, body) })
	}
}

func TestGeminiStreamCommentsAndCRLF(t *testing.T) {
	body := ": " + leakKey + "\r\n\r\n" + strings.ReplaceAll(gev(gtext("a sk-LEAK"))+gev(gtext("ABLE12345678")), "\n", "\r\n") + ": " + leakKey + "\r\n\r\n"
	out := redacted(t, FormatGemini, body)
	assertNoLeak(t, out, leakKey)
	if strings.Contains(string(out), "\n") && !strings.Contains(string(out), "\r\n") {
		t.Fatal("line endings not kept")
	}
	quiet := strings.NewReplacer("sk-LEAK", "no-key", "ABLE12345678", "ok").Replace(body)
	if _, got, err := streamOutcome(t, FormatGemini, quiet, keyMasker()); err != nil || string(got) != quiet {
		t.Fatalf("untouched stream changed: %v", err)
	}
}

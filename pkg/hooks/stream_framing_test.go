package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const (
	chatDelta = `{"choices":[{"index":0,"delta":{"content":"key sk-LEAKABLE12345678"}}]}`
	antDelta  = `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"key sk-LEAKABLE12345678"}}`
	respDelta = `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"key sk-LEAKABLE12345678"}`
	gemDelta  = `{"candidates":[{"content":{"parts":[{"text":"key sk-LEAKABLE12345678"}]},"index":0}]}`
)

var framingFormats = []struct{ format, delta string }{
	{FormatOpenAIChat, chatDelta},
	{FormatAnthropicMessages, antDelta},
	{FormatOpenAIResponses, respDelta},
	{FormatGemini, gemDelta},
}

func TestLoneCarriageReturnIsRefusedInEveryFormat(t *testing.T) {
	for _, f := range framingFormats {
		t.Run(f.format, func(t *testing.T) {
			// A conforming client ends the comment line at the CR and
			// delivers the data line; the inspector must not read it as a
			// comment.
			refused(t, f.format, ": c\rdata: "+f.delta+"\n\n")
			refused(t, f.format, "data: "+f.delta+"\r\r")
			refused(t, f.format, "data: "+f.delta+"\n\n\r")
			refused(t, f.format, "data: "+f.delta+"\r\n\r\r\n")
		})
	}
}

func TestEscapedCarriageReturnInsideAStringIsNotALineEnd(t *testing.T) {
	for _, f := range framingFormats {
		t.Run(f.format, func(t *testing.T) {
			delta := strings.Replace(f.delta, "key ", `a\rb `, 1)
			body := "data: " + delta + "\n\n"
			if _, err := ExtractResponse(f.format, "text/event-stream", []byte(body)); err != nil {
				t.Fatalf("escaped CR refused: %v", err)
			}
		})
	}
}

func TestCRLFFramedStreamsAreInspectedAndRedacted(t *testing.T) {
	for _, f := range framingFormats {
		t.Run(f.format, func(t *testing.T) {
			body := "data: " + f.delta + "\r\n\r\ndata: " + f.delta + "\r\n\r\n"
			out := redacted(t, f.format, body)
			assertNoLeak(t, out, leakKey)
			if !strings.Contains(string(out), "\r\n\r\n") || strings.Contains(strings.ReplaceAll(string(out), "\r\n", ""), "\n") {
				t.Fatalf("CRLF framing not kept: %q", out)
			}
			if _, err := ExtractResponse(f.format, "text/event-stream", out); err != nil {
				t.Fatalf("output does not parse: %v", err)
			}
		})
	}
}

func TestUnterminatedFinalEventIsRefusedInEveryFormat(t *testing.T) {
	for _, f := range framingFormats {
		t.Run(f.format, func(t *testing.T) {
			refused(t, f.format, "data: "+f.delta+"\n\ndata: "+f.delta)
			refused(t, f.format, "data: "+f.delta+"\n\ndata: "+f.delta+"\n")
		})
	}
	// A stream whose last event is terminated is untouched by the rule.
	if _, err := ExtractResponse(FormatOpenAIChat, "text/event-stream", []byte("data: "+chatDelta+"\n\ndata: [DONE]\n\n")); err != nil {
		t.Fatal(err)
	}
}

func respEvent(fields string) string {
	return rev("response.completed", `"response":{"id":"r","object":"response","status":"completed","output":[]`+fields+`}`)
}

func TestResponsesUnknownFieldsAreInspectedNotRefused(t *testing.T) {
	// A field the provider adds passes when it is a number, boolean or null,
	// and is inspected (and redacted in place) when it holds text.
	quiet := respEvent(`,"frequency_penalty":0.0,"presence_penalty":0.0,"content_filters":[],"new_flag":true,"nothing":null,"store":true,"temperature":0.5`)
	out, got, err := streamOutcome(t, FormatOpenAIResponses, quiet, keyMasker())
	if err != nil || out.Action != Allow || string(got) != quiet {
		t.Fatalf("healthy stream: %v %+v", err, out)
	}
	content, _ := ExtractResponse(FormatOpenAIResponses, "text/event-stream", []byte(quiet))
	if len(content.Parts) != 0 || len(content.Uninspected) != 0 {
		t.Fatalf("parts %+v uninspected %+v", content.Parts, content.Uninspected)
	}
	for name, field := range map[string]string{
		"unknown string":        `,"zz_new_output":"` + leakKey + `"`,
		"unknown object":        `,"zz":{"a":[{"b":"` + leakKey + `"}]}`,
		"unknown array":         `,"zz":["x","` + leakKey + `"]`,
		"string where bool":     `,"store":"` + leakKey + `"`,
		"string where number":   `,"temperature":"` + leakKey + `"`,
		"container in a scalar": `,"model":{"v":"` + leakKey + `"}`,
		"string in usage":       `,"usage":{"input_tokens":1,"note":"` + leakKey + `"}`,
		"nested in usage":       `,"usage":{"input_tokens_details":{"x":"` + leakKey + `"}}`,
		"incomplete details":    `,"incomplete_details":{"reason":"max","extra":"` + leakKey + `"}`,
		"error":                 `,"error":{"code":"c","message":"m","extra":"` + leakKey + `"}`,
		"output_text":           `,"output_text":"` + leakKey + `"`,
		"error message":         `,"error":{"code":"c","message":"` + leakKey + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			assertNoLeak(t, redacted(t, FormatOpenAIResponses, respEvent(field)), leakKey)
		})
	}
	// Provider enumerations are relayed whatever their value.
	enum := respEvent(`,"service_tier":"zz-new","truncation":"zz","reasoning":{"effort":"xhigh"}`)
	if c, err := ExtractResponse(FormatOpenAIResponses, "text/event-stream", []byte(enum)); err != nil || len(c.Parts) != 0 {
		t.Fatalf("%v %+v", err, c)
	}
	// Bounds: beyond them the stream is refused.
	deep := strings.Repeat(`{"a":`, maxWalkDepth+2) + `"x"` + strings.Repeat(`}`, maxWalkDepth+2)
	refused(t, FormatOpenAIResponses, respEvent(`,"zz":`+deep))
	refused(t, FormatOpenAIResponses, respEvent(`,"zz":[`+strings.Repeat(`"x",`, maxWalkLeaves)+`"x"]`))
}

func TestResponsesEchoedRequestIsRelayedAsSent(t *testing.T) {
	// The caller's own request, echoed: not model output, not uninspected.
	echo := respEvent(`,"instructions":"` + leakKey + `","user":"` + leakKey + `","safety_identifier":"` + leakKey + `","prompt_cache_key":"` + leakKey +
		`","metadata":{"` + leakKey + `":"` + leakKey + `"},"tools":[{"type":"function","name":"f","description":"` + leakKey + `"}],"tool_choice":{"type":"function","name":"f"},` +
		`"text":{"format":{"type":"json_schema","name":"s","schema":{"x":"` + leakKey + `"}}},"reasoning":{"effort":"` + leakKey + `"},"prompt":{"id":"` + leakKey + `"},"conversation":{"id":"c"},"billing":{"payer":"x"}`)
	content, err := ExtractResponse(FormatOpenAIResponses, "text/event-stream", []byte(echo))
	if err != nil || len(content.Parts) != 0 || len(content.Uninspected) != 0 {
		t.Fatalf("%v %+v", err, content)
	}
	if out := content.Apply(nil); string(out) != echo {
		t.Fatal("echo changed")
	}
}

// TestResponsesStreamedAndWholeAgree delivers the same response streamed and
// whole: the decision, the redacted text and the uninspected reports are equal.
func TestResponsesStreamedAndWholeAgree(t *testing.T) {
	echo := `,"instructions":"system ` + leakKey + `","tools":[{"type":"function","name":"f","parameters":{"d":"` + leakKey + `"}}],"tool_choice":{"type":"function","name":"f"},` +
		`"text":{"format":{"type":"json_schema","name":"s"}},"metadata":{"k":"` + leakKey + `"},"user":"u"`
	reasoning := true
	item := func(answer string) string {
		extra := ""
		if reasoning {
			extra = `,{"id":"rs","type":"reasoning","summary":[]}`
		}
		return `[{"id":"m","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"` + answer + `","annotations":[]}]}` + extra + `]`
	}
	withPolicy := func(nonText, action string) Resolved {
		return Resolve([]Policy{{ID: "p", Scope: Scope{Kind: ScopeTenant}, NonText: nonText, Hooks: []Hook{redactHook("mask", RedactRule{ID: "key", Pattern: `sk-[A-Za-z0-9]{8,}`, Action: action})}}}, Target{Plane: PlaneModels})
	}
	type result struct {
		refused      bool
		action, text string
		uninspected  int
	}
	run := func(content *Content, resolved Resolved, original string) result {
		out := Run(context.Background(), Runtime{}, resolved, Meta{Plane: PlaneModels}, PhaseResponse, content, nil)
		res := result{refused: out.Refused(), action: out.Action, uninspected: len(content.Uninspected)}
		body := original
		if out.Action == Transform {
			body = string(out.Body)
		}
		res.text = strings.Join(textsOf(body), "|")
		return res
	}
	for _, with := range []bool{true, false} {
		reasoning = with
		for _, answer := range []string{"clean answer", "answer " + leakKey} {
			whole := `{"id":"r","object":"response","status":"completed","output":` + item(answer) + echo + `}`
			stream := rev("response.completed", `"response":`+whole)
			for _, nonText := range []string{"deny", "allow"} {
				for _, action := range []string{ActionMask, "deny"} {
					resolved := withPolicy(nonText, action)
					a, err := ExtractResponse(FormatOpenAIResponses, "application/json", []byte(whole))
					if err != nil {
						t.Fatal(err)
					}
					b, err := ExtractResponse(FormatOpenAIResponses, "text/event-stream", []byte(stream))
					if err != nil {
						t.Fatal(err)
					}
					ra, rb := run(a, resolved, whole), run(b, resolved, stream)
					if ra != rb {
						t.Fatalf("answer %q non_text=%s action=%s: whole %+v, streamed %+v", answer, nonText, action, ra, rb)
					}
				}
			}
		}
	}
}

// textsOf lists the output text and the echoed instructions of a body, which
// is either a response object or a stream of one completed event.
func textsOf(body string) []string {
	if i := strings.Index(body, "data: "); i >= 0 {
		body = strings.TrimSpace(strings.SplitN(body[i+6:], "\n", 2)[0])
		var ev struct {
			Response json.RawMessage `json:"response"`
		}
		_ = json.Unmarshal([]byte(body), &ev)
		body = string(ev.Response)
	}
	var doc struct {
		Instructions string `json:"instructions"`
		Output       []struct {
			Content []struct{ Text string } `json:"content"`
		} `json:"output"`
	}
	_ = json.Unmarshal([]byte(body), &doc)
	out := []string{doc.Instructions}
	for _, o := range doc.Output {
		for _, c := range o.Content {
			out = append(out, c.Text)
		}
	}
	return out
}

func TestGeminiTextAroundAFunctionCallIsOneText(t *testing.T) {
	call := `{"functionCall":{"name":"f","args":{"k":"v"}}}`
	streamed := gev(`{"candidates":[{"content":{"parts":[{"text":"key sk-LEAK"},` + call + `,{"text":"ABLE12345678 ok"}]},"index":0}]}`)
	content, err := ExtractResponse(FormatGemini, "text/event-stream", []byte(streamed))
	if err != nil || len(content.Parts) != 2 || content.Parts[0].Text != "key "+leakKey+" ok" {
		t.Fatalf("streamed: %v %+v", err, content)
	}
	out := redacted(t, FormatGemini, streamed)
	assertNoLeak(t, out, leakKey)
	if !strings.Contains(string(out), `"name":"f"`) {
		t.Fatalf("function call lost: %s", out)
	}
	// Across chunks as well.
	split := gev(gtext("key sk-LEAK")) + gev(`{"candidates":[{"content":{"parts":[`+call+`]},"index":0}]}`) + gev(gtext("ABLE12345678"))
	assertNoLeak(t, redacted(t, FormatGemini, split), leakKey)

	// Not streamed: the same candidate in one JSON body.
	body := `{"candidates":[{"content":{"role":"model","parts":[{"text":"key sk-LEAK"},` + call + `,{"text":"ABLE12345678 ok"}]},"index":0}]}`
	whole, err := ExtractResponse(FormatGemini, "application/json", []byte(body))
	if err != nil || len(whole.Parts) != 2 || whole.Parts[0].Text != "key "+leakKey+" ok" {
		t.Fatalf("not streamed: %v %+v", err, whole)
	}
	res := Run(context.Background(), Runtime{}, keyMasker(), Meta{}, PhaseResponse, whole, nil)
	if res.Action != Transform {
		t.Fatalf("not redacted: %+v", res)
	}
	assertNoLeakJSON(t, res.Body)
	if !json.Valid(res.Body) || !strings.Contains(string(res.Body), `"name":"f"`) {
		t.Fatalf("body: %s", res.Body)
	}
	// Thought text stays apart from answer text.
	th := `{"candidates":[{"content":{"parts":[{"text":"think ","thought":true},{"text":"say "},{"text":"more","thought":true}]}}]}`
	c, err := ExtractResponse(FormatGemini, "application/json", []byte(th))
	if err != nil || len(c.Parts) != 2 || c.Parts[0].Text != "think more" || c.Parts[1].Text != "say " {
		t.Fatalf("%v %+v", err, c)
	}
}

var strictMasker = Resolve([]Policy{{ID: "p", Scope: Scope{Kind: ScopeTenant}, NonText: "deny", Hooks: []Hook{redactHook("mask", RedactRule{ID: "key", Pattern: `sk-[A-Za-z0-9]{8,}`, Action: ActionMask})}}}, Target{Plane: PlaneModels})

func assertNoLeakJSON(t *testing.T, body []byte) {
	t.Helper()
	if strings.Contains(string(body), leakKey) || strings.Contains(string(body), "sk-LEAK") || strings.Contains(string(body), "ABLE1234") {
		t.Fatalf("original text survives: %s", body)
	}
}

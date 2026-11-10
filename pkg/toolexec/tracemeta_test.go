package toolexec

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

const (
	tp = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	ts = "vendor=opaque,other=v2"
)

// awkward is a request whose bytes must survive: big integers beyond float64,
// a float with an exponent, negative zero, unicode in both forms, escapes,
// unusual spacing, key order, a nested _meta inside arguments and a duplicate-
// looking name at another depth.
const awkward = `{ "jsonrpc" : "2.0",  "id":9007199254740993123, "method":"tools/call",
 "params" : { "name":"echo", "arguments": {"big":123456789012345678901234567890,"f":1.0e+100,"z":-0.0,"s":"caf\u00e9 \ud83d\ude00 é 😀 \"q\" \\ \/","_meta":{"traceparent":"keep-me-inside-arguments"},"order":[3,1,2]}%s}  }`

func withMeta(meta string) string {
	if meta != "" {
		meta = "," + meta
	}
	return strings.Replace(awkward, "%s", meta, 1)
}

// stripped removes the members PatchTraceMeta may add so the remainder can be
// compared byte for byte with the original.
func TestInsertedMetaLeavesEveryOtherByteUnchanged(t *testing.T) {
	in := strings.Replace(awkward, "%s", "", 1)
	out, err := PatchTraceMeta([]byte(in), tp, ts, true)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(awkward, "%s", `,"_meta":{"traceparent":"`+tp+`","tracestate":"`+ts+`"}`, 1)
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
	// Everything before the insertion point and after it is the original text.
	i := strings.Index(string(out), `,"_meta":{"traceparent":"00-`)
	if string(out[:i]) != in[:i] || !strings.HasPrefix(in[i:], "}") && !strings.HasPrefix(string(out[i:]), `,"_meta"`) {
		t.Fatal("bytes outside the inserted member changed")
	}
	var parsed struct {
		Params struct {
			Arguments json.RawMessage `json:"arguments"`
			Meta      map[string]string
		} `json:"params"`
	}
	if err = json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if !bytes.Contains(parsed.Params.Arguments, []byte("123456789012345678901234567890")) || !bytes.Contains(parsed.Params.Arguments, []byte("1.0e+100")) || !bytes.Contains(parsed.Params.Arguments, []byte("keep-me-inside-arguments")) {
		t.Fatalf("arguments changed: %s", parsed.Params.Arguments)
	}
}

func TestCallerTraceContextIsReplacedAndOtherMetaKeysKeepTheirOrderAndBytes(t *testing.T) {
	meta := `"_meta" : { "progressToken": 12345678901234567890 , "traceparent":"00-11111111111111111111111111111111-2222222222222222-00", "io.modelcontextprotocol/clientInfo":{"name":"x","version":"1.0"}, "tracestate":"caller=1", "baggage":"user=alice" }`
	out, err := PatchTraceMeta([]byte(withMeta(meta)), tp, "", true)
	if err != nil {
		t.Fatal(err)
	}
	want := withMeta(`"_meta" : {"progressToken": 12345678901234567890,"traceparent":"` + tp + `","io.modelcontextprotocol/clientInfo":{"name":"x","version":"1.0"}}`)
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
	// The same input with a span that has a tracestate.
	out, err = PatchTraceMeta([]byte(withMeta(meta)), tp, ts, true)
	if err != nil || !strings.Contains(string(out), `"tracestate":"`+ts+`"`) || strings.Contains(string(out), "caller=1") || strings.Contains(string(out), "alice") {
		t.Fatalf("%s %v", out, err)
	}
	if strings.Contains(string(out), "11111111111111111111111111111111") {
		t.Fatal("the caller's traceparent survived")
	}
	// Bytes before and after the _meta object are the original's.
	pre, post := withMeta("")[:strings.Index(withMeta(""), `"arguments"`)], `}  }`
	if !strings.HasPrefix(string(out), pre) || !strings.HasSuffix(string(out), post) {
		t.Fatalf("surrounding bytes changed: %s", out)
	}
}

func TestPropagationOffStripsTraceContextButTouchesNothingElse(t *testing.T) {
	meta := `"_meta":{"traceparent":"` + tp + `","tracestate":"a=b","baggage":"k=v","progressToken":"p1"}`
	out, err := PatchTraceMeta([]byte(withMeta(meta)), "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := withMeta(`"_meta":{"progressToken":"p1"}`); string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
	// No _meta and propagation off: the same slice back.
	plain := strings.Replace(awkward, "%s", "", 1)
	same, err := PatchTraceMeta([]byte(plain), "", "", false)
	if err != nil || string(same) != plain {
		t.Fatalf("%s %v", same, err)
	}
}

func TestMetaEdgeCases(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"empty params":      {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"_meta":{"traceparent":"` + tp + `"}}}`},
		"padded empty":      {`{"id":1,"params": { } }`, `{"id":1,"params": {"_meta":{"traceparent":"` + tp + `"}} }`},
		"null meta":         {`{"id":1,"params":{"name":"a","_meta":null}}`, `{"id":1,"params":{"name":"a","_meta":{"traceparent":"` + tp + `"}}}`},
		"empty meta":        {`{"id":1,"params":{"name":"a","_meta":{}}}`, `{"id":1,"params":{"name":"a","_meta":{"traceparent":"` + tp + `"}}}`},
		"meta first":        {`{"id":1,"params":{"_meta":{"a":1},"name":"a"}}`, `{"id":1,"params":{"_meta":{"a":1,"traceparent":"` + tp + `"},"name":"a"}}`},
		"no params":         {`{"id":1,"method":"ping"}`, `{"id":1,"method":"ping"}`},
		"params not object": {`{"id":1,"params":[1]}`, `{"id":1,"params":[1]}`},
		"braces in strings": {`{"id":1,"params":{"name":"}{\"","_meta":{"x":"}"}}}`, `{"id":1,"params":{"name":"}{\"","_meta":{"x":"}","traceparent":"` + tp + `"}}}`},
	} {
		out, err := PatchTraceMeta([]byte(c.in), tp, "", true)
		if err != nil || string(out) != c.want {
			t.Errorf("%s:\n got %s (%v)\nwant %s", name, out, err, c.want)
		}
		if !json.Valid(out) {
			t.Errorf("%s: invalid JSON %s", name, out)
		}
	}
	for name, in := range map[string]string{
		"meta is a string": `{"id":1,"params":{"_meta":"x"}}`,
		"duplicate meta":   `{"id":1,"params":{"_meta":{},"_meta":{}}}`,
		"duplicate params": `{"id":1,"params":{},"params":{}}`,
		"duplicate tp":     `{"id":1,"params":{"_meta":{"traceparent":"a","traceparent":"b"}}}`,
	} {
		if _, err := PatchTraceMeta([]byte(in), tp, "", true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTraceparentFromTheExchangeSpanReachesTheUpstreamBody(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanID, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled}))
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"n":1},"_meta":{"traceparent":"00-11111111111111111111111111111111-2222222222222222-00"}}}`
	out, err := withTraceMeta(ctx, []byte(in))
	if err != nil || !strings.Contains(string(out), `"traceparent":"00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"`) || strings.Contains(string(out), "1111") {
		t.Fatalf("%s %v", out, err)
	}
	// No span in the context: the caller's value is removed, not forwarded.
	out, err = withTraceMeta(context.Background(), []byte(in))
	if err != nil || strings.Contains(string(out), "traceparent") {
		t.Fatalf("%s %v", out, err)
	}
}

func TestParseRequestAcceptsMetaButDigestsIgnoreIt(t *testing.T) {
	plain := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"n":1}}}`
	withMetaObj := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"n":1},"_meta":{"progressToken":"p"}}}`
	a, err := ParseRequest([]byte(plain), []string{"echo"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseRequest([]byte(withMetaObj), []string{"echo"})
	if err != nil {
		t.Fatalf("a request with _meta must be accepted: %v", err)
	}
	if a.SemanticDigest != b.SemanticDigest || a.PayloadDigest == b.PayloadDigest {
		t.Fatal("_meta is outside the semantic digest and inside the payload digest")
	}
	if _, err = ParseRequest([]byte(strings.Replace(plain, `"arguments"`, `"_meta":"x","arguments"`, 1)), []string{"echo"}); err == nil {
		t.Fatal("a non-object _meta was accepted")
	}
}

package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The buffered-stream tests of the formats whose protocol repeats content
// (OpenAI Responses, Gemini). A redaction must leave no copy of the original
// text anywhere in what the caller receives.

const leakKey = "sk-LEAKABLE12345678"

func keyMasker() Resolved {
	return resolvedOf(redactHook("mask", RedactRule{ID: "key", Pattern: `sk-[A-Za-z0-9]{8,}`, Action: ActionMask}))
}

// streamOutcome runs the response hooks over a buffered stream exactly as the
// gateway does and returns the bytes the caller would receive.
func streamOutcome(t *testing.T, format, body string, resolved Resolved) (Outcome, []byte, error) {
	t.Helper()
	content, err := ExtractResponse(format, "text/event-stream", []byte(body))
	if err != nil {
		return Outcome{}, nil, err
	}
	out := Run(context.Background(), Runtime{}, resolved, Meta{Plane: PlaneModels}, PhaseResponse, content, nil)
	if out.Refused() {
		return out, nil, ErrUninspectable
	}
	if out.Action == Transform {
		return out, out.Body, nil
	}
	return out, []byte(body), nil
}

// redacted fails the test unless the stream is accepted and transformed.
func redacted(t *testing.T, format, body string) []byte {
	t.Helper()
	out, got, err := streamOutcome(t, format, body, keyMasker())
	if err != nil {
		t.Fatalf("stream refused: %v (%+v)", err, out)
	}
	if out.Action != Transform {
		t.Fatalf("nothing was redacted: %+v", out)
	}
	return got
}

// refused fails the test unless the stream is refused as uninspectable.
func refused(t *testing.T, format, body string) {
	t.Helper()
	content, err := ExtractResponse(format, "text/event-stream", []byte(body))
	if err == nil {
		t.Fatalf("stream accepted, want refusal: %+v", content.Parts)
	}
	if !strings.Contains(err.Error(), ErrUninspectable.Error()) {
		t.Fatalf("refusal is not ErrUninspectable: %v", err)
	}
	// And the hook chain turns that into a deny, never a pass.
	out := Run(context.Background(), Runtime{}, keyMasker(), Meta{Plane: PlaneModels}, PhaseResponse, nil, err)
	if !out.Refused() {
		t.Fatalf("an uninspectable stream was not refused: %+v", out)
	}
}

// assertNoLeak checks the delivered stream for the original text in every
// form a client could reassemble: the raw bytes, each data payload decoded
// (so escapes cannot hide it), and the payload strings of the whole stream
// joined (so a value split across events cannot hide it).
func assertNoLeak(t *testing.T, out []byte, secrets ...string) {
	t.Helper()
	var joined strings.Builder
	for _, event := range parseSSE(out).events {
		data := event.data()
		if len(data) == 0 || string(data) == "[DONE]" {
			continue
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("re-emitted event is not JSON: %v\n%s", err, data)
		}
		walkStrings(doc, func(s string) { joined.WriteString(s) })
	}
	for _, secret := range secrets {
		if bytes.Contains(out, []byte(secret)) {
			t.Fatalf("original text %q is in the re-emitted bytes:\n%s", secret, out)
		}
		if strings.Contains(joined.String(), secret) {
			t.Fatalf("original text %q is in the re-emitted payload strings:\n%s", secret, out)
		}
		// Fragments of the secret long enough to matter.
		for _, piece := range []string{secret[:7], secret[len(secret)-7:]} {
			if bytes.Contains(out, []byte(piece)) {
				t.Fatalf("a piece %q of the original text survives:\n%s", piece, out)
			}
		}
	}
}

func walkStrings(v any, f func(string)) {
	switch v := v.(type) {
	case string:
		f(v)
	case []any:
		for _, e := range v {
			walkStrings(e, f)
		}
	case map[string]any:
		for _, e := range v {
			walkStrings(e, f)
		}
	}
}

// eventTypes lists the type (or "") of each data event, in order.
func eventTypes(t *testing.T, stream []byte) []string {
	t.Helper()
	var out []string
	for _, event := range parseSSE(stream).events {
		data := event.data()
		if len(data) == 0 {
			continue
		}
		var probe struct{ Type string }
		_ = json.Unmarshal(data, &probe)
		out = append(out, probe.Type)
	}
	return out
}

// fieldOf returns a top-level field of the first event whose type matches
// (Responses) or of the event at the given position.
func eventField(t *testing.T, stream []byte, typ, field string) any {
	t.Helper()
	for _, event := range parseSSE(stream).events {
		var doc map[string]any
		if json.Unmarshal(event.data(), &doc) == nil && doc["type"] == typ {
			return doc[field]
		}
	}
	t.Fatalf("no %s event", typ)
	return nil
}

func sameJSON(t *testing.T, a, b any, what string) {
	t.Helper()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("%s changed:\n%v\n%v", what, a, b)
	}
}

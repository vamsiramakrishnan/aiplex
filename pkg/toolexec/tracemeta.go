package toolexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
)

// withTraceMeta applies PatchTraceMeta with the span context of ctx. Without
// a valid span, or with AIPLEX_TRACE_PROPAGATE_UPSTREAM=false, any trace
// context the caller put in _meta is removed, as the HTTP headers are.
func withTraceMeta(ctx context.Context, body []byte) ([]byte, error) {
	traceparent, tracestate, ok := telemetry.TraceContextFields(ctx)
	return PatchTraceMeta(body, traceparent, tracestate, ok)
}

// W3C trace context in an MCP request's params._meta.
//
// The MCP specification reserves the _meta keys traceparent, tracestate and
// baggage for OpenTelemetry trace context, "to maintain compatibility with
// existing implementations and OpenTelemetry semantic conventions for MCP"
// (https://modelcontextprotocol.io/specification/draft/basic/index, section
// "General fields", "_meta", "OpenTelemetry trace context"; the example there
// is a tools/call with params._meta.traceparent). AIPlex puts the exchange's
// span context there, in addition to the HTTP headers, for tools/call.
//
// The caller's request is relayed byte for byte except for that one object:
// PatchTraceMeta edits the text of params._meta (or inserts it as the last
// member of params) and copies every other byte unchanged, so numbers, string
// escapes, key order and whitespace elsewhere cannot change. The request is
// never decoded and re-encoded.

// Reserved _meta keys.
const (
	metaTraceparent = "traceparent"
	metaTracestate  = "tracestate"
	metaBaggage     = "baggage"
)

var errMeta = errors.New("params._meta cannot be edited")

// PatchTraceMeta sets params._meta.traceparent (and tracestate, when not
// empty) in a JSON-RPC request. A traceparent or tracestate the caller put
// there is replaced, or removed when propagate is false (or tracestate is
// empty); a caller's baggage is always removed, because baggage is neither read
// nor forwarded. With propagate false and no _meta, the body is returned
// unchanged. body must be one JSON object that was already validated.
func PatchTraceMeta(body []byte, traceparent, tracestate string, propagate bool) ([]byte, error) {
	root, err := parseObject(body, skipSpace(body, 0))
	if err != nil {
		return nil, errMeta
	}
	params, ok := root.single("params")
	if !ok {
		if root.count("params") > 0 {
			return nil, errMeta
		}
		return body, nil
	}
	if body[params.valStart] != '{' {
		return body, nil
	}
	pobj, err := parseObject(body, params.valStart)
	if err != nil {
		return nil, errMeta
	}
	desired := metaMembers(traceparent, tracestate, propagate)
	meta, found := pobj.single("_meta")
	if !found && pobj.count("_meta") > 0 {
		return nil, errMeta
	}
	switch {
	case !found && !propagate:
		return body, nil
	case !found:
		return insertMember(body, pobj, "_meta", metaObject(desired))
	case bytes.Equal(bytes.TrimSpace(body[meta.valStart:meta.valEnd]), []byte("null")):
		if !propagate {
			return body, nil
		}
		return splice(body, meta.valStart, meta.valEnd, metaObject(desired)), nil
	case body[meta.valStart] != '{':
		return nil, errMeta
	}
	mobj, err := parseObject(body, meta.valStart)
	if err != nil {
		return nil, errMeta
	}
	var out [][]byte
	done := map[string]bool{}
	for _, m := range mobj.members {
		switch m.key {
		case metaBaggage:
			continue
		case metaTraceparent, metaTracestate:
			if mobj.count(m.key) != 1 {
				return nil, errMeta
			}
			value, set := desired[m.key]
			if !set {
				continue
			}
			done[m.key] = true
			out = append(out, memberText(m.rawKey(body), value))
		default:
			out = append(out, body[m.keyStart:m.valEnd])
		}
	}
	for _, key := range []string{metaTraceparent, metaTracestate} {
		if value, set := desired[key]; set && !done[key] {
			out = append(out, memberText(quote(key), value))
		}
	}
	rebuilt := append(append([]byte{'{'}, bytes.Join(out, []byte{','})...), '}')
	return splice(body, meta.valStart, meta.valEnd, rebuilt), nil
}

// metaMembers returns the members AIPlex sets, as JSON string values.
func metaMembers(traceparent, tracestate string, propagate bool) map[string][]byte {
	if !propagate || traceparent == "" {
		return map[string][]byte{}
	}
	out := map[string][]byte{metaTraceparent: quote(traceparent)}
	if tracestate != "" {
		out[metaTracestate] = quote(tracestate)
	}
	return out
}

func metaObject(members map[string][]byte) []byte {
	var parts [][]byte
	for _, key := range []string{metaTraceparent, metaTracestate} {
		if value, ok := members[key]; ok {
			parts = append(parts, memberText(quote(key), value))
		}
	}
	return append(append([]byte{'{'}, bytes.Join(parts, []byte{','})...), '}')
}

func memberText(key, value []byte) []byte {
	out := append([]byte{}, key...)
	out = append(out, ':')
	return append(out, value...)
}

func quote(s string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func splice(body []byte, start, end int, replacement []byte) []byte {
	out := make([]byte, 0, len(body)-(end-start)+len(replacement))
	out = append(out, body[:start]...)
	out = append(out, replacement...)
	return append(out, body[end:]...)
}

// insertMember adds key as the last member of an object, leaving every
// existing byte as it was.
func insertMember(body []byte, obj object, key string, value []byte) ([]byte, error) {
	member := memberText(quote(key), value)
	if len(obj.members) == 0 {
		return splice(body, obj.start+1, obj.end-1, member), nil
	}
	last := obj.members[len(obj.members)-1]
	return splice(body, last.valEnd, last.valEnd, append([]byte{','}, member...)), nil
}

// ---- a minimal JSON object scanner over already validated text ----

type member struct {
	key              string
	keyStart, keyEnd int
	valStart, valEnd int
}

func (m member) rawKey(body []byte) []byte { return body[m.keyStart:m.keyEnd] }

type object struct {
	start, end int // the braces: body[start] == '{', body[end-1] == '}'
	members    []member
}

func (o object) count(key string) int {
	n := 0
	for _, m := range o.members {
		if m.key == key {
			n++
		}
	}
	return n
}

func (o object) single(key string) (member, bool) {
	var found member
	n := 0
	for _, m := range o.members {
		if m.key == key {
			found = m
			n++
		}
	}
	return found, n == 1
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

func scanString(b []byte, i int) (int, error) {
	if i >= len(b) || b[i] != '"' {
		return 0, fmt.Errorf("string expected")
	}
	for i++; i < len(b); i++ {
		switch b[i] {
		case '\\':
			i++
		case '"':
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated string")
}

// scanValue returns the end of the value starting at i.
func scanValue(b []byte, i int) (int, error) {
	if i >= len(b) {
		return 0, fmt.Errorf("value expected")
	}
	switch b[i] {
	case '"':
		return scanString(b, i)
	case '{', '[':
		depth := 0
		for j := i; j < len(b); j++ {
			switch b[j] {
			case '"':
				end, err := scanString(b, j)
				if err != nil {
					return 0, err
				}
				j = end - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return j + 1, nil
				}
			}
		}
		return 0, fmt.Errorf("unterminated container")
	}
	j := i
	for j < len(b) && b[j] != ',' && b[j] != '}' && b[j] != ']' && b[j] != ' ' && b[j] != '\t' && b[j] != '\n' && b[j] != '\r' {
		j++
	}
	if j == i {
		return 0, fmt.Errorf("value expected")
	}
	return j, nil
}

func parseObject(b []byte, i int) (object, error) {
	if i >= len(b) || b[i] != '{' {
		return object{}, fmt.Errorf("object expected")
	}
	end, err := scanValue(b, i)
	if err != nil {
		return object{}, err
	}
	obj := object{start: i, end: end}
	j := skipSpace(b, i+1)
	for j < end-1 {
		keyEnd, err := scanString(b, j)
		if err != nil {
			return object{}, err
		}
		var key string
		if err = json.Unmarshal(b[j:keyEnd], &key); err != nil {
			return object{}, err
		}
		colon := skipSpace(b, keyEnd)
		if colon >= end || b[colon] != ':' {
			return object{}, fmt.Errorf("colon expected")
		}
		valStart := skipSpace(b, colon+1)
		valEnd, err := scanValue(b, valStart)
		if err != nil {
			return object{}, err
		}
		obj.members = append(obj.members, member{key: key, keyStart: j, keyEnd: keyEnd, valStart: valStart, valEnd: valEnd})
		j = skipSpace(b, valEnd)
		if j < end-1 {
			if b[j] != ',' {
				return object{}, fmt.Errorf("comma expected")
			}
			j = skipSpace(b, j+1)
		}
	}
	return obj, nil
}

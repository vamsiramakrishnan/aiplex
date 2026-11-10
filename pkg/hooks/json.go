package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// node is one JSON value with the byte span it occupies in the source
// document. Extractors read text from string nodes and record their spans;
// a transform splices new string literals into exactly those spans, so every
// other byte (key order, whitespace, unknown fields, number spelling, escapes)
// stays as the caller sent it.
type node struct {
	kind       byte // 'o' object, 'a' array, 's' string, 'n' number, 'b' bool, 'z' null
	start, end int
	keys       []string
	vals       []*node
	elems      []*node
	str        string
}

const maxJSONDepth = 128

var errInvalidJSON = errors.New("body is not one unambiguous JSON value")

// parseJSON parses one JSON value strictly: valid UTF-8, no trailing data,
// no duplicate object keys at any depth (a duplicate could make AIPlex
// inspect one value while the upstream uses another), bounded depth.
func parseJSON(data []byte) (*node, error) {
	if !utf8.Valid(data) {
		return nil, errInvalidJSON
	}
	p := &jsonParser{data: data}
	p.space()
	n, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(p.data) {
		return nil, errInvalidJSON
	}
	return n, nil
}

type jsonParser struct {
	data []byte
	pos  int
}

func (p *jsonParser) space() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *jsonParser) value(depth int) (*node, error) {
	if depth > maxJSONDepth || p.pos >= len(p.data) {
		return nil, errInvalidJSON
	}
	start := p.pos
	switch c := p.data[p.pos]; {
	case c == '{':
		p.pos++
		n := &node{kind: 'o', start: start}
		seen := map[string]bool{}
		p.space()
		if p.pos < len(p.data) && p.data[p.pos] == '}' {
			p.pos++
			n.end = p.pos
			return n, nil
		}
		for {
			p.space()
			key, err := p.value(depth + 1)
			if err != nil || key.kind != 's' {
				return nil, errInvalidJSON
			}
			if seen[key.str] {
				return nil, errInvalidJSON
			}
			seen[key.str] = true
			p.space()
			if p.pos >= len(p.data) || p.data[p.pos] != ':' {
				return nil, errInvalidJSON
			}
			p.pos++
			p.space()
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			n.keys = append(n.keys, key.str)
			n.vals = append(n.vals, v)
			p.space()
			if p.pos >= len(p.data) {
				return nil, errInvalidJSON
			}
			if p.data[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.data[p.pos] == '}' {
				p.pos++
				n.end = p.pos
				return n, nil
			}
			return nil, errInvalidJSON
		}
	case c == '[':
		p.pos++
		n := &node{kind: 'a', start: start}
		p.space()
		if p.pos < len(p.data) && p.data[p.pos] == ']' {
			p.pos++
			n.end = p.pos
			return n, nil
		}
		for {
			p.space()
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			n.elems = append(n.elems, v)
			p.space()
			if p.pos >= len(p.data) {
				return nil, errInvalidJSON
			}
			if p.data[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.data[p.pos] == ']' {
				p.pos++
				n.end = p.pos
				return n, nil
			}
			return nil, errInvalidJSON
		}
	case c == '"':
		p.pos++
		for p.pos < len(p.data) {
			switch p.data[p.pos] {
			case '\\':
				p.pos += 2
				continue
			case '"':
				p.pos++
				n := &node{kind: 's', start: start, end: p.pos}
				if err := json.Unmarshal(p.data[start:p.pos], &n.str); err != nil {
					return nil, errInvalidJSON
				}
				return n, nil
			}
			if p.data[p.pos] < 0x20 {
				return nil, errInvalidJSON
			}
			p.pos++
		}
		return nil, errInvalidJSON
	case c == 't' || c == 'f' || c == 'n':
		for _, lit := range []string{"true", "false", "null"} {
			if bytes.HasPrefix(p.data[p.pos:], []byte(lit)) {
				p.pos += len(lit)
				kind := byte('b')
				if lit == "null" {
					kind = 'z'
				}
				return &node{kind: kind, start: start, end: p.pos}, nil
			}
		}
		return nil, errInvalidJSON
	case c == '-' || (c >= '0' && c <= '9'):
		for p.pos < len(p.data) && strings.IndexByte("+-0123456789.eE", p.data[p.pos]) >= 0 {
			p.pos++
		}
		if !json.Valid(p.data[start:p.pos]) {
			return nil, errInvalidJSON
		}
		return &node{kind: 'n', start: start, end: p.pos}, nil
	}
	return nil, errInvalidJSON
}

// get returns an object member, or nil.
func (n *node) get(key string) *node {
	if n == nil || n.kind != 'o' {
		return nil
	}
	for i, k := range n.keys {
		if k == key {
			return n.vals[i]
		}
	}
	return nil
}

// first returns the member under the first spelling present. Providers that
// parse proto JSON (Gemini) accept both camelCase and snake_case; both
// present at once is ambiguous and refused by checkSpelling.
func (n *node) first(keys ...string) *node {
	for _, k := range keys {
		if v := n.get(k); v != nil {
			return v
		}
	}
	return nil
}

// checkSpelling refuses an object whose member names differ from a field
// AIPlex inspects only by letter case or by the camel/snake spelling the
// provider also accepts, when more than one spelling is present. Upstream
// parsers that match names case-insensitively (Go's encoding/json does)
// would otherwise read content AIPlex did not inspect.
func (n *node) checkSpelling(fields ...string) error {
	if n == nil || n.kind != 'o' {
		return nil
	}
	for _, field := range fields {
		canonical := normalizeName(field)
		matches := 0
		for _, k := range n.keys {
			if normalizeName(k) == canonical {
				matches++
				if k != field && !alternateSpelling(k, field) {
					return fmt.Errorf("ambiguous spelling of field %q", field)
				}
			}
		}
		if matches > 1 {
			return fmt.Errorf("field %q is present under more than one spelling", field)
		}
	}
	return nil
}

func normalizeName(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }

// alternateSpelling accepts the camelCase/snake_case twin of a field.
func alternateSpelling(k, field string) bool { return k == snake(field) || snake(k) == field }

func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// stringValue returns the decoded string, or "" with ok false.
func (n *node) stringValue() (string, bool) {
	if n == nil || n.kind != 's' {
		return "", false
	}
	return n.str, true
}

// locator builds stable JSON paths: messages[0].content[1].text.
func locator(parent, key string) string {
	ident := key != ""
	for _, r := range key {
		if !(r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			ident = false
			break
		}
	}
	if !ident {
		quoted, _ := json.Marshal(key)
		return parent + "[" + string(quoted) + "]"
	}
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func index(parent string, i int) string { return parent + "[" + strconv.Itoa(i) + "]" }

// edit replaces the JSON string literal at [start,end) with value.
type edit struct {
	start, end int
	value      string
}

// encodeString writes a JSON string literal without HTML escaping.
func encodeString(s string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// splice applies non-overlapping edits to data and returns a new slice.
func splice(data []byte, edits []edit) []byte {
	if len(edits) == 0 {
		return data
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	out := make([]byte, 0, len(data))
	at := 0
	for _, e := range edits {
		if e.start < at {
			continue
		}
		out = append(out, data[at:e.start]...)
		out = append(out, encodeString(e.value)...)
		at = e.end
	}
	return append(out, data[at:]...)
}

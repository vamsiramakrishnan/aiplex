package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// object is a JSON object that remembers key order, so merging into a user's
// settings file does not reshuffle the keys they wrote.
type object struct {
	keys []string
	vals map[string]any
}

func newObject() *object { return &object{vals: map[string]any{}} }

func (o *object) get(key string) (any, bool) {
	v, ok := o.vals[key]
	return v, ok
}

func (o *object) set(key string, v any) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

// child returns the nested object at key, creating it when absent. It fails
// when the key holds something that is not an object, rather than replacing it.
func (o *object) child(key string) (*object, error) {
	v, ok := o.vals[key]
	if !ok {
		c := newObject()
		o.set(key, c)
		return c, nil
	}
	c, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("%q exists but is not a JSON object; refusing to replace it", key)
	}
	return c, nil
}

// parseObject parses a JSON document whose top level must be an object. An
// empty document is an empty object.
func parseObject(data []byte) (*object, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return newObject(), nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after the top-level JSON value")
	}
	o, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("top level is not a JSON object")
	}
	return o, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, isDelim := tok.(json.Delim)
	if !isDelim {
		return tok, nil
	}
	switch d {
	case '{':
		o := newObject()
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key := kt.(string)
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			o.set(key, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return o, nil
	case '[':
		arr := []any{}
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return arr, nil
	}
	return nil, fmt.Errorf("unexpected delimiter %v", d)
}

// marshal renders the object with two-space indentation and a trailing newline.
func (o *object) marshal() []byte {
	var buf bytes.Buffer
	writeValue(&buf, o, 0)
	buf.WriteByte('\n')
	return buf.Bytes()
}

func writeString(buf *bytes.Buffer, s string) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	buf.Write(bytes.TrimRight(b.Bytes(), "\n"))
}

func writeValue(buf *bytes.Buffer, v any, depth int) {
	pad := strings.Repeat("  ", depth+1)
	end := strings.Repeat("  ", depth)
	switch t := v.(type) {
	case *object:
		if len(t.keys) == 0 {
			buf.WriteString("{}")
			return
		}
		buf.WriteString("{\n")
		for i, k := range t.keys {
			buf.WriteString(pad)
			writeString(buf, k)
			buf.WriteString(": ")
			writeValue(buf, t.vals[k], depth+1)
			if i < len(t.keys)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(end + "}")
	case []any:
		if len(t) == 0 {
			buf.WriteString("[]")
			return
		}
		buf.WriteString("[\n")
		for i, e := range t {
			buf.WriteString(pad)
			writeValue(buf, e, depth+1)
			if i < len(t)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(end + "]")
	case string:
		writeString(buf, t)
	case json.Number:
		buf.WriteString(t.String())
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case nil:
		buf.WriteString("null")
	default:
		b, _ := json.Marshal(t)
		buf.Write(b)
	}
}

// obj builds an object from alternating key, value arguments.
func obj(kv ...any) *object {
	o := newObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

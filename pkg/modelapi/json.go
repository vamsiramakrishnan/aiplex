package modelapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ValidateJSON rejects malformed/trailing JSON and duplicate object fields at
// every nesting level. Facades must call it before decoding/re-encoding native
// payloads, so normalization cannot erase ambiguous security-relevant fields.
// It does not rewrite the supplied bytes or validate a provider's request schema.
func ValidateJSON(body []byte) error { return validateJSON(body) }

// Native payloads remain byte-for-byte unchanged, but ambiguous duplicate JSON
// fields are rejected before security-relevant fields are interpreted.
func validateJSON(body []byte) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var value func() error
	value = func() error {
		token, e := d.Token()
		if e != nil {
			return e
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return e
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate or invalid JSON field")
				}
				seen[name] = true
				if e = value(); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return fmt.Errorf("invalid JSON object")
			}
		case '[':
			for d.More() {
				if e = value(); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return fmt.Errorf("invalid JSON array")
			}
		default:
			return fmt.Errorf("invalid JSON delimiter")
		}
		return nil
	}
	if e := value(); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

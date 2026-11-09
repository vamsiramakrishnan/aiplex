package nativeapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
)

// Validator performs pure provider-specific validation before a desired action
// is persisted or planned. It must not make network requests or mutate state.
type Validator interface {
	ValidateOperation(Operation) error
}

// ValidatePublicPayload rejects credential material in public resource specs
// and native operation payloads. Credential references remain valid. Provider
// validators must additionally constrain accepted fields and operation schemas.
func ValidatePublicPayload(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > MaxBodyBytes {
		return errors.New("native payload exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	remaining := 100000
	if err := validatePublicValue(decoder, 0, &remaining); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("native payload must contain one JSON value")
	}
	return nil
}

func normalizedKey(key string) string {
	var out strings.Builder
	for _, r := range key {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(unicode.ToLower(r))
		}
	}
	return out.String()
}

var credentialFields = map[string]bool{
	"authorization": true, "clientsecret": true, "accesstoken": true,
	"refreshtoken": true, "idtoken": true, "registrationaccesstoken": true,
	"apikey": true, "password": true, "privatekey": true,
	"userassertion": true, "workloadidentitytoken": true, "bearertoken": true,
	"clientassertion": true, "subjecttoken": true, "assertion": true,
	"credentialvalue": true, "secretvalue": true, "clientsecretjwt": true,
}

func validatePublicValue(d *json.Decoder, depth int, remaining *int) error {
	if depth > 32 || *remaining <= 0 {
		return errors.New("native payload complexity exceeds limit")
	}
	*remaining--
	token, err := d.Token()
	if err != nil {
		return errors.New("native payload is not valid JSON")
	}
	delimiter, nested := token.(json.Delim)
	if !nested {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			keyToken, err := d.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return errors.New("native payload object key is invalid")
			}
			normalized := normalizedKey(key)
			if keys[normalized] {
				return errors.New("native payload contains duplicate object keys")
			}
			keys[normalized] = true
			if credentialFields[normalized] {
				return errors.New("native payload must use credential references instead of credential material")
			}
			if err := validatePublicValue(d, depth+1, remaining); err != nil {
				return err
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim('}') {
			return errors.New("native payload object is invalid")
		}
	case '[':
		for d.More() {
			if err := validatePublicValue(d, depth+1, remaining); err != nil {
				return err
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim(']') {
			return errors.New("native payload array is invalid")
		}
	default:
		return errors.New("native payload contains invalid delimiters")
	}
	return nil
}

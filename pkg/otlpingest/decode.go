// Package otlpingest receives OpenTelemetry (OTLP/HTTP) exports from coding
// harnesses and maps the signals it recognizes to usage ledger records with
// source=reported.
//
// Everything in an export is untrusted: resource and record attributes are
// labels the sender chose. Identity (tenant, principal, client, caller kind)
// is supplied by the caller of this package from the verified token and is
// never read from the payload. Prompts, responses, tool inputs and error
// text are never copied into a record even when a harness exports them.
package otlpingest

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Signal is one OTLP signal.
type Signal string

const (
	SignalLogs    Signal = "logs"
	SignalMetrics Signal = "metrics"
	SignalTraces  Signal = "traces"
)

// Content kinds.
const (
	KindProtobuf = "protobuf"
	KindJSON     = "json"
)

var (
	// ErrTooLarge means the body (as sent or once decompressed) is over its bound.
	ErrTooLarge = errors.New("otlp request is too large")
	// ErrUnsupportedEncoding is a Content-Encoding other than gzip or identity.
	ErrUnsupportedEncoding = errors.New("unsupported content encoding")
	// ErrUnsupportedMediaType is a Content-Type other than protobuf or JSON.
	ErrUnsupportedMediaType = errors.New("unsupported content type")
	// ErrMalformed means the body did not decode as the signal's export request.
	ErrMalformed = errors.New("malformed otlp request")
)

// ContentKind classifies a Content-Type header: application/x-protobuf (and
// application/protobuf) or application/json.
func ContentKind(contentType string) (string, error) {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", ErrUnsupportedMediaType
	}
	switch strings.ToLower(media) {
	case "application/x-protobuf", "application/protobuf":
		return KindProtobuf, nil
	case "application/json":
		return KindJSON, nil
	}
	return "", ErrUnsupportedMediaType
}

// ReadBody reads a request body of at most maxWire bytes as sent and at most
// maxDecoded bytes once decompressed. encoding is the Content-Encoding header:
// "", "identity" or "gzip".
func ReadBody(body io.Reader, encoding string, maxWire, maxDecoded int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxWire+1))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, ErrTooLarge
		}
		return nil, fmt.Errorf("%w: read failed", ErrMalformed)
	}
	if int64(len(raw)) > maxWire {
		return nil, ErrTooLarge
	}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
		if int64(len(raw)) > maxDecoded {
			return nil, ErrTooLarge
		}
		return raw, nil
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("%w: gzip", ErrMalformed)
		}
		defer zr.Close()
		out, err := io.ReadAll(io.LimitReader(zr, maxDecoded+1))
		if err != nil {
			return nil, fmt.Errorf("%w: gzip", ErrMalformed)
		}
		if int64(len(out)) > maxDecoded {
			return nil, ErrTooLarge
		}
		return out, nil
	}
	return nil, ErrUnsupportedEncoding
}

// Unmarshal decodes an export request. Unknown JSON fields are ignored so a
// newer sender does not break the receiver.
func Unmarshal(kind string, raw []byte, msg proto.Message) error {
	switch kind {
	case KindProtobuf:
		if err := proto.Unmarshal(raw, msg); err != nil {
			return ErrMalformed
		}
		return nil
	case KindJSON:
		normalized, err := normalizeJSONIDs(raw)
		if err != nil {
			return ErrMalformed
		}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(normalized, msg); err != nil {
			return ErrMalformed
		}
		return nil
	}
	return ErrUnsupportedMediaType
}

// OTLP/JSON writes traceId, spanId and parentSpanId as hexadecimal strings,
// while the protobuf JSON mapping expects base64 for bytes. Convert those keys
// before handing the document to protojson.
var idKeys = map[string]int{"traceId": 16, "spanId": 8, "parentSpanId": 8, "trace_id": 16, "span_id": 8, "parent_span_id": 8}

func normalizeJSONIDs(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("trailing data")
	}
	walkIDs(doc, 0)
	return json.Marshal(doc)
}

func walkIDs(v any, depth int) {
	if depth > 64 {
		return
	}
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if n, ok := idKeys[k]; ok {
				if s, isString := child.(string); isString {
					if b, err := hex.DecodeString(s); err == nil && len(b) == n {
						x[k] = base64.StdEncoding.EncodeToString(b)
					}
				}
				continue
			}
			walkIDs(child, depth+1)
		}
	case []any:
		for _, child := range x {
			walkIDs(child, depth+1)
		}
	}
}

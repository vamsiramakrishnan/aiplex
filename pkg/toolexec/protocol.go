// Package toolexec provides a bounded stateless JSON HTTP tools/call adapter.
// OAuth registration, identity acquisition and admission remain host-owned.
package toolexec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const StatelessJSON = "stateless-json"

var toolName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var protocolVersion = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// Connection explicitly selects a server that accepts independent JSON calls.
// Session initialization, SSE delivery and delegated OAuth tokens are not
// inferred from an OAuth registration or a stateless endpoint declaration.
type Connection struct {
	ID              string   `json:"id"`
	Endpoint        string   `json:"endpoint"`
	IdentityRef     string   `json:"identity_ref"`
	AllowedTools    []string `json:"allowed_tools"`
	ProtocolVersion string   `json:"protocol_version"`
	Mode            string   `json:"mode"`
}

func (c Connection) Validate() error {
	_, versionErr := time.Parse("2006-01-02", c.ProtocolVersion)
	if !validIdentifier(c.ID) || !validIdentifier(c.IdentityRef) || c.Mode != StatelessJSON || !protocolVersion.MatchString(c.ProtocolVersion) || versionErr != nil || len(c.AllowedTools) == 0 || len(c.AllowedTools) > 1024 {
		return fmt.Errorf("bounded identity, explicit stateless mode, protocol version and tools are required")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || len(c.Endpoint) > 8192 || u.Host == "" || u.User != nil || u.Opaque != "" || u.Fragment != "" || u.RawQuery != "" || strings.ContainsAny(c.Endpoint, "\\\x00\r\n") {
		return fmt.Errorf("pinned tool endpoint must be an unambiguous URL without credentials or query")
	}
	if u.Scheme != "https" {
		ip, e := netip.ParseAddr(u.Hostname())
		if u.Scheme != "http" || (u.Hostname() != "localhost" && (e != nil || !ip.IsLoopback())) {
			return fmt.Errorf("HTTPS is required outside explicitly allowed loopback transports")
		}
	}
	seen := map[string]bool{}
	for _, name := range c.AllowedTools {
		if !toolName.MatchString(name) || seen[name] {
			return fmt.Errorf("tool names must be unique bounded identifiers")
		}
		seen[name] = true
	}
	return nil
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character == 0x7f {
			return false
		}
	}
	return true
}

type Request struct {
	ID        json.RawMessage
	Name      string
	Arguments json.RawMessage
	Body      []byte
	// PayloadDigest covers the complete native request, including its RPC ID.
	PayloadDigest string
	// SemanticDigest excludes only the RPC correlation ID. It does not grant
	// permission to replay dispatch; hosts still use durable attempt claims.
	SemanticDigest string
}

func copyRequest(request Request) Request {
	request.ID = append(json.RawMessage(nil), request.ID...)
	request.Arguments = append(json.RawMessage(nil), request.Arguments...)
	request.Body = append([]byte(nil), request.Body...)
	return request
}

func ParseRequest(body []byte, allowed []string) (Request, error) {
	if err := validateJSON(body); err != nil {
		return Request{}, err
	}
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments,omitempty"`
		} `json:"params"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return Request{}, fmt.Errorf("a single tools/call request is required")
	}
	if envelope.JSONRPC != "2.0" || envelope.Method != "tools/call" || !toolName.MatchString(envelope.Params.Name) {
		return Request{}, fmt.Errorf("only JSON-RPC 2.0 tools/call is supported")
	}
	if _, err := rpcID(envelope.ID); err != nil {
		return Request{}, err
	}
	found := false
	for _, name := range allowed {
		if name == envelope.Params.Name {
			found = true
			break
		}
	}
	if !found {
		return Request{}, fmt.Errorf("tool is not allowed by this connection")
	}
	arguments := envelope.Params.Arguments
	if len(arguments) > 0 && (bytes.TrimSpace(arguments)[0] != '{' || bytes.Equal(bytes.TrimSpace(arguments), []byte("null"))) {
		return Request{}, fmt.Errorf("tool arguments must be an object")
	}
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	canonical, err := json.Marshal(struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}{envelope.Params.Name, arguments})
	if err != nil {
		return Request{}, fmt.Errorf("tool arguments cannot be encoded")
	}
	rawDigest, semanticDigest := sha256.Sum256(body), sha256.Sum256(canonical)
	return Request{ID: append(json.RawMessage(nil), envelope.ID...), Name: envelope.Params.Name, Arguments: append(json.RawMessage(nil), arguments...), Body: append([]byte(nil), body...), PayloadDigest: hex.EncodeToString(rawDigest[:]), SemanticDigest: hex.EncodeToString(semanticDigest[:])}, nil
}

// ValidateResponse establishes only that the dispatched RPC completed. It does
// not infer successful business work from a result or error payload.
func ValidateResponse(body []byte, requestID json.RawMessage) error {
	if err := validateJSON(body); err != nil {
		return err
	}
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.JSONRPC != "2.0" {
		return fmt.Errorf("response is not JSON-RPC 2.0")
	}
	want, err := rpcID(requestID)
	if err != nil {
		return err
	}
	got, err := rpcID(envelope.ID)
	if err != nil || got != want {
		return fmt.Errorf("response RPC ID does not identify this request")
	}
	if (len(envelope.Result) == 0) == (len(envelope.Error) == 0) {
		return fmt.Errorf("response must contain exactly one result or error")
	}
	if len(envelope.Error) > 0 {
		var rpcError struct {
			Code    *int64  `json:"code"`
			Message *string `json:"message"`
		}
		if json.Unmarshal(envelope.Error, &rpcError) != nil || rpcError.Code == nil || rpcError.Message == nil {
			return fmt.Errorf("invalid JSON-RPC error")
		}
	}
	return nil
}

func rpcID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || len(raw) > 4096 {
		return "", fmt.Errorf("bounded non-null RPC ID is required")
	}
	var id any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&id); err != nil {
		return "", fmt.Errorf("invalid RPC ID")
	}
	switch value := id.(type) {
	case string:
		if len(value) > 1024 {
			return "", fmt.Errorf("RPC ID is too long")
		}
		return "string:" + value, nil
	case json.Number:
		number, err := strconv.ParseInt(string(value), 10, 64)
		if err != nil {
			return "", fmt.Errorf("integer RPC ID is required")
		}
		return "number:" + strconv.FormatInt(number, 10), nil
	default:
		return "", fmt.Errorf("string or integer RPC ID is required")
	}
}

// Reject ambiguity recursively before interpreting names or forwarding bytes.
func validateJSON(body []byte) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || !utf8.Valid(body) || trimmed[0] != '{' {
		return fmt.Errorf("one JSON object is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("JSON nesting exceeds its bound")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate JSON field")
				}
				seen[name] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return fmt.Errorf("invalid JSON object")
			}
		case '[':
			for decoder.More() {
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return fmt.Errorf("invalid JSON array")
			}
		default:
			return fmt.Errorf("invalid JSON delimiter")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

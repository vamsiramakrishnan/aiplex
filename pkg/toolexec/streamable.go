package toolexec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// validateStreamResponse accepts a bounded, finite SSE response for one tools/call.
// It does not resume disconnected streams or accept server-initiated RPCs. The
// complete native bytes are released only after the terminal call is acknowledged.
func validateStreamResponse(body []byte, wanted json.RawMessage) error {
	if len(body) == 0 {
		return fmt.Errorf("missing tool stream")
	}
	normalized := bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	if !bytes.HasSuffix(normalized, []byte("\n\n")) {
		return fmt.Errorf("incomplete SSE frame")
	}
	terminal := false
	for _, frame := range bytes.Split(normalized, []byte("\n\n")) {
		if len(frame) == 0 {
			continue
		}
		var data []byte
		event := ""
		for _, line := range bytes.Split(frame, []byte("\n")) {
			if len(line) == 0 || line[0] == ':' {
				continue
			}
			key, value, has := bytes.Cut(line, []byte(":"))
			if !has {
				value = nil
			}
			value = bytes.TrimPrefix(value, []byte(" "))
			switch string(key) {
			case "data":
				if len(data) > 0 {
					data = append(data, '\n')
				}
				data = append(data, value...)
			case "event":
				event = string(value)
			case "id":
				if bytes.ContainsAny(value, "\x00\r\n") {
					return fmt.Errorf("invalid SSE identifier")
				}
			case "retry": // A reconnect hint never authorizes replay.
			default:
				return fmt.Errorf("unsupported SSE field")
			}
		}
		if len(data) == 0 {
			continue
		}
		if terminal {
			return fmt.Errorf("native data after terminal tool response")
		}
		if event != "" && event != "message" {
			return fmt.Errorf("unsupported MCP SSE event")
		}
		if validateJSON(data) != nil {
			return fmt.Errorf("invalid MCP SSE JSON")
		}
		var message map[string]json.RawMessage
		if json.Unmarshal(data, &message) != nil {
			return fmt.Errorf("invalid MCP SSE object")
		}
		if _, response := message["id"]; response {
			if ValidateResponse(data, wanted) != nil {
				return fmt.Errorf("tool SSE response does not match call")
			}
			terminal = true
			continue
		}
		var version, method string
		_ = json.Unmarshal(message["jsonrpc"], &version)
		_ = json.Unmarshal(message["method"], &method)
		if version != "2.0" || (method != "notifications/progress" && method != "notifications/message") || !strings.HasPrefix(method, "notifications/") {
			return fmt.Errorf("unsupported server notification")
		}
		for key := range message {
			if key != "jsonrpc" && key != "method" && key != "params" {
				return fmt.Errorf("invalid server notification")
			}
		}
	}
	if !terminal {
		return fmt.Errorf("tool SSE stream has no terminal response")
	}
	return nil
}

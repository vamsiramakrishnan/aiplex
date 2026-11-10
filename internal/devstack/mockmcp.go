package devstack

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// MockMCP is a tiny stateless MCP server (Streamable HTTP, JSON responses).
// It implements initialize, notifications/initialized, ping, tools/list and
// tools/call for three tools:
//
//   - echo   (read-only)  returns its "text" argument
//   - add    (read-only)  returns a+b
//   - reset_counter (destructive, not read-only) resets a call counter
//
// When APIKeyHeader is set, every request must carry that header with
// APIKeyValue, otherwise the server answers 401 — used to prove that a
// tool connection's api_key credential reaches the upstream.
type MockMCP struct {
	APIKeyHeader string
	APIKeyValue  string

	calls atomic.Int64
	mu    sync.Mutex
	seen  []string // methods, in order
}

// SupportedProtocolVersions are the MCP revisions this mock speaks.
var SupportedProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

// Methods returns the JSON-RPC methods received, in order.
func (m *MockMCP) Methods() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.seen...)
}

// MockTools are the tool names the mock serves.
var MockTools = []string{"echo", "add", "reset_counter"}

func (m *MockMCP) toolsList() []map[string]any {
	return []map[string]any{
		{"name": "echo", "description": "Echo the given text back.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}}, "annotations": map[string]any{"readOnlyHint": true}},
		{"name": "add", "description": "Add two numbers.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "number"}, "b": map[string]any{"type": "number"}}, "required": []string{"a", "b"}}, "annotations": map[string]any{"readOnlyHint": true}},
		{"name": "reset_counter", "description": "Reset the server's call counter.", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": false, "destructiveHint": true}},
	}
}

func rpcResult(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func rpcError(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}}
}

func (m *MockMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if m.APIKeyHeader != "" && r.Header.Get(m.APIKeyHeader) != m.APIKeyValue {
		writeJSON(w, 401, rpcError(nil, -32001, "missing or invalid API key"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, "read error", 400)
		return
	}
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		writeJSON(w, 400, rpcError(nil, -32700, "parse error"))
		return
	}
	m.mu.Lock()
	m.seen = append(m.seen, msg.Method)
	m.mu.Unlock()
	if len(msg.ID) == 0 || string(msg.ID) == "null" {
		// Notifications (for example notifications/initialized) get 202.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		version := SupportedProtocolVersions[0]
		for _, v := range SupportedProtocolVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		writeJSON(w, 200, rpcResult(msg.ID, map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": "aiplex-mock-mcp", "version": "1.0.0"}}))
	case "ping":
		writeJSON(w, 200, rpcResult(msg.ID, map[string]any{}))
	case "tools/list":
		writeJSON(w, 200, rpcResult(msg.ID, map[string]any{"tools": m.toolsList()}))
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			writeJSON(w, 200, rpcError(msg.ID, -32602, "invalid params"))
			return
		}
		n := m.calls.Add(1)
		var text string
		switch p.Name {
		case "echo":
			text = fmt.Sprint(p.Arguments["text"])
		case "add":
			a, _ := toFloat(p.Arguments["a"])
			b, _ := toFloat(p.Arguments["b"])
			text = strconv.FormatFloat(a+b, 'f', -1, 64)
		case "reset_counter":
			m.calls.Store(0)
			text = "counter reset after " + strconv.FormatInt(n, 10) + " calls"
		default:
			writeJSON(w, 200, rpcError(msg.ID, -32602, "unknown tool "+p.Name))
			return
		}
		writeJSON(w, 200, rpcResult(msg.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": false}))
	default:
		writeJSON(w, 200, rpcError(msg.ID, -32601, "method not found: "+msg.Method))
	}
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

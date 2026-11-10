//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// mcpClient is a minimal MCP client over Streamable HTTP that follows the
// lifecycle the specification defines: initialize, notifications/initialized,
// then requests carrying MCP-Protocol-Version (and Mcp-Session-Id when the
// server assigned one).
type mcpClient struct {
	e        *env
	path     string
	version  string
	session  string
	nextID   int
	extraHdr []string
}

type rpcResponse struct {
	Status int
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	} `json:"error"`
	Raw string
}

func (c *mcpClient) send(method string, params any, notification bool) rpcResponse {
	c.e.t.Helper()
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	if !notification {
		c.nextID++
		msg["id"] = c.nextID
	}
	body, _ := json.Marshal(msg)
	headers := append(c.e.auth(), "Accept", "application/json, text/event-stream", "Content-Type", "application/json")
	if c.version != "" {
		headers = append(headers, "MCP-Protocol-Version", c.version)
	}
	if c.session != "" {
		headers = append(headers, "Mcp-Session-Id", c.session)
	}
	headers = append(headers, c.extraHdr...)
	r := c.e.do("POST", c.path, string(body), headers...)
	out := rpcResponse{Status: r.Status, Raw: r.Body}
	if s := r.Header.Get("Mcp-Session-Id"); s != "" {
		c.session = s
	}
	payload := r.Body
	if strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") {
		data := sseData(r.Body)
		if len(data) > 0 {
			payload = data[len(data)-1]
		}
	}
	if !notification && r.Status == 200 {
		if err := json.Unmarshal([]byte(payload), &out); err != nil {
			c.e.t.Fatalf("%s: response is not JSON-RPC: %s", method, r.Body)
		}
	}
	return out
}

func (c *mcpClient) initialize(version string) rpcResponse {
	c.e.t.Helper()
	r := c.send("initialize", map[string]any{"protocolVersion": version, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "aiplex-e2e", "version": "1.0"}}, false)
	if r.Status != 200 || r.Error != nil {
		c.e.t.Fatalf("initialize: %d %s", r.Status, r.Raw)
	}
	var result struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      map[string]any `json:"serverInfo"`
	}
	_ = json.Unmarshal(r.Result, &result)
	if result.ProtocolVersion == "" || result.Capabilities["tools"] == nil || result.ServerInfo["name"] == nil {
		c.e.t.Fatalf("initialize result lacks protocolVersion, tools capability or serverInfo: %s", r.Raw)
	}
	c.version = result.ProtocolVersion
	n := c.send("notifications/initialized", nil, true)
	if n.Status != 202 && n.Status != 200 && n.Status != 204 {
		c.e.t.Fatalf("notifications/initialized: %d %s", n.Status, n.Raw)
	}
	return r
}

func (c *mcpClient) tools() []string {
	c.e.t.Helper()
	r := c.send("tools/list", map[string]any{}, false)
	if r.Status != 200 || r.Error != nil {
		c.e.t.Fatalf("tools/list: %d %s", r.Status, r.Raw)
	}
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(r.Result, &result)
	var names []string
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func (c *mcpClient) call(name string, args map[string]any) rpcResponse {
	c.e.t.Helper()
	return c.send("tools/call", map[string]any{"name": name, "arguments": args}, false)
}

func callText(t *testing.T, r rpcResponse) string {
	t.Helper()
	if r.Status != 200 || r.Error != nil {
		t.Fatalf("tools/call failed: %d %s", r.Status, r.Raw)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	_ = json.Unmarshal(r.Result, &result)
	if len(result.Content) == 0 {
		t.Fatalf("tools/call result has no content: %s", r.Raw)
	}
	return result.Content[0].Text
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// 4. MCP: tool connections with none and api_key auth, the aggregate
// endpoint lifecycle, allow/deny and read-only policy.
func TestMCPToolConnectionsThroughAccessPoint(t *testing.T) {
	keyed := &devstack.MockMCP{APIKeyHeader: "X-Api-Key", APIKeyValue: "mcp-upstream-key"}
	srv, keyedURL, err := devstack.Listen(keyed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	e := start(t, func(o *devstack.Options) {
		o.ExtraOrigins = append(o.ExtraOrigins, keyedURL)
		o.Env = append(o.Env, "AIPLEX_TOOL_SECRET_MCP_KEY=mcp-upstream-key")
	})

	open := devstack.ToolConnectionSpec("open", e.stack.MCPURL, devstack.MockTools, nil, nil)
	e.mustCLI("resources", "put", "tool-connection", "open", writeFile(t, "open.json", open), "0")
	keyedSpec := devstack.ToolConnectionSpec("keyed", keyedURL, devstack.MockTools, nil, nil)
	e.mustCLI("resources", "put", "tool-connection", "keyed", writeFile(t, "keyed.json", keyedSpec), "0")

	// The keyed upstream refuses a call without its key.
	r := e.mustCLI("tools", "set-auth", "keyed", "--auth-type", "api_key", "--auth-in", "header", "--auth-name", "X-Api-Key", "--secret-ref", "mcp-key", "--read-only")
	contains(t, r.Out, "api_key", "set-auth output")
	r = e.mustCLI("tools", "set-auth", "open", "--deny", "reset_counter")

	ap := map[string]any{"id": "tools", "caller_kinds": []string{"user"}, "subjects": map[string]any{},
		"tools": []map[string]any{{"connection_ref": "open"}, {"connection_ref": "keyed"}}}
	e.mustCLI("access-points", "apply", "-f", writeFile(t, "ap.json", ap))

	t.Run("aggregate lifecycle", func(t *testing.T) {
		c := &mcpClient{e: e, path: "/ap/tools/mcp"}
		c.initialize("2025-06-18")
		if c.version != "2025-06-18" {
			t.Fatalf("negotiated %q", c.version)
		}
		names := c.tools()
		for _, want := range []string{"open__echo", "open__add", "keyed__echo", "keyed__add"} {
			if !has(names, want) {
				t.Fatalf("tools/list lacks %s: %v", want, names)
			}
		}
		if has(names, "open__reset_counter") {
			t.Fatalf("denied tool listed: %v", names)
		}
		if has(names, "keyed__reset_counter") {
			t.Fatalf("read-only connection listed a non-read-only tool: %v", names)
		}
		if got := callText(t, c.call("open__echo", map[string]any{"text": "ping"})); got != "ping" {
			t.Fatalf("echo = %q", got)
		}
		if got := callText(t, c.call("keyed__add", map[string]any{"a": 2, "b": 3})); got != "5" {
			t.Fatalf("add through api_key connection = %q", got)
		}
		// Deny wins.
		d := c.call("open__reset_counter", map[string]any{})
		if d.Error == nil || d.Error.Code != -32001 {
			t.Fatalf("denied tool call: %s", d.Raw)
		}
		// Read-only.
		d = c.call("keyed__reset_counter", map[string]any{})
		if d.Error == nil {
			t.Fatalf("read-only connection ran a destructive tool: %s", d.Raw)
		}
	})

	t.Run("unsupported version header", func(t *testing.T) {
		c := &mcpClient{e: e, path: "/ap/tools/mcp", version: "1999-01-01"}
		r := c.send("tools/list", map[string]any{}, false)
		if r.Status != 400 || !strings.Contains(r.Raw, "supported") {
			t.Fatalf("unsupported MCP-Protocol-Version: %d %s", r.Status, r.Raw)
		}
	})

	t.Run("single connection endpoint", func(t *testing.T) {
		c := &mcpClient{e: e, path: "/ap/tools/mcp/open"}
		c.initialize("2025-11-25")
		names := c.tools()
		if !has(names, "echo") || has(names, "reset_counter") {
			t.Fatalf("per-connection tools/list: %v", names)
		}
		if got := callText(t, c.call("add", map[string]any{"a": 1, "b": 1})); got != "2" {
			t.Fatalf("add = %q", got)
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		r := e.do("POST", "/ap/tools/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "Content-Type", "application/json")
		if r.Status != 401 {
			t.Fatalf("no token: %d %s", r.Status, r.Body)
		}
	})

	t.Run("usage records tool calls", func(t *testing.T) {
		r := e.mustCLI("usage", "records", "--plane", "mcplex", "-o", "json")
		for _, tool := range []string{"echo", "add"} {
			contains(t, r.Out, fmt.Sprintf(`"tool_name": %q`, tool), "mcplex usage")
		}
	})
}

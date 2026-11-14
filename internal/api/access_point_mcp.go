package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/pkg/errorcode"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
)

// MCP Streamable HTTP, stateless subset. The access point is an
// initialize-era ("legacy" in the 2026-07-28 terminology) server: it answers
// initialize, notifications, ping, tools/list and tools/call over POST with
// JSON responses, never mints an Mcp-Session-Id and answers GET/DELETE with
// 405. A dual-era client that first tries a newer revision receives a 400
// whose body is not a modern MCP error, which is the documented signal to
// fall back to initialize.
var accessPointMCPVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

// accessPointMCPDefaultVersion is assumed when MCP-Protocol-Version is
// absent, as the 2025-06-18 and later transports allow.
const accessPointMCPDefaultVersion = "2025-03-26"

const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
	rpcQuotaExceeded  = -32003
)

const toolNamespaceSeparator = "__"

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func writeRPCResult(w http.ResponseWriter, status int, id json.RawMessage, result any) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeRPCError answers with a JSON-RPC error that AIPlex itself produced:
// rpcCode is the JSON-RPC code in the body, refusal AIPlex's own code for the
// response header X-Aiplex-Error-Code. The endpoint never relays an upstream
// error body, so every JSON-RPC error written here is AIPlex's decision.
func writeRPCError(w http.ResponseWriter, status int, id json.RawMessage, rpcCode int, refusal, message string, data map[string]any) {
	errorcode.Set(w.Header(), refusal)
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	e := map[string]any{"code": rpcCode, "message": message}
	if len(data) > 0 {
		e["data"] = data
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": e})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeRPCMember answers with one raw JSON-RPC result or error member. An
// error member is a governed denial produced by AIPlex: refusal is the code the
// managed path attached to it.
func writeRPCMember(w http.ResponseWriter, id json.RawMessage, member, refusal string, value json.RawMessage) {
	if member == "error" {
		errorcode.Set(w.Header(), refusal)
	}
	body, _ := json.Marshal(map[string]json.RawMessage{"jsonrpc": json.RawMessage(`"2.0"`), "id": id, member: value})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *ConnectedHandler) publicBase() string { return strings.TrimSuffix(h.PublicURL, "/") }

// accessPointResourcePath is the MCP resource path of an access point.
func accessPointResourcePath(apID, connection string) string {
	path := "/ap/" + apID + "/mcp"
	if connection != "" {
		path += "/" + connection
	}
	return path
}

// protectedResourceMetadataURL is the RFC 9728 well-known URL: the
// well-known segment is inserted between host and the resource path.
func (h *ConnectedHandler) protectedResourceMetadataURL(apID, connection string) string {
	if h.PublicURL == "" {
		return ""
	}
	return h.publicBase() + "/.well-known/oauth-protected-resource" + accessPointResourcePath(apID, connection)
}

func (h *ConnectedHandler) originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	for _, allowed := range h.AccessPoint.AllowedOrigins {
		if allowed == "*" || allowed == origin {
			return true
		}
	}
	if h.PublicURL != "" {
		if public, err := url.Parse(h.PublicURL); err == nil && public.Scheme+"://"+public.Host == origin {
			return true
		}
	}
	return false
}

func (h *ConnectedHandler) mcpGateErrorWriter(apID, connection string, id *json.RawMessage) accessPointErrorWriter {
	return func(w http.ResponseWriter, r *http.Request, status int, code, message string) {
		if status == http.StatusUnauthorized {
			challenge := `Bearer`
			if metadata := h.protectedResourceMetadataURL(apID, connection); metadata != "" {
				challenge += ` resource_metadata="` + metadata + `"`
			}
			w.Header().Set("WWW-Authenticate", challenge)
		}
		rpcCode := rpcInvalidRequest
		if status == http.StatusForbidden {
			rpcCode = toolexec.RPCForbidden
		}
		writeRPCError(w, status, *id, rpcCode, code, message, map[string]any{"reason": strings.ToLower(code)})
	}
}

// AccessPointMCP serves /ap/{id}/mcp (aggregate) and /ap/{id}/mcp/{connection}.
func (h *ConnectedHandler) AccessPointMCP(w http.ResponseWriter, r *http.Request) {
	apID, connection := chi.URLParam(r, "apid"), chi.URLParam(r, "connection")
	if !accessPointIDPattern.MatchString(apID) || (connection != "" && !accessPointConnectionPattern.MatchString(connection)) {
		errorcode.Set(w.Header(), "NOT_FOUND")
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		// No server-initiated stream and no sessions to delete.
		w.Header().Set("Allow", http.MethodPost)
		errorcode.Refuse(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "this MCP endpoint accepts POST only")
		return
	}
	null := json.RawMessage("null")
	if !h.originAllowed(r.Header.Get("Origin")) {
		writeRPCError(w, http.StatusForbidden, null, rpcInvalidRequest, "ORIGIN_NOT_ALLOWED", "origin not allowed", nil)
		return
	}
	if GetWIFIdentity(r.Context()) == nil {
		h.mcpGateErrorWriter(apID, connection, &null)(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "verified identity required")
		return
	}
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		writeRPCError(w, http.StatusUnsupportedMediaType, null, rpcInvalidRequest, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json", nil)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		writeRPCError(w, http.StatusRequestEntityTooLarge, null, rpcInvalidRequest, "REQUEST_TOO_LARGE", "request exceeds 1 MiB", nil)
		return
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		writeRPCError(w, http.StatusBadRequest, null, rpcInvalidRequest, "INVALID_REQUEST", "JSON-RPC batches are not supported", nil)
		return
	}
	var message rpcEnvelope
	if json.Unmarshal(raw, &message) != nil {
		writeRPCError(w, http.StatusBadRequest, null, rpcParseError, "INVALID_REQUEST", "invalid JSON", nil)
		return
	}
	id := message.ID
	if len(id) > 0 && !validRPCID(id) {
		writeRPCError(w, http.StatusBadRequest, null, rpcInvalidRequest, "INVALID_REQUEST", "id must be a string or integer", nil)
		return
	}
	if message.JSONRPC != "2.0" || message.Method == "" {
		writeRPCError(w, http.StatusBadRequest, id, rpcInvalidRequest, "INVALID_REQUEST", "a JSON-RPC 2.0 request or notification is required", nil)
		return
	}
	version := r.Header.Get("MCP-Protocol-Version")
	if version == "" {
		version = accessPointMCPDefaultVersion
	}
	if !accessPointMCPSupported(version) {
		writeRPCError(w, http.StatusBadRequest, id, rpcInvalidRequest, "UNSUPPORTED_PROTOCOL_VERSION", "unsupported MCP-Protocol-Version "+strconv.Quote(version)+"; supported: "+strings.Join(accessPointMCPVersions, ", "), map[string]any{"supported": accessPointMCPVersions})
		return
	}
	_, ap, call, ok := h.accessPointGate(w, r, h.mcpGateErrorWriter(apID, connection, &id))
	if !ok {
		return
	}
	if !h.moduleEnabled("mcplex") {
		writeRPCError(w, http.StatusNotFound, id, rpcInvalidRequest, "MODULE_DISABLED", "tool endpoints are disabled", nil)
		return
	}
	if connection != "" && ap.tool(connection) == nil {
		writeRPCError(w, http.StatusNotFound, id, rpcInvalidRequest, "NOT_FOUND", "connection is not exposed by this access point", nil)
		return
	}
	if len(id) == 0 {
		// Notifications (including notifications/initialized) are accepted.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch message.Method {
	case "initialize":
		h.mcpInitialize(w, id, message.Params, ap, connection)
	case "ping":
		writeRPCResult(w, http.StatusOK, id, map[string]any{})
	case "tools/list":
		h.mcpToolsList(w, r, call, ap, connection, id, message.Params)
	case "tools/call":
		h.mcpToolsCall(w, r, call, ap, connection, id, message.Params)
	default:
		writeRPCError(w, http.StatusOK, id, rpcMethodNotFound, "METHOD_NOT_FOUND", "method not found: "+message.Method, nil)
	}
}

func validRPCID(id json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(id))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return false
	}
	switch v := value.(type) {
	case string:
		return len(v) <= 1024
	case json.Number:
		_, err := strconv.ParseInt(string(v), 10, 64)
		return err == nil
	}
	return false
}

func accessPointMCPSupported(version string) bool {
	for _, v := range accessPointMCPVersions {
		if v == version {
			return true
		}
	}
	return false
}

func (ap AccessPoint) tool(connection string) *AccessPointTool {
	for i := range ap.Tools {
		if ap.Tools[i].ConnectionRef == connection {
			return &ap.Tools[i]
		}
	}
	return nil
}

func (t AccessPointTool) exposes(name string) bool {
	if t.Expose == nil {
		return true
	}
	return toolexec.ToolPolicy{Allow: t.Expose}.Permits(name)
}

func (h *ConnectedHandler) mcpInitialize(w http.ResponseWriter, id, params json.RawMessage, ap AccessPoint, connection string) {
	var input struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &input)
	version := accessPointMCPVersions[0]
	if accessPointMCPSupported(input.ProtocolVersion) {
		version = input.ProtocolVersion
	}
	name := "aiplex-access-point/" + ap.ID
	if connection != "" {
		name += "/" + connection
	}
	info := map[string]any{"name": name, "version": "1.0.0"}
	if ap.DisplayName != "" {
		info["title"] = ap.DisplayName
	}
	writeRPCResult(w, http.StatusOK, id, map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": info})
}

// bufferedResponse captures a managed handler's bounded response.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}
func (b *bufferedResponse) Flush() {}

// invokeManagedTool runs one canonical JSON-RPC message through the existing
// managed tool path (connection policy, caller scope, quota, upstream
// credential, ledger) for one connection.
func (h *ConnectedHandler) invokeManagedTool(r *http.Request, call *accessPointCall, connection string, body []byte) *bufferedResponse {
	inner := delegateRequest(r, call, "/api/v1/mcp/connections/"+connection+"/invoke", connection, body)
	for _, name := range []string{"MCP-Protocol-Version", "Mcp-Session-Id", "Last-Event-ID", "Accept"} {
		inner.Header.Del(name)
	}
	inner.Header.Set("Content-Type", "application/json")
	inner.Method = http.MethodPost
	out := &bufferedResponse{header: http.Header{}}
	h.NativeTool(out, inner)
	if out.status == 0 {
		out.status = http.StatusOK
	}
	return out
}

type listedEntries struct {
	Tools      []json.RawMessage `json:"tools"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

// refusalCode is the code the managed path attached to a refusal it produced.
// Every non-2xx answer of the managed tool path is AIPlex's own (it never
// relays an upstream error body), so a refusal that lacks a code is marked
// TOOL_CALL_FAILED rather than left looking like a provider's error.
func (b *bufferedResponse) refusalCode() string {
	if code := b.header.Get(errorcode.Header); code != "" {
		return code
	}
	return "TOOL_CALL_FAILED"
}

// managedList returns a connection's governed tool entries, or an error that
// is safe to show; upstream error details are not returned.
func (h *ConnectedHandler) managedList(r *http.Request, call *accessPointCall, connection string, id json.RawMessage, cursor *string) (listedEntries, json.RawMessage, string, error) {
	params := map[string]any{}
	if cursor != nil {
		params["cursor"] = *cursor
	}
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/list"}
	if len(params) > 0 {
		request["params"] = params
	}
	body, _ := json.Marshal(request)
	out := h.invokeManagedTool(r, call, connection, body)
	var envelope struct {
		Result *listedEntries  `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	media, _, _ := mime.ParseMediaType(out.header.Get("Content-Type"))
	if media != "application/json" || json.Unmarshal(out.body.Bytes(), &envelope) != nil {
		return listedEntries{}, nil, "", fmt.Errorf("tool listing unavailable")
	}
	if len(envelope.Error) > 0 {
		return listedEntries{}, envelope.Error, out.refusalCode(), fmt.Errorf("tool listing denied")
	}
	if out.status != http.StatusOK || envelope.Result == nil {
		return listedEntries{}, nil, "", fmt.Errorf("tool listing unavailable")
	}
	return *envelope.Result, nil, "", nil
}

func renameTool(entry json.RawMessage, connection string, tool AccessPointTool) (json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	var name string
	if json.Unmarshal(entry, &fields) != nil || json.Unmarshal(fields["name"], &name) != nil || !tool.exposes(name) {
		return nil, false
	}
	if connection == "" {
		return entry, true
	}
	fields["name"], _ = json.Marshal(connection + toolNamespaceSeparator + name)
	renamed, err := json.Marshal(fields)
	return renamed, err == nil
}

func (h *ConnectedHandler) mcpToolsList(w http.ResponseWriter, r *http.Request, call *accessPointCall, ap AccessPoint, connection string, id, rawParams json.RawMessage) {
	var params struct {
		Cursor *string         `json:"cursor"`
		Meta   json.RawMessage `json:"_meta"`
	}
	if len(rawParams) > 0 && string(rawParams) != "null" {
		decoder := json.NewDecoder(bytes.NewReader(rawParams))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&params) != nil {
			writeRPCError(w, http.StatusOK, id, rpcInvalidParams, "INVALID_PARAMS", "tools/list accepts only cursor", nil)
			return
		}
	}
	if connection != "" {
		listed, rpcErr, refusal, err := h.managedList(r, call, connection, id, params.Cursor)
		if len(rpcErr) > 0 {
			writeRPCMember(w, id, "error", refusal, rpcErr)
			return
		}
		if err != nil {
			writeRPCError(w, http.StatusOK, id, rpcInternalError, "TOOL_LISTING_UNAVAILABLE", err.Error(), nil)
			return
		}
		tool := ap.tool(connection)
		out := listedEntries{Tools: []json.RawMessage{}, NextCursor: listed.NextCursor}
		for _, entry := range listed.Tools {
			if kept, ok := renameTool(entry, "", *tool); ok {
				out.Tools = append(out.Tools, kept)
			}
		}
		writeRPCResult(w, http.StatusOK, id, out)
		return
	}
	if params.Cursor != nil {
		writeRPCError(w, http.StatusOK, id, rpcInvalidParams, "INVALID_PARAMS", "the aggregate endpoint returns a single page; cursors are not supported", nil)
		return
	}
	// A connection that fails, is denied or is unavailable is omitted; its
	// upstream error is never relayed through the aggregate. Pagination is
	// followed within bounds; see aggregateTools.
	tools, truncated := h.aggregateTools(r, call, ap)
	result := map[string]any{"tools": tools}
	if len(truncated) > 0 {
		result["_meta"] = map[string]any{aggregateTruncationMeta: truncated}
	}
	writeRPCResult(w, http.StatusOK, id, result)
}

func (h *ConnectedHandler) mcpToolsCall(w http.ResponseWriter, r *http.Request, call *accessPointCall, ap AccessPoint, connection string, id, rawParams json.RawMessage) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      json.RawMessage `json:"_meta"`
	}
	decoder := json.NewDecoder(bytes.NewReader(rawParams))
	decoder.DisallowUnknownFields()
	if len(rawParams) == 0 || decoder.Decode(&params) != nil || params.Name == "" {
		writeRPCError(w, http.StatusOK, id, rpcInvalidParams, "INVALID_PARAMS", "tools/call requires params.name and optional object arguments", nil)
		return
	}
	target, name := connection, params.Name
	if connection == "" {
		before, after, found := strings.Cut(params.Name, toolNamespaceSeparator)
		if !found || before == "" || after == "" {
			writeRPCError(w, http.StatusOK, id, rpcInvalidParams, "INVALID_PARAMS", "unknown tool: aggregate tool names are {connection}__{tool}", nil)
			return
		}
		target, name = before, after
	}
	tool := ap.tool(target)
	if tool == nil || !tool.exposes(name) {
		writeRPCError(w, http.StatusOK, id, rpcInvalidParams, "INVALID_PARAMS", "unknown tool: "+params.Name, nil)
		return
	}
	if ap.QoS != nil && h.Quota == nil {
		writeRPCError(w, http.StatusOK, id, rpcInternalError, "QUOTA_UNAVAILABLE", "access point QoS requires quota enforcement, which is not configured", map[string]any{"reason": "quota_unavailable"})
		return
	}
	arguments := params.Arguments
	if len(bytes.TrimSpace(arguments)) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments}})
	if err != nil {
		writeRPCError(w, http.StatusOK, id, rpcInvalidParams, "INVALID_PARAMS", "tool arguments must be a JSON object", nil)
		return
	}
	out := h.invokeManagedTool(r, call, target, body)
	if retry := out.header.Get("Retry-After"); retry != "" {
		w.Header().Set("Retry-After", retry)
	}
	if attempt := out.header.Get("X-Aiplex-Attempt-ID"); attempt != "" {
		w.Header().Set("X-Aiplex-Attempt-ID", attempt)
	}
	media, _, _ := mime.ParseMediaType(out.header.Get("Content-Type"))
	if out.status >= 200 && out.status < 300 {
		// The managed path validated that the response answers this id.
		w.Header().Set("Content-Type", out.header.Get("Content-Type"))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(out.status)
		_, _ = w.Write(out.body.Bytes())
		return
	}
	if media == "application/json" {
		var envelope struct {
			ID    json.RawMessage `json:"id"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(out.body.Bytes(), &envelope) == nil && len(envelope.Error) > 0 && bytes.Equal(envelope.ID, id) {
			// Governed denials keep their JSON-RPC error and request id.
			writeRPCMember(w, id, "error", out.refusalCode(), envelope.Error)
			return
		}
	}
	code := rpcInternalError
	switch {
	case out.status == http.StatusTooManyRequests:
		code = rpcQuotaExceeded
	case out.status == http.StatusForbidden:
		code = toolexec.RPCForbidden
	case out.status == http.StatusBadRequest || out.status == http.StatusNotFound:
		code = rpcInvalidParams
	}
	// Bodies here are server-defined refusals (the managed path never relays
	// an upstream error body); provider details are not exposed.
	message, data := "", map[string]any{"http_status": out.status}
	if reason, text, ok := aiplexError(out.header.Get("Content-Type"), out.body.Bytes()); ok {
		message = text
		if reason != "" {
			data["reason"] = strings.ToLower(reason)
		}
	}
	if len(message) > 512 || message == "" {
		message = http.StatusText(out.status)
	}
	writeRPCError(w, http.StatusOK, id, code, out.refusalCode(), message, data)
}

package toolexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/errorcode"
)

// ListedTool is the authorization-relevant view of one upstream tools/list
// entry. ReadOnlyHint is nil when the server did not declare the annotation.
type ListedTool struct {
	Name         string
	ReadOnlyHint *bool
	// The other MCP annotation hints; nil when the server did not declare them.
	DestructiveHint, IdempotentHint, OpenWorldHint *bool
}

// ListOptions governs tools/list. Listing is discovery: it is authorized and
// filtered but not quota-metered, and it never opens an MCP session.
type ListOptions struct {
	// Authorize runs before any credential is acquired.
	Authorize func(context.Context, contracts.RequestContext, Connection) error
	// Observe receives every well-formed upstream entry before filtering, so
	// the host can record gateway-observed annotations.
	Observe func(context.Context, contracts.RequestContext, []ListedTool)
	// Visible decides whether the caller may see (and so call) a tool.
	Visible func(context.Context, contracts.RequestContext, ListedTool) bool
}

func isListRequest(body []byte) bool {
	var probe struct {
		Method string `json:"method"`
	}
	return json.Unmarshal(body, &probe) == nil && probe.Method == "tools/list"
}

func parseListRequest(body []byte) (json.RawMessage, error) {
	if err := validateJSON(body); err != nil {
		return nil, err
	}
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  *struct {
			Cursor *string `json:"cursor,omitempty"`
		} `json:"params,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || envelope.JSONRPC != "2.0" || envelope.Method != "tools/list" {
		return nil, fmt.Errorf("a single JSON-RPC 2.0 tools/list request is required")
	}
	if _, err := rpcID(envelope.ID); err != nil {
		return nil, err
	}
	if envelope.Params != nil && envelope.Params.Cursor != nil && len(*envelope.Params.Cursor) > 4096 {
		return nil, fmt.Errorf("tools/list cursor is too long")
	}
	return append(json.RawMessage(nil), envelope.ID...), nil
}

func (g *Gateway) credential(ctx context.Context) (Credential, error) {
	if g.options.Credentials != nil {
		credential, err := g.options.Credentials(ctx)
		if err != nil || credential == nil {
			return nil, errors.New("tool credential unavailable")
		}
		return credential, nil
	}
	token, err := g.options.TokenSource.Token(ctx)
	if err != nil || !validBearer(token) {
		return nil, errors.New("operator tool credential unavailable")
	}
	return bearerToken(token), nil
}

func (g *Gateway) upstreamRequest(ctx context.Context, body []byte) (Credential, *http.Request, error) {
	credential, err := g.credential(ctx)
	if err != nil {
		return nil, nil, err
	}
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, g.connection.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", "application/json")
	if g.connection.Mode == StatelessStreamableHTTP {
		upstream.Header.Set("Accept", "application/json, text/event-stream")
	}
	upstream.Header.Set("MCP-Protocol-Version", g.connection.ProtocolVersion)
	if err = credential.Apply(upstream); err != nil {
		return nil, nil, err
	}
	return credential, upstream, nil
}

func (g *Gateway) unauthorized(ctx context.Context, response *http.Response, credential Credential) {
	if response.StatusCode == http.StatusUnauthorized && g.options.Unauthorized != nil {
		g.options.Unauthorized(ctx, credential)
	}
}

// rpcDenied reports an authorization denial as a JSON-RPC error that keeps the
// caller's request ID. Upstream challenges and bodies never reach it.
func rpcDenied(w http.ResponseWriter, id json.RawMessage, err error) {
	code, reason, message, rule := RPCForbidden, "denied", "tool invocation denied", ""
	status := http.StatusForbidden
	var denial *Denial
	if errors.As(err, &denial) {
		if denial.Status >= 400 && denial.Status <= 599 {
			status = denial.Status
		}
		if denial.Code != 0 {
			code = denial.Code
		}
		if denial.Reason != "" {
			reason = denial.Reason
		}
		if denial.Message != "" {
			message = denial.Message
		}
		rule = denial.Rule
	}
	type rpcError struct {
		Code    int               `json:"code"`
		Message string            `json:"message"`
		Data    map[string]string `json:"data"`
	}
	data := map[string]string{"reason": reason}
	if rule != "" {
		data["rule"] = rule
	}
	body, _ := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   rpcError        `json:"error"`
	}{"2.0", id, rpcError{code, message, data}})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	// The denial's reason is the producer's own token (hook_denied, scope,
	// tool_policy_denied...); its upper-case form is the refusal's code.
	errorcode.Set(w.Header(), strings.ToUpper(reason))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (g *Gateway) serveList(w http.ResponseWriter, r *http.Request, trusted contracts.RequestContext, body []byte) {
	id, err := parseListRequest(body)
	if err != nil {
		errorcode.Refuse(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	list := g.options.List
	if list.Authorize == nil || list.Visible == nil {
		errorcode.Refuse(w, http.StatusServiceUnavailable, "TOOL_LISTING_UNAVAILABLE", "tool listing is unavailable")
		return
	}
	if err = list.Authorize(r.Context(), copyContext(trusted), g.Connection()); err != nil {
		rpcDenied(w, id, err)
		return
	}
	// Listing only reads: a transport failure may be retried (retry.go).
	credential, response, err := g.send(r.Context(), body, true)
	if err != nil {
		if _, ok := err.(errCredential); ok {
			errorcode.Refuse(w, http.StatusServiceUnavailable, "CREDENTIAL_UNAVAILABLE", "operator tool credential unavailable")
			return
		}
		errorcode.Refuse(w, http.StatusBadGateway, "UPSTREAM_UNAVAILABLE", "tool upstream unavailable")
		return
	}
	defer response.Body.Close()
	g.unauthorized(r.Context(), response, credential)
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, g.options.MaxResponseBytes+1))
	media, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	var message []byte
	switch {
	case readErr != nil || int64(len(raw)) > g.options.MaxResponseBytes || mediaErr != nil || response.StatusCode < 200 || response.StatusCode > 299 || response.Header.Get("MCP-Session-Id") != "":
	case media == "application/json" && ValidateResponse(raw, id) == nil:
		message = raw
	case media == "text/event-stream" && g.connection.Mode == StatelessStreamableHTTP:
		message, _ = streamTerminal(raw, id)
	}
	if message == nil {
		errorcode.Refuse(w, http.StatusBadGateway, "UPSTREAM_RESPONSE_INVALID", "tool list response was invalid")
		return
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(message, &envelope) != nil {
		errorcode.Refuse(w, http.StatusBadGateway, "UPSTREAM_RESPONSE_INVALID", "tool list response was invalid")
		return
	}
	if len(envelope.Error) > 0 {
		writeRPC(w, id, "error", envelope.Error)
		return
	}
	var result map[string]json.RawMessage
	var entries []json.RawMessage
	if json.Unmarshal(envelope.Result, &result) != nil || json.Unmarshal(result["tools"], &entries) != nil || len(entries) > 4096 {
		errorcode.Refuse(w, http.StatusBadGateway, "UPSTREAM_RESPONSE_INVALID", "tool list response was invalid")
		return
	}
	observed := make([]ListedTool, 0, len(entries))
	visible := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		var tool struct {
			Name        string `json:"name"`
			Annotations *struct {
				ReadOnlyHint    *bool `json:"readOnlyHint"`
				DestructiveHint *bool `json:"destructiveHint"`
				IdempotentHint  *bool `json:"idempotentHint"`
				OpenWorldHint   *bool `json:"openWorldHint"`
			} `json:"annotations"`
		}
		if json.Unmarshal(entry, &tool) != nil || !toolName.MatchString(tool.Name) {
			continue // An entry that cannot be authorized is never forwarded.
		}
		listed := ListedTool{Name: tool.Name}
		if tool.Annotations != nil {
			listed.ReadOnlyHint = tool.Annotations.ReadOnlyHint
			listed.DestructiveHint, listed.IdempotentHint, listed.OpenWorldHint = tool.Annotations.DestructiveHint, tool.Annotations.IdempotentHint, tool.Annotations.OpenWorldHint
		}
		observed = append(observed, listed)
		if list.Visible(r.Context(), copyContext(trusted), listed) {
			visible = append(visible, entry)
		}
	}
	if list.Observe != nil {
		list.Observe(r.Context(), copyContext(trusted), observed)
	}
	result["tools"], _ = json.Marshal(visible)
	encoded, err := json.Marshal(result)
	if err != nil {
		errorcode.Refuse(w, http.StatusBadGateway, "UPSTREAM_RESPONSE_INVALID", "tool list response was invalid")
		return
	}
	writeRPC(w, id, "result", encoded)
}

func writeRPC(w http.ResponseWriter, id json.RawMessage, member string, value json.RawMessage) {
	body, _ := json.Marshal(map[string]json.RawMessage{"jsonrpc": json.RawMessage(`"2.0"`), "id": id, member: value})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

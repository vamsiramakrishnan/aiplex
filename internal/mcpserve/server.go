// Package mcpserve is a stdio MCP server that exposes read-only AIPlex
// inspection tools, backed by the Go SDK and the caller's own token, so a
// coding agent can inspect and configure itself.
//
// It speaks the current stateless revision (per-request _meta, server/discover)
// and the handshake-based revisions (initialize) that today's harnesses use.
// There are no mutating tools; every tool is annotated read-only. Tool errors
// carry the API's message with the token removed.
package mcpserve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// SupportedVersions lists the protocol revisions served, newest first.
var SupportedVersions = []string{"2026-07-28", "2025-11-25", "2025-06-18"}

const modernVersion = "2026-07-28"

// JSON-RPC and MCP error codes.
const (
	codeParse           = -32700
	codeInvalidRequest  = -32600
	codeMethodNotFound  = -32601
	codeInvalidParams   = -32602
	codeInternal        = -32603
	codeUnsupportedVers = -32022
)

// Server serves MCP over a pair of streams.
type Server struct {
	Client  *aiplex.Client
	Version string   // server version reported to clients
	Secrets []string // values removed from every error message (the token)
	Log     io.Writer
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads newline-delimited JSON-RPC messages from in and writes
// responses to out until in is closed or ctx is done.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	r := bufio.NewReaderSize(in, 1<<20)
	w := bufio.NewWriter(out)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if resp := s.handleLine(ctx, bytes.TrimSpace(line)); resp != nil {
				b, merr := json.Marshal(resp)
				if merr != nil {
					return merr
				}
				w.Write(b)
				w.WriteByte('\n')
				if ferr := w.Flush(); ferr != nil {
					return ferr
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (s *Server) handleLine(ctx context.Context, line []byte) *response {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return &response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: codeParse, Message: "parse error"}}
	}
	if req.Method == "" || req.JSONRPC != "2.0" {
		return &response{JSONRPC: "2.0", ID: nullID(req.ID), Error: &rpcError{Code: codeInvalidRequest, Message: "invalid request"}}
	}
	if len(req.ID) == 0 { // notification: never answered
		return nil
	}
	result, rerr := s.dispatch(ctx, &req)
	resp := &response{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	return resp
}

func nullID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func supported(v string) bool {
	for _, s := range SupportedVersions {
		if s == v {
			return true
		}
	}
	return false
}

func (s *Server) serverInfo() map[string]any {
	return map[string]any{"name": "aiplex", "title": "AIPlex", "version": s.Version}
}

const instructions = "Read-only view of AIPlex for the signed-in caller: who you are, which access points you can use, " +
	"their models and client configuration, and your own usage. Nothing here changes AIPlex."

// requestVersion returns the modern per-request protocol version, if any.
func requestVersion(params json.RawMessage) string {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil {
		return ""
	}
	raw, ok := p.Meta["io.modelcontextprotocol/protocolVersion"]
	if !ok {
		return ""
	}
	var v string
	_ = json.Unmarshal(raw, &v)
	return v
}

func (s *Server) dispatch(ctx context.Context, req *request) (any, *rpcError) {
	modern := false
	if v := requestVersion(req.Params); v != "" {
		if !supported(v) {
			return nil, &rpcError{Code: codeUnsupportedVers, Message: "Unsupported protocol version",
				Data: map[string]any{"supported": SupportedVersions, "requested": v}}
		}
		modern = v == modernVersion
	}
	wrap := func(res map[string]any) map[string]any {
		if modern {
			res["resultType"] = "complete"
			res["_meta"] = map[string]any{"io.modelcontextprotocol/serverInfo": s.serverInfo()}
		}
		return res
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := SupportedVersions[1] // newest handshake-based revision
		if supported(p.ProtocolVersion) && p.ProtocolVersion != modernVersion {
			version = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      s.serverInfo(),
			"instructions":    instructions,
		}, nil
	case "server/discover":
		return wrap(map[string]any{
			"supportedVersions": SupportedVersions,
			"capabilities":      map[string]any{"tools": map[string]any{"listChanged": false}},
			"instructions":      instructions,
		}), nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return wrap(map[string]any{"tools": toolDefinitions()}), nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "tools/call needs a tool name"}
		}
		tool, ok := toolByName(p.Name)
		if !ok {
			return nil, &rpcError{Code: codeInvalidParams, Message: "Unknown tool: " + p.Name}
		}
		value, err := tool.run(ctx, s, p.Arguments)
		if err != nil {
			return wrap(map[string]any{
				"content": []any{map[string]any{"type": "text", "text": s.scrub(err.Error())}},
				"isError": true,
			}), nil
		}
		text, merr := json.MarshalIndent(value, "", "  ")
		if merr != nil {
			return nil, &rpcError{Code: codeInternal, Message: "could not encode the result"}
		}
		return wrap(map[string]any{
			"content":           []any{map[string]any{"type": "text", "text": s.scrub(string(text))}},
			"structuredContent": value,
			"isError":           false,
		}), nil
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "Method not found: " + req.Method}
}

// scrub removes secret values from text bound for the client.
func (s *Server) scrub(text string) string {
	for _, secret := range s.Secrets {
		if len(secret) >= 8 {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return text
}

func (s *Server) logf(format string, args ...any) {
	if s.Log != nil {
		fmt.Fprintf(s.Log, format+"\n", args...)
	}
}

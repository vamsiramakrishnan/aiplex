package aiplex_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func TestRegisterToolConnectionPostsWithoutBodyAndDecodesRegistration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/mcp/connections/files/registration" || len(body) != 0 {
			t.Errorf("unexpected request %s %s body=%q", r.Method, r.URL.Path, body)
		}
		w.Write([]byte(`{"tenant_id":"t","kind":"oauth-registration","id":"files","revision":2,"spec":{"mode":"pre-registered","client_id":"cid","issuer":"https://issuer.example"}}`))
	}))
	defer server.Close()
	rec, err := aiplex.NewClient(server.URL).RegisterToolConnection(context.Background(), "files")
	if err != nil || rec.Revision != 2 || rec.Kind != "oauth-registration" || !strings.Contains(string(rec.Spec), `"client_id":"cid"`) {
		t.Fatalf("registration %+v %v", rec, err)
	}
	if _, err = aiplex.NewClient(server.URL).RegisterToolConnection(context.Background(), "../x"); err == nil {
		t.Fatal("unsafe id accepted")
	}
}

func TestRegisterToolConnectionSurfacesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		w.Write([]byte(`{"code":"REGISTRATION_FAILED","message":"invalid_redirect_uri"}`))
	}))
	defer server.Close()
	_, err := aiplex.NewClient(server.URL).RegisterToolConnection(context.Background(), "files")
	var apiErr *aiplex.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 502 || apiErr.Code != "REGISTRATION_FAILED" {
		t.Fatalf("error %v", err)
	}
}

func TestCallConnectionToolResults(t *testing.T) {
	var got map[string]any
	status, reply := 200, ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/mcp/connections/files/invoke" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		got = nil
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
		w.Write([]byte(reply))
	}))
	defer server.Close()
	c := aiplex.NewClient(server.URL)

	reply = `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"hi"},{"type":"image","mimeType":"image/png","data":"AAAA"}],"isError":false}}`
	res, err := c.CallConnectionTool(context.Background(), "files", "echo", json.RawMessage(`{"text":"hi"}`))
	params, _ := got["params"].(map[string]any)
	if err != nil || got["method"] != "tools/call" || params["name"] != "echo" || params["arguments"].(map[string]any)["text"] != "hi" {
		t.Fatalf("request %v err %v", got, err)
	}
	if len(res.Content) != 2 || res.Content[0].Text != "hi" || res.Content[1].MimeType != "image/png" || res.IsError || !strings.Contains(string(res.Raw), "isError") {
		t.Fatalf("result %+v", res)
	}

	// A policy denial arrives as HTTP 403 carrying a JSON-RPC error.
	status, reply = 403, `{"jsonrpc":"2.0","id":1,"error":{"code":-32001,"message":"no echo","data":{"reason":"tool_policy_denied","rule":"deny-echo"}}}`
	_, err = c.CallConnectionTool(context.Background(), "files", "echo", nil)
	var call *aiplex.ToolCallError
	if !errors.As(err, &call) || call.StatusCode != 403 || call.Code != -32001 || call.Reason != "tool_policy_denied" || call.Rule != "deny-echo" || call.Message != "no echo" {
		t.Fatalf("denial %#v", err)
	}
	if args, ok := got["params"].(map[string]any)["arguments"].(map[string]any); !ok || len(args) != 0 {
		t.Fatal("missing arguments must be sent as an empty object")
	}

	// An ordinary API error keeps its shape.
	status, reply = 403, `{"code":"FORBIDDEN","message":"action grant required"}`
	_, err = c.CallConnectionTool(context.Background(), "files", "echo", nil)
	var apiErr *aiplex.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "FORBIDDEN" {
		t.Fatalf("api error %#v", err)
	}

	// A success status with neither result nor error is not a result.
	status, reply = 200, `{"jsonrpc":"2.0","id":1}`
	if _, err = c.CallConnectionTool(context.Background(), "files", "echo", nil); err == nil {
		t.Fatal("empty answer accepted")
	}
	status, reply = 200, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"boom"}],"isError":true}}`
	res, err = c.CallConnectionTool(context.Background(), "files", "echo", nil)
	if err != nil || !res.IsError {
		t.Fatalf("isError result %+v %v", res, err)
	}
	if _, err = c.CallConnectionTool(context.Background(), "files", "", nil); err == nil {
		t.Fatal("empty tool accepted")
	}
	if _, err = c.CallConnectionTool(context.Background(), "files", "echo", json.RawMessage(`[1]`)); err == nil {
		t.Fatal("non-object arguments accepted")
	}
}

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// invokeStub answers /invoke with the given status and body and records the
// last JSON-RPC request it saw.
type invokeStub struct {
	status int
	body   string
	last   map[string]any
	calls  int
}

func newInvokeStub(t *testing.T, status int, body string) *invokeStub {
	t.Helper()
	s := &invokeStub{status: status, body: body}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/iam/whoami" { // the CLI explains a 403 by asking who it is
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path != "/api/v1/mcp/connections/files/invoke" && r.URL.Path != "/api/v1/mcp/connections/files/registration" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		s.calls++
		s.last = nil
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &s.last)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		w.Write([]byte(s.body))
	}))
	t.Cleanup(ts.Close)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AIPLEX_URL", ts.URL)
	t.Setenv("AIPLEX_TOKEN", "live-token-must-not-appear-1234567890")
	old := output
	output = "table"
	t.Cleanup(func() { output = old })
	return s
}

func exitOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	return reportError(io.Discard, nil, nil, err)
}

const echoOK = `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"hello"},{"type":"image","mimeType":"image/png","data":"QkFTRTY0LU5PVC1TSE9XTg=="}],"isError":false}}`

func TestMCPConnectionsCallPrintsTextAndSendsArguments(t *testing.T) {
	s := newInvokeStub(t, 200, echoOK)
	out, errText, err := runCLI(t, "mcp", "connections", "call", "files", "echo", "--args", `{"text":"hello"}`)
	if err != nil {
		t.Fatal(err, errText)
	}
	params := s.last["params"].(map[string]any)
	if s.last["method"] != "tools/call" || params["name"] != "echo" || params["arguments"].(map[string]any)["text"] != "hello" {
		t.Fatalf("request %v", s.last)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "image/png") || strings.Contains(out, "QkFTRTY0") {
		t.Fatalf("text output %q", out)
	}
	if strings.Contains(out+errText, "live-token-must-not-appear") {
		t.Fatal("token printed")
	}
}

func TestMCPConnectionsCallJSONPrintsRawResult(t *testing.T) {
	newInvokeStub(t, 200, echoOK)
	out, _, err := runCLI(t, "mcp", "connections", "call", "files", "echo", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Content []map[string]any `json:"content"`
		IsError bool             `json:"isError"`
	}
	if json.Unmarshal([]byte(out), &raw) != nil || len(raw.Content) != 2 || raw.Content[0]["text"] != "hello" || raw.Content[1]["data"] == nil {
		t.Fatalf("json output %q", out)
	}
}

func TestMCPConnectionsCallArgsFileAndStdin(t *testing.T) {
	s := newInvokeStub(t, 200, echoOK)
	path := filepath.Join(t.TempDir(), "args.json")
	if err := os.WriteFile(path, []byte(`{"text":"from file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, e, err := runCLI(t, "mcp", "connections", "call", "files", "echo", "--args-file", path); err != nil {
		t.Fatal(err, e)
	}
	if s.last["params"].(map[string]any)["arguments"].(map[string]any)["text"] != "from file" {
		t.Fatalf("file args %v", s.last)
	}
	root := newRootCmd()
	root.SetIn(strings.NewReader(`text: from stdin`))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"mcp", "connections", "call", "files", "echo", "--args-file", "-"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if s.last["params"].(map[string]any)["arguments"].(map[string]any)["text"] != "from stdin" {
		t.Fatalf("stdin args %v", s.last)
	}
}

func TestMCPConnectionsCallRefusesBadInputWithExitTwoBeforeTheNetwork(t *testing.T) {
	s := newInvokeStub(t, 200, echoOK)
	missing := filepath.Join(t.TempDir(), "missing.json")
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte("[1,2"), 0o600)
	cases := map[string][]string{
		"not json":             {"--args", `{nope`},
		"array":                {"--args", `[1]`},
		"scalar":               {"--args", `"x"`},
		"both":                 {"--args", `{}`, "--args-file", bad},
		"missing file":         {"--args-file", missing},
		"file not parseable":   {"--args-file", bad},
		"unknown flag":         {"--argz", `{}`},
		"unsafe connection id": nil,
	}
	for name, extra := range cases {
		args := append([]string{"mcp", "connections", "call", "files", "echo"}, extra...)
		if name == "unsafe connection id" {
			args = []string{"mcp", "connections", "call", "../x", "echo"}
		}
		_, _, err := runCLI(t, args...)
		if err == nil || exitOf(t, err) != 2 {
			t.Errorf("%s: exit %d err %v", name, exitOf(t, err), err)
		}
	}
	if _, _, err := runCLI(t, "mcp", "connections", "call", "files"); err == nil || exitOf(t, err) != 2 {
		t.Errorf("missing tool: %v", err)
	}
	if s.calls != 0 {
		t.Fatalf("rejected input reached the API %d times", s.calls)
	}
}

func TestMCPConnectionsCallShowsPolicyDenialAsDenial(t *testing.T) {
	newInvokeStub(t, 403, `{"jsonrpc":"2.0","id":1,"error":{"code":-32001,"message":"echo is blocked","data":{"reason":"tool_policy_denied","rule":"no-echo"}}}`)
	out, errText, err := runCLI(t, "mcp", "connections", "call", "files", "echo", "--args", `{"text":"x"}`)
	if err == nil || exitOf(t, err) != 3 {
		t.Fatalf("exit %d err %v", exitOf(t, err), err)
	}
	var sb strings.Builder
	reportError(&sb, nil, nil, err)
	shown := out + errText + sb.String()
	for _, want := range []string{"denied", "echo is blocked", "no-echo"} {
		if !strings.Contains(shown, want) {
			t.Errorf("denial output lacks %q: %s", want, shown)
		}
	}
	if strings.Contains(shown, "live-token") {
		t.Fatal("token printed")
	}
}

func TestMCPConnectionsCallUpstreamAndToolErrors(t *testing.T) {
	// The tool server's JSON-RPC error: an error, not a denial.
	newInvokeStub(t, 200, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"unknown tool"}}`)
	_, _, err := runCLI(t, "mcp", "connections", "call", "files", "nope")
	if err == nil || exitOf(t, err) != 1 || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("rpc error: exit %d %v", exitOf(t, err), err)
	}
	// A gateway failure reaching the upstream.
	newInvokeStub(t, 502, `{"code":"UPSTREAM_UNAVAILABLE","message":"upstream did not answer"}`)
	_, _, err = runCLI(t, "mcp", "connections", "call", "files", "echo")
	if err == nil || exitOf(t, err) != 1 || !strings.Contains(err.Error(), "upstream did not answer") {
		t.Fatalf("502: exit %d %v", exitOf(t, err), err)
	}
	// Unknown connection.
	newInvokeStub(t, 404, `{"code":"NOT_FOUND","message":"resource not found"}`)
	_, _, err = runCLI(t, "mcp", "connections", "call", "files", "echo")
	if err == nil || exitOf(t, err) != 4 {
		t.Fatalf("404: exit %d %v", exitOf(t, err), err)
	}
	// The tool ran and reported a failure: its text is shown, the exit is 1.
	newInvokeStub(t, 200, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"disk full"}],"isError":true}}`)
	out, _, err := runCLI(t, "mcp", "connections", "call", "files", "echo")
	if err == nil || exitOf(t, err) != 1 || !strings.Contains(out, "disk full") {
		t.Fatalf("isError: exit %d out %q err %v", exitOf(t, err), out, err)
	}
	// An answer with no result is not shown as an empty success.
	newInvokeStub(t, 200, `{"jsonrpc":"2.0","id":1}`)
	if _, _, err = runCLI(t, "mcp", "connections", "call", "files", "echo"); err == nil || exitOf(t, err) != 1 {
		t.Fatalf("empty answer: %v", err)
	}
}

func TestToolAuthRegisterPrintsPublicRegistrationOnly(t *testing.T) {
	s := newInvokeStub(t, 200, `{"tenant_id":"t","kind":"oauth-registration","id":"files","revision":3,"spec":{"mode":"pre-registered","client_id":"cid-123","credential_ref":"tenant/ns/client-secret","issuer":"https://issuer.example"}}`)
	out, errText, err := runCLI(t, "tool-auth", "register", "files")
	if err != nil {
		t.Fatal(err, errText)
	}
	if s.calls != 1 {
		t.Fatalf("calls %d", s.calls)
	}
	for _, want := range []string{"cid-123", "pre-registered", "tenant/ns/client-secret"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out+errText, "live-token") {
		t.Fatal("token printed")
	}
}

func TestToolAuthRegisterErrorsUseDocumentedExitCodes(t *testing.T) {
	cases := []struct {
		status int
		body   string
		exit   int
	}{
		{404, `{"code":"NOT_FOUND","message":"resource not found"}`, 4},
		{403, `{"code":"FORBIDDEN","message":"role required"}`, 3},
		{409, `{"code":"CONNECTION_CHANGED","message":"changed"}`, 5},
		{502, `{"code":"REGISTRATION_FAILED","message":"invalid_redirect_uri"}`, 1},
		{503, `{"code":"REGISTRATION_UNAVAILABLE","message":"configure outbound endpoint policy"}`, 1},
	}
	for _, c := range cases {
		newInvokeStub(t, c.status, c.body)
		_, _, err := runCLI(t, "tool-auth", "register", "files")
		if err == nil || exitOf(t, err) != c.exit {
			t.Errorf("%d: exit %d err %v", c.status, exitOf(t, err), err)
		}
	}
	s := newInvokeStub(t, 200, `{}`)
	for _, args := range [][]string{{"tool-auth", "register"}, {"tool-auth", "register", "../x"}, {"tool-auth", "register", "a", "b"}, {"tool-auth", "register", "files", "--client-secret", "s3cret"}} {
		_, _, err := runCLI(t, args...)
		if err == nil || exitOf(t, err) != 2 {
			t.Errorf("%v: exit %d err %v", args, exitOf(t, err), err)
		}
	}
	if s.calls != 0 {
		t.Fatal("rejected input reached the API")
	}
	// A registration without a client id is shown as unknown, not blank.
	newInvokeStub(t, 200, `{"tenant_id":"t","kind":"oauth-registration","id":"files","revision":1,"spec":{"mode":"dynamic"}}`)
	out, _, err := runCLI(t, "tool-auth", "register", "files")
	if err != nil || !strings.Contains(out, "unknown") {
		t.Fatalf("missing client id not shown as unknown: %q %v", out, err)
	}
}

func TestToolAuthStatusHasDescription(t *testing.T) {
	for _, c := range toolAuthCmd().Commands() {
		if c.Short == "" {
			t.Errorf("tool-auth %s has no one-line description", c.Name())
		}
	}
}

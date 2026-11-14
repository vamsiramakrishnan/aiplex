package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func cliErrText(err error) string {
	ce, ok := err.(*cliError)
	if !ok {
		return err.Error()
	}
	return ce.msg + "\n" + strings.Join(ce.hints, "\n")
}

// A 503 from the invoke route is shown with the server's own code and message.
// The credential explanation and the aiplex dev hint appear only for the
// credential failure (plain text, which the SDK reports with code "unknown"),
// for both `connections test` and `connections call`.
func TestGateway503Explanations(t *testing.T) {
	t.Setenv("AIPLEX_URL", "http://127.0.0.1:8080")
	cases := []struct {
		name       string
		err        *aiplex.Error
		credential bool
		want       string
	}{
		{"credential", &aiplex.Error{Code: "unknown", Message: "operator tool credential unavailable", StatusCode: 503}, true, "operator tool credential unavailable"},
		{"identity profile", &aiplex.Error{Code: "unknown", Message: "tool credential unavailable", StatusCode: 503}, true, "tool credential unavailable"},
		{"configuration", &aiplex.Error{Code: "TOOL_CONFIGURATION_UNAVAILABLE", Message: "tool execution prerequisites are unavailable", StatusCode: 503}, false, "TOOL_CONFIGURATION_UNAVAILABLE: tool execution prerequisites"},
		{"quota", &aiplex.Error{Code: "QUOTA_UNAVAILABLE", Message: "quota control is unavailable", StatusCode: 503}, false, "QUOTA_UNAVAILABLE: quota control"},
		{"execution", &aiplex.Error{Code: "TOOL_EXECUTION_UNAVAILABLE", Message: "tool execution is unavailable", StatusCode: 503}, false, "TOOL_EXECUTION_UNAVAILABLE: tool execution"},
		{"unknown plain text", &aiplex.Error{Code: "unknown", Message: "503 Service Unavailable", StatusCode: 503}, false, "503 Service Unavailable"},
	}
	for _, tc := range cases {
		for via, err := range map[string]error{"test": explainToolTest("c1", tc.err), "call": explainToolCall("c1", "echo", tc.err)} {
			text := cliErrText(err)
			if !strings.Contains(text, tc.want) {
				t.Errorf("%s/%s: server text missing:\n%s", tc.name, via, text)
			}
			if got := strings.Contains(text, "AIPLEX_TOOL_SECRET_") || strings.Contains(text, "secret reference"); got != tc.credential {
				t.Errorf("%s/%s: credential/secret hint = %v, want %v:\n%s", tc.name, via, got, tc.credential, text)
			}
		}
	}
	// The aiplex dev environment hint is for a loopback API only.
	t.Setenv("AIPLEX_URL", "https://aiplex.example.com")
	text := cliErrText(explainToolTest("c1", cases[0].err))
	if strings.Contains(text, "AIPLEX_TOOL_SECRET_") || !strings.Contains(text, "aiplex mcp connections get c1") {
		t.Errorf("non-loopback credential failure:\n%s", text)
	}
}

// The whole path: a plain-text 503 from the server reaches the message.
func TestConnectionTestShowsPlainTextGateway503(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "operator tool credential unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	t.Setenv("AIPLEX_URL", srv.URL)
	err := printConnectionTools(context.Background(), io.Discard, aiplex.NewClient(srv.URL), "c1")
	text := cliErrText(err)
	if !strings.Contains(text, "operator tool credential unavailable") || !strings.Contains(text, "AIPLEX_TOOL_SECRET_") {
		t.Fatalf("%s", text)
	}
}

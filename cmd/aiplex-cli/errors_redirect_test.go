package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// An API that answers with a redirect is aimed at the wrong place. The CLI
// (through the SDK) does not follow it, says what the likely cause is, exits
// non-zero, and prints nothing from the Location beyond its scheme and host.
func TestCLIReportsARedirectFromTheAPI(t *testing.T) {
	var hits atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer sink.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL+"/leak/SECRET?token=SECRET", http.StatusFound)
	}))
	defer api.Close()
	healthEnv(t, api.URL, "live-token-must-not-appear-1234567890")

	root := newRootCmd()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs([]string{"budgets", "get", "b"})
	err := root.Execute()
	if err == nil {
		t.Fatal("a redirect from the API must fail the command")
	}
	var report bytes.Buffer
	code := reportError(&report, root, []string{"budgets", "get", "b"}, err)
	if code == 0 {
		t.Fatal("exit code is zero")
	}
	text := out.String() + errb.String() + report.String() + err.Error()
	for _, want := range []string{"redirect", sink.URL, "AIPLEX_URL", "proxy", "aiplex config show"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	for _, leak := range []string{"SECRET", "/leak", "live-token-must-not-appear"} {
		if strings.Contains(text, leak) {
			t.Errorf("output leaks %q:\n%s", leak, text)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("the redirect target received %d request(s)", hits.Load())
	}
}

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func runComplete(t *testing.T, args ...string) string {
	t.Helper()
	root := newRootCmd()
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"__complete"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestCompletionOffersIDsFromTheServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/access-points/":
			w.Write([]byte(`[{"id":"local","revision":1,"spec":{"id":"local"}},{"id":"team","revision":1,"spec":{"id":"team"}}]`))
		case "/api/v1/mcp/connections/":
			w.Write([]byte(`[{"kind":"tool-connection","id":"local-tools","revision":1,"spec":{}}]`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("AIPLEX_URL", server.URL)
	t.Setenv("HOME", t.TempDir())
	if out := runComplete(t, "access-points", "get", "t"); !strings.Contains(out, "team") || strings.Contains(out, "local\n") {
		t.Fatalf("access point ids: %q", out)
	}
	if out := runComplete(t, "connect", ""); !strings.Contains(out, "local") || !strings.Contains(out, "team") {
		t.Fatalf("connect ids: %q", out)
	}
	if out := runComplete(t, "mcp", "connections", "test", ""); !strings.Contains(out, "local-tools") {
		t.Fatalf("connection ids: %q", out)
	}
	if out := runComplete(t, "-o", ""); !strings.Contains(out, "wide") {
		t.Fatalf("output formats: %q", out)
	}
}

// Completion never hangs on an API that does not answer.
func TestCompletionIsBounded(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer server.Close()
	defer close(block)
	t.Setenv("AIPLEX_URL", server.URL)
	t.Setenv("HOME", t.TempDir())
	start := time.Now()
	out := runComplete(t, "access-points", "get", "")
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("completion took %s", d)
	}
	if strings.Contains(out, "local") {
		t.Fatalf("unexpected ids: %q", out)
	}
}

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

// --help on every command answers offline: it never contacts the API.
func TestHelpNeverContactsServer(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(500)
	}))
	defer server.Close()
	t.Setenv("AIPLEX_URL", server.URL)
	t.Setenv("HOME", t.TempDir())
	var paths [][]string
	walkCommands(newRootCmd(), func(c *cobra.Command) {
		paths = append(paths, strings.Fields(strings.TrimPrefix(c.CommandPath(), "aiplex ")))
	})
	for _, p := range paths {
		root := newRootCmd()
		var out strings.Builder
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append(p, "--help"))
		if err := root.Execute(); err != nil {
			t.Errorf("aiplex %s --help: %v", strings.Join(p, " "), err)
		}
		if !strings.Contains(out.String(), "Usage:") {
			t.Errorf("aiplex %s --help printed no usage", strings.Join(p, " "))
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("--help contacted the server %d times", n)
	}
}

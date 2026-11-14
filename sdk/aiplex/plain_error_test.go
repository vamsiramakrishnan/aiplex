package aiplex_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// A non-JSON error body keeps its first line as the message (trimmed, printable
// characters only, at most 300 characters); an empty body keeps the status.
func TestClientKeepsPlainTextErrorBody(t *testing.T) {
	for name, tc := range map[string]struct {
		body, want string
	}{
		"plain":      {"operator tool credential unavailable\n", "operator tool credential unavailable"},
		"first line": {"first\nsecond", "first"},
		"controls":   {"bad\x1b[31m text", "bad[31m text"},
		"c1 control": {"x\u009b31m y", "x31m y"},
		"bidi":       {"safe \u202eevil\u202c end", "safe evil end"},
		"zero width": {"a\u200bb", "ab"},
		"tab":        {"a\tb", "a b"},
		"nbsp":       {"a\u00a0b", "a b"},
		"long":       {strings.Repeat("x", 500), strings.Repeat("x", 300)},
		"empty":      {"", "503 Service Unavailable"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(tc.body))
		}))
		_, err := aiplex.NewClient(srv.URL).ListConnectionTools(context.Background(), "c")
		srv.Close()
		var apiErr *aiplex.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "unknown" || apiErr.StatusCode != 503 || apiErr.Message != tc.want {
			t.Errorf("%s: %+v (%v)", name, apiErr, err)
		}
	}
}

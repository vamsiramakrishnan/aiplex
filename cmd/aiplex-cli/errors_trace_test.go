package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// The API error body carries trace_id beside request_id; the SDK decodes it and
// the CLI shows it wherever it shows the request ID.
func TestAPIErrorTraceIDIsDecodedAndShown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":"USAGE_LEDGER_ERROR","message":"usage ledger unavailable","request_id":"req-1","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}`))
	}))
	defer server.Close()
	client := aiplex.NewClient(server.URL)
	_, err := client.GetModelPrice(t.Context(), "p")
	var apiErr *aiplex.Error
	if !errors.As(err, &apiErr) || apiErr.RequestID != "req-1" || apiErr.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("decoded error: %+v (%v)", apiErr, err)
	}
	ce := explain(&cobra.Command{Use: "get"}, nil, err)
	text := strings.Join(ce.hints, "\n")
	if !strings.Contains(text, "Request ID: req-1") || !strings.Contains(text, "Trace ID: 4bf92f3577b34da6a3ce929d0e0e4736") {
		t.Fatalf("hints: %v", ce.hints)
	}
}

package toolexec

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
)

// dropFirst closes the connection of the first request without answering
// (a transport failure), then answers normally.
func dropFirst(t *testing.T, calls *atomic.Int32) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, toolResult)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestToolCallRetriesTransportFailureOnlyForRepeatableTools(t *testing.T) {
	for _, repeatable := range []bool{false, true} {
		var calls atomic.Int32
		server := dropFirst(t, &calls)
		lease := &recordingLease{}
		opts := toolOptions(server, lease)
		opts.Retry = &TransportRetry{Attempts: 2, Call: func(context.Context, Request) bool { return repeatable }}
		g, err := NewGateway(toolConnection(server), opts)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, toolRequest(toolBody))
		if repeatable && (w.Code != 200 || calls.Load() != 2 || lease.claims != 1) {
			t.Fatalf("read-only call retried once under one claim: %d calls=%d claims=%d", w.Code, calls.Load(), lease.claims)
		}
		if !repeatable && (w.Code != http.StatusBadGateway || calls.Load() != 1) {
			t.Fatalf("other calls are never retried: %d calls=%d", w.Code, calls.Load())
		}
	}
}

func TestToolListRetriesTransportFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"search","inputSchema":{"type":"object"}}]}}`)
	}))
	t.Cleanup(server.Close)
	opts := toolOptions(server, &recordingLease{})
	opts.List = &ListOptions{Authorize: func(context.Context, contracts.RequestContext, Connection) error { return nil }, Visible: func(context.Context, contracts.RequestContext, ListedTool) bool { return true }}
	opts.Retry = &TransportRetry{Attempts: 2}
	g, err := NewGateway(toolConnection(server), opts)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, toolRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("tools/list retried: %d calls=%d %s", w.Code, calls.Load(), w.Body)
	}
}

func TestToolRetryDoesNotReauthorizeOrReinspect(t *testing.T) {
	var calls atomic.Int32
	server := dropFirst(t, &calls)
	authorized, inspected := 0, 0
	opts := toolOptions(server, &recordingLease{})
	inner := opts.Authorize
	opts.Authorize = func(ctx context.Context, c contracts.RequestContext, conn Connection, r Request) error {
		authorized++
		return inner(ctx, c, conn, r)
	}
	opts.InspectRequest = func(_ context.Context, r Request) ([]byte, error) { inspected++; return r.Body, nil }
	opts.Retry = &TransportRetry{Attempts: 2, Call: func(context.Context, Request) bool { return true }}
	g, err := NewGateway(toolConnection(server), opts)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, toolRequest(toolBody))
	if w.Code != 200 || calls.Load() != 2 || authorized != 1 || inspected != 1 {
		t.Fatalf("retry: %d sends=%d authorize=%d inspect=%d", w.Code, calls.Load(), authorized, inspected)
	}
}

package toolexec

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// profiledLease is a recording lease that declares a metering profile.
type profiledLease struct {
	recordingLease
	profile string
}

func (l *profiledLease) MeteringProfile() string { return l.profile }

type usageRun struct {
	code    int
	lease   *profiledLease
	ledger  []map[string]int64
	upCalls int32
}

// runToolUsage relays one tools/call to handler with the given lease profile
// ("" for no lease: the ledger observer then reports usage).
func runToolUsage(t *testing.T, handler http.HandlerFunc, profile string, mutate func(*Connection, *Options)) usageRun {
	t.Helper()
	run := usageRun{}
	var upCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upCalls.Add(1)
		handler(w, r)
	}))
	defer server.Close()
	lease := &profiledLease{profile: profile}
	opts := toolOptions(server, &lease.recordingLease)
	opts.Prepare = func(context.Context, Invocation) (execution.Lease, error) { return lease, nil }
	if profile == "" {
		opts.Prepare = nil
	}
	opts.ObserveUsage = func(_ context.Context, counters map[string]int64) {
		copied := map[string]int64{}
		for k, v := range counters {
			copied[k] = v
		}
		run.ledger = append(run.ledger, copied)
	}
	connection := toolConnection(server)
	if mutate != nil {
		mutate(&connection, &opts)
	}
	gateway, err := NewGateway(connection, opts)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, toolRequest(toolBody))
	run.code, run.lease, run.upCalls = w.Code, lease, upCalls.Load()
	return run
}

func jsonResult(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, toolResult)
}

// A complete response: exact bytes (the body as received) and the duration
// from dispatch to the end of the body, in whole milliseconds rounded up and
// at least 1.
func TestToolUsageCompleteResponseRecordsBytesAndDuration(t *testing.T) {
	for _, profile := range []string{"", "request-count", MeteringProfileToolUsage} {
		slow := func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(20 * time.Millisecond)
			jsonResult(w, r)
		}
		run := runToolUsage(t, slow, profile, nil)
		if run.code != 200 {
			t.Fatalf("%q: %d", profile, run.code)
		}
		var counters map[string]int64
		if profile == "" {
			if len(run.ledger) != 1 {
				t.Fatalf("no ledger observation without a lease: %+v", run.ledger)
			}
			counters = run.ledger[0]
		} else {
			if len(run.ledger) != 0 || len(run.lease.observations) != 1 || !run.lease.observations[0].Terminal {
				t.Fatalf("%q: with a lease the lease reports usage once: ledger=%+v lease=%+v", profile, run.ledger, run.lease.observations)
			}
			counters = run.lease.observations[0].Counters
		}
		if counters[CounterToolResponseBytes] != int64(len(toolResult)) || counters[CounterToolDurationMilliseconds] < 20 || counters["requests"] != 1 || counters["tool_calls"] != 1 {
			t.Fatalf("%q: %+v", profile, counters)
		}
	}
}

// An HTTP 200 carrying a JSON-RPC error is the tool's complete answer: it
// settles with its bytes and duration.
func TestToolUsageJSONRPCErrorIsComplete(t *testing.T) {
	rpcError := `{"jsonrpc":"2.0","id":7,"error":{"code":-32000,"message":"tool failed"}}`
	run := runToolUsage(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, rpcError)
	}, MeteringProfileToolUsage, nil)
	if run.code != 200 || len(run.lease.observations) != 1 || !run.lease.observations[0].Terminal || run.lease.observations[0].Counters[CounterToolResponseBytes] != int64(len(rpcError)) || run.lease.observations[0].Counters[CounterToolDurationMilliseconds] < 1 {
		t.Fatalf("%d %+v", run.code, run.lease.observations)
	}
}

// A finite SSE response is read whole: its bytes are the stream's bytes.
func TestToolUsageFiniteStreamRecordsTheWholeStream(t *testing.T) {
	stream := "event: message\ndata: " + toolResult + "\n\n"
	run := runToolUsage(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}, MeteringProfileToolUsage, func(c *Connection, _ *Options) { c.Mode = StatelessStreamableHTTP })
	if run.code != 200 || len(run.lease.observations) != 1 || run.lease.observations[0].Counters[CounterToolResponseBytes] != int64(len(stream)) {
		t.Fatalf("%d %+v", run.code, run.lease.observations)
	}
}

func assertUnknown(t *testing.T, name string, run usageRun) {
	t.Helper()
	if run.code == 200 {
		t.Fatalf("%s: delivered", name)
	}
	for _, o := range run.lease.observations {
		if _, ok := o.Counters[CounterToolResponseBytes]; ok {
			t.Fatalf("%s: bytes were reported for an incomplete response: %+v", name, o)
		}
		if _, ok := o.Counters[CounterToolDurationMilliseconds]; ok {
			t.Fatalf("%s: duration was reported for an incomplete response: %+v", name, o)
		}
	}
	if len(run.ledger) != 0 {
		t.Fatalf("%s: ledger usage reported: %+v", name, run.ledger)
	}
	results := run.lease.results
	if len(results) != 1 || results[0].Terminal || results[0].NotSent || results[0].Rejected || results[0].Err == nil {
		t.Fatalf("%s: the outcome must stay unknown: %+v", name, results)
	}
}

// Endings without a complete response report neither bytes nor duration and
// leave the outcome unknown.
func TestToolUsageIncompleteEndingsAreUnknown(t *testing.T) {
	big := strings.Repeat("x", 4096)
	cases := map[string]struct {
		handler http.HandlerFunc
		mutate  func(*Connection, *Options)
	}{
		"cut at the size bound": {handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":7,"result":{"content":[{"type":"text","text":"`+big+`"}]}}`)
		}, mutate: func(c *Connection, _ *Options) { c.MaxResponseBytes = 1024 }},
		"connection reset mid-body": {handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "1000")
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":7,"result":`)
			w.(http.Flusher).Flush()
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				if tcp, ok := conn.(*net.TCPConn); ok {
					_ = tcp.SetLinger(0)
				}
				_ = conn.Close()
			}
		}},
		"timeout at the duration bound": {handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":7,"result":`)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
			}
		}, mutate: func(c *Connection, _ *Options) { c.MaxDurationSeconds = 1 }},
		"upstream error status with a body": {handler: func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"tool server failure"}`, http.StatusInternalServerError)
		}},
	}
	for name, c := range cases {
		run := runToolUsage(t, c.handler, MeteringProfileToolUsage, c.mutate)
		assertUnknown(t, name, run)
	}
}

// Never-started calls follow the shared rule (execution.ConnectionTrace,
// execution.RejectedBeforeProcessing), and only for a lease that meters
// usage: a request-count lease keeps today's behaviour.
func TestToolUsageNeverStartedCalls(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	endpoint := closed.URL + "/mcp"
	closed.Close()
	refused := func(c *Connection, o *Options) {
		c.Endpoint = endpoint
		o.HTTPClient = &http.Client{}
	}
	run := runToolUsage(t, jsonResult, MeteringProfileToolUsage, refused)
	if run.upCalls != 0 || len(run.lease.results) != 1 || !run.lease.results[0].NotSent || run.lease.results[0].Err == nil {
		t.Fatalf("refused connection: %+v", run.lease.results)
	}
	run = runToolUsage(t, jsonResult, "request-count", refused)
	if len(run.lease.results) != 1 || run.lease.results[0].NotSent {
		t.Fatalf("a request-count lease must keep today's result: %+v", run.lease.results)
	}

	forbidden := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
	}
	run = runToolUsage(t, forbidden, MeteringProfileToolUsage, nil)
	if run.code == 200 || len(run.lease.results) != 1 || !run.lease.results[0].Rejected || !run.lease.results[0].ResponseComplete || run.lease.results[0].StatusCode != 403 || len(run.lease.observations) != 0 {
		t.Fatalf("403: %+v %+v", run.lease.results, run.lease.observations)
	}
	run = runToolUsage(t, forbidden, "request-count", nil)
	if len(run.lease.results) != 1 || run.lease.results[0].Rejected || run.lease.results[0].ResponseComplete {
		t.Fatalf("a request-count lease must keep today's result: %+v", run.lease.results)
	}
}

// A credential that resolves for the pre-dispatch check but not for the send
// is a call that was never sent: a tool-usage lease settles it at zero; a
// request-count lease keeps today's result.
func TestToolUsageCredentialFailureAtSendIsNotSent(t *testing.T) {
	for profile, wantNotSent := range map[string]bool{MeteringProfileToolUsage: true, "request-count": false} {
		var tokens atomic.Int32
		run := runToolUsage(t, jsonResult, profile, func(_ *Connection, o *Options) {
			o.TokenSource = TokenSourceFunc(func(context.Context) (string, error) {
				if tokens.Add(1) > 1 {
					return "", errors.New("secret reference cannot be resolved")
				}
				return "operator-token", nil
			})
		})
		if run.code != http.StatusServiceUnavailable || run.upCalls != 0 || run.lease.claims != 1 {
			t.Fatalf("%s: %d calls=%d claims=%d", profile, run.code, run.upCalls, run.lease.claims)
		}
		if len(run.lease.results) != 1 || run.lease.results[0].NotSent != wantNotSent {
			t.Fatalf("%s: %+v", profile, run.lease.results)
		}
	}
}

func TestToolUsageDurationIsRoundedUpAndClampedToTheBound(t *testing.T) {
	bound := 5 * time.Second
	for elapsed, want := range map[time.Duration]int64{time.Nanosecond: 1, time.Millisecond: 1, time.Millisecond + 1: 2, 1500 * time.Microsecond: 2, bound: 5000, bound + 3*time.Millisecond: 5000} {
		if got := durationMilliseconds(elapsed, bound); got != want {
			t.Errorf("%v -> %d ms, want %d", elapsed, got, want)
		}
	}
}

func TestToolConnectionBoundsValidate(t *testing.T) {
	base := Connection{ID: "tool", Endpoint: "https://tools.example/mcp", IdentityRef: "host", AllowedTools: []string{"search"}, ProtocolVersion: "2025-03-26", Mode: StatelessJSON}
	for _, bad := range []func(*Connection){
		func(c *Connection) { c.MaxResponseBytes = -1 },
		func(c *Connection) { c.MaxResponseBytes = 64<<20 + 1 },
		func(c *Connection) { c.MaxDurationSeconds = -1 },
		func(c *Connection) { c.MaxDurationSeconds = 1801 },
	} {
		c := base
		bad(&c)
		if c.Validate() == nil {
			t.Fatalf("invalid bounds accepted: %+v", c)
		}
	}
	good := base
	good.MaxResponseBytes, good.MaxDurationSeconds = 1<<20, 30
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
}

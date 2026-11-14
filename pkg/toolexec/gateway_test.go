package toolexec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

const toolBody = ` {"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"search","arguments":{"password":"legitimate-tool-field","query":"hello"}}} `
const toolResult = `{"jsonrpc":"2.0","id":7,"result":{"content":[{"type":"text","text":"native result"}]}}`

type recordingLease struct {
	events                          *[]string
	claimErr, observeErr, finishErr error
	claims, aborts                  int
	observations                    []execution.Observation
	results                         []execution.Result
}

func (l *recordingLease) BeforeDispatch(context.Context) error {
	l.claims++
	if l.events != nil {
		*l.events = append(*l.events, "claim")
	}
	return l.claimErr
}
func (l *recordingLease) Observe(_ context.Context, o execution.Observation) error {
	l.observations = append(l.observations, o)
	if l.events != nil {
		*l.events = append(*l.events, "observe")
	}
	return l.observeErr
}
func (l *recordingLease) Resource(context.Context, execution.Resource) error { return nil }
func (l *recordingLease) Finish(_ context.Context, r execution.Result) error {
	l.results = append(l.results, r)
	if l.events != nil {
		*l.events = append(*l.events, "finish")
	}
	return l.finishErr
}
func (l *recordingLease) Abort(context.Context) error { l.aborts++; return nil }

func toolOptions(server *httptest.Server, lease *recordingLease) Options {
	return Options{HTTPClient: server.Client(), TokenSource: TokenSourceFunc(func(context.Context) (string, error) { return "operator-token", nil }), Context: func(*http.Request) (contracts.RequestContext, error) {
		return contracts.RequestContext{Version: contracts.Version, TenantID: "tenant", RequestID: "correlation", PrincipalID: "principal", ResourceID: "tool"}, nil
	}, Authorize: func(context.Context, contracts.RequestContext, Connection, Request) error { return nil }, Prepare: func(context.Context, Invocation) (execution.Lease, error) { return lease, nil }, AllowHTTPForTests: true}
}
func toolConnection(server *httptest.Server) Connection {
	return Connection{ID: "tool", Endpoint: server.URL + "/mcp", IdentityRef: "host", AllowedTools: []string{"search"}, ProtocolVersion: "2025-03-26", Mode: StatelessJSON}
}
func toolRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://gateway.example/invoke", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestGatewayBindsValidatedBytesAndAcknowledgesBeforeForwarding(t *testing.T) {
	events := []string{}
	lease := &recordingLease{events: &events}
	var upstreamBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events = append(events, "dispatch")
		body, _ := io.ReadAll(r.Body)
		upstreamBody = string(body)
		if r.URL.Path != "/mcp" || r.Header.Get("Authorization") != "Bearer operator-token" || r.Header.Get("Cookie") != "" || r.Header.Get("Idempotency-Key") != "" || r.Header.Get("Accept") != "application/json" || r.Header.Get("MCP-Protocol-Version") != "2025-03-26" {
			t.Errorf("unsafe upstream request %+v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Request-ID", "provider-id")
		w.Header().Set("Set-Cookie", "never-forward")
		_, _ = io.WriteString(w, toolResult)
	}))
	defer server.Close()
	opts := toolOptions(server, lease)
	opts.Authorize = func(_ context.Context, _ contracts.RequestContext, c Connection, r Request) error {
		c.AllowedTools[0] = "untrusted"
		r.Body[0] = '['
		r.Arguments[0] = '['
		r.ID[0] = '9'
		return nil
	}
	opts.Prepare = func(_ context.Context, in Invocation) (execution.Lease, error) {
		if in.Request.Name != "search" || in.Connection.AllowedTools[0] != "search" || string(in.Request.Body) != toolBody || in.Headers.Get("Authorization") != "" || in.Headers.Get("Idempotency-Key") != "attempt-key" {
			t.Fatal("callback snapshots were not isolated", in.Headers)
		}
		in.Request.Body[0] = '['
		in.Request.Arguments[0] = '['
		in.Request.ID[0] = '9'
		return lease, nil
	}
	gateway, err := NewGateway(toolConnection(server), opts)
	if err != nil {
		t.Fatal(err)
	}
	r := toolRequest(toolBody)
	r.Header.Set("Authorization", "Bearer caller-token")
	r.Header.Set("Cookie", "caller-cookie")
	r.Header.Set("Idempotency-Key", "attempt-key")
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != toolResult || upstreamBody != toolBody || w.Header().Get("Set-Cookie") != "" || w.Header().Get("X-Request-ID") != "provider-id" {
		t.Fatalf("native forwarding changed: status=%d body=%s upstream=%s", w.Code, w.Body.String(), upstreamBody)
	}
	if !reflect.DeepEqual(events, []string{"claim", "dispatch", "observe", "finish"}) || lease.aborts != 0 || len(lease.results) != 1 || !lease.results[0].Terminal || !lease.results[0].ResponseComplete {
		t.Fatalf("lifecycle mismatch %+v %+v", events, lease)
	}
	if len(lease.observations) != 1 || lease.observations[0].Counters["requests"] != 1 || lease.observations[0].Counters["tool_calls"] != 1 || lease.observations[0].Counters[CounterToolResponseBytes] != int64(len(toolResult)) || !lease.observations[0].Terminal {
		t.Fatal(lease.observations)
	}
}

func TestGatewayRetainsUncertainWork(t *testing.T) {
	for _, test := range []struct {
		name                       string
		status                     int
		contentType, body, session string
		max                        int64
	}{
		{"wrong id", 200, "application/json", strings.Replace(toolResult, `"id":7`, `"id":8`, 1), "", 0},
		{"duplicate id", 200, "application/json", `{"jsonrpc":"2.0","id":7,"id":8,"result":{}}`, "", 0},
		{"truncated", 200, "application/json", `{"jsonrpc":"2.0","id":7,"result":`, "", 0},
		{"SSE", 200, "text/event-stream", "data: " + toolResult + "\n\n", "", 0},
		{"non success", 500, "application/json", `{"jsonrpc":"2.0","id":7,"error":{"code":-1,"message":"failed"}}`, "", 0},
		{"session", 200, "application/json", toolResult, "new-session", 0},
		{"oversized", 200, "application/json", toolResult, "", 16},
		{"whitespace", 200, "application/json", "   \n\t", "", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				if test.session != "" {
					w.Header().Set("MCP-Session-Id", test.session)
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			lease := &recordingLease{}
			opts := toolOptions(server, lease)
			opts.MaxResponseBytes = test.max
			gateway, err := NewGateway(toolConnection(server), opts)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			gateway.ServeHTTP(w, toolRequest(toolBody))
			if w.Code != 502 || lease.claims != 1 || lease.aborts != 0 || len(lease.observations) != 0 || len(lease.results) != 1 || lease.results[0].Terminal || lease.results[0].Err == nil || lease.results[0].StatusCode != test.status {
				t.Fatalf("uncertain work became consumption/refund: status=%d %+v", w.Code, lease)
			}
		})
	}
}

func TestGatewayPreDispatchFailuresAndAcknowledgementFailures(t *testing.T) {
	for _, test := range []string{"credential", "claim", "observe", "finish", "denied", "nil lease"} {
		t.Run(test, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, toolResult)
			}))
			defer server.Close()
			lease := &recordingLease{}
			opts := toolOptions(server, lease)
			switch test {
			case "credential":
				opts.TokenSource = TokenSourceFunc(func(context.Context) (string, error) { return "", errors.New("secret backend failed") })
			case "claim":
				lease.claimErr = &execution.StatusError{Status: 409, Code: "STALE", Message: "configuration changed"}
			case "observe":
				lease.observeErr = errors.New("journal unavailable")
			case "finish":
				lease.finishErr = errors.New("settlement unavailable")
			case "denied":
				opts.Prepare = func(context.Context, Invocation) (execution.Lease, error) {
					return nil, &execution.StatusError{Status: 429, Code: "QUOTA", Message: "quota denied", RetryAfter: 1500 * time.Millisecond}
				}
			case "nil lease":
				opts.Prepare = func(context.Context, Invocation) (execution.Lease, error) { return nil, nil }
			}
			gateway, err := NewGateway(toolConnection(server), opts)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			gateway.ServeHTTP(w, toolRequest(toolBody))
			if w.Code < 400 || strings.Contains(w.Body.String(), "native result") {
				t.Fatal("unacknowledged body exposed", w.Code, w.Body.String())
			}
			if test == "denied" && w.Header().Get("Retry-After") != "2" {
				t.Fatal("retry delay was not rounded safely", w.Header())
			}
			switch test {
			case "credential", "claim":
				if calls != 0 || lease.aborts != 1 || len(lease.results) != 0 {
					t.Fatal(calls, lease)
				}
			case "denied", "nil lease":
				if calls != 0 || lease.aborts != 0 || lease.claims != 0 {
					t.Fatal(calls, lease)
				}
			case "observe":
				if calls != 1 || lease.aborts != 0 || len(lease.results) != 1 || lease.results[0].Terminal {
					t.Fatal(calls, lease)
				}
			case "finish":
				if calls != 1 || lease.aborts != 0 || len(lease.results) != 1 || !lease.results[0].Terminal {
					t.Fatal("terminal acknowledgement was replaced with uncertainty", calls, lease)
				}
			}
		})
	}
}

func TestProtocolRejectsAmbiguityAndSessionShapes(t *testing.T) {
	for _, body := range []string{"", " ", `[]`, `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"search"}}`, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"other","name":"search"}}`, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"x":1,"x":2}}}`, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"name":"search"}}`, `{"jsonrpc":"2.0","id":null,"method":"tools/call","params":{"name":"search"}}`, `{"jsonrpc":"2.0","id":1.5,"method":"tools/call","params":{"name":"search"}}`, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":null}}`, toolBody + `{}`} {
		if _, err := ParseRequest([]byte(body), []string{"search"}); err == nil {
			t.Fatalf("accepted ambiguous call %s", body)
		}
	}
	first, err := ParseRequest([]byte(toolBody), []string{"search"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseRequest([]byte(strings.Replace(toolBody, `"id":7`, `"id":"new-correlation"`, 1)), []string{"search"})
	if err != nil {
		t.Fatal(err)
	}
	if first.SemanticDigest != second.SemanticDigest || first.PayloadDigest == second.PayloadDigest {
		t.Fatal("RPC correlation changed business semantics")
	}
	for _, body := range []string{`{"jsonrpc":"2.0","id":7,"result":{},"error":{"code":-1,"message":"x"}}`, `{"jsonrpc":"2.0","id":7,"error":null}`, `{"jsonrpc":"2.0","id":"7","result":{}}`} {
		if err := ValidateResponse([]byte(body), json.RawMessage(`7`)); err == nil {
			t.Fatalf("accepted ambiguous response %s", body)
		}
	}
	if err := ValidateResponse([]byte(`{"jsonrpc":"2.0","id":7,"error":{"code":-1,"message":"tool error"}}`), json.RawMessage(`7`)); err != nil {
		t.Fatal("valid completed error was not terminal", err)
	}
}

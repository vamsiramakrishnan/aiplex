package toolexec

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamableToolResponsePreservesBytesAfterTerminalAcknowledgement(t *testing.T) {
	native := ": heartbeat\r\nevent: message\r\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":1}}\r\n\r\nid: final\r\nevent: message\r\ndata: " + toolResult + "\r\n\r\n"
	for _, ackFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(ackFailure), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept") != "application/json, text/event-stream" {
					t.Error("streamable content negotiation missing")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, native)
			}))
			defer server.Close()
			lease := &recordingLease{}
			if ackFailure {
				lease.observeErr = errors.New("durability unavailable")
			}
			connection := toolConnection(server)
			connection.Mode = StatelessStreamableHTTP
			gateway, e := NewGateway(connection, toolOptions(server, lease))
			if e != nil {
				t.Fatal(e)
			}
			w := httptest.NewRecorder()
			gateway.ServeHTTP(w, toolRequest(toolBody))
			if ackFailure {
				if w.Code == 200 || strings.Contains(w.Body.String(), toolResult) {
					t.Fatal("unacknowledged SSE terminal released")
				}
			} else {
				if w.Code != 200 || w.Body.String() != native || len(lease.observations) != 1 || !lease.observations[0].Terminal {
					t.Fatal("native SSE bytes/accounting changed", w.Code, w.Body.String(), lease.observations)
				}
			}
		})
	}
}
func TestStreamableToolRejectsUncertainOrForeignProtocolMessages(t *testing.T) {
	for name, body := range map[string]string{
		"missing final":  "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n",
		"wrong id":       "data: {\"jsonrpc\":\"2.0\",\"id\":8,\"result\":{}}\n\n",
		"truncated":      "data: " + toolResult + "\n",
		"server request": "data: {\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"sampling/createMessage\",\"params\":{}}\n\n",
		"post terminal":  "data: " + toolResult + "\n\ndata: {}\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			if validateStreamResponse([]byte(body), []byte("7")) == nil {
				t.Fatal("uncertain stream accepted")
			}
		})
	}
}
func TestBeforeDispatchGrantFailureCannotClaimOrReachProvider(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	lease := &recordingLease{}
	opts := toolOptions(server, lease)
	opts.BeforeDispatch = func(context.Context) error { return errors.New("grant revoked") }
	gateway, e := NewGateway(toolConnection(server), opts)
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, toolRequest(toolBody))
	if calls != 0 || lease.claims != 0 || lease.aborts != 1 {
		t.Fatal("revoked grant crossed dispatch boundary", calls, lease.claims, lease.aborts)
	}
}

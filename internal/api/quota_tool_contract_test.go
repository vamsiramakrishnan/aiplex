package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
)

func managedToolStreamMode(t *testing.T, h *ConnectedHandler) {
	t.Helper()
	record, err := h.Store.Get(context.Background(), "tenant", "tool-connection", "surface")
	if err != nil {
		t.Fatal(err)
	}
	var connection ToolConnection
	if err = json.Unmarshal(record.Spec, &connection); err != nil {
		t.Fatal(err)
	}
	connection.Execution.Mode = toolexec.StatelessStreamableHTTP
	record.Spec, err = json.Marshal(connection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Store.Put(context.Background(), record, record.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestManagedToolNativeFinalityIsAcknowledgedBeforeDelivery(t *testing.T) {
	const rpcError = `{"jsonrpc":"2.0","id":"rpc","error":{"code":-32000,"message":"native failure"}}`
	for _, test := range []struct{ name, media, body string }{
		{"JSON business error", "application/json", rpcError},
		{"finite SSE", "text/event-stream", ": native comment\r\nevent: message\r\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":1}}\r\n\r\nevent: message\r\nid: native-final\r\ndata: " + managedToolResponse + "\r\n\r\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, _ := io.ReadAll(r.Body)
				if string(body) != managedToolBody || r.Header.Get("Idempotency-Key") != "" || r.Header.Get("Authorization") != "Bearer operator-tool-token" {
					t.Error("native tool bytes or credential boundary changed")
				}
				w.Header().Set("Content-Type", test.media)
				io.WriteString(w, test.body)
			}))
			defer upstream.Close()
			t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
			h, quota, request := protectedToolFixture(t, upstream.URL+"/mcp")
			if test.media == "text/event-stream" {
				managedToolStreamMode(t, h)
			}
			written := false
			w := &quotaHostModelInspectWriter{ResponseRecorder: httptest.NewRecorder()}
			w.beforeWrite = func([]byte) {
				written = true
				id := w.Header().Get(execution.AttemptHeader)
				engine := quota.Engines()["tenant"]
				info, err := engine.GetAttempt(context.Background(), "tenant", id)
				if err != nil || info.Decision.State != "settled" || info.Actual["calls"] != 1 || info.Actual["requests"] != 1 {
					t.Fatal("tool result escaped before ledger acknowledgement", info, err)
				}
				event, _ := quotaDigest([]string{"tenant", id, "native-tool", "terminal"})
				ack, err := engine.GetUsageAcknowledgement(context.Background(), "tenant", event)
				if err != nil || ack.Status != quotaplex.UsageSettled {
					t.Fatal("tool result escaped before durable terminal evidence", ack, err)
				}
			}
			h.NativeTool(w, request("native-final"))
			if w.Code != 200 || w.Body.String() != test.body || calls.Load() != 1 || !written {
				t.Fatal("native result/error/SSE semantics changed", w.Code, w.Body.String(), calls.Load())
			}
		})
	}
}

func TestManagedToolIncompleteExecutionRetainsSharedExposure(t *testing.T) {
	for _, ending := range []string{"truncated JSON", "SSE without final RPC", "client cancellation"} {
		t.Run(ending, func(t *testing.T) {
			entered := make(chan struct{})
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if ending == "SSE without final RPC" {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":1}}\n\n")
					return
				}
				io.WriteString(w, `{"jsonrpc":"2.0","id":"rpc","result":`)
				if ending == "client cancellation" {
					w.(http.Flusher).Flush()
					close(entered)
					<-r.Context().Done()
				}
			}))
			defer upstream.Close()
			t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
			h, quota, request := protectedToolFixture(t, upstream.URL+"/mcp")
			if ending == "SSE without final RPC" {
				managedToolStreamMode(t, h)
			}
			r := request("uncertain-tool")
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); h.NativeTool(w, r.WithContext(ctx)) }()
			if ending == "client cancellation" {
				select {
				case <-entered:
					cancel()
				case <-ctx.Done():
					t.Fatal("tool dispatch did not start")
				}
			}
			select {
			case <-done:
			case <-time.After(4 * time.Second):
				t.Fatal("incomplete tool execution did not terminate")
			}
			id := w.Header().Get(execution.AttemptHeader)
			engine := quota.Engines()["tenant"]
			info, err := engine.GetAttempt(context.Background(), "tenant", id)
			if err != nil || info.Decision.State != "unknown" || info.Claim.Fence == 0 || calls.Load() != 1 || w.Code != 502 {
				t.Fatal("incomplete execution lost its attempt/unknown exposure", info, err, w.Code)
			}
			balance, err := engine.BudgetBalance(context.Background(), "tenant", info.Plan.Budgets[0].Key)
			if err != nil || balance.Held != 1 || balance.Consumed != 0 {
				t.Fatal("possibly executed tool work was refunded", balance, err)
			}
			for _, test := range []struct {
				key  string
				want int
			}{{"uncertain-tool", 409}, {"fresh-tool", 429}} {
				next := httptest.NewRecorder()
				h.NativeTool(next, request(test.key))
				if next.Code != test.want || calls.Load() != 1 || strings.Contains(next.Body.String(), "operator-tool-token") {
					t.Fatal("unknown work bypassed replay/remaining exposure", next.Code, next.Body.String(), calls.Load())
				}
			}
		})
	}
}

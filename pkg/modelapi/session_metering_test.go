package modelapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// tokenSessionRun is one token-metered replay: every admitted exchange
// (response or allowance window) is its own lease.
type tokenSessionRun struct {
	leases  []*collectingLease
	bodies  [][]byte
	replies [][]byte
	dials   int
}

func (r tokenSessionRun) settledSum() (map[string]int64, int, int) {
	var acc usageledger.Accumulator
	settled, open := 0, 0
	for _, lease := range r.leases {
		lease.mu.Lock()
		terminal := false
		for _, result := range lease.results {
			terminal = terminal || (result.Terminal && result.Err == nil)
		}
		if terminal {
			settled++
			for _, o := range lease.observations {
				if o.Terminal {
					acc.Observe(o.Counters, false)
				}
			}
		} else {
			open++
		}
		lease.mu.Unlock()
	}
	return acc.Snapshot(), settled, open
}

// runTokenSession replays a WebSocket fixture against a token-metered
// surface. The fake upstream waits for an admitted client event before each
// response.created, as a real provider only creates admitted responses.
func runTokenSession(t *testing.T, route fixtureRoute, data []byte, allowance int64) tokenSessionRun {
	t.Helper()
	interrupted := strings.Contains(route.file, "interrupted")
	kind := route.call.ws
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if kind == "live" || kind == "realtime" {
			if _, _, err = conn.ReadMessage(); err != nil { // setup / session.update
				return
			}
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			if bytes.Contains(line, []byte(`"type":"response.created"`)) {
				if _, _, err = conn.ReadMessage(); err != nil {
					return
				}
			}
			if conn.WriteMessage(websocket.TextMessage, line) != nil {
				return
			}
		}
		if interrupted {
			_ = conn.UnderlyingConn().Close()
			return
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	s := route.call.surface(upstream.URL)
	s.WebSocket = &WebSocketSessions{Metering: SessionMeteringToken, TokenAllowance: allowance}
	opts := options(t, upstream.URL, newOwnership())
	if s.Provider != nil {
		opts.TokenSource = nil
		opts.Credential = NoCredential()
	}
	var mu sync.Mutex
	run := tokenSessionRun{}
	opts.Execution = ExecutionPreparerFunc(func(_ context.Context, request ExecutionRequest) (execution.Lease, error) {
		mu.Lock()
		defer mu.Unlock()
		lease := &collectingLease{}
		run.leases = append(run.leases, lease)
		run.bodies = append(run.bodies, request.Body)
		return lease, nil
	})
	g, err := NewGateway(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		g.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+route.call.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	responses := bytes.Count(data, []byte(`"type":"response.created"`))
	create := []byte(`{"type":"response.create","response":{"max_output_tokens":64}}`)
	switch kind {
	case "live":
		_ = conn.WriteMessage(websocket.TextMessage, []byte(route.call.body))
	case "realtime":
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.update","session":{"type":"realtime","audio":{"input":{"turn_detection":null}}}}`))
	case "responses":
		create = []byte(`{"type":"response.create","model":"fixture-model-b","input":"hi","max_output_tokens":64,"store":false}`)
	}
	if responses > 0 {
		_ = conn.WriteMessage(websocket.TextMessage, create)
		responses--
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var event struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(payload, &event)
		if event.Type == "error" {
			run.replies = append(run.replies, payload)
		}
		if responses > 0 && (event.Type == "response.done" || event.Type == "response.completed" || event.Type == "response.failed" || event.Type == "response.incomplete") {
			_ = conn.WriteMessage(websocket.TextMessage, create)
			responses--
		}
	}
	conn.Close()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not finish")
	}
	return run
}

// checkTokenSessionFixture verifies token-metered enforcement on a session
// fixture: every admitted exchange settles exactly or retains its hold, and
// the settled exchanges add up to the fixture's usage (for an interrupted
// Live session: the settled windows are the reported lower bound and the
// open window is retained).
func checkTokenSessionFixture(t *testing.T, route fixtureRoute, data []byte, want canonical) {
	t.Helper()
	allowance := int64(0)
	if route.call.ws == "live" {
		allowance = MinTokenAllowance // small enough to force renewals
	}
	run := runTokenSession(t, route, data, allowance)
	if len(run.leases) == 0 {
		t.Fatal("token-metered session admitted nothing")
	}
	sum, settled, open := run.settledSum()
	t.Logf("token session: %d admitted, %d settled, %d retained, %d refusals; settled %v", len(run.leases), settled, open, len(run.replies), sum)
	if route.final && open != 0 {
		t.Fatalf("complete session left %d exchanges unsettled", open)
	}
	if !route.final && open == 0 {
		t.Fatal("incomplete session released every hold")
	}
	got := observedCanonical(route.native, sum)
	if settled == 0 {
		got = canonical{}
	}
	for field, value := range got {
		if want[field] == nil && value != nil && *value == 0 {
			got[field] = nil // omitted proto3 counters settle as 0
		}
	}
	if route.native == natOpenAI && route.call.ws != "live" {
		// Realtime/Responses settle per response; the canonical total is
		// the sum of the per-response totals.
		if got["total_tokens"] == nil && got["input_tokens"] != nil && got["output_tokens"] != nil {
			deriveTotal(got)
		}
	}
	if diffs := compareCanonical(want, got, route.allowUnknown); len(diffs) > 0 {
		t.Errorf("token-metered settlement mismatch: %s\n want %s\n got  %s", strings.Join(diffs, "; "), describe(want), describe(got))
	}
}

func TestTokenSessionRefusesUnadmittedResponses(t *testing.T) {
	route := fixtureRoute{provider: "openai", file: "realtime.ws.jsonl", call: callRealtime, native: natOpenAI, final: true}
	data := []byte(`{"type":"response.created","response":{"id":"resp_auto"}}` + "\n")
	// The client never sends session.update: the gateway refuses its
	// response.create with a native error and admits nothing.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, data[:len(data)-1])
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	s := route.call.surface(upstream.URL)
	s.WebSocket = &WebSocketSessions{Metering: SessionMeteringToken}
	opts := options(t, upstream.URL, newOwnership())
	opts.TokenSource, opts.Credential = nil, NoCredential()
	admitted := 0
	opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) {
		admitted++
		return &collectingLease{}, nil
	})
	g, err := NewGateway(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(g)
	defer gateway.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+route.call.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var closeErr *websocket.CloseError
	for {
		if _, _, err = conn.ReadMessage(); err != nil {
			break
		}
	}
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.ClosePolicyViolation || admitted != 0 {
		t.Fatalf("unadmitted response not refused: %v admitted=%d", err, admitted)
	}
}

func TestWebSocketSessionSettingsValidation(t *testing.T) {
	for _, bad := range []WebSocketSessions{{Metering: "per-turn"}, {MaxSessionSeconds: 10}, {MaxSessionSeconds: 90000}, {Metering: SessionMeteringToken, TokenAllowance: 10}, {TokenAllowance: 4096}} {
		if bad.validate() == nil {
			t.Fatalf("invalid session settings accepted: %+v", bad)
		}
	}
	good := &WebSocketSessions{Metering: SessionMeteringToken, MaxSessionSeconds: 600}
	if good.validate() != nil || good.maxSession(time.Hour) != 10*time.Minute || (Surface{WebSocket: good}).SessionAllowance() != DefaultTokenAllowance {
		t.Fatal("valid session settings misread")
	}
	if (Surface{}).SessionAllowance() != 0 {
		t.Fatal("session-metered surface reports an allowance")
	}
}

func TestRealtimeTokenCountersRequireConsistentBreakdown(t *testing.T) {
	ok := `{"total_tokens":253,"input_tokens":132,"output_tokens":121,"input_token_details":{"text_tokens":119,"audio_tokens":13,"image_tokens":0,"cached_tokens":64,"cached_tokens_details":{"text_tokens":64,"audio_tokens":0,"image_tokens":0}},"output_token_details":{"text_tokens":30,"audio_tokens":91}}`
	counters, err := realtimeTokenCounters(json.RawMessage(ok))
	if err != nil || counters["uncached_text_input_tokens"] != 55 || counters["cached_text_input_tokens"] != 64 || counters["audio_output_tokens"] != 91 {
		t.Fatal(counters, err)
	}
	for _, bad := range []string{
		`{"input_tokens":132,"output_tokens":121}`,
		strings.Replace(ok, `"audio_tokens":13`, `"audio_tokens":14`, 1),
		strings.Replace(ok, `"cached_tokens":64`, `"cached_tokens":65`, 1),
		strings.Replace(ok, `"total_tokens":253`, `"total_tokens":254`, 1),
	} {
		if _, err := realtimeTokenCounters(json.RawMessage(bad)); err == nil {
			t.Fatal("inconsistent Realtime usage accepted", bad)
		}
	}
}

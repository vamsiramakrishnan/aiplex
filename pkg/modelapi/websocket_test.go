package modelapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
)

type wsScript struct {
	readFirst bool
	messages  []string
	// waitClient makes the upstream relay client frames until the client
	// closes, recording them.
	waitClient bool
	mu         sync.Mutex
	received   []string
	headers    http.Header
	query      string
}

func (s *wsScript) server(t *testing.T) *httptest.Server {
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.headers, s.query = r.Header.Clone(), r.URL.RawQuery
		s.mu.Unlock()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if s.readFirst {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
		}
		for _, m := range s.messages {
			if conn.WriteMessage(websocket.TextMessage, []byte(m)) != nil {
				return
			}
		}
		if s.waitClient {
			for {
				_, payload, err := conn.ReadMessage()
				if err != nil {
					return
				}
				s.mu.Lock()
				s.received = append(s.received, string(payload))
				s.mu.Unlock()
			}
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
}

func dialGateway(t *testing.T, g *Gateway, path string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	gateway := httptest.NewServer(g)
	t.Cleanup(gateway.Close)
	return websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+path, header)
}

func drain(conn *websocket.Conn) error {
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return err
		}
	}
}

var observedMu sync.Mutex

func realtimeGateway(t *testing.T, origin string, observed *[]UsageObservation, limits *[]providerlimits.Limits) *Gateway {
	t.Helper()
	s := callRealtime.surface(origin)
	opts := options(t, origin, newOwnership())
	opts.TokenSource = nil
	opts.Credential = headerCredential("Authorization", "Bearer ")
	opts.Observe = func(_ context.Context, _ Owner, value UsageObservation) {
		observedMu.Lock()
		defer observedMu.Unlock()
		*observed = append(*observed, value)
	}
	opts.ObserveLimits = func(_ context.Context, _ Owner, value providerlimits.Limits) {
		observedMu.Lock()
		defer observedMu.Unlock()
		*limits = append(*limits, value)
	}
	g, err := NewGateway(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestRealtimeRelayCredentialsModelPinLimitsAndTranscription(t *testing.T) {
	script := &wsScript{messages: []string{
		`{"type":"session.created","session":{"type":"realtime","model":"fixture-model-c"}}`,
		`{"type":"rate_limits.updated","rate_limits":[{"name":"requests","limit":100,"remaining":99,"reset_seconds":1.5},{"name":"tokens","limit":5000,"remaining":4000,"reset_seconds":60}]}`,
		`{"type":"conversation.item.input_audio_transcription.completed","usage":{"type":"duration","seconds":2.0005}}`,
	}, waitClient: true}
	up := script.server(t)
	defer up.Close()
	var observed []UsageObservation
	var limits []providerlimits.Limits
	g := realtimeGateway(t, up.URL, &observed, &limits)
	if _, response, err := dialGateway(t, g, "/v1/realtime?model=other-model", nil); err == nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("unlisted Realtime model admitted", err)
	}
	if _, response, err := dialGateway(t, g, "/v1/realtime?model=fixture-model-c&call_id=x", nil); err == nil || response.StatusCode != http.StatusBadRequest {
		t.Fatal("unscoped SIP sideband call admitted", err)
	}
	conn, _, err := dialGateway(t, g, "/v1/realtime?model=fixture-model-c", http.Header{"Authorization": {"Bearer caller-token"}})
	if err != nil {
		t.Fatal(err)
	}
	for range script.messages {
		if _, _, err = conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.update","session":{"type":"realtime","model":"other-model"}}`))
	var closeErr *websocket.CloseError
	if err = drain(conn); !websocketCloseCode(err, &closeErr) || closeErr.Code != websocket.ClosePolicyViolation {
		t.Fatal("model switch not refused", err)
	}
	// Wait for the relay's session-end observation before reading shared state.
	for deadline := time.Now().Add(2 * time.Second); ; {
		observedMu.Lock()
		ended := len(observed) > 0 && (observed[len(observed)-1].Terminal || observed[len(observed)-1].Bound != "")
		observedMu.Unlock()
		if ended {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session end not observed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	observedMu.Lock()
	defer observedMu.Unlock()
	script.mu.Lock()
	defer script.mu.Unlock()
	if len(script.received) != 0 {
		t.Fatal("denied client event reached upstream", script.received)
	}
	if script.headers.Get("Authorization") != "Bearer "+providerSecret || script.query != "model=fixture-model-c" {
		t.Fatal("upstream credential or query", script.headers, script.query)
	}
	inBand := false
	for _, l := range limits {
		if l.Requests != nil && l.Requests.Remaining != nil && *l.Requests.Remaining == 99 && l.Tokens != nil && *l.Tokens.Limit == 5000 && l.Requests.ResetAt != nil {
			inBand = true
		}
	}
	if !inBand {
		t.Fatal("rate_limits.updated not observed", limits)
	}
	transcription := false
	for _, o := range observed {
		if o.Counters["transcription_audio_milliseconds"] == 2001 {
			transcription = true
		}
		if _, merged := o.Counters["input_tokens"]; merged && o.Counters["transcription_audio_milliseconds"] > 0 {
			t.Fatal("transcription merged into session tokens", o)
		}
	}
	if !transcription || observed[len(observed)-1].Terminal {
		t.Fatal("transcription seconds not observed separately / policy-closed session settled", observed)
	}
}

func websocketCloseCode(err error, target **websocket.CloseError) bool {
	ce, ok := err.(*websocket.CloseError)
	if ok {
		*target = ce
	}
	return ok
}

func TestResponsesWebSocketAuthorizesEachResponseCreate(t *testing.T) {
	script := &wsScript{messages: []string{`{"type":"response.created","response":{"id":"resp_owned","status":"in_progress","usage":null}}`, `{"type":"response.completed","response":{"id":"resp_owned","status":"completed","usage":{"input_tokens":5,"input_tokens_details":{"cached_tokens":0},"output_tokens":2,"total_tokens":7}}}`}, waitClient: true}
	up := script.server(t)
	defer up.Close()
	ownership := newOwnership()
	s := callResponsesWS.surface(up.URL)
	opts := options(t, up.URL, ownership)
	opts.TokenSource, opts.Credential = nil, NoCredential()
	g, err := NewGateway(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	_ = ownership.Bind(context.Background(), Owner{"tenant", "mallory", "native"}, Resource{"response", "resp_foreign"})
	for _, event := range []string{
		`{"type":"response.create","model":"not-allowed","input":"x"}`,
		`{"type":"response.create","model":"fixture-model-b","input":"x","previous_response_id":"resp_foreign"}`,
		`{"type":"response.create","model":"fixture-model-b","input":"x","conversation":"conv_1"}`,
		`{"type":"response.create","Model":"fixture-model-b","model":"fixture-model-b"}`,
	} {
		conn, _, err := dialGateway(t, g, "/v1/responses", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(event))
		var closeErr *websocket.CloseError
		if err = drain(conn); !websocketCloseCode(err, &closeErr) || closeErr.Code != websocket.ClosePolicyViolation {
			t.Fatal("unauthorized response.create relayed", event, err)
		}
	}
	script.mu.Lock()
	received := len(script.received)
	script.mu.Unlock()
	if received != 0 {
		t.Fatal("denied response.create reached upstream", script.received)
	}
	if ownership.Check(context.Background(), Owner{"tenant", "alice", "native"}, Resource{"response", "resp_owned"}) != nil {
		t.Fatal("created response not bound to the caller")
	}
}

// Live: a resumed session's usage is ambiguous; activity after the last
// snapshot leaves it a lower bound; a regressing snapshot is ambiguous.
func TestLiveSessionUsageLabels(t *testing.T) {
	usage := func(prompt, response int) string {
		return `{"usageMetadata":{"promptTokenCount":` + itoa(prompt) + `,"responseTokenCount":` + itoa(response) + `,"totalTokenCount":` + itoa(prompt+response) + `}}`
	}
	for _, test := range []struct {
		name, setup string
		messages    []string
		want        string
	}{
		{"final", `{"setup":{"model":"model-a"}}`, []string{`{"serverContent":{"turnComplete":true}}`, usage(10, 5)}, ""},
		{"output after snapshot", `{"setup":{"model":"model-a"}}`, []string{usage(10, 5), `{"serverContent":{"modelTurn":{"parts":[]}}}`}, "lower_bound"},
		{"regression", `{"setup":{"model":"model-a"}}`, []string{usage(10, 5), usage(4, 2)}, "ambiguous"},
		{"resumed", `{"setup":{"model":"model-a","sessionResumption":{"handle":"h1"}}}`, []string{usage(10, 5)}, "ambiguous"},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := &wsScript{readFirst: true, messages: test.messages}
			up := script.server(t)
			defer up.Close()
			ownership := newOwnership()
			_ = ownership.Bind(context.Background(), Owner{"tenant", "alice", "native"}, Resource{"live-session", "h1"})
			opts := options(t, up.URL, ownership)
			var mu sync.Mutex
			var observed []UsageObservation
			opts.Observe = func(_ context.Context, _ Owner, value UsageObservation) {
				mu.Lock()
				defer mu.Unlock()
				observed = append(observed, value)
			}
			g, err := NewGateway(surface(t, up.URL, "live.connect"), opts)
			if err != nil {
				t.Fatal(err)
			}
			conn, _, err := dialGateway(t, g, "/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent", nil)
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.WriteMessage(websocket.TextMessage, []byte(test.setup))
			_ = drain(conn)
			deadline := time.Now().Add(2 * time.Second)
			for {
				mu.Lock()
				n := len(observed)
				var last UsageObservation
				if n > 0 {
					last = observed[n-1]
				}
				mu.Unlock()
				if n > 0 && (last.Terminal || last.Bound != "") {
					if last.Bound != test.want || last.Terminal != (test.want == "") {
						t.Fatalf("want bound %q, got %+v", test.want, last)
					}
					return
				}
				if time.Now().After(deadline) {
					t.Fatal("session end not observed", observed)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

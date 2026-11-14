package modelapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// voiceSession relays one OpenAI Live voice session through the gateway with
// a lease of the given metering profile. The fake upstream replays a recorded
// fixture; interrupted fixtures drop the connection without a close frame.
type voiceSessionRun struct {
	lease *collectingLease
	dials int32
	code  int
}

func runVoiceSession(t *testing.T, file string, settings *WebSocketSessions, profile string, deadline time.Duration) voiceSessionRun {
	t.Helper()
	data := readFixture(t, "openai", file)
	interrupted := strings.Contains(file, "interrupted")
	var dials atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials.Add(1)
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil { // session.start
			return
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			if line := bytes.TrimSpace(scanner.Bytes()); len(line) > 0 {
				if conn.WriteMessage(websocket.TextMessage, line) != nil {
					return
				}
			}
		}
		if interrupted && deadline == 0 {
			_ = conn.UnderlyingConn().Close()
			return
		}
		if deadline > 0 {
			// Keep the session open until the gateway ends it.
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
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
	run := relayVoice(t, upstream.URL, settings, profile, deadline)
	run.dials = dials.Load()
	return run
}

// relayVoice runs one session through the gateway against origin.
func relayVoice(t *testing.T, origin string, settings *WebSocketSessions, profile string, deadline time.Duration) voiceSessionRun {
	t.Helper()
	s := providerSurfaceFor(openAIMediaProvider, ProtocolOpenAILive, []string{"fixture-model-live"}, openAILiveOperation)(origin)
	s.WebSocket = settings
	opts := options(t, origin, newOwnership())
	opts.TokenSource, opts.Credential = nil, NoCredential()
	lease := &collectingLease{profile: profile}
	opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
	g, err := NewGateway(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		if deadline > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), deadline)
			defer cancel()
			r = r.WithContext(ctx)
		}
		g.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	run := voiceSessionRun{lease: lease}
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/live/sessions?model=fixture-model-live", nil)
	if err != nil {
		if response != nil {
			run.code = response.StatusCode
		}
		<-finished
	} else {
		run.code = http.StatusSwitchingProtocols
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.start","session":{"model":"fixture-model-live"}}`))
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
		conn.Close()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("relay did not finish")
		}
	}
	return run
}

func secondsSettings() *WebSocketSessions {
	return &WebSocketSessions{Metering: SessionMeteringSeconds, MaxSessionSeconds: 600}
}

func (r voiceSessionRun) terminal() []execution.Observation {
	r.lease.mu.Lock()
	defer r.lease.mu.Unlock()
	var out []execution.Observation
	for _, o := range r.lease.observations {
		if o.Terminal {
			out = append(out, o)
		}
	}
	return out
}

func (r voiceSessionRun) results() []execution.Result {
	r.lease.mu.Lock()
	defer r.lease.mu.Unlock()
	return append([]execution.Result(nil), r.lease.results...)
}

// A complete recorded session settles once from session.closed: the observed
// milliseconds are the terminal evidence (the quota host rounds them up to
// whole seconds).
func TestVoiceSecondsSessionSettlesFromSessionClosed(t *testing.T) {
	run := runVoiceSession(t, "live.ws.jsonl", secondsSettings(), MeteringProfileVoiceSeconds, 0)
	if run.dials != 1 || run.code != http.StatusSwitchingProtocols {
		t.Fatalf("session not relayed: dials=%d code=%d", run.dials, run.code)
	}
	terminal := run.terminal()
	if len(terminal) != 1 || terminal[0].Counters[CounterVoiceSessionMilliseconds] != 45800 {
		t.Fatalf("terminal: %+v", terminal)
	}
	results := run.results()
	if len(results) != 1 || !results[0].Terminal || results[0].Err != nil {
		t.Fatalf("results: %+v", results)
	}
}

// An interrupted session, and one cut by a deadline, end without a confirmed
// length: no terminal observation, and the lease finishes with an error (the
// quota host keeps the hold as unknown), never a settlement at the lower
// bound the session.usage.updated snapshots showed.
func TestVoiceSecondsSessionWithoutConfirmedLengthIsNotFinal(t *testing.T) {
	// "cut by the deadline": the upstream stays open after a lower-bound
	// snapshot and the session's deadline ends it, as the gateway's
	// max_session_seconds timer does (the same context deadline).
	for name, deadline := range map[string]time.Duration{"upstream dropped": 0, "cut by the deadline": 300 * time.Millisecond} {
		t.Run(name, func(t *testing.T) {
			run := runVoiceSession(t, "live.interrupted.ws.jsonl", secondsSettings(), MeteringProfileVoiceSeconds, deadline)
			if run.dials != 1 {
				t.Fatalf("dials=%d", run.dials)
			}
			if terminal := run.terminal(); len(terminal) != 0 {
				t.Fatalf("a session without session.closed was final: %+v", terminal)
			}
			results := run.results()
			if len(results) != 1 || results[0].Terminal || results[0].Err == nil {
				t.Fatalf("the lease must finish with an error (unknown), not settle: %+v", results)
			}
		})
	}
}

// Without an opted-in surface, a seconds lease is refused before the dial, and
// the OpenAI Live operation keeps refusing token metering.
func TestVoiceSecondsNeedsAnOptedInSurface(t *testing.T) {
	for name, settings := range map[string]*WebSocketSessions{"no settings": nil, "session metering": {MaxSessionSeconds: 600}} {
		run := runVoiceSession(t, "live.ws.jsonl", settings, MeteringProfileVoiceSeconds, 0)
		if run.dials != 0 || run.code != http.StatusNotImplemented || run.lease.aborts != 1 {
			t.Fatalf("%s: a seconds lease on a surface that did not opt in must be refused before the dial: dials=%d code=%d aborts=%d", name, run.dials, run.code, run.lease.aborts)
		}
	}
	// A token lease stays refused on an opted-in surface too.
	run := runVoiceSession(t, "live.ws.jsonl", secondsSettings(), "", 0)
	if run.dials != 0 || run.code != http.StatusNotImplemented {
		t.Fatalf("token lease on live voice: dials=%d code=%d", run.dials, run.code)
	}
	// Request-count metering is unchanged on a surface that did not opt in.
	run = runVoiceSession(t, "live.ws.jsonl", nil, "request-count", 0)
	if run.dials != 1 || len(run.results()) != 1 {
		t.Fatalf("request-count session: dials=%d results=%+v", run.dials, run.results())
	}
}

// A surface that meters by seconds accepts only a seconds lease: a
// request-count lease is released and refused before credentials and the
// dial, as token mode refuses a request-count lease.
func TestVoiceSecondsSurfaceRefusesARequestCountLease(t *testing.T) {
	run := runVoiceSession(t, "live.ws.jsonl", secondsSettings(), "request-count", 0)
	if run.dials != 0 || run.code != http.StatusNotImplemented || run.lease.aborts != 1 || len(run.results()) != 0 || len(run.terminal()) != 0 {
		t.Fatalf("request-count lease on a seconds surface: dials=%d code=%d aborts=%d results=%+v", run.dials, run.code, run.lease.aborts, run.results())
	}
}

// A session that never started follows the HTTP exchange rule: no connection
// obtained is not sent; a handshake answered with a documented not-processed
// status is a rejection; a handshake written and not answered stays unknown.
func TestVoiceSecondsDialOutcomesFollowTheExchangeRule(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	refusedOrigin := closed.URL
	closed.Close()
	run := relayVoice(t, refusedOrigin, secondsSettings(), MeteringProfileVoiceSeconds, 0)
	results := run.results()
	if len(results) != 1 || !results[0].NotSent || results[0].Rejected || results[0].Err == nil {
		t.Fatalf("connection refused must be not sent: %+v", results)
	}

	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"type":"invalid_request_error"}}`, http.StatusForbidden)
	}))
	defer rejecting.Close()
	run = relayVoice(t, rejecting.URL, secondsSettings(), MeteringProfileVoiceSeconds, 0)
	results = run.results()
	if len(results) != 1 || results[0].NotSent || !results[0].Rejected || results[0].StatusCode != http.StatusForbidden || !results[0].ResponseComplete {
		t.Fatalf("a 403 handshake must be a rejection: %+v", results)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream failure", http.StatusBadGateway)
	}))
	defer failing.Close()
	run = relayVoice(t, failing.URL, secondsSettings(), MeteringProfileVoiceSeconds, 0)
	results = run.results()
	if len(results) != 1 || results[0].NotSent || results[0].Rejected || results[0].Err == nil {
		t.Fatalf("a 502 handshake must stay unknown: %+v", results)
	}

	release := make(chan struct{})
	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select { // the handshake was received; never answer it
		case <-release:
		case <-time.After(5 * time.Second):
		}
	}))
	defer silent.Close()
	defer close(release)
	run = relayVoice(t, silent.URL, secondsSettings(), MeteringProfileVoiceSeconds, 300*time.Millisecond)
	results = run.results()
	if len(results) != 1 || results[0].NotSent || results[0].Rejected || results[0].ResponseComplete || results[0].Err == nil {
		t.Fatalf("a written, unanswered handshake must stay unknown: %+v", results)
	}
}

// Seconds are read exactly from the decimal text: no length is rounded down,
// and any confirmed length up to N seconds is at most N*1000 ms.
func TestSecondsToMillisecondsIsAnExactCeiling(t *testing.T) {
	for text, want := range map[string]int64{"0": 0, "-0": 0, "1": 1000, "1.000": 1000, "1.0000000001": 1001, "1.0001": 1001, "0.0000000001": 1, "1e-12": 1, "45.8": 45800, "0.1": 100, "2.5e1": 25000, "120": 120000, "119.9999": 120000} {
		got, err := secondsToMilliseconds(json.RawMessage(text))
		if err != nil || got != want {
			t.Errorf("%s s -> %d ms (%v), want %d", text, got, err, want)
		}
	}
	for _, bad := range []string{"-1", "1e10", "1e999999999", `"5"`, "null", "NaN", "1/2"} {
		if _, err := secondsToMilliseconds(json.RawMessage(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	for n := int64(1); n <= 300; n++ {
		for _, text := range []string{strconv.FormatInt(n, 10), strconv.FormatInt(n-1, 10) + ".999999999999", strconv.FormatInt(n-1, 10) + ".0000000001"} {
			ms, err := secondsToMilliseconds(json.RawMessage(text))
			if err != nil || ms > n*1000 {
				t.Fatalf("%s s -> %d ms exceeds %d s", text, ms, n)
			}
		}
	}
}

func TestVoiceSecondsSurfaceValidation(t *testing.T) {
	live := providerSurfaceFor(openAIMediaProvider, ProtocolOpenAILive, []string{"fixture-model-live"}, openAILiveOperation)("https://api.openai.com")
	ok := live
	ok.WebSocket = secondsSettings()
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if ok.SessionMaxSeconds() != 600 || live.SessionMaxSeconds() != 0 {
		t.Fatal("SessionMaxSeconds is the configured maximum only when the surface meters by seconds")
	}
	noMax := live
	noMax.WebSocket = &WebSocketSessions{Metering: SessionMeteringSeconds}
	if err := noMax.Validate(); err == nil || !strings.Contains(err.Error(), "max_session_seconds") {
		t.Fatalf("seconds metering without a maximum: %v", err)
	}
	negative := live
	negative.WebSocket = &WebSocketSessions{Metering: SessionMeteringSeconds, MaxSessionSeconds: -5}
	if negative.Validate() == nil || negative.SessionMaxSeconds() != 0 || negative.validateSecondsMetering() == nil {
		t.Fatal("a negative maximum (an unvalidated surface) must be no maximum")
	}
	realtime := providerSurfaceFor(openAIMediaProvider, ProtocolOpenAIRealtime, []string{"m"}, "openai.v1.realtime")("https://api.openai.com")
	realtime.WebSocket = secondsSettings()
	if err := realtime.Validate(); err == nil || !strings.Contains(err.Error(), openAILiveOperation) {
		t.Fatalf("seconds metering on a surface without live voice: %v", err)
	}
	token := live
	token.WebSocket = &WebSocketSessions{Metering: SessionMeteringToken}
	if token.Validate() == nil {
		t.Fatal("token metering on live voice must stay refused")
	}
	if (&WebSocketSessions{Metering: SessionMeteringSeconds, TokenAllowance: 4096}).validate() == nil {
		t.Fatal("token_allowance with seconds metering accepted")
	}
}

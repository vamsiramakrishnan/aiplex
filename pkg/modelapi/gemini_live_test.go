package modelapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

// Gemini Live on the Gemini Developer API (gemini.live.connect).

const (
	daLivePath   = "/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"
	daLiveSecret = "AIza-da-live-upstream-secret-from-reference"
	daCallerKey  = "caller-aiplex-token-never-forwarded"
)

var callDALive = &fixtureCall{surface: geminiDASurface(geminiLiveOperation), path: daLivePath, body: `{"setup":{"model":"models/model-a"}}`, ws: "live"}

func TestGeminiDeveloperLiveCatalogue(t *testing.T) {
	ops, err := SelectProviderOperations(ProviderGeminiDeveloper, "gemini.live.connect")
	if err != nil {
		t.Fatal(err)
	}
	want := Operation{ID: "gemini.live.connect", Method: "GET", Path: daLivePath, Transport: WebSocket, Action: Invoke, ResourceKind: "live-session", Stateful: true, SourceURL: "https://aistudio.google.com/api-reference/live/bidi-generate-content"}
	if ops[0] != want {
		t.Fatalf("catalogue entry\n got %+v\nwant %+v", ops[0], want)
	}
	// Request count only: the Developer API does not document its Live
	// usage snapshots as cumulative, so no token profile settles from them.
	if got := Coverage(ops[0]); got != (ExecutionCoverage{RequestCount: true}) {
		t.Fatalf("coverage %+v", got)
	}
	s := geminiDASurface("gemini.live.connect")("https://generativelanguage.googleapis.com")
	s.WebSocket = &WebSocketSessions{Metering: SessionMeteringToken}
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "gemini.live.connect") {
		t.Fatalf("token metering accepted for Developer API Live: %v", err)
	}
	if OperationProtocol(ProviderGeminiDeveloper, "gemini.live.connect") != ProtocolGoogleGenAI {
		t.Fatal("Live is not a google-genai operation")
	}
	// The Vertex operation is not a Developer API operation, and the
	// Developer API operation is not a Vertex one.
	if _, err = SelectProviderOperations(ProviderGeminiDeveloper, "live.connect"); err == nil {
		t.Fatal("Vertex Live accepted on the Developer API")
	}
	if _, err = SelectOperations("gemini.live.connect"); err == nil {
		t.Fatal("Developer API Live accepted on a Google Cloud surface")
	}
	// The origin allowlist stays exact.
	for origin, ok := range map[string]bool{"https://generativelanguage.googleapis.com": true, "https://us-central1-aiplatform.googleapis.com": false, "https://generativelanguage.googleapis.com.evil.example": false, "https://evil.example": false} {
		s := geminiDASurface("gemini.live.connect")(origin)
		if err := s.Validate(); (err == nil) != ok {
			t.Errorf("origin %s: validate error %v, want accepted=%t", origin, err, ok)
		}
	}
}

// daLiveUpstream is a fake Generative Language Live endpoint that replays a
// recorded fixture and records how it was reached.
type daLiveUpstream struct {
	mu      sync.Mutex
	dials   int
	uri     []string
	query   []string
	headers []http.Header
	setup   []string
	reject  int
}

func (u *daLiveUpstream) server(t *testing.T, data []byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.dials++
		u.uri = append(u.uri, r.URL.Path)
		u.query = append(u.query, r.URL.RawQuery)
		u.headers = append(u.headers, r.Header.Clone())
		reject := u.reject
		u.mu.Unlock()
		if reject != 0 {
			// An upstream error that echoes the request credential.
			w.WriteHeader(reject)
			_, _ = w.Write([]byte(`{"error":{"message":"API key not valid: ` + r.Header.Get("X-Goog-Api-Key") + `"}}`))
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, setup, err := conn.ReadMessage()
		if err != nil {
			return
		}
		u.mu.Lock()
		u.setup = append(u.setup, string(setup))
		u.mu.Unlock()
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			if line := bytes.TrimSpace(scanner.Bytes()); len(line) > 0 {
				if conn.WriteMessage(websocket.TextMessage, line) != nil {
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
	t.Cleanup(server.Close)
	return server
}

// captureLogs routes the standard and structured loggers to a buffer.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prevWriter, prevFlags, prevSlog := log.Writer(), log.Flags(), slog.Default()
	log.SetOutput(buf)
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
		slog.SetDefault(prevSlog)
	})
	return buf
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type daLiveSession struct {
	observations []UsageObservation
	closeErr     error
	messages     []string
}

// runDALive relays one session through a Developer API Live surface whose
// credential injects the surface API key the way a secret-reference api_key
// credential does (a request header).
func runDALive(t *testing.T, upstreamURL string, credential Credential, clientPath, setup string, clientHeaders http.Header) daLiveSession {
	t.Helper()
	s := geminiDASurface(geminiLiveOperation)(upstreamURL)
	opts := options(t, upstreamURL, newOwnership())
	opts.TokenSource = nil
	opts.Credential = credential
	var mu sync.Mutex
	run := daLiveSession{}
	opts.Observe = func(_ context.Context, _ Owner, value UsageObservation) {
		mu.Lock()
		defer mu.Unlock()
		run.observations = append(run.observations, value)
	}
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
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+clientPath, clientHeaders)
	if err != nil {
		t.Fatalf("connect: %v (%v)", err, response)
	}
	_ = conn.WriteMessage(websocket.TextMessage, []byte(setup))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			run.closeErr = err
			break
		}
		run.messages = append(run.messages, string(payload))
	}
	conn.Close()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not finish")
	}
	mu.Lock()
	defer mu.Unlock()
	return run
}

func headerKeyCredential(name, value string) Credential {
	return CredentialFunc(func(_ context.Context, r *http.Request) error {
		r.Header.Set(name, value)
		return nil
	})
}

func readLiveFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "usage-formats", "google", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestGeminiDeveloperLiveKeyOnlyInHeader(t *testing.T) {
	logs := captureLogs(t)
	data := readLiveFixture(t, "gemini-live.ws.jsonl")
	up := &daLiveUpstream{}
	server := up.server(t, data)
	callerHeaders := http.Header{"Authorization": {"Bearer " + daCallerKey}, "X-Goog-Api-Key": {daCallerKey}, "X-Goog-User-Project": {"caller-project"}, "User-Agent": {"google-genai-sdk/test"}}
	setup := `{"setup":{"model":"models/model-a","generationConfig":{"responseModalities":["AUDIO"]}}}`
	// The caller's own key= and access_token= parameters and its routing
	// selector never cross to the provider.
	run := runDALive(t, server.URL, headerKeyCredential("x-goog-api-key", daLiveSecret), daLivePath+"?model=model-a&key="+daCallerKey+"&access_token="+daCallerKey, setup, callerHeaders)

	var closeErr *websocket.CloseError
	if !errors.As(run.closeErr, &closeErr) || closeErr.Code != websocket.CloseNormalClosure {
		t.Fatalf("session end %v", run.closeErr)
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.dials != 1 || up.uri[0] != daLivePath || up.query[0] != "" {
		t.Fatalf("upstream reached as %v ?%q (%d dials)", up.uri, up.query, up.dials)
	}
	headers := up.headers[0]
	if got := headers.Values("X-Goog-Api-Key"); len(got) != 1 || got[0] != daLiveSecret {
		t.Fatalf("upstream x-goog-api-key %q", got)
	}
	for name, values := range headers {
		for _, value := range values {
			if name != "X-Goog-Api-Key" && strings.Contains(value, daLiveSecret) {
				t.Errorf("surface key in upstream header %s", name)
			}
			if strings.Contains(value, daCallerKey) || strings.Contains(value, "caller-project") {
				t.Errorf("caller credential or billing claim forwarded in %s: %q", name, value)
			}
		}
	}
	if headers.Get("Authorization") != "" {
		t.Error("Authorization sent to the Developer API")
	}
	if len(up.setup) != 1 || up.setup[0] != setup {
		t.Fatalf("setup not forwarded unchanged: %q", up.setup)
	}
	// Ledger: the session's final usage, and no credential anywhere.
	final := run.observations[len(run.observations)-1]
	if !final.Terminal || final.Bound != "" || final.OperationID != "gemini.live.connect" || final.Counters["input_tokens"] != 1130 || final.Counters["output_tokens"] != 455 || final.Counters["cached_tokens"] != 600 || final.Counters["total_tokens"] != 1585 {
		t.Fatalf("final usage %+v", final)
	}
	ledger, _ := json.Marshal(run.observations)
	if bytes.Contains(ledger, []byte(daLiveSecret)) {
		t.Fatal("surface key reached a usage observation")
	}
	for _, message := range run.messages {
		if strings.Contains(message, daLiveSecret) {
			t.Fatal("surface key relayed to the caller")
		}
	}
	if strings.Contains(logs.String(), daLiveSecret) {
		t.Fatal("surface key logged")
	}
}

// The same recorded Live session is metered identically on the Vertex
// relay and on the Developer API relay.
func TestGeminiDeveloperLiveMetersLikeVertex(t *testing.T) {
	for _, file := range []string{"gemini-live.ws.jsonl", "gemini-live-interrupted.ws.jsonl", "vertex-live.ws.jsonl", "vertex-live-interrupted.ws.jsonl"} {
		t.Run(file, func(t *testing.T) {
			data := readLiveFixture(t, file)
			for _, mode := range []leaseMode{modeLegacy, modeCount} {
				vertex, vertexDials := runFixtureWebSocket(t, fixtureRoute{provider: "google", file: file, call: callLive}, data, mode)
				developer, developerDials := runFixtureWebSocket(t, fixtureRoute{provider: "google", file: file, call: callDALive}, data, mode)
				if vertexDials != 1 || developerDials != 1 {
					t.Fatalf("mode %d: dials vertex=%d developer=%d", mode, vertexDials, developerDials)
				}
				if !reflect.DeepEqual(vertex.accumulate(), developer.accumulate()) {
					t.Fatalf("mode %d: usage differs\n vertex    %v\n developer %v", mode, vertex.accumulate(), developer.accumulate())
				}
				vertexFinal, vertexTerminal := vertex.terminal()
				developerFinal, developerTerminal := developer.terminal()
				if vertexTerminal != developerTerminal || !reflect.DeepEqual(vertexFinal, developerFinal) || bound(vertex) != bound(developer) {
					t.Fatalf("mode %d: finality differs: vertex %v %t %q, developer %v %t %q", mode, vertexFinal, vertexTerminal, bound(vertex), developerFinal, developerTerminal, bound(developer))
				}
				if mode == modeCount && (len(vertex.results) != 1 || len(developer.results) != 1 || vertex.results[0].Terminal != developer.results[0].Terminal) {
					t.Fatalf("settlement differs: vertex %+v developer %+v", vertex.results, developer.results)
				}
			}
		})
	}
}

func bound(r fixtureRun) string {
	for _, o := range r.observations {
		if o.Bound != "" {
			return o.Bound
		}
	}
	return ""
}

func TestGeminiDeveloperLiveFailsClosedWithoutLeakingTheKey(t *testing.T) {
	logs := captureLogs(t)
	data := readLiveFixture(t, "gemini-live.ws.jsonl")
	setup := `{"setup":{"model":"models/model-a"}}`
	for _, test := range []struct {
		name       string
		credential Credential
		reject     int
		path       string
		setup      string
		dials      int
		code       int
		reason     string
	}{
		{name: "secret unavailable", credential: CredentialFunc(func(context.Context, *http.Request) error {
			return errors.New("secret " + daLiveSecret + " unavailable")
		}), path: daLivePath, setup: setup, code: websocket.CloseTryAgainLater, reason: "upstream identity unavailable"},
		{name: "credential in the URL is refused", credential: CredentialFunc(func(_ context.Context, r *http.Request) error {
			q := r.URL.Query()
			q.Set("key", daLiveSecret)
			r.URL.RawQuery = q.Encode()
			return nil
		}), path: daLivePath, setup: setup, code: websocket.CloseTryAgainLater, reason: "upstream identity unavailable"},
		{name: "upstream rejects the key", credential: headerKeyCredential("x-goog-api-key", daLiveSecret), reject: http.StatusUnauthorized, path: daLivePath, setup: setup, dials: 1, code: websocket.CloseTryAgainLater, reason: "upstream unavailable"},
		{name: "model outside the surface", credential: headerKeyCredential("x-goog-api-key", daLiveSecret), path: daLivePath, setup: `{"setup":{"model":"models/model-z"}}`, code: websocket.ClosePolicyViolation, reason: "setup denied"},
		{name: "Vertex model name", credential: headerKeyCredential("x-goog-api-key", daLiveSecret), path: daLivePath, setup: `{"setup":{"model":"projects/p/locations/us-central1/publishers/google/models/model-a"}}`, code: websocket.ClosePolicyViolation, reason: "setup denied"},
		{name: "routing selector differs from setup", credential: headerKeyCredential("x-goog-api-key", daLiveSecret), path: daLivePath + "?model=model-b", setup: setup, code: websocket.ClosePolicyViolation, reason: "setup denied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			up := &daLiveUpstream{reject: test.reject}
			server := up.server(t, data)
			run := runDALive(t, server.URL, test.credential, test.path, test.setup, nil)
			var closeErr *websocket.CloseError
			if !errors.As(run.closeErr, &closeErr) || closeErr.Code != test.code || closeErr.Text != test.reason {
				t.Fatalf("close %v, want %d %q", run.closeErr, test.code, test.reason)
			}
			up.mu.Lock()
			if up.dials != test.dials {
				t.Fatalf("upstream dialled %d times, want %d", up.dials, test.dials)
			}
			for _, query := range up.query {
				if query != "" {
					t.Fatalf("upstream URL carried a query: %q", query)
				}
			}
			up.mu.Unlock()
			ledger, _ := json.Marshal(run.observations)
			if bytes.Contains(ledger, []byte(daLiveSecret)) || strings.Contains(run.closeErr.Error(), daLiveSecret) {
				t.Fatal("surface key in a usage observation or close reason")
			}
		})
	}
	if strings.Contains(logs.String(), daLiveSecret) {
		t.Fatal("surface key logged")
	}
}

// A token-metered Developer API Live surface that bypassed Validate (built in
// memory, or loaded from a store other code wrote) is still refused by the
// relay: no admission, no credential, no dial, nothing settled.
func TestGeminiDeveloperLiveRelayRefusesTokenMetering(t *testing.T) {
	up := &daLiveUpstream{}
	server := up.server(t, readLiveFixture(t, "gemini-live.ws.jsonl"))
	opts := options(t, server.URL, newOwnership())
	opts.TokenSource = nil
	var mu sync.Mutex
	credentials, prepares, admits := 0, 0, 0
	opts.Credential = CredentialFunc(func(_ context.Context, r *http.Request) error {
		mu.Lock()
		credentials++
		mu.Unlock()
		r.Header.Set("x-goog-api-key", daLiveSecret)
		return nil
	})
	lease := &collectingLease{}
	opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) {
		mu.Lock()
		prepares++
		mu.Unlock()
		return lease, nil
	})
	opts.Admit = func(context.Context, contracts.RequestContext, Operation, Metadata) error {
		mu.Lock()
		admits++
		mu.Unlock()
		return nil
	}
	g, err := NewGateway(geminiDASurface(geminiLiveOperation)(server.URL), opts)
	if err != nil {
		t.Fatal(err)
	}
	// Bypass Validate: the surface the gateway serves becomes token-metered.
	g.surface.WebSocket = &WebSocketSessions{Metering: SessionMeteringToken, TokenAllowance: MinTokenAllowance}
	finished := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		g.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+daLivePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"setup":{"model":"models/model-a"}}`))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err = conn.ReadMessage(); err != nil {
			break
		}
	}
	conn.Close()
	<-finished
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.ClosePolicyViolation {
		t.Fatalf("session not refused: %v", err)
	}
	up.mu.Lock()
	dials := up.dials
	up.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if dials != 0 || credentials != 0 || prepares != 0 || admits != 0 || len(lease.observations) != 0 || len(lease.results) != 0 {
		t.Fatalf("refused session reached: dials=%d credentials=%d prepares=%d admits=%d observations=%d results=%d", dials, credentials, prepares, admits, len(lease.observations), len(lease.results))
	}
}

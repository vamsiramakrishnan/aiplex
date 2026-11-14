package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// Gemini Live on the Developer API behind the connected handler: the surface
// key is resolved from its secret reference and reaches the provider only in
// x-goog-api-key; the session is one admitted request, settled once.
func TestManagedGeminiDeveloperLiveKeyInHeaderOnly(t *testing.T) {
	logs := &lockedLog{}
	prevWriter, prevSlog := log.Writer(), slog.Default()
	log.SetOutput(logs)
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { log.SetOutput(prevWriter); slog.SetDefault(prevSlog) })

	setup := `{"setup":{"model":"models/model-v1"}}`
	var mu sync.Mutex
	var forwardedSetup string
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, payload, _ := conn.ReadMessage() // setup
		mu.Lock()
		forwardedSetup = string(payload)
		mu.Unlock()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"setupComplete":{}}`))
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"usageMetadata":{"promptTokenCount":300,"responseTokenCount":300,"totalTokenCount":600}}`))
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _, _ = conn.ReadMessage()
	}, func(cfg *QuotaHostConfig) {
		// One request-count binding for the session operation.
		cfg.Bindings = cfg.Bindings[:1]
		binding := &cfg.Bindings[0]
		binding.OperationID, binding.Provider, binding.BillingAccount, binding.Region = "gemini.live.connect", modelapi.ProviderGeminiDeveloper, "", ""
	})
	record, _ := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
	var saved ModelConnection
	_ = json.Unmarshal(record.Spec, &saved)
	installProviderCredentials(t, f.handler)
	connection := providerConnection(t, saved.Surface.Origin, modelapi.Provider{ID: modelapi.ProviderGeminiDeveloper}, modelapi.ProtocolGoogleGenAI, geminiKeyRef, "gemini.live.connect")
	if err := f.handler.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, connection)); err != nil {
		t.Fatal("surface rejected:", err)
	}
	putProviderSurface(t, f.resources, connection)
	path := geminiDeveloperLivePath
	records := attachLedger(t, f.handler)
	conn, done := f.dialSession(t, path+"?key=caller-supplied-key")
	defer conn.Close()
	_ = conn.WriteMessage(websocket.TextMessage, []byte(setup))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var err error
	for {
		if _, _, err = conn.ReadMessage(); err != nil {
			break
		}
	}
	conn.Close()
	<-done
	var closeErr *websocket.CloseError
	if !asCloseError(err, &closeErr) || closeErr.Code != websocket.CloseNormalClosure {
		t.Fatalf("session end %v", err)
	}
	settled := 0
	for _, record := range quotaHostTestAttempts(t, f.store) {
		raw, _ := json.Marshal(record)
		if bytes.Contains(raw, []byte(providerTestSecret)) {
			t.Fatal("surface key in a quota attempt")
		}
		info, _ := f.host.Engines()["tenant"].GetAttempt(context.Background(), "tenant", record.ID)
		if info.Decision.State == "settled" {
			settled++
		}
	}
	if settled != 1 {
		t.Fatalf("session settled %d request attempts, want 1", settled)
	}
	mu.Lock()
	defer mu.Unlock()
	f.upstream.Lock()
	upstream := f.upstream.requests
	f.upstream.Unlock()
	if len(upstream) != 1 {
		t.Fatalf("provider reached %d times", len(upstream))
	}
	if upstream[0].RequestURI != geminiDeveloperLivePath {
		t.Fatalf("provider reached at %q: the key or a caller parameter crossed in the URL", upstream[0].RequestURI)
	}
	headers := upstream[0].Header
	if got := headers.Values("X-Goog-Api-Key"); len(got) != 1 || got[0] != providerTestSecret || headers.Get("Authorization") != "" {
		t.Fatalf("upstream credential headers: x-goog-api-key=%q authorization=%q", got, headers.Get("Authorization"))
	}
	for name, values := range headers {
		for _, value := range values {
			if name != "X-Goog-Api-Key" && strings.Contains(value, providerTestSecret) {
				t.Fatalf("surface key in upstream header %s", name)
			}
		}
	}
	if forwardedSetup != setup {
		t.Fatalf("setup changed: %s", forwardedSetup)
	}
	ledger, _ := json.Marshal(records())
	if bytes.Contains(ledger, []byte(providerTestSecret)) || !bytes.Contains(ledger, []byte("gemini.live.connect")) {
		t.Fatalf("ledger: key recorded or session missing: %s", ledger)
	}
	if strings.Contains(logs.String(), providerTestSecret) {
		t.Fatal("surface key logged")
	}
}

// The allowance profile is not offered for Developer API Live: its usage
// snapshots are not documented as cumulative, so no token hold may be
// settled from them (fail closed). Only Vertex Live binds it.
func TestGeminiDeveloperLiveHasNoTokenAllowance(t *testing.T) {
	if quotaProfileOperation(QuotaBinding{Profile: quotaProfileLiveAllowance, Provider: modelapi.ProviderGeminiDeveloper, OperationID: "gemini.live.connect"}) {
		t.Fatal("gemini-live-allowance binds the Developer API Live operation")
	}
	if !quotaProfileOperation(QuotaBinding{Profile: quotaProfileLiveAllowance, Provider: "google", OperationID: "live.connect"}) {
		t.Fatal("Vertex Live lost its allowance profile")
	}
	connection := providerConnection(t, "https://generativelanguage.googleapis.com", modelapi.Provider{ID: modelapi.ProviderGeminiDeveloper}, modelapi.ProtocolGoogleGenAI, geminiKeyRef, "gemini.live.connect")
	connection.Surface.WebSocket = &modelapi.WebSocketSessions{Metering: modelapi.SessionMeteringToken}
	if err := connection.Surface.Validate(); err == nil || !strings.Contains(err.Error(), "gemini.live.connect") {
		t.Fatalf("token metering accepted for Developer API Live: %v", err)
	}
}

// The facade serves Developer API Live at the SDK's own path, and the GenAI
// SDK's x-goog-api-key header carries the caller's AIPlex token there.
func TestGeminiDeveloperLiveFacadePath(t *testing.T) {
	ops, err := modelapi.SelectProviderOperations(modelapi.ProviderGeminiDeveloper, "gemini.live.connect")
	if err != nil {
		t.Fatal(err)
	}
	s := modelapi.Surface{ID: "surface", Protocol: modelapi.ProtocolGoogleGenAI, Origin: "https://generativelanguage.googleapis.com", Models: []string{"model-v1"}, Operations: ops, Provider: &modelapi.Provider{ID: modelapi.ProviderGeminiDeveloper}}
	native, _, err := clientNativePath(s, httptest.NewRequest(http.MethodGet, "/", nil), geminiDeveloperLivePath)
	if err != nil || native != geminiDeveloperLivePath {
		t.Fatalf("facade path %q %v", native, err)
	}
	if _, _, err = clientNativePath(s, httptest.NewRequest(http.MethodPost, "/", nil), geminiDeveloperLivePath); err == nil {
		t.Fatal("POST accepted on the Live path")
	}
	// The Vertex Live path (the access points' google-live wire) is not
	// translated to the Developer API.
	if _, _, err = clientNativePath(s, httptest.NewRequest(http.MethodGet, "/", nil), geminiLivePath); err == nil {
		t.Fatal("Vertex Live path mapped onto a Developer API surface")
	}
	if accessPointWireServes(s, WireGeminiLive, "model-v1") == nil {
		t.Fatal("access point google-live wire claims a Developer API surface")
	}
	// Not installed: the facade refuses it.
	s.Operations, _ = modelapi.SelectProviderOperations(modelapi.ProviderGeminiDeveloper, "gemini.models.generateContent")
	if _, _, err = clientNativePath(s, httptest.NewRequest(http.MethodGet, "/", nil), geminiDeveloperLivePath); err == nil {
		t.Fatal("Live path served by a surface without the operation")
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/llm/gateway/surface"+geminiDeveloperLivePath, nil)
	if model, kind, _ := nativeKeyRoute(r); !model || kind != nativeKeyGenAI {
		t.Fatalf("x-goog-api-key not accepted on the facade Live path (model=%t kind=%v)", model, kind)
	}
}

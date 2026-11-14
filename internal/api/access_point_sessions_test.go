package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// addProviderAccessPointSurfaces saves a Bedrock Converse surface and an
// OpenAI Realtime surface and routes them through the "ide" access point.
func addProviderAccessPointSurfaces(t *testing.T, f *apFixture) {
	t.Helper()
	bedrockOps, err := modelapi.SelectProviderOperations(modelapi.ProviderBedrock, "bedrock.converse", "bedrock.converse-stream")
	if err != nil {
		t.Fatal(err)
	}
	realtimeOps, err := modelapi.SelectProviderOperations(modelapi.ProviderOpenAI, "openai.v1.realtime")
	if err != nil {
		t.Fatal(err)
	}
	bedrock := modelapi.Surface{ID: "bedrock", Protocol: modelapi.ProtocolBedrockConverse, Origin: f.upstream.URL, Models: []string{"anthropic.claude-v1:0"}, Operations: bedrockOps, Provider: &modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}}
	realtime := modelapi.Surface{ID: "realtime", Protocol: modelapi.ProtocolOpenAIRealtime, Origin: f.upstream.URL, Models: []string{"fixture-rt"}, Operations: realtimeOps, Provider: &modelapi.Provider{ID: modelapi.ProviderOpenAI}}
	// Bedrock signs with an operator AWS identity (SigV4), as in production.
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLEOPERATOR")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "operator-secret-access-key")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{f.upstream.URL}, AllowLoopback: true}, Identities: []connections.Identity{
		{ID: "host", TenantID: "tenant", Type: "bearer-env", TokenEnv: "COHESION_HOST_CREDENTIAL", AllowedOrigins: []string{f.upstream.URL}},
		{ID: "aws", TenantID: "tenant", Type: "aws-default", AllowedOrigins: []string{f.upstream.URL}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.h.Runtime = runtime
	f.put("model-surface", bedrock.ID, ModelConnection{Surface: bedrock, IdentityRef: "aws"})
	f.put("model-surface", realtime.ID, ModelConnection{Surface: realtime, IdentityRef: "host"})
	spec := apSpec()
	spec.Models = append(spec.Models, AccessPointModel{Name: "claude-bedrock", SurfaceRef: "bedrock", NativeModel: "anthropic.claude-v1:0"}, AccessPointModel{Name: "voice", SurfaceRef: "realtime", NativeModel: "fixture-rt"})
	// Save-time validation now accepts Bedrock and Realtime surfaces, aliases
	// included (the model is the path segment / connect-time selector).
	if err = f.h.validateAccessPointModels(context.Background(), "tenant", spec.Models); err != nil {
		t.Fatal(err)
	}
	spec.ID = "prov"
	f.put(AccessPointKind, "prov", spec)
	f.as(apUser("alice", "platform-eng"), append(append([]string(nil), apAllScopes...), "llm:surface:bedrock:bedrock.converse", "llm:surface:bedrock:bedrock.converse-stream", "llm:model:anthropic.claude-v1:0", "llm:surface:realtime:openai.v1.realtime", "llm:model:fixture-rt")...)
}

func TestAccessPointBedrockNativePathsRouteWithLedgerDimensions(t *testing.T) {
	f := newAPFixture(t)
	addProviderAccessPointSurfaces(t, f)
	records := attachLedger(t, f.h)
	body := `{"messages":[{"role":"user","content":[{"text":"hi"}]}]}`
	w := f.do("POST", "/ap/prov/model/claude-bedrock/converse", body, "Content-Type", "application/json")
	if w.Code != 200 {
		t.Fatalf("Bedrock Converse through the access point: %d %s", w.Code, w.Body.String())
	}
	calls := f.upstreamCalls()
	if last := calls[len(calls)-1]; last.Path != "/model/anthropic.claude-v1:0/converse" || last.Body != body || !strings.HasPrefix(last.Authorization, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLEOPERATOR/") {
		t.Fatalf("alias not rewritten to the native path or body changed: %+v", last)
	}
	r := onlyRecord(t, records())
	if r.AccessPointID != "prov" || r.CallerKind == "" || r.Operation != "bedrock.converse" || r.Provider != modelapi.ProviderBedrock || r.ModelID != "anthropic.claude-v1:0" {
		t.Fatalf("ledger dimensions: %+v", r)
	}
	// Protocol-native errors: AWS JSON {"message"} plus X-Amzn-ErrorType.
	for _, test := range []struct {
		path      string
		status    int
		errorType string
	}{
		{"/ap/prov/model/unknown/converse", 404, "ResourceNotFoundException"},
		{"/ap/prov/model/claude-bedrock/invoke", 400, "ValidationException"},
		{"/ap/prov/model/gemini-pro/converse", 400, "ValidationException"},
	} {
		w = f.do("POST", test.path, body, "Content-Type", "application/json")
		if w.Code != test.status || w.Header().Get("X-Amzn-ErrorType") != test.errorType || !strings.HasPrefix(w.Body.String(), `{"message":`) {
			t.Fatalf("%s: %d %q %s", test.path, w.Code, w.Header().Get("X-Amzn-ErrorType"), w.Body.String())
		}
	}
	// Without the caller's model grant the native gateway denies.
	f.as(apUser("alice", "platform-eng"), "llm:surface:bedrock:bedrock.converse")
	if w = f.do("POST", "/ap/prov/model/claude-bedrock/converse", body, "Content-Type", "application/json"); w.Code != 403 || w.Header().Get("X-Amzn-ErrorType") != "AccessDeniedException" {
		t.Fatalf("ungranted model: %d %s", w.Code, w.Body.String())
	}
}

func TestAccessPointRealtimeSessionRoutesAndRecordsSessionUsage(t *testing.T) {
	f := newAPFixture(t)
	addProviderAccessPointSurfaces(t, f)
	records := attachLedger(t, f.h)
	sessionDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.router.ServeHTTP(w, r)
		if r.URL.Query().Get("model") == "voice" {
			close(sessionDone)
		}
	}))
	defer server.Close()
	base := "ws" + strings.TrimPrefix(server.URL, "http")
	if _, response, err := websocket.DefaultDialer.Dial(base+"/ap/prov/v1/realtime", nil); err == nil || response.StatusCode != http.StatusBadRequest {
		t.Fatal("session without a model selector admitted", err)
	}
	conn, _, err := websocket.DefaultDialer.Dial(base+"/ap/prov/v1/realtime?model=voice", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	events := 0
	for {
		if _, _, err = conn.ReadMessage(); err != nil {
			break
		}
		events++
	}
	conn.Close()
	if events != 2 {
		t.Fatalf("relayed %d events", events)
	}
	calls := f.upstreamCalls()
	if last := calls[len(calls)-1]; last.Path != "/v1/realtime" || last.Query != "model=fixture-rt" || last.Authorization != "Bearer operator-upstream-token" {
		t.Fatalf("upstream session: %+v", last)
	}
	// The ledger record is submitted when the session handler returns.
	select {
	case <-sessionDone:
	case <-time.After(3 * time.Second):
		t.Fatal("session handler did not return")
	}
	r := onlyRecord(t, records())
	if r.AccessPointID != "prov" || r.Operation != "openai.v1.realtime" || r.Status != usageledger.StatusOK || r.UsageBound != "" || r.Usage == nil || *r.Usage.InputTokens != 9 || *r.Usage.CacheReadTokens != 4 || *r.Usage.OutputTokens != 3 || !r.Streaming {
		t.Fatalf("session record: %+v usage=%+v", r, r.Usage)
	}
}

// The 101 of a WebSocket upgrade carries the request ids, and the id is the
// ledger record's request_id. The router has no request id override (the
// fixture's own router replaces the id with "request-1").
func TestWebSocketUpgradeCarriesTheRequestIDs(t *testing.T) {
	f := newAPFixture(t)
	addProviderAccessPointSurfaces(t, f)
	records := attachLedger(t, f.h)
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), keyWIFIdentity, f.identity)
			ctx = context.WithValue(ctx, keyWIFAccess, f.access)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	f.h.MountAccessPointEndpoints(router)
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		router.ServeHTTP(w, r)
		close(done)
	}))
	defer server.Close()
	base := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, response, err := websocket.DefaultDialer.Dial(base+"/ap/prov/v1/realtime?model=voice", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	id := response.Header.Get("X-Aiplex-Request-Id")
	if response.StatusCode != http.StatusSwitchingProtocols || id == "" || !strings.HasPrefix(id, "req-") || response.Header.Get("X-Request-Id") != id {
		t.Fatalf("101 headers: %d %v", response.StatusCode, response.Header)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err = conn.ReadMessage(); err != nil {
			break
		}
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("session handler did not return")
	}
	if r := onlyRecord(t, records()); r.RequestID != id {
		t.Fatalf("ledger request_id %q, 101 X-Aiplex-Request-Id %q", r.RequestID, id)
	}
}

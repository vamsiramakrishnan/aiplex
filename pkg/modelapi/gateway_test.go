package modelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
)

type memoryOwnership struct {
	mu     sync.Mutex
	owners map[string]Owner
}

func newOwnership() *memoryOwnership { return &memoryOwnership{owners: map[string]Owner{}} }
func (m *memoryOwnership) Bind(_ context.Context, o Owner, r Resource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := r.Kind + "/" + r.ID
	if prev, ok := m.owners[key]; ok && prev != o {
		return fmt.Errorf("resource already owned")
	}
	m.owners[key] = o
	return nil
}
func (m *memoryOwnership) Check(_ context.Context, o Owner, r Resource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev, ok := m.owners[r.Kind+"/"+r.ID]; !ok || prev != o {
		return fmt.Errorf("not owner")
	}
	return nil
}
func surface(t *testing.T, origin string, ids ...string) Surface {
	t.Helper()
	ops, e := SelectOperations(ids...)
	if e != nil {
		t.Fatal(e)
	}
	return Surface{ID: "native", Protocol: "google-native", Origin: origin, APIVersion: "v1", Project: "test-project", Location: "global", Publisher: "google", Endpoint: "openapi", Models: []string{"model-a", "model-b"}, Operations: ops}
}
func options(t *testing.T, origin string, ownership Ownership) Options {
	t.Helper()
	policy, e := endpointpolicy.New(endpointpolicy.Config{AllowedOrigins: []string{origin}, AllowLoopback: true})
	if e != nil {
		t.Fatal(e)
	}
	dialer, e := NewLiveDialer(policy, nil)
	if e != nil {
		t.Fatal(e)
	}
	return Options{HTTPClient: policy.StreamingClient(), Context: func(r *http.Request) (contracts.RequestContext, error) {
		p := r.Header.Get("Test-Principal")
		if p == "" {
			p = "alice"
		}
		return contracts.RequestContext{Version: "v1", TenantID: "tenant", RequestID: "request", PrincipalID: p, ResourceID: "native"}, nil
	}, Authorize: func(context.Context, contracts.RequestContext, Surface, Operation, Metadata) error { return nil }, Ownership: ownership, TokenSource: TokenSourceFunc(func(context.Context) (string, error) { return "upstream-token", nil }), LiveDialer: dialer, AllowHTTPForTests: true}
}
func serve(t *testing.T, g *Gateway, method, path, body, principal string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer forged-client-token")
	if principal != "" {
		r.Header.Set("Test-Principal", principal)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}
func TestNativeBodyAndMetadataPreserved(t *testing.T) {
	body := `{"model":"model-a","input":"hello","new_provider_option":{"x":1},"background":true}`
	var gotBody string
	var upstreamHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		if r.Header.Get("Authorization") != "Bearer upstream-token" {
			t.Error("caller credentials leaked")
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"interaction-1","status":"in_progress"}`)
	}))
	defer server.Close()
	opts := options(t, server.URL, newOwnership())
	opts.Authorize = func(_ context.Context, _ contracts.RequestContext, _ Surface, _ Operation, m Metadata) error {
		if m.Model != "model-a" {
			t.Errorf("body model missing from authorization: %+v", m)
		}
		return nil
	}
	g, e := NewGateway(surface(t, server.URL, "interaction.create"), opts)
	if e != nil {
		t.Fatal(e)
	}
	w := serve(t, g, "POST", "/v1/projects/test-project/locations/global/interactions", body, "")
	if w.Code != 200 || gotBody != body {
		t.Fatalf("native relay: %d %s %s", w.Code, w.Body, gotBody)
	}
	if upstreamHits.Load() != 1 {
		t.Fatal(upstreamHits.Load())
	}
}
func TestDeniedBodyModelAndContextNeverAcquireCredentials(t *testing.T) {
	var tokens, requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	opts := options(t, server.URL, newOwnership())
	opts.TokenSource = TokenSourceFunc(func(context.Context) (string, error) { tokens.Add(1); return "token", nil })
	opts.Authorize = func(_ context.Context, _ contracts.RequestContext, _ Surface, _ Operation, m Metadata) error {
		if m.Model == "model-b" {
			return fmt.Errorf("model denied")
		}
		return nil
	}
	g, _ := NewGateway(surface(t, server.URL, "interaction.create", "models.generateContent"), opts)
	tests := []struct{ path, body string }{{"/v1/projects/test-project/locations/global/interactions", `{"model":"model-b","input":"x"}`}, {"/v1/projects/test-project/locations/global/interactions", `{"model":"outside","input":"x"}`}, {"/v1/projects/test-project/locations/global/publishers/google/models/model-a:generateContent", `{"model":"model-b"}`}, {"/v1/projects/other-project/locations/global/interactions", `{"model":"model-a"}`}}
	for _, tc := range tests {
		w := serve(t, g, "POST", tc.path, tc.body, "")
		if w.Code != 403 && w.Code != 404 {
			t.Fatalf("denied: %d %s", w.Code, w.Body)
		}
	}
	if tokens.Load() != 0 || requests.Load() != 0 {
		t.Fatal("denied request reached credentials/network")
	}
}
func TestStatefulResourcesAndHistoryArePrincipalScoped(t *testing.T) {
	var calls atomic.Int32
	owner := newOwnership()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"interaction-1","input":"private"}`)
	}))
	defer server.Close()
	g, e := NewGateway(surface(t, server.URL, "interaction.create", "interaction.get", "interaction.list", "cache.get"), options(t, server.URL, owner))
	if e != nil {
		t.Fatal(e)
	}
	path := "/v1/projects/test-project/locations/global/interactions"
	if w := serve(t, g, "POST", path, `{"model":"model-a","input":"private"}`, "alice"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := serve(t, g, "GET", path+"/interaction-1", "", "bob"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := serve(t, g, "POST", path, `{"model":"model-a","previous_interaction_id":"interaction-1","input":"steal"}`, "bob"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := serve(t, g, "GET", path, "", "alice"); w.Code != 403 {
		t.Fatal("list not isolated", w.Code)
	}
	if w := serve(t, g, "GET", path+"/interaction-1", "", "alice"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}
func TestSSEPreservesEventsBindsCreationAndReportsUsage(t *testing.T) {
	events := "id: 1\r\nevent: interaction.created\r\ndata: {\"interaction\":{\"id\":\"stream-1\"},\"event_type\":\"interaction.created\"}\r\n\r\n" + "id: 2\nevent: interaction.completed\ndata: {\"interaction\":{\"id\":\"stream-1\",\"usage\":{\"total_input_tokens\":4,\"total_output_tokens\":6}},\"event_type\":\"interaction.completed\"}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, events)
	}))
	defer server.Close()
	owner := newOwnership()
	opts := options(t, server.URL, owner)
	var observations []UsageObservation
	opts.Observe = func(_ context.Context, _ Owner, o UsageObservation) { observations = append(observations, o) }
	g, _ := NewGateway(surface(t, server.URL, "interaction.create"), opts)
	w := serve(t, g, "POST", "/v1/projects/test-project/locations/global/interactions", `{"model":"model-a","stream":true}`, "")
	if w.Code != 200 || w.Body.String() != events {
		t.Fatal(w.Code, w.Body.String())
	}
	if e := owner.Check(context.Background(), Owner{"tenant", "alice", "native"}, Resource{"interaction", "stream-1"}); e != nil {
		t.Fatal(e)
	}
	if len(observations) != 1 || !observations[0].Terminal || observations[0].Counters["input_tokens"] != 4 {
		t.Fatalf("observations %+v", observations)
	}
}
func TestStreamCancellationClosesUpstream(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	g, _ := NewGateway(surface(t, server.URL, "models.streamGenerateContent"), options(t, server.URL, nil))
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/v1/projects/test-project/locations/global/publishers/google/models/model-a:streamGenerateContent", strings.NewReader(`{}`)).WithContext(ctx)
	done := make(chan struct{})
	go func() { g.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("gateway ignored cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("upstream ignored cancellation")
	}
}
func TestLiveFunctionalAdapterAndModelGuard(t *testing.T) {
	var tokens, hits atomic.Int32
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer upstream-token" {
			t.Error("bad upstream credential")
		}
		ws, e := upgrader.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer ws.Close()
		kind, setup, e := ws.ReadMessage()
		if e != nil {
			return
		}
		var doc map[string]any
		if json.Unmarshal(setup, &doc) != nil {
			t.Error("setup changed")
		}
		_ = ws.WriteMessage(kind, []byte(`{"setupComplete":{}}`))
	}))
	defer server.Close()
	owner := newOwnership()
	opts := options(t, server.URL, owner)
	opts.TokenSource = TokenSourceFunc(func(context.Context) (string, error) { tokens.Add(1); return "upstream-token", nil })
	g, e := NewGateway(surface(t, server.URL, "live.connect"), opts)
	if e != nil {
		t.Fatal(e)
	}
	gateway := httptest.NewServer(g)
	defer gateway.Close()
	for _, model := range []string{"outside", "model-a"} {
		ws, _, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+"/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent", nil)
		if e != nil {
			t.Fatal(e)
		}
		_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"setup":{"model":"`+model+`"}}`))
		_ = ws.SetReadDeadline(time.Now().Add(time.Second))
		_, response, e := ws.ReadMessage()
		ws.Close()
		if model == "model-a" && (e != nil || string(response) != `{"setupComplete":{}}`) {
			t.Fatal(e, string(response))
		}
	}
	if tokens.Load() != 1 || hits.Load() != 1 {
		t.Fatal("denied Live setup reached upstream", tokens.Load(), hits.Load())
	}
}
func TestCatalogSpecializedPathsAndImmutableProfiles(t *testing.T) {
	all := GoogleOperations()
	if len(all) < 30 {
		t.Fatal("specialized catalog missing", len(all))
	}
	for _, id := range []string{"cache.create", "batch.cancel", "tuning.create", "models.predictLongRunning", "models.fetchPredictOperation", "live.connect", "interaction.cancel", "openai.chat-completions"} {
		if _, e := SelectOperations(id); e != nil {
			t.Fatal(e)
		}
	}
	s := surface(t, "https://aiplatform.googleapis.com", "interaction.create")
	r, e := NewRegistry(s)
	if e != nil {
		t.Fatal(e)
	}
	s.Models[0] = "evil"
	got, _ := r.Get("native")
	if got.Models[0] != "model-a" {
		t.Fatal("mutable profile")
	}
	s.Location = "us-central1"
	if s.Validate() == nil {
		t.Fatal("single-region interactions allowed")
	}
}

func TestDuplicateSecurityFieldsDeniedBeforeCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("duplicate field forwarded") }))
	defer server.Close()
	opts := options(t, server.URL, newOwnership())
	opts.TokenSource = TokenSourceFunc(func(context.Context) (string, error) { t.Error("credentials acquired"); return "", nil })
	g, _ := NewGateway(surface(t, server.URL, "interaction.create"), opts)
	for _, body := range []string{`{"model":"model-a","model":"model-b"}`, `{"Model":"model-a"}`, `{"model":"model-a","agent":"agent-b"}`} {
		if w := serve(t, g, "POST", "/v1/projects/test-project/locations/global/interactions", body, ""); w.Code != 403 {
			t.Fatal(w.Code, w.Body)
		}
	}
}

func TestPredictionOperationOwnershipRoundTrip(t *testing.T) {
	name := "projects/test-project/locations/global/publishers/google/models/model-a/operations/video-1"
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "done": strings.HasSuffix(r.URL.Path, ":fetchPredictOperation")})
	}))
	defer upstream.Close()
	opts := options(t, upstream.URL, newOwnership())
	g, e := NewGateway(surface(t, upstream.URL, "models.predictLongRunning", "models.fetchPredictOperation"), opts)
	if e != nil {
		t.Fatal(e)
	}
	prefix := "/v1/projects/test-project/locations/global/publishers/google/models/model-a"
	if w := serve(t, g, "POST", prefix+":predictLongRunning", `{"instances":[{"prompt":"video"}]}`, "alice"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	body, _ := json.Marshal(map[string]string{"operationName": name})
	if w := serve(t, g, "POST", prefix+":fetchPredictOperation", string(body), "bob"); w.Code != 403 {
		t.Fatal("cross-principal video output readable", w.Code, w.Body)
	}
	if w := serve(t, g, "POST", prefix+":fetchPredictOperation", string(body), "alice"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if calls.Load() != 2 {
		t.Fatal("denied operation reached provider", calls.Load())
	}
}
func TestResponsesOwnershipAndNativeStream(t *testing.T) {
	frame := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"response-1\"}}\n\n" + "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-1\",\"usage\":{\"input_tokens\":4,\"output_tokens\":6,\"output_tokens_details\":{\"reasoning_tokens\":2}}}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, frame)
	}))
	defer upstream.Close()
	opts := options(t, upstream.URL, newOwnership())
	var observed []UsageObservation
	opts.Observe = func(_ context.Context, _ Owner, u UsageObservation) { observed = append(observed, u) }
	g, e := NewGateway(surface(t, upstream.URL, "response.create", "response.get"), opts)
	if e != nil {
		t.Fatal(e)
	}
	path := "/v1/projects/test-project/locations/global/endpoints/openapi/responses"
	if w := serve(t, g, "POST", path, `{"model":"model-a","stream":true}`, "alice"); w.Code != 200 || w.Body.String() != frame {
		t.Fatal(w.Code, w.Body)
	}
	if w := serve(t, g, "GET", path+"/response-1", "", "bob"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := serve(t, g, "POST", path, `{"model":"model-a","previous_response_id":"response-1"}`, "bob"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if len(observed) != 1 || !observed[0].Terminal || !observed[0].Cumulative || observed[0].Counters["thought_tokens"] != 2 {
		t.Fatalf("bad Responses observation %+v", observed)
	}
}

func TestCloudStorageReferencesRequireExplicitSurfaceScope(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"usageMetadata":{"promptTokenCount":2}}`)
	}))
	defer server.Close()
	s := surface(t, server.URL, "models.generateContent")
	s.DataPrefixes = []string{"gs://tenant-bucket/alice"}
	opts := options(t, server.URL, nil)
	g, e := NewGateway(s, opts)
	if e != nil {
		t.Fatal(e)
	}
	path := "/v1/projects/test-project/locations/global/publishers/google/models/model-a:generateContent"
	for _, uri := range []string{"gs://tenant-bucket/alice/photo.png", "gs://tenant-bucket/alice-other/photo.png", "gs://tenant-bucket/alice/../bob/private.png", "gs://other-bucket/alice/photo.png"} {
		body, _ := json.Marshal(map[string]any{"contents": []any{map[string]any{"fileData": map[string]string{"fileUri": uri}}}})
		w := serve(t, g, "POST", path, string(body), "")
		if uri == "gs://tenant-bucket/alice/photo.png" {
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
		} else if w.Code != 403 {
			t.Fatal("out of scope URI accepted", uri, w.Code)
		}
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}
func TestLiveRequiresFunctionalAdapterAndOwnedResumption(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	opts := options(t, server.URL, newOwnership())
	opts.LiveDialer = nil
	if _, e := NewGateway(surface(t, server.URL, "live.connect"), opts); e == nil {
		t.Fatal("advertised Live without transport")
	}
	opts = options(t, server.URL, newOwnership())
	g, _ := NewGateway(surface(t, server.URL, "live.connect"), opts)
	if _, e := g.validateLiveSetup(context.Background(), Owner{"tenant", "alice", "native"}, []byte(`{"setup":{"model":"model-a","sessionResumption":{"handle":"someone-elses-session"}}}`)); e == nil {
		t.Fatal("foreign resumption handle accepted")
	}
}

func TestRawPredictionRetainsArbitraryPayload(t *testing.T) {
	payload := []byte{0, 1, 2, 255, 10}
	var received []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		if r.Header.Get("Content-Type") != "application/octet-stream" {
			t.Error("content type changed")
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(received)
	}))
	defer upstream.Close()
	g, e := NewGateway(surface(t, upstream.URL, "models.rawPredict"), options(t, upstream.URL, nil))
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "/v1/projects/test-project/locations/global/publishers/google/models/model-a:rawPredict", bytes.NewReader(payload))
	r.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 || !bytes.Equal(received, payload) || !bytes.Equal(w.Body.Bytes(), payload) {
		t.Fatal(w.Code, w.Body)
	}
}

func TestNativePublisherReferencesRespectProjectPublisher(t *testing.T) {
	s := surface(t, "http://127.0.0.1:1", "models.generateContent")
	g, e := NewGateway(s, options(t, s.Origin, nil))
	if e != nil {
		t.Fatal(e)
	}
	for _, model := range []string{"model-a", "google/model-a", "models/model-a", "publishers/google/models/model-a", "projects/test-project/locations/global/publishers/google/models/model-a"} {
		if !g.allowedModel(model) {
			t.Error("native model alias rejected", model)
		}
	}
	for _, model := range []string{"projects/other-project/locations/global/publishers/google/models/model-a", "publishers/other/models/model-a", "other/model-a"} {
		if g.allowedModel(model) {
			t.Error("model scope escaped", model)
		}
	}
}

func TestTrustedBillingHeadersArePinnedAndCannotOverrideCredentials(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Goog-User-Project") != "billing-project" || r.Header.Get("Authorization") != "Bearer upstream-token" || r.Header.Get("X-Goog-Api-Client") != "antigravity/test" {
			t.Error("trusted billing/attribution not preserved", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	s := surface(t, upstream.URL, "models.generateContent")
	opts := options(t, upstream.URL, nil)
	opts.UpstreamHeaders = http.Header{"X-Goog-User-Project": []string{"billing-project"}}
	g, e := NewGateway(s, opts)
	if e != nil {
		t.Fatal(e)
	}
	opts.UpstreamHeaders.Set("X-Goog-User-Project", "changed-after-registration")
	r := httptest.NewRequest("POST", "/v1/projects/test-project/locations/global/publishers/google/models/model-a:generateContent", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Goog-User-Project", "spoofed-caller-project")
	r.Header.Set("X-Goog-Api-Client", "antigravity/test")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	opts.UpstreamHeaders = http.Header{"Authorization": []string{"Bearer injected"}}
	if _, e = NewGateway(s, opts); e == nil {
		t.Fatal("trusted header override accepted")
	}
}

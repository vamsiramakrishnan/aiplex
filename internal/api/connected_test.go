package api

import (
	"context"
	"encoding/json"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func trustedConnectedRequest(path, body string, scopes []string) *http.Request {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.SetPathValue("id", "surface")
	identity := models.WIFIdentity{PrincipalID: "principal-a", Subject: "subject"}
	ctx := context.WithValue(r.Context(), keyWIFIdentity, &identity)
	ctx = context.WithValue(ctx, keyWIFAccess, &models.ResolvedAccess{Identity: identity, Scopes: scopes})
	ctx = context.WithValue(ctx, keyRequestID, "request-1")
	return r.WithContext(ctx)
}
func TestClientGatewayUsesGovernedPathAndCredentials(t *testing.T) {
	var calls atomic.Int32
	var path, authorization, body, attribution string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		path = r.URL.Path
		authorization = r.Header.Get("Authorization")
		attribution = r.Header.Get("X-Goog-Api-Client")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	t.Setenv("AIPLEX_TEST_BEARER", "upstream-token")
	runtime, e := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{upstream.URL}, AllowLoopback: true}, Identities: []connections.Identity{{ID: "adc", TenantID: "tenant", Type: "bearer-env", TokenEnv: "AIPLEX_TEST_BEARER", AllowedOrigins: []string{upstream.URL}}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	store := resources.NewMemoryStore()
	ops, _ := modelapi.SelectOperations("models.generateContent")
	surface := modelapi.Surface{ID: "surface", Protocol: "google-genai", Origin: upstream.URL, APIVersion: "v1", Project: "project", Location: "global", Publisher: "google", Models: []string{"model-a"}, Operations: ops}
	spec, _ := json.Marshal(ModelConnection{Surface: surface, IdentityRef: "adc"})
	_, e = store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "model-surface", ID: "surface", Spec: spec}, 0)
	if e != nil {
		t.Fatal(e)
	}
	h := NewConnectedHandler(store, runtime, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
	h.PublicURL = "https://gateway.example"
	request := trustedConnectedRequest("/api/v1/llm/gateway/surface/v1beta/models/model-a:generateContent", `{"contents":[],"native_extension":{"keep":true}}`, []string{"llm:surface:surface:models.generateContent", "llm:model:model-a"})
	request.Header.Set("Authorization", "Bearer caller-token")
	request.Header.Set("X-Goog-Api-Client", "antigravity-test")
	rec := httptest.NewRecorder()
	h.ClientGateway(rec, request)
	if rec.Code != 200 || calls.Load() != 1 || path != "/v1/projects/project/locations/global/publishers/google/models/model-a:generateContent" || authorization != "Bearer upstream-token" || attribution != "antigravity-test" || !strings.Contains(body, "native_extension") {
		t.Fatalf("facade failed %d calls%d path%q auth%q body%q", rec.Code, calls.Load(), path, authorization, body)
	}
	request = trustedConnectedRequest("/api/v1/llm/gateway/surface/v1beta/models/model-a:generateContent", `{"contents":[]}`, []string{"llm:model:model-a"})
	rec = httptest.NewRecorder()
	h.ClientGateway(rec, request)
	if rec.Code != 403 || calls.Load() != 1 {
		t.Fatal("credential relay without action grant", rec.Code, calls.Load())
	}
	d, e := h.ResolveDestination(context.Background(), "tenant", "surface")
	if e != nil || d.Endpoint != "https://gateway.example/api/v1/llm/gateway/surface" {
		t.Fatal("client bypassed gateway", d.Endpoint, e)
	}
}
func TestConversationOwnersCannotCrossPrincipals(t *testing.T) {
	s := cloudSessions{resources.NewMemoryStore()}
	ctx := context.Background()
	if e := s.Bind(ctx, "a", "p1", "c", "session"); e != nil {
		t.Fatal(e)
	}
	if e := s.Check(ctx, "a", "p2", "c", "session"); e == nil {
		t.Fatal("cross principal conversation")
	}
	if e := s.Check(ctx, "b", "p1", "c", "session"); e == nil {
		t.Fatal("cross tenant conversation")
	}
}

func TestAuditRetainsStreamingCapabilities(t *testing.T) {
	rec := httptest.NewRecorder()
	handler := AuditLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flush, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("audit dropped stream capability")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: first\n\n"))
		flush.Flush()
	}))
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/llm/native/test/x", nil))
	if !rec.Flushed || rec.Body.String() != "data: first\n\n" {
		t.Fatal("stream buffered by audit")
	}
}

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/errorcode"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/routing"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// headerFixture is an access point "r" in front of one mock provider that
// answers with an X-Request-Id of its own, behind the real RequestID
// middleware, so the response headers and the ledger can be compared.
type headerFixture struct {
	*routeFixture
	handler http.Handler
}

const providerRequestID = "provider-req-9"

func newHeaderFixture(t *testing.T) *headerFixture {
	t.Helper()
	return newHeaderFixtureWith(t, nil)
}

// newHeaderFixtureWith lets a test answer some provider requests itself.
func newHeaderFixtureWith(t *testing.T, intercept func(http.ResponseWriter, *http.Request) bool) *headerFixture {
	t.Helper()
	f := &routeFixture{t: t, a: &devstack.MockModels{}, b: &devstack.MockModels{Name: "b"}}
	wrap := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Request-Id", providerRequestID)
			if intercept != nil && intercept(w, r) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	sa, sb := httptest.NewServer(wrap(f.a)), httptest.NewServer(wrap(f.b))
	t.Cleanup(sa.Close)
	t.Cleanup(sb.Close)
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{sa.URL, sb.URL}, AllowLoopback: true, Timeout: 3 * time.Second}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.store = resources.NewMemoryStore()
	f.ledger = usageledger.NewMemoryStore()
	f.h = NewConnectedHandler(f.store, runtime, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
	f.h.PublicURL = "https://gateway.example"
	f.h.Ledger = usageledger.NewRecorder(f.ledger, usageledger.WithErrorHandler(func(_ usageledger.Record, err error) { t.Errorf("ledger write failed: %v", err) }))
	installProviderCredentials(t, f.h)
	for id, origin := range map[string]string{"a": sa.URL, "b": sb.URL} {
		c := providerConnection(t, origin, modelapi.Provider{ID: modelapi.ProviderOpenAICompatible, UsageSemantics: modelapi.UsageSemanticsOpenAI, StreamUsage: "inject"}, "openai-chat", bearerRef, "compat.chat-completions")
		c.Surface.ID, c.Surface.Models = id, append(append([]string{}, routeModels...), "m-fault-s429", "m-fault-s400", "m-fault-s401")
		putProviderSurface(t, f.store, c)
	}
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			f.mu.Lock()
			if f.identity != nil {
				ctx = context.WithValue(ctx, keyWIFIdentity, f.identity)
				ctx = context.WithValue(ctx, keyWIFAccess, &models.ResolvedAccess{Identity: *f.identity, Scopes: f.scopes, Roles: []models.IAMRole{models.RoleAdmin}})
			}
			f.mu.Unlock()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	f.h.MountAccessPointEndpoints(router)
	router.Route("/api/v1", func(r chi.Router) { f.h.MountAccessPoints(r) })
	f.router = router
	f.as(apUser("alice"), "llm:surface:a:*", "llm:surface:b:*", "llm:model:*")
	return &headerFixture{routeFixture: f, handler: router}
}

// the ledger record of the only exchange so far.
func (f *headerFixture) onlyRecord() usageledger.Record {
	f.t.Helper()
	recs := f.records()
	if len(recs) != 1 {
		f.t.Fatalf("want one ledger record, got %d", len(recs))
	}
	return recs[0]
}

func TestAIPlexRequestIDEqualsLedgerRequestID(t *testing.T) {
	cases := []struct {
		name    string
		model   AccessPointModel
		body    string
		headers []string
		status  int
		// callerID is the request id the caller sent, if any.
		callerID string
	}{
		{name: "provider answers with its own X-Request-Id", model: AccessPointModel{Name: "m", Backends: backends("a/m")}, body: hello, status: 200},
		{name: "single backend model", model: AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m"}, body: hello, status: 200},
		{name: "caller sent its own X-Request-Id", model: AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m"}, body: hello, headers: []string{"X-Request-Id", "caller-rid-1"}, callerID: "caller-rid-1", status: 200},
		{name: "streamed response", model: AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m"}, body: `{"model":"m","stream":true,"messages":[{"role":"user","content":"hello"}]}`, status: 200},
		{name: "refused before any model was called", model: AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m"}, body: `{"model":"nope","messages":[{"role":"user","content":"hello"}]}`, status: 404},
		{name: "provider error relayed", model: AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m-fault-s429"}, body: hello, status: 429},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newHeaderFixture(t)
			f.model(c.model)
			w := f.chat(c.body, append([]string{"Content-Type", "application/json"}, c.headers...)...)
			if w.Code != c.status {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			got := w.Header().Get("X-Aiplex-Request-Id")
			rec := f.onlyRecord()
			if got == "" || got != rec.RequestID {
				t.Fatalf("X-Aiplex-Request-Id %q, ledger request_id %q", got, rec.RequestID)
			}
			if c.callerID != "" && got != c.callerID {
				t.Fatalf("a request id the caller chose is kept: %q", got)
			}
			if c.status == 200 && c.name != "refused before any model was called" {
				// The provider's own id keeps X-Request-Id, as it does today.
				if w.Header().Get("X-Request-Id") != providerRequestID || got == providerRequestID {
					t.Fatalf("X-Request-Id %q must stay the provider's, X-Aiplex-Request-Id %q must be AIPlex's", w.Header().Get("X-Request-Id"), got)
				}
			}
			if strings.Contains(c.name, "streamed") && !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
				t.Fatalf("not a streamed response: %v", w.Header())
			}
		})
	}
}

func errCodeOf(w *httptest.ResponseRecorder) string { return w.Header().Get(errorcode.Header) }

func TestRelayedProviderErrorsCarryNoErrorCode(t *testing.T) {
	for _, status := range []string{"s429", "s400", "s500", "s503"} {
		t.Run(status, func(t *testing.T) {
			f := newHeaderFixture(t)
			f.model(AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m-fault-" + status})
			w := f.chat(hello)
			if w.Code < 400 || !strings.Contains(w.Body.String(), "mock fault") {
				t.Fatalf("not a relayed provider error: %d %s", w.Code, w.Body)
			}
			if got := errCodeOf(w); got != "" {
				t.Fatalf("a relayed provider error carries %s: %q", errorcode.Header, got)
			}
			if w.Header().Get("X-Aiplex-Request-Id") == "" {
				t.Fatal("the request id is on every response")
			}
		})
	}
	t.Run("routed and held then delivered", func(t *testing.T) {
		f := newHeaderFixture(t)
		f.model(AccessPointModel{Name: "m", Backends: backends("a/m-fault-s503", "b/m-fault-s503")})
		w := f.chat(hello)
		if w.Code != 503 || errCodeOf(w) != "" {
			t.Fatalf("%d %q", w.Code, errCodeOf(w))
		}
	})
}

func TestProviderRejectingTheGatewayCredentialIsAIPlexsDecision(t *testing.T) {
	f := newHeaderFixture(t)
	f.model(AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m-fault-s401"})
	w := f.chat(hello)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "upstream_credential_rejected") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := errCodeOf(w); got != "UPSTREAM_CREDENTIAL_REJECTED" {
		t.Fatalf("%s = %q", errorcode.Header, got)
	}
}

func TestRefusalsCarryAIPlexErrorCode(t *testing.T) {
	f := newHeaderFixture(t)
	f.model(AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m"})
	f.as(apUser("alice"), "llm:model:*") // no surface grant
	for _, c := range []struct {
		name, path, body string
		status           int
		code             string
	}{
		{"model not found", "/ap/r/v1/chat/completions", `{"model":"nope","messages":[]}`, 404, "MODEL_NOT_FOUND"},
		{"bad body", "/ap/r/v1/chat/completions", `not json`, 400, "INVALID_REQUEST"},
		{"missing grant", "/ap/r/v1/chat/completions", hello, 403, "ACTION_DENIED"},
		{"unknown access point", "/ap/missing/v1/chat/completions", hello, 404, "NOT_FOUND"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := f.do(c.path, c.body)
			if w.Code != c.status || errCodeOf(w) != c.code {
				t.Fatalf("%d %s = %q (want %d %s): %s", w.Code, errorcode.Header, errCodeOf(w), c.status, c.code, w.Body)
			}
		})
	}
	t.Run("unauthenticated", func(t *testing.T) {
		f.as(nil)
		w := f.do("/ap/r/v1/chat/completions", hello)
		if w.Code != 401 || errCodeOf(w) != "UNAUTHENTICATED" || w.Header().Get("X-Aiplex-Request-Id") == "" {
			t.Fatalf("%d %q %v", w.Code, errCodeOf(w), w.Header())
		}
	})
}

func (f *headerFixture) do(path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func TestNoBackendAvailableIsAIPlexsDecisionAndAProviderErrorIsNot(t *testing.T) {
	f := newHeaderFixture(t)
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m-fault-s503"), Routing: &routing.Routing{Breaker: &routing.Breaker{FailureThreshold: 1, OpenMS: 60000}}})
	first := f.chat(hello)
	if first.Code != 503 || errCodeOf(first) != "" {
		t.Fatalf("the provider's 503 is relayed without a code: %d %q", first.Code, errCodeOf(first))
	}
	second := f.chat(hello)
	if second.Code != 503 || errCodeOf(second) != "NO_BACKEND_AVAILABLE" || second.Header().Get("Retry-After") != "1" {
		t.Fatalf("breaker open, no backend: %d %q %v", second.Code, errCodeOf(second), second.Header())
	}
	recs := f.records()
	if len(recs) != 2 || second.Header().Get("X-Aiplex-Request-Id") != recs[1].RequestID {
		t.Fatalf("a refusal that never reached a backend still has its ledger record: %d records", len(recs))
	}
}

// A held provider error whose body is over the retained bound is replaced by
// AIPlex's own text; AIPlex wrote that body, so it carries a code.
func TestOversizedHeldProviderErrorIsAnAIPlexRefusal(t *testing.T) {
	f := newHeaderFixtureWith(t, func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":"` + strings.Repeat("x", routeHeldBodyLimit+10) + `"}`))
		return true
	})
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m", "b/m")})
	w := f.chat(hello)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "exceeds the retained bound") {
		t.Fatalf("%d %.200s", w.Code, w.Body)
	}
	if got := errCodeOf(w); got != "UPSTREAM_RESPONSE_TOO_LARGE" {
		t.Fatalf("%s = %q", errorcode.Header, got)
	}
}

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/routing"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// routeFixture serves an access point "r" whose models route between two
// independent mock providers (a and b) on openai-compatible surfaces.
type routeFixture struct {
	t        *testing.T
	h        *ConnectedHandler
	store    resources.Store
	ledger   usageledger.Store
	router   http.Handler
	a, b     *devstack.MockModels
	aURL     string
	mu       sync.Mutex
	identity *models.WIFIdentity
	scopes   []string
}

var routeModels = []string{"m", "m-b", "m-fault-s503", "m-fault-s503-n1", "m-fault-s500", "m-fault-s429-ra9", "m-fault-s429-n1-ra1", "m-fault-dropafter", "m-fault-dropbefore"}

func newRouteFixture(t *testing.T) *routeFixture {
	t.Helper()
	f := &routeFixture{t: t, a: &devstack.MockModels{}, b: &devstack.MockModels{Name: "b"}}
	sa, sb := httptest.NewServer(f.a), httptest.NewServer(f.b)
	t.Cleanup(sa.Close)
	t.Cleanup(sb.Close)
	f.aURL = sa.URL
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
		c.Surface.ID, c.Surface.Models = id, routeModels
		putProviderSurface(t, f.store, c)
	}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), keyRequestID, "request-1")
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
	return f
}

func (f *routeFixture) as(identity *models.WIFIdentity, scopes ...string) {
	f.mu.Lock()
	f.identity, f.scopes = identity, scopes
	f.mu.Unlock()
}

// model saves the access point with one model.
func (f *routeFixture) model(m AccessPointModel) {
	f.t.Helper()
	ap := AccessPoint{ID: "r", CallerKinds: []string{CallerUser}, Models: []AccessPointModel{m}}
	raw, _ := json.Marshal(ap)
	if _, err := f.h.validateAccessPoint(context.Background(), "tenant", "r", raw); err != nil {
		f.t.Fatalf("access point does not validate: %v", err)
	}
	prior, err := f.store.Get(context.Background(), "tenant", AccessPointKind, "r")
	revision := uint64(0)
	if err == nil {
		revision = prior.Revision
	}
	if _, err := f.store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: AccessPointKind, ID: "r", Spec: raw}, revision); err != nil {
		f.t.Fatal(err)
	}
}

func (f *routeFixture) chat(body string, headers ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest("POST", "/ap/r/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}

// records waits for the ledger and returns every record, oldest first.
func (f *routeFixture) records() []usageledger.Record {
	f.t.Helper()
	f.h.Ledger.Flush()
	page, err := f.ledger.List(context.Background(), "tenant", usageledger.Query{Limit: 100})
	if err != nil {
		f.t.Fatal(err)
	}
	out := page.Records
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func outcomes(r usageledger.Record) string {
	var parts []string
	for _, a := range r.Attempts {
		parts = append(parts, a.SurfaceRef+":"+a.Outcome)
	}
	return strings.Join(parts, ",")
}

func backends(pairs ...string) []routing.Backend {
	out := []routing.Backend{}
	for _, p := range pairs {
		surface, native, _ := strings.Cut(p, "/")
		out = append(out, routing.Backend{SurfaceRef: surface, NativeModel: native})
	}
	return out
}

const hello = `{"model":"m","messages":[{"role":"user","content":"hello routing"}]}`

func TestRoutedModelFallsOverOnUnavailable(t *testing.T) {
	f := newRouteFixture(t)
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m-fault-s503", "b/m-b")})
	w := f.chat(hello)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "[b] Hello") {
		t.Fatalf("fallback to b: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("X-Aiplex-Attempts") != "2" {
		t.Fatalf("X-Aiplex-Attempts = %q", w.Header().Get("X-Aiplex-Attempts"))
	}
	// Only the model changed between the two attempts.
	ra, rb := f.a.Requests(), f.b.Requests()
	if len(ra) != 1 || len(rb) != 1 || strings.Replace(ra[0].Body, "m-fault-s503", "m-b", 1) != rb[0].Body {
		t.Fatalf("bodies differ beyond the model:\n%v\n%v", ra, rb)
	}
	recs := f.records()
	if len(recs) != 1 {
		t.Fatalf("one record per exchange, got %d", len(recs))
	}
	r := recs[0]
	if outcomes(r) != "a:unavailable,b:ok" || r.Backend == nil || r.Backend.SurfaceRef != "b" || r.ResourceID != "b" || r.RoutingStrategy != "priority" || r.Status != usageledger.StatusOK {
		t.Fatalf("record %+v attempts %s", r, outcomes(r))
	}
	if r.Attempts[0].UpstreamStatus != 503 || r.Usage == nil || *r.Usage.InputTokens == 0 || r.Attempts[1].Reason != routing.ReasonPriority {
		t.Fatalf("attempt detail: %+v usage %+v", r.Attempts, r.Usage)
	}
	if r.SpanID == "" || r.TraceID == "" {
		t.Fatal("record must point at the exchange span")
	}
}

func TestRoutedModelRetriesOnlyWhenConfigured(t *testing.T) {
	f := newRouteFixture(t)
	// No routing.retry block: no retry on the same backend; 503 falls over.
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m-fault-s503-n1")})
	w := f.chat(hello)
	if w.Code != 503 || len(f.a.Requests()) != 1 || w.Header().Get("X-Aiplex-Attempts") != "1" {
		t.Fatalf("no retry by default: %d %s (%d upstream)", w.Code, w.Body, len(f.a.Requests()))
	}
	if !strings.Contains(w.Body.String(), "mock fault 503") {
		t.Fatalf("the last upstream error is relayed in its own shape: %s", w.Body)
	}
	f.a.ResetFaults()
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m-fault-s503-n1"), Routing: &routing.Routing{Retry: &routing.Retry{BaseBackoffMS: 1, MaxBackoffMS: 2}}})
	w = f.chat(hello)
	if w.Code != 200 || len(f.a.Requests()) != 3 {
		t.Fatalf("retry then success: %d %s (%d upstream)", w.Code, w.Body, len(f.a.Requests()))
	}
	recs := f.records()
	if got := outcomes(recs[len(recs)-1]); got != "a:unavailable,a:ok" {
		t.Fatalf("attempts %s", got)
	}
}

func TestRoutedModelNeverRetriesServerErrorsByDefault(t *testing.T) {
	f := newRouteFixture(t)
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m-fault-s500", "b/m-b"), Routing: &routing.Routing{Retry: &routing.Retry{}}})
	w := f.chat(hello)
	if w.Code != 500 || len(f.a.Requests()) != 1 || len(f.b.Requests()) != 0 {
		t.Fatalf("500 is final by default: %d (a=%d b=%d)", w.Code, len(f.a.Requests()), len(f.b.Requests()))
	}
	// Opt in: 500 retries and falls over.
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m-fault-s500", "b/m-b"), Routing: &routing.Routing{Retry: &routing.Retry{On: []string{"server_error"}, BaseBackoffMS: 1, MaxBackoffMS: 1}, Fallback: &routing.Fallback{On: []string{"server_error"}}}})
	w = f.chat(hello)
	if w.Code != 200 || len(f.a.Requests()) != 3 || len(f.b.Requests()) != 1 {
		t.Fatalf("opted-in 500: %d (a=%d b=%d)", w.Code, len(f.a.Requests()), len(f.b.Requests()))
	}
}

func TestRoutedModelHonoursRetryAfterCap(t *testing.T) {
	f := newRouteFixture(t)
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m-fault-s429-ra9", "b/m-b"), Routing: &routing.Routing{Retry: &routing.Retry{MaxRetryAfterMS: 1000}}})
	start := time.Now()
	w := f.chat(hello)
	if w.Code != 200 || len(f.a.Requests()) != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("a hint above the cap is not waited for: %d after %v (a=%d)", w.Code, time.Since(start), len(f.a.Requests()))
	}
	r := f.records()[0]
	if r.Attempts[0].RetryAfterMS == nil || *r.Attempts[0].RetryAfterMS != 9000 || r.Attempts[0].Outcome != routing.OutcomeRateLimited {
		t.Fatalf("retry hint recorded: %+v", r.Attempts[0])
	}
	// Within the cap the hint is honoured on the same backend.
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m-fault-s429-n1-ra1", "b/m-b"), Routing: &routing.Routing{Retry: &routing.Retry{MaxRetryAfterMS: 2000}}})
	start = time.Now()
	w = f.chat(hello)
	if w.Code != 200 || time.Since(start) < 900*time.Millisecond || strings.Contains(w.Body.String(), "[b]") {
		t.Fatalf("hint honoured: %d after %v %s", w.Code, time.Since(start), w.Body)
	}
}

func TestRoutedModelNeverRetriesAfterFirstStreamedByte(t *testing.T) {
	f := newRouteFixture(t)
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m-fault-dropafter", "b/m-b"), Routing: &routing.Routing{Retry: &routing.Retry{On: []string{"connect_error", "unavailable", "bad_gateway"}}, Fallback: &routing.Fallback{On: []string{"connect_error", "unavailable", "bad_gateway"}}}})
	w := f.chat(`{"model":"m","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "partial") || len(f.b.Requests()) != 0 || len(f.a.Requests()) != 1 {
		t.Fatalf("a broken stream is not retried: %d %q (a=%d b=%d)", w.Code, w.Body, len(f.a.Requests()), len(f.b.Requests()))
	}
	// A connection dropped before any byte is a transport error (the request
	// was sent): not retried, not failed over.
	w = f.chat(strings.Replace(hello, `"m"`, `"m"`, 1))
	_ = w
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m-fault-dropbefore", "b/m-b")})
	w = f.chat(hello)
	if w.Code != 502 || len(f.b.Requests()) != 0 {
		t.Fatalf("sent then dropped is final: %d %s (b=%d)", w.Code, w.Body, len(f.b.Requests()))
	}
	recs := f.records()
	if got := outcomes(recs[len(recs)-1]); got != "a:transport_error" {
		t.Fatalf("attempts %s", got)
	}
}

func TestRoutedModelConnectErrorFallsOver(t *testing.T) {
	f := newRouteFixture(t)
	// Point surface "a" at a closed port: the request never leaves.
	closed := httptest.NewServer(http.NotFoundHandler())
	origin := closed.URL
	closed.Close()
	f.h.Runtime, _ = connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{origin, f.aURL}, AllowLoopback: true, Timeout: 2 * time.Second}}, nil)
	installProviderCredentials(t, f.h)
	c := providerConnection(t, origin, modelapi.Provider{ID: modelapi.ProviderOpenAICompatible, UsageSemantics: modelapi.UsageSemanticsOpenAI}, "openai-chat", bearerRef, "compat.chat-completions")
	c.Surface.ID, c.Surface.Models = "down", routeModels
	putProviderSurface(t, f.store, c)
	c.Surface.ID, c.Surface.Origin = "a", f.aURL
	putProviderSurface(t, f.store, c)
	f.as(apUser("alice"), "llm:surface:a:*", "llm:surface:down:*", "llm:model:*")
	f.model(AccessPointModel{Name: "m", Backends: backends("down/m", "a/m")})
	w := f.chat(hello)
	if w.Code != 200 || len(f.a.Requests()) != 1 {
		t.Fatalf("connect error falls over: %d %s", w.Code, w.Body)
	}
	if got := outcomes(f.records()[0]); got != "down:connect_error,a:ok" {
		t.Fatalf("attempts %s", got)
	}
}

func TestRoutedModelBreakerOpensAndHalfOpens(t *testing.T) {
	f := newRouteFixture(t)
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m-fault-s503", "b/m-b"), Routing: &routing.Routing{Breaker: &routing.Breaker{FailureThreshold: 2, OpenMS: 200}}})
	for i := 0; i < 2; i++ {
		if w := f.chat(hello); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	w := f.chat(hello)
	if w.Code != 200 || len(f.a.Requests()) != 2 || w.Header().Get("X-Aiplex-Attempts") != "1" {
		t.Fatalf("open breaker skips a: %d a=%d attempts=%s", w.Code, len(f.a.Requests()), w.Header().Get("X-Aiplex-Attempts"))
	}
	recs := f.records()
	if got := outcomes(recs[2]); got != "a:breaker_open,b:ok" {
		t.Fatalf("attempts %s", got)
	}
	time.Sleep(250 * time.Millisecond)
	if w = f.chat(hello); w.Code != 200 || len(f.a.Requests()) != 3 {
		t.Fatalf("half-open probe reaches a: a=%d", len(f.a.Requests()))
	}
	recs = f.records()
	if recs[3].Attempts[0].BreakerState != routing.StateHalfOpen {
		t.Fatalf("probe attempt state %+v", recs[3].Attempts[0])
	}
	// The operator view shows the state and the last outcomes.
	r := httptest.NewRequest("GET", "/api/v1/access-points/r/routing", nil)
	rw := httptest.NewRecorder()
	f.router.ServeHTTP(rw, r)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), `"state":"open"`) || !strings.Contains(rw.Body.String(), `"outcome":"unavailable"`) {
		t.Fatalf("routing view: %d %s", rw.Code, rw.Body)
	}
}

func TestRoutedModelSkipsBackendsTheCallerIsNotGranted(t *testing.T) {
	f := newRouteFixture(t)
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m", "b/m-b")})
	f.as(apUser("bob"), "llm:surface:b:*", "llm:model:m-b")
	w := f.chat(hello)
	if w.Code != 200 || len(f.a.Requests()) != 0 || !strings.Contains(w.Body.String(), "[b]") {
		t.Fatalf("ungranted a is skipped: %d %s", w.Code, w.Body)
	}
	if got := outcomes(f.records()[0]); got != "a:not_granted,b:ok" {
		t.Fatalf("attempts %s", got)
	}
	f.as(apUser("carol"))
	w = f.chat(hello)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "llm:surface:a:") || strings.Contains(w.Body.String(), "llm:surface:b") {
		t.Fatalf("no grant anywhere: the refusal names the primary backend only: %d %s", w.Code, w.Body)
	}
}

func TestRoutedModelLowestCostWithUnknownPrice(t *testing.T) {
	f := newRouteFixture(t)
	// Only b's model has a price: a's is unknown and is never assumed cheap.
	f.store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: usageledger.PriceKind, ID: "m-b", Spec: json.RawMessage(`{"id":"m-b","model_id":"m-b","currency":"USD","scale":6,"meters":{"input_tokens":{"numerator":1,"denominator":1},"output_tokens":{"numerator":2,"denominator":1}}}`)}, 0)
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m", "b/m-b"), Routing: &routing.Routing{Strategy: routing.StrategyLowestCost}})
	w := f.chat(hello)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "[b]") {
		t.Fatalf("priced backend first: %d %s", w.Code, w.Body)
	}
	r := f.records()[0]
	if r.RoutingStrategy != "lowest_cost" || r.Attempts[0].Reason != routing.ReasonLowestCost {
		t.Fatalf("reason recorded: %+v", r.Attempts)
	}
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m", "b/m-b"), Routing: &routing.Routing{Strategy: routing.StrategyLowestCost, LowestCost: &routing.LowestCost{RequirePrice: true}}})
	f.chat(hello)
	recs := f.records()
	if got := outcomes(recs[len(recs)-1]); got != "a:backend_unavailable,b:ok" || recs[len(recs)-1].Attempts[0].Reason != routing.ReasonExcludedNoCost {
		t.Fatalf("require_price excludes a: %s", got)
	}
}

func TestRoutedModelRunsRequestHooksOnce(t *testing.T) {
	f := newRouteFixture(t)
	putHookPolicy(t, f.store, `{"id":"pii","scope":{"kind":"tenant"},"hooks":[{"id":"mask","type":"redact","phases":["request"],"redact":{"rules":[{"id":"email","builtin":"email","action":"mask"}]}}]}`)
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m-fault-s503", "b/m-b")})
	w := f.chat(`{"model":"m","messages":[{"role":"user","content":"mail jane@example.com"}]}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	ra, rb := f.a.Requests(), f.b.Requests()
	if !strings.Contains(ra[0].Body, "[EMAIL]") || strings.Replace(ra[0].Body, "m-fault-s503", "m-b", 1) != rb[0].Body {
		t.Fatalf("the hooked body is dispatched to both backends with only the model rewritten:\n%s\n%s", ra[0].Body, rb[0].Body)
	}
	r := f.records()[0]
	if r.Hooks == nil || len(r.Hooks.Results) != 1 || r.Hooks.Decision != usageledger.HookTransform {
		t.Fatalf("the hooks ran once: %+v", r.Hooks)
	}
}

func TestRoutedModelCache(t *testing.T) {
	f := newRouteFixture(t)
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m", "b/m-b"), Cache: &routing.Cache{TTL: "1m"}})
	first := f.chat(hello)
	if first.Code != 200 || first.Header().Get("X-Aiplex-Cache") != "miss" {
		t.Fatalf("miss: %d %q", first.Code, first.Header().Get("X-Aiplex-Cache"))
	}
	second := f.chat(strings.Replace(hello, `{"model":"m",`, `{ "model" : "m" ,`, 1))
	if second.Code != 200 || second.Header().Get("X-Aiplex-Cache") != "hit" || second.Body.String() != first.Body.String() || len(f.a.Requests()) != 1 {
		t.Fatalf("hit: %d %q upstream=%d\n%s\n%s", second.Code, second.Header().Get("X-Aiplex-Cache"), len(f.a.Requests()), first.Body, second.Body)
	}
	if second.Header().Get("X-Aiplex-Attempts") != "0" {
		t.Fatalf("a hit asks no provider: %q", second.Header().Get("X-Aiplex-Attempts"))
	}
	// Another principal never gets alice's answer under the default scope.
	f.as(apUser("bob"), "llm:surface:a:*", "llm:surface:b:*", "llm:model:*")
	third := f.chat(hello)
	if third.Header().Get("X-Aiplex-Cache") != "miss" || len(f.a.Requests()) != 2 {
		t.Fatalf("principal isolation: %q upstream=%d", third.Header().Get("X-Aiplex-Cache"), len(f.a.Requests()))
	}
	// Streaming, no-store and user identifiers bypass.
	for _, c := range []struct{ body, header string }{
		{`{"model":"m","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hello routing"}]}`, ""},
		{hello, "no-store"},
		{`{"model":"m","user":"u-1","messages":[{"role":"user","content":"hello routing"}]}`, ""},
	} {
		w := f.chat(c.body, "Cache-Control", c.header)
		if w.Header().Get("X-Aiplex-Cache") != "bypass" {
			t.Fatalf("bypass expected for %s %s: %q", c.body, c.header, w.Header().Get("X-Aiplex-Cache"))
		}
	}
	recs := f.records()
	hit := recs[1]
	if hit.Cache == nil || hit.Cache.Status != "hit" || hit.Cost.Basis != usageledger.BasisCached || hit.Cost.Amount != "0" || hit.CachedResponseUsage == nil || *hit.CachedResponseUsage.InputTokens == 0 || *hit.Usage.InputTokens != 0 {
		t.Fatalf("hit record: cache=%+v cost=%+v usage=%+v cached=%+v", hit.Cache, hit.Cost, hit.Usage, hit.CachedResponseUsage)
	}
	if recs[0].Cache.Status != "miss" || recs[4].Cache.Reason != "no_store" || recs[5].Cache.Reason != "user_identifier" {
		t.Fatalf("cache reasons: %+v %+v %+v", recs[0].Cache, recs[4].Cache, recs[5].Cache)
	}
	sum, err := usageledger.Summarize(context.Background(), f.ledger, "tenant", usageledger.SummaryQuery{})
	if err != nil || len(sum.Groups) != 1 || sum.Groups[0].CachedResponseRecords != 1 || sum.Groups[0].UnknownCostRecords != int64(len(recs)-1) {
		t.Fatalf("summary counts the hit apart and not as unknown cost: %+v %v", sum.Groups, err)
	}
	// Operators can purge.
	r := httptest.NewRequest("POST", "/api/v1/access-points/r/cache/purge", nil)
	rw := httptest.NewRecorder()
	f.router.ServeHTTP(rw, r)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), `"purged":2`) {
		t.Fatalf("purge: %d %s", rw.Code, rw.Body)
	}
}

func TestRoutedModelValidation(t *testing.T) {
	f := newRouteFixture(t)
	emb := providerConnection(t, f.aURL, modelapi.Provider{ID: modelapi.ProviderOpenAI}, "openai-embeddings", bearerRef, "openai.v1.embeddings")
	emb.Surface.ID, emb.Surface.Models = "emb", []string{"m"}
	f.h.Runtime, _ = connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{f.aURL}, AllowLoopback: true}}, nil)
	putProviderSurface(t, f.store, emb)
	check := func(m AccessPointModel, want string) {
		t.Helper()
		raw, _ := json.Marshal(AccessPoint{ID: "r", CallerKinds: []string{CallerUser}, Models: []AccessPointModel{m}})
		_, err := f.h.validateAccessPoint(context.Background(), "tenant", "r", raw)
		if want == "" && err != nil {
			t.Fatalf("valid model refused: %v", err)
		}
		if want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Fatalf("want %q, got %v", want, err)
		}
	}
	check(AccessPointModel{Name: "m", Backends: backends("a/m", "emb/m")}, "backend 2 (emb/m) serves")
	check(AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m", Backends: backends("b/m")}, "either surface_ref")
	check(AccessPointModel{Name: "m", Backends: backends("a/m", "a/m")}, "listed twice")
	check(AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m", Cache: &routing.Cache{TTL: "2d"}}, "cache.ttl")
	check(AccessPointModel{Name: "m", Backends: backends("a/m", "b/m"), Routing: &routing.Routing{Retry: &routing.Retry{On: []string{"transport_error"}}}}, "routing.retry.on")
	check(AccessPointModel{Name: "m", SurfaceRef: "a", NativeModel: "m", Cache: &routing.Cache{TTL: "10m", Scope: "access_point"}}, "")
}

func TestRouteAttemptClassification(t *testing.T) {
	x := &routeExchange{policy: (&routing.Routing{}).Effective()}
	cases := []struct {
		set    func(*routeAttempt)
		status int
		want   string
	}{
		{func(a *routeAttempt) { a.errorCode = "QUOTA_RATE_LIMITED" }, 429, routing.OutcomeQuotaDenied},
		{func(a *routeAttempt) { a.errorCode = "QUOTA_UNAVAILABLE" }, 503, routing.OutcomeRefused},
		{func(a *routeAttempt) { a.errorCode = "CAPACITY_COOLDOWN" }, 429, routing.OutcomeCapacity},
		{func(a *routeAttempt) { a.notGranted = true }, 403, routing.OutcomeNotGranted},
		{func(a *routeAttempt) { a.dispatched, a.dispatch = true, modelapi.DispatchOutcome{Status: 529} }, 529, routing.OutcomeOverloaded},
		{func(a *routeAttempt) { a.dispatched, a.dispatch = true, modelapi.DispatchOutcome{Status: 200} }, 502, routing.OutcomeRefused},
		{func(a *routeAttempt) {
			a.dispatched, a.dispatch = true, modelapi.DispatchOutcome{Err: context.DeadlineExceeded}
		}, 502, routing.OutcomeTransportError},
		{func(a *routeAttempt) {}, 400, routing.OutcomeRefused},
	}
	for i, c := range cases {
		a := &routeAttempt{route: x, number: 1}
		c.set(a)
		a.classify(c.status)
		if a.outcome != c.want {
			t.Errorf("case %d: %s, want %s", i, a.outcome, c.want)
		}
	}
	if !x.policy.FallsOver(routing.OutcomeCapacity) || x.policy.FallsOver(routing.OutcomeQuotaDenied) {
		t.Fatal("capacity is per backend and falls over; quota is about the caller and is final")
	}
}

func TestRoutedModelRefusalsKeepTheCallersProtocol(t *testing.T) {
	f := newRouteFixture(t)
	putHookPolicy(t, f.store, hookPolicySpec)
	f.model(AccessPointModel{Name: "m", Backends: backends("a/m", "b/m-b")})
	w := f.chat(`{"model":"m","messages":[{"role":"user","content":"-----BEGIN PRIVATE KEY-----"}]}`)
	if w.Code != 403 || !strings.Contains(w.Body.String(), `"code":"hook_denied"`) || w.Header().Get("X-Aiplex-Error-Code") != "hook_denied" || len(f.a.Requests())+len(f.b.Requests()) != 0 {
		t.Fatalf("a hook denial is final and protocol-native: %d %s", w.Code, w.Body)
	}
	if got := outcomes(f.records()[0]); got != "a:refused" {
		t.Fatalf("attempts %s", got)
	}
}

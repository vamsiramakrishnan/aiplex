package api_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
)

// ── BearerToken ───────────────────────────────────────────────────────────

func mountWith(mw func(http.Handler) http.Handler, h http.HandlerFunc) http.Handler {
	r := chi.NewRouter()
	r.Use(mw)
	r.Post("/x", h)
	return r
}

func TestBearerToken_MissingHeader_401(t *testing.T) {
	t.Setenv("TAPE_INGEST_TOKEN", "right")
	srv := mountWith(api.BearerToken("TAPE_INGEST_TOKEN"),
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without bearer, got %d", rec.Code)
	}
}

func TestBearerToken_WrongToken_401(t *testing.T) {
	t.Setenv("TAPE_INGEST_TOKEN", "right")
	srv := mountWith(api.BearerToken("TAPE_INGEST_TOKEN"),
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong bearer, got %d", rec.Code)
	}
}

func TestBearerToken_RightToken_Passes(t *testing.T) {
	t.Setenv("TAPE_INGEST_TOKEN", "right")
	srv := mountWith(api.BearerToken("TAPE_INGEST_TOKEN"),
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("Authorization", "Bearer right")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 with right bearer, got %d", rec.Code)
	}
}

func TestBearerToken_EmptyEnv_503(t *testing.T) {
	t.Setenv("TAPE_INGEST_TOKEN", "")
	srv := mountWith(api.BearerToken("TAPE_INGEST_TOKEN"),
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (fail-closed) on empty env, got %d", rec.Code)
	}
}

// ── IPRateLimit ───────────────────────────────────────────────────────────

func TestIPRateLimit_AllowsBurstThenRateLimits(t *testing.T) {
	srv := mountWith(api.IPRateLimit(2, 3), // 2 req/sec, burst 3
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	// First 3 from one IP go through (burst).
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Errorf("burst slot %d: expected 204, got %d", i, rec.Code)
		}
	}
	// 4th should be rate-limited.
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("post-burst: expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "1" {
		t.Errorf("expected Retry-After header, got %q", rec.Header().Get("Retry-After"))
	}
}

func TestIPRateLimit_DifferentIPs_IndependentBuckets(t *testing.T) {
	srv := mountWith(api.IPRateLimit(1, 1),
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })

	// IP A exhausts its bucket.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		_ = rec.Code // first 204, second 429 — either is fine
	}
	// IP B still has its full bucket.
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "10.0.0.99:1234"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("different IP should not be limited: %d", rec.Code)
	}
}

// ── Idempotency ───────────────────────────────────────────────────────────

func TestIdempotency_SameKey_Dedupes(t *testing.T) {
	var hits int32
	srv := mountWith(api.Idempotency(func(r *http.Request) string { return "x" }),
		func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"hit":1}`))
		})

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"a":1}`)))
		req.Header.Set("Idempotency-Key", "same-key")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Errorf("attempt %d: expected 202, got %d", i, rec.Code)
		}
	}
	if h := atomic.LoadInt32(&hits); h != 1 {
		t.Errorf("expected handler called once, got %d", h)
	}
}

func TestIdempotency_DifferentKeys_BothFire(t *testing.T) {
	var hits int32
	srv := mountWith(api.Idempotency(func(r *http.Request) string { return "x" }),
		func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(204)
		})

	for _, key := range []string{"key-a", "key-b"} {
		req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Errorf("expected 204 for fresh call, got %d", rec.Code)
		}
	}
	if h := atomic.LoadInt32(&hits); h != 2 {
		t.Errorf("expected 2 handler calls, got %d", h)
	}
}

func TestIdempotency_DerivedKeyFromBody(t *testing.T) {
	var hits int32
	srv := mountWith(api.Idempotency(func(r *http.Request) string { return "x" }),
		func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(204)
		})

	// Same body, no explicit key → derived hash dedupes. Build a fresh
	// reader on each call so the previous Read doesn't leave the stream
	// drained (the middleware does ReadAll once and replays anyway, but
	// we want the test's intent to be clear).
	for i := 0; i < 2; i++ {
		srv.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"a":1}`))))
	}

	if h := atomic.LoadInt32(&hits); h != 1 {
		t.Errorf("expected derived key to dedupe, got %d calls", h)
	}
}

func TestIdempotency_4xx_NotCached(t *testing.T) {
	var hits int32
	srv := mountWith(api.Idempotency(func(r *http.Request) string { return "x" }),
		func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(http.StatusBadRequest)
		})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Idempotency-Key", "k1")
		srv.ServeHTTP(httptest.NewRecorder(), req)
	}
	// Both calls should hit the handler because 400 is not cached.
	if h := atomic.LoadInt32(&hits); h != 2 {
		t.Errorf("expected non-cached failures to refire, got %d calls", h)
	}
}

func TestIdempotency_BodyReplayable(t *testing.T) {
	// Downstream handler must see the original body.
	srv := mountWith(api.Idempotency(func(r *http.Request) string { return "x" }),
		func(w http.ResponseWriter, r *http.Request) {
			buf := make([]byte, 100)
			n, _ := r.Body.Read(buf)
			if !strings.Contains(string(buf[:n]), "treasure") {
				t.Errorf("body not visible downstream: %q", buf[:n])
			}
			w.WriteHeader(http.StatusOK)
		})
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"treasure":true}`)))
	srv.ServeHTTP(httptest.NewRecorder(), req)
}

// ── TenantFromContext + RequireTenant ────────────────────────────────────

func TestTenantFromContext_FromScope(t *testing.T) {
	ctx := context.Background()
	// Without WIFAccess present, returns empty.
	gotID, gotCross, err := api.TenantFromContext(ctx)
	if gotID != "" || gotCross || err != nil {
		t.Errorf("expected ('', false, nil) for empty ctx, got (%q, %v, %v)", gotID, gotCross, err)
	}
}

// Outside development mode, a request with no verified identity is refused
// even with the tenant opt-out set: the opt-out admits tenantless verified
// callers, never unauthenticated ones. (The full matrix is in
// runs_tenant_test.go.)
func TestRequireTenant_Unauthenticated_Refused(t *testing.T) {
	for _, setting := range []string{"", "1", "0"} {
		t.Setenv("AIPLEX_REQUIRE_TENANT", setting)
		srv := chi.NewRouter()
		srv.Use(api.RequireTenant)
		srv.Get("/x", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("setting %q: expected 401, got %d", setting, rec.Code)
		}
	}
}

// ── exercise the 5-minute TTL via tiny helper ─────────────────────────────

func TestIdempotency_CacheReleasesAfterTTL(t *testing.T) {
	// We can't easily wait 5 minutes; this test asserts the structural
	// invariant: a fresh key always hits the handler, even when only
	// one digit of the body changes. (Real TTL behaviour is exercised by
	// TestIdempotency_ExpiredEntryRunsAgain.)
	var hits int32
	srv := mountWith(api.Idempotency(func(r *http.Request) string { return "x" }),
		func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(204)
		})
	// Two distinct bodies.
	srv.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"a":1}`))))
	srv.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"a":2}`))))
	if h := atomic.LoadInt32(&hits); h != 2 {
		t.Errorf("expected distinct hashes to both fire, got %d", h)
	}
	_ = time.Now() // keep the time import (we're documenting TTL semantics)
}

func idempotentPost(srv http.Handler, ctx context.Context, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body)).WithContext(ctx)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestIdempotency_ConcurrentIdenticalRequests_RunOnce(t *testing.T) {
	const n = 16
	var hits int32
	started := make(chan struct{})
	release := make(chan struct{})
	srv := mountWith(api.Idempotency(func(r *http.Request) string { return "x" }),
		func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&hits, 1) == 1 {
				close(started)
			}
			<-release
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"run":"once"}`))
		})

	results := make(chan *httptest.ResponseRecorder, n)
	for i := 0; i < n; i++ {
		go func() { results <- idempotentPost(srv, context.Background(), "same", `{"a":1}`) }()
	}
	<-started
	time.Sleep(50 * time.Millisecond) // let the others join the in-flight call
	close(release)

	replays := 0
	for i := 0; i < n; i++ {
		rec := <-results
		if rec.Code != http.StatusAccepted || rec.Body.String() != `{"run":"once"}` {
			t.Errorf("caller %d: got %d %q", i, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("caller %d: headers not replayed: %v", i, rec.Header())
		}
		if rec.Header().Get("X-Idempotent-Replay") == "true" {
			replays++
		}
	}
	// Late arrivals are answered from the cache, so this holds however the
	// goroutines are scheduled; the handler must still run exactly once.
	if h := atomic.LoadInt32(&hits); h != 1 {
		t.Fatalf("expected handler to run once, ran %d times", h)
	}
	if replays != n-1 {
		t.Errorf("expected %d replays, got %d", n-1, replays)
	}
}

func TestIdempotency_Non2xx_NotCachedAfterCompletion(t *testing.T) {
	var hits int32
	srv := mountWith(api.Idempotency(func(r *http.Request) string { return "x" }),
		func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"busy"}`))
		})
	for i := 1; i <= 3; i++ {
		rec := idempotentPost(srv, context.Background(), "k", `{}`)
		if rec.Code != http.StatusConflict || rec.Header().Get("X-Idempotent-Replay") != "" {
			t.Fatalf("attempt %d: got %d replay=%q", i, rec.Code, rec.Header().Get("X-Idempotent-Replay"))
		}
		if h := atomic.LoadInt32(&hits); h != int32(i) {
			t.Fatalf("attempt %d: expected %d handler runs, got %d", i, i, h)
		}
	}
}

func TestIdempotency_SizeCapEvictsOldest(t *testing.T) {
	var hits int32
	srv := mountWith(api.IdempotencyWithLimits(func(r *http.Request) string { return "x" }, 2, time.Minute),
		func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(http.StatusOK)
		})
	for _, key := range []string{"a", "b", "c"} {
		idempotentPost(srv, context.Background(), key, `{}`)
	}
	// "b" and "c" are still cached; "a" was evicted when "c" was added.
	for _, key := range []string{"c", "b"} {
		if rec := idempotentPost(srv, context.Background(), key, `{}`); rec.Header().Get("X-Idempotent-Replay") != "true" {
			t.Errorf("key %q: expected replay", key)
		}
	}
	if h := atomic.LoadInt32(&hits); h != 3 {
		t.Fatalf("expected 3 runs before the eviction check, got %d", h)
	}
	if rec := idempotentPost(srv, context.Background(), "a", `{}`); rec.Header().Get("X-Idempotent-Replay") != "" {
		t.Errorf("evicted key replayed")
	}
	if h := atomic.LoadInt32(&hits); h != 4 {
		t.Errorf("expected the evicted key to run again (4 runs), got %d", h)
	}
}

func TestIdempotency_ExpiredEntryRunsAgain(t *testing.T) {
	var hits int32
	srv := mountWith(api.IdempotencyWithLimits(func(r *http.Request) string { return "x" }, 10, 20*time.Millisecond),
		func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(http.StatusOK)
		})
	idempotentPost(srv, context.Background(), "k", `{}`)
	time.Sleep(40 * time.Millisecond)
	idempotentPost(srv, context.Background(), "k", `{}`)
	if h := atomic.LoadInt32(&hits); h != 2 {
		t.Errorf("expected an expired entry to run again, got %d runs", h)
	}
}

func TestIdempotency_FirstCallerCancellationDoesNotAbortHandler(t *testing.T) {
	var hits int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := mountWith(api.Idempotency(func(r *http.Request) string { return "x" }),
		func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			cancel() // the first client disconnects mid-action
			if err := r.Context().Err(); err != nil {
				t.Errorf("handler context cancelled by the client: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
		})
	idempotentPost(srv, ctx, "k", `{}`)
	rec := idempotentPost(srv, context.Background(), "k", `{}`)
	if rec.Code != http.StatusAccepted || rec.Header().Get("X-Idempotent-Replay") != "true" {
		t.Errorf("retry after disconnect: got %d replay=%q", rec.Code, rec.Header().Get("X-Idempotent-Replay"))
	}
	if h := atomic.LoadInt32(&hits); h != 1 {
		t.Errorf("expected one run, got %d", h)
	}
}

func TestIdempotency_PanicPropagatesAndIsNotCached(t *testing.T) {
	var hits int32
	srv := mountWith(api.Idempotency(func(r *http.Request) string { return "x" }),
		func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&hits, 1) == 1 {
				panic(http.ErrAbortHandler)
			}
			w.WriteHeader(http.StatusOK)
		})
	func() {
		defer func() {
			if got := recover(); got != http.ErrAbortHandler {
				t.Errorf("expected the original panic value, got %v", got)
			}
		}()
		idempotentPost(srv, context.Background(), "k", `{}`)
	}()
	if rec := idempotentPost(srv, context.Background(), "k", `{}`); rec.Code != http.StatusOK || rec.Header().Get("X-Idempotent-Replay") != "" {
		t.Errorf("after panic: got %d replay=%q", rec.Code, rec.Header().Get("X-Idempotent-Replay"))
	}
}

func TestIdempotency_RecorderUnwraps(t *testing.T) {
	srv := mountWith(api.Idempotency(func(r *http.Request) string { return "x" }),
		func(w http.ResponseWriter, r *http.Request) {
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Errorf("ResponseController cannot reach the writer: %v", err)
			}
		})
	idempotentPost(srv, context.Background(), "k", `{}`)
}

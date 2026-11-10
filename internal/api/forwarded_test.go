package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

func forwardedRequest(xff ...string) *http.Request {
	r := httptest.NewRequest("GET", "/x", nil)
	r.RemoteAddr = "192.0.2.10:5555"
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func TestClientIPIgnoresForwardedForByDefault(t *testing.T) {
	if got := clientIPWithHops(forwardedRequest("203.0.113.9"), 0); got != "192.0.2.10" {
		t.Fatalf("0 hops: %q, want the peer address", got)
	}
}

func TestClientIPTakesEntriesFromTheRight(t *testing.T) {
	// The client wrote 198.51.100.1 (spoofed); the edge proxy appended the
	// real client 203.0.113.9; an inner proxy appended the edge 10.0.0.2.
	r := forwardedRequest("198.51.100.1, 203.0.113.9", "10.0.0.2")
	if got := clientIPWithHops(r, 1); got != "10.0.0.2" {
		t.Fatalf("1 hop: %q", got)
	}
	if got := clientIPWithHops(r, 2); got != "203.0.113.9" {
		t.Fatalf("2 hops: %q", got)
	}
	if got := clientIPWithHops(forwardedRequest("203.0.113.9"), 2); got != "192.0.2.10" {
		t.Fatalf("fewer entries than hops must fall back to the peer: %q", got)
	}
}

func TestClientIPMalformedOrOversizedFallsBackToPeer(t *testing.T) {
	for _, v := range []string{"not-an-ip", "203.0.113.9, ", "203.0.113.9;rm", strings.Repeat("203.0.113.9, ", 300) + "203.0.113.9"} {
		if got := clientIPWithHops(forwardedRequest(v), 1); got != "192.0.2.10" {
			t.Errorf("%.40q: %q, want the peer address", v, got)
		}
	}
}

// Rotating X-Forwarded-For must not give an anonymous caller fresh buckets,
// whatever the trusted hop setting (the anonymous key is the peer address).
func TestAnonymousRateLimitIgnoresRotatedForwardedFor(t *testing.T) {
	SetTrustedProxyHops(0)
	defer SetTrustedProxyHops(0)
	for _, limiter := range []http.Handler{
		RateLimit(5)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		IPRateLimit(1, 3)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
	} {
		limited := false
		for i := 0; i < 20; i++ {
			r := forwardedRequest("203.0.113." + strconv.Itoa(i))
			w := httptest.NewRecorder()
			limiter.ServeHTTP(w, r)
			limited = limited || w.Code == http.StatusTooManyRequests
		}
		if !limited {
			t.Fatal("rotating X-Forwarded-For bypassed the per-address rate limit")
		}
	}
}

func TestDevelopmentRefusesProxiedAnonymousRequests(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	dev := RefuseProxiedAnonymousInDevelopment(true)(ok)
	prod := RefuseProxiedAnonymousInDevelopment(false)(ok)
	for _, h := range []string{"X-Forwarded-For", "Forwarded", "X-Real-IP"} {
		r := httptest.NewRequest("GET", "/api/v1/access-points", nil)
		r.Header.Set(h, "203.0.113.9")
		w := httptest.NewRecorder()
		dev.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "PROXIED_DEVELOPMENT_REQUEST") || !strings.Contains(w.Body.String(), h) {
			t.Errorf("%s: %d %s", h, w.Code, w.Body)
		}
		// Outside development mode the rule does nothing.
		w = httptest.NewRecorder()
		prod.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Errorf("%s outside development mode: %d", h, w.Code)
		}
		// A verified caller through a proxy is not anonymous.
		verified := r.WithContext(context.WithValue(r.Context(), keyWIFIdentity, &models.WIFIdentity{PrincipalID: "principal-x"}))
		w = httptest.NewRecorder()
		dev.ServeHTTP(w, verified)
		if w.Code != 204 {
			t.Errorf("%s with a verified identity: %d", h, w.Code)
		}
	}
	// A direct local client sends none of them.
	w := httptest.NewRecorder()
	dev.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/access-points", nil))
	if w.Code != 204 {
		t.Errorf("direct anonymous request in development mode: %d", w.Code)
	}
}

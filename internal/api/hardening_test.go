package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestCORSNeverReflectsArbitraryOriginsWithAWildcard(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	for _, test := range []struct {
		name, origin string
		allowed      []string
		want         string
	}{
		{"configured origin", "https://console.example", []string{"https://console.example"}, "https://console.example"},
		{"unlisted origin", "https://evil.example", []string{"https://console.example"}, ""},
		{"wildcard is literal", "https://evil.example", []string{"*"}, "*"},
		{"no configuration", "https://evil.example", nil, ""},
	} {
		r := httptest.NewRequest("GET", "/api/v1/iam/whoami", nil)
		r.Header.Set("Origin", test.origin)
		w := httptest.NewRecorder()
		CORS(test.allowed...)(ok).ServeHTTP(w, r)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != test.want {
			t.Errorf("%s: Access-Control-Allow-Origin %q, want %q", test.name, got, test.want)
		}
		if w.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("%s: credentials must never be allowed cross-origin", test.name)
		}
		if test.want != "" && test.want != "*" && !strings.Contains(w.Header().Get("Vary"), "Origin") {
			t.Errorf("%s: reflected origin without Vary: Origin", test.name)
		}
	}
}

func TestRequestIDIsBoundedAndPrintable(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = GetRequestID(r.Context()) }))
	for _, test := range []struct{ in, want string }{{"abc-123", "abc-123"}, {"evil\nlog-line", ""}, {strings.Repeat("a", 500), ""}} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Request-Id", test.in)
		h.ServeHTTP(httptest.NewRecorder(), r)
		if test.want != "" && seen != test.want {
			t.Errorf("request id %q became %q", test.in, seen)
		}
		if test.want == "" && (seen == test.in || !strings.HasPrefix(seen, "req-")) {
			t.Errorf("unsafe request id %q was accepted as %q", test.in, seen)
		}
	}
}

func TestAuthenticatedResponsesAreNotCacheable(t *testing.T) {
	router := chi.NewRouter()
	router.Use(RoutePolicy(router, RoutePolicyConfig{Development: true, Rules: []RouteRule{{Pattern: "/public", Access: RoutePublic}}}))
	router.Get("/private", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	router.Get("/public", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/private", nil))
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("authenticated route Cache-Control %q", w.Header().Get("Cache-Control"))
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/public", nil))
	if w.Header().Get("Cache-Control") == "no-store" {
		t.Fatal("public routes keep their own caching")
	}
}

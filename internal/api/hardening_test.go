package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
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

func TestBrowserGatewayHeaders(t *testing.T) {
	const console = "https://console.example"
	for _, tc := range []struct {
		name, origin  string
		allowed       []string
		expose, named bool
	}{
		{"named origin", console, []string{console}, true, true},
		{"unlisted origin", "https://other.example", []string{console}, false, false},
		{"wildcard", console, []string{"*"}, true, false},
		{"named and wildcard", console, []string{"*", console}, true, true},
		{"fallback wildcard", "https://other.example", []string{"*", console}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodOptions, "/api/v1/mcp/connections/local", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Access-Control-Request-Method", http.MethodPut)
			r.Header.Set("Access-Control-Request-Headers", "authorization, if-match")
			w := httptest.NewRecorder()
			CORS(tc.allowed...)(http.NotFoundHandler()).ServeHTTP(w, r)
			if w.Code != http.StatusNoContent {
				t.Fatalf("preflight: %d", w.Code)
			}
			if w.Header().Get("Vary") != "Origin" {
				t.Error("preflight header policy varies by origin")
			}
			allow := strings.Split(w.Header().Get("Access-Control-Allow-Headers"), ", ")
			for _, name := range []string{"If-Match", "X-Client-Request-Id", "Traceparent", "Tracestate"} {
				if slices.Contains(allow, name) != tc.named {
					t.Errorf("allowed %s: %v", name, allow)
				}
			}
			r = httptest.NewRequest(http.MethodGet, "/ap/test/v1/models", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Accept-Encoding", "gzip")
			w = httptest.NewRecorder()
			CORS(tc.allowed...)(Compress(http.NotFoundHandler())).ServeHTTP(w, r)
			if tc.named && !slices.Contains(w.Header().Values("Vary"), "Origin") {
				t.Error("compression discarded the origin cache key")
			}
			expose := strings.Split(w.Header().Get("Access-Control-Expose-Headers"), ", ")
			for _, name := range []string{"X-Aiplex-Request-Id", "X-Aiplex-Error-Code", "X-Aiplex-Attempt-ID"} {
				if slices.Contains(expose, name) != tc.expose {
					t.Errorf("exposed %s: %v", name, expose)
				}
			}
		})
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

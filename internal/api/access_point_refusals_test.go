package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/errorcode"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// The inventory of refusals AIPlex itself writes under /ap/{id}/ (the table in
// docs/access-points.md): each carries X-Aiplex-Error-Code with AIPlex's own
// code, every response carries X-Aiplex-Request-Id, and a provider's error that
// AIPlex relays carries no code.

func (f *apFixture) serve(method, path, body string, headers ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	RequestID(f.router).ServeHTTP(w, r)
	return w
}

func TestAccessPointRefusalInventory(t *testing.T) {
	type refusal struct {
		name           string
		method, path   string
		body           string
		headers        []string
		prepare        func(f *apFixture)
		status         int
		code           string
		wantBodyHasStr string
	}
	mcp := []string{"Accept", "application/json, text/event-stream", "MCP-Protocol-Version", "2025-06-18"}
	rpc := func(method, params string) string {
		return `{"jsonrpc":"2.0","id":7,"method":"` + method + `"` + params + `}`
	}
	cases := []refusal{
		// Router.
		{name: "route not served", method: "POST", path: "/ap/ide/v1/nothing", body: `{}`, status: 404, code: "NOT_FOUND"},
		{name: "method not served", method: "GET", path: "/ap/ide/v1/chat/completions", status: 405, code: "METHOD_NOT_ALLOWED"},
		{name: "the /ap/ prefix itself", method: "POST", path: "/ap/", body: `{}`, status: 404, code: "NOT_FOUND"},
		{name: "the /ap/ prefix itself, GET", method: "GET", path: "/ap/", status: 404, code: "NOT_FOUND"},
		// The gate.
		{name: "not authenticated", method: "POST", path: "/ap/ide/v1/chat/completions", body: `{"model":"chat-model"}`, prepare: func(f *apFixture) { f.as(nil) }, status: 401, code: "UNAUTHENTICATED"},
		{name: "access point not found", method: "POST", path: "/ap/missing/v1/chat/completions", body: `{"model":"chat-model"}`, status: 404, code: "NOT_FOUND"},
		{name: "caller not admitted", method: "POST", path: "/ap/ide/v1/chat/completions", body: `{"model":"chat-model"}`, prepare: func(f *apFixture) {
			agent := apUser("robot")
			agent.IsWorkforce = false
			f.as(agent, apAllScopes...)
		}, status: 403, code: "CALLER_NOT_ALLOWED"},
		// Model routes.
		{name: "body is not one JSON object", method: "POST", path: "/ap/ide/v1/chat/completions", body: `nope`, status: 400, code: "INVALID_REQUEST"},
		{name: "body too large", method: "POST", path: "/ap/ide/v1/chat/completions", body: `{"model":"chat-model","x":"` + strings.Repeat("a", 8<<20) + `"}`, status: 413, code: "REQUEST_TOO_LARGE"},
		{name: "model not found", method: "POST", path: "/ap/ide/v1/chat/completions", body: `{"model":"nope"}`, status: 404, code: "MODEL_NOT_FOUND"},
		{name: "wrong wire", method: "POST", path: "/ap/ide/v1/messages", body: `{"model":"chat-model"}`, status: 400, code: "PROTOCOL_MISMATCH"},
		{name: "missing grants", method: "POST", path: "/ap/ide/v1/chat/completions", body: `{"model":"chat-model","messages":[]}`, prepare: func(f *apFixture) { f.as(apUser("alice")) }, status: 403, code: "ACTION_DENIED"},
		{name: "quota not configured", method: "POST", path: "/ap/ide/v1/chat/completions", body: `{"model":"chat-model"}`, prepare: func(f *apFixture) {
			setQoS(f, &AccessPointQoS{Class: "interactive", Limits: &AccessPointLimit{Concurrency: 2}})
		}, status: 503, code: "QUOTA_UNAVAILABLE"},
		{name: "quota control missing for the access point", method: "POST", path: "/ap/ide/v1/chat/completions", body: `{"model":"chat-model","messages":[{"role":"user","content":"x"}]}`, prepare: func(f *apFixture) {
			setQoS(f, &AccessPointQoS{Class: "interactive", Limits: &AccessPointLimit{Concurrency: 2}})
			withAPQuota(t, f, []quotaplex.ConcurrencyRule{{ID: "tenant", Scope: quotaplex.Scope{Kind: "tenant"}, Limit: 5}})
		}, status: 503, code: "QUOTA_COVERAGE_UNAVAILABLE"},
		// MCP.
		{name: "mcp: GET", method: "GET", path: "/ap/ide/mcp", status: 405, code: "METHOD_NOT_ALLOWED"},
		{name: "mcp: origin", method: "POST", path: "/ap/ide/mcp", body: rpc("ping", ""), headers: append([]string{"Origin", "https://evil.example"}, mcp...), status: 403, code: "ORIGIN_NOT_ALLOWED"},
		{name: "mcp: not authenticated", method: "POST", path: "/ap/ide/mcp", body: rpc("ping", ""), headers: mcp, prepare: func(f *apFixture) { f.as(nil) }, status: 401, code: "UNAUTHENTICATED"},
		{name: "mcp: content type", method: "POST", path: "/ap/ide/mcp", body: rpc("ping", ""), headers: append([]string{"Content-Type", "text/plain"}, mcp...), status: 415, code: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "mcp: invalid JSON", method: "POST", path: "/ap/ide/mcp", body: `{`, headers: mcp, status: 400, code: "INVALID_REQUEST"},
		{name: "mcp: batch", method: "POST", path: "/ap/ide/mcp", body: `[]`, headers: mcp, status: 400, code: "INVALID_REQUEST"},
		{name: "mcp: protocol version", method: "POST", path: "/ap/ide/mcp", body: rpc("ping", ""), headers: []string{"MCP-Protocol-Version", "1999-01-01"}, status: 400, code: "UNSUPPORTED_PROTOCOL_VERSION"},
		{name: "mcp: connection not exposed", method: "POST", path: "/ap/ide/mcp/nothere", body: rpc("ping", ""), headers: mcp, status: 404, code: "NOT_FOUND"},
		{name: "mcp: method not found", method: "POST", path: "/ap/ide/mcp", body: rpc("resources/list", ""), headers: mcp, status: 200, code: "METHOD_NOT_FOUND"},
		{name: "mcp: invalid params", method: "POST", path: "/ap/ide/mcp", body: rpc("tools/call", `,"params":{"name":"github__"}`), headers: mcp, status: 200, code: "INVALID_PARAMS"},
		{name: "mcp: unknown tool", method: "POST", path: "/ap/ide/mcp", body: rpc("tools/call", `,"params":{"name":"github__nothing"}`), headers: mcp, status: 200, code: "INVALID_REQUEST"},
		{name: "mcp: tool grant missing", method: "POST", path: "/ap/ide/mcp", body: rpc("tools/call", `,"params":{"name":"github__delete"}`), headers: mcp, prepare: func(f *apFixture) { f.as(apUser("alice")) }, status: 200, code: "SCOPE"},
		{name: "mcp: tool upstream answer is invalid", method: "POST", path: "/ap/ide/mcp", body: rpc("tools/call", `,"params":{"name":"broken__search"}`), headers: mcp, status: 200, code: "UPSTREAM_RESPONSE_INVALID"},
		{name: "mcp: quota not configured", method: "POST", path: "/ap/ide/mcp", body: rpc("tools/call", `,"params":{"name":"github__search"}`), headers: mcp, prepare: func(f *apFixture) {
			setQoS(f, &AccessPointQoS{Class: "interactive", Limits: &AccessPointLimit{Concurrency: 2}})
		}, status: 200, code: "QUOTA_UNAVAILABLE"},
		// Discovery.
		{name: "servers: not authenticated", method: "GET", path: "/ap/ide/v0.1/servers", prepare: func(f *apFixture) { f.as(nil) }, status: 401, code: "UNAUTHENTICATED"},
		{name: "skills: not authenticated", method: "GET", path: "/ap/ide/skills", prepare: func(f *apFixture) { f.as(nil) }, status: 401, code: "UNAUTHENTICATED"},
		{name: "protected resource: invalid id", method: "GET", path: "/ap/bad$id/.well-known/oauth-protected-resource", status: 404, code: "NOT_FOUND"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newAPFixture(t)
			if c.prepare != nil {
				c.prepare(f)
			}
			w := f.serve(c.method, c.path, c.body, c.headers...)
			if w.Code != c.status {
				t.Fatalf("status %d, want %d: %.300s", w.Code, c.status, w.Body)
			}
			if got := w.Header().Get(errorcode.Header); got != c.code {
				t.Fatalf("%s = %q, want %q: %.300s", errorcode.Header, got, c.code, w.Body)
			}
			if !errorcode.Valid(w.Header().Get(errorcode.Header)) {
				t.Fatalf("malformed code %q", w.Header().Get(errorcode.Header))
			}
			if w.Header().Get("X-Aiplex-Request-Id") == "" || w.Header().Get("X-Aiplex-Request-Id") != w.Header().Get("X-Request-Id") {
				t.Fatalf("request id headers: %v", w.Header())
			}
		})
	}
}

func TestAccessPointAnswersThatAreNotRefusalsCarryNoCode(t *testing.T) {
	f := newAPFixture(t)
	for _, c := range []struct {
		name, method, path, body string
		headers                  []string
		status                   int
	}{
		{"model success", "POST", "/ap/ide/v1/chat/completions", `{"model":"chat-model","messages":[{"role":"user","content":"x"}]}`, nil, 200},
		{"model list", "GET", "/ap/ide/v1/models", "", nil, 200},
		{"provider's own error is relayed", "POST", "/ap/ide/v1beta/models/provider-error:generateContent", `{"contents":[]}`, nil, 400},
		{"mcp initialize", "POST", "/ap/ide/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`, []string{"Accept", "application/json", "MCP-Protocol-Version", "2025-06-18"}, 200},
		{"mcp tool call", "POST", "/ap/ide/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"github__search"}}`, []string{"Accept", "application/json", "MCP-Protocol-Version", "2025-06-18"}, 200},
		{"servers", "GET", "/ap/ide/v0.1/servers", "", nil, 200},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := f.serve(c.method, c.path, c.body, c.headers...)
			if w.Code != c.status {
				t.Fatalf("status %d: %.300s", w.Code, w.Body)
			}
			if got := w.Header().Get(errorcode.Header); got != "" {
				t.Fatalf("%s = %q on an answer that is not an AIPlex refusal", errorcode.Header, got)
			}
			if w.Header().Get("X-Aiplex-Request-Id") == "" {
				t.Fatal("no X-Aiplex-Request-Id")
			}
		})
	}
}

func TestMiddlewareRefusalsUnderAccessPointsCarryCodes(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	serve := func(h http.Handler, path string, mutate func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, nil)
		if mutate != nil {
			mutate(r)
		}
		w := httptest.NewRecorder()
		RequestID(h).ServeHTTP(w, r)
		return w
	}
	t.Run("rate limit", func(t *testing.T) {
		h := RateLimit(1)(ok)
		serve(h, "/ap/ide/v1/chat/completions", nil)
		w := serve(h, "/ap/ide/v1/chat/completions", nil)
		if w.Code != 429 || w.Header().Get(errorcode.Header) != "RATE_LIMITED" || w.Header().Get("X-Aiplex-Request-Id") == "" {
			t.Fatalf("%d %v", w.Code, w.Header())
		}
		// The same limiter on a control API route adds nothing.
		other := RateLimit(1)(ok)
		serve(other, "/api/v1/usage/records", nil)
		if w = serve(other, "/api/v1/usage/records", nil); w.Code != 429 || w.Header().Get(errorcode.Header) != "" || w.Header().Get("X-Aiplex-Request-Id") != "" {
			t.Fatalf("control API response carries access point headers: %v", w.Header())
		}
	})
	t.Run("provider key conflict", func(t *testing.T) {
		w := serve(NativeKeyHeaders(ok), "/ap/ide/v1/messages", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer one")
			r.Header.Set("X-Api-Key", "two")
		})
		if w.Code != 401 || w.Header().Get(errorcode.Header) != "UNAUTHENTICATED" {
			t.Fatalf("%d %v", w.Code, w.Header())
		}
	})
	t.Run("credential in the URL", func(t *testing.T) {
		w := serve(NativeKeyHeaders(ok), "/ap/ide/v1/chat/completions?api_key=x", nil)
		if w.Code != 401 || w.Header().Get(errorcode.Header) != "UNAUTHENTICATED" {
			t.Fatalf("%d %v", w.Code, w.Header())
		}
	})
	t.Run("panic", func(t *testing.T) {
		boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
		r := httptest.NewRequest("POST", "/ap/ide/v1/chat/completions", nil)
		w := httptest.NewRecorder()
		Recover(RequestID(boom)).ServeHTTP(w, r)
		if w.Code != 500 || w.Header().Get(errorcode.Header) != "INTERNAL" || w.Header().Get("X-Aiplex-Request-Id") == "" {
			t.Fatalf("%d %v", w.Code, w.Header())
		}
	})
	t.Run("route policy", func(t *testing.T) {
		router := chi.NewRouter()
		router.Use(RoutePolicy(router, RoutePolicyConfig{Rules: []RouteRule{{Pattern: "/ap/{apid}/", Prefix: true, Access: RouteAuthenticated}}}))
		router.Post("/ap/{apid}/v1/chat/completions", ok)
		w := httptest.NewRecorder()
		RequestID(router).ServeHTTP(w, httptest.NewRequest("POST", "/ap/ide/v1/chat/completions", nil))
		if w.Code != 401 || w.Header().Get(errorcode.Header) != "UNAUTHENTICATED" || w.Header().Get("X-Aiplex-Request-Id") == "" {
			t.Fatalf("%d %v", w.Code, w.Header())
		}
	})
}

var _ = models.RoleAdmin

// The protected-resource route nested under /ap/{id}/ is an access point route
// and carries both headers.
func TestNestedProtectedResourceRefusalCarriesCode(t *testing.T) {
	f := newAPFixture(t)
	f.h.AccessPoint.AuthorizationServers = nil
	w := f.serve("GET", "/ap/ide/.well-known/oauth-protected-resource", "")
	if w.Code != 404 || w.Header().Get(errorcode.Header) != "NOT_CONFIGURED" || w.Header().Get("X-Aiplex-Request-Id") == "" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}

// The well-known aliases outside /ap/ carry neither header, on either refusal.
func TestWellKnownAliasesCarryNeitherHeader(t *testing.T) {
	f := newAPFixture(t)
	for name, c := range map[string]struct {
		path    string
		prepare func()
		status  int
	}{
		"bad id":         {"/.well-known/oauth-protected-resource/ap/bad$id/mcp", func() {}, 404},
		"not configured": {"/.well-known/oauth-protected-resource/ap/ide/mcp", func() { f.h.AccessPoint.AuthorizationServers = nil }, 404},
	} {
		c.prepare()
		w := f.serve("GET", c.path, "")
		if w.Code != c.status || w.Header().Get(errorcode.Header) != "" || w.Header().Get("X-Aiplex-Request-Id") != "" {
			t.Errorf("%s: %d %v", name, w.Code, w.Header())
		}
	}
}

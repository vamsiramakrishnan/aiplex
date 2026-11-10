package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

const nativeKeyToken = "aiplex-caller-secret-token-7f3a"

type nativeKeyVerifier struct{}

func (nativeKeyVerifier) Verify(_ context.Context, token string) (map[string]any, error) {
	if token != nativeKeyToken {
		return nil, context.Canceled
	}
	return map[string]any{"iss": apIssuer, "sub": "alice", "groups": []any{"platform-eng"}, "iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix()}, nil
}

type nativeKeyUpstream struct {
	mu   sync.Mutex
	seen []string // every header name and value plus the URL, per request
}

func (u *nativeKeyUpstream) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		b.WriteString(r.URL.String())
		for name, values := range r.Header {
			b.WriteString("\n" + name + ": " + strings.Join(values, ","))
		}
		u.mu.Lock()
		u.seen = append(u.seen, b.String())
		u.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

// nativeKeyChain serves the access-point and client-gateway routes behind the
// production-order middleware (request id, logger, native key adapter, WIF).
func nativeKeyChain(t *testing.T, f *apFixture) (http.Handler, *nativeKeyUpstream, *bytes.Buffer) {
	t.Helper()
	store := registry.NewMemoryStore()
	if err := store.PutRoleBinding(context.Background(), &models.RoleBinding{ID: "eng", Group: "platform-eng", Role: models.RoleAdmin, Scopes: apAllScopes}); err != nil {
		t.Fatal(err)
	}
	validator := auth.NewWIFValidator(store, auth.WIFConfig{Verifier: nativeKeyVerifier{}, TrustedIssuers: []string{apIssuer}, WorkforcePoolID: "people", IsWorkforce: true})
	f.h.ResolveAccess = validator.SyncUserScopes
	f.as(nil)
	upstream := &nativeKeyUpstream{}
	f.upstream.Config.Handler = upstream.wrap(f.upstream.Config.Handler)

	logs := &bytes.Buffer{}
	previous := log.Logger
	log.Logger = zerolog.New(logs)
	t.Cleanup(func() { log.Logger = previous })

	outer := chi.NewRouter()
	outer.Use(RequestID, Logger, NativeKeyHeaders, WIFAuthMode(validator, false), AuditLog)
	outer.Handle("/ap/*", f.router)
	outer.Route("/api/v1", func(r chi.Router) {
		r.Use(RequireIdentity(false))
		r.Handle("/llm/gateway/{id}/*", http.HandlerFunc(f.h.ClientGateway))
		r.Get("/instances", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	})
	return outer, upstream, logs
}

func nativeKeyDo(h http.Handler, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const (
	nativeAnthropicBody = `{"model":"claude","max_tokens":5,"messages":[{"role":"user","content":"hi"}]}`
	nativeGenAIBody     = `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
)

func TestNativeKeyHeaderAuthenticatesPerWireAndRecordsIdentity(t *testing.T) {
	cases := []struct {
		name, path, body, header string
		gateway                  bool
	}{
		{"access point anthropic x-api-key", "/ap/ide/v1/messages", nativeAnthropicBody, "X-Api-Key", false},
		{"access point genai x-goog-api-key", "/ap/ide/v1beta/models/gemini-pro:generateContent", nativeGenAIBody, "X-Goog-Api-Key", false},
		{"gateway anthropic x-api-key", "/api/v1/llm/gateway/claude/v1/messages", `{"model":"claude-sonnet-4-5","max_tokens":5,"messages":[{"role":"user","content":"hi"}]}`, "X-Api-Key", true},
		{"gateway genai x-goog-api-key", "/api/v1/llm/gateway/genai/v1beta/models/gemini-2.5-pro:generateContent", nativeGenAIBody, "X-Goog-Api-Key", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newAPFixture(t)
			records := attachLedger(t, f.h)
			chain, upstream, logs := nativeKeyChain(t, f)
			w := nativeKeyDo(chain, "POST", c.path, c.body, "Content-Type", "application/json", c.header, nativeKeyToken)
			if w.Code != 200 {
				t.Fatalf("native-header request failed: %d %s", w.Code, w.Body.String())
			}
			if calls := f.upstreamCalls(); len(calls) != 1 || calls[0].Authorization != "Bearer operator-upstream-token" {
				t.Fatalf("upstream call: %+v", calls)
			}
			for _, seen := range upstream.seen {
				if strings.Contains(seen, nativeKeyToken) {
					t.Fatalf("caller token reached the upstream: %s", seen)
				}
			}
			if strings.Contains(logs.String(), nativeKeyToken) || strings.Contains(w.Body.String(), nativeKeyToken) {
				t.Fatal("caller token was logged or echoed")
			}
			recs := records()
			if len(recs) != 1 {
				t.Fatalf("ledger records: %+v", recs)
			}
			raw, _ := json.Marshal(recs[0])
			if strings.Contains(string(raw), nativeKeyToken) || recs[0].PrincipalID != auth.CanonicalPrincipalID(apIssuer, "people", "alice") {
				t.Fatalf("ledger identity wrong or token recorded: %s", raw)
			}
			if !c.gateway && recs[0].AccessPointID != "ide" {
				t.Fatalf("access point not recorded: %s", raw)
			}
			// The same request with Bearer yields the same identity: no new credential type.
			w = nativeKeyDo(chain, "POST", c.path, c.body, "Content-Type", "application/json", "Authorization", "Bearer "+nativeKeyToken)
			if w.Code != 200 {
				t.Fatalf("bearer baseline failed: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestNativeKeyHeaderFailuresMatchBearerFailures(t *testing.T) {
	f := newAPFixture(t)
	chain, _, logs := nativeKeyChain(t, f)
	path := "/ap/ide/v1/messages"
	bad := nativeKeyDo(chain, "POST", path, nativeAnthropicBody, "X-Api-Key", "not-a-valid-token-xyz")
	bearer := nativeKeyDo(chain, "POST", path, nativeAnthropicBody, "Authorization", "Bearer not-a-valid-token-xyz")
	none := nativeKeyDo(chain, "POST", path, nativeAnthropicBody)
	if bad.Code != 401 || bad.Code != bearer.Code || bad.Body.String() != bearer.Body.String() || none.Code != 401 {
		t.Fatalf("invalid native token must fail like an invalid bearer: %d/%d %q vs %q", bad.Code, bearer.Code, bad.Body.String(), bearer.Body.String())
	}
	if strings.Contains(bad.Body.String(), "not-a-valid-token-xyz") || strings.Contains(logs.String(), "not-a-valid-token-xyz") {
		t.Fatal("rejected token appeared in a response or log")
	}
	// Wrong header for the wire is ignored, never treated as a credential.
	if w := nativeKeyDo(chain, "POST", path, nativeAnthropicBody, "X-Goog-Api-Key", nativeKeyToken); w.Code != 401 {
		t.Fatalf("x-goog-api-key accepted on an Anthropic path: %d", w.Code)
	}
	if w := nativeKeyDo(chain, "POST", "/ap/ide/v1beta/models/gemini-pro:generateContent", nativeGenAIBody, "X-Api-Key", nativeKeyToken); w.Code != 401 {
		t.Fatalf("x-api-key accepted on a GenAI path: %d", w.Code)
	}
	if w := nativeKeyDo(chain, "POST", "/ap/ide/v1/chat/completions", `{"model":"chat-model"}`, "X-Api-Key", nativeKeyToken); w.Code != 401 {
		t.Fatalf("native header accepted on an OpenAI path: %d", w.Code)
	}
}

func TestNativeKeyHeaderConflictsAreRejected(t *testing.T) {
	f := newAPFixture(t)
	chain, _, logs := nativeKeyChain(t, f)
	path := "/ap/ide/v1/messages"
	// Differing values: rejected even though the bearer one is valid.
	w := nativeKeyDo(chain, "POST", path, nativeAnthropicBody, "Authorization", "Bearer "+nativeKeyToken, "X-Api-Key", "other-token-value")
	if w.Code != 401 || !strings.Contains(w.Body.String(), `"type":"error"`) {
		t.Fatalf("conflicting headers: %d %s", w.Code, w.Body.String())
	}
	// Differing, the valid one in the native header.
	if w = nativeKeyDo(chain, "POST", path, nativeAnthropicBody, "Authorization", "Bearer other-token-value", "X-Api-Key", nativeKeyToken); w.Code != 401 {
		t.Fatalf("conflicting headers (native valid): %d", w.Code)
	}
	// Non-bearer Authorization with a native key.
	if w = nativeKeyDo(chain, "POST", path, nativeAnthropicBody, "Authorization", "Basic abc", "X-Api-Key", nativeKeyToken); w.Code != 401 {
		t.Fatalf("basic plus native key: %d", w.Code)
	}
	// Equal values are accepted.
	if w = nativeKeyDo(chain, "POST", path, nativeAnthropicBody, "Authorization", "Bearer "+nativeKeyToken, "X-Api-Key", nativeKeyToken); w.Code != 200 {
		t.Fatalf("equal headers rejected: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(logs.String(), nativeKeyToken) || strings.Contains(logs.String(), "other-token-value") {
		t.Fatal("conflict path logged a credential")
	}
}

func TestNativeKeyQueryParameterIsRejectedAndNeverLogged(t *testing.T) {
	f := newAPFixture(t)
	chain, upstream, logs := nativeKeyChain(t, f)
	for _, path := range []string{
		"/ap/ide/v1beta/models/gemini-pro:generateContent?key=" + nativeKeyToken,
		"/ap/ide/v1beta/models/gemini-pro:streamGenerateContent?alt=sse&KEY=" + nativeKeyToken,
		"/api/v1/llm/gateway/genai/v1beta/models/gemini-2.5-pro:generateContent?api_key=" + nativeKeyToken,
		"/ap/ide/v1/messages?access_token=" + nativeKeyToken,
	} {
		w := nativeKeyDo(chain, "POST", path, nativeGenAIBody, "Content-Type", "application/json")
		if w.Code != 401 || !strings.Contains(w.Body.String(), "query") || strings.Contains(w.Body.String(), nativeKeyToken) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if strings.Contains(path, "v1beta") && !strings.Contains(w.Body.String(), `"status":"UNAUTHENTICATED"`) {
			t.Fatalf("%s: refusal is not Google-shaped: %s", path, w.Body.String())
		}
		// A valid header alongside a query token is still refused.
		w = nativeKeyDo(chain, "POST", path, nativeGenAIBody, "Authorization", "Bearer "+nativeKeyToken)
		if w.Code != 401 {
			t.Fatalf("%s: query token tolerated next to a header: %d", path, w.Code)
		}
	}
	if len(upstream.seen) != 0 || len(f.upstreamCalls()) != 0 {
		t.Fatal("a query-token request reached the upstream")
	}
	if strings.Contains(logs.String(), nativeKeyToken) {
		t.Fatalf("query token appeared in captured logs: %s", logs.String())
	}
}

// Control-plane and MCP routes keep requiring Authorization: the adapter does
// not touch them, so a provider key header authenticates nothing there.
func TestNativeKeyHeaderIsIgnoredOffTheModelRoutes(t *testing.T) {
	f := newAPFixture(t)
	chain, _, _ := nativeKeyChain(t, f)
	for _, header := range []string{"X-Api-Key", "X-Goog-Api-Key", "Api-Key"} {
		if w := nativeKeyDo(chain, "GET", "/api/v1/instances", "", header, nativeKeyToken); w.Code != 401 {
			t.Fatalf("control plane accepted %s: %d", header, w.Code)
		}
		w := mcpPost(&apFixture{t: t, router: chain}, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, header, nativeKeyToken)
		if w.Code == 200 {
			t.Fatalf("MCP accepted %s: %s", header, w.Body.String())
		}
	}
	if w := nativeKeyDo(chain, "GET", "/api/v1/instances", "", "Authorization", "Bearer "+nativeKeyToken); w.Code != 200 {
		t.Fatalf("control plane bearer baseline: %d", w.Code)
	}
}

// The adapter's own contract, on every wire including those without a full
// routed fixture (Azure paths, Gemini Live).
func TestNativeKeyHeaderNormalisation(t *testing.T) {
	var gotAuth string
	var gotHeaders http.Header
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotHeaders = r.Header.Get("Authorization"), r.Header.Clone()
		w.WriteHeader(204)
	})
	h := NativeKeyHeaders(next)
	cases := []struct {
		name, path, header string
		accepted           bool
	}{
		{"anthropic messages", "/ap/x/v1/messages", "X-Api-Key", true},
		{"anthropic count tokens", "/ap/x/v1/messages/count_tokens", "X-Api-Key", true},
		{"gateway anthropic", "/api/v1/llm/gateway/s/v1/messages", "X-Api-Key", true},
		{"genai", "/ap/x/v1beta/models/m:generateContent", "X-Goog-Api-Key", true},
		{"genai list", "/ap/x/v1beta/models", "X-Goog-Api-Key", true},
		{"gemini live websocket", "/ap/x" + geminiLivePath, "X-Goog-Api-Key", true},
		{"gateway genai vertex path", "/api/v1/llm/gateway/s/v1/projects/p/locations/l/publishers/google/models/m:streamGenerateContent", "X-Goog-Api-Key", true},
		{"gateway azure v1", "/api/v1/llm/gateway/s/openai/v1/chat/completions", "Api-Key", true},
		{"gateway azure deployment", "/api/v1/llm/gateway/s/openai/deployments/d/chat/completions", "Api-Key", true},
		{"access point openai chat accepts the Azure api-key header", "/ap/x/v1/chat/completions", "Api-Key", true},
		{"access point azure v1", "/ap/x/openai/v1/chat/completions", "Api-Key", true},
		{"access point azure deployment", "/ap/x/openai/deployments/d/chat/completions", "Api-Key", true},
		{"access point openai chat rejects the anthropic header", "/ap/x/v1/chat/completions", "X-Api-Key", false},
		{"openai realtime", "/ap/x/v1/realtime", "X-Api-Key", false},
		{"bedrock", "/ap/x/model/m/converse", "X-Api-Key", false},
		{"mcp", "/ap/x/mcp", "X-Api-Key", false},
		{"skills", "/ap/x/skills", "X-Api-Key", false},
		{"control plane", "/api/v1/instances", "X-Api-Key", false},
		{"native llm route", "/api/v1/llm/native/s/v1/messages", "X-Api-Key", false},
		{"azure header on anthropic path", "/ap/x/v1/messages", "Api-Key", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotAuth, gotHeaders = "", nil
			r := httptest.NewRequest("POST", c.path, nil)
			r.Header.Set(c.header, nativeKeyToken)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if c.accepted {
				if gotAuth != "Bearer "+nativeKeyToken || gotHeaders.Get(c.header) != "" || r.Header.Get(c.header) == "" {
					t.Fatalf("not normalised (auth=%q, header kept=%q, original mutated=%v)", gotAuth, gotHeaders.Get(c.header), r.Header.Get(c.header) == "")
				}
				return
			}
			if strings.HasPrefix(c.path, "/ap/x/v1") || strings.HasPrefix(c.path, "/ap/x/model") {
				// Model routes strip but never honour a header that is not theirs.
				if gotAuth != "" || gotHeaders.Get(c.header) != "" {
					t.Fatalf("foreign header honoured or forwarded: auth=%q", gotAuth)
				}
				return
			}
			if gotAuth != "" || gotHeaders.Get(c.header) != nativeKeyToken {
				t.Fatalf("non-model route modified: auth=%q header=%q", gotAuth, gotHeaders.Get(c.header))
			}
		})
	}
	t.Run("websocket subprotocol token is not read", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/ap/x/v1/realtime?model=m", nil)
		r.Header.Set("Sec-WebSocket-Protocol", "realtime, openai-insecure-api-key."+nativeKeyToken)
		gotAuth = ""
		h.ServeHTTP(httptest.NewRecorder(), r)
		if gotAuth != "" {
			t.Fatalf("subprotocol was promoted to a credential: %q", gotAuth)
		}
	})
	t.Run("IAP assertion plus native key is ambiguous", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/ap/x/v1/messages", nil)
		r.Header.Set("X-Goog-Iap-Jwt-Assertion", "iap")
		r.Header.Set("X-Api-Key", nativeKeyToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("ambiguous credentials accepted: %d", w.Code)
		}
	})
}

// The generic connect snippet shows the ordinary SDK configuration that the
// tests above prove: an API-key variable carrying the AIPlex token.
func TestConnectSnippetShowsSDKKeyVariables(t *testing.T) {
	c := OverviewConnect{BaseURLs: map[string]string{"anthropic": "https://gw/ap/x", "genai": "https://gw/ap/x"}}
	var content string
	for _, s := range overviewGenericSnippets(c, map[string]string{}) {
		content += s.Content
	}
	for _, want := range []string{`ANTHROPIC_API_KEY="$` + overviewTokenEnv, `GEMINI_API_KEY="$` + overviewTokenEnv, `ANTHROPIC_AUTH_TOKEN="$` + overviewTokenEnv} {
		if !strings.Contains(content, want) {
			t.Fatalf("snippet lacks %s:\n%s", want, content)
		}
	}
}

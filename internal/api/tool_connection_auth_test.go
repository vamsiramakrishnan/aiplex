package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	stdlog "log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
)

const (
	apiSecret       = "api-secret-6b1e0f"
	apiClientSecret = "client-secret-29cd"
	idTokenValue    = "google-id-token-aa11"
)

type apiSecrets struct{ missing bool }

func (s apiSecrets) Resolve(_ context.Context, tenant, ref string) (string, error) {
	if tenant != "tenant" {
		return "", fmt.Errorf("namespace")
	}
	switch ref {
	case "tool-key":
		return apiSecret, nil
	case "client":
		return apiClientSecret, nil
	}
	return "", fmt.Errorf("missing")
}
func (s apiSecrets) Exists(_ context.Context, _ string, ref string) (bool, error) {
	return !s.missing && (ref == "tool-key" || ref == "client"), nil
}
func (apiSecrets) Namespace(tenant string) string { return "ns-" + tenant + "-" }

type apiIDTokens struct{ calls atomic.Int32 }

func (f *apiIDTokens) IDToken(_ context.Context, tenant, ref, audience string) (string, error) {
	f.calls.Add(1)
	if tenant != "tenant" || ref != "workload" || audience != "https://tool.run.app" {
		return "", fmt.Errorf("unexpected")
	}
	return idTokenValue, nil
}

// toolUpstream serves /mcp (tools) and /token (OAuth) on one origin.
type toolUpstream struct {
	*httptest.Server
	mu           sync.Mutex
	toolCalls    int
	tokenCalls   int
	headers      http.Header
	query        url.Values
	forms        []url.Values
	unauthorized int
	list         string
}

func newToolUpstream(t *testing.T) *toolUpstream {
	u := &toolUpstream{list: `[{"name":"search","annotations":{"readOnlyHint":true}},{"name":"delete","annotations":{"readOnlyHint":false}},{"name":"files.read"}]`}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		if r.URL.Path == "/token" {
			u.tokenCalls++
			_ = r.ParseForm()
			u.forms = append(u.forms, r.PostForm)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("service-token-%d", u.tokenCalls), "token_type": "Bearer", "expires_in": 600})
			return
		}
		u.toolCalls++
		u.headers, u.query = r.Header.Clone(), r.URL.Query()
		if u.unauthorized > 0 {
			u.unauthorized--
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			w.WriteHeader(401)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &request)
		w.Header().Set("Content-Type", "application/json")
		if request.Method == "tools/list" {
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":%s}}`, request.ID, u.list)
			return
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"ok"}]}}`, request.ID)
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *toolUpstream) counts() (int, int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.toolCalls, u.tokenCalls
}

func authHandler(t *testing.T, store resources.Store, origin string, secrets toolcredential.SecretResolver) (*ConnectedHandler, *apiIDTokens) {
	t.Helper()
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{origin}, AllowLoopback: true}, Identities: []connections.Identity{
		{ID: "host", TenantID: "tenant", Type: "bearer-env", TokenEnv: "COHESION_HOST_CREDENTIAL", AllowedOrigins: []string{origin}},
		{ID: "workload", TenantID: "tenant", Type: "gcp-adc", AllowedOrigins: []string{origin}},
		{ID: "elsewhere", TenantID: "tenant", Type: "gcp-adc", AllowedOrigins: []string{"https://other.example"}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := NewConnectedHandler(store, runtime, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
	ids := &apiIDTokens{}
	h.ToolCredentials, err = toolcredential.NewProvider(toolcredential.Config{HTTPClient: runtime.HTTP, Policy: runtime.Policy, Secrets: secrets, IDTokens: ids})
	if err != nil {
		t.Fatal(err)
	}
	return h, ids
}

func authConnection(endpoint string, auth *toolcredential.Auth, tools *toolexec.ToolPolicy) ToolConnection {
	return ToolConnection{ID: "surface", ResourceURL: endpoint, Auth: auth, Tools: tools, Execution: &toolexec.Connection{ID: "surface", Endpoint: endpoint, CredentialMode: toolexec.ConnectionAuth, AllowedTools: []string{"search", "delete", "files.read"}, ProtocolVersion: "2025-03-26", Mode: toolexec.StatelessJSON}}
}

func toolCall(name string, scopes []string) *http.Request {
	body := `{"jsonrpc":"2.0","id":"rpc-` + name + `","method":"tools/call","params":{"name":"` + name + `","arguments":{}}}`
	r := trustedConnectedRequest("/api/v1/mcp/connections/surface/invoke", body, scopes)
	r.Header.Set("Content-Type", "application/json")
	return r
}

func toolList(scopes []string) *http.Request {
	r := trustedConnectedRequest("/api/v1/mcp/connections/surface/invoke", `{"jsonrpc":"2.0","id":9,"method":"tools/list"}`, scopes)
	r.Header.Set("Content-Type", "application/json")
	return r
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buffer := &bytes.Buffer{}
	previous, level := log.Logger, zerolog.GlobalLevel()
	log.Logger = zerolog.New(buffer)
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	stdlog.SetOutput(buffer)
	t.Cleanup(func() {
		log.Logger = previous
		zerolog.SetGlobalLevel(level)
		stdlog.SetOutput(io.Discard)
	})
	return buffer
}

func TestToolConnectionAuthValidation(t *testing.T) {
	const origin = "https://tools.example"
	endpoint := origin + "/mcp"
	store := resources.NewMemoryStore()
	h, _ := authHandler(t, store, origin, apiSecrets{})
	valid := map[string]*toolcredential.Auth{
		"none":           {Type: toolcredential.None},
		"api_key header": {Type: toolcredential.APIKey, In: "header", Name: "X-Api-Key", SecretRef: "tool-key"},
		"api_key query":  {Type: toolcredential.APIKey, In: "query", Name: "key", SecretRef: "tool-key"},
		"bearer":         {Type: toolcredential.Bearer, SecretRef: "tool-key"},
		"basic":          {Type: toolcredential.Basic, Username: "svc", SecretRef: "tool-key"},
		"client creds":   {Type: toolcredential.OAuthClientCredentials, TokenEndpoint: origin + "/token", ClientID: "svc", ClientSecretRef: "client", ClientAuthMethod: "client_secret_basic"},
		"token exchange": {Type: toolcredential.TokenExchange, TokenEndpoint: origin + "/token", Audience: "tools"},
		"google":         {Type: toolcredential.GoogleIDToken, Audience: "https://tool.run.app", IdentityRef: "workload"},
	}
	for name, auth := range valid {
		raw, _ := json.Marshal(authConnection(endpoint, auth, &toolexec.ToolPolicy{Allow: []string{"search"}, ReadOnly: true}))
		if err := h.validate(context.Background(), "tenant", "tool-connection", "surface", raw); err != nil {
			t.Errorf("%s: valid connection rejected: %v", name, err)
		}
	}
	legacy, _ := json.Marshal(managedToolConnection(endpoint))
	if strings.Contains(string(legacy), `"auth"`) || strings.Contains(string(legacy), `"tools"`) {
		t.Fatal("connection without auth/tools serializes new members; legacy digests would change")
	}
	mutate := func(change func(*ToolConnection)) []byte {
		c := authConnection(endpoint, &toolcredential.Auth{Type: toolcredential.Bearer, SecretRef: "tool-key"}, nil)
		change(&c)
		raw, _ := json.Marshal(c)
		return raw
	}
	invalid := map[string][]byte{
		"connection-auth without auth": mutate(func(c *ToolConnection) { c.Auth = nil }),
		"bearer on operator mode": mutate(func(c *ToolConnection) {
			c.Execution.CredentialMode, c.Execution.IdentityRef = toolexec.OperatorBearer, "host"
		}),
		"oauth_user on connection-auth": mutate(func(c *ToolConnection) {
			c.Auth = &toolcredential.Auth{Type: toolcredential.OAuthUser}
		}),
		"auth without execution": mutate(func(c *ToolConnection) { c.Execution = nil }),
		"issuer with bearer":     mutate(func(c *ToolConnection) { c.Issuer = "https://tools.example" }),
		"unapproved token endpoint": mutate(func(c *ToolConnection) {
			c.Auth = &toolcredential.Auth{Type: toolcredential.OAuthClientCredentials, TokenEndpoint: "https://login.unapproved.example/token", ClientID: "svc", ClientSecretRef: "client", ClientAuthMethod: "client_secret_post"}
		}),
		"google with bearer-env profile": mutate(func(c *ToolConnection) {
			c.Auth = &toolcredential.Auth{Type: toolcredential.GoogleIDToken, Audience: "https://tool.run.app", IdentityRef: "host"}
		}),
		"google profile without origin": mutate(func(c *ToolConnection) {
			c.Auth = &toolcredential.Auth{Type: toolcredential.GoogleIDToken, Audience: "https://tool.run.app", IdentityRef: "elsewhere"}
		}),
		"secret does not exist": mutate(func(c *ToolConnection) { c.Auth.SecretRef = "not-provisioned" }),
		"denied header": mutate(func(c *ToolConnection) {
			c.Auth = &toolcredential.Auth{Type: toolcredential.APIKey, In: "header", Name: "Host", SecretRef: "tool-key"}
		}),
		"tools without execution": mutate(func(c *ToolConnection) {
			c.Auth, c.Execution, c.Tools = nil, nil, &toolexec.ToolPolicy{Deny: []string{"x"}}
		}),
		"bad tools pattern":   mutate(func(c *ToolConnection) { c.Tools = &toolexec.ToolPolicy{Allow: []string{"a*b"}} }),
		"unknown auth field":  []byte(strings.Replace(string(mutate(func(*ToolConnection) {})), `"type":"bearer"`, `"type":"bearer","value":"`+apiSecret+`"`, 1)),
		"unknown tools field": []byte(strings.Replace(string(mutate(func(c *ToolConnection) { c.Tools = &toolexec.ToolPolicy{ReadOnly: true} })), `"read_only":true`, `"read_only":true,"admin":true`, 1)),
	}
	for name, raw := range invalid {
		if err := h.validate(context.Background(), "tenant", "tool-connection", "surface", raw); err == nil {
			t.Errorf("%s: invalid connection accepted", name)
		}
	}
}

func TestToolConnectionAuthDispatchSendsExactCredentialWithoutLeaking(t *testing.T) {
	upstream := newToolUpstream(t)
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("svc:"+apiSecret))
	cases := []struct {
		name  string
		auth  toolcredential.Auth
		check func(http.Header, url.Values) bool
	}{
		{"none", toolcredential.Auth{Type: "none"}, func(h http.Header, q url.Values) bool { return h.Get("Authorization") == "" && len(q) == 0 }},
		{"api_key header", toolcredential.Auth{Type: "api_key", In: "header", Name: "X-Api-Key", SecretRef: "tool-key"}, func(h http.Header, q url.Values) bool {
			return h.Get("X-Api-Key") == apiSecret && h.Get("Authorization") == "" && len(q) == 0
		}},
		{"api_key query", toolcredential.Auth{Type: "api_key", In: "query", Name: "key", SecretRef: "tool-key"}, func(h http.Header, q url.Values) bool {
			return q.Get("key") == apiSecret && h.Get("Authorization") == ""
		}},
		{"bearer", toolcredential.Auth{Type: "bearer", SecretRef: "tool-key"}, func(h http.Header, _ url.Values) bool { return h.Get("Authorization") == "Bearer "+apiSecret }},
		{"basic", toolcredential.Auth{Type: "basic", Username: "svc", SecretRef: "tool-key"}, func(h http.Header, _ url.Values) bool { return h.Get("Authorization") == basic }},
		{"client credentials", toolcredential.Auth{Type: "oauth_client_credentials", TokenEndpoint: upstream.URL + "/token", ClientID: "svc", ClientSecretRef: "client", ClientAuthMethod: "client_secret_post", Scopes: []string{"tools"}}, func(h http.Header, _ url.Values) bool {
			return strings.HasPrefix(h.Get("Authorization"), "Bearer service-token-")
		}},
		{"google id token", toolcredential.Auth{Type: "google_id_token", Audience: "https://tool.run.app", IdentityRef: "workload"}, func(h http.Header, _ url.Values) bool { return h.Get("Authorization") == "Bearer "+idTokenValue }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			logs := captureLogs(t)
			store := resources.NewMemoryStore()
			h, _ := authHandler(t, store, upstream.URL, apiSecrets{})
			auth := test.auth
			connection := authConnection(upstream.URL+"/mcp", &auth, nil)
			raw, _ := json.Marshal(connection)
			if err := h.validate(context.Background(), "tenant", "tool-connection", "surface", raw); err != nil {
				t.Fatal(err)
			}
			record := cohesionRecord(t, store, "tool-connection", "surface", connection)
			w := httptest.NewRecorder()
			Logger(http.HandlerFunc(h.NativeTool)).ServeHTTP(w, toolCall("search", []string{"mcp:tools:search"}))
			upstream.mu.Lock()
			headers, query := upstream.headers, upstream.query
			upstream.mu.Unlock()
			if w.Code != 200 || !test.check(headers, query) {
				t.Fatalf("status=%d body=%s headers=%v query=%v", w.Code, w.Body.String(), headers, query)
			}
			configuration, err := h.toolConfiguration(context.Background(), "tenant", record, connection)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, _ := json.Marshal(configuration)
			everything := w.Body.String() + fmt.Sprint(w.Header()) + logs.String() + string(raw) + string(snapshot)
			listed, _ := store.List(context.Background(), "tenant", "tool-connection")
			for _, rec := range listed {
				everything += string(rec.Spec)
			}
			for _, secret := range []string{apiSecret, apiClientSecret, idTokenValue, "service-token-"} {
				if strings.Contains(everything, secret) {
					t.Fatalf("secret material %q appeared in a response, log, snapshot or resource", secret)
				}
			}
		})
	}
}

func TestToolConnectionClientCredentialsInvalidatedOnUpstream401(t *testing.T) {
	upstream := newToolUpstream(t)
	store := resources.NewMemoryStore()
	h, _ := authHandler(t, store, upstream.URL, apiSecrets{})
	cohesionRecord(t, store, "tool-connection", "surface", authConnection(upstream.URL+"/mcp", &toolcredential.Auth{Type: "oauth_client_credentials", TokenEndpoint: upstream.URL + "/token", ClientID: "svc", ClientSecretRef: "client", ClientAuthMethod: "client_secret_basic"}, nil))
	call := func() int {
		w := httptest.NewRecorder()
		h.NativeTool(w, toolCall("search", []string{"mcp:tools:search"}))
		return w.Code
	}
	if call() != 200 || call() != 200 {
		t.Fatal("client credentials dispatch failed")
	}
	if _, tokens := upstream.counts(); tokens != 1 {
		t.Fatal("service token not cached", tokens)
	}
	upstream.mu.Lock()
	upstream.unauthorized = 1
	upstream.mu.Unlock()
	if code := call(); code != 502 {
		t.Fatal("upstream 401 not surfaced as a sanitized gateway error", code)
	}
	if call() != 200 {
		t.Fatal("dispatch after invalidation failed")
	}
	if _, tokens := upstream.counts(); tokens != 2 {
		t.Fatal("rejected service token was not invalidated", tokens)
	}
}

func TestToolConnectionDenialsPrecedeQuotaAndCredentials(t *testing.T) {
	upstream := newToolUpstream(t)
	cases := []struct {
		name   string
		tools  *toolexec.ToolPolicy
		scopes []string
		reason string
	}{
		{"deny wins", &toolexec.ToolPolicy{Allow: []string{"*"}, Deny: []string{"sea*"}}, []string{"mcp:tools:search"}, "connection_policy"},
		{"not allowed", &toolexec.ToolPolicy{Allow: []string{"files.*"}}, []string{"mcp:tools:search"}, "connection_policy"},
		{"missing scope", nil, []string{"mcp:tools:other"}, "scope"},
		{"read only unknown", &toolexec.ToolPolicy{ReadOnly: true}, []string{"mcp:tools:search"}, "read_only_unknown"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			h, quota, request := protectedToolFixture(t, upstream.URL+"/mcp")
			h.ToolCredentials, _ = toolcredential.NewProvider(toolcredential.Config{HTTPClient: h.Runtime.HTTP, Policy: h.Runtime.Policy, Secrets: apiSecrets{}})
			current, _ := h.Store.Get(context.Background(), "tenant", "tool-connection", "surface")
			connection := authConnection(upstream.URL+"/mcp", &toolcredential.Auth{Type: "oauth_client_credentials", TokenEndpoint: upstream.URL + "/token", ClientID: "svc", ClientSecretRef: "client", ClientAuthMethod: "client_secret_basic"}, test.tools)
			raw, _ := json.Marshal(connection)
			if _, err := h.Store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "tool-connection", ID: "surface", Spec: raw}, current.Revision); err != nil {
				t.Fatal(err)
			}
			scopes := test.scopes
			h.ResolveAccess = func(_ context.Context, identity *models.WIFIdentity) (*models.ResolvedAccess, error) {
				return &models.ResolvedAccess{Identity: *identity, Scopes: scopes}, nil
			}
			beforeTools, beforeTokens := upstream.counts()
			w := httptest.NewRecorder()
			h.NativeTool(w, request("denied-"+test.name))
			afterTools, afterTokens := upstream.counts()
			var response struct {
				ID    string `json:"id"`
				Error struct {
					Code int               `json:"code"`
					Data map[string]string `json:"data"`
				} `json:"error"`
			}
			if w.Code != 403 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.ID != "rpc" || response.Error.Code != toolexec.RPCForbidden || response.Error.Data["reason"] != test.reason {
				t.Fatalf("expected JSON-RPC %s denial, got %d %s", test.reason, w.Code, w.Body.String())
			}
			if w.Header().Get("X-Aiplex-Attempt-ID") != "" || afterTools != beforeTools || afterTokens != beforeTokens {
				t.Fatal("denial reserved quota, acquired a credential or reached upstream")
			}
			engine := quota.Engines()["tenant"]
			if _, err := engine.GetAttempt(context.Background(), "tenant", "denied"); err == nil {
				t.Fatal("unexpected attempt")
			}
		})
	}
	t.Run("allowed call settles quota with connection auth", func(t *testing.T) {
		h, quota, request := protectedToolFixture(t, upstream.URL+"/mcp")
		h.ToolCredentials, _ = toolcredential.NewProvider(toolcredential.Config{HTTPClient: h.Runtime.HTTP, Policy: h.Runtime.Policy, Secrets: apiSecrets{}})
		current, _ := h.Store.Get(context.Background(), "tenant", "tool-connection", "surface")
		raw, _ := json.Marshal(authConnection(upstream.URL+"/mcp", &toolcredential.Auth{Type: "bearer", SecretRef: "tool-key"}, &toolexec.ToolPolicy{Allow: []string{"search"}}))
		if _, err := h.Store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "tool-connection", ID: "surface", Spec: raw}, current.Revision); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.NativeTool(w, request("allowed"))
		info, err := quota.Engines()["tenant"].GetAttempt(context.Background(), "tenant", w.Header().Get("X-Aiplex-Attempt-ID"))
		if w.Code != 200 || err != nil || info.Decision.State != "settled" {
			t.Fatal("permitted connection-auth call did not settle", w.Code, w.Body.String(), err)
		}
	})
}

func TestToolConnectionReadOnlyUsesGatewayObservedAnnotations(t *testing.T) {
	upstream := newToolUpstream(t)
	store := resources.NewMemoryStore()
	h, _ := authHandler(t, store, upstream.URL, apiSecrets{})
	connection := authConnection(upstream.URL+"/mcp", &toolcredential.Auth{Type: "none"}, &toolexec.ToolPolicy{ReadOnly: true})
	record := cohesionRecord(t, store, "tool-connection", "surface", connection)
	scopes := []string{"mcp:tools:search", "mcp:tools:delete", "mcp:tools:files.read"}
	call := func(name string) (int, string) {
		w := httptest.NewRecorder()
		h.NativeTool(w, toolCall(name, scopes))
		return w.Code, w.Body.String()
	}
	if code, body := call("search"); code != 403 || !strings.Contains(body, "read_only_unknown") || !strings.Contains(body, "unknown") {
		t.Fatal("unknown annotation did not fail closed with an explanation", code, body)
	}
	if tools, _ := upstream.counts(); tools != 0 {
		t.Fatal("read-only check reached upstream")
	}
	w := httptest.NewRecorder()
	h.NativeTool(w, toolList(scopes))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"search"`) || strings.Contains(w.Body.String(), `"delete"`) || strings.Contains(w.Body.String(), `files.read`) {
		t.Fatal("read_only list not filtered to annotated read-only tools", w.Code, w.Body.String())
	}
	if code, body := call("search"); code != 200 {
		t.Fatal("annotated read-only tool denied", code, body)
	}
	if code, body := call("delete"); code != 403 || !strings.Contains(body, "not_read_only") {
		t.Fatal("non-read-only tool allowed", code, body)
	}
	if code, body := call("files.read"); code != 403 || !strings.Contains(body, "read_only_unknown") {
		t.Fatal("tool without annotation allowed", code, body)
	}
	// A new connection revision invalidates observed annotations.
	raw, _ := json.Marshal(connection)
	if _, err := store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "tool-connection", ID: "surface", Spec: raw}, record.Revision); err != nil {
		t.Fatal(err)
	}
	if code, _ := call("search"); code != 403 {
		t.Fatal("annotations from an older connection revision were trusted")
	}
}

func TestToolListFilteredToCallableTools(t *testing.T) {
	upstream := newToolUpstream(t)
	store := resources.NewMemoryStore()
	h, _ := authHandler(t, store, upstream.URL, apiSecrets{})
	cohesionRecord(t, store, "tool-connection", "surface", authConnection(upstream.URL+"/mcp", &toolcredential.Auth{Type: "bearer", SecretRef: "tool-key"}, &toolexec.ToolPolicy{Deny: []string{"files.*"}}))
	w := httptest.NewRecorder()
	h.NativeTool(w, toolList([]string{"mcp:tools:search", "mcp:tools:files.read"}))
	var response struct {
		ID     int `json:"id"`
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.ID != 9 || len(response.Result.Tools) != 1 || response.Result.Tools[0].Name != "search" {
		t.Fatal("tools/list not filtered by scope and connection policy", w.Code, w.Body.String())
	}
	before, _ := upstream.counts()
	w = httptest.NewRecorder()
	h.NativeTool(w, toolList([]string{"mcp:tools:files.read"}))
	after, _ := upstream.counts()
	if w.Code != 403 || !strings.Contains(w.Body.String(), `"id":9`) || after != before {
		t.Fatal("caller with no callable tools listed upstream", w.Code, w.Body.String())
	}
	// Legacy connections (no auth block) also get governed listing.
	legacyStore := resources.NewMemoryStore()
	t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
	legacy, _ := authHandler(t, legacyStore, upstream.URL, apiSecrets{})
	cohesionRecord(t, legacyStore, "tool-connection", "surface", managedToolConnection(upstream.URL+"/mcp"))
	w = httptest.NewRecorder()
	legacy.NativeTool(w, toolList([]string{"mcp:tools:search"}))
	upstream.mu.Lock()
	authorization := upstream.headers.Get("Authorization")
	upstream.mu.Unlock()
	if w.Code != 200 || authorization != "Bearer operator-tool-token" || strings.Contains(w.Body.String(), "delete") {
		t.Fatal("legacy listing changed credential or was not filtered", w.Code, authorization, w.Body.String())
	}
}

func TestToolConnectionTokenExchangeUsesVerifiedCallerToken(t *testing.T) {
	upstream := newToolUpstream(t)
	store := resources.NewMemoryStore()
	h, _ := authHandler(t, store, upstream.URL, apiSecrets{})
	cohesionRecord(t, store, "tool-connection", "surface", authConnection(upstream.URL+"/mcp", &toolcredential.Auth{Type: "token_exchange", TokenEndpoint: upstream.URL + "/token", Audience: "tools-api"}, nil))
	r := toolCall("search", []string{"mcp:tools:search"})
	r.Header.Set("Authorization", "Bearer caller-jwt")
	w := httptest.NewRecorder()
	h.NativeTool(w, r)
	upstream.mu.Lock()
	authorization, form := upstream.headers.Get("Authorization"), upstream.forms[len(upstream.forms)-1]
	upstream.mu.Unlock()
	if w.Code != 200 || form.Get("subject_token") != "caller-jwt" || form.Get("audience") != "tools-api" || !strings.HasPrefix(authorization, "Bearer service-token-") {
		t.Fatal("token exchange did not exchange the verified caller token", w.Code, form, authorization)
	}
	// Another principal never receives the first principal's exchanged token.
	other := toolCall("search", []string{"mcp:tools:search"})
	identity := models.WIFIdentity{PrincipalID: "principal-b", Subject: "b"}
	other = other.WithContext(context.WithValue(other.Context(), keyWIFIdentity, &identity))
	other.Header.Set("Authorization", "Bearer caller-jwt")
	_, tokensBefore := upstream.counts()
	w = httptest.NewRecorder()
	h.NativeTool(w, other)
	if _, tokensAfter := upstream.counts(); w.Code != 200 || tokensAfter != tokensBefore+1 {
		t.Fatal("exchanged token shared across principals", w.Code, tokensAfter-tokensBefore)
	}
	// Without the caller token the exchange fails closed.
	_, tokensBefore = upstream.counts()
	toolsBefore, _ := upstream.counts()
	w = httptest.NewRecorder()
	h.NativeTool(w, toolCall("search", []string{"mcp:tools:search"}))
	toolsAfter, tokensAfter := upstream.counts()
	if w.Code != 503 || tokensAfter != tokensBefore || toolsAfter != toolsBefore {
		t.Fatal("token exchange without caller token did not fail closed", w.Code)
	}
}

func TestToolQuotaDigestBindsAuthAndAuthorization(t *testing.T) {
	const origin = "https://tools.example"
	store := resources.NewMemoryStore()
	h, _ := authHandler(t, store, origin, apiSecrets{})
	base := authConnection(origin+"/mcp", &toolcredential.Auth{Type: "bearer", SecretRef: "tool-key"}, nil)
	record := cohesionRecord(t, store, "tool-connection", "surface", base)
	digest := func(c ToolConnection) string {
		configuration, err := h.toolConfiguration(context.Background(), "tenant", record, c)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(configuration)
		return string(raw)
	}
	original := digest(base)
	changedAuth := base
	changedAuth.Auth = &toolcredential.Auth{Type: "api_key", In: "header", Name: "X-Key", SecretRef: "tool-key"}
	changedTools := base
	changedTools.Tools = &toolexec.ToolPolicy{Deny: []string{"delete"}}
	readOnly := base
	readOnly.Tools = &toolexec.ToolPolicy{ReadOnly: true}
	google := base
	google.Auth = &toolcredential.Auth{Type: "google_id_token", Audience: "https://tool.run.app", IdentityRef: "workload"}
	seen := map[string]bool{original: true}
	for name, c := range map[string]ToolConnection{"auth": changedAuth, "tools": changedTools, "read_only": readOnly, "google": google} {
		value := digest(c)
		if seen[value] {
			t.Errorf("%s change did not change the quota configuration digest", name)
		}
		seen[value] = true
	}
	if !strings.Contains(digest(google), `"workload"`) {
		t.Fatal("google identity profile not pinned in configuration")
	}
}

func TestToolAuthTypesCatalog(t *testing.T) {
	store := resources.NewMemoryStore()
	h, _ := authHandler(t, store, "https://tools.example", apiSecrets{})
	w := httptest.NewRecorder()
	h.ToolAuthTypes(w, httptest.NewRequest(http.MethodGet, "/api/v1/mcp/auth-types", nil))
	var response struct {
		Types []struct {
			Type      string `json:"type"`
			Execution string `json:"execution_credential_mode"`
			Fields    []struct {
				Name      string `json:"name"`
				Type      string `json:"type"`
				Required  bool   `json:"required"`
				SecretRef bool   `json:"secret_reference"`
			} `json:"fields"`
		} `json:"types"`
		DefaultType string `json:"default_type"`
		Secrets     struct {
			Namespace string `json:"namespace"`
		} `json:"secret_references"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools_policy"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Types) != 8 || response.DefaultType != "oauth_user" || response.Secrets.Namespace != "ns-tenant-" || len(response.Tools) != 3 {
		t.Fatal("auth-types catalog shape", w.Code, w.Body.String())
	}
	secretFields := 0
	for _, spec := range response.Types {
		for _, field := range spec.Fields {
			if field.SecretRef {
				secretFields++
			}
		}
	}
	if secretFields != 5 {
		t.Fatal("secret reference fields not flagged", secretFields)
	}
}

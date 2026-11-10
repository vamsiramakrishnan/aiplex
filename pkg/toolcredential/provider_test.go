package toolcredential

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
)

type mapSecrets map[string]string

func (m mapSecrets) Resolve(_ context.Context, tenant, ref string) (string, error) {
	value, ok := m[tenant+"/"+ref]
	if !ok {
		return "", fmt.Errorf("missing %s", ref)
	}
	return value, nil
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

const (
	secretValue  = "sv-7f3c2a-super-secret"
	clientSecret = "cs-91ab-client-secret"
)

func newProvider(t *testing.T, origins []string, ids IDTokenSource) (*Provider, *clock) {
	t.Helper()
	policy, err := endpointpolicy.New(endpointpolicy.Config{AllowedOrigins: origins, AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
	p, err := NewProvider(Config{HTTPClient: policy.Client(), Policy: policy, Secrets: mapSecrets{"tenant/key": secretValue, "tenant/client": clientSecret, "other/key": "other-tenant-value", "other/client": "other-client-secret"}, IDTokens: ids, Clock: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	return p, c
}

type capture struct {
	mu      sync.Mutex
	headers http.Header
	query   url.Values
	calls   int
}

func captureServer(t *testing.T) (*httptest.Server, *capture) {
	t.Helper()
	got := &capture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.mu.Lock()
		got.headers, got.query = r.Header.Clone(), r.URL.Query()
		got.calls++
		got.mu.Unlock()
	}))
	t.Cleanup(server.Close)
	return server, got
}

func send(t *testing.T, credential Credential, target string) error {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, target, nil)
	if err := credential.Apply(req); err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return nil
}

func TestStaticCredentialsAreSentExactlyAndOnlyToTheResourceOrigin(t *testing.T) {
	resource, got := captureServer(t)
	other, otherGot := captureServer(t)
	p, _ := newProvider(t, []string{resource.URL, other.URL}, nil)
	target := Target{TenantID: "tenant", ConnectionID: "c", Revision: 1, Resource: resource.URL + "/mcp"}
	basic := base64.StdEncoding.EncodeToString([]byte("svc:" + secretValue))
	cases := []struct {
		name  string
		auth  Auth
		check func(*capture) bool
	}{
		{"none", Auth{Type: None}, func(c *capture) bool { return c.headers.Get("Authorization") == "" && len(c.query) == 0 }},
		{"api key header", Auth{Type: APIKey, In: InHeader, Name: "X-Api-Key", SecretRef: "key"}, func(c *capture) bool {
			return c.headers.Get("X-Api-Key") == secretValue && c.headers.Get("Authorization") == "" && len(c.query) == 0
		}},
		{"api key header prefix", Auth{Type: APIKey, In: InHeader, Name: "Authorization", Prefix: "Token", SecretRef: "key"}, func(c *capture) bool {
			return c.headers.Get("Authorization") == "Token "+secretValue
		}},
		{"api key query", Auth{Type: APIKey, In: InQuery, Name: "key", SecretRef: "key"}, func(c *capture) bool {
			return c.query.Get("key") == secretValue && c.headers.Get("Authorization") == ""
		}},
		{"bearer", Auth{Type: Bearer, SecretRef: "key"}, func(c *capture) bool { return c.headers.Get("Authorization") == "Bearer "+secretValue }},
		{"basic", Auth{Type: Basic, Username: "svc", SecretRef: "key"}, func(c *capture) bool { return c.headers.Get("Authorization") == "Basic "+basic }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			credential, err := p.Acquire(context.Background(), test.auth, target)
			if err != nil {
				t.Fatal(err)
			}
			if err = send(t, credential, resource.URL+"/mcp"); err != nil || !test.check(got) {
				t.Fatalf("unexpected upstream credential: %v headers=%v query=%v", err, got.headers, got.query)
			}
			before := otherGot.calls
			if err = send(t, credential, other.URL+"/mcp"); err == nil || otherGot.calls != before {
				t.Fatal("credential was applied to a different origin")
			}
			for _, rendered := range []string{credential.String(), fmt.Sprintf("%v %+v %#v", credential, credential, credential)} {
				if strings.Contains(rendered, secretValue) {
					t.Fatal("credential formatted its value")
				}
			}
			if raw, _ := json.Marshal(credential); strings.Contains(string(raw), secretValue) {
				t.Fatal("credential serialized its value")
			}
		})
	}
}

func TestSecretReferencesAreTenantScopedAndFailClosed(t *testing.T) {
	resource, _ := captureServer(t)
	p, _ := newProvider(t, []string{resource.URL}, nil)
	_, err := p.Acquire(context.Background(), Auth{Type: Bearer, SecretRef: "missing"}, Target{TenantID: "tenant", ConnectionID: "c", Resource: resource.URL})
	if err == nil || strings.Contains(err.Error(), "missing") {
		t.Fatal("missing secret did not fail closed with a safe error", err)
	}
	credential, err := p.Acquire(context.Background(), Auth{Type: Bearer, SecretRef: "key"}, Target{TenantID: "other", ConnectionID: "c", Resource: resource.URL})
	req, _ := http.NewRequest(http.MethodGet, resource.URL, nil)
	if err != nil || credential.Apply(req) != nil || req.Header.Get("Authorization") != "Bearer other-tenant-value" {
		t.Fatal("secret was not resolved in the caller's tenant namespace")
	}
	none, _ := NewProvider(Config{HTTPClient: http.DefaultClient, Policy: p.policy})
	if _, err = none.Acquire(context.Background(), Auth{Type: Bearer, SecretRef: "key"}, Target{TenantID: "tenant", ConnectionID: "c", Resource: resource.URL}); err == nil {
		t.Fatal("secret type succeeded without secret storage")
	}
	if _, err = p.Acquire(context.Background(), Auth{Type: None}, Target{TenantID: "tenant", ConnectionID: "c", Resource: "https://unapproved.example/mcp"}); err == nil {
		t.Fatal("credential issued for an origin outside endpoint policy")
	}
}

type tokenServer struct {
	*httptest.Server
	calls   atomic.Int32
	mu      sync.Mutex
	forms   []url.Values
	auths   []string
	status  int
	expires int64
	release chan struct{}
}

func newTokenServer(t *testing.T) *tokenServer {
	s := &tokenServer{status: 200, expires: 300}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := s.calls.Add(1)
		_ = r.ParseForm()
		s.mu.Lock()
		s.forms = append(s.forms, r.PostForm)
		s.auths = append(s.auths, r.Header.Get("Authorization"))
		status, expires, release := s.status, s.expires, s.release
		s.mu.Unlock()
		if release != nil {
			<-release
		}
		if status != 200 {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_client", secret="`+clientSecret+`"`)
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"invalid_client","echo":"`+clientSecret+`"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		subject := r.PostForm.Get("subject_token")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("issued-%d-%s", n, subject), "token_type": "Bearer", "expires_in": expires})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *tokenServer) set(status int, expires int64) {
	s.mu.Lock()
	s.status, s.expires = status, expires
	s.mu.Unlock()
}

func bearerOf(t *testing.T, credential Credential, resource string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, resource, nil)
	if err := credential.Apply(req); err != nil {
		t.Fatal(err)
	}
	return req.Header.Get("Authorization")
}

func TestClientCredentialsRequestShape(t *testing.T) {
	resource, _ := captureServer(t)
	tokens := newTokenServer(t)
	p, _ := newProvider(t, []string{resource.URL, tokens.URL}, nil)
	target := Target{TenantID: "tenant", ConnectionID: "c", Revision: 1, Resource: resource.URL + "/mcp"}
	basicAuth := Auth{Type: OAuthClientCredentials, TokenEndpoint: tokens.URL + "/token", ClientID: "svc client", ClientSecretRef: "client", ClientAuthMethod: ClientSecretBasic, Scopes: []string{"tools.read", "tools.call"}, Resource: "https://tools.example/mcp"}
	credential, err := p.Acquire(context.Background(), basicAuth, target)
	if err != nil || bearerOf(t, credential, resource.URL+"/mcp") != "Bearer issued-1-" {
		t.Fatal("client credentials token not applied", err)
	}
	form, authorization := tokens.forms[0], tokens.auths[0]
	wantBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte(url.QueryEscape("svc client")+":"+url.QueryEscape(clientSecret)))
	if form.Get("grant_type") != "client_credentials" || form.Get("scope") != "tools.read tools.call" || form.Get("resource") != "https://tools.example/mcp" || form.Has("client_secret") || authorization != wantBasic {
		t.Fatalf("client_secret_basic request shape: form=%v auth=%q", form, authorization)
	}
	postAuth := basicAuth
	postAuth.ClientAuthMethod, postAuth.Scopes, postAuth.Resource = ClientSecretPost, nil, ""
	if _, err = p.Acquire(context.Background(), postAuth, target); err != nil {
		t.Fatal(err)
	}
	form, authorization = tokens.forms[1], tokens.auths[1]
	if form.Get("client_id") != "svc client" || form.Get("client_secret") != clientSecret || authorization != "" || form.Has("scope") || form.Has("resource") {
		t.Fatalf("client_secret_post request shape: form=%v auth=%q", form, authorization)
	}
}

func TestClientCredentialsCachingExpiryAndInvalidation(t *testing.T) {
	resource, _ := captureServer(t)
	tokens := newTokenServer(t)
	p, c := newProvider(t, []string{resource.URL, tokens.URL}, nil)
	auth := Auth{Type: OAuthClientCredentials, TokenEndpoint: tokens.URL + "/token", ClientID: "svc", ClientSecretRef: "client", ClientAuthMethod: ClientSecretPost}
	target := Target{TenantID: "tenant", ConnectionID: "c", Revision: 1, Resource: resource.URL + "/mcp"}
	first, _ := p.Acquire(context.Background(), auth, target)
	second, _ := p.Acquire(context.Background(), auth, target)
	if tokens.calls.Load() != 1 || bearerOf(t, first, resource.URL) != bearerOf(t, second, resource.URL) {
		t.Fatal("valid token was not reused", tokens.calls.Load())
	}
	// Changing revision, connection, tenant or configuration never reuses it.
	for _, other := range []Target{{TenantID: "tenant", ConnectionID: "c", Revision: 2, Resource: target.Resource}, {TenantID: "tenant", ConnectionID: "d", Revision: 1, Resource: target.Resource}, {TenantID: "other", ConnectionID: "c", Revision: 1, Resource: target.Resource}} {
		before := tokens.calls.Load()
		if _, err := p.Acquire(context.Background(), auth, other); err != nil || tokens.calls.Load() != before+1 {
			t.Fatal("token shared across cache namespaces", other, err)
		}
	}
	changed := auth
	changed.Scopes = []string{"more"}
	before := tokens.calls.Load()
	if _, err := p.Acquire(context.Background(), changed, target); err != nil || tokens.calls.Load() != before+1 {
		t.Fatal("token reused after configuration change")
	}
	// Shortly before expiry (300s lifetime, 30s skew) a new token is fetched.
	c.Add(269 * time.Second)
	before = tokens.calls.Load()
	if _, err := p.Acquire(context.Background(), auth, target); err != nil || tokens.calls.Load() != before {
		t.Fatal("token refreshed too early")
	}
	c.Add(2 * time.Second)
	refreshed, err := p.Acquire(context.Background(), auth, target)
	if err != nil || tokens.calls.Load() != before+1 || bearerOf(t, refreshed, resource.URL) == bearerOf(t, first, resource.URL) {
		t.Fatal("token used too close to expiry")
	}
	// A stale credential does not evict the newer cached token.
	p.Invalidate(auth, target, first)
	if _, err = p.Acquire(context.Background(), auth, target); err != nil || tokens.calls.Load() != before+1 {
		t.Fatal("invalidating an old token evicted the current one")
	}
	// Upstream 401 for the current token evicts it.
	p.Invalidate(auth, target, refreshed)
	if _, err = p.Acquire(context.Background(), auth, target); err != nil || tokens.calls.Load() != before+2 {
		t.Fatal("rejected token was reused")
	}
}

func TestTokensWithoutExpiryAreNotCached(t *testing.T) {
	resource, _ := captureServer(t)
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"no-expiry","token_type":"bearer"}`)
	}))
	defer tokens.Close()
	var calls atomic.Int32
	counting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		tokens.Config.Handler.ServeHTTP(w, r)
	}))
	defer counting.Close()
	p, _ := newProvider(t, []string{resource.URL, counting.URL}, nil)
	auth := Auth{Type: OAuthClientCredentials, TokenEndpoint: counting.URL + "/token", ClientID: "svc", ClientSecretRef: "client", ClientAuthMethod: ClientSecretPost}
	target := Target{TenantID: "tenant", ConnectionID: "c", Revision: 1, Resource: resource.URL}
	for i := 0; i < 2; i++ {
		credential, err := p.Acquire(context.Background(), auth, target)
		if err != nil || bearerOf(t, credential, resource.URL) != "Bearer no-expiry" {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("token without expiry was cached", calls.Load())
	}
}

func TestClientCredentialsErrorsAreNotCachedAndDoNotLeak(t *testing.T) {
	resource, _ := captureServer(t)
	tokens := newTokenServer(t)
	p, _ := newProvider(t, []string{resource.URL, tokens.URL}, nil)
	auth := Auth{Type: OAuthClientCredentials, TokenEndpoint: tokens.URL + "/token", ClientID: "svc", ClientSecretRef: "client", ClientAuthMethod: ClientSecretPost}
	target := Target{TenantID: "tenant", ConnectionID: "c", Revision: 1, Resource: resource.URL}
	tokens.set(401, 300)
	_, err := p.Acquire(context.Background(), auth, target)
	if err == nil || strings.Contains(err.Error(), clientSecret) || strings.Contains(err.Error(), "invalid_client") {
		t.Fatal("token endpoint error leaked or succeeded", err)
	}
	tokens.set(200, 300)
	if _, err = p.Acquire(context.Background(), auth, target); err != nil || tokens.calls.Load() != 2 {
		t.Fatal("error response was cached", err, tokens.calls.Load())
	}
}

func TestClientCredentialsSingleFlight(t *testing.T) {
	resource, _ := captureServer(t)
	tokens := newTokenServer(t)
	tokens.release = make(chan struct{})
	p, _ := newProvider(t, []string{resource.URL, tokens.URL}, nil)
	auth := Auth{Type: OAuthClientCredentials, TokenEndpoint: tokens.URL + "/token", ClientID: "svc", ClientSecretRef: "client", ClientAuthMethod: ClientSecretPost}
	target := Target{TenantID: "tenant", ConnectionID: "c", Revision: 1, Resource: resource.URL}
	var wg sync.WaitGroup
	results := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			credential, err := p.Acquire(context.Background(), auth, target)
			if err != nil {
				results <- "error"
				return
			}
			req, _ := http.NewRequest(http.MethodGet, resource.URL, nil)
			_ = credential.Apply(req)
			results <- req.Header.Get("Authorization")
		}()
	}
	for tokens.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(tokens.release)
	wg.Wait()
	close(results)
	for value := range results {
		if value != "Bearer issued-1-" {
			t.Fatal("concurrent acquisitions did not share one token request", value)
		}
	}
	if tokens.calls.Load() != 1 {
		t.Fatal("token endpoint called concurrently", tokens.calls.Load())
	}
}

func TestTokenEndpointMustPassPolicyAndRedirectsAreRefused(t *testing.T) {
	resource, _ := captureServer(t)
	tokens := newTokenServer(t)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, tokens.URL+"/token", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	p, _ := newProvider(t, []string{resource.URL, redirect.URL}, nil)
	target := Target{TenantID: "tenant", ConnectionID: "c", Revision: 1, Resource: resource.URL}
	auth := Auth{Type: OAuthClientCredentials, TokenEndpoint: tokens.URL + "/token", ClientID: "svc", ClientSecretRef: "client", ClientAuthMethod: ClientSecretPost}
	if _, err := p.Acquire(context.Background(), auth, target); err == nil || tokens.calls.Load() != 0 {
		t.Fatal("unapproved token endpoint was contacted")
	}
	auth.TokenEndpoint = redirect.URL + "/token"
	if _, err := p.Acquire(context.Background(), auth, target); err == nil || tokens.calls.Load() != 0 {
		t.Fatal("token request followed a redirect")
	}
}

func TestTokenExchangeIsolatesPrincipalsAndFailsClosed(t *testing.T) {
	resource, _ := captureServer(t)
	tokens := newTokenServer(t)
	p, c := newProvider(t, []string{resource.URL, tokens.URL}, nil)
	auth := Auth{Type: TokenExchange, TokenEndpoint: tokens.URL + "/token", Audience: "downstream", Scopes: []string{"read"}}
	alice := Target{TenantID: "tenant", ConnectionID: "c", Revision: 1, Resource: resource.URL, PrincipalID: "alice", SubjectToken: "alice-jwt"}
	bob := alice
	bob.PrincipalID, bob.SubjectToken = "bob", "bob-jwt"
	a1, err := p.Acquire(context.Background(), auth, alice)
	if err != nil {
		t.Fatal(err)
	}
	form := tokens.forms[0]
	if form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:token-exchange" || form.Get("subject_token") != "alice-jwt" || form.Get("subject_token_type") != TokenTypeJWT || form.Get("audience") != "downstream" || form.Get("scope") != "read" || form.Get("requested_token_type") != TokenTypeAccessToken {
		t.Fatalf("token exchange request shape %v", form)
	}
	b1, _ := p.Acquire(context.Background(), auth, bob)
	a2, _ := p.Acquire(context.Background(), auth, alice)
	if tokens.calls.Load() != 2 || bearerOf(t, a1, resource.URL) != bearerOf(t, a2, resource.URL) || bearerOf(t, a1, resource.URL) == bearerOf(t, b1, resource.URL) || !strings.HasSuffix(bearerOf(t, b1, resource.URL), "bob-jwt") {
		t.Fatal("exchanged token crossed principals or was not cached per principal", tokens.calls.Load())
	}
	// Same principal presenting the same subject token as a different actor
	// is a different cache entry.
	actor := alice
	actor.ActorID = "agent"
	if _, err = p.Acquire(context.Background(), auth, actor); err != nil || tokens.calls.Load() != 3 {
		t.Fatal("exchanged token shared across actors")
	}
	// Caching never outlives the caller's own token.
	short := alice
	short.SubjectToken, short.SubjectExpiresAt = "short-jwt", c.Now().Add(10*time.Second)
	_, _ = p.Acquire(context.Background(), auth, short)
	c.Add(11 * time.Second)
	if _, err = p.Acquire(context.Background(), auth, short); err == nil {
		t.Fatal("expired subject token was exchanged or its exchange reused")
	}
	before := tokens.calls.Load()
	for _, missing := range []Target{{TenantID: "tenant", ConnectionID: "c", Resource: resource.URL, PrincipalID: "alice"}, {TenantID: "tenant", ConnectionID: "c", Resource: resource.URL, SubjectToken: "x"}} {
		if _, err = p.Acquire(context.Background(), auth, missing); err == nil {
			t.Fatal("token exchange without a verified caller token did not fail closed")
		}
	}
	if tokens.calls.Load() != before {
		t.Fatal("token endpoint contacted without a subject token")
	}
}

type fakeIDTokens struct {
	calls atomic.Int32
	token string
}

func (f *fakeIDTokens) IDToken(_ context.Context, tenant, ref, audience string) (string, error) {
	f.calls.Add(1)
	if tenant != "tenant" || ref != "workload" || audience != "https://svc.example" {
		return "", fmt.Errorf("unexpected request")
	}
	return f.token, nil
}

func TestGoogleIDTokenUsesInjectedSourceAndDefaultsToNotConfigured(t *testing.T) {
	resource, _ := captureServer(t)
	auth := Auth{Type: GoogleIDToken, Audience: "https://svc.example", IdentityRef: "workload"}
	target := Target{TenantID: "tenant", ConnectionID: "c", Revision: 1, Resource: resource.URL}
	unconfigured, _ := newProvider(t, []string{resource.URL}, nil)
	_, err := unconfigured.Acquire(context.Background(), auth, target)
	var typed *Error
	if err == nil || !errorsAs(err, &typed) || typed.Code != "google_id_token_not_configured" {
		t.Fatal("unconfigured ID token source did not report not configured", err)
	}
	p, c := newProvider(t, []string{resource.URL}, nil)
	claims, _ := json.Marshal(map[string]any{"aud": "https://svc.example", "exp": c.Now().Add(time.Hour).Unix()})
	ids := &fakeIDTokens{token: "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"}
	p.idTokens = ids
	first, err := p.Acquire(context.Background(), auth, target)
	if err != nil || bearerOf(t, first, resource.URL) != "Bearer "+ids.token {
		t.Fatal("ID token not applied as bearer", err)
	}
	_, _ = p.Acquire(context.Background(), auth, target)
	if ids.calls.Load() != 1 {
		t.Fatal("ID token not cached until expiry")
	}
}

func errorsAs(err error, target **Error) bool {
	typed, ok := err.(*Error)
	if ok {
		*target = typed
	}
	return ok
}

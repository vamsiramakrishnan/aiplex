package toolauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
)

type fixture struct {
	server        *httptest.Server
	registrations atomic.Int32
	resource      string
	issuer        string
}
type options struct {
	initialToken                           string
	cimd, wrongIssuer, badRedirect, noPKCE bool
	status                                 int
}

func newFixture(t *testing.T, o options) *fixture {
	t.Helper()
	f := &fixture{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			json.NewEncoder(w).Encode(ProtectedResourceMetadata{Resource: f.resource, AuthorizationServers: []string{f.issuer}})
		case "/.well-known/oauth-authorization-server/tenant":
			issuer := f.issuer
			if o.wrongIssuer {
				issuer = "https://attacker.example"
			}
			pkce := []string{"S256"}
			if o.noPKCE {
				pkce = nil
			}
			json.NewEncoder(w).Encode(AuthorizationServerMetadata{Issuer: issuer, AuthorizationEndpoint: f.server.URL + "/authorize", TokenEndpoint: f.server.URL + "/token", RegistrationEndpoint: f.server.URL + "/register", CIMDSupported: o.cimd, CodeChallengeMethodsSupported: pkce, TokenEndpointAuthMethodsSupported: []string{"none"}})
		case "/register":
			f.registrations.Add(1)
			if o.initialToken != "" && r.Header.Get("Authorization") != "Bearer "+o.initialToken {
				w.WriteHeader(403)
				return
			}
			if r.Method != http.MethodPost {
				t.Error("registration method", r.Method)
				w.WriteHeader(405)
				return
			}
			if o.status != 0 {
				w.WriteHeader(o.status)
				w.Write([]byte(`{"client_secret":"must-not-leak"}`))
				return
			}
			var m ClientMetadata
			if e := json.NewDecoder(r.Body).Decode(&m); e != nil {
				t.Error(e)
			}
			if m.ApplicationType != "native" || m.TokenEndpointAuthMethod != "none" {
				t.Error("unexpected request metadata", m)
			}
			m.ClientID = "issued-client"
			if o.badRedirect {
				m.RedirectURIs = []string{"https://attacker.example/cb"}
			}
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(registrationResponse{ClientMetadata: m, ClientSecret: "server-secret", RegistrationAccessToken: "manage-secret"})
		default:
			w.WriteHeader(404)
		}
	}))
	f.resource = f.server.URL + "/mcp"
	f.issuer = f.server.URL + "/tenant"
	t.Cleanup(f.server.Close)
	return f
}
func (f *fixture) config() Config {
	return Config{Network: endpointpolicy.Config{AllowedOrigins: []string{f.server.URL}, AllowLoopback: true}, Credentials: NewMemoryCredentials()}
}
func (f *fixture) connection() Connection {
	return Connection{TenantID: "tenant", ID: "tool", ResourceURL: f.resource}
}
func profile() ClientProfile {
	return ClientProfile{ID: "desktop", Name: "Desktop Client", RedirectURIs: []string{"http://127.0.0.1:31337/callback"}, ApplicationType: "native"}
}
func mustService(t *testing.T, cfg Config) *Service {
	t.Helper()
	s, e := NewService(cfg)
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func TestDCRDiscoveryAndCredentialIsolation(t *testing.T) {
	f := newFixture(t, options{})
	cfg := f.config()
	s := mustService(t, cfg)
	r, e := s.Prepare(context.Background(), f.connection(), profile())
	if e != nil {
		t.Fatal(e)
	}
	if r.Mode != DCR || r.ClientID != "issued-client" || r.CredentialRef == "" || r.ResourceParameter != f.resource || r.PKCEMethod != "S256" {
		t.Fatalf("unexpected registration %+v", r)
	}
	payload, _ := json.Marshal(r)
	if bytes.Contains(payload, []byte("server-secret")) || bytes.Contains(payload, []byte("manage-secret")) {
		t.Fatal("secret leaked")
	}
	stored, _, e := cfg.Credentials.Get(context.Background(), CredentialKey{"tenant", f.issuer, "desktop"})
	if e != nil || stored.ClientSecret != "server-secret" {
		t.Fatal("credentials not stored", e)
	}
	encoded, _ := json.Marshal(stored)
	if bytes.Contains(encoded, []byte("server-secret")) {
		t.Fatal("credential JSON leaks secret")
	}
	if _, e = s.Prepare(context.Background(), f.connection(), profile()); e != nil {
		t.Fatal(e)
	}
	if f.registrations.Load() != 1 {
		t.Fatal("DCR registration not reused")
	}
	other := f.connection()
	other.TenantID = "another"
	if _, e = s.Prepare(context.Background(), other, profile()); e != nil {
		t.Fatal(e)
	}
	if f.registrations.Load() != 2 {
		t.Fatal("registration reused across tenants")
	}
}
func TestRegistrationPriorityPreRegisteredThenCIMD(t *testing.T) {
	f := newFixture(t, options{cimd: true})
	p := profile()
	p.MetadataURL = "https://client.example/oauth/client.json"
	document := ClientMetadata{ClientID: p.MetadataURL, ClientName: p.Name, RedirectURIs: p.RedirectURIs, TokenEndpointAuthMethod: "none"}
	cfg := f.config()
	cfg.MetadataDocuments = []ClientMetadata{document}
	cfg.PreRegistered = []Registration{{TenantID: "tenant", ProfileID: p.ID, Issuer: f.issuer, ClientID: "static-client", CredentialRef: "secret/static", RedirectURIs: p.RedirectURIs}}
	s := mustService(t, cfg)
	r, e := s.Prepare(context.Background(), f.connection(), p)
	if e != nil || r.Mode != PreRegistered || r.ClientID != "static-client" {
		t.Fatal(r, e)
	}
	cfg.PreRegistered = nil
	s = mustService(t, cfg)
	r, e = s.Prepare(context.Background(), f.connection(), p)
	if e != nil || r.Mode != CIMD || r.ClientID != p.MetadataURL {
		t.Fatal(r, e)
	}
	if f.registrations.Load() != 0 {
		t.Fatal("DCR called despite stronger registration")
	}
	handler, e := s.MetadataHandler("https://client.example")
	if e != nil {
		t.Fatal(e)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "https://spoofed-host.example/oauth/client.json", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), p.MetadataURL) || strings.Contains(rec.Body.String(), "spoofed-host") {
		t.Fatal("CIMD hosting used untrusted host", rec.Body.String())
	}
}
func TestRegistrationRejectsProtocolConfusion(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts options
	}{{"issuer", options{wrongIssuer: true}}, {"redirect", options{badRedirect: true}}, {"pkce", options{noPKCE: true}}, {"http-error", options{status: 400}}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.opts)
			s := mustService(t, f.config())
			_, e := s.Prepare(context.Background(), f.connection(), profile())
			if e == nil {
				t.Fatal("invalid server accepted")
			}
			if strings.Contains(e.Error(), "must-not-leak") {
				t.Fatal("server secret leaked through error")
			}
		})
	}
}
func TestRegistrationRejectsUnapprovedEndpointAndRedirects(t *testing.T) {
	f := newFixture(t, options{})
	s := mustService(t, f.config())
	c := f.connection()
	c.Challenge = `Bearer resource_metadata="http://169.254.169.254/latest/meta-data"`
	if _, e := s.Prepare(context.Background(), c, profile()); e == nil {
		t.Fatal("metadata SSRF accepted")
	}
	for _, redirect := range []string{"http://localhost.attacker.example/callback", "javascript:alert(1)", "https://user:password@example.com/cb", "https://example.com/cb#fragment"} {
		if e := ValidateRedirectURI(redirect); e == nil {
			t.Fatal("unsafe redirect", redirect)
		}
	}
	for _, redirect := range []string{"https://example.com/cb", "http://127.0.0.1:123/cb", "http://[::1]:123/cb", "http://localhost:123/cb"} {
		if e := ValidateRedirectURI(redirect); e != nil {
			t.Fatal(redirect, e)
		}
	}
}
func TestChallengeParsing(t *testing.T) {
	for _, tc := range []struct{ header, want string }{{`Bearer resource_metadata="https://a.example/prm", scope="read,write"`, "https://a.example/prm"}, {`Basic realm="x", Bearer realm="tools", resource_metadata="https://a.example/prm"`, "https://a.example/prm"}, {`Basic realm="resource_metadata=https://attacker.example"`, ""}, {`Bearer scope="foo, resource_metadata=oops"`, ""}} {
		got, e := ResourceMetadataURL(tc.header)
		if e != nil || got != tc.want {
			t.Fatalf("%s: %s %v", tc.header, got, e)
		}
	}
	for _, header := range []string{`Bearer resource_metadata="unterminated`, `Bearer resource_metadata="https://a.example", resource_metadata="https://b.example"`} {
		if _, e := ResourceMetadataURL(header); e == nil {
			t.Fatal("invalid challenge accepted")
		}
	}
}
func TestCIMDFetchCacheAndExactClientID(t *testing.T) {
	var hits atomic.Int32
	clientID := ""
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		json.NewEncoder(w).Encode(ClientMetadata{ClientID: clientID, ClientName: "App", RedirectURIs: []string{"https://client.example/callback"}})
	}))
	defer server.Close()
	clientID = server.URL + "/client.json"
	cfg := Config{Network: endpointpolicy.Config{AllowedOrigins: []string{server.URL}, AllowLoopback: true, TLSConfig: server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}, MetadataCacheTTL: time.Minute}
	s := mustService(t, cfg)
	for i := 0; i < 2; i++ {
		m, e := s.FetchClientMetadata(context.Background(), clientID)
		if e != nil {
			t.Fatal(e)
		}
		m.RedirectURIs[0] = "https://attacker.example"
	}
	if hits.Load() != 1 {
		t.Fatal("metadata cache not reused")
	}
	clientID = server.URL + "/wrong.json"
	if _, e := s.FetchClientMetadata(context.Background(), server.URL+"/other.json"); e == nil {
		t.Fatal("client_id mismatch accepted")
	}
}
func TestNoStoreDoesNotCacheCIMD(t *testing.T) {
	var hits atomic.Int32
	var clientID string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, `{"client_id":%q,"client_name":"A","redirect_uris":["https://client.example/callback"]}`, clientID)
	}))
	defer server.Close()
	clientID = server.URL + "/client.json"
	s := mustService(t, Config{Network: endpointpolicy.Config{AllowedOrigins: []string{server.URL}, AllowLoopback: true, TLSConfig: server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}})
	for i := 0; i < 2; i++ {
		if _, e := s.FetchClientMetadata(context.Background(), clientID); e != nil {
			t.Fatal(e)
		}
	}
	if hits.Load() != 2 {
		t.Fatal("no-store cached")
	}
}

type failingCredentials struct{}

func (failingCredentials) Get(context.Context, CredentialKey) (Credential, string, error) {
	return Credential{}, "", ErrCredentialsNotFound
}
func (failingCredentials) Put(context.Context, CredentialKey, Credential) (string, error) {
	return "", fmt.Errorf("internal-store-secret")
}
func TestDCRRequiresCredentialPersistenceAndRejectsIssuerReuse(t *testing.T) {
	first := newFixture(t, options{})
	second := newFixture(t, options{})
	cfg := first.config()
	cfg.Network.AllowedOrigins = append(cfg.Network.AllowedOrigins, second.server.URL)
	s := mustService(t, cfg)
	for _, f := range []*fixture{first, second} {
		if _, e := s.Prepare(context.Background(), f.connection(), profile()); e != nil {
			t.Fatal(e)
		}
	}
	if first.registrations.Load() != 1 || second.registrations.Load() != 1 {
		t.Fatal("credentials reused across issuers")
	}
	cfg.Credentials = failingCredentials{}
	s = mustService(t, cfg)
	result, e := s.Prepare(context.Background(), first.connection(), profile())
	if e == nil || result.ClientID != "" || strings.Contains(e.Error(), "internal-store-secret") {
		t.Fatal("failed credential persistence returned registration", result, e)
	}
}
func TestMetadataResponseBodyBound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat(" ", (1<<20)+1))) }))
	defer server.Close()
	s := mustService(t, Config{Network: endpointpolicy.Config{AllowedOrigins: []string{server.URL}, AllowLoopback: true}})
	if _, e := s.DiscoverResource(context.Background(), server.URL+"/mcp", ""); e == nil {
		t.Fatal("oversized metadata accepted")
	}
}
func TestExpiredCIMDCacheDoesNotAuthorizeOnFetchFailure(t *testing.T) {
	var hits atomic.Int32
	var clientID string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) > 1 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprintf(w, `{"client_id":%q,"client_name":"A","redirect_uris":["https://client.example/callback"]}`, clientID)
	}))
	defer server.Close()
	clientID = server.URL + "/client.json"
	s := mustService(t, Config{Network: endpointpolicy.Config{AllowedOrigins: []string{server.URL}, AllowLoopback: true, TLSConfig: server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}, MetadataCacheTTL: time.Nanosecond})
	if _, e := s.FetchClientMetadata(context.Background(), clientID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.FetchClientMetadata(context.Background(), clientID); e == nil {
		t.Fatal("stale cached client metadata authorized")
	}
}

func TestConcurrentDCRCallsCoalescePerRegistration(t *testing.T) {
	f := newFixture(t, options{})
	s := mustService(t, f.config())
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			r, e := s.Prepare(context.Background(), f.connection(), profile())
			if e != nil || r.ClientID != "issued-client" {
				t.Error(r, e)
			}
		}()
	}
	group.Wait()
	if f.registrations.Load() != 1 {
		t.Fatal("duplicate concurrent DCR issuance", f.registrations.Load())
	}
}

type staticRegistrationAuthorizer string

func (a staticRegistrationAuthorizer) InitialAccessToken(context.Context, string) (string, error) {
	return string(a), nil
}
func TestProtectedDCRUsesHostOwnedInitialAccessToken(t *testing.T) {
	f := newFixture(t, options{initialToken: "initial-secret"})
	cfg := f.config()
	cfg.RegistrationAuthorizer = staticRegistrationAuthorizer("initial-secret")
	s := mustService(t, cfg)
	r, e := s.Prepare(context.Background(), f.connection(), profile())
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "initial-secret") {
		t.Fatal("initial access token leaked")
	}
}

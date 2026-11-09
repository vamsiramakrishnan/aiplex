package toolauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type runtimeFixture struct {
	runtime      *Runtime
	service      *Service
	store        resources.Store
	vault        *MemorySessionVault
	now          time.Time
	key          SessionKey
	reg          Registration
	calls        atomic.Int32
	server       *httptest.Server
	tokenHandler func(http.ResponseWriter, *http.Request)
}

func newRuntimeFixture(t *testing.T) *runtimeFixture {
	t.Helper()
	f := &runtimeFixture{store: resources.NewMemoryStore(), vault: NewMemorySessionVault(), now: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[%q]}`, f.server.URL+"/mcp", f.server.URL)
		case "/.well-known/oauth-authorization-server":
			fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"code_challenge_methods_supported":["S256"],"authorization_response_iss_parameter_supported":true}`, f.server.URL, f.server.URL+"/authorize", f.server.URL+"/token")
		case "/token":
			f.calls.Add(1)
			if e := r.ParseForm(); e != nil {
				t.Error(e)
			}
			if r.Form.Get("resource") != f.server.URL+"/mcp" || r.Form.Get("client_id") != "client" {
				t.Error("lost resource or client binding", r.Form)
			}
			if f.tokenHandler != nil {
				f.tokenHandler(w, r)
				return
			}
			if r.Form.Get("grant_type") == "authorization_code" {
				if len(r.Form.Get("code_verifier")) < 43 {
					t.Error("missing PKCE verifier")
				}
				fmt.Fprint(w, `{"access_token":"access-1","refresh_token":"refresh-1","token_type":"bearer","expires_in":60,"scope":"read"}`)
			} else {
				fmt.Fprint(w, `{"access_token":"access-2","refresh_token":"refresh-2","token_type":"Bearer","expires_in":60,"scope":"read"}`)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.server.Close)
	service, e := NewService(Config{Network: endpointpolicy.Config{AllowedOrigins: []string{f.server.URL}, AllowLoopback: true}, Credentials: NewMemoryCredentials(), PreRegistered: []Registration{{TenantID: "tenant", ProfileID: "profile", Issuer: f.server.URL, ClientID: "client", RedirectURIs: []string{"http://127.0.0.1:4321/callback"}}}})
	if e != nil {
		t.Fatal(e)
	}
	f.service = service
	reg, e := service.Prepare(context.Background(), Connection{TenantID: "tenant", ID: "tool", ResourceURL: f.server.URL + "/mcp", Issuer: f.server.URL}, ClientProfile{ID: "profile", Name: "Tool", ApplicationType: "native", RedirectURIs: []string{"http://127.0.0.1:4321/callback"}, RefreshTokens: true})
	if e != nil {
		t.Fatal(e)
	}
	f.reg = reg
	f.key = SessionKey{Owner: Owner{TenantID: "tenant", PrincipalID: "human", ActorID: "agent"}, ConnectionID: "tool", ConnectionRevision: 1, Resource: f.server.URL + "/mcp", Issuer: f.server.URL, ProfileID: "profile"}
	f.runtime, e = NewRuntime(RuntimeConfig{Registration: service, Store: f.store, Vault: f.vault, Clock: func() time.Time { return f.now }})
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *runtimeFixture) start(t *testing.T) (AuthorizationChallenge, AuthorizationResponse) {
	t.Helper()
	c, e := f.runtime.Begin(context.Background(), AuthorizationStart{Key: f.key, Registration: f.reg, RedirectURI: "http://127.0.0.1:4321/callback", Scopes: []string{"read"}})
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(c.AuthorizationURL)
	q := u.Query()
	if q.Get("resource") != f.key.Resource || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("response_type") != "code" {
		t.Fatal("authorization request binding missing", q)
	}
	return c, AuthorizationResponse{FlowID: c.FlowID, State: q.Get("state"), Code: "private-code", Issuer: f.key.Issuer}
}
func (f *runtimeFixture) authorize(t *testing.T) SessionInfo {
	t.Helper()
	_, input := f.start(t)
	info, e := f.runtime.Complete(context.Background(), f.key, input)
	if e != nil {
		t.Fatal(e)
	}
	return info
}
func TestOAuthRuntimeBindsCallbackOwnerResourceIssuerAndSingleUseState(t *testing.T) {
	f := newRuntimeFixture(t)
	ctx := context.Background()
	challenge, input := f.start(t)
	for _, mutate := range []func(*SessionKey, *AuthorizationResponse){func(k *SessionKey, _ *AuthorizationResponse) { k.Owner.ActorID = "other" }, func(k *SessionKey, _ *AuthorizationResponse) { k.Owner.PrincipalID = "other" }, func(k *SessionKey, _ *AuthorizationResponse) { k.Owner.TenantID = "other" }, func(k *SessionKey, _ *AuthorizationResponse) { k.ConnectionRevision++ }, func(_ *SessionKey, r *AuthorizationResponse) { r.State = "bad" }, func(_ *SessionKey, r *AuthorizationResponse) { r.Issuer = "https://other.example" }, func(_ *SessionKey, r *AuthorizationResponse) { r.Issuer = "" }} {
		key, callback := f.key, input
		mutate(&key, &callback)
		if _, e := f.runtime.Complete(ctx, key, callback); e == nil {
			t.Fatal("forged callback accepted", key, callback)
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("invalid callbacks reached token endpoint")
	}
	info, e := f.runtime.Complete(ctx, f.key, input)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.Complete(ctx, f.key, input); e == nil || f.calls.Load() != 1 {
		t.Fatal("callback replay repeated token exchange", e, f.calls.Load())
	}
	token, e := f.runtime.AccessToken(ctx, f.key, info.GrantID)
	if e != nil || token != "access-1" {
		t.Fatal("bound credential unavailable", e)
	}
	for _, kind := range []string{"tool-oauth-flow", "tool-oauth-session"} {
		records, e := f.store.List(ctx, "tenant", kind)
		if e != nil {
			t.Fatal(e)
		}
		for _, rec := range records {
			for _, secret := range []string{"private-code", "access-1", "refresh-1", input.State} {
				if strings.Contains(string(rec.Spec), secret) {
					t.Fatal("secret persisted in public resource", kind, secret)
				}
			}
		}
	}
	public, _ := json.Marshal(struct {
		Session SessionInfo
		Secrets SessionSecrets
	}{info, SessionSecrets{AccessToken: "access-1", RefreshToken: "refresh-1", Verifier: "private-verifier"}})
	if strings.Contains(string(public), "access-1") || strings.Contains(string(public), "private-verifier") {
		t.Fatal("secret leaked in serialization", string(public))
	}
	if !challenge.ExpiresAt.Before(f.now.Add(6 * time.Minute)) {
		t.Fatal("unbounded authorization lifetime")
	}
}
func TestOAuthRuntimeConcurrentCallbackUsesOneDurableClaim(t *testing.T) {
	f := newRuntimeFixture(t)
	_, input := f.start(t)
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := f.runtime.Complete(context.Background(), f.key, input); e == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || f.calls.Load() != 1 {
		t.Fatal("callback dispatch was not one-shot", ok.Load(), f.calls.Load())
	}
}
func TestOAuthRuntimeExpiredOrUnknownExchangeCannotReplay(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		f := newRuntimeFixture(t)
		_, input := f.start(t)
		f.now = f.now.Add(6 * time.Minute)
		if _, e := f.runtime.Complete(context.Background(), f.key, input); e == nil || f.calls.Load() != 0 {
			t.Fatal(e, f.calls.Load())
		}
	})
	t.Run("ambiguous", func(t *testing.T) {
		f := newRuntimeFixture(t)
		f.tokenHandler = func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(500)
			fmt.Fprint(w, "private-provider-secret")
		}
		_, input := f.start(t)
		_, e := f.runtime.Complete(context.Background(), f.key, input)
		if e == nil || strings.Contains(e.Error(), "private-provider-secret") {
			t.Fatal(e)
		}
		_, _ = f.runtime.Complete(context.Background(), f.key, input)
		if f.calls.Load() != 1 {
			t.Fatal("ambiguous exchange replayed")
		}
	})
}
func TestOAuthRuntimeRefreshRotationIsFencedAcrossReplicas(t *testing.T) {
	f := newRuntimeFixture(t)
	info := f.authorize(t)
	f.now = f.now.Add(40 * time.Second)
	started, release := make(chan struct{}), make(chan struct{})
	f.tokenHandler = func(w http.ResponseWriter, r *http.Request) {
		if r.Form.Get("refresh_token") != "refresh-1" {
			t.Error("wrong refresh generation")
		}
		close(started)
		<-release
		fmt.Fprint(w, `{"access_token":"access-2","refresh_token":"refresh-2","token_type":"Bearer","expires_in":60,"scope":"read"}`)
	}
	second, e := NewRuntime(RuntimeConfig{Registration: f.service, Store: f.store, Vault: f.vault, Clock: func() time.Time { return f.now }})
	if e != nil {
		t.Fatal(e)
	}
	result := make(chan error, 1)
	go func() {
		token, e := f.runtime.AccessToken(context.Background(), f.key, info.GrantID)
		if e == nil && token != "access-2" {
			e = fmt.Errorf("wrong refreshed token")
		}
		result <- e
	}()
	<-started
	if _, e = second.AccessToken(context.Background(), f.key, info.GrantID); e == nil {
		t.Error("second replica replayed rotating refresh")
	}
	close(release)
	if e = <-result; e != nil {
		t.Fatal(e)
	}
	if f.calls.Load() != 2 {
		t.Fatal("refresh was not one-shot", f.calls.Load())
	}
	if token, e := second.AccessToken(context.Background(), f.key, info.GrantID); e != nil || token != "access-2" {
		t.Fatal(e)
	}
}
func TestOAuthRuntimeRejectsRefreshReuseAndEscalatedAudienceOrScope(t *testing.T) {
	for name, response := range map[string]string{"refresh reuse": `{"access_token":"new","refresh_token":"refresh-1","token_type":"Bearer","expires_in":60}`, "wrong resource": `{"access_token":"new","refresh_token":"new-refresh","token_type":"Bearer","expires_in":60,"resource":"https://other.example"}`, "scope escalation": `{"access_token":"new","refresh_token":"new-refresh","token_type":"Bearer","expires_in":60,"scope":"admin"}`} {
		t.Run(name, func(t *testing.T) {
			f := newRuntimeFixture(t)
			info := f.authorize(t)
			f.now = f.now.Add(40 * time.Second)
			f.tokenHandler = func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, response) }
			if _, e := f.runtime.AccessToken(context.Background(), f.key, info.GrantID); e == nil {
				t.Fatal("unsafe token response accepted")
			}
			_, _ = f.runtime.AccessToken(context.Background(), f.key, info.GrantID)
			if f.calls.Load() != 2 {
				t.Fatal("unknown refresh retried")
			}
		})
	}
}
func TestOAuthRuntimeRevocationInvalidatesDispatchGrantAndStaleAuthorization(t *testing.T) {
	f := newRuntimeFixture(t)
	ctx := context.Background()
	info := f.authorize(t)
	_, input := f.start(t)
	if e := f.runtime.Revoke(ctx, f.key); e != nil {
		t.Fatal(e)
	}
	if e := f.runtime.CheckGrant(ctx, f.key, info.GrantID); e == nil {
		t.Fatal("revoked grant passed dispatch check")
	}
	if _, e := f.runtime.AccessToken(ctx, f.key, info.GrantID); e == nil {
		t.Fatal("revoked token exposed")
	}
	if _, e := f.runtime.Complete(ctx, f.key, input); e == nil {
		t.Fatal("authorization before revocation restored stale grant")
	}
	if e := f.runtime.Revoke(ctx, f.key); e != nil {
		t.Fatal("repeated revocation failed", e)
	}
	fresh := f.authorize(t)
	if e := f.runtime.CheckGrant(ctx, f.key, fresh.GrantID); e != nil {
		t.Fatal(e)
	}
	if e := f.runtime.CheckGrant(ctx, f.key, info.GrantID); e == nil {
		t.Fatal("old generation became eligible again")
	}
}

type runtimeLostAckStore struct {
	resources.Store
	lost atomic.Bool
}

func (s *runtimeLostAckStore) Put(ctx context.Context, in resources.Record, expected uint64) (resources.Record, error) {
	out, e := s.Store.Put(ctx, in, expected)
	if e == nil && in.Kind == "tool-oauth-session" && strings.Contains(string(in.Spec), `"status":"active"`) && s.lost.CompareAndSwap(true, false) {
		return resources.Record{}, fmt.Errorf("commit acknowledgement lost")
	}
	return out, e
}

type runtimeCleanupVault struct {
	*MemorySessionVault
	fail atomic.Bool
}

func (v *runtimeCleanupVault) Destroy(ctx context.Context, ref string) error {
	if v.fail.Load() {
		return fmt.Errorf("cleanup temporarily unavailable")
	}
	return v.MemorySessionVault.Destroy(ctx, ref)
}
func TestOAuthRuntimeLostSessionAcknowledgementDoesNotDestroyCommittedToken(t *testing.T) {
	f := newRuntimeFixture(t)
	store := &runtimeLostAckStore{Store: f.store}
	store.lost.Store(true)
	f.store = store
	var e error
	f.runtime, e = NewRuntime(RuntimeConfig{Registration: f.service, Store: store, Vault: f.vault, Clock: func() time.Time { return f.now }})
	if e != nil {
		t.Fatal(e)
	}
	info := f.authorize(t)
	if token, e := f.runtime.AccessToken(context.Background(), f.key, info.GrantID); e != nil || token != "access-1" {
		t.Fatal("lost acknowledgement destroyed live credentials", token, e)
	}
	f.now = f.now.Add(40 * time.Second)
	store.lost.Store(true)
	if token, e := f.runtime.AccessToken(context.Background(), f.key, info.GrantID); e != nil || token != "access-2" {
		t.Fatal("refresh lost acknowledgement destroyed live credentials", token, e)
	}
	if f.calls.Load() != 2 {
		t.Fatal("lost acknowledgements replayed token writes", f.calls.Load())
	}
}
func TestOAuthRuntimeRevocationCleanupIsRetryableWithoutRestoringAccess(t *testing.T) {
	f := newRuntimeFixture(t)
	vault := &runtimeCleanupVault{MemorySessionVault: f.vault}
	var e error
	f.runtime, e = NewRuntime(RuntimeConfig{Registration: f.service, Store: f.store, Vault: vault, Clock: func() time.Time { return f.now }})
	if e != nil {
		t.Fatal(e)
	}
	info := f.authorize(t)
	vault.fail.Store(true)
	if e = f.runtime.Revoke(context.Background(), f.key); e == nil {
		t.Fatal("failed cleanup was not surfaced")
	}
	if _, e = f.runtime.AccessToken(context.Background(), f.key, info.GrantID); e == nil {
		t.Fatal("failed cleanup restored access")
	}
	_, saved, e := f.runtime.loadSession(context.Background(), f.key)
	if e != nil || saved.Status != "revoked" || len(saved.CleanupRefs) != 1 {
		t.Fatal("cleanup authority was lost", saved, e)
	}
	vault.fail.Store(false)
	if e = f.runtime.Revoke(context.Background(), f.key); e != nil {
		t.Fatal("revocation cleanup did not retry", e)
	}
	_, saved, e = f.runtime.loadSession(context.Background(), f.key)
	if e != nil || len(saved.CleanupRefs) != 0 || saved.Status != "revoked" {
		t.Fatal(saved, e)
	}
}

func TestOAuthRuntimeRevokePendingFirstGrantFencesItsLateCallback(t *testing.T) {
	f := newRuntimeFixture(t)
	_, input := f.start(t)
	if e := f.runtime.Revoke(context.Background(), f.key); e != nil {
		t.Fatal(e)
	}
	if _, e := f.runtime.Complete(context.Background(), f.key, input); e == nil || f.calls.Load() != 0 {
		t.Fatal("pending first authorization escaped revocation", e, f.calls.Load())
	}
	info := f.authorize(t)
	if e := f.runtime.CheckGrant(context.Background(), f.key, info.GrantID); e != nil {
		t.Fatal("new authorization after revocation unavailable", e)
	}
}

func TestOAuthRuntimeRejectsTypedNilPersistenceDependencies(t *testing.T) {
	f := newRuntimeFixture(t)
	var vault *MemorySessionVault
	var store *resources.MemoryStore
	for _, cfg := range []RuntimeConfig{{Registration: f.service, Store: f.store, Vault: vault}, {Registration: f.service, Store: store, Vault: f.vault}} {
		if _, e := NewRuntime(cfg); e == nil {
			t.Fatal("typed nil dependency enabled OAuth runtime")
		}
	}
}

func TestDCRRefreshCapabilityIsPinnedToPersistedRegisteredGrantTypes(t *testing.T) {
	f := newFixture(t, options{})
	cfg := f.config()
	service := mustService(t, cfg)
	p := profile()
	ctx := context.Background()
	if _, e := service.Prepare(ctx, f.connection(), p); e != nil {
		t.Fatal(e)
	}
	p.RefreshTokens = true
	if _, e := service.Prepare(ctx, f.connection(), p); e == nil || f.registrations.Load() != 1 {
		t.Fatal("cached authorization-code-only client claimed refresh capability", e, f.registrations.Load())
	}
	p.ID = "refresh-capable"
	reg, e := service.Prepare(ctx, f.connection(), p)
	if e != nil || !reg.AllowRefresh || f.registrations.Load() != 2 {
		t.Fatal(reg, e, f.registrations.Load())
	}
	key := CredentialKey{TenantID: "tenant", Issuer: f.issuer, ProfileID: p.ID}
	credential, _, e := cfg.Credentials.Get(ctx, key)
	if e != nil || !contains(credential.GrantTypes, "refresh_token") {
		t.Fatal("registered grant capability not persisted", credential, e)
	}
	credential.GrantTypes[0] = "changed"
	if _, e = service.Prepare(ctx, f.connection(), p); e != nil || f.registrations.Load() != 2 {
		t.Fatal("refresh client was unnecessarily registered again", e, f.registrations.Load())
	}
}

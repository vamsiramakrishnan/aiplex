package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type federationFixture struct {
	key      *rsa.PrivateKey
	server   *httptest.Server
	fetches  atomic.Int32
	store    *agentfederation.Store
	resolver *JWKSKeyResolver
	engine   *agentfederation.Engine
	binding  agentfederation.Binding
}

func newFederationFixture(t *testing.T) *federationFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &federationFixture{key: key}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.fetches.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(f.server.Close)
	roots := f.server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	f.resolver = NewJWKSKeyResolver(endpointpolicy.Config{AllowLoopback: true, TLSConfig: &tls.Config{RootCAs: roots}})
	f.store = agentfederation.NewStore(resources.NewMemoryStore())
	f.engine = &agentfederation.Engine{Keys: f.resolver, Agents: func(_ context.Context, id string) (agentfederation.Agent, error) {
		if id != "tutor-agent" {
			return agentfederation.Agent{}, agentfederation.ErrNotFound
		}
		return agentfederation.Agent{ClientID: id, Active: true, ActorSubject: "spiffe://aiplex.test/sa/tutor-agent"}, nil
	}}
	f.binding, err = agentfederation.NewBinding("tutor-agent", "ext", agentfederation.BindingRequest{Preset: agentfederation.PresetOIDC, Inputs: map[string]string{"issuer": "https://issuer.test", "jwks_url": f.server.URL + "/jwks", "subject": "workload-7", "audience": "https://aiplex.test"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Put(context.Background(), "tenant-a", f.binding, 0); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *federationFixture) token(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	claims := map[string]any{"iss": "https://issuer.test", "sub": "workload-7", "aud": "https://aiplex.test", "exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Add(-time.Minute).Unix(),
		"groups": []string{"aiplex-admins"}, "hd": "example.com", "email": "spoof@example.com", "act": map[string]any{"sub": "spoofed-actor"}}
	if mutate != nil {
		mutate(claims)
	}
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	c, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func bearer(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/iam/whoami", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

type rejectVerifier struct{}

func (rejectVerifier) Verify(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func TestFederatedTokenAuthenticatesAsBoundAgent(t *testing.T) {
	f := newFederationFixture(t)
	static := IdentitySource{Issuer: "https://static.example.com", Audience: "aud", Verifier: rejectVerifier{}, PoolID: "pool"}
	v, err := NewWIFValidatorFromSources(nil, []IdentitySource{static})
	if err != nil {
		t.Fatal(err)
	}
	token := f.token(t, nil)
	if _, err := v.ExtractIdentity(bearer(token)); err == nil || !strings.Contains(err.Error(), "no trusted identity source") {
		t.Fatalf("without federation the token must be rejected as before: %v", err)
	}
	v.WithFederation(&AgentFederation{Tenant: "tenant-a", Bindings: f.store, Engine: f.engine})
	identity, err := v.ExtractIdentity(bearer(token))
	if err != nil {
		t.Fatalf("federated identity: %v", err)
	}
	pool := FederationPoolID("tenant-a")
	if identity.Provider != "https://issuer.test" || identity.PoolID != pool || identity.Subject != "workload-7" || identity.IsWorkforce {
		t.Fatalf("identity = %+v", identity)
	}
	if identity.PrincipalID != CanonicalPrincipalID("https://issuer.test", pool, "workload-7") {
		t.Fatalf("principal = %s", identity.PrincipalID)
	}
	if identity.AuthorizedParty != "tutor-agent" || identity.ActorSubject != "spiffe://aiplex.test/sa/tutor-agent" || identity.ActorID != CanonicalPrincipalID("https://issuer.test", pool, "spiffe://aiplex.test/sa/tutor-agent") {
		t.Fatalf("actor = %+v", identity)
	}
	if len(identity.Groups) != 0 || identity.Domain != "" || identity.Email != "" {
		t.Fatalf("external groups/domain/email must not flow into AIPlex identity: %+v", identity)
	}
	if identity.VerifiedEvidence == nil || !identity.VerifiedEvidence.LifetimeValid {
		t.Fatalf("verified lifetime evidence missing: %+v", identity.VerifiedEvidence)
	}
	// Access resolves through the ordinary path: no role from external groups.
	access := v.resolveBindings(identity, nil)
	if len(access.Roles) != 0 || len(access.Scopes) != 0 {
		t.Fatalf("binding must not grant roles or scopes by itself: %+v", access)
	}
	// Keys are cached by the verifier.
	if _, err := v.ExtractIdentity(bearer(f.token(t, nil))); err != nil || f.fetches.Load() != 1 {
		t.Fatalf("second request: %v, fetches=%d", err, f.fetches.Load())
	}
}

func TestFederatedTokenFailuresFailClosed(t *testing.T) {
	f := newFederationFixture(t)
	v := NewWIFValidator(nil, WIFConfig{}).WithFederation(&AgentFederation{Tenant: "tenant-a", Bindings: f.store, Engine: f.engine})
	cases := map[string]func(map[string]any){
		"wrong audience": func(c map[string]any) { c["aud"] = "https://other.test" },
		"expired":        func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() },
		"unbound":        func(c map[string]any) { c["sub"] = "workload-8" },
		"cnf bound":      func(c map[string]any) { c["cnf"] = map[string]any{"x5t#S256": "x"} },
	}
	for name, mutate := range cases {
		if _, err := v.ExtractIdentity(bearer(f.token(t, mutate))); err == nil {
			t.Fatalf("%s: token accepted", name)
		}
	}
	// Bindings of another tenant do not authenticate.
	other := NewWIFValidator(nil, WIFConfig{}).WithFederation(&AgentFederation{Tenant: "tenant-b", Bindings: f.store, Engine: f.engine})
	if _, err := other.ExtractIdentity(bearer(f.token(t, nil))); err == nil {
		t.Fatal("tenant-b accepted a tenant-a binding")
	}
}

func TestJWKSKeyResolverHonoursEndpointPolicy(t *testing.T) {
	f := newFederationFixture(t)
	strict := NewJWKSKeyResolver(endpointpolicy.Config{}) // no loopback opt-in
	engine := *f.engine
	engine.Keys = strict
	r := engine.Evaluate(context.Background(), agentfederation.Input{Token: f.token(t, nil), Finder: f.store.Tenant("tenant-a")})
	if r.Valid {
		t.Fatal("loopback JWKS fetched without opt-in")
	}
	for _, c := range r.Checks {
		if c.Name == agentfederation.CheckSignature && (c.Status != agentfederation.StatusFail || c.Reason == "") {
			t.Fatalf("signature check = %+v", c)
		}
	}
	if f.fetches.Load() != 0 {
		t.Fatal("policy-denied JWKS endpoint was contacted")
	}
}

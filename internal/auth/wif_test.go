package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

type verifyFunc func(context.Context, string) (map[string]any, error)

func (f verifyFunc) Verify(ctx context.Context, token string) (map[string]any, error) {
	return f(ctx, token)
}

func TestWIFRejectsUnverifiedAndUntrustedClaims(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer forged")
	v := NewWIFValidator(registry.NewMemoryStore(), WIFConfig{})
	if _, err := v.ExtractIdentity(r); err == nil {
		t.Fatal("unconfigured verifier accepted request")
	}
	v = NewWIFValidator(registry.NewMemoryStore(), WIFConfig{TrustedIssuers: []string{"issuer"}, Verifier: verifyFunc(func(context.Context, string) (map[string]any, error) {
		return map[string]any{"sub": "a", "iss": "evilissuer"}, nil
	})})
	if _, err := v.ExtractIdentity(r); err == nil {
		t.Fatal("untrusted issuer accepted")
	}
}

func TestWIFGroupsCanonicalIdentityAndRevocation(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	store.PutRoleBinding(ctx, &models.RoleBinding{ID: "research", Group: "research", Provider: "issuer", Role: models.RoleViewer, Scopes: []string{"llm:model:r"}})
	store.PutRoleBinding(ctx, &models.RoleBinding{ID: "engineering", Group: "engineering", Role: models.RoleDeployer, Scopes: []string{"llm:model:e", "llm:model:r"}})
	store.PutRoleBinding(ctx, &models.RoleBinding{ID: "other-provider", Group: "research", Provider: "elsewhere", Role: models.RoleAdmin, Scopes: []string{"llm:model:secret"}})
	v := NewWIFValidator(store, WIFConfig{WorkforcePoolID: "pool", IsWorkforce: true, Verifier: verifyFunc(func(context.Context, string) (map[string]any, error) {
		return map[string]any{"sub": "immutable", "iss": "issuer", "email": "mutable@example.com", "groups": []any{"research", "engineering", "research"}}, nil
	})})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer verified-by-adapter")
	identity, err := v.ExtractIdentity(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(identity.Groups, []string{"engineering", "research"}) {
		t.Fatalf("groups: %v", identity.Groups)
	}
	store.SetUserScopes(ctx, identity.PrincipalID, []string{"mcp:tools:manual"})
	access, err := v.SyncUserScopes(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(access.Scopes, []string{"llm:model:e", "llm:model:r", "mcp:tools:manual"}) {
		t.Fatalf("scopes: %v", access.Scopes)
	}
	if !reflect.DeepEqual(access.MatchedBindings, []string{"engineering", "research"}) {
		t.Fatalf("bindings: %v", access.MatchedBindings)
	}
	identity.Groups = nil
	identity.Email = "changed@example.com"
	after, err := v.SyncUserScopes(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.Scopes, []string{"mcp:tools:manual"}) {
		t.Fatalf("revoked group scopes persisted: %v", after.Scopes)
	}
	saved, _ := store.GetUserScopes(ctx, identity.PrincipalID)
	if !reflect.DeepEqual(saved, []string{"mcp:tools:manual"}) {
		t.Fatalf("derived grants leaked into manual store: %v", saved)
	}
	if CanonicalPrincipalID("other", "pool", "immutable") == identity.PrincipalID || CanonicalPrincipalID("issuer", "different-pool", "immutable") == identity.PrincipalID {
		t.Fatal("principal namespace collision")
	}
}

func signJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test"}`))
	body, _ := json.Marshal(claims)
	input := header + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}
func TestJWKSVerificationAndCache(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprintf(w, `{"keys":[{"kty":"RSA","kid":"test","n":"%s","e":"AQAB"}]}`, base64.RawURLEncoding.EncodeToString(key.N.Bytes()))
	}))
	defer server.Close()
	verifier, err := NewJWKSVerifier(JWKSConfig{Issuer: "issuer", Audience: "resource", JWKSURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		claims map[string]any
		valid  bool
	}{
		{"valid", map[string]any{"iss": "issuer", "aud": "resource", "sub": "a", "exp": time.Now().Add(time.Hour).Unix()}, true},
		{"audience list", map[string]any{"iss": "issuer", "aud": []string{"other", "resource"}, "sub": "a", "exp": time.Now().Add(time.Hour).Unix()}, true},
		{"wrong issuer", map[string]any{"iss": "evil", "aud": "resource", "sub": "a", "exp": time.Now().Add(time.Hour).Unix()}, false},
		{"wrong audience", map[string]any{"iss": "issuer", "aud": "other", "sub": "a", "exp": time.Now().Add(time.Hour).Unix()}, false},
		{"expired", map[string]any{"iss": "issuer", "aud": "resource", "sub": "a", "exp": time.Now().Add(-time.Minute).Unix()}, false},
		{"missing subject", map[string]any{"iss": "issuer", "aud": "resource", "exp": time.Now().Add(time.Hour).Unix()}, false},
		{"missing expiry", map[string]any{"iss": "issuer", "aud": "resource", "sub": "a"}, false},
		{"future nbf", map[string]any{"iss": "issuer", "aud": "resource", "sub": "a", "nbf": time.Now().Add(time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix()}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verifier.Verify(context.Background(), signJWT(t, key, tc.claims))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	if requests.Load() != 1 {
		t.Fatalf("JWKS cache made %d calls", requests.Load())
	}
	valid := signJWT(t, key, cases[0].claims)
	parts := strings.Split(valid, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"issuer","aud":"resource","sub":"admin","exp":9999999999}`))
	if _, err := verifier.Verify(context.Background(), strings.Join(parts, ".")); err == nil {
		t.Fatal("tampered token accepted")
	}
	if _, err := verifier.Verify(context.Background(), "eyJhbGciOiJub25lIn0.e30."); err == nil {
		t.Fatal("unsigned token accepted")
	}
}

func TestJWKSFailureRefreshBackoff(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	v, err := NewJWKSVerifier(JWKSConfig{Issuer: "issuer", Audience: "aud", JWKSURL: server.URL, HTTPClient: &http.Client{}})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := v.key(context.Background(), "missing"); err == nil {
			t.Fatal("unavailable key server accepted")
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("failed refresh hammered endpoint %d times", requests.Load())
	}
	if v.cfg.HTTPClient.Timeout == 0 {
		t.Fatal("unbounded HTTP timeout")
	}
}

func TestWIFPrincipalValidation(t *testing.T) {
	for _, p := range []string{"", "principal://iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/agents/subject/user", "principalSet://iam.googleapis.com/locations/global/workforcePools/users/group/research", "principalSet://iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/agents/attribute.role/research", "principalSet://iam.googleapis.com/locations/global/workforcePools/users/*"} {
		if err := ValidateWIFPrincipal(p); err != nil {
			t.Errorf("valid principal %q: %v", p, err)
		}
	}
	for _, p := range []string{"principal://iam.googleapis.com/workloadIdentityPools/x", "principal://iam.googleapis.com/projects/not-number/locations/global/workloadIdentityPools/agents/subject/user", "principal://iam.googleapis.com/locations/global/workforcePools/users/group/research", "principalSet://iam.googleapis.com/locations/global/workforcePools/users/group/", "principal://iam.googleapis.com/locations/global/workforcePools/users/subject/user?evil=1"} {
		if err := ValidateWIFPrincipal(p); err == nil {
			t.Errorf("malformed principal accepted: %s", p)
		}
	}
}

func TestMultipleIdentitySourcesAndVerifiedActor(t *testing.T) {
	sources := []IdentitySource{
		{Issuer: "workforce", Audience: "api", PoolID: "people", IsWorkforce: true, Verifier: verifyFunc(func(_ context.Context, token string) (map[string]any, error) {
			return map[string]any{"iss": "workforce", "aud": "api", "sub": "human", "azp": "agent-client", "act": map[string]any{"sub": "agent"}}, nil
		})},
		{Issuer: "workload", Audience: "api", PoolID: "machines", Verifier: verifyFunc(func(_ context.Context, token string) (map[string]any, error) {
			return map[string]any{"iss": "workload", "aud": "api", "sub": "machine"}, nil
		})},
	}
	v, err := NewWIFValidatorFromSources(registry.NewMemoryStore(), sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		payload, _ := json.Marshal(map[string]any{"iss": source.Issuer, "aud": "api"})
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer e30."+base64.RawURLEncoding.EncodeToString(payload)+".signature")
		identity, err := v.ExtractIdentity(req)
		if err != nil {
			t.Fatal(err)
		}
		if identity.PoolID != source.PoolID || identity.IsWorkforce != source.IsWorkforce {
			t.Fatalf("wrong source classification: %+v", identity)
		}
		if source.IsWorkforce && (identity.ActorID != CanonicalPrincipalID("workforce", "people", "agent") || identity.AuthorizedParty != "agent-client") {
			t.Fatalf("lost verified delegation: %+v", identity)
		}
	}
	sources[0].Verifier = verifyFunc(func(context.Context, string) (map[string]any, error) {
		return map[string]any{"iss": "wrong", "aud": "api", "sub": "human"}, nil
	})
	v, _ = NewWIFValidatorFromSources(registry.NewMemoryStore(), sources)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer e30."+base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"workforce","aud":"api"}`))+".signature")
	if _, err := v.ExtractIdentity(req); err == nil {
		t.Fatal("unverified dispatch claims accepted as identity")
	}
	sources = append(sources, sources[0])
	if _, err := NewWIFValidatorFromSources(registry.NewMemoryStore(), sources); err == nil {
		t.Fatal("ambiguous identity sources accepted")
	}
}

func TestJWKSVerifiesES256Assertion(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	coordinate := func(n []byte) string {
		buf := make([]byte, 32)
		copy(buf[32-len(n):], n)
		return base64.RawURLEncoding.EncodeToString(buf)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"keys":[{"kty":"EC","kid":"iap","crv":"P-256","x":"%s","y":"%s"}]}`, coordinate(key.X.Bytes()), coordinate(key.Y.Bytes()))
	}))
	defer server.Close()
	verifier, err := NewJWKSVerifier(JWKSConfig{Issuer: "https://cloud.google.com/iap", Audience: "/projects/123/global/backendServices/456", JWKSURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"iss": "https://cloud.google.com/iap", "aud": "/projects/123/global/backendServices/456", "sub": "user", "exp": time.Now().Add(time.Minute).Unix()})
	input := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","kid":"iap"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	token := input + "." + base64.RawURLEncoding.EncodeToString(signature)
	if _, err := verifier.Verify(context.Background(), token); err != nil {
		t.Fatalf("signed EC assertion rejected: %v", err)
	}
	signature[0] ^= 1
	if _, err := verifier.Verify(context.Background(), input+"."+base64.RawURLEncoding.EncodeToString(signature)); err == nil {
		t.Fatal("tampered EC signature accepted")
	}
}

func TestMultiSourceGroupBindingsRequireNamespace(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	store.PutRoleBinding(ctx, &models.RoleBinding{ID: "ambiguous", Group: "admin", Role: models.RoleAdmin, Scopes: []string{"llm:model:secret"}})
	store.PutRoleBinding(ctx, &models.RoleBinding{ID: "scoped", Group: "admin", Provider: "one", PoolID: "pool-one", Role: models.RoleViewer, Scopes: []string{"llm:model:public"}})
	verifier := verifyFunc(func(context.Context, string) (map[string]any, error) { return nil, nil })
	v, err := NewWIFValidatorFromSources(store, []IdentitySource{{Issuer: "one", PoolID: "pool-one", Verifier: verifier}, {Issuer: "two", PoolID: "pool-two", Verifier: verifier}})
	if err != nil {
		t.Fatal(err)
	}
	access, err := v.ResolveAccess(ctx, &models.WIFIdentity{Subject: "u", Provider: "two", PoolID: "pool-two", Groups: []string{"admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(access.Scopes) != 0 {
		t.Fatalf("same group label crossed identity domains: %v", access.Scopes)
	}
	access, err = v.ResolveAccess(ctx, &models.WIFIdentity{Subject: "u", Provider: "one", PoolID: "pool-one", Groups: []string{"admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(access.Scopes, []string{"llm:model:public"}) {
		t.Fatalf("scoped grant: %v", access.Scopes)
	}
}

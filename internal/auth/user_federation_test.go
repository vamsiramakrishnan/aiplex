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
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/agentfederation"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/userfederation"
)

const (
	userIssuer   = "https://idp.test"
	userAudience = "aiplex-users"
)

type userFixture struct {
	key       *rsa.PrivateKey
	providers *userfederation.Store
	bindings  *agentfederation.Store
	users     *UserFederation
	agents    *AgentFederation
}

func newUserFixture(t *testing.T) *userFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	t.Cleanup(server.Close)
	roots := server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	keys := NewJWKSKeyResolver(endpointpolicy.Config{AllowLoopback: true, TLSConfig: &tls.Config{RootCAs: roots}})
	f := &userFixture{key: key, providers: userfederation.NewStore(resources.NewMemoryStore()), bindings: agentfederation.NewStore(resources.NewMemoryStore())}
	userEngine := &userfederation.Engine{Keys: UserKeyResolver{Keys: keys}}
	f.users = &UserFederation{Tenant: "tenant-a", Providers: f.providers, Engine: userEngine}
	provider, err := userfederation.NewProvider("corp", userfederation.ProviderRequest{Preset: userfederation.PresetOIDC, Inputs: map[string]string{"issuer": userIssuer, "jwks_url": server.URL + "/jwks", "audience": userAudience, "groups_from": "claim", "group_claim": "groups"}, RequiredClaims: map[string]string{"org": "acme"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.providers.Put(context.Background(), "tenant-a", provider, 0); err != nil {
		t.Fatal(err)
	}
	agentEngine := &agentfederation.Engine{Keys: keys, Agents: func(_ context.Context, id string) (agentfederation.Agent, error) {
		return agentfederation.Agent{ClientID: id, Active: true, ActorSubject: "spiffe://aiplex.test/sa/" + id}, nil
	}}
	f.agents = &AgentFederation{Tenant: "tenant-a", Bindings: f.bindings, Engine: agentEngine}
	binding, err := agentfederation.NewBinding("tutor-agent", "svc", agentfederation.BindingRequest{Preset: agentfederation.PresetOIDC, Inputs: map[string]string{"issuer": userIssuer, "jwks_url": server.URL + "/jwks", "subject": "svc-1", "audience": userAudience}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.bindings.Put(context.Background(), "tenant-a", binding, 0); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *userFixture) token(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	claims := map[string]any{"iss": userIssuer, "sub": "user-42", "aud": userAudience, "org": "acme", "exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Add(-time.Minute).Unix(),
		"groups": []string{"students", "admins"},
		// Injection attempts: none of these may reach the AIPlex identity.
		"act": map[string]any{"sub": "spoofed-actor"}, "azp": "tutor-agent", "email": "spoof@example.com", "hd": "tenant-b.example", "roles": []string{"admin"}, "name": "Mallory"}
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

type staticStub struct{ claims map[string]any }

func (s staticStub) Verify(context.Context, string) (map[string]any, error) { return s.claims, nil }

func TestUserFederationAuthenticatesWorkforceUser(t *testing.T) {
	f := newUserFixture(t)
	v := NewWIFValidator(nil, WIFConfig{})
	if _, err := v.ExtractIdentity(bearer(f.token(t, nil))); err == nil {
		t.Fatal("user federation must be off until enabled")
	}
	v.WithUserFederation(f.users)
	identity, err := v.ExtractIdentity(bearer(f.token(t, nil)))
	if err != nil {
		t.Fatal(err)
	}
	pool := UserFederationPoolID("tenant-a")
	if identity.Provider != userIssuer || identity.PoolID != pool || identity.Subject != "user-42" || !identity.IsWorkforce {
		t.Fatalf("identity = %+v", identity)
	}
	if identity.PrincipalID != CanonicalPrincipalID(userIssuer, pool, "user-42") {
		t.Fatalf("principal = %s", identity.PrincipalID)
	}
	if strings.Join(identity.Groups, ",") != "admins,students" {
		t.Fatalf("groups = %v", identity.Groups)
	}
	if identity.ActorSubject != "" || identity.ActorID != "" || identity.AuthorizedParty != "" || identity.Email != "" || identity.Domain != "" || identity.DisplayName != "" {
		t.Fatalf("external claims leaked into identity: %+v", identity)
	}
	ev := identity.VerifiedEvidence
	if ev == nil || !ev.LifetimeValid || !ev.GroupsPresent || !ev.GroupsValid {
		t.Fatalf("evidence = %+v", ev)
	}
	snapshot, err := PrincipalMembership(identity, "tenant-a", time.Now())
	if err != nil || len(snapshot.Groups) != 2 {
		t.Fatalf("membership = %+v, %v", snapshot, err)
	}
	// The provider grants nothing by itself; access comes from role bindings.
	if access := v.resolveBindings(identity, nil); len(access.Roles) != 0 || len(access.Scopes) != 0 {
		t.Fatalf("provider granted access by itself: %+v", access)
	}
}

func TestUserFederationGroupsUnavailableAreAbsentNotEmpty(t *testing.T) {
	f := newUserFixture(t)
	v := NewWIFValidator(nil, WIFConfig{}).WithUserFederation(f.users)
	cases := map[string]func(map[string]any){
		"missing":   func(c map[string]any) { delete(c, "groups") },
		"null":      func(c map[string]any) { c["groups"] = nil },
		"malformed": func(c map[string]any) { c["groups"] = "students" },
		"overage": func(c map[string]any) {
			delete(c, "groups")
			c["_claim_names"] = map[string]any{"groups": "src1"}
			c["_claim_sources"] = map[string]any{"src1": map[string]any{"endpoint": "https://graph.windows.net/x"}}
		},
		"hasgroups": func(c map[string]any) { delete(c, "groups"); c["hasgroups"] = true },
	}
	for name, mutate := range cases {
		identity, err := v.ExtractIdentity(bearer(f.token(t, mutate)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if identity.Groups != nil || identity.VerifiedEvidence.GroupsPresent {
			t.Fatalf("%s: groups must be absent: %+v / %+v", name, identity.Groups, identity.VerifiedEvidence)
		}
		if _, err := PrincipalMembership(identity, "tenant-a", time.Now()); err == nil {
			t.Fatalf("%s: unavailable groups treated as a membership view", name)
		}
	}
	// An authoritative empty list is a real, empty view.
	identity, err := v.ExtractIdentity(bearer(f.token(t, func(c map[string]any) { c["groups"] = []string{} })))
	if err != nil || !identity.VerifiedEvidence.GroupsPresent || !identity.VerifiedEvidence.GroupsValid || len(identity.Groups) != 0 {
		t.Fatalf("empty list: %+v %v", identity, err)
	}
}

func TestUserFederationFailsClosed(t *testing.T) {
	f := newUserFixture(t)
	v := NewWIFValidator(nil, WIFConfig{}).WithUserFederation(f.users)
	cases := map[string]func(map[string]any){
		"wrong audience":      func(c map[string]any) { c["aud"] = "other" },
		"unconfigured issuer": func(c map[string]any) { c["iss"] = "https://other.test" },
		"expired":             func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() },
		"missing org pin":     func(c map[string]any) { delete(c, "org") },
		"wrong org pin":       func(c map[string]any) { c["org"] = "evil" },
		"cnf bound":           func(c map[string]any) { c["cnf"] = map[string]any{"x5t#S256": "x"} },
		"no subject":          func(c map[string]any) { delete(c, "sub") },
	}
	for name, mutate := range cases {
		token := f.token(t, mutate)
		_, err := v.ExtractIdentity(bearer(token))
		if err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), strings.Split(token, ".")[1]) {
			t.Fatalf("%s: error contains the token: %v", name, err)
		}
	}
	// A forged signature is rejected.
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	forger := &userFixture{key: other}
	if _, err := v.ExtractIdentity(bearer(forger.token(t, nil))); err == nil {
		t.Fatal("token signed by another key accepted")
	}
	// Providers of another tenant do not authenticate.
	other2 := NewWIFValidator(nil, WIFConfig{}).WithUserFederation(&UserFederation{Tenant: "tenant-b", Providers: f.providers, Engine: f.users.Engine})
	if _, err := other2.ExtractIdentity(bearer(f.token(t, nil))); err == nil {
		t.Fatal("tenant-b accepted a tenant-a provider")
	}
	// A disabled provider authenticates nobody.
	p, _ := f.providers.Get(context.Background(), "tenant-a", "corp")
	p.Disabled = true
	if _, err := f.providers.Put(context.Background(), "tenant-a", p, p.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := v.ExtractIdentity(bearer(f.token(t, nil))); err == nil {
		t.Fatal("disabled provider authenticated")
	}
}

func TestStaticSourcesKeepPrecedenceOverUserFederation(t *testing.T) {
	f := newUserFixture(t)
	static := IdentitySource{Issuer: userIssuer, Audience: userAudience, Verifier: staticStub{claims: map[string]any{"iss": userIssuer, "aud": userAudience, "sub": "static-user", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}}, PoolID: "static-pool"}
	v, err := NewWIFValidatorFromSources(nil, []IdentitySource{static})
	if err != nil {
		t.Fatal(err)
	}
	v.WithUserFederation(f.users)
	identity, err := v.ExtractIdentity(bearer(f.token(t, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if identity.PoolID != "static-pool" || identity.Subject != "static-user" || identity.IsWorkforce {
		t.Fatalf("static source must win: %+v", identity)
	}
	// A token from a different issuer still reaches the provider through the fallback.
	other := IdentitySource{Issuer: "https://static.example.com", Audience: "x", Verifier: staticStub{}, PoolID: "p"}
	v2, _ := NewWIFValidatorFromSources(nil, []IdentitySource{other})
	v2.WithUserFederation(f.users)
	if identity, err = v2.ExtractIdentity(bearer(f.token(t, nil))); err != nil || identity.PoolID != UserFederationPoolID("tenant-a") || !identity.IsWorkforce {
		t.Fatalf("fallback: %+v %v", identity, err)
	}
}

func TestAgentBindingAndUserProviderPrecedence(t *testing.T) {
	f := newUserFixture(t)
	chain := func() *WIFValidator {
		return NewWIFValidator(nil, WIFConfig{}).WithFederation(f.agents).WithUserFederation(f.users)
	}
	reverse := func() *WIFValidator {
		return NewWIFValidator(nil, WIFConfig{}).WithUserFederation(f.users).WithFederation(f.agents)
	}
	for name, v := range map[string]*WIFValidator{"agent then user": chain(), "user then agent": reverse()} {
		// Subject bound to an agent AND issuer+audience trusted by a user provider: ambiguous.
		agentTok := f.token(t, func(c map[string]any) { c["sub"] = "svc-1" })
		_, err := v.ExtractIdentity(bearer(agentTok))
		if err == nil || !errors.Is(err, ErrAmbiguousFederation) {
			t.Fatalf("%s: ambiguous token = %v", name, err)
		}
		if strings.Contains(err.Error(), agentTok) {
			t.Fatal("ambiguity error contains the token")
		}
		// Any other subject of the same issuer+audience is a user.
		identity, err := v.ExtractIdentity(bearer(f.token(t, nil)))
		if err != nil || !identity.IsWorkforce || identity.ActorSubject != "" || identity.PoolID != UserFederationPoolID("tenant-a") {
			t.Fatalf("%s: user = %+v %v", name, identity, err)
		}
	}
	// With the provider's audience not named by the token, only the agent binding can claim it.
	f2 := newUserFixture(t)
	p, _ := f2.providers.Get(context.Background(), "tenant-a", "corp")
	if err := f2.providers.Delete(context.Background(), "tenant-a", "corp", p.Revision); err != nil {
		t.Fatal(err)
	}
	v := NewWIFValidator(nil, WIFConfig{}).WithFederation(f2.agents).WithUserFederation(f2.users)
	identity, err := v.ExtractIdentity(bearer(f2.token(t, func(c map[string]any) { c["sub"] = "svc-1" })))
	if err != nil || identity.IsWorkforce || identity.AuthorizedParty != "tutor-agent" || identity.PoolID != FederationPoolID("tenant-a") || identity.ActorSubject == "" {
		t.Fatalf("agent still authenticates: %+v %v", identity, err)
	}
	// Agent federation alone keeps rejecting unknown subjects; groups never flow into agents.
	if _, err := v.ExtractIdentity(bearer(f2.token(t, nil))); err == nil || !strings.Contains(err.Error(), "no trusted identity source") {
		t.Fatalf("unclaimed token = %v", err)
	}
	if identity.Groups != nil && len(identity.Groups) != 0 {
		t.Fatalf("agent identity gained groups: %v", identity.Groups)
	}
}
